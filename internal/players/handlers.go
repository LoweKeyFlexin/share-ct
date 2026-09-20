package players

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/httpx"
	"github.com/LoweKeyFlexin/share-ct/internal/namefilter"
	"github.com/LoweKeyFlexin/share-ct/internal/ratelimit"
)

// Registrations per CF-Connecting-IP per day (design brief §C).
const (
	RegistrationsPerIP = 10
	registrationWindow = 24 * time.Hour
)

// ScoreSummary is a player's best submission on one source, as the profile reports it.
type ScoreSummary struct {
	Score     int     `json:"score"`
	BestMs    float64 `json:"best_ms"`
	AvgMs     float64 `json:"avg_ms"`
	Accuracy  float64 `json:"accuracy"`
	Tier      string  `json:"tier"`
	Platform  string  `json:"platform"`
	CreatedAt int64   `json:"created_at"`
}

// Bests is a player's best per input source; a source with no submission is null.
type Bests struct {
	Touch    *ScoreSummary `json:"touch"`
	Pad      *ScoreSummary `json:"pad"`
	Keyboard *ScoreSummary `json:"keyboard"`
}

// Set stores s under its source name; an unknown source is ignored.
func (b *Bests) Set(source string, s *ScoreSummary) {
	switch source {
	case "touch":
		b.Touch = s
	case "pad":
		b.Pad = s
	case "keyboard":
		b.Keyboard = s
	}
}

// Scores is what the leaderboard lends the profile: a player's best per source and
// how many submissions it holds for them.
type Scores interface {
	Bests(ctx context.Context, playerID string) (Bests, int, error)
}

// Module is the players feature: its store, routes and the bearer middleware other
// features wrap their routes in.
type Module struct {
	log           *slog.Logger
	store         *Store
	scores        Scores
	registrations *ratelimit.Limiter
	rejectName    func(string) bool
}

// New wires the feature on the shared connection. now is injectable for tests.
func New(log *slog.Logger, db *sql.DB, now func() time.Time, scores Scores) *Module {
	return &Module{
		log:           log,
		store:         NewStore(db, now),
		scores:        scores,
		registrations: ratelimit.New(RegistrationsPerIP, registrationWindow, now),
		rejectName:    namefilter.Rejects,
	}
}

// Store exposes the players table to other features and tests.
func (m *Module) Store() *Store { return m.store }

// Register mounts the routes on mux.
func (m *Module) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/players", m.create)
	mux.HandleFunc("GET /v1/players/{id}", m.get)
	mux.Handle("PATCH /v1/players/{id}", m.RequireBearer(requireOwner(http.HandlerFunc(m.rename))))
	mux.Handle("DELETE /v1/players/{id}", m.requireExists(m.RequireBearer(requireOwner(http.HandlerFunc(m.erase)))))
}

type createRequest struct {
	DisplayName string `json:"display_name"`
	Platform    string `json:"platform"`
}

type createResponse struct {
	PlayerID string `json:"player_id"`
	Token    string `json:"token"`
}

// create is POST /v1/players: mint an id and a token for a display name.
func (m *Module) create(w http.ResponseWriter, r *http.Request) {
	if ok, retry := m.registrations.Allow(httpx.ClientIP(r)); !ok {
		httpx.WriteRateLimited(w, retry)
		return
	}
	var req createRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	// Optional, not required: a player who has not named themselves registers anyway and
	// is stored with an empty name, rendered AnonymousName wherever it is shown. Making
	// this required is what broke the app - it sent an empty name, got 422, discarded the
	// reason, and the player was left opted in with no account (controller-tester-fgc#6112).
	name, reason := validateOptionalDisplayName(req.DisplayName, m.rejectName)
	if reason != "" {
		httpx.WriteInvalid(w, reason)
		return
	}
	if !ValidPlatform(req.Platform) {
		httpx.WriteInvalid(w, "platform")
		return
	}
	p, token, err := m.store.Create(r.Context(), name, req.Platform)
	if err != nil {
		m.log.Error("create player", "error", err.Error())
		httpx.WriteError(w, http.StatusInternalServerError, "internal")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, createResponse{PlayerID: p.ID, Token: token})
}

type renameRequest struct {
	DisplayName string `json:"display_name"`
}

type playerResponse struct {
	PlayerID    string `json:"player_id"`
	PlayerShort string `json:"player_short"`
	DisplayName string `json:"display_name"`
	Platform    string `json:"platform"`
}

// rename is PATCH /v1/players/{id} (bearer, own id only).
func (m *Module) rename(w http.ResponseWriter, r *http.Request) {
	p, _ := FromContext(r.Context())
	var req renameRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	// Clearing the field is a way back to anonymous, not an error. Aaron, 2026-09-11:
	// "entering nothing on the keyboard for Entry should just default the player back to
	// NO NAME."
	name, reason := validateOptionalDisplayName(req.DisplayName, m.rejectName)
	if reason != "" {
		httpx.WriteInvalid(w, reason)
		return
	}
	if err := m.store.Rename(r.Context(), p.ID, name); err != nil {
		m.fail(w, "rename player", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, playerResponse{
		PlayerID: p.ID, PlayerShort: p.Short(), DisplayName: Display(name), Platform: p.Platform,
	})
}

// erase is DELETE /v1/players/{id} (bearer, own id only): the player and every row
// that references it are gone when this returns 204. The codes are load-bearing: the
// app runs the erase as a job it retries until it hears 204 or 404, and only then
// forgets the player id and token. A row that vanished between requireExists and here
// is a 404 through fail, never a 500.
func (m *Module) erase(w http.ResponseWriter, r *http.Request) {
	p, _ := FromContext(r.Context())
	if err := m.store.Delete(r.Context(), p.ID); err != nil {
		m.fail(w, "delete player", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireExists answers 404 when {id} names no player, before the bearer is checked.
// An erased player's token died with its row, so the app's retry of a delete whose 204
// was lost arrives unauthenticated; read as 401 it would be retried forever, with the
// credentials kept on the phone for a player that no longer exists. 404 says "already
// erased". A live player's id still needs its own token: 401 or 403 as before, never
// 404, so a wrong token cannot be mistaken for "nothing to delete". Nothing is revealed
// here that the public GET /v1/players/{id} does not already say.
func (m *Module) requireExists(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := m.store.Get(r.Context(), r.PathValue("id")); err != nil {
			m.fail(w, "look up player", err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Profile is GET /v1/players/{id}: public, the same facts the board shows.
type Profile struct {
	PlayerID    string `json:"player_id"`
	PlayerShort string `json:"player_short"`
	DisplayName string `json:"display_name"`
	Platform    string `json:"platform"`
	Best        Bests  `json:"best"`
	Submissions int    `json:"submissions"`
	CreatedAt   int64  `json:"created_at"`
}

func (m *Module) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := m.store.Get(r.Context(), id)
	if err != nil {
		m.fail(w, "get player", err)
		return
	}
	best, n, err := m.scores.Bests(r.Context(), id)
	if err != nil {
		m.fail(w, "player bests", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, Profile{
		PlayerID: p.ID, PlayerShort: p.Short(), DisplayName: Display(p.DisplayName), Platform: p.Platform,
		Best: best, Submissions: n, CreatedAt: p.CreatedAt,
	})
}

// fail maps a store error to 404 or a logged 500.
func (m *Module) fail(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	m.log.Error(what, "error", err.Error())
	httpx.WriteError(w, http.StatusInternalServerError, "internal")
}
