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
//
// 15 -> 20 on 2026-09-11 (Aaron: "if we need to make the max username longer, we can,
// 15 characters is arbitrary"). Verified at 20 against the public board page before
// shipping: a 20-character name still sets on one line on a 390pt phone viewport, and
// the row wraps rather than clipping past that.
const (
	MaxNameLen = 20
	minNameLen = 3
)

// AnonymousName is what a player with no name of their own is CALLED, everywhere a name
// is shown. It is never what is STORED: the stored value for such a player is the empty
// string, and this word is substituted at every read boundary by Display.
//
// That split is the whole point, and it was chosen over two alternatives that look
// simpler (controller-tester-fgc#6112, Aaron's ruling 2026-09-11):
//
//   - Storing "NO NAME" as an ordinary name would mean taking it OFF reservedNormalized,
//     because registration is one endpoint with one field and no privileged path - the
//     server cannot tell the app's default from a player who typed the same words. Any
//     player could then become indistinguishable from every unnamed player on a shared
//     board, which is precisely what the reservation protects against.
//   - Leaving it reserved and refusing to register the nameless means a player who only
//     wants to post a score must name themselves first.
//
// Storing ABSENCE removes the contradiction instead of relocating it: there is nothing
// for a player to type that collides, because "" is not a name any validator will accept
// (minNameLen is 3), and the word itself stays reserved so it cannot be claimed either.
const AnonymousName = "NO NAME"

// Display is the stored display name as a reader should see it. Apply it at EVERY point
// a stored name reaches JSON or a page; the empty string is the anonymous sentinel and
// must never reach a reader as an empty string.
func Display(stored string) string {
	if stored == "" {
		return AnonymousName
	}
	return stored
}

// reservedNormalized is the app's CTUserIdentity.reservedNormalized, verbatim. Keep the
// two in step.
//
// NO NAME and NONAME STAY here deliberately - see AnonymousName for why reserving the
// word and using it as the default are compatible only because the default is stored as
// absence rather than as that word.
//
// Two entries could never fire while MaxNameLen was 15: "CONTROLLER TESTER" (17 runes)
// tripped the length check first, and "CT" (2 runes) still does. Raising the limit to 20
// brings the first one to life; the second remains unreachable and is kept only so the
// list reads as the app's list verbatim. Both are refused either way.
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
// ValidateOptionalDisplayName is ValidateDisplayName with one difference: a name that is
// empty or nothing but whitespace is ANONYMOUS rather than an error, and returns ("", "").
// Store that empty string; render it with Display.
//
// Use this wherever a player sets their own name - registration and rename both. Aaron,
// 2026-09-11: "empty names just register with NO NAME" and "entering nothing on the
// keyboard for Entry should just default the player back to NO NAME". The second half is
// why rename uses it too: clearing the field is a way to go back to anonymous, not a 422.
func ValidateOptionalDisplayName(raw string) (string, string) {
	if strings.TrimFunc(raw, isSwiftWhitespace) == "" {
		return "", ""
	}
	return ValidateDisplayName(raw)
}

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
