package leaderboard

import (
	"context"
	"testing"
	"time"
)

func TestAuthenticatedBoardCertifiesExistingLeaderWithoutAnotherSubmission(t *testing.T) {
	for _, source := range Sources {
		t.Run(source, func(t *testing.T) {
			hs := newHarness(t)
			leaderID, leader := hs.player()
			_, other := hs.player()
			hs.submit(leader, trial("existing-leader", source, []float64{200, 205, 210}, 0))
			hs.submit(other, trial("runner-up", source, []float64{260, 265, 270}, 0))
			// Simulate scores predating the achievement release.
			if _, err := hs.db.DB().Exec(`UPDATE submissions SET reaction_crown_earned = 0`); err != nil {
				t.Fatal(err)
			}
			rec, public := hs.do("GET", "/v1/board?source=all", "", "", "")
			if rec.Code != 200 || public["reaction_crown"] != nil {
				t.Fatalf("public must have no private receipt: %d %v", rec.Code, public)
			}
			rec, noAward := hs.do("GET", "/v1/board?source=all", "", other, "")
			if rec.Code != 200 || noAward["reaction_crown"] != nil {
				t.Fatalf("other account: %d %v", rec.Code, noAward)
			}
			rec, own := hs.do("GET", "/v1/board?source=keyboard&window=all&sort=score&limit=1", "", leader, "")
			receipt, ok := own["reaction_crown"].(map[string]any)
			if rec.Code != 200 || !ok || receipt["client_id"] != "existing-leader" || receipt["submission_id"].(float64) <= 0 {
				t.Fatalf("existing monthly leader: %d %v", rec.Code, own)
			}
			if rec.Header().Get("Cache-Control") != "private, no-store" || rec.Header().Get("Vary") != "Authorization" {
				t.Fatalf("private reply cache headers: %v", rec.Header())
			}
			var count int
			if err := hs.db.DB().QueryRow(`SELECT count(*) FROM submissions`).Scan(&count); err != nil || count != 2 {
				t.Fatalf("board read must not fabricate a run: count=%d err=%v", count, err)
			}
			hs.now = hs.now.Add(time.Second)
			hs.submit(other, trial("overtake", source, []float64{180, 185, 190}, 0))
			hs.now = hs.now.Add(31 * 24 * time.Hour)
			_, restored := hs.do("GET", "/v1/board", "", leader, "")
			if restored["reaction_crown"] == nil {
				t.Fatal("earned receipt must survive rank/window loss")
			}
			// Player erasure includes its retained achievement, without a new table.
			hs.do("DELETE", "/v1/players/"+leaderID, "", leader, "")
			var remaining int
			if err := hs.db.DB().QueryRowContext(context.Background(), `SELECT count(*) FROM submissions WHERE player_id = ?`, leaderID).Scan(&remaining); err != nil || remaining != 0 {
				t.Fatalf("erasure: %d %v", remaining, err)
			}
		})
	}
}

func TestAuthenticatedBoardDoesNotCertifyExpiredOrUnqualifiedLeader(t *testing.T) {
	hs := newHarness(t)
	_, token := hs.player()
	hs.submit(token, trial("expired", "pad", []float64{200, 205, 210}, 0))
	if _, err := hs.db.DB().Exec(`UPDATE submissions SET reaction_crown_earned = 0`); err != nil {
		t.Fatal(err)
	}
	hs.now = hs.now.Add(31 * 24 * time.Hour)
	rec, out := hs.do("GET", "/v1/board", "", token, "")
	if rec.Code != 200 || out["reaction_crown"] != nil {
		t.Fatalf("expired all-time leader is not monthly leader: %d %v", rec.Code, out)
	}
	if rec, _ := hs.do("GET", "/v1/board", "", "not-a-token", ""); rec.Code != 401 {
		t.Fatalf("invalid authenticated request: %d", rec.Code)
	}
	if rec, out := hs.do("GET", "/v1/board", "", "", ""); rec.Code != 200 || out["reaction_crown"] != nil {
		t.Fatalf("anonymous public board remains available: %d %v", rec.Code, out)
	}
}
