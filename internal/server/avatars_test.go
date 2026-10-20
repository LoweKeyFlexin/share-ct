package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAvatarArtIsServedAtItsContentAddressOnly(t *testing.T) {
	h, _ := newApp(t)
	rec, body := do(t, h, "GET", avatarsURL)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: %d", avatarsURL, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("cache control %q", cc)
	}
	var art struct {
		Format   string `json:"format"`
		Fighters map[string]struct {
			Palette [][3]int  `json:"palette"`
			Box     []float64 `json:"box"`
			Parts   []string  `json:"parts"`
			Head    []string  `json:"head"`
		} `json:"fighters"`
	}
	if err := json.Unmarshal([]byte(body), &art); err != nil {
		t.Fatal(err)
	}
	// The default look (no, bad or unknown descriptor) is Standard: it must be drawable.
	if std, ok := art.Fighters["puppet"]; !ok || len(std.Palette) == 0 || len(std.Head) == 0 || len(std.Box) != 3 {
		t.Fatalf("Standard missing or incomplete")
	}
	// Vector only: no raster image of any kind rides in the art.
	for _, banned := range []string{"<image", "data:", ".png", "href="} {
		if strings.Contains(body, banned) {
			t.Errorf("art contains %q", banned)
		}
	}
	for _, path := range []string{"/assets/board-avatars.json", "/assets/board-avatars-000000000000.json", "/assets/x"} {
		if rec, _ := do(t, h, "GET", path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, rec.Code)
		}
	}
}

func TestPageLoadsTheCurrentAvatarArt(t *testing.T) {
	h, _ := newApp(t)
	_, page := do(t, h, "GET", "/")
	if !strings.Contains(page, "'"+avatarsURL+"'") || strings.Contains(page, "__AVATARS_URL__") {
		t.Error("the page script must carry the current avatar art URL")
	}
}
