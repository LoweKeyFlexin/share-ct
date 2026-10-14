package drills

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

func harness(t *testing.T) (*Module, *players.Store, http.Handler) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Unix(1800000000, 0) }
	pl := players.New(slog.New(slog.NewTextHandler(io.Discard, nil)), db.DB(), now, nil)
	m := New(db.DB(), now)
	mux := http.NewServeMux()
	m.Register(mux, pl.RequireBearer)
	return m, pl.Store(), mux
}
func req(id, drill string, score int) Request {
	return Request{ClientID: id, DrillID: drill, Score: &score, Misses: 3, Source: "pad", AppBuild: "1", Platform: "ios", DeviceModel: "iPhone", DeviceLabel: "Pad"}
}
func call(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestCompletionValidationAuthAndOwnStanding(t *testing.T) {
	_, pl, h := harness(t)
	ctx := context.Background()
	p, token, err := pl.Create(ctx, "Alpha", "ios")
	if err != nil {
		t.Fatal(err)
	}
	payload := req("run1", "whiff-punish", 6)
	data, _ := json.Marshal(payload)
	if w := call(h, "POST", "/v1/drills/scores", string(data), ""); w.Code != 401 {
		t.Fatalf("unauth=%d", w.Code)
	}
	for _, misses := range []int{0, 1, 2, 4} {
		payload.Misses = misses
		d, _ := json.Marshal(payload)
		if w := call(h, "POST", "/v1/drills/scores", string(d), token); w.Code != 422 {
			t.Fatalf("misses %d accepted", misses)
		}
	}
	if w := call(h, "POST", "/v1/drills/scores", strings.TrimSuffix(string(data), "}")+`,"unknown":1}`, token); w.Code != 422 {
		t.Fatal(w.Code)
	}
	for _, want := range []int{201, 200} {
		if w := call(h, "POST", "/v1/drills/scores", string(data), token); w.Code != want {
			t.Fatalf("want %d got %d %s", want, w.Code, w.Body.String())
		}
	}
	w := call(h, "GET", "/v1/drills/board?drill_id=whiff-punish", "", token)
	var b Board
	if err = json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || b.Own == nil || b.Own.PlayerShort != p.Short() || b.Own.Score != 6 || b.BoardSize != 1 {
		t.Fatalf("board=%+v", b)
	}
	if w := call(h, "GET", "/v1/drills/board?drill_id=unknown", "", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call(h, "GET", "/v1/drills/board?drill_id=whiff-punish", "", "invalid"); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestBestPerDrillTiesConflictsBoundsAndErase(t *testing.T) {
	m, pl, _ := harness(t)
	ctx := context.Background()
	p, _, _ := pl.Create(ctx, "Alpha", "ios")
	q, _, _ := pl.Create(ctx, "Beta", "ios")
	first, repeat, err := m.save(ctx, p.ID, req("a", "whiff-punish", 8))
	if err != nil || repeat {
		t.Fatal(err)
	}
	if id, repeat, err := m.save(ctx, p.ID, req("a", "whiff-punish", 8)); err != nil || !repeat || id != first {
		t.Fatal(id, repeat, err)
	}
	if _, _, err := m.save(ctx, p.ID, req("a", "whiff-punish", 9)); err != errConflict {
		t.Fatal(err)
	}
	for _, r := range []Request{req("b", "whiff-punish", 2), req("c", "hit-confirm", 10)} {
		if _, _, err := m.save(ctx, p.ID, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := m.save(ctx, q.ID, req("d", "whiff-punish", 8)); err != nil {
		t.Fatal(err)
	}
	b, err := m.read(ctx, "whiff-punish", q.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Entries) != 1 || b.Entries[0].PlayerShort != p.Short() || b.Entries[0].Score != 8 || b.Own == nil || b.Own.Rank != 2 {
		t.Fatalf("%+v", b)
	}
	for i := 0; i < 105; i++ {
		if _, _, err := m.save(ctx, p.ID, req(fmt.Sprint("more", i), "whiff-punish", 1)); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err = m.db.QueryRow(`SELECT count(*) FROM drill_receipts WHERE player_id=?`, p.ID).Scan(&n); err != nil || n != 100 {
		t.Fatal(n, err)
	}
	if err = pl.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"drill_bests", "drill_receipts"} {
		if err = m.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE player_id=?`, p.ID).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
}

func TestAllInputDevicesCompeteOnOneDrillBoard(t *testing.T) {
	m, pl, h := harness(t)
	ctx := context.Background()
	for i, source := range []string{"pad", "keyboard", "touch"} {
		p, _, err := pl.Create(ctx, fmt.Sprintf("Player %d", i), "ios")
		if err != nil {
			t.Fatal(err)
		}
		r := req(fmt.Sprintf("run-%d", i), "anti-air", 10+i)
		r.Source = source
		r.DeviceLabel = source
		if _, _, err = m.save(ctx, p.ID, r); err != nil {
			t.Fatal(err)
		}
	}
	w := call(h, "GET", "/v1/drills/board?drill_id=anti-air", "", "")
	var b Board
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(b.Entries) != 3 || b.BoardSize != 3 {
		t.Fatalf("mixed board %+v", b)
	}
	for i, source := range []string{"touch", "keyboard", "pad"} {
		e := b.Entries[i]
		if e.Rank != i+1 || e.Score != 12-i || e.Source != source {
			t.Fatalf("entry %d %+v", i, e)
		}
	}
}
