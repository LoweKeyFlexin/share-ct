package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LoweKeyFlexin/share-ct/internal/leaderboard"
)

func newHandler(ping Pinger) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(log, ping, leaderboard.Handler())
}

func do(t *testing.T, h http.Handler, method, path string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec, rec.Body.String()
}

func TestHealthzOK(t *testing.T) {
	rec, body := do(t, newHandler(func(context.Context) error { return nil }), "GET", "/healthz")
	if rec.Code != http.StatusOK || body != "ok" {
		t.Fatalf("got %d %q, want 200 ok", rec.Code, body)
	}
}

func TestHealthzDatabaseDown(t *testing.T) {
	ping := func(context.Context) error { return errors.New("no such file") }
	rec, body := do(t, newHandler(ping), "GET", "/healthz")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(body, "no such file") {
		t.Fatalf("got %d %q, want 503 without the raw error", rec.Code, body)
	}
}

func TestIndexPage(t *testing.T) {
	rec, body := do(t, newHandler(nil), "GET", "/")
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

func TestBoardPlaceholder(t *testing.T) {
	rec, body := do(t, newHandler(nil), "GET", "/v1/board?source=pad&window=30d")
	const want = `{"source":"touch","window":"all","entries":[]}` + "\n"
	if rec.Code != http.StatusOK || body != want {
		t.Fatalf("got %d %q, want 200 %q", rec.Code, body, want)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type %q, want application/json", ct)
	}
}

func TestEverythingElseIs404JSON(t *testing.T) {
	h := newHandler(func(context.Context) error { return nil })
	for _, c := range []struct{ method, path string }{
		{"GET", "/nope"}, {"GET", "/v1"}, {"GET", "/v1/board/"}, {"POST", "/healthz"}, {"POST", "/"},
	} {
		rec, body := do(t, h, c.method, c.path)
		if rec.Code != http.StatusNotFound || body != `{"error":"not_found"}`+"\n" {
			t.Errorf("%s %s: got %d %q, want 404 JSON", c.method, c.path, rec.Code, body)
		}
	}
}

func TestPanicBecomes500(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	rec, body := do(t, New(log, nil, boom), "GET", "/v1/board")
	if rec.Code != http.StatusInternalServerError || body != `{"error":"internal"}`+"\n" {
		t.Fatalf("got %d %q, want 500 JSON", rec.Code, body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec, _ := do(t, newHandler(nil), "GET", "/")
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options: nosniff")
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("index page missing Content-Security-Policy")
	}
}
