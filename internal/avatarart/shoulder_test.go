package avatarart

import (
	"strings"
	"testing"
)

func TestShoulderCatUsesBareHeadHeightAndSignedTorsoSeat(t *testing.T) {
	p := &posed{
		r:        &rig{boneIndex: map[string]int{"torso": 0}, slots: []slot{{ID: "head-fill", ImageWidth: 20, ImageHeight: 20}}},
		toCanvas: []Affine{{1, 0, 0, 1, 10, 20}}, boneToCanvas: []Affine{{1, 0, 0, -1, 0, 0}},
		bounds: Rect{0, 0, 100, 200},
	}
	got := shoulderCat(p, Rect{10, 0, 90, 80})
	// Height 200 gives scale 290. Seat x=20-.09*200-10, y=20+.13*200.
	if !strings.Contains(got, `matrix(-290 0 0 -290 -8 46)`) {
		t.Fatal("shoulder seat differs from the native idle placement")
	}
	for _, color := range []string{"#14121a", "#6e6878", "#8fe36b", "#08070c"} {
		if !strings.Contains(got, color) {
			t.Errorf("missing fixed cat paint %s", color)
		}
	}
	if strings.Contains(got, "var(--") {
		t.Fatal("body/gear tint must not recolor the cat")
	}
	delete(p.r.boneIndex, "torso")
	if shoulderCat(p, Rect{10, 0, 90, 80}) != "" {
		t.Fatal("unrecognized rig must not invent a seat")
	}
}
