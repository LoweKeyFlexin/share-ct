// Package server wires the HTTP mux, its middleware and the static page.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/httpx"
)

// Pinger reports whether the database answers; /healthz turns its error into a 503.
type Pinger func(ctx context.Context) error

const healthTimeout = 2 * time.Second

// New returns the root handler: the routes wrapped in recovery, headers and request logging.
func New(log *slog.Logger, ping Pinger, board http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", handleIndex)
	mux.HandleFunc("GET /healthz", handleHealthz(log, ping))
	mux.Handle("GET /v1/board", board)
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
