package drills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/LoweKeyFlexin/share-ct/internal/players"
	"net/http"
	"testing"
	"time"
)

func arcadeReq(id string, enemies, total, ms, perfect, hits int) Request {
	a := &ArcadeStats{EnemiesDefeated: enemies, TotalOpponents: total, Completed: enemies == total, ElapsedMS: ms, PerfectOpponents: perfect, Hits: hits}
	score := a.points()
	r := req(id, "whiff-punish", score)
	r.Mode, r.ArcadeStats, r.Misses = "arcade", a, 0
	return r
}
func postArcade(t *testing.T, h interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, token, route string, r Request, want int) {
	t.Helper()
	data, _ := json.Marshal(r)
	w := call(h, "POST", route, string(data), token)
	if w.Code != want {
		t.Fatalf("%s: want%d got%d %s", r.ClientID, want, w.Code, w.Body.String())
	}
}

func TestArcadeFormulaAndValidation(t *testing.T) {
	r := arcadeReq("one", 6, 13, 123456, 4, 20)
	if *r.Score != 60 || validate(r) != "" {
		t.Fatalf("%+v %s", r, validate(r))
	}
	clear := arcadeReq("clear", 12, 12, 123456, 10, 40)
	if *clear.Score != 307 {
		t.Fatalf("clear score=%d", *clear.Score)
	}
	slow := arcadeReq("slow", 13, 13, 900000, 0, 40)
	if *slow.Score != 80 {
		t.Fatal(*slow.Score)
	}
	for name, change := range map[string]func(*Request){
		"total_opponents":   func(r *Request) { r.TotalOpponents = 99 },
		"completed":         func(r *Request) { r.Completed = true },
		"perfect_opponents": func(r *Request) { r.PerfectOpponents = 7 },
		"hits":              func(r *Request) { r.Hits = 0 },
		"elapsed_ms":        func(r *Request) { r.ElapsedMS = 86400001 },
		"misses":            func(r *Request) { r.Misses = -1 },
		"score":             func(r *Request) { v := 1; r.Score = &v },
		"consistency_bonus": func(r *Request) { r.ConsistencyBonus = 999 },
		"time_bonus":        func(r *Request) { r.TimeBonus = 1 },
		"drill_id":          func(r *Request) { r.DrillID = "mental-stack" },
	} {
		v := arcadeReq(name, 6, 13, 123456, 4, 20)
		change(&v)
		if got := validate(v); got != name {
			t.Errorf("%s got %s", name, got)
		}
	}
	noStats := req("missing", "anti-air", 0)
	noStats.Mode = "arcade"
	if validate(noStats) != "arcade" {
		t.Fatal(validate(noStats))
	}
	explicitEndless := r
	explicitEndless.Mode = "endless"
	if validate(explicitEndless) != "mode" {
		t.Fatal(validate(explicitEndless))
	}
}

