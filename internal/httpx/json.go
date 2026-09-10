// Package httpx holds the response helpers every handler shares.
package httpx

import (
	"encoding/json"
	"net/http"
)

// ErrorBody is the shape of every error response: {"error":"<code>"}.
type ErrorBody struct {
	Error string `json:"error"`
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
