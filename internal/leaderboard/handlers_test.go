package leaderboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/players"
	"github.com/LoweKeyFlexin/share-ct/internal/store"
	"github.com/LoweKeyFlexin/share-ct/migrations"
)

type harness struct {
	t     *testing.T
	h     http.Handler
	db    *store.Store
	pl    *players.Module
	lb    *Store
	now   time.Time
	names int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	hs := &harness{t: t, db: db, now: time.Unix(1_800_000_000, 0)}
	clock := func() time.Time { return hs.now }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hs.lb = NewStore(db.DB(), clock)
	hs.pl = players.New(log, db.DB(), clock, hs.lb)
	mux := http.NewServeMux()
	hs.pl.Register(mux)
	New(log, hs.lb, hs.pl.RequireBearer, clock).Register(mux)
	hs.h = mux
	return hs
}

// player creates a player straight in the store (no registration limit) and returns
// its id and token.
func (hs *harness) player() (id, token string) {
	hs.t.Helper()
	hs.names++
	p, token, err := hs.pl.Store().Create(context.Background(), fmt.Sprintf("Player %d", hs.names), "ios")
	if err != nil {
		hs.t.Fatal(err)
	}
	return p.ID, token
}

func (hs *harness) do(method, path, body, token, ip string) (*httptest.ResponseRecorder, map[string]any) {
	hs.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, rd)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if ip != "" {
		r.Header.Set("CF-Connecting-IP", ip)
	}
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, r)
	var out map[string]any
	if rec.Body.Len() > 0 && strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			hs.t.Fatalf("%s %s: bad JSON %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec, out
}

// trial is a POST /v1/scores body whose client numbers are the app's own recompute,
// so the request is honest unless the caller edits it.
func trial(clientID, source string, attempts []float64, misfires int) map[string]any {
	ts := ScoreTrial(attempts, misfires)
	return map[string]any{
		"client_id": clientID, "source": source,
		"best_ms": ts.BestMs, "avg_ms": ts.AvgMs, "accuracy": ts.Accuracy, "score": ts.Score,
		"attempts_ms": attempts, "misfires": misfires,
		"app_build": "2214", "platform": "ios", "device_model": "iPhone17,2", "device_label": "Touch",
	}
}