func TestArcadeRankingAndPersonalBestUseSameOrder(t *testing.T) {
	m, pl, _ := harness(t)
	ctx := context.Background()
	cases := []Request{
		arcadeReq("partial13", 12, 13, 1000, 12, 999),
		arcadeReq("clear12", 12, 12, 190000, 0, 12),
		arcadeReq("fast", 13, 13, 180000, 0, 13),
		arcadeReq("rich", 13, 13, 180000, 13, 40),
		arcadeReq("partiallow", 11, 13, 1000, 11, 9999),
		arcadeReq("partialpoints", 12, 13, 9000, 0, 12),
	}
	playerIDs := []string{}
	for i, r := range cases {
		p, _, _ := pl.Create(ctx, fmt.Sprintf("Player %c", 'A'+i), "ios")
		playerIDs = append(playerIDs, p.ID)
		if _, _, err := m.saveArcade(ctx, p.ID, r, false); err != nil {
			t.Fatal(err)
		}
	}
	b, err := m.readArcade(ctx, "whiff-punish", playerIDs[0], 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{playerIDs[3], playerIDs[2], playerIDs[1], playerIDs[0], playerIDs[5], playerIDs[4]}
	for i, e := range b.Entries {
		if e.PlayerShort != players.Short(want[i]) {
			t.Fatalf("rank%d: %+v", i, e)
		}
	}
	if b.Mode != "arcade" || b.BoardSize != 6 || b.Own.Rank != 4 {
		t.Fatalf("%+v", b)
	}
	// Worse progress never wins on points. A clear always replaces a partial;
	// a slower clear never replaces a faster clear on bonus points.
	for _, r := range []Request{arcadeReq("worse", 11, 13, 1, 11, 9999), arcadeReq("clear", 12, 12, 200000, 0, 12), arcadeReq("slower", 13, 13, 250000, 13, 9000)} {
		if _, _, err = m.saveArcade(ctx, playerIDs[0], r, false); err != nil {
			t.Fatal(err)
		}
	}
	b, err = m.readArcade(ctx, "whiff-punish", playerIDs[0], 10, 0, false)
	if err != nil || b.Own.ElapsedMS != 200000 || !b.Own.Completed {
		t.Fatalf("%+v %v", b.Own, err)
	}
	tied := arcadeReq("tie", 12, 12, 200000, 0, 12)
	if _, _, err = m.saveArcade(ctx, playerIDs[0], tied, false); err != nil {
		t.Fatal(err)
	}
	var retained string
	if err = m.db.QueryRow(`SELECT client_id FROM arcade_bests WHERE player_id=?`, playerIDs[0]).Scan(&retained); err != nil || retained != "clear" {
		t.Fatal(retained, err)
	}
}

func TestArcadeModeIsolationIntentReplayAndErase(t *testing.T) {
	m, pl, h := harness(t)
	ctx := context.Background()
	p, token, _ := pl.Create(ctx, "Alpha", "ios")
	endless := req("legacy", "whiff-punish", 999)
	postArcade(t, h, token, "/v1/drills/runs", endless, 201)
	a := arcadeReq("arcade", 0, 13, 1000, 0, 0)
	postArcade(t, h, token, "/v1/drills/scores", a, 201)
	postArcade(t, h, token, "/v1/drills/scores", a, 200)
	postArcade(t, h, token, "/v1/drills/runs", a, 422)
	a.ClientID = "newrun"
	postArcade(t, h, token, "/v1/drills/runs", a, 201)
	postArcade(t, h, token, "/v1/drills/runs", a, 200)
	changed := arcadeReq("newrun", 1, 13, 2000, 0, 1)
	postArcade(t, h, token, "/v1/drills/runs", changed, 422)
	for _, mode := range []string{"arcade", "endless"} {
		for _, sort := range []string{"top", "recent"} {
			w := call(h, "GET", "/v1/drills/board?drill_id=whiff-punish&mode="+mode+"&sort="+sort, "", token)
			var b Board
			if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || w.Code != 200 || b.Mode != mode || len(b.Entries) != 1 || b.BoardSize != 1 {
				t.Fatalf("%s %s: %s %v", mode, sort, w.Body.String(), err)
			}
			if mode == "arcade" && (b.Entries[0].ArcadeStats == nil || b.Entries[0].Score != 0) {
				t.Fatal(w.Body.String())
			}
			if mode == "endless" && (b.Entries[0].ArcadeStats != nil || b.Entries[0].Score != 999) {
				t.Fatal(w.Body.String())
			}
		}
	}
	for _, query := range []string{"drill_id=mental-stack&mode=arcade", "drill_id=anti-air&mode=unknown"} {
		if w := call(h, "GET", "/v1/drills/board?"+query, "", ""); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	// Public best-only never leaks into Recent. Current avatar and zero-valued
	// Arcade facts are present; the mode echo lets old-server clients fail closed.
	avatar := "1:puppet-oni:1:7:pirate-hat"
	if err := pl.SetAvatar(ctx, p.ID, &avatar); err != nil {
		t.Fatal(err)
	}
	w := call(h, "GET", "/v1/drills/board?drill_id=whiff-punish&mode=arcade&sort=recent", "", "")
	var raw map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	e := raw["entries"].([]any)[0].(map[string]any)
	for _, field := range []string{"enemies_defeated", "total_opponents", "completed", "elapsed_ms", "perfect_opponents", "hits", "consistency_bonus", "time_bonus"} {
		if _, ok := e[field]; !ok {
			t.Fatalf("missing %s", field)
		}
	}
	if e["avatar"] != avatar || e["rank"] != float64(0) || e["run_ref"] == "" {
		t.Fatal(e)
	}
	if err := pl.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"arcade_bests", "arcade_runs", "arcade_receipts", "drill_feed_order"} {
		var n int
		if err := m.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
}

func TestArcadeWindowRetentionAndDurableRetry(t *testing.T) {
	m, pl, _ := harness(t)
	ctx := context.Background()
	p, _, _ := pl.Create(ctx, "Alpha", "ios")
	old := arcadeReq("old", 13, 13, 1000, 13, 100)
	if _, _, err := m.saveArcade(ctx, p.ID, old, true); err != nil {
		t.Fatal(err)
	}
	then := m.now().Add(-40 * 24 * time.Hour).Unix()
	for _, table := range []string{"arcade_bests", "arcade_runs"} {
		if _, err := m.db.Exec(`UPDATE `+table+` SET created_at=?`, then); err != nil {
			t.Fatal(err)
		}
	}
	fresh := arcadeReq("fresh", 3, 13, 2000, 1, 5)
	if _, _, err := m.saveArcade(ctx, p.ID, fresh, true); err != nil {
		t.Fatal(err)
	}
	since := m.now().Add(-30 * 24 * time.Hour).Unix()
	b, err := m.readArcade(ctx, "whiff-punish", p.ID, 10, since, false)
	if err != nil || b.Own.EnemiesDefeated != 3 {
		t.Fatal(b, err)
	}
	// Overflow retry ring; retained best or run remains durable retry proof.
	for i := 0; i < 205; i++ {
		r := arcadeReq(fmt.Sprintf("r%d", i), 0, 13, 3000+i, 0, 0)
		if _, _, err = m.saveArcade(ctx, p.ID, r, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, repeated, err := m.saveArcade(ctx, p.ID, old, true); err != nil || !repeated {
		t.Fatal(repeated, err)
	}
	for table, want := range map[string]int{"arcade_receipts": 100, "arcade_runs": 200} {
		var n int
		if err = m.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != want {
			t.Fatal(table, n, err)
		}
	}
	if _, err = m.db.Exec(`UPDATE arcade_runs SET created_at=?`, m.now().Add(-91*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	b, err = m.readArcade(ctx, "whiff-punish", p.ID, 10, 0, true)
	if err != nil || len(b.Entries) != 0 || b.BoardSize != 1 || b.Own == nil {
		t.Fatal(b, err)
	}
}

func TestLegacyRequestHashUnchanged(t *testing.T) {
	m, pl, h := harness(t)
	ctx := context.Background()
	p, token, _ := pl.Create(ctx, "Alpha", "ios")
	// Literal ordering is the old Request encoding, already persisted by Ver43.
	body := `{"client_id":"old","drill_id":"whiff-punish","score":2,"misses":3,"source":"pad","app_build":"1","platform":"ios","device_model":"iPhone","device_label":"Pad"}`
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])
	if _, err := m.db.Exec(`INSERT INTO drill_receipts(player_id,client_id,body_hash,created_at,publishes_run) VALUES(?,?,?,?,0)`, p.ID, "old", hash, m.now().Unix()); err != nil {
		t.Fatal(err)
	}
	w := call(h, "POST", "/v1/drills/scores", body, token)
	if w.Code != 200 {
		t.Fatalf("legacy retry: %d %s", w.Code, w.Body.String())
	}
}

func TestEveryAtomicDrillHasItsOwnArcadeStanding(t *testing.T) {
	m, pl, h := harness(t)
	ctx := context.Background()
	p, token, _ := pl.Create(ctx, "Alpha", "ios")
	for i, drill := range []string{"whiff-punish", "hit-confirm", "anti-air", "crosscut", "drive-impact"} {
		r := arcadeReq("run-"+drill, i, 13, 10000+i, 0, i)
		r.DrillID = drill
		postArcade(t, h, token, "/v1/drills/runs", r, 201)
	}
	for i, drill := range []string{"whiff-punish", "hit-confirm", "anti-air", "crosscut", "drive-impact"} {
		b, err := m.readArcade(ctx, drill, p.ID, 10, 0, false)
		if err != nil || b.BoardSize != 1 || b.Own == nil || b.Own.EnemiesDefeated != i {
			t.Fatalf("%s %+v %v", drill, b, err)
		}
		endless, err := m.read(ctx, drill, p.ID, 10)
		if err != nil || endless.BoardSize != 0 || endless.Own != nil {
			t.Fatalf("cross-mode %s %+v %v", drill, endless, err)
		}
	}
}

func TestRecentCapIsSharedAcrossArcadeAndEndless(t *testing.T) {
	m, pl, _ := harness(t)
	ctx := context.Background()
	p, _, _ := pl.Create(ctx, "Alpha", "ios")
	for i := 0; i < 210; i++ {
		id := fmt.Sprintf("mixed-%d", i)
		var err error
		if i%2 == 0 {
			_, _, err = m.saveWithIntent(ctx, p.ID, req(id, "whiff-punish", i), true)
		} else {
			_, _, err = m.saveArcade(ctx, p.ID, arcadeReq(id, 0, 13, 1000+i, 0, 0), true)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	var total int
	if err := m.db.QueryRow(`SELECT (SELECT count(*) FROM drill_runs)+(SELECT count(*) FROM arcade_runs)`).Scan(&total); err != nil || total != RunCap {
		t.Fatal(total, err)
	}
	for _, table := range []string{"drill_runs", "arcade_runs"} {
		var present bool
		// Equal-time inserts use a stable table/id tie. Each mode still retains
		// its newest100 rows; both oldestfive must have been removed.
		if err := m.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM ` + table + ` WHERE client_id IN ('mixed-0','mixed-1'))`).Scan(&present); err != nil || present {
			t.Fatal(table, present, err)
		}
	}
}
