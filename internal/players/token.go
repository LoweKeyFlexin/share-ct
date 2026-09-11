package players

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	tokenBytes = 32
	// tokenLen is the base64url length of tokenBytes without padding.
	tokenLen = 43
)

// newToken is 32 random bytes as unpadded base64url. Only its hash is ever stored.
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is what the players table stores: hex(sha256(token)).
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// looksLikeToken rejects anything that cannot be one of ours before the database
// sees it.
func looksLikeToken(s string) bool {
	if len(s) != tokenLen {
		return false
	}
	for _, c := range s {
		alnum := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if !alnum && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// newUUID is a random (version 4) UUID in the canonical lowercase form.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// Short is the id tail the board shows beside the name (NAME · A1B2): the app's
// CTUserIdentity.localHandle rule, the last four hex digits of the UUID, upper-cased.
func Short(id string) string {
	h := strings.ToUpper(strings.ReplaceAll(id, "-", ""))
	if len(h) <= 4 {
		return h
	}
	return h[len(h)-4:]
}
