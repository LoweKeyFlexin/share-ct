package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/store"
	"github.com/LoweKeyFlexin/share-ct/migrations"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newApp is the real stack on a temp database: what main.go serves, minus the listener.
func newApp(t *testing.T) (http.Handler, *store.Store) {
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
	return New(quiet(), db, time.Now), db
}

func do(t *testing.T, h http.Handler, method, path string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec, rec.Body.String()
}

func TestHealthzOK(t *testing.T) {
	h, _ := newApp(t)
	rec, body := do(t, h, "GET", "/healthz")
	if rec.Code != http.StatusOK || body != "ok" {
		t.Fatalf("got %d %q, want 200 ok", rec.Code, body)
	}
}

func TestHealthzDatabaseDown(t *testing.T) {
	h, db := newApp(t)
	db.Close()
	rec, body := do(t, h, "GET", "/healthz")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(body, "closed") {
		t.Fatalf("got %d %q, want 503 without the raw error", rec.Code, body)
	}
}

func TestIndexPage(t *testing.T) {
	h, _ := newApp(t)
	rec, body := do(t, h, "GET", "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type %q, want text/html", ct)
	}
	for _, want := range []string{"Share CT · BETA · MAY GO DOWN", "Reaction leaderboard coming soon"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "<script") || strings.Contains(body, "http") {
		t.Error("page must have no scripts and no external assets")
	}
}

func TestEverythingElseIs404JSON(t *testing.T) {
	h, _ := newApp(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/nope"}, {"GET", "/v1"}, {"GET", "/v1/board/"}, {"POST", "/healthz"}, {"POST", "/"}, {"GET", "/v1/players"},
	} {
		rec, body := do(t, h, c.method, c.path)
		if rec.Code != http.StatusNotFound || body != `{"error":"not_found"}`+"\n" {
			t.Errorf("%s %s: got %d %q, want 404 JSON", c.method, c.path, rec.Code, body)
		}
	}
}

func TestPanicBecomes500(t *testing.T) {
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	rec, body := do(t, recoverer(quiet(), boom), "GET", "/v1/board")
	if rec.Code != http.StatusInternalServerError || body != `{"error":"internal"}`+"\n" {
		t.Fatalf("got %d %q, want 500 JSON", rec.Code, body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h, _ := newApp(t)
	rec, _ := do(t, h, "GET", "/")
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options: nosniff")
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("index page missing Content-Security-Policy")
	}
}

// TestRoundTrip is the CI smoke's shape through the real stack: register, submit a
// known trial, read it back on the board and the profile, erase, gone.
func TestRoundTrip(t *testing.T) {
	h, _ := newApp(t)
	call := func(method, path, body, token string) (int, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		r := httptest.NewRequest(method, path, rd)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out map[string]any
		if rec.Body.Len() > 0 {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("%s %s: %d %q", method, path, rec.Code, rec.Body.String())
			}
		}
		return rec.Code, out
	}

	code, reg := call("POST", "/v1/players", `{"display_name":"Aaron","platform":"ios"}`, "")
	if code != 201 {
		t.Fatalf("register: %d %v", code, reg)
	}
	id, token := reg["player_id"].(string), reg["token"].(string)

	const body = `{"client_id":"smoke-1","source":"touch","best_ms":200,"avg_ms":205,"accuracy":1,"score":2100,
	  "attempts_ms":[200,205,210],"misfires":0,"app_build":"2214","platform":"ios","device_model":"iPhone17,2","device_label":"Touch"}`
	if code, out := call("POST", "/v1/scores", body, token); code != 201 || out["rank"] != float64(1) {
		t.Fatalf("submit: %d %v", code, out)
	}
	if code, out := call("POST", "/v1/scores", strings.Replace(body, `"score":2100`, `"score":2101`, 1), token); code != 422 || out["reason"] != "score_mismatch" {
		t.Fatalf("mismatched score: %d %v", code, out)
	}
	code, board := call("GET", "/v1/board?source=touch", "", "")
	entries := board["entries"].([]any)
	if code != 200 || len(entries) != 1 {
		t.Fatalf("board: %d %v", code, board)
	}
	if e := entries[0].(map[string]any); e["score"] != float64(2100) || e["tier"] != "DIAMOND" || e["display_name"] != "Aaron" {
		t.Errorf("entry %v", e)
	}
	if code, prof := call("GET", "/v1/players/"+id, "", ""); code != 200 || prof["submissions"] != float64(1) {
		t.Errorf("profile: %d %v", code, prof)
	}
	if code, _ := call("DELETE", "/v1/players/"+id, "", token); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if _, board := call("GET", "/v1/board?source=touch", "", ""); len(board["entries"].([]any)) != 0 {
		t.Errorf("board after delete: %v", board)
	}
}
