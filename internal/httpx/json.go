// Package httpx holds the request and response helpers every handler shares.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MaxBodyBytes caps every request body (design brief §C: 4 KB). The largest honest
// body, a score submission, is well under 1 KB.
const MaxBodyBytes = 4096

// ErrorBody is the shape of every error response: {"error":"<code>"}.
type ErrorBody struct {
	Error string `json:"error"`
}

// ReasonBody is the shape of a 400 or 422: {"error":"<code>","reason":"<which rule>"}.
type ReasonBody struct {
	Error  string `json:"error"`
	Reason string `json:"reason"`
}

// WriteJSON writes v as a JSON body with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// Once the header is out an encode failure has nowhere to go but the request log's status.
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes {"error": code} with the given status.
func WriteError(w http.ResponseWriter, status int, code string) {
	WriteJSON(w, status, ErrorBody{Error: code})
}

// WriteInvalid writes 422 {"error":"invalid","reason":reason}: the body parsed but
// broke a rule, and reason names the rule.
func WriteInvalid(w http.ResponseWriter, reason string) {
	WriteJSON(w, http.StatusUnprocessableEntity, ReasonBody{Error: "invalid", Reason: reason})
}

// WriteBadRequest writes 400 {"error":"bad_request","reason":reason}.
func WriteBadRequest(w http.ResponseWriter, reason string) {
	WriteJSON(w, http.StatusBadRequest, ReasonBody{Error: "bad_request", Reason: reason})
}

// WriteRateLimited writes 429 with Retry-After in whole seconds, rounded up.
func WriteRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	secs := int(math.Ceil(retryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	WriteError(w, http.StatusTooManyRequests, "rate_limited")
}

// DecodeJSON reads at most MaxBodyBytes of the body into v. When it cannot, it answers
// the request itself (413 for an oversize body, 400 for anything unparseable or with
// trailing data) and returns false. Unknown fields are ignored so an older server still
// accepts a newer app's body.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			WriteError(w, http.StatusRequestEntityTooLarge, "body_too_large")
			return false
		}
		WriteBadRequest(w, "json")
		return false
	}
	if _, err := dec.Token(); err != io.EOF {
		WriteBadRequest(w, "json")
		return false
	}
	return true
}

// ClientIP is the address rate limits key on: Cloudflare's CF-Connecting-IP when the
// tunnel sets it, otherwise the peer address. It is never logged.
func ClientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
