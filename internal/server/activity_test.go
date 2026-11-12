package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/LoweKeyFlexin/share-ct/internal/drills"
	"github.com/LoweKeyFlexin/share-ct/internal/leaderboard"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRecentActivityMixesOnlyPublicRunsAndKeepsEraseAndLimit(t *testing.T) {
	h, db := newApp(t)
	fixtureClock := time.Now()
	h = New(quiet(), db, func() time.Time { return fixtureClock })
	request := func(method, path string, body any, token string, want int) []byte {
		t.Helper()
		var raw []byte
		if body != nil {
			var err error
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		// Keep this behavior fixture below the unchanged request-rate limits.
		fixtureClock = fixtureClock.Add(time.Minute)
		if res.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, res.Code, res.Body.String())
		}
		if path == "/v1/activity" && res.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("activity must not cache erased players")
		}
		return res.Body.Bytes()
	}
	var player struct {
		ID    string `json:"player_id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(request("POST", "/v1/players", map[string]string{"display_name": "Alpha", "platform": "ios"}, "", 201), &player); err != nil {
		t.Fatal(err)
	}
	samples := []float64{200, 205, 210}
	points := leaderboard.ScoreTrial(samples, 0)
	reaction := leaderboard.SubmitRequest{ClientID: "fixture-reaction", Source: "touch", BestMs: points.BestMs, AvgMs: points.AvgMs, Accuracy: points.Accuracy, Score: points.Score, AttemptsMs: samples, AppBuild: "test", Platform: "ios", DeviceLabel: "Touch"}
	request("POST", "/v1/scores", reaction, player.Token, 201)
	base := time.Now().Add(-time.Minute).Unix()
	if _, err := db.DB().Exec(`UPDATE submissions SET created_at=?`, base); err != nil {
		t.Fatal(err)
	}
	ids := []string{"whiff-punish", "hit-confirm-ii", "mental-stack", "parry", "whiff-punish"}
	for i, id := range ids {
		score := 0
		run := drills.Request{ClientID: fmt.Sprintf("fixture-drill-%d", i), DrillID: id, Score: &score, Misses: 3, Source: "pad", AppBuild: "test", Platform: "android", DeviceLabel: "Pad"}
		if i >= 3 {
			run.Mode = "arcade"
			run.ArcadeStats = &drills.ArcadeStats{TotalOpponents: 14, ElapsedMS: 1000}
		}
		request("POST", "/v1/drills/runs", run, player.Token, 201)
		request("POST", "/v1/drills/runs", run, player.Token, 200)
		table := "drill_runs"
		if i >= 3 {
			table = "arcade_runs"
		}
		if _, err := db.DB().Exec(`UPDATE `+table+` SET created_at=? WHERE client_id=?`, base+int64(i+1), run.ClientID); err != nil {
			t.Fatal(err)
		}
	}
	// Best-only submissions must never be republished into this aggregate.
	score := 90
	best := drills.Request{ClientID: "fixture-best-only", DrillID: "anti-air", Score: &score, Misses: 3, Source: "keyboard", AppBuild: "test", Platform: "ios", DeviceLabel: "Keys"}
	request("POST", "/v1/drills/scores", best, player.Token, 201)
	feed := func(path string) ([]map[string]any, []byte) {
		t.Helper()
		raw := request("GET", path, nil, "", 200)
		var envelope struct {
			Entries []map[string]any `json:"entries"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Entries, raw
	}
	rows, raw := feed("/v1/activity")
	if len(rows) != 6 {
		t.Fatalf("want all six runs (no best-only or duplicate): %s", raw)
	}
	for i, row := range rows {
		for _, key := range []string{"player_id", "client_id", "token", "token_hash", "body_hash", "device_model"} {
			if _, ok := row[key]; ok {
				t.Fatalf("private field %s", key)
			}
		}
		if row["rank"] != float64(0) {
			t.Fatalf("feed must not claim rank: %+v", row)
		}
		if i < 5 && row["drill_id"] != ids[4-i] {
			t.Fatalf("wrong cross-board ordering: %s", raw)
		}
	}
	if rows[0]["mode"] != "arcade" || rows[1]["mode"] != "arcade" || rows[2]["mode"] != "endless" || rows[5]["activity_type"] != "reaction" {
		t.Fatalf("lost score types: %s", raw)
	}
	if strings.Contains(string(raw), player.ID) || strings.Contains(string(raw), player.Token) || strings.Contains(string(raw), "fixture-") {
		t.Fatal("private identity or retry IDs leaked")
	}
	// Expired published drill runs remain excluded even before scheduled cleanup.
	if _, err := db.DB().Exec(`UPDATE drill_runs SET created_at=? WHERE drill_id='mental-stack'`, time.Now().Add(-91*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	rows, _ = feed("/v1/activity")
	if len(rows) != 5 {
		t.Fatalf("expired drill row retained: %+v", rows)
	}
	rows, _ = feed("/v1/activity?limit=2")
	if len(rows) != 2 || rows[0]["drill_id"] != "whiff-punish" || rows[1]["drill_id"] != "parry" {
		t.Fatalf("global limit/order: %+v", rows)
	}
	for _, q := range []string{"0", "51", "-1", "wat"} {
		request("GET", "/v1/activity?limit="+q, nil, "", 400)
	}
	// Fill only the local store, avoiding test HTTP rate limits, to prove the final cap.
	clock := time.Now().Add(-30 * time.Second)
	rs := leaderboard.NewStore(db.DB(), func() time.Time { return clock })
	for i := 0; i < 55; i++ {
		sub := leaderboard.Submission{PlayerID: player.ID, ClientID: fmt.Sprint("fixture-cap-", i), Source: "touch", BestMs: 200, AvgMs: 200, Accuracy: 1, Score: 100, AttemptsMs: []float64{200, 200, 200}, AppBuild: "test", Platform: "ios", DeviceLabel: "Touch"}
		clock = clock.Add(time.Second)
		if _, err := rs.Insert(context.Background(), &sub); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ = feed("/v1/activity")
	if len(rows) != 50 {
		t.Fatalf("unbounded aggregate: %d", len(rows))
	}
	request("DELETE", "/v1/players/"+player.ID, nil, player.Token, 204)
	rows, _ = feed("/v1/activity")
	if len(rows) != 0 {
		t.Fatal("erased identity remains public")
	}
}
