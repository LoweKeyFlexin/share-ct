package drills

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Aaron, 2026-10-20: "Add 30D to drill boards". window=30d ranks each player's best score set
// in the last 30 days, from published runs and from bests set in the window; the default
// response is unchanged.
func TestThirtyDayWindowRanksBestsSetInTheWindow(t *testing.T) {
	m, pl, h := harness(t)
	ctx := context.Background()
	now := time.Unix(1800000000, 0)
	day := int64(24 * time.Hour / time.Second)
	alpha, alphaToken, _ := pl.Create(ctx, "Alpha", "ios")
	beta, betaToken, _ := pl.Create(ctx, "Beta", "ios")
	post := func(path, token string, r Request) {
		t.Helper()
		data, _ := json.Marshal(r)
		if w := call(h, "POST", path, string(data), token); w.Code != 201 {
			t.Fatalf("%s %d %s", path, w.Code, w.Body.String())
		}
	}
	// Alpha: an all-time best of 90 set 40 days ago, and a published run of 50 ten days ago.
	post("/v1/drills/runs", alphaToken, req("a-old", "anti-air", 90))
	post("/v1/drills/runs", alphaToken, req("a-new", "anti-air", 50))
	// Beta: a best of 70 five days ago, sent without the Recent feed.
	post("/v1/drills/scores", betaToken, req("b", "anti-air", 70))
	set := func(sql string, args ...any) {
		t.Helper()
		if _, err := m.db.ExecContext(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	old, recent := now.Unix()-40*day, now.Unix()-10*day
	set(`UPDATE drill_runs SET created_at=? WHERE score=90`, old)
	set(`UPDATE drill_runs SET created_at=? WHERE score=50`, recent)
	set(`UPDATE drill_bests SET created_at=? WHERE player_id=?`, old, alpha.ID)
	set(`UPDATE drill_bests SET created_at=? WHERE player_id=?`, now.Unix()-5*day, beta.ID)

	all, err := m.readSince(ctx, "anti-air", alpha.ID, 50, 0)
	if err != nil || len(all.Entries) != 2 || all.Entries[0].Score != 90 || all.Entries[1].Score != 70 {
		t.Fatalf("all-time %+v %v", all, err)
	}
	w := call(h, "GET", "/v1/drills/board?drill_id=anti-air&window=30d", "", alphaToken)
	var board Board
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &board) != nil {
		t.Fatalf("30d %d %s", w.Code, w.Body.String())
	}
	if board.Window != "30d" || board.BoardSize != 2 || len(board.Entries) != 2 ||
		board.Entries[0].Score != 70 || board.Entries[1].Score != 50 {
		t.Fatalf("30d board %+v", board)
	}
	if board.Own == nil || board.Own.Rank != 2 || board.Own.Score != 50 {
		t.Fatalf("own standing in the window %+v", board.Own)
	}
	// Recent in the window: the 40-day-old run is gone, the 10-day-old one stays.
	w = call(h, "GET", "/v1/drills/board?drill_id=anti-air&sort=recent&window=30d", "", alphaToken)
	var feed Board
	_ = json.Unmarshal(w.Body.Bytes(), &feed)
	if w.Code != 200 || feed.Window != "30d" || len(feed.Entries) != 1 || feed.Entries[0].Score != 50 {
		t.Fatalf("30d recent %d %+v", w.Code, feed)
	}
	// The default board keeps its old shape (no window key).
	w = call(h, "GET", "/v1/drills/board?drill_id=anti-air", "", alphaToken)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if _, ok := body["window"]; ok {
		t.Fatal("changed default board contract")
	}
	if w = call(h, "GET", "/v1/drills/board?drill_id=anti-air&window=7d", "", alphaToken); w.Code != 400 {
		t.Fatalf("unknown window %d", w.Code)
	}
}
