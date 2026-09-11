package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type body struct {
	Name string `json:"name"`
}

func decode(t *testing.T, raw string) (*httptest.ResponseRecorder, body, bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", strings.NewReader(raw))
	var v body
	ok := DecodeJSON(rec, r, &v)
	return rec, v, ok
}

func TestDecodeJSON(t *testing.T) {
	if rec, v, ok := decode(t, `{"name":"a","extra":1}`); !ok || v.Name != "a" || rec.Code != 200 {
		t.Errorf("valid body with an unknown field: ok=%v v=%+v code=%d", ok, v, rec.Code)
	}
	for name, raw := range map[string]string{
		"empty": "", "garbage": "nope", "trailing": `{"name":"a"} {"name":"b"}`, "array": `[1]`,
	} {
		if rec, _, ok := decode(t, raw); ok || rec.Code != http.StatusBadRequest {
			t.Errorf("%s: ok=%v code=%d, want 400", name, ok, rec.Code)
		}
	}
	huge := `{"name":"` + strings.Repeat("x", MaxBodyBytes) + `"}`
	if rec, _, ok := decode(t, huge); ok || rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize: ok=%v code=%d, want 413", ok, rec.Code)
	}
}

func decodeStrict(t *testing.T, raw string) (*httptest.ResponseRecorder, body, bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", strings.NewReader(raw))
	var v body
	ok := DecodeJSONStrict(rec, r, &v)
	return rec, v, ok
}

func TestDecodeJSONStrict(t *testing.T) {
	if rec, v, ok := decodeStrict(t, `{"name":"a"}`); !ok || v.Name != "a" || rec.Code != 200 {
		t.Errorf("declared body: ok=%v v=%+v code=%d", ok, v, rec.Code)
	}
	want := `{"error":"invalid","reason":"unknown_field"}` + "\n"
	for name, raw := range map[string]string{
		"extra key": `{"name":"a","latitude":51.5}`, "extra key first": `{"latitude":51.5,"name":"a"}`, "only an extra key": `{"latitude":51.5}`,
	} {
		if rec, _, ok := decodeStrict(t, raw); ok || rec.Code != http.StatusUnprocessableEntity || rec.Body.String() != want {
			t.Errorf("%s: ok=%v code=%d body=%q, want 422 %q", name, ok, rec.Code, rec.Body.String(), want)
		}
	}
	// Malformed and oversize bodies keep their own answers; the field check comes after.
	for name, raw := range map[string]string{"garbage": "nope", "trailing": `{"name":"a"} {"name":"b"}`, "array": `[1]`} {
		if rec, _, ok := decodeStrict(t, raw); ok || rec.Code != http.StatusBadRequest {
			t.Errorf("%s: ok=%v code=%d, want 400", name, ok, rec.Code)
		}
	}
	huge := `{"name":"a","pad":"` + strings.Repeat("x", MaxBodyBytes) + `"}`
	if rec, _, ok := decodeStrict(t, huge); ok || rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize with an extra key: ok=%v code=%d, want 413", ok, rec.Code)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.7:5555"
	if got := ClientIP(r); got != "10.0.0.7" {
		t.Errorf("peer address: %q", got)
	}
	r.Header.Set("CF-Connecting-IP", " 203.0.113.9 ")
	if got := ClientIP(r); got != "203.0.113.9" {
		t.Errorf("CF-Connecting-IP: %q", got)
	}
}

func TestWriteRateLimited(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteRateLimited(rec, 1500*time.Millisecond)
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "2" {
		t.Errorf("got %d Retry-After %q, want 429 and 2", rec.Code, rec.Header().Get("Retry-After"))
	}
	rec = httptest.NewRecorder()
	WriteRateLimited(rec, 0)
	if rec.Header().Get("Retry-After") != "1" {
		t.Errorf("zero wait must still say 1, got %q", rec.Header().Get("Retry-After"))
	}
}
