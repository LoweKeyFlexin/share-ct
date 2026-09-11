// Package server wires the HTTP mux, its middleware, the static page and every
// feature's routes.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/httpx"
	"github.com/LoweKeyFlexin/share-ct/internal/leaderboard"
	"github.com/LoweKeyFlexin/share-ct/internal/players"
	"github.com/LoweKeyFlexin/share-ct/internal/store"
)

// Pinger reports whether the database answers; /healthz turns its error into a 503.
type Pinger func(ctx context.Context) error

const healthTimeout = 2 * time.Second

// New returns the root handler: /, /healthz, the players and leaderboard routes and
// the JSON 404, wrapped in recovery, headers and request logging. now is injectable
// so tests can drive the clock behind created_at, the 30d window and the rate limits.
func New(log *slog.Logger, db *store.Store, now func() time.Time) http.Handler {
	if now == nil {
		now = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", handleIndex)
	mux.HandleFunc("GET /healthz", handleHealthz(log, db.Ping))

	scores := leaderboard.NewStore(db.DB(), now)
	pl := players.New(log, db.DB(), now, scores)
	pl.Register(mux)
	leaderboard.New(log, scores, pl.RequireBearer, now).Register(mux)

	mux.HandleFunc("/", handleNotFound)
	return recoverer(log, secureHeaders(requestLogger(log, mux)))
}

// handleHealthz answers 200 "ok" once SELECT 1 succeeds, 503 otherwise.
func handleHealthz(log *slog.Logger, ping Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
		defer cancel()
		w.Header().Set("Cache-Control", "no-store")
		if err := ping(ctx); err != nil {
			log.Error("healthz: database unavailable", "error", err.Error())
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	}
}

func handleNotFound(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteError(w, http.StatusNotFound, "not_found")
}
