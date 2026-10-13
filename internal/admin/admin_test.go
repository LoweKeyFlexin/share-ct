package admin_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/admin"
	"github.com/LoweKeyFlexin/share-ct/internal/moderation"
	"github.com/LoweKeyFlexin/share-ct/internal/players"
	"github.com/LoweKeyFlexin/share-ct/internal/store"
	"github.com/LoweKeyFlexin/share-ct/migrations"
)

const aud = "test-aud-tag"

var clock = time.Unix(1_800_000_000, 0)

type team struct {
	key    *rsa.PrivateKey
	kid    string
	domain string
	client *http.Client
}

// newTeam serves a JWKS like <team>.cloudflareaccess.com/cdn-cgi/access/certs.
func newTeam(t *testing.T) *team {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tm := &team{key: key, kid: "kid-1"}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cdn-cgi/access/certs" {
			http.NotFound(w, r)
			return
		}
		e := big.NewInt(int64(key.PublicKey.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": tm.kid, "kty": "RSA", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(e),
		}}})
	}))
	t.Cleanup(srv.Close)
	tm.domain = strings.TrimPrefix(srv.URL, "https://")
	tm.client = srv.Client()
	return tm
}

func (tm *team) token(t *testing.T, signer *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	head := enc(map[string]string{"alg": "RS256", "kid": tm.kid, "typ": "JWT"})
	body := enc(claims)
	sum := sha256.Sum256([]byte(head + "." + body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, signer, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (tm *team) good() map[string]any {
	return map[string]any{"aud": []string{aud}, "iss": "https://" + tm.domain,
		"exp": clock.Add(time.Hour).Unix(), "nbf": clock.Add(-time.Minute).Unix(), "email": "owner@example.com"}
}

func openDB(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), migrations.FS); err != nil {
		t.Fatal(err)
	}
	return db
}

func handler(db *store.Store, v *admin.AccessVerifier) http.Handler {
	mux := http.NewServeMux()
	admin.New(slog.New(slog.NewTextHandler(io.Discard, nil)), db.DB(), v).Register(mux)
	return mux
}

func get(h http.Handler, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/admin", nil)
	if token != "" {
		req.Header.Set("Cf-Access-Jwt-Assertion", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type failTransport struct{}

func (failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, io.ErrUnexpectedEOF
}

// The shipped settings refuse a token forged with our own key that names the real team's
// issuer and AUD: it cannot verify against the team's published keys (here unreachable,
// so nothing can verify at all). Runs offline.
func TestShippedSettingsRejectAForgedToken(t *testing.T) {
	if admin.AccessTeamDomain == "" || admin.AccessAudience == "" {
		t.Fatal("shipped Access settings are empty: /admin would be off")
	}
	tm := newTeam(t)
	tm.domain = admin.AccessTeamDomain
	claims := tm.good()
	claims["aud"] = []string{admin.AccessAudience}
	v := &admin.AccessVerifier{TeamDomain: admin.AccessTeamDomain, Audience: admin.AccessAudience,
		Client: &http.Client{Transport: failTransport{}}, Now: func() time.Time { return clock }}
	if rec := get(handler(openDB(t), v), tm.token(t, tm.key, claims)); rec.Code != 404 {
		t.Fatalf("forged token with the real issuer/AUD = %d, want 404", rec.Code)
	}
}

func TestEveryBadLoginIsA404(t *testing.T) {
	tm := newTeam(t)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := &admin.AccessVerifier{TeamDomain: tm.domain, Audience: aud, Client: tm.client, Now: func() time.Time { return clock }}
	h := handler(openDB(t), v)
	mut := func(f func(c map[string]any)) map[string]any { c := tm.good(); f(c); return c }
	cases := map[string]string{
		"no token":     "",
		"garbage":      "not.a.jwt",
		"forged":       tm.token(t, other, tm.good()),
		"wrong aud":    tm.token(t, tm.key, mut(func(c map[string]any) { c["aud"] = "someone-else" })),
		"wrong issuer": tm.token(t, tm.key, mut(func(c map[string]any) { c["iss"] = "https://evil.example" })),
		"expired":      tm.token(t, tm.key, mut(func(c map[string]any) { c["exp"] = clock.Add(-2 * time.Minute).Unix() })),
		"not yet":      tm.token(t, tm.key, mut(func(c map[string]any) { c["nbf"] = clock.Add(5 * time.Minute).Unix() })),
		"no exp":       tm.token(t, tm.key, mut(func(c map[string]any) { delete(c, "exp") })),
	}
	for name, tok := range cases {
		if rec := get(h, tok); rec.Code != 404 || strings.Contains(rec.Body.String(), "<table") {
			t.Errorf("%s: got %d, want 404", name, rec.Code)
		}
	}
}

func TestSignedInListsReportsNewestFirstEscaped(t *testing.T) {
	tm := newTeam(t)
	db := openDB(t)
	now := clock
	ps := players.NewStore(db.DB(), func() time.Time { return now })
	reporter, _, err := ps.Create(context.Background(), "Reporter", "ios")
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := ps.Create(context.Background(), "<b>Bad</b>", "mac")
	if err != nil {
		t.Fatal(err)
	}
	ms := moderation.NewStore(db.DB(), func() time.Time { return now })
	if _, _, err := ms.Accept(context.Background(), reporter.ID, target.Ref, "spam"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if _, _, err := ms.Accept(context.Background(), reporter.ID, target.Ref, "harassment"); err != nil {
		t.Fatal(err)
	}
	v := &admin.AccessVerifier{TeamDomain: tm.domain, Audience: aud, Client: tm.client, Now: func() time.Time { return clock }}
	rec := get(handler(db, v), tm.token(t, tm.key, tm.good()))
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("signed in = %d %s", rec.Code, body)
	}
	if strings.Contains(body, "<b>Bad</b>") || !strings.Contains(body, "&lt;b&gt;Bad&lt;/b&gt;") {
		t.Error("player name not HTML-escaped")
	}
	if !strings.Contains(body, target.Ref) || !strings.Contains(body, "Reporter") {
		t.Error("report row missing reporter or target ref")
	}
	if strings.Index(body, "harassment") > strings.Index(body, ">spam<") {
		t.Error("not newest first")
	}
	if strings.Contains(body, "owner@example.com") {
		t.Error("page echoed the signed-in email")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
}
