package leaderboard

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/httpx"
	"github.com/LoweKeyFlexin/share-ct/internal/players"
	"github.com/LoweKeyFlexin/share-ct/internal/ratelimit"
)

// Limits on POST /v1/scores: per player and per CF-Connecting-IP, each a minute wide.
// A trial takes at least 12 s, so ten a minute is already faster than a human plays.
const (
	SubmitsPerPlayer = 10
	SubmitsPerIP     = 60
	submitWindow     = time.Minute

	// DefaultLimit and MaxLimit bound GET /v1/board?limit=.
	DefaultLimit = 50
	MaxLimit     = 100

	maxClientIDLen = 64
	maxAppBuildLen = 32
	maxDeviceLen   = 64

	// bestMsTolerance is how far the client's best_ms may sit from min(attempts_ms):
	// the same Double travelled through JSON, so only formatting noise is allowed.
	bestMsTolerance = 0.001
)

// Module is the leaderboard feature: POST /v1/scores and GET /v1/board.
type Module struct {
	log       *slog.Logger
	store     *Store
	auth      func(http.Handler) http.Handler
	perPlayer *ratelimit.Limiter
	perIP     *ratelimit.Limiter
}

// New wires the feature. auth is the players module's RequireBearer; now is
// injectable for tests.
func New(log *slog.Logger, store *Store, auth func(http.Handler) http.Handler, now func() time.Time) *Module {
	return &Module{
		log:       log,
		store:     store,
		auth:      auth,
		perPlayer: ratelimit.New(SubmitsPerPlayer, submitWindow, now),
		perIP:     ratelimit.New(SubmitsPerIP, submitWindow, now),
	}
}

// Register mounts the routes on mux.
func (m *Module) Register(mux *http.ServeMux) {
	mux.Handle("POST /v1/scores", m.auth(http.HandlerFunc(m.submit)))
	mux.HandleFunc("GET /v1/board", m.board)
}

// SubmitRequest is the POST /v1/scores body (design brief §D): exactly these twelve
// keys, the set the app's disclosure sheet promises its users and its contract test
// asserts. The body is decoded strictly, so an undeclared key is 422 unknown_field
// rather than a silent drop; a field added here is a change to that promise. best_ms,
// avg_ms, accuracy and score are what the app computed; the server recomputes all four
// from attempts_ms and misfires and ranks only its own numbers.
type SubmitRequest struct {
	ClientID    string    `json:"client_id"`
	Source      string    `json:"source"`
	BestMs      float64   `json:"best_ms"`
	AvgMs       float64   `json:"avg_ms"`
	Accuracy    float64   `json:"accuracy"`
	Score       int       `json:"score"`
	AttemptsMs  []float64 `json:"attempts_ms"`
	Misfires    int       `json:"misfires"`
	AppBuild    string    `json:"app_build"`
	Platform    string    `json:"platform"`
	DeviceModel string    `json:"device_model"`
	DeviceLabel string    `json:"device_label"`
}

// SubmitResponse is the POST /v1/scores reply: 201 on a new row, 200 on a repeat.
// rank and board_size are on the source's all-time board in its default (fastest)
// order, so they agree with what a bare GET /v1/board shows.
type SubmitResponse struct {
	SubmissionID int64 `json:"submission_id"`
	Rank         int   `json:"rank"`
	BoardSize    int   `json:"board_size"`
}

// Validate checks req against the app's own rules, trimming its strings in place, and
// returns the recomputed trial or the 422 reason:
//
//	client_id, source, platform, app_build, device  a field is missing, unknown or too long
//	attempts          not len(attempts_ms) + misfires == 3 with at least one hit
//	implausible       a sample outside [100, 3000] ms, or a best outside the 10-200 frame band
//	best_ms_mismatch  best_ms is not min(attempts_ms)
//	score_mismatch    score is not what scoreTrial gives for these attempts
func Validate(req *SubmitRequest) (TrialScore, string) {
	req.ClientID = strings.TrimSpace(req.ClientID)
	req.AppBuild = strings.TrimSpace(req.AppBuild)
	req.DeviceModel = strings.TrimSpace(req.DeviceModel)
	req.DeviceLabel = strings.TrimSpace(req.DeviceLabel)
	switch {
	case req.ClientID == "" || len(req.ClientID) > maxClientIDLen:
		return TrialScore{}, "client_id"
	case !ValidSource(req.Source):
		return TrialScore{}, "source"
	case !players.ValidPlatform(req.Platform):
		return TrialScore{}, "platform"
	case req.AppBuild == "" || len(req.AppBuild) > maxAppBuildLen:
		return TrialScore{}, "app_build"
	case len(req.DeviceModel) > maxDeviceLen || len(req.DeviceLabel) > maxDeviceLen:
		return TrialScore{}, "device"
	case req.Misfires < 0 || len(req.AttemptsMs) == 0 || len(req.AttemptsMs)+req.Misfires != TrialAttempts:
		return TrialScore{}, "attempts"
	}
	for _, a := range req.AttemptsMs {
		if math.IsNaN(a) || a < MinPlausibleReactionMs || a > MaxPlausibleReactionMs {
			return TrialScore{}, "implausible"
		}
	}
	ts := ScoreTrial(req.AttemptsMs, req.Misfires)
	if !IsValidHighScore(ts.BestMs) {
		return TrialScore{}, "implausible"
	}
	if math.Abs(req.BestMs-ts.BestMs) > bestMsTolerance {
		return TrialScore{}, "best_ms_mismatch"
	}
	if req.Score != ts.Score {
		return TrialScore{}, "score_mismatch"
	}
	return ts, ""
}

