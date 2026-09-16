package leaderboard

import (
	"context"
	"fmt"
	"testing"
)

func (hs *harness) recent(query string) ([]map[string]any, map[string]any) {
	hs.t.Helper()
	rec, out := hs.do("GET", "/v1/recent"+query, "", "", "")
	if rec.Code != 200 {
		hs.t.Fatalf("recent%s: %d %s", query, rec.Code, rec.Body.String())
	}
	raw := out["entries"].([]any)
	entries := make([]map[string]any, len(raw))
	for i, e := range raw {
		entries[i] = e.(map[string]any)
	}
	return entries, out
}

func (hs *harness) insertRecent(playerID, clientID, source string) {
	hs.t.Helper()
	_, err := hs.lb.Insert(context.Background(), &Submission{
		PlayerID: playerID, ClientID: clientID, Source: source,
		BestMs: 200, AvgMs: 205, Accuracy: 1, Score: 2100,
		AttemptsMs: []float64{200, 205, 210}, Misfires: 0,
		ClientBestMs: 200, ClientAvgMs: 205, ClientAccuracy: 1, ClientScore: 2100,
		AppBuild: "test", Platform: "ios", DeviceLabel: source,
	})
	if err != nil {
		hs.t.Fatal(err)
	}
}

func TestRecentSourceFiltersBeforeLimit(t *testing.T) {
	hs := newHarness(t)
	touchID, _ := hs.player()
	padID, _ := hs.player()

	// Pad is inserted last, so it occupies the entire mixed 50-row response. TOUCH must still
	// return 50: filtering a mixed response after LIMIT would return zero and recreate the app
	// bug this endpoint parameter exists to prevent.
	for i := 0; i < 55; i++ {
		hs.insertRecent(touchID, fmt.Sprintf("touch-%02d", i), "touch")
	}
	for i := 0; i < 55; i++ {
		hs.insertRecent(padID, fmt.Sprintf("pad-%02d", i), "pad")
	}

	mixed, envelope := hs.recent("?limit=50")
	if len(mixed) != 50 || mixed[0]["source"] != "pad" || mixed[49]["source"] != "pad" {
		t.Fatalf("mixed recent = %d rows, edge sources %v/%v; want newest 50 pad rows",
			len(mixed), mixed[0]["source"], mixed[49]["source"])
	}
	if envelope["source"] != "recent" {
		t.Fatalf("source envelope = %v, want recent for decoder compatibility", envelope["source"])
	}

	for _, source := range []string{"touch", "pad"} {
		entries, _ := hs.recent("?source=" + source + "&limit=50")
		if len(entries) != 50 {
			t.Fatalf("%s recent = %d rows, want 50", source, len(entries))
		}
		for i, entry := range entries {
			if entry["source"] != source {
				t.Fatalf("%s recent row %d source = %v", source, i, entry["source"])
			}
		}
	}
	keyboard, _ := hs.recent("?source=keyboard&limit=50")
	if len(keyboard) != 0 {
		t.Fatalf("keyboard recent = %v, want empty", keyboard)
	}
}

func TestRecentRejectsUnknownSourceAndBadLimit(t *testing.T) {
	hs := newHarness(t)
	for _, query := range []string{"?source=mouse", "?source=all", "?limit=abc", "?limit=0"} {
		rec, out := hs.do("GET", "/v1/recent"+query, "", "", "")
		if rec.Code != 400 || out["error"] != "bad_request" || out["reason"] == nil {
			t.Errorf("%s: got %d %s, want 400 bad_request with reason", query, rec.Code, rec.Body.String())
		}
	}
}
