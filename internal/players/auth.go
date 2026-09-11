package players

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/LoweKeyFlexin/share-ct/internal/httpx"
)

type ctxKey struct{}

// FromContext returns the player RequireBearer authenticated for this request.
func FromContext(ctx context.Context) (Player, bool) {
	p, ok := ctx.Value(ctxKey{}).(Player)
	return p, ok
}

// RequireBearer authenticates "Authorization: Bearer <token>" and hands the player to
// next through the context; a missing, malformed or unknown token is 401. The token is
// never logged.
func (m *Module) RequireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			unauthorized(w)
			return
		}
		p, err := m.store.Authenticate(r.Context(), token)
		if errors.Is(err, ErrNotFound) {
			unauthorized(w)
			return
		}
		if err != nil {
			m.log.Error("authenticate", "error", err.Error())
			httpx.WriteError(w, http.StatusInternalServerError, "internal")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

// bearerToken extracts the token from an Authorization header (scheme case-insensitive)
// and checks its shape before the database is asked.
func bearerToken(header string) (string, bool) {
	scheme, token, found := strings.Cut(strings.TrimSpace(header), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, looksLikeToken(token)
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="share-ct"`)
	httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
}

// requireOwner lets an authenticated player act only on its own {id}; another
// player's id is 403.
func requireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := FromContext(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		if r.PathValue("id") != p.ID {
			httpx.WriteError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}