// submit is POST /v1/scores (bearer).
func (m *Module) submit(w http.ResponseWriter, r *http.Request) {
	p, ok := players.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if ok, retry := m.perPlayer.Allow("player:" + p.ID); !ok {
		httpx.WriteRateLimited(w, retry)
		return
	}
	if ok, retry := m.perIP.Allow("ip:" + httpx.ClientIP(r)); !ok {
		httpx.WriteRateLimited(w, retry)
		return
	}
	var req SubmitRequest
	if !httpx.DecodeJSONStrict(w, r, &req) {
		return
	}
	ts, reason := Validate(&req)
	if reason != "" {
		httpx.WriteInvalid(w, reason)
		return
	}
	sub := &Submission{
		PlayerID: p.ID, ClientID: req.ClientID, Source: req.Source,
		BestMs: ts.BestMs, AvgMs: ts.AvgMs, Accuracy: ts.Accuracy, Score: ts.Score,
		AttemptsMs: req.AttemptsMs, Misfires: req.Misfires,
		ClientBestMs: req.BestMs, ClientAvgMs: req.AvgMs, ClientAccuracy: req.Accuracy, ClientScore: req.Score,
		AppBuild: req.AppBuild, Platform: req.Platform, DeviceModel: req.DeviceModel, DeviceLabel: req.DeviceLabel,
	}
	created, err := m.store.Insert(r.Context(), sub)
	if err != nil {
		m.log.Error("insert submission", "player", p.ID, "error", err.Error())
		httpx.WriteError(w, http.StatusInternalServerError, "internal")
		return
	}
	rank, size, err := m.store.Rank(r.Context(), sub.Source, p.ID)
	if err != nil {
		m.log.Error("rank submission", "player", p.ID, "error", err.Error())
		httpx.WriteError(w, http.StatusInternalServerError, "internal")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, SubmitResponse{SubmissionID: sub.ID, Rank: rank, BoardSize: size})
}

// Board is the GET /v1/board response (design brief §D). Entries is always a JSON
// array, never null.
type Board struct {
	Source  string  `json:"source"`
	Window  string  `json:"window"`
	Entries []Entry `json:"entries"`
}

// board is GET /v1/board?source=touch|pad|keyboard&window=all|30d&sort=fastest|score&limit=50.
// Every parameter has a default and every unknown value is a 400 naming it; sort in
// particular never falls back, because a typo that quietly returned a different order
// would be indistinguishable from the board being wrong.
func (m *Module) board(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	source := q.Get("source")
	if source == "" {
		source = Sources[0]
	}
	// The READ side accepts the mixed board; the SUBMIT side still does not - a score
	// may never arrive without naming its input.
	if !ValidBoardSource(source) {
		httpx.WriteBadRequest(w, "source")
		return
	}
	window := q.Get("window")
	if window == "" {
		window = WindowAll
	}
	if !ValidWindow(window) {
		httpx.WriteBadRequest(w, "window")
		return
	}
	sort := q.Get("sort")
	if sort == "" {
		sort = SortFastest
	}
	if !ValidSort(sort) {
		httpx.WriteBadRequest(w, "sort")
		return
	}
	limit := DefaultLimit
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			httpx.WriteBadRequest(w, "limit")
			return
		}
		limit = min(n, MaxLimit)
	}
	entries, err := m.store.Board(r.Context(), source, window, sort, limit)
	if err != nil {
		m.log.Error("board", "source", source, "window", window, "sort", sort, "error", err.Error())
		httpx.WriteError(w, http.StatusInternalServerError, "internal")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	httpx.WriteJSON(w, http.StatusOK, Board{Source: source, Window: window, Entries: entries})
}
