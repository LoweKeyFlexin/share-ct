package players

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"

	"github.com/LoweKeyFlexin/share-ct/internal/httpx"
)

// MaxAvatarBytes caps a stored avatar descriptor. The app's largest is about 60 bytes.
const MaxAvatarBytes = 64

// avatarV1 is the version-1 descriptor: version:fighter:outfit:color[:gear[,gear...]].
// Ids are lowercase [a-z0-9-] of at most 32 characters; outfit is 1-9, color 0-9, and
// there are at most 4 gear ids. Only the SHAPE is checked, never whether an id exists,
// so newer app content works without a server change; the app draws an unknown fighter
// as its default and drops unknown gear. Nothing in this grammar can carry free text.
var avatarV1 = regexp.MustCompile(`^1:[a-z0-9-]{1,32}:[1-9]:[0-9](:[a-z0-9-]{1,32}(,[a-z0-9-]{1,32}){0,3})?$`)

// NormalizeAvatar returns the descriptor to store, or nil. Anything that is not a valid
// version-1 descriptor (wrong shape, unknown version, oversize) becomes nil: "no avatar",
// never an error, so a newer or broken client degrades to the default picture.
func NormalizeAvatar(raw *string) *string {
	if raw == nil || len(*raw) > MaxAvatarBytes || !avatarV1.MatchString(*raw) {
		return nil
	}
	v := *raw
	return &v
}

type avatarRequest struct {
	Avatar *string `json:"avatar"`
}

type avatarResponse struct {
	Avatar *string `json:"avatar"`
}

// setAvatar is PUT /v1/players/{id}/avatar (bearer, own id only). It stores the
// normalized descriptor and answers 200 with what was stored, null included. An invalid
// descriptor clears the avatar rather than failing.
func (m *Module) setAvatar(w http.ResponseWriter, r *http.Request) {
	p, _ := FromContext(r.Context())
	var req avatarRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	stored := NormalizeAvatar(req.Avatar)
	if err := m.store.SetAvatar(r.Context(), p.ID, stored); err != nil {
		m.fail(w, "set avatar", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, avatarResponse{Avatar: stored})
}

// SetAvatar stores the descriptor (nil clears it) and bumps last_seen_at.
func (s *Store) SetAvatar(ctx context.Context, id string, avatar *string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE players SET avatar = ?, last_seen_at = ? WHERE id = ?`,
		avatar, s.now().Unix(), id)
	return affectedOne(res, err)
}

// StoredAvatar turns a scanned column into the wire value. It re-checks the shape, so a
// row written by anything other than SetAvatar still reaches readers only as a valid
// descriptor or null.
func StoredAvatar(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return NormalizeAvatar(&v.String)
}
