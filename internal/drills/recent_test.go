package drills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestRecentFeedNeedsExplicitIntentAndRetainsLowerZeroAndRepeatedPlayerRuns(t *testing.T) {
	m, pl, h := harness(t)
	ctx := context.Background()
	p, token, _ := pl.Create(ctx, "Alpha", "ios")
	post := func(path string, r Request, want int) {
		t.Helper()
		data, _ := json.Marshal(r)
		w := call(h, "POST", path, string(data), token)
		if w.Code != want {
			t.Fatalf("%s got%d want%d %s", path, w.Code, want, w.Body.String())
		}
	}
	legacy := req("legacy", "anti-air", 90)
	post("/v1/drills/scores", legacy, 201)
	board, err := m.readRecent(ctx, "anti-air", p.ID, 50)
	if err != nil || len(board.Entries) != 0 {
		t.Fatalf("legacy publication %+v %v", board, err)
	}
	// A retry cannot upgrade the disclosure under which an existing run was saved.
	post("/v1/drills/runs", legacy, 422)
	for i, points := range []int{7, 2, 0} {
		r := req(fmt.Sprint("feed", i), "anti-air", points)
		r.Source = []string{"pad", "keyboard", "touch"}[i]
		post("/v1/drills/runs", r, 201)
	}
	post("/v1/drills/runs", req("feed2", "anti-air", 0), 422) // changed source conflicts
	zero := req("feed2", "anti-air", 0)
	zero.Source = "touch"
	post("/v1/drills/runs", zero, 200)
	post("/v1/drills/scores", zero, 422) // reverse cross-route retry also conflicts
	board, err = m.readRecent(ctx, "anti-air", p.ID, 50)
	if err != nil || board.Sort != "recent" || len(board.Entries) != 3 {
		t.Fatalf("feed %+v %v", board, err)
	}
	refs := map[string]bool{}
	for i, points := range []int{0, 2, 7} {
		e := board.Entries[i]
		if e.Score != points || e.Rank != 0 || e.RunRef == "" || refs[e.RunRef] || e.PlayerRef != p.Ref {
			t.Fatalf("entry %+v", e)
		}
		refs[e.RunRef] = true
	}
	if board.Entries[0].Source != "touch" || board.Entries[1].Source != "keyboard" {
		t.Fatal("input metadata changed")
	}
	if board.Own == nil || board.Own.Score != 90 || board.Own.Rank != 1 {
		t.Fatalf("own all-time standing %+v", board.Own)
	}
	top, err := m.read(ctx, "anti-air", p.ID, 50)
	if err != nil || len(top.Entries) != 1 || top.Entries[0].Score != 90 {
		t.Fatalf("top %+v %v", top, err)
	}
	// The default response remains byte-for-byte the old top shape; only the explicit
	// Recent view advertises its capability marker and opaque per-run reference.
	raw, _ := json.Marshal(top)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if _, ok := body["sort"]; ok {
		t.Fatal("changed default board contract")
	}
}

func TestRecentNewestSelectionPrecedesLimitAcrossPlayersAndDrills(t *testing.T) {
	m, pl, _ := harness(t)
	ctx := context.Background()
	base := m.now()
	clock := base
	m.now = func() time.Time { return clock }
	high, _, _ := pl.Create(ctx, "High", "ios")
	low, _, _ := pl.Create(ctx, "Low", "android")
	_, _, _ = m.saveWithIntent(ctx, high.ID, req("high", "anti-air", 100), true)
	clock = base.Add(time.Second)
	r := req("new-low", "anti-air", 1)
	r.Platform = "android"
	_, _, _ = m.saveWithIntent(ctx, low.ID, r, true)
	clock = base.Add(2 * time.Second)
	_, _, _ = m.saveWithIntent(ctx, high.ID, req("other", "crosscut", 999), true)
	b, err := m.readRecent(ctx, "anti-air", low.ID, 1)
	if err != nil || len(b.Entries) != 1 || b.Entries[0].Score != 1 || b.Entries[0].Platform != "android" {
		t.Fatalf("recent %+v %v", b, err)
	}
	top, err := m.read(ctx, "anti-air", low.ID, 1)
	if err != nil || top.Entries[0].Score != 100 || top.Own == nil || top.Own.Rank != 2 {
		t.Fatalf("top %+v %v", top, err)
	}
}

