// Package leaderboard owns the Reaction board: GET /v1/board now, POST /v1/scores in M1.
package leaderboard

import (
	"net/http"

	"github.com/LoweKeyFlexin/share-ct/internal/httpx"
)

// Board is the GET /v1/board response shape from the design brief (§C).
// Entries is always a JSON array, never null, so the app decodes an empty board
// with no special case.
type Board struct {
	Source  string `json:"source"`
	Window  string `json:"window"`
	Entries []any  `json:"entries"` // M1 replaces any with the row type
}

// Handler serves the placeholder board: touch / all / empty regardless of the query,
// so the app side has a stable target before M1 lands the real query.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, Board{Source: "touch", Window: "all", Entries: []any{}})
	})
}
