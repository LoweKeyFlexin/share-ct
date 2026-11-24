package avatarart

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestBakeReferenceGearVectorAndArtwork(t *testing.T) {
	in := writeFixture(t)
	refDir := filepath.Join(t.TempDir(), "ReferenceGear")
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(refDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Catalog covering one tintable vector head item, one multi-part vector head item,
	// one artwork head item (chef-hat black softening), and one artwork shoulder item.
	write("catalog.json", `{
	  "banana-guard": {"width":40,"height":30,"components":{"art":{"x":0,"y":0,"width":40,"height":30}}},
	  "witching-hour": {"width":40,"height":30,"components":{
	    "art":{"x":0,"y":0,"width":40,"height":24},
	    "rear":{"x":0,"y":12,"width":40,"height":12},
	    "bat":{"x":4,"y":14,"width":10,"height":12}
	  }},
	  "chef-hat": {"width":40,"height":40,"components":{"art":{"x":0,"y":0,"width":40,"height":40}}},
	  "axolotl": {"width":40,"height":30,"components":{"art":{"x":0,"y":0,"width":40,"height":30}}}
	}`)
	write("fits.json", `{"schema_version":1,"fits":{"puppet":{
	  "banana-guard":[1,0,0,-1,-20,45],
	  "witching-hour":[1,0,0,-1,-20,50],
	  "witching-hour-rear":[1,0,0,-1,-20,50],
	  "witching-hour-bat":[1,0,0,-1,-20,50],
	  "chef-hat":[1,0,0,-1,-20,55],
	  "axolotl":[0.5,0,0,-0.5,-30,100]
	}}}`)

	// Note: fixture rig has "root" and "head", no "torso", so axolotl (torso bone) skips cleanly
	// while head items resolve on "head". Verify both cases and then add a torso test below.
	vecSlot := func(w, h int, fill, stroke string) string {
		wf, hf := itoa(w), itoa(h)
		return `{"size":[` + wf + `,` + hf + `],"origin":[0,0],` +
			`"silhouette":[0,0,0,1,` + wf + `,0,1,` + wf + `,-` + hf + `,1,0,-` + hf + `,3],` +
			`"layers":[` +
			`{"fill":"#000000","opacity":0},` +
			`{"fill":"` + fill + `","opacity":1,"shape":[0,4,-4,1,36,-4,1,36,-26,1,4,-26,3]},` +
			`{"lattice":[[4,-4,36,-4,36,-26,4,-26,4,-4]],"stroke":"` + fill + `","width":6},` +
			`{"fill":"` + fill + `","opacity":1,"shape":[0,4,-4,1,36,-4,1,36,-26,1,4,-26,3]},` +
			`{"lattice":[[6,-6,34,-6,34,-24]],"stroke":"` + stroke + `","width":3}` +
			`]}`
	}
	write("banana-guard/parts.vec.json", `{"format":"puppet-part-vectors","schema_version":1,"raster_pixels_per_design_unit":1,"slots":{`+
		`"art.png":`+vecSlot(40, 30, "#1A1325", "#FFDC38")+`,`+
		`"neutral.png":`+vecSlot(40, 30, "#001600", "#00FF00")+`}}`)
	write("witching-hour/parts.vec.json", `{"format":"puppet-part-vectors","schema_version":1,"raster_pixels_per_design_unit":1,"slots":{`+
		`"art.png":`+vecSlot(40, 24, "#1A1325", "#CE78FF")+`,"neutral.png":`+vecSlot(40, 24, "#001600", "#00FF00")+`,`+
		`"rear.png":`+vecSlot(40, 12, "#1A1325", "#CE78FF")+`,"rear-neutral.png":`+vecSlot(40, 12, "#001600", "#00FF00")+`,`+
		`"bat.png":`+vecSlot(10, 12, "#1A1325", "#CE78FF")+`,"bat-neutral.png":`+vecSlot(10, 12, "#001600", "#00FF00")+`}}`)

	// Synthetic chef-hat PNG: white border around a pure black interior (must soften to #383B42).
	chef := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	for y := 4; y < 36; y++ {
		for x := 4; x < 36; x++ {
			if x < 9 || x >= 31 || y < 9 || y >= 31 {
				chef.SetNRGBA(x, y, color.NRGBA{220, 220, 224, 255})
			} else {
				chef.SetNRGBA(x, y, color.NRGBA{0, 0, 0, 255})
			}
		}
	}
	writePNG(t, filepath.Join(refDir, "chef-hat/art.png"), chef)

	// Synthetic axolotl PNG: neon pink outline around dark body.
	axo := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for y := 3; y < 27; y++ {
		for x := 3; x < 37; x++ {
			if x < 8 || x >= 32 || y < 8 || y >= 22 {
				axo.SetNRGBA(x, y, color.NRGBA{254, 86, 104, 255})
			} else {
				axo.SetNRGBA(x, y, color.NRGBA{10, 14, 18, 255})
			}
		}
	}
	writePNG(t, filepath.Join(refDir, "axolotl/art.png"), axo)

	in.ReferenceGear = refDir
	first, err := BakeJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BakeJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("ReferenceGear bake must be byte-deterministic")
	}
	for _, banned := range []string{"<image", "data:", ".png", "href="} {
		if strings.Contains(string(first), banned) {
			t.Fatalf("baked JSON contains banned raster token %q", banned)
		}
	}

	var out Output
	if err := json.Unmarshal(first, &out); err != nil {
		t.Fatal(err)
	}
	bg, ok := out.Accessories["banana-guard"]
	if !ok || bg.Slot != "head" || !bg.Tintable || !bg.Tall || len(bg.Components) != 1 {
		t.Fatalf("banana-guard accessory wrong: %+v", bg)
	}
	if !strings.Contains(bg.Components[0].SVG, "var(--rh0,#1A1325)") || !strings.Contains(bg.Components[0].SVG, "var(--rh1,#FFDC38)") {
		t.Errorf("banana-guard missing tintable authored fallback vars: %s", bg.Components[0].SVG)
	}
	if strings.Contains(bg.Components[0].SVG, `fill-opacity="0"`) {
		t.Errorf("opacity-0 layer should be omitted: %s", bg.Components[0].SVG)
	}
	// Redundant L1 casing fill before same-colour stroke + identical L3 fill should be deduplicated to 1 fill path.
	if got := strings.Count(bg.Components[0].SVG, `style="fill:var(--rh0,#1A1325)"`); got != 1 {
		t.Errorf("expected 1 deduplicated casing fill path, got %d in %s", got, bg.Components[0].SVG)
	}

	wh, ok := out.Accessories["witching-hour"]
	if !ok || wh.Tintable || len(wh.Components) != 3 {
		t.Fatalf("witching-hour accessory wrong: %+v", wh)
	}
	if wh.Components[0].Layer != "head" || wh.Components[1].Layer != "behind" || wh.Components[2].Layer != "head" {
		t.Errorf("witching-hour layers wrong: %+v", wh.Components)
	}
	if strings.Contains(wh.Components[0].SVG, "var(--") {
		t.Errorf("non-tintable witching-hour must keep literal colours: %s", wh.Components[0].SVG)
	}

	ch, ok := out.Accessories["chef-hat"]
	if !ok || ch.Tintable || len(ch.Components) != 1 {
		t.Fatalf("chef-hat accessory wrong: %+v", ch)
	}
	if !strings.Contains(ch.Components[0].SVG, "#383B42") || !strings.Contains(ch.Components[0].SVG, "#DCDCE0") {
		t.Errorf("chef-hat should soften black interior to #383B42 and keep white border: %s", ch.Components[0].SVG)
	}

	ax, ok := out.Accessories["axolotl"]
	if !ok || ax.Slot != "shoulder" || ax.Tintable || !ax.Companion || ax.Components[0].Layer != "behind_body" {
		t.Fatalf("axolotl metadata wrong: %+v", ax)
	}
	if !strings.Contains(ax.Components[0].SVG, "#FE5668") || !strings.Contains(ax.Components[0].SVG, "#0A0E12") {
		t.Errorf("axolotl colours wrong: %s", ax.Components[0].SVG)
	}

	f := out.Fighters["puppet"]
	for _, compID := range []string{"banana-guard", "witching-hour", "witching-hour-rear", "witching-hour-bat", "chef-hat"} {
		if _, ok := f.RefFits[compID]; !ok {
			t.Errorf("puppet missing RefFit for %s", compID)
		}
	}
	if _, ok := f.RefFits["axolotl"]; ok {
		t.Error("axolotl binds to torso bone which does not exist in two-bone fixture")
	}
}

