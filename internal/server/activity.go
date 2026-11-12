package server

import (
	"context"
	"github.com/LoweKeyFlexin/share-ct/internal/drills"
	"github.com/LoweKeyFlexin/share-ct/internal/httpx"
	"github.com/LoweKeyFlexin/share-ct/internal/leaderboard"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"
)

type reactionActivity struct {
	leaderboard.Entry
	ActivityType string `json:"activity_type"`
}
type drillActivity struct {
	drills.ActivityEntry
	ActivityType string `json:"activity_type"`
}

// Website-only aggregate of already-public feed rows. Two bounded reads, no
// backfill, new stored data, publication scope, or app-side API changes.
func handleActivity(log *slog.Logger, reaction *leaderboard.Store, drill *drills.Module) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 50 {
				httpx.WriteError(w, http.StatusBadRequest, "limit")
				return
			}
			limit = n
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		reactions, err := reaction.Recent(ctx, "", limit)
		if err != nil {
			log.Error("activity read failed", "error", err.Error())
			httpx.WriteError(w, 500, "internal")
			return
		}
		runs, err := drill.RecentActivity(ctx, limit)
		if err != nil {
			log.Error("activity read failed", "error", err.Error())
			httpx.WriteError(w, 500, "internal")
			return
		}
		type row struct {
			stamp string
			value any
		}
		merged := make([]row, 0, len(reactions)+len(runs))
		for _, e := range reactions {
			e.Rank = 0
			merged = append(merged, row{e.CreatedAt, reactionActivity{e, "reaction"}})
		}
		for _, e := range runs {
			merged = append(merged, row{e.CreatedAt, drillActivity{e, "drill"}})
		}
		// Stable source ordering breaks same-second ties; a feed has no standing.
		sort.SliceStable(merged, func(i, j int) bool { return merged[i].stamp > merged[j].stamp })
		if len(merged) > limit {
			merged = merged[:limit]
		}
		entries := make([]any, 0, len(merged))
		for _, e := range merged {
			entries = append(entries, e.value)
		}
		httpx.WriteJSON(w, 200, struct {
			Entries []any `json:"entries"`
		}{entries})
	}
}
