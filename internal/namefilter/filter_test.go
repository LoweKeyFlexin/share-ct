package namefilter

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// These harmless fixture words exercise every matching mode without publishing
// the reviewed policy's plaintext terms in this public repository.
func fixturePolicy(t *testing.T) policy {
	t.Helper()
	line := func(mode, word string) string {
		key := digest(mode, word)
		return fmt.Sprintf("%s\t%d\t%s", mode, key.length, hex.EncodeToString(key.hash[:]))
	}
	data := strings.Join([]string{
		header,
		line("whole", "BLUE MOON"),
		line("token", "TEAPOT"),
		line("substring", "QUASAR"),
	}, "\n") + "\n"
	p, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPortableDigestVector(t *testing.T) {
	key := digest("token", "TEAPOT")
	if got, want := hex.EncodeToString(key.hash[:]), "833822fbf0764d0523f2cf8822b8c08b29789be660c71ed092a665759d7fdc4b"; got != want {
		t.Errorf("digest = %s, want %s", got, want)
	}
	if got, ok := normalizeASCII("  tEa   pot  "); !ok || got != "TEA POT" {
		t.Errorf("normalization = %q, %t", got, ok)
	}
}

func TestMatchingModesAndEvasions(t *testing.T) {
	p := fixturePolicy(t)
	for _, raw := range []string{
		"blue moon", " TEAPOT ", "a teapot", "t e a p o t", "tea p0t",
		"superquasarx", "qu4sar", "q u a s a r",
	} {
		if !p.rejects(raw) {
			t.Errorf("should reject harmless fixture %q", raw)
		}
	}
	for _, raw := range []string{
		"blue moonlight", "teapottery", "supernova", "QUAD", "BUTTER", "BUTTON",
		"CAFÉ", "   ",
	} {
		if p.rejects(raw) {
			t.Errorf("should allow harmless fixture %q", raw)
		}
	}
}

func TestEmbeddedPolicyAndMalformedAsset(t *testing.T) {
	if got := AssetSHA256(); got != "fa64c27413bd1557b9b4dabfab08a4fea61da8ce9e09ccb7f6bbfa9a790a8688" {
		t.Errorf("asset revision changed to %s; compare with app manifest", got)
	}
	if len(active.whole) != 29 || len(active.token) != 381 || len(active.substring) != 25 {
		t.Errorf("unexpected embedded policy counts: whole=%d token=%d substring=%d",
			len(active.whole), len(active.token), len(active.substring))
	}
	for _, bad := range []string{"", "wrong header\n", header + "\n", header + "\nwhole\t6\tinvalid\n"} {
		if _, err := parse(bad); err == nil {
			t.Errorf("bad policy %q parsed successfully", bad)
		}
	}
}