func TestCommittedAssetCoversAllReferenceGear(t *testing.T) {
	data, err := os.ReadFile("../server/static/board-avatars.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"<image", "data:", ".png", "href="} {
		if strings.Contains(string(data), banned) {
			t.Fatalf("committed board-avatars.json contains banned token %q", banned)
		}
	}
	var out Output
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	specs := refItemCatalog()
	if len(out.Accessories) != len(specs) {
		t.Fatalf("got %d baked accessories, want %d", len(out.Accessories), len(specs))
	}
	for id, spec := range specs {
		acc, ok := out.Accessories[id]
		if !ok {
			t.Errorf("missing accessory %s", id)
			continue
		}
		if acc.Slot != spec.slot || acc.Tintable != spec.tintable {
			t.Errorf("%s: got slot=%q tintable=%v, want slot=%q tintable=%v", id, acc.Slot, acc.Tintable, spec.slot, spec.tintable)
		}
		for _, comp := range acc.Components {
			if comp.SVG == "" || !strings.Contains(comp.SVG, "<path") {
				t.Errorf("%s/%s: empty or non-path SVG", id, comp.ID)
			}
		}
	}
	for body, f := range out.Fighters {
		for id, acc := range out.Accessories {
			for _, comp := range acc.Components {
				if _, ok := f.RefFits[comp.ID]; !ok {
					t.Errorf("%s outfit 1 missing ref_fit for %s (%s)", body, comp.ID, id)
				}
				if o2, ok := f.Outfits["2"]; ok {
					if _, ok := o2.RefFits[comp.ID]; !ok {
						t.Errorf("%s outfit 2 missing ref_fit for %s (%s)", body, comp.ID, id)
					}
				}
			}
		}
	}
	for _, body := range []string{"puppet-luchador", "puppet-oni", "puppet-tempest"} {
		if !out.Fighters[body].Outfits["2"].FrontBehind {
			t.Errorf("%s outfit 2 must set front_behind=true", body)
		}
	}
}
