package players

import "testing"

func TestValidateDisplayName(t *testing.T) {
	cases := []struct{ raw, want, reason string }{
		{"Aaron", "Aaron", ""},
		{"  Aaron  ", "Aaron", ""},
		{"\tAaron\t", "Aaron", ""},
		{"abc", "abc", ""},
		{"ab", "", "name_too_short"},
		{"", "", "name_too_short"},
		{"   ", "", "name_too_short"},
		{"123456789012345", "123456789012345", ""},
		{"1234567890123456", "", "name_too_long"},
		{"José Ñandú", "José Ñandú", ""},
		{"Aaron!", "", "name_invalid_characters"},
		{"Aaron_1", "", "name_invalid_characters"},
		{"😀😀😀", "", "name_invalid_characters"},
		{"a\nb\nc", "", "name_invalid_characters"},
		{"admin", "", "name_reserved"},
		{"No   Name", "", "name_reserved"},
		{"Fighter  CT", "", "name_reserved"},
		{"Support", "", "name_reserved"},
		{"Supporter", "Supporter", ""},
	}
	for _, c := range cases {
		got, reason := ValidateDisplayName(c.raw)
		if got != c.want || reason != c.reason {
			t.Errorf("ValidateDisplayName(%q) = %q, %q; want %q, %q", c.raw, got, reason, c.want, c.reason)
		}
	}
}

func TestNormalize(t *testing.T) {
	if got := Normalize("  no \t name "); got != "NO NAME" {
		t.Errorf("got %q", got)
	}
}

func TestValidPlatform(t *testing.T) {
	for _, p := range []string{"ios", "mac", "windows", "android"} {
		if !ValidPlatform(p) {
			t.Errorf("%s must be valid", p)
		}
	}
	for _, p := range []string{"", "iOS", "linux", "web"} {
		if ValidPlatform(p) {
			t.Errorf("%q must be invalid", p)
		}
	}
}

func TestShort(t *testing.T) {
	if got := Short("6f1a2b3c-4d5e-4f60-8a7b-9c0d1e2f3a4b"); got != "3A4B" {
		t.Errorf("got %q, want 3A4B", got)
	}
}

func TestTokenShape(t *testing.T) {
	tok, err := newToken()
	if err != nil || !looksLikeToken(tok) {
		t.Fatalf("newToken() = %q, %v", tok, err)
	}
	if looksLikeToken(tok[:tokenLen-1]) || looksLikeToken(tok[:tokenLen-1]+"=") || looksLikeToken(tok[:tokenLen-1]+"+") {
		t.Error("a token of the wrong length or alphabet must be rejected before the lookup")
	}
	if hashToken(tok) == tok || len(hashToken(tok)) != 64 {
		t.Error("hashToken must be a 64-hex sha256")
	}
}
