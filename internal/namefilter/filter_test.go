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
		line("token", "IOTA"),
		line("token", "LILY"),
		// Harmless stand-ins for the generator's offline I/L-to-1 variants.
		line("token", "1ILY"),
		line("token", "L1LY"),
		line("token", "11LY"),
		line("token", "BO1ST"),
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
		"1ily", "l1ly", "11ly", "1 1 l y", "8O157",
		"superquasarx", "qu4sar", "q u a s a r",
	} {
		if !p.rejects(raw) {
			t.Errorf("should reject harmless fixture %q", raw)
		}
	}
	for _, raw := range []string{
		"blue moonlight", "teapottery", "supernova", "QUAD", "BUTTER", "BUTTON",
		"1OTA", "CAFÉ", "   ",
	} {
		if p.rejects(raw) {
			t.Errorf("should allow harmless fixture %q", raw)
		}
	}
}

func TestEmbeddedPolicyAndMalformedAsset(t *testing.T) {
	if got := AssetSHA256(); got != "847845bbcd6cd4044d85021573d192a648d05b489fba783e95312aacfe2f0b5a" {
		t.Errorf("asset revision changed to %s; compare with app manifest", got)
	}
	if len(active.whole) != 165 || len(active.token) != 760 || len(active.substring) != 43 {
		t.Errorf("unexpected embedded policy counts: whole=%d token=%d substring=%d",
			len(active.whole), len(active.token), len(active.substring))
	}
	for _, bad := range []string{"", "wrong header\n", header + "\n", header + "\nwhole\t6\tinvalid\n"} {
		if _, err := parse(bad); err == nil {
			t.Errorf("bad policy %q parsed successfully", bad)
		}
	}
}

func BenchmarkRejectsSafeMaxLength(b *testing.B) {
	const name = "MAXIMUM SAFE NAME 99"
	for i := 0; i < b.N; i++ {
		if Rejects(name) {
			b.Fatal("benchmark fixture unexpectedly rejected")
		}
	}
}
