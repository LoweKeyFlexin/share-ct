// Package players owns the identity shared by every Share CT feature: an opaque
// per-device player id with a bearer token, minted by POST /v1/players (design brief
// §B.2). Later features (layouts, combos) are rows owned by a player_id; nothing
// per-feature is minted.
package players

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxNameLen and minNameLen are the app's CTUserIdentity.usernameMaxLen and the
// validateUsername floor.
const (
	MaxNameLen = 15
	minNameLen = 3
)

// reservedNormalized is the app's CTUserIdentity.reservedNormalized, verbatim.
var reservedNormalized = map[string]bool{
	"ADMIN": true, "MODERATOR": true, "SYSTEM": true, "ANONYMOUS": true, "NONAME": true, "NO NAME": true,
	"CONTROLLER TESTER": true, "FIGHTER CT": true, "CT": true, "NULL": true, "UNDEFINED": true, "SUPPORT": true,
}

var platforms = map[string]bool{"ios": true, "mac": true, "windows": true, "android": true}

// ValidPlatform reports whether p is one of ios, mac, windows, android.
func ValidPlatform(p string) bool { return platforms[p] }

// isSwiftWhitespace mirrors CharacterSet.whitespaces: Unicode Zs plus tab.
func isSwiftWhitespace(r rune) bool { return r == '\t' || unicode.Is(unicode.Zs, r) }

// Normalize is CTUserIdentity.normalizedUsername: upper-cased, runs of whitespace
// collapsed to one space, trimmed. It is the reserved-name key and the future
// collision key.
func Normalize(raw string) string {
	return strings.Join(strings.FieldsFunc(strings.ToUpper(raw), unicode.IsSpace), " ")
}

// ValidateDisplayName ports CTUserIdentity.validateUsername: trimmed, 3 to 15
// characters, letters, marks, digits and spaces only, not a reserved handle. It returns
// the trimmed name and "" on success, otherwise the 422 reason.
//
// Length counts code points where the app counts grapheme clusters; the two agree for
// every name the character rule admits except combining sequences, which the server
// counts longer (and so may reject one character earlier).
func ValidateDisplayName(raw string) (string, string) {
	trimmed := strings.TrimFunc(raw, isSwiftWhitespace)
	n := utf8.RuneCountInString(trimmed)
	if n < minNameLen {
		return "", "name_too_short"
	}
	if n > MaxNameLen {
		return "", "name_too_long"
	}
	for _, r := range trimmed {
		if !(unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r) || r == ' ') {
			return "", "name_invalid_characters"
		}
	}
	if reservedNormalized[Normalize(trimmed)] {
		return "", "name_reserved"
	}
	return trimmed, ""
}
