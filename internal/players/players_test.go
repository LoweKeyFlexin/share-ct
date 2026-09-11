package players

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/store"
	"github.com/LoweKeyFlexin/share-ct/migrations"
)

type stubScores struct {
	bests Bests
	n     int
}

func (s stubScores) Bests(context.Context, string) (Bests, int, error) { return s.bests, s.n, nil }

type harness struct {
	t   *testing.T
	h   http.Handler
	m   *Module
	db  *store.Store
	now time.Time
}

func newHarness(t *testing.T, scores Scores) *harness {
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
	hs.m = New(slog.New(slog.NewTextHandler(io.Discard, nil)), db.DB(), func() time.Time { return hs.now }, scores)
	mux := http.NewServeMux()
	hs.m.Register(mux)
	hs.h = mux
	return hs
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

func (hs *harness) register(name, ip string) (id, token string) {
	hs.t.Helper()
	rec, out := hs.do("POST", "/v1/players", `{"display_name":"`+name+`","platform":"ios"}`, "", ip)
	if rec.Code != http.StatusCreated {
		hs.t.Fatalf("register %q: %d %s", name, rec.Code, rec.Body.String())
	}
	return out["player_id"].(string), out["token"].(string)
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestCreatePlayer(t *testing.T) {
	hs := newHarness(t, stubScores{})
	rec, out := hs.do("POST", "/v1/players", `{"display_name":"  Aaron ","platform":"ios"}`, "", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	id, token := out["player_id"].(string), out["token"].(string)
	if !uuidV4.MatchString(id) {
		t.Errorf("player_id %q is not a v4 UUID", id)
	}
	if !looksLikeToken(token) {
		t.Errorf("token %q is not 43 chars of base64url", token)
	}

	var stored, name string
	row := hs.db.DB().QueryRow(`SELECT token_hash, display_name FROM players WHERE id = ?`, id)
	if err := row.Scan(&stored, &name); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(token))
	if stored == token || stored != hex.EncodeToString(sum[:]) {
		t.Errorf("stored %q, want hex(sha256(token)) and never the token", stored)
	}
	if name != "Aaron" {
		t.Errorf("display name stored as %q, want trimmed", name)
	}

	a, _ := hs.register("Other", "")
	if a == id {
		t.Error("two registrations share an id")
	}
}

func TestCreateRejects(t *testing.T) {
	hs := newHarness(t, stubScores{})
	cases := map[string]struct {
		body   string
		code   int
		reason string
	}{
		"short":       {`{"display_name":"ab","platform":"ios"}`, 422, "name_too_short"},
		"long":        {`{"display_name":"1234567890123456","platform":"ios"}`, 422, "name_too_long"},
		"chars":       {`{"display_name":"a!b","platform":"ios"}`, 422, "name_invalid_characters"},
		"reserved":    {`{"display_name":"ADMIN","platform":"ios"}`, 422, "name_reserved"},
		"platform":    {`{"display_name":"Aaron","platform":"linux"}`, 422, "platform"},
		"no platform": {`{"display_name":"Aaron"}`, 422, "platform"},
		"bad json":    {`{"display_name":`, 400, "json"},
		"empty":       {``, 400, "json"},
	}
	for name, c := range cases {
		rec, out := hs.do("POST", "/v1/players", c.body, "", "")
		if rec.Code != c.code || out["reason"] != c.reason {
			t.Errorf("%s: got %d %s, want %d reason %q", name, rec.Code, rec.Body.String(), c.code, c.reason)
		}
	}
	huge := `{"display_name":"Aaron","platform":"ios","pad":"` + strings.Repeat("x", 5000) + `"}`
	if rec, _ := hs.do("POST", "/v1/players", huge, "", ""); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("5 KB body: got %d, want 413", rec.Code)
	}
}

func TestCreateRateLimitPerIP(t *testing.T) {
	hs := newHarness(t, stubScores{})
	for i := 0; i < RegistrationsPerIP; i++ {
		hs.register("Player", "203.0.113.5")
	}
	rec, _ := hs.do("POST", "/v1/players", `{"display_name":"Player","platform":"ios"}`, "", "203.0.113.5")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("11th registration: got %d Retry-After %q, want 429 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
	hs.register("Player", "203.0.113.6")
	hs.now = hs.now.Add(24 * time.Hour)
	hs.register("Player", "203.0.113.5")
}

func TestRenameAuth(t *testing.T) {
	hs := newHarness(t, stubScores{})
	id, token := hs.register("Aaron", "")
	other, otherToken := hs.register("Someone", "")
	body := `{"display_name":"Aaron2"}`

	rec, out := hs.do("PATCH", "/v1/players/"+id, body, "", "")
	if rec.Code != 401 || out["error"] != "unauthorized" || rec.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("no token: got %d %s", rec.Code, rec.Body.String())
	}
	if rec, _ := hs.do("PATCH", "/v1/players/"+id, body, "not-a-token", ""); rec.Code != 401 {
		t.Errorf("malformed token: got %d", rec.Code)
	}
	fake := strings.Repeat("A", tokenLen)
	if rec, _ := hs.do("PATCH", "/v1/players/"+id, body, fake, ""); rec.Code != 401 {
		t.Errorf("unknown token: got %d", rec.Code)
	}
	if rec, out := hs.do("PATCH", "/v1/players/"+id, body, otherToken, ""); rec.Code != 403 || out["error"] != "forbidden" {
		t.Errorf("other player's token: got %d %s", rec.Code, rec.Body.String())
	}
	if rec, out := hs.do("PATCH", "/v1/players/"+id, `{"display_name":"x"}`, token, ""); rec.Code != 422 || out["reason"] != "name_too_short" {
		t.Errorf("bad name: got %d %s", rec.Code, rec.Body.String())
	}

	r := httptest.NewRequest("PATCH", "/v1/players/"+id, strings.NewReader(body))
	r.Header.Set("Authorization", "bearer "+token)
	rec = httptest.NewRecorder()
	hs.h.ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("rename with a lower-case scheme: got %d %s", rec.Code, rec.Body.String())
	}
	if p, err := hs.m.Store().Get(context.Background(), id); err != nil || p.DisplayName != "Aaron2" {
		t.Errorf("after rename: %+v, %v", p, err)
	}
	if p, _ := hs.m.Store().Get(context.Background(), other); p.DisplayName != "Someone" {
		t.Errorf("the other player was renamed: %+v", p)
	}
}

