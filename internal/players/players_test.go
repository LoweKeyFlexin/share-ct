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
		"short": {`{"display_name":"ab","platform":"ios"}`, 422, "name_too_short"},
		// NOT here, deliberately: an empty or blank display_name is ANONYMOUS, not an
		// error. Its 201 is asserted in TestRegisteringWithNoNameIsAnonymous below.
		// Derived from MaxNameLen, never a literal: this fixture was "1234567890123456",
		// a 16-character name, and silently became a VALID name the moment the limit
		// moved to 20 - the test then failed on correct code and said "too_long" about a
		// name that is not.
		"long":        {`{"display_name":"` + strings.Repeat("a", MaxNameLen+1) + `","platform":"ios"}`, 422, "name_too_long"},
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

func TestNamePolicyOnRegistrationAndRename(t *testing.T) {
	hs := newHarness(t, stubScores{})
	// A harmless test predicate proves both HTTP write paths use the policy. The
	// digest matcher itself is tested with separate harmless vectors.
	hs.m.rejectName = func(name string) bool { return strings.EqualFold(name, "TEAPOT") }
	rec, out := hs.do("POST", "/v1/players", `{"display_name":"Teapot","platform":"ios"}`, "", "")
	if rec.Code != 422 || out["error"] != "invalid" || out["reason"] != "name_inappropriate" {
		t.Fatalf("register rejection: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "teapot") {
		t.Error("rejection response echoed the name")
	}
	var count int
	if err := hs.db.DB().QueryRow(`SELECT COUNT(*) FROM players`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected registration wrote a player: count=%d err=%v", count, err)
	}
	id, token := hs.register("Aaron", "")
	rec, out = hs.do("PATCH", "/v1/players/"+id, `{"display_name":"teapot"}`, token, "")
	if rec.Code != 422 || out["reason"] != "name_inappropriate" {
		t.Fatalf("rename rejection: %d %s", rec.Code, rec.Body.String())
	}
	if p, err := hs.m.Store().Get(context.Background(), id); err != nil || p.DisplayName != "Aaron" {
		t.Errorf("rejected rename changed stored name: %+v, %v", p, err)
	}
	// Empty/whitespace still intentionally clears back to the anonymous sentinel.
	rec, out = hs.do("PATCH", "/v1/players/"+id, `{"display_name":"  "}`, token, "")
	if rec.Code != 200 || out["display_name"] != AnonymousName {
		t.Errorf("anonymous rename: %d %s", rec.Code, rec.Body.String())
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
	// The token died with the row, so the app's retry of a delete whose 204 was lost
	// arrives unauthenticated. It reads as already erased, 404, never 401: the app keeps
	// the credentials and retries until it hears 204 or 404.
	if rec, out := hs.do("DELETE", "/v1/players/"+id, "", token, ""); rec.Code != 404 || out["error"] != "not_found" {
		t.Errorf("repeat delete after the erase: got %d %s, want 404 not_found", rec.Code, rec.Body.String())
	}
	// Everything else the dead token tries is still refused.
	if rec, _ := hs.do("PATCH", "/v1/players/"+id, `{"display_name":"Ghost"}`, token, ""); rec.Code != 401 {
		t.Errorf("the token must die with the player: PATCH got %d, want 401", rec.Code)
	}
	// An id that never existed is 404 too, with or without a token, and never 500.
	for name, tok := range map[string]string{"no token": "", "another player's token": otherToken} {
		if rec, _ := hs.do("DELETE", "/v1/players/00000000-0000-4000-8000-000000000000", "", tok, ""); rec.Code != 404 {
			t.Errorf("delete of an unknown id, %s: got %d, want 404", name, rec.Code)
		}
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

// TestRegisteringWithNoNameIsAnonymous pins controller-tester-fgc#6112 and Aaron's
// ruling of 2026-09-11: "empty names just register with NO NAME. Players already have a
// unique player ID", and "entering nothing on the keyboard for Entry should just default
// the player back to NO NAME".
//
// The shape being pinned is the one that was CHOSEN over two simpler ones. The anonymous
// player is stored with an EMPTY name and rendered AnonymousName at every read boundary.
// Storing the literal "NO NAME" instead would have required unreserving it - registration
// is one endpoint with one field and no privileged path, so the server cannot tell the
// app's default from a player who typed the same words - and any player could then hide
// among the unnamed on a shared board. Absence has no spelling, so there is nothing to
// collide with.
//
// Before this, the app sent an empty name, got 422 name_too_short, discarded the reason,
// and left the player opted in with no account and no message.
func TestRegisteringWithNoNameIsAnonymous(t *testing.T) {
	hs := newHarness(t, stubScores{})

	for _, body := range []string{
		`{"platform":"ios"}`,                               // omitted entirely
		`{"display_name":"","platform":"ios"}`,             // empty
		`{"display_name":"  ","platform":"ios"}`,           // whitespace only
		"{\"display_name\":\"\\t \",\"platform\":\"ios\"}", // tab, which CharacterSet.whitespaces includes
	} {
		rec, out := hs.do("POST", "/v1/players", body, "", "")
		if rec.Code != http.StatusCreated {
			t.Fatalf("%s: got %d %s, want 201", body, rec.Code, rec.Body.String())
		}
		id, _ := out["player_id"].(string)
		if id == "" {
			t.Fatalf("%s: no player_id", body)
		}
		// The identity that actually tells two nameless players apart.
		prec, pout := hs.do("GET", "/v1/players/"+id, "", "", "")
		if prec.Code != http.StatusOK {
			t.Fatalf("profile: got %d", prec.Code)
		}
		if got := pout["display_name"]; got != AnonymousName {
			t.Errorf("%s: profile display_name = %v, want %q", body, got, AnonymousName)
		}
		if short, _ := pout["player_short"].(string); short == "" {
			t.Errorf("%s: an anonymous player still needs a player_short to be told apart", body)
		}
	}

	// The word itself stays unclaimable: that is what makes rendering it safe.
	for _, claim := range []string{"NO NAME", "no name", "NoName", "  NO   NAME  "} {
		body := `{"display_name":"` + claim + `","platform":"ios"}`
		rec, out := hs.do("POST", "/v1/players", body, "", "")
		if rec.Code != 422 || out["reason"] != "name_reserved" {
			t.Errorf("claiming %q: got %d %s, want 422 name_reserved", claim, rec.Code, rec.Body.String())
		}
	}
}

// TestClearingTheNameReturnsToAnonymous is the second half of the ruling: the rename
// endpoint treats a blank field as "go back to anonymous", not as a 422.
func TestClearingTheNameReturnsToAnonymous(t *testing.T) {
	hs := newHarness(t, stubScores{})
	id, token := hs.register("Aaron", "")

	rec, out := hs.do("PATCH", "/v1/players/"+id, `{"display_name":"  "}`, token, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear: got %d %s, want 200", rec.Code, rec.Body.String())
	}
	if got := out["display_name"]; got != AnonymousName {
		t.Errorf("rename response display_name = %v, want %q", got, AnonymousName)
	}
	_, pout := hs.do("GET", "/v1/players/"+id, "", "", "")
	if got := pout["display_name"]; got != AnonymousName {
		t.Errorf("profile after clearing = %v, want %q", got, AnonymousName)
	}
	// And back again, so the sentinel is not a one-way door.
	hs.do("PATCH", "/v1/players/"+id, `{"display_name":"Aaron"}`, token, id)
	_, pout2 := hs.do("GET", "/v1/players/"+id, "", "", "")
	if got := pout2["display_name"]; got != "Aaron" {
		t.Errorf("profile after renaming back = %v, want Aaron", got)
	}
}
