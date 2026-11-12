package drills

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// Original ladders field seven cast, six newcomers and one boss when the
// player's fighter is outside that cast. Removing a mirror can shorten them;
// all shipped sizes must remain valid without changing points or mode identity.
func TestOriginalArcadeLadderSizes(t *testing.T) {
	for _, id := range []string{"whiff-punish", "hit-confirm", "anti-air", "crosscut", "drive-impact"} {
		t.Run(id, func(t *testing.T) {
			for _, total := range []int{12, 13, 14} {
				for _, defeated := range []int{0, 6, total} {
					r := arcadeReq("valid", defeated, total, 90000, 0, defeated*3)
					r.DrillID = id
					if got := validate(r); got != "" {
						t.Fatalf("total=%d defeated=%d rejected: %s", total, defeated, got)
					}
				}
			}
			for _, total := range []int{0, 11, 15, 99} {
				r := arcadeReq("invalid-size", 0, total, 90000, 0, 0)
				r.DrillID = id
				if got := validate(r); got != "total_opponents" {
					t.Fatalf("total=%d: %s", total, got)
				}
			}
			r := arcadeReq("invalid-points", 14, 14, 90000, 0, 42)
			r.DrillID = id
			wrong := *r.Score + 1
			r.Score = &wrong
			if got := validate(r); got != "score" {
				t.Fatalf("fourteen must not bypass points validation: %s", got)
			}
		})
	}
}

// Both existing submission routes accept the same fourteen-opponent facts and
// retain them on the Arcade board. This harness is local; nothing is sent live.
func TestFourteenOpponentOriginalRunsThroughExistingHTTPRoutes(t *testing.T) {
	_, players, h := harness(t)
	for i, id := range []string{"whiff-punish", "hit-confirm", "anti-air", "crosscut", "drive-impact"} {
		t.Run(id, func(t *testing.T) {
			var token string
			for routeIndex, route := range []string{"/v1/drills/scores", "/v1/drills/runs"} {
				// Separate route fixtures stay below the real per-player rate limit.
				var err error
				_, token, err = players.Create(context.Background(), fmt.Sprintf("Player%c%d", 'A'+i, routeIndex), "ios")
				if err != nil {
					t.Fatal(err)
				}
				for _, defeated := range []int{0, 6, 14} {
					r := arcadeReq(fmt.Sprintf("r%d-%d", routeIndex, defeated), defeated, 14, 90000, 0, defeated*3)
					r.DrillID = id
					postArcade(t, h, token, route, r, 201)
					postArcade(t, h, token, route, r, 200)
				}
			}
			w := call(h, "GET", "/v1/drills/board?drill_id="+id+"&mode=arcade", "", token)
			if w.Code != 200 {
				t.Fatalf("board: %d %s", w.Code, w.Body.String())
			}
			var board Board
			if err := json.Unmarshal(w.Body.Bytes(), &board); err != nil {
				t.Fatal(err)
			}
			if board.Own == nil || board.Own.TotalOpponents != 14 || !board.Own.Completed {
				t.Fatalf("fourteen-opponent clear was not retained: %+v", board.Own)
			}
		})
	}
}