func enc(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (hs *harness) submit(token string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	hs.t.Helper()
	return hs.do("POST", "/v1/scores", enc(body), token, "")
}

func (hs *harness) board(query string) []map[string]any {
	hs.t.Helper()
	rec, out := hs.do("GET", "/v1/board"+query, "", "", "")
	if rec.Code != 200 {
		hs.t.Fatalf("board%s: %d %s", query, rec.Code, rec.Body.String())
	}
	raw := out["entries"].([]any)
	entries := make([]map[string]any, len(raw))
	for i, e := range raw {
		entries[i] = e.(map[string]any)
	}
	return entries
}

func TestSubmitRecomputesAndRanks(t *testing.T) {
	hs := newHarness(t)
	id, token := hs.player()

	// The client claims a nonsense average and accuracy; only the score and best are
	// checked, and the ranked columns are the server's recompute regardless.
	body := trial("c1", "touch", []float64{200, 205, 210}, 0)
	body["avg_ms"], body["accuracy"] = 999.0, 0.5
	rec, out := hs.submit(token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if out["submission_id"].(float64) < 1 || out["rank"] != float64(1) || out["board_size"] != float64(1) {
		t.Errorf("response %s", rec.Body.String())
	}

	var score int
	var best, avg, acc, cAvg, cAcc float64
	var attempts string
	row := hs.db.DB().QueryRow(`SELECT score, best_ms, avg_ms, accuracy, client_avg_ms, client_accuracy, attempts_ms
	                            FROM submissions WHERE player_id = ?`, id)
	if err := row.Scan(&score, &best, &avg, &acc, &cAvg, &cAcc, &attempts); err != nil {
		t.Fatal(err)
	}
	if score != 2100 || best != 200 || avg != 205 || acc != 1 {
		t.Errorf("ranked columns %d %v %v %v, want the recompute 2100 200 205 1", score, best, avg, acc)
	}
	if cAvg != 999 || cAcc != 0.5 || attempts != "[200,205,210]" {
		t.Errorf("audit columns %v %v %q", cAvg, cAcc, attempts)
	}

	entries := hs.board("?source=touch")
	if len(entries) != 1 || entries[0]["avg_ms"] != float64(205) || entries[0]["accuracy"] != float64(1) ||
		entries[0]["score"] != float64(2100) || entries[0]["tier"] != "DIAMOND" || entries[0]["player_short"] != players.Short(id) ||
		entries[0]["display_name"] != "Player 1" || entries[0]["platform"] != "ios" || entries[0]["rank"] != float64(1) {
		t.Errorf("board shows %v", entries)
	}
	var seen int64
	if err := hs.db.DB().QueryRow(`SELECT last_seen_at FROM players WHERE id = ?`, id).Scan(&seen); err != nil || seen != hs.now.Unix() {
		t.Errorf("last_seen_at %d (err %v), want %d", seen, err, hs.now.Unix())
	}
}

func TestSubmitRejects(t *testing.T) {
	hs := newHarness(t)
	_, token := hs.player()
	ok := func() map[string]any { return trial("c", "touch", []float64{200, 205, 210}, 0) }
	cases := []struct {
		name   string
		edit   func(b map[string]any)
		reason string
	}{
		{"two hits no misfire", func(b map[string]any) { b["attempts_ms"] = []float64{200, 205} }, "attempts"},
		{"four hits", func(b map[string]any) { b["attempts_ms"] = []float64{200, 205, 210, 215} }, "attempts"},
		{"three hits plus a misfire", func(b map[string]any) { b["misfires"] = 1 }, "attempts"},
		{"negative misfires", func(b map[string]any) { b["misfires"] = -1 }, "attempts"},
		{"no hits", func(b map[string]any) { b["attempts_ms"] = []float64{}; b["misfires"] = 3 }, "attempts"},
		{"missing attempts", func(b map[string]any) { delete(b, "attempts_ms") }, "attempts"},
		{"sample under 100", func(b map[string]any) { b["attempts_ms"] = []float64{99, 205, 210} }, "implausible"},
		{"sample over 3000", func(b map[string]any) { b["attempts_ms"] = []float64{200, 205, 3001} }, "implausible"},
		{"best under the 10f floor", func(b map[string]any) {
			b["attempts_ms"] = []float64{150, 205, 210}
			ts := ScoreTrial([]float64{150, 205, 210}, 0)
			b["best_ms"], b["score"] = ts.BestMs, ts.Score
		}, "implausible"},
		{"score off by one", func(b map[string]any) { b["score"] = 2099 }, "score_mismatch"},
		{"score inflated", func(b map[string]any) { b["score"] = 2500 }, "score_mismatch"},
		{"best not the minimum", func(b map[string]any) { b["best_ms"] = 205 }, "best_ms_mismatch"},
		{"no client id", func(b map[string]any) { b["client_id"] = "  " }, "client_id"},
		{"unknown source", func(b map[string]any) { b["source"] = "mouse" }, "source"},
		{"unknown platform", func(b map[string]any) { b["platform"] = "web" }, "platform"},
		{"no app build", func(b map[string]any) { b["app_build"] = "" }, "app_build"},
		{"device label too long", func(b map[string]any) { b["device_label"] = strings.Repeat("x", 65) }, "device"},
	}
	for _, c := range cases {
		hs.now = hs.now.Add(time.Minute) // rejections count against the per-player limit too
		b := ok()
		c.edit(b)
		rec, out := hs.submit(token, b)
		if rec.Code != http.StatusUnprocessableEntity || out["error"] != "invalid" || out["reason"] != c.reason {
			t.Errorf("%s: got %d %s, want 422 reason %q", c.name, rec.Code, rec.Body.String(), c.reason)
		}
	}
	hs.now = hs.now.Add(time.Minute)
	if rec, out := hs.do("POST", "/v1/scores", `{"client_id":`, token, ""); rec.Code != 400 || out["reason"] != "json" {
		t.Errorf("bad json: %d %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := hs.db.DB().QueryRow(`SELECT count(*) FROM submissions`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d rows stored from rejected bodies", n)
	}
}

func TestSubmitAuth(t *testing.T) {
	hs := newHarness(t)
	body := enc(trial("c", "touch", []float64{200, 205, 210}, 0))
	if rec, out := hs.do("POST", "/v1/scores", body, "", ""); rec.Code != 401 || out["error"] != "unauthorized" {
		t.Errorf("no token: %d %s", rec.Code, rec.Body.String())
	}
	if rec, _ := hs.do("POST", "/v1/scores", body, strings.Repeat("B", 43), ""); rec.Code != 401 {
		t.Errorf("unknown token: %d", rec.Code)
	}
}

func TestSubmitIdempotent(t *testing.T) {
	hs := newHarness(t)
	_, token := hs.player()
	first := trial("same", "touch", []float64{200, 205, 210}, 0)
	rec, out := hs.submit(token, first)
	if rec.Code != 201 {
		t.Fatalf("first: %d %s", rec.Code, rec.Body.String())
	}
	id := out["submission_id"]

	rec, out = hs.submit(token, first)
	if rec.Code != 200 || out["submission_id"] != id {
		t.Errorf("repeat: got %d %s, want 200 with submission %v", rec.Code, rec.Body.String(), id)
	}
	better := trial("same", "touch", []float64{170, 175, 180}, 0)
	rec, out = hs.submit(token, better)
	if rec.Code != 200 || out["submission_id"] != id {
		t.Errorf("repeat with a new body: got %d %s, want the original row", rec.Code, rec.Body.String())
	}
	var n int
	if err := hs.db.DB().QueryRow(`SELECT count(*) FROM submissions`).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
	if entries := hs.board("?source=touch"); len(entries) != 1 || entries[0]["score"] != float64(2100) {
		t.Errorf("board %v, want the original 2100", entries)
	}
}

func TestSubmitRateLimitPerPlayer(t *testing.T) {
	hs := newHarness(t)
	_, token := hs.player()
	for i := 0; i < SubmitsPerPlayer; i++ {
		if rec, _ := hs.submit(token, trial(fmt.Sprint("c", i), "touch", []float64{200, 205, 210}, 0)); rec.Code != 201 {
			t.Fatalf("submit %d: %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec, out := hs.submit(token, trial("c-over", "touch", []float64{200, 205, 210}, 0))
	if rec.Code != 429 || out["error"] != "rate_limited" || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("11th in a minute: got %d %s Retry-After %q", rec.Code, rec.Body.String(), rec.Header().Get("Retry-After"))
	}
	hs.now = hs.now.Add(time.Minute)
	if rec, _ := hs.submit(token, trial("c-over", "touch", []float64{200, 205, 210}, 0)); rec.Code != 201 {
		t.Errorf("a minute later: %d", rec.Code)
	}
}

func TestSubmitRateLimitPerIP(t *testing.T) {
	hs := newHarness(t)
	sent := 0
	for p := 0; p < SubmitsPerIP/SubmitsPerPlayer; p++ {
		_, token := hs.player()
		for i := 0; i < SubmitsPerPlayer; i++ {
			body := enc(trial(fmt.Sprint("c", i), "pad", []float64{200, 205, 210}, 0))
			if rec, _ := hs.do("POST", "/v1/scores", body, token, "198.51.100.4"); rec.Code != 201 {
				t.Fatalf("request %d from one IP: %d %s", sent+1, rec.Code, rec.Body.String())
			}
			sent++
		}
	}
	_, token := hs.player()
	body := enc(trial("c0", "pad", []float64{200, 205, 210}, 0))
	if rec, _ := hs.do("POST", "/v1/scores", body, token, "198.51.100.4"); rec.Code != 429 {
		t.Errorf("request %d from one IP: got %d, want 429", sent+1, rec.Code)
	}
	if rec, _ := hs.do("POST", "/v1/scores", body, token, "198.51.100.5"); rec.Code != 201 {
		t.Errorf("same player, another IP: got %d, want 201", rec.Code)
	}
}

func TestBoardOrderingOneRowPerPlayer(t *testing.T) {
	hs := newHarness(t)
	a, ta := hs.player()
	b, tb := hs.player()
	c, tc := hs.player()
	d, td := hs.player()
	step := func(token string, body map[string]any) {
		t.Helper()
		hs.now = hs.now.Add(time.Second)
		if rec, _ := hs.submit(token, body); rec.Code != 201 {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	}
	step(tb, trial("1", "touch", []float64{200, 205, 215}, 0)) // 2100, best 200, first
	step(ta, trial("1", "touch", []float64{200, 205, 210}, 0)) // 2100, best 200, later
	step(tc, trial("1", "touch", []float64{199, 205, 210}, 0)) // 2104
	step(td, trial("1", "touch", []float64{300, 380, 450}, 0)) // 350
	step(td, trial("2", "touch", []float64{200, 206, 220}, 0)) // 1350: D's best, the only D row shown
	step(ta, trial("2", "touch", []float64{300, 380, 450}, 0)) // 350: does not displace A's 2100
	step(ta, trial("3", "pad", []float64{170, 175, 180}, 0))   // another board entirely

	entries := hs.board("?source=touch")
	want := []struct {
		id    string
		score float64
		tier  string
	}{{c, 2104, "MASTER"}, {b, 2100, "DIAMOND"}, {a, 2100, "DIAMOND"}, {d, 1350, "DIAMOND"}}
	if len(entries) != len(want) {
		t.Fatalf("%d entries, want %d: %v", len(entries), len(want), entries)
	}
	for i, w := range want {
		e := entries[i]
		if e["rank"] != float64(i+1) || e["player_short"] != players.Short(w.id) || e["score"] != w.score || e["tier"] != w.tier {
			t.Errorf("row %d = %v, want %s %v %s", i+1, e, players.Short(w.id), w.score, w.tier)
		}
	}
	if pad := hs.board("?source=pad"); len(pad) != 1 || pad[0]["tier"] != "LEGEND" || pad[0]["score"] != float64(2220) {
		t.Errorf("pad board %v", pad)
	}
	if kb := hs.board("?source=keyboard"); len(kb) != 0 {
		t.Errorf("keyboard board %v, want empty", kb)
	}

	rank, size, err := hs.lb.Rank(context.Background(), "touch", a)
	if err != nil || rank != 3 || size != 4 {
		t.Errorf("Rank(a) = %d/%d %v, want 3/4", rank, size, err)
	}
	if rank, size, _ := hs.lb.Rank(context.Background(), "keyboard", a); rank != 0 || size != 0 {
		t.Errorf("Rank on an empty board = %d/%d, want 0/0", rank, size)
	}
}

func TestBoardWindowAndLimit(t *testing.T) {
	hs := newHarness(t)
	_, ta := hs.player()
	_, tb := hs.player()
	_, tc := hs.player()
	if rec, _ := hs.submit(ta, trial("1", "touch", []float64{200, 205, 210}, 0)); rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	hs.now = hs.now.Add(31 * 24 * time.Hour)
	if rec, _ := hs.submit(tb, trial("1", "touch", []float64{250, 255, 260}, 0)); rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	if rec, out := hs.submit(tc, trial("1", "touch", []float64{300, 305, 310}, 0)); rec.Code != 201 || out["rank"] != float64(3) || out["board_size"] != float64(3) {
		t.Fatalf("third player: %d %s, want rank 3 of 3", rec.Code, rec.Body.String())
	}

	if all := hs.board("?source=touch&window=all"); len(all) != 3 {
		t.Errorf("all: %d entries", len(all))
	}
	recentBest := float64(ScoreTrial([]float64{250, 255, 260}, 0).Score)
	if recent := hs.board("?source=touch&window=30d"); len(recent) != 2 || recent[0]["score"] != recentBest {
		t.Errorf("30d: %v, want only the two recent rows", recent)
	}
	if two := hs.board("?source=touch&limit=2"); len(two) != 2 || two[1]["rank"] != float64(2) {
		t.Errorf("limit=2: %v", two)
	}
	if capped := hs.board("?limit=100000"); len(capped) != 3 {
		t.Errorf("limit above the cap must clamp, not fail: %v", capped)
	}
	if def := hs.board(""); len(def) != 3 {
		t.Errorf("defaults (touch, all, 50): %v", def)
	}
}

func TestBoardParamsAndEmpty(t *testing.T) {
	hs := newHarness(t)
	rec, _ := hs.do("GET", "/v1/board", "", "", "")
	if want := `{"source":"touch","window":"all","entries":[]}` + "\n"; rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("empty board: %d %q, want %q", rec.Code, rec.Body.String(), want)
	}
	for _, q := range []string{"?source=mouse", "?window=7d", "?limit=abc", "?limit=0", "?limit=-1"} {
		rec, out := hs.do("GET", "/v1/board"+q, "", "", "")
		if rec.Code != 400 || out["error"] != "bad_request" || out["reason"] == nil {
			t.Errorf("%s: got %d %s, want 400 bad_request with a reason", q, rec.Code, rec.Body.String())
		}
	}
}

func TestBestsAndDeleteErasesSubmissions(t *testing.T) {
	hs := newHarness(t)
	id, token := hs.player()
	for _, b := range []map[string]any{
		trial("1", "touch", []float64{200, 205, 210}, 0),
		trial("2", "touch", []float64{300, 380, 450}, 0),
		trial("3", "pad", []float64{170, 175, 180}, 0),
	} {
		if rec, _ := hs.submit(token, b); rec.Code != 201 {
			t.Fatal(rec.Body.String())
		}
	}
	rec, out := hs.do("GET", "/v1/players/"+id, "", "", "")
	if rec.Code != 200 || out["submissions"] != float64(3) {
		t.Fatalf("profile: %d %s", rec.Code, rec.Body.String())
	}
	best := out["best"].(map[string]any)
	if best["keyboard"] != nil || best["touch"].(map[string]any)["score"] != float64(2100) || best["pad"].(map[string]any)["tier"] != "LEGEND" {
		t.Errorf("best %v", best)
	}

	if rec, _ := hs.do("DELETE", "/v1/players/"+id, "", token, ""); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := hs.db.DB().QueryRow(`SELECT count(*) FROM submissions WHERE player_id = ?`, id).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d submissions survive the player (err %v)", n, err)
	}
	if entries := hs.board("?source=touch"); len(entries) != 0 {
		t.Errorf("board still lists the erased player: %v", entries)
	}
	if rec, _ := hs.do("GET", "/v1/players/"+id, "", "", ""); rec.Code != 404 {
		t.Errorf("profile after delete: %d", rec.Code)
	}
}
