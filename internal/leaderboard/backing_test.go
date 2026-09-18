package leaderboard

import (
	"testing"
	"time"
)

// Aaron's row, verbatim from the live touch board on 2026-09-18: rank 1, a 10.05f best
// standing on a trial whose other two attempts were 17.56f and 17.75f. Every gate the
// app and this server already had let it through, which is why the rule exists.
var anticipation = []float64{292.6468749938067, 295.8010000002105, 167.5449583271984}

// KAGE's row from the same board, and the reason the rule is not simply a floor: a
// genuinely fast player is fast on EVERY attempt, so the gap between their best and
// their next-best is small. This run must keep ranking.
var backedUp = []float64{188, 195, 205}

func TestQualifies(t *testing.T) {
	cases := []struct {
		name     string
		attempts []float64
		want     bool
		why      string
	}{
		{"the anticipation that took #1", anticipation, false,
			"10.05f with a 17.56f second-fastest is a 7.5 frame gap: nobody's reaction improves that much inside one trial"},
		{"a genuinely fast trial", backedUp, true,
			"11.28f backed by 11.70f, well inside two frames"},
		{"slow runs are never policed", []float64{400, 900, 1200}, true,
			"at or slower than the threshold there is no requirement: nobody anticipates their way to a slow time"},
		{"exactly at the threshold", []float64{BackingThresholdFrames * FrameMs, 9999}, true,
			"the threshold is inclusive, so a best AT 13.5f needs no backing"},
		{"a hair under the threshold, unbacked", []float64{BackingThresholdFrames*FrameMs - 0.001, 9999}, false,
			"one millisecond faster and the rule applies"},
		{"exactly two frames apart", []float64{200, 200 + BackingWithinFrames*FrameMs}, true,
			"the window is inclusive: a second attempt exactly two frames back still backs it up"},
		{"a hair over two frames", []float64{200, 200 + BackingWithinFrames*FrameMs + 0.001}, false,
			"just outside and it no longer counts"},
		{"identical attempts", []float64{200, 200}, true,
			"two attempts at the same time are a perfectly backed-up pair, which a strict > would have thrown away"},
		{"one landed attempt, fast", []float64{180}, false,
			"a lone fast attempt has nothing behind it, however fast it was"},
		{"one landed attempt, slow", []float64{400}, true,
			"but a lone SLOW attempt is not what this rule is for"},
		{"no attempts at all", nil, false,
			"a trial with nothing landed cannot qualify"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Qualifies(c.attempts); got != c.want {
				t.Errorf("Qualifies(%v) = %v, want %v: %s", c.attempts, got, c.want, c.why)
			}
		})
	}
}

// The rule reaches EXISTING ROWS at read time, and the cutoff is what decides which.
//
// This is the test that would fail without the change, in both directions: the same
// trial, submitted on either side of BackingSinceUnix, ranks or does not. A version
// asserting only the dethroning would pass equally well against a server that had
// simply dropped every fast row, which is not what was ruled.
func TestBackingRuleAppliesOnlyAfterTheCutoff(t *testing.T) {
	before := time.Unix(BackingSinceUnix-1, 0).UTC()
	after := time.Unix(BackingSinceUnix+1, 0).UTC()

	t.Run("grandfathered before the cutoff", func(t *testing.T) {
		hs := newHarness(t)
		_, tok := hs.named("OLDTIMER")
		hs.submitAt(tok, on("Pad", trial("a", "pad", anticipation, 0)), before)
		if got := hs.board("?source=pad&window=all"); len(got) != 1 {
			t.Fatalf("got %d entries, want 1: a run submitted before the cutoff keeps its place whatever its shape", len(got))
		}
	})

	t.Run("dethroned on or after the cutoff", func(t *testing.T) {
		hs := newHarness(t)
		_, tok := hs.named("LUCKY")
		hs.submitAt(tok, on("Pad", trial("a", "pad", anticipation, 0)), after)
		if got := hs.board("?source=pad&window=all"); len(got) != 0 {
			t.Fatalf("got %d entries, want 0: an unbacked fast best submitted after the cutoff does not rank", len(got))
		}
	})

	t.Run("a backed-up run after the cutoff still ranks", func(t *testing.T) {
		hs := newHarness(t)
		_, tok := hs.named("KAGE")
		hs.submitAt(tok, on("Pad", trial("a", "pad", backedUp, 0)), after)
		if got := hs.board("?source=pad&window=all"); len(got) != 1 {
			t.Fatalf("got %d entries, want 1: the rule must not cost an honest fast player their place", len(got))
		}
	})
}

// A dethroned run is not a deleted run, and a player is not deleted with it: their next
// best QUALIFYING trial becomes their board row. The filter sits inside bestPerPlayer,
// before the row numbering, precisely so the player survives their own bad row.
func TestAPlayerKeepsTheirBestQualifyingRun(t *testing.T) {
	hs := newHarness(t)
	_, tok := hs.named("BOTH")
	after := time.Unix(BackingSinceUnix+1, 0).UTC()
	hs.submitAt(tok, on("Pad", trial("lucky", "pad", anticipation, 0)), after)
	hs.submitAt(tok, on("Pad", trial("honest", "pad", backedUp, 0)), after.Add(time.Second))

	got := hs.board("?source=pad&window=all&sort=fastest")
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1: one row per player", len(got))
	}
	// 188 ms is the backed-up trial's best; 167.5 was the anticipation's.
	if best := got[0]["best_ms"].(float64); best < 187 || best > 189 {
		t.Errorf("best_ms = %v, want the BACKED-UP trial's 188: the player keeps their place with the run that qualifies, not the one that does not", best)
	}
}