func TestDeletePlayer(t *testing.T) {
	hs := newHarness(t, stubScores{})
	id, token := hs.register("Aaron", "")
	_, otherToken := hs.register("Someone", "")

	if rec, _ := hs.do("DELETE", "/v1/players/"+id, "", otherToken, ""); rec.Code != 403 {
		t.Errorf("other player's token: got %d", rec.Code)
	}
	if rec, _ := hs.do("DELETE", "/v1/players/"+id, "", "", ""); rec.Code != 401 {
		t.Errorf("no token: got %d", rec.Code)
	}
	rec, _ := hs.do("DELETE", "/v1/players/"+id, "", token, "")
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("delete: got %d %q", rec.Code, rec.Body.String())
	}
	if rec, _ := hs.do("GET", "/v1/players/"+id, "", "", ""); rec.Code != 404 {
		t.Errorf("after delete GET: got %d, want 404", rec.Code)
	}
	if rec, _ := hs.do("DELETE", "/v1/players/"+id, "", token, ""); rec.Code != 401 {
		t.Errorf("the token must die with the player: got %d, want 401", rec.Code)
	}
}

func TestGetProfile(t *testing.T) {
	best := Bests{Pad: &ScoreSummary{Score: 2100, BestMs: 200, AvgMs: 205, Accuracy: 1, Tier: "DIAMOND", Platform: "ios", CreatedAt: 5}}
	hs := newHarness(t, stubScores{bests: best, n: 3})
	id, _ := hs.register("Aaron", "")

	rec, out := hs.do("GET", "/v1/players/"+id, "", "", "")
	if rec.Code != 200 {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if out["display_name"] != "Aaron" || out["player_short"] != Short(id) || out["submissions"] != float64(3) || out["platform"] != "ios" {
		t.Errorf("profile %s", rec.Body.String())
	}
	b := out["best"].(map[string]any)
	if b["touch"] != nil || b["keyboard"] != nil || b["pad"].(map[string]any)["tier"] != "DIAMOND" {
		t.Errorf("best %v", b)
	}
	if rec, _ := hs.do("GET", "/v1/players/00000000-0000-4000-8000-000000000000", "", "", ""); rec.Code != 404 {
		t.Errorf("unknown id: got %d", rec.Code)
	}
}
