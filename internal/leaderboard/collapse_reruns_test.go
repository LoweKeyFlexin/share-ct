package leaderboard

import (
	"context"
	"testing"
)

// Migration 006 collapses a run submitted more than once under DIFFERENT client_ids —
// the case `UNIQUE (player_id, client_id)` cannot catch. Observed live 2026-09-20: one
// player's run stored nine times, another's twice, every copy identical on all twelve
// public fields, each with its own id.
//
// Aaron's rule, verbatim: "only remove duplicates if every single time is identical in
// the run." So the test that matters is not that duplicates go — it is that anything
// differing by ONE field STAYS. A DELETE that is too eager destroys real runs and the
// board has no undo.
const collapseStmt = `DELETE FROM submissions WHERE id NOT IN (
	SELECT MIN(id) FROM submissions
	GROUP BY player_id, source, attempts_ms, misfires,
	         best_ms, avg_ms, accuracy, score,
	         client_best_ms, client_avg_ms, client_accuracy, client_score)`

func insertRun(t *testing.T, hs *harness, playerID, clientID, source string,
	attempts []float64, score, misfires int) {
	t.Helper()
	best := attempts[0]
	for _, a := range attempts {
		if a < best {
			best = a
		}
	}
	if _, err := hs.lb.Insert(context.Background(), &Submission{
		PlayerID: playerID, ClientID: clientID, Source: source,
		BestMs: best, AvgMs: 205, Accuracy: 1, Score: score,
		AttemptsMs: attempts, Misfires: misfires,
		ClientBestMs: best, ClientAvgMs: 205, ClientAccuracy: 1, ClientScore: score,
		AppBuild: "test", Platform: "ios", DeviceLabel: source,
	}); err != nil {
		t.Fatal(err)
	}
}

func subCount(t *testing.T, hs *harness) int {
	t.Helper()
	var n int
	if err := hs.lb.db.QueryRow(`SELECT COUNT(*) FROM submissions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func collapse(t *testing.T, hs *harness) {
	t.Helper()
	if _, err := hs.lb.db.Exec(collapseStmt); err != nil {
		t.Fatal(err)
	}
}

func TestCollapseKeepsTheEarliestOfIdenticalRuns(t *testing.T) {
	hs := newHarness(t)
	p, _ := hs.player()
	for _, cid := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		insertRun(t, hs, p, cid, "touch", []float64{188, 195, 205}, 2148, 0)
	}
	if got := subCount(t, hs); got != 9 {
		t.Fatalf("setup: want 9, got %d", got)
	}
	collapse(t, hs)
	if got := subCount(t, hs); got != 1 {
		t.Fatalf("want 1 row after collapse, got %d", got)
	}
	var kept string
	if err := hs.lb.db.QueryRow(`SELECT client_id FROM submissions`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != "a" {
		t.Fatalf("the EARLIEST submission must survive; kept %q", kept)
	}
}

// The half that protects real data: each row differs from the base by exactly one field.
func TestCollapseSparesRunsDifferingByOneField(t *testing.T) {
	hs := newHarness(t)
	p, _ := hs.player()
	other, _ := hs.player()
	base := []float64{188, 195, 205}

	insertRun(t, hs, p, "base", "touch", base, 2148, 0)
	insertRun(t, hs, p, "reordered", "touch", []float64{195, 188, 205}, 2148, 0)
	insertRun(t, hs, p, "onems", "touch", []float64{188, 195, 206}, 2148, 0)
	insertRun(t, hs, p, "source", "pad", base, 2148, 0)
	insertRun(t, hs, p, "misfires", "touch", base, 2148, 1)
	insertRun(t, hs, p, "score", "touch", base, 2149, 0)
	insertRun(t, hs, other, "base", "touch", base, 2148, 0)

	before := subCount(t, hs)
	if before != 7 {
		t.Fatalf("setup: want 7, got %d", before)
	}
	collapse(t, hs)
	if got := subCount(t, hs); got != before {
		t.Fatalf("collapse deleted a run differing by one field: %d -> %d", before, got)
	}
}

func TestCollapseIsIdempotent(t *testing.T) {
	hs := newHarness(t)
	p, _ := hs.player()
	for _, cid := range []string{"x", "y", "z"} {
		insertRun(t, hs, p, cid, "touch", []float64{188, 205, 240}, 1348, 0)
	}
	collapse(t, hs)
	first := subCount(t, hs)
	collapse(t, hs)
	if got := subCount(t, hs); got != first || first != 1 {
		t.Fatalf("want a stable 1 row, got %d then %d", first, got)
	}
}
