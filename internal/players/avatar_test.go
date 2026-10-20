package players

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
)

func strp(s string) *string { return &s }

func TestNormalizeAvatarAcceptsOnlyTheV1Shape(t *testing.T) {
	ok := []string{
		"1:puppet:1:0",
		"1:puppet-ronin:1:6:pirate-hat",
		"1:puppet-from-the-future:9:9:a,b,c,d", // ids are shape-checked, never looked up
	}
	for _, s := range ok {
		if got := NormalizeAvatar(strp(s)); got == nil || *got != s {
			t.Errorf("%q: want stored verbatim, got %v", s, got)
		}
	}
	bad := []string{
		"", "puppet", "2:puppet:1:0", "0:puppet:1:0", "1:Puppet:1:0", "1:puppet:0:0",
		"1:puppet:1:10", "1:puppet:1:0:a,b,c,d,e", "1:puppet:1:0:", "1:<b>x</b>:1:0",
		"1:puppet:1:0:pirate hat", "1:" + strings.Repeat("a", 33) + ":1:0",
		"1:puppet:1:0:" + strings.Repeat("a", 30) + "," + strings.Repeat("b", 30),
	}
	for _, s := range bad {
		if got := NormalizeAvatar(strp(s)); got != nil {
			t.Errorf("%q: want nil, got %q", s, *got)
		}
	}
	if NormalizeAvatar(nil) != nil {
		t.Error("nil must stay nil")
	}
	if StoredAvatar(sql.NullString{}) != nil || StoredAvatar(sql.NullString{Valid: true, String: "junk"}) != nil {
		t.Error("a NULL or malformed stored column must read back as null")
	}
}

func TestSetAvatarStoresClearsAndNeverErrorsOnBadInput(t *testing.T) {
	hs := newHarness(t, stubScores{})
	id, token := hs.register("Aaron", "203.0.113.1")
	path := "/v1/players/" + id + "/avatar"

	rec, out := hs.do("PUT", path, `{"avatar":"1:puppet-oni:1:7:pirate-hat"}`, token, "")
	if rec.Code != http.StatusOK || out["avatar"] != "1:puppet-oni:1:7:pirate-hat" {
		t.Fatalf("set: %d %v", rec.Code, out)
	}
	if got := readAvatar(t, hs, id); got == nil || *got != "1:puppet-oni:1:7:pirate-hat" {
		t.Fatalf("stored %v", got)
	}

	// Invalid input is not an error to the client: it clears to null.
	for _, body := range []string{`{"avatar":"<script>"}`, `{"avatar":42}`, `{"avatar":"9:x:1:0"}`, `{}`} {
		rec, out = hs.do("PUT", path, body, token, "")
		if body == `{"avatar":42}` {
			// A wrong JSON type is unparseable, the shared 400 every route gives; nothing stored.
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: %d", body, rec.Code)
			}
			continue
		}
		if rec.Code != http.StatusOK || out["avatar"] != nil {
			t.Fatalf("%s: want 200 null, got %d %v", body, rec.Code, out)
		}
		if got := readAvatar(t, hs, id); got != nil {
			t.Fatalf("%s: stored %q, want NULL", body, *got)
		}
	}

	rec, out = hs.do("PUT", path, `{"avatar":"1:puppet:1:0"}`, token, "")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	rec, out = hs.do("PUT", path, `{"avatar":null}`, token, "")
	if rec.Code != http.StatusOK || out["avatar"] != nil || readAvatar(t, hs, id) != nil {
		t.Fatalf("explicit null must clear: %d %v", rec.Code, out)
	}
}

func TestSetAvatarIsOwnerOnly(t *testing.T) {
	hs := newHarness(t, stubScores{})
	a, _ := hs.register("Alpha", "203.0.113.1")
	_, tokenB := hs.register("Bravo", "203.0.113.2")
	if rec, _ := hs.do("PUT", "/v1/players/"+a+"/avatar", `{"avatar":"1:puppet:1:0"}`, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec, _ := hs.do("PUT", "/v1/players/"+a+"/avatar", `{"avatar":"1:puppet:1:0"}`, tokenB, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("other player's token: %d", rec.Code)
	}
	if readAvatar(t, hs, a) != nil {
		t.Fatal("a refused request stored an avatar")
	}
}

// A player who never sends an avatar (every older app) keeps NULL, and renaming does not
// touch it.
func TestOlderClientsAreUnaffected(t *testing.T) {
	hs := newHarness(t, stubScores{})
	id, token := hs.register("Old", "203.0.113.1")
	if readAvatar(t, hs, id) != nil {
		t.Fatal("a new player must start with no avatar")
	}
	if rec, _ := hs.do("PATCH", "/v1/players/"+id, `{"display_name":"Older"}`, token, ""); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if readAvatar(t, hs, id) != nil {
		t.Fatal("rename changed the avatar")
	}
}

func readAvatar(t *testing.T, hs *harness, id string) *string {
	t.Helper()
	var v sql.NullString
	if err := hs.db.DB().QueryRow(`SELECT avatar FROM players WHERE id = ?`, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if !v.Valid {
		return nil
	}
	return &v.String
}
