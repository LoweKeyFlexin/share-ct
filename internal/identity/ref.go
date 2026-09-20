// Package identity mints public, non-credential player and report references.
package identity

import (
	"crypto/rand"
	"encoding/hex"
)

// NewRef returns 128 bits from the operating system's cryptographic random source.
func NewRef() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ValidRef accepts only our canonical lowercase public reference representation.
func ValidRef(ref string) bool {
	if len(ref) != 32 {
		return false
	}
	for _, c := range ref {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