func TestFeedCountAgeBoundsAndRetryProofAfterReceiptRingRollsOver(t *testing.T) {
	m, pl, _ := harness(t)
	ctx := context.Background()
	p, _, _ := pl.Create(ctx, "Alpha", "ios")
	base := m.now()
	clock := base
	m.now = func() time.Time { return clock }
	for i := 0; i < 250; i++ {
		clock = base.Add(time.Duration(i) * time.Second)
		drill := "anti-air"
		if i%2 == 1 {
			drill = "crosscut"
		}
		if _, _, err := m.saveWithIntent(ctx, p.ID, req(fmt.Sprint(i), drill, 250-i), true); err != nil {
			t.Fatal(err)
		}
	}
	var count, receipts int
	_ = m.db.QueryRow(`SELECT count(*) FROM drill_runs WHERE player_id=?`, p.ID).Scan(&count)
	_ = m.db.QueryRow(`SELECT count(*) FROM drill_receipts WHERE player_id=?`, p.ID).Scan(&receipts)
	if count != RunCap || receipts != 100 {
		t.Fatal(count, receipts)
	}
	// Run60 remains in the 200-row feed after its retry receipt left the 100-row ring.
	original, err := m.readRecent(ctx, "anti-air", p.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, repeated, err := m.saveWithIntent(ctx, p.ID, req("60", "anti-air", 190), true); err != nil || !repeated {
		t.Fatal(repeated, err)
	}
	after, _ := m.readRecent(ctx, "anti-air", p.ID, 100)
	a, _ := json.Marshal(original)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("retry changed feed")
	}
	if _, _, err := m.save(ctx, p.ID, req("60", "anti-air", 190)); err != errConflict {
		t.Fatal("cross-route dedupe proof lost", err)
	}
	clock = base.Add(RunRetention + time.Hour)
	expired, err := m.readRecent(ctx, "anti-air", p.ID, 50)
	if err != nil || len(expired.Entries) != 0 {
		t.Fatal(expired, err)
	}
	_ = m.db.QueryRow(`SELECT count(*) FROM drill_runs`).Scan(&count)
	if count != 0 {
		t.Fatal("expired rows retained", count)
	}
	top, err := m.read(ctx, "anti-air", p.ID, 50)
	if err != nil || top.Own == nil || top.Own.Score != 250 {
		t.Fatal("expiry changed all-time best", top, err)
	}
}

func TestFeedInsertAndBestReceiptAreOneTransaction(t *testing.T) {
	m, pl, _ := harness(t)
	ctx := context.Background()
	p, _, _ := pl.Create(ctx, "Alpha", "ios")
	_, err := m.db.Exec(`CREATE TRIGGER reject_feed BEFORE INSERT ON drill_runs BEGIN SELECT RAISE(ABORT,'test insert failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = m.saveWithIntent(ctx, p.ID, req("fail", "anti-air", 20), true); err == nil {
		t.Fatal("expected failure")
	}
	for _, table := range []string{"drill_runs", "drill_bests", "drill_receipts"} {
		var count int
		_ = m.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count)
		if count != 0 {
			t.Fatal("partial transaction", table, count)
		}
	}
}

func TestRunsHTTPValidationAndRecentCapability(t *testing.T) {
	_, pl, h := harness(t)
	_, token, _ := pl.Create(context.Background(), "Alpha", "ios")
	data, _ := json.Marshal(req("run", "anti-air", 3))
	if w := call(h, "POST", "/v1/drills/runs", string(data), ""); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
	bad := req("bad", "anti-air", 3)
	bad.Misses = 2
	raw, _ := json.Marshal(bad)
	if w := call(h, "POST", "/v1/drills/runs", string(raw), token); w.Code != 422 {
		t.Fatal(w.Code)
	}
	if w := call(h, "POST", "/v1/drills/runs", string(data[:len(data)-1])+`,"publish_run":true}`, token); w.Code != 422 {
		t.Fatal(w.Code)
	}
	if w := call(h, "POST", "/v1/drills/runs", string(data), token); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(h, "GET", "/v1/drills/board?drill_id=anti-air&sort=recent", "", "bad-token"); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(h, "GET", "/v1/drills/board?drill_id=anti-air&sort=newest", "", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	w := call(h, "GET", "/v1/drills/board?drill_id=anti-air&sort=recent&limit=999", "", token)
	var board Board
	err := json.Unmarshal(w.Body.Bytes(), &board)
	if w.Code != 200 || err != nil || board.Sort != "recent" || len(board.Entries) != 1 || board.Entries[0].RunRef == "" {
		t.Fatal(w.Code, board, err)
	}
}

func TestRetainedLegacyBestCannotBePublishedAfterReceiptExpiry(t *testing.T) {
	m, pl, _ := harness(t)
	ctx := context.Background()
	p, _, _ := pl.Create(ctx, "Alpha", "ios")
	_, _, _ = m.save(ctx, p.ID, req("legacy-best", "anti-air", 1000))
	for i := 0; i < 105; i++ {
		if _, _, err := m.save(ctx, p.ID, req(fmt.Sprint("later", i), "anti-air", 1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := m.saveWithIntent(ctx, p.ID, req("legacy-best", "anti-air", 1000), true); err != errConflict {
		t.Fatal(err)
	}
	feed, err := m.readRecent(ctx, "anti-air", p.ID, 50)
	if err != nil || len(feed.Entries) != 0 {
		t.Fatal(feed, err)
	}
}

func TestRecentHTTPMaximumAndOpaqueRunReferences(t *testing.T) {
	m, pl, h := harness(t)
	ctx := context.Background()
	p, token, _ := pl.Create(ctx, "Alpha", "ios")
	q, _, _ := pl.Create(ctx, "Beta", "ios")
	for i := 0; i < 65; i++ {
		if _, _, err := m.saveWithIntent(ctx, p.ID, req(fmt.Sprint(i), "anti-air", i), true); err != nil {
			t.Fatal(err)
		}
	}
	w := call(h, "GET", "/v1/drills/board?drill_id=anti-air&sort=recent&limit=999", "", token)
	var feed Board
	if err := json.Unmarshal(w.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(feed.Entries) != 50 || feed.Entries[0].Score != 64 || feed.Entries[49].Score != 15 {
		t.Fatalf("limit/newest %+v", feed)
	}
	if publicRunRef(p.Ref, "same-client") == publicRunRef(q.Ref, "same-client") {
		t.Fatal("cross-player run identity collision")
	}
	if publicRunRef(p.Ref, "same-client") != publicRunRef(p.Ref, "same-client") {
		t.Fatal("unstable run identity")
	}
}
