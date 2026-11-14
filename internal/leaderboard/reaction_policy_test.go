package leaderboard

import (
	"net/http"
	"testing"
)

func TestBackedSixFramePolicyThroughSubmissionAndSQL(t *testing.T) {
	cases := []struct {
		name     string
		attempts []float64
		ranked   bool
	}{
		{"six-frame inclusive", []float64{6 * FrameMs, 6*FrameMs + 2.5*FrameMs, 12 * FrameMs}, true},
		{"witnessed run", []float64{12.1 * FrameMs, 8.8 * FrameMs, 11.1 * FrameMs}, true},
		{"just outside backing", []float64{6 * FrameMs, 6*FrameMs + 2.5*FrameMs + 0.001, 12 * FrameMs}, false},
		{"unbacked fast run", []float64{6 * FrameMs, 12 * FrameMs, 13 * FrameMs}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Qualifies(c.attempts); got != c.ranked {
				t.Fatalf("Qualifies=%v", got)
			}
			hs := newHarness(t)
			_, token := hs.player()
			rec, out := hs.do("POST", "/v1/scores", enc(trial("policy", "touch", c.attempts, 0)), token, "")
			if rec.Code != http.StatusCreated {
				t.Fatalf("POST %d: %v", rec.Code, out)
			}
			board := hs.board("?source=touch&window=all")
			if (len(board) == 1) != c.ranked {
				t.Fatalf("ranked SQL returned %d entries; want ranked=%v", len(board), c.ranked)
			}
			if c.ranked && board[0]["best_ms"] != ScoreTrial(c.attempts, 0).BestMs {
				t.Fatal("ranking changed away from fastest")
			}
		})
	}
}

func TestBelowSixFramesStillFailsIndividualPressGuard(t *testing.T) {
	hs := newHarness(t)
	_, token := hs.player()
	rec, out := hs.do("POST", "/v1/scores", enc(trial("below", "touch", []float64{99.999, 110, 120}, 0)), token, "")
	if rec.Code != http.StatusUnprocessableEntity || out["reason"] != "implausible" {
		t.Fatalf("%d: %v", rec.Code, out)
	}
}
