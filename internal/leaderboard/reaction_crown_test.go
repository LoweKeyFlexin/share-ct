package leaderboard

import (
	"testing"
	"time"
)

func TestReactionCrownUsesMonthlyBoardRatherThanAllTimeHeldRank(t *testing.T) {
	hs := newHarness(t)
	_, old := hs.player()
	_, fresh := hs.player()
	_, slow := hs.player()
	if rec, out := hs.submit(old, trial("old", "pad", []float64{190, 195, 200}, 0)); rec.Code != 201 || out["reaction_crown_earned"] != true {
		t.Fatalf("initial leader: %d %v", rec.Code, out)
	}
	hs.now = hs.now.Add(31 * 24 * time.Hour)
	if rec, out := hs.submit(fresh, trial("monthly", "pad", []float64{210, 215, 220}, 0)); rec.Code != 201 || out["rank"] != float64(2) || out["reaction_crown_earned"] != true {
		t.Fatalf("monthly leader can be all-time #2: %d %v", rec.Code, out)
	}
	if rec, out := hs.submit(slow, trial("not-first", "pad", []float64{260, 265, 270}, 0)); rec.Code != 201 || out["reaction_crown_earned"] != false {
		t.Fatalf("monthly runner-up: %d %v", rec.Code, out)
	}
}

func TestReactionCrownPersistsOnIdempotentRetryAfterLosingLeadAndWindow(t *testing.T) {
	hs := newHarness(t)
	_, first := hs.player()
	_, next := hs.player()
	body := trial("earned", "touch", []float64{220, 225, 230}, 0)
	rec, earned := hs.submit(first, body)
	if rec.Code != 201 || earned["reaction_crown_earned"] != true {
		t.Fatalf("earn: %d %v", rec.Code, earned)
	}
	hs.now = hs.now.Add(time.Second)
	hs.submit(next, trial("faster", "touch", []float64{200, 205, 210}, 0))
	if rec, restored := hs.submit(first, trial("restore-after-loss", "touch", []float64{240, 245, 250}, 0)); rec.Code != 201 || restored["rank"] != float64(2) || restored["reaction_crown_earned"] != true {
		t.Fatalf("later accepted run restores past achievement: %d %v", rec.Code, restored)
	}
	hs.now = hs.now.Add(31 * 24 * time.Hour)
	rec, retry := hs.submit(first, body)
	if rec.Code != 200 || retry["submission_id"] != earned["submission_id"] || retry["reaction_crown_earned"] != true {
		t.Fatalf("lost response must retain verified achievement: %d %v", rec.Code, retry)
	}
}

func TestReactionCrownIsSourceSpecificAndCannotBeClaimedByRequest(t *testing.T) {
	for _, source := range Sources {
		t.Run(source, func(t *testing.T) {
			hs := newHarness(t)
			_, leader := hs.player()
			_, second := hs.player()
			rec, out := hs.submit(leader, trial("first", source, []float64{200, 205, 210}, 0))
			if rec.Code != 201 || out["reaction_crown_earned"] != true {
				t.Fatalf("source leader: %d %v", rec.Code, out)
			}
			rec, out = hs.submit(second, trial("second", source, []float64{250, 255, 260}, 0))
			if rec.Code != 201 || out["reaction_crown_earned"] != false {
				t.Fatalf("source nonleader: %d %v", rec.Code, out)
			}
			forged := trial("forged", source, []float64{250, 255, 260}, 0)
			forged["reaction_crown_earned"] = true
			if rec, _ := hs.submit(second, forged); rec.Code != 422 {
				t.Fatalf("derived receipt cannot be submitted: %d", rec.Code)
			}
		})
	}
}
