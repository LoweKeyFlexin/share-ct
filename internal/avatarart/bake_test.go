package avatarart

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A two-bone rig: root → head. One body with a torso slot (layer 2) and the three head
// slots (layers 0, 1, 2); the pirate hat replaces the head slots with a taller image.
func writeFixture(t *testing.T) Inputs {
	t.Helper()
	dir := t.TempDir()
	kit := filepath.Join(dir, "kit")
	gearDir := filepath.Join(dir, "gear")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(kit, "shared/skeleton.json"), `{"bones":[
	  {"id":"root","parent":null,"rest_x":0,"rest_y":0,"rest_rotation_degrees":0},
	  {"id":"head","parent":"root","rest_x":0,"rest_y":100,"rest_rotation_degrees":0}],
	 "draw_groups":["core"]}`)
	write(filepath.Join(kit, "shared/animations/neutral.idle.json"), `{"engine_frame_count":2,"loop":true,
	  "tracks":[{"bone":"head","keyframes":[{"frame":0,"rotation_degrees":0},{"frame":1,"rotation_degrees":10}]}]}`)
	slot := func(id, bone, image string, layer, order, w, h int, py float64) string {
		return `{"id":"` + id + `","bone":"` + bone + `","image":"` + image + `","image_width":` + itoa(w) +
			`,"image_height":` + itoa(h) + `,"pivot_x":0.5,"pivot_y":` + ftoa(py) + `,"rest_scale":1,"draw_order":` +
			itoa(order) + `,"draw_group":"core","draw_layer":` + itoa(layer) + `}`
	}
	write(filepath.Join(kit, "puppet/puppet.json"), `{"raster_pixels_per_design_unit":4,"slots":[`+
		slot("torso-fill", "root", "parts/torso-fill.png", 2, 10, 160, 400, 1)+","+
		slot("head-key", "head", "parts/head-key.png", 0, 20, 160, 160, 1)+","+
		slot("head-line", "head", "parts/head-line.png", 1, 21, 160, 160, 1)+","+
		slot("head-fill", "head", "parts/head-fill.png", 2, 22, 160, 160, 1)+`]}`)
	square := func(w, h float64) string { // a w×h design-unit box standing on its pivot
		return `{"size":[` + ftoa(w) + `,` + ftoa(h) + `],"origin":[` + ftoa(w/2) + `,` + ftoa(h) + `],` +
			`"silhouette":[0,` + ftoa(-w/2) + `,0,1,` + ftoa(w/2) + `,0,2,` + ftoa(w/2) + `,0,1,` + ftoa(-w/2) + `,0,3],` +
			`"layers":[{"fill":"#082D2B","opacity":1},{"lattice":[[-1,10,1,10],[500,500,501,501]],"stroke":"#45D982","width":1.1}]}`
	}
	write(filepath.Join(kit, "puppet/parts.vec.json"), `{"format":"puppet-part-vectors","schema_version":1,
	  "raster_pixels_per_design_unit":4,"slots":{
	  "parts/torso-fill.png":`+square(40, 100)+`,"parts/head-key.png":`+square(40, 40)+`,
	  "parts/head-line.png":`+square(40, 40)+`,"parts/head-fill.png":`+square(40, 40)+`}}`)
	hatSlot := func() string {
		return `{"image":"puppet/hat.png","image_width":240,"image_height":200,"pivot_x":0.5,"pivot_y":1,"bone":"head"}`
	}
	write(filepath.Join(gearDir, "gear.json"), `{"item":"pirate-hat","slot":"head","bodies":{"puppet":{
	  "head-key":`+hatSlot()+`,"head-line":`+hatSlot()+`,"head-fill":`+hatSlot()+`}}}`)
	write(filepath.Join(gearDir, "puppet/parts.vec.json"), `{"format":"puppet-part-vectors","schema_version":1,
	  "raster_pixels_per_design_unit":4,"slots":{"hat.png":`+square(60, 50)+`}}`)
	write(filepath.Join(dir, "FighterRoster.swift"), `enum CTFighterRoster {
    static let all: [CTFighterDefinition] = [
        .init("puppet", "STANDARD", mood: "x",
              palette: ["default-blue", "default-orange"],
              bonus: [CTCharacterBonusLook.ex01.id]),
        .init("puppet-ghost", "GHOST", mood: "not shipped", palette: ["default-orange"]),
    ]
}
struct CTCharacterBonusLook {
    static let ex01 = Self(id: "bonus-ex01", name: "EX01", fill: CTRGB(r: 100, g: 210, b: 255),
                           outline: CTRGB(r: 255, g: 201, b: 60))
}`)
	write(filepath.Join(dir, "DrillsColourCatalog.swift"), `
        .init(id: "default-blue", name: "Player One Cyan", rgb: CTRGB(r: 100, g: 210, b: 255)),
        .init(id: "default-orange", name: "Player Two Orange", rgb: CTRGB(r: 255.0, g: 159, b: 10)),`)
	return Inputs{KitResources: kit, GearDir: gearDir,
		Roster: filepath.Join(dir, "FighterRoster.swift"), Colours: filepath.Join(dir, "DrillsColourCatalog.swift")}
}

func itoa(i int) string { return ftoa(float64(i)) }
func ftoa(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func TestBakeIsDeterministic(t *testing.T) {
	in := writeFixture(t)
	a, err := BakeJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		b, err := BakeJSON(in)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Fatalf("bake %d differs from the first", i+2)
		}
	}
}

func TestBakeFixture(t *testing.T) {
	out, err := Bake(writeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Fighters["puppet-ghost"]; ok {
		t.Error("a fighter the kit does not ship must be left out (the page draws the default)")
	}
	f, ok := out.Fighters["puppet"]
	if !ok {
		t.Fatal("puppet missing")
	}
	if got := f.Palette; len(got) != 2 || got[0] != [3]int{100, 210, 255} || got[1] != [3]int{255, 159, 10} {
		t.Errorf("palette %v", got)
	}
	if len(f.Bonus) != 1 || f.Bonus[0] != "bonus-ex01" {
		t.Errorf("bonus %v", f.Bonus)
	}

	// Figure: torso 0..100 up, head 100..140 up → canvas y -140..0, height 140, so
	// unitsPerPx = 140/455. Hatless head x -20..20; hatted head x -30..30 (same centre).
	u := 140.0 / standardHeightPx
	side := cropSidePx * u
	want := [3]float64{round(-side/2, 2), round(-140-cropHeadroomPx*u, 2), round(side, 2)}
	if f.Box != want {
		t.Errorf("box %v, want %v", f.Box, want)
	}

	// Head slots are placeholders filled from Head or Hat, in order.
	if n := strings.Count(strings.Join(f.Parts, "\x00"), "\x00\x00"); len(f.Head) != 3 || len(f.Hat) != 3 || n < 1 {
		t.Fatalf("parts/head/hat shape: %d parts, %d head, %d hat", len(f.Parts), len(f.Head), len(f.Hat))
	}
	empties := 0
	for _, p := range f.Parts {
		if p == "" {
			empties++
		}
	}
	if empties < 3 {
		t.Errorf("want a placeholder per head slot, got %d empties", empties)
	}
	// Layers 0 and 1 take the outline route, layer 2 the fill route.
	if !strings.Contains(f.Head[0], "var(--o") || !strings.Contains(f.Head[1], "var(--o") || strings.Contains(f.Head[2], "var(--o") {
		t.Errorf("tint routes wrong:\n%v", f.Head)
	}
	// Green levels: #082D2B → 45/255, #45D982 → 217/255.
	if len(f.Levels) != 2 || math.Abs(f.Levels[0]-0.176) > 1e-9 || math.Abs(f.Levels[1]-0.851) > 1e-9 {
		t.Errorf("levels %v", f.Levels)
	}
	// The lattice line far outside the crop is dropped; the one inside is kept.
	all := strings.Join(append(append([]string{}, f.Parts...), f.Head...), "")
	if strings.Contains(all, "M500 500") || !strings.Contains(all, "M-1 10L1 10") {
		t.Errorf("lattice culling wrong: %s", all)
	}
	// The hat is its own art (60 wide), the bare head is not.
	if !strings.Contains(f.Hat[2], "M-30 0") || strings.Contains(f.Head[2], "M-30 0") {
		t.Errorf("hat markup wrong:\nhead %s\nhat %s", f.Head[2], f.Hat[2])
	}
	// Clip ids are templated per symbol.
	if !strings.Contains(all, `id="{id}`) {
		t.Error("clip ids must be templated with {id}")
	}
}

func TestPathDataArcFlagsCarryOver(t *testing.T) {
	d, err := pathData([]float64{0, 1, 2, 2, 5.25, 1, 0, 3, 4, 3})
	if err != nil {
		t.Fatal(err)
	}
	if d != "M1 2A5.3 5.3 0 1 0 3 4Z" {
		t.Errorf("got %q", d)
	}
	if _, err := pathData([]float64{0, 1}); err == nil {
		t.Error("a truncated stream must fail")
	}
}

// With CT_KIT and CT_FGC set, re-bake from the real sources and require the committed
// file byte for byte (the private sources are not in CI, so it skips there).
func TestCommittedBakeMatchesSources(t *testing.T) {
	kit, fgc := os.Getenv("CT_KIT"), os.Getenv("CT_FGC")
	if kit == "" || fgc == "" {
		t.Skip("set CT_KIT and CT_FGC to check the committed bake against the sources")
	}
	got, err := BakeJSON(Inputs{
		KitResources: filepath.Join(kit, "PuppetPresentation/Resources/PuppetPresentation"),
		GearDir:      filepath.Join(fgc, "Sources/CTDrillsDiagnostic/Gear/pirate-hat"),
		Roster:       filepath.Join(fgc, "CTCore/FighterRoster.swift"),
		Colours:      filepath.Join(fgc, "CTCore/DrillsColourCatalog.swift"),
		Cosmetics:    cosmeticsDir(kit),
	})
	if err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(t.TempDir(), "avatars.json")
	if err := os.WriteFile(asset, got, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "../../tools/avatar-cosmetics.py", "--fgc", fgc, "--asset", asset)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("recipe bake: %v\n%s", err, output)
	}
	got, err = os.ReadFile(asset)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../server/static/board-avatars.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("internal/server/static/board-avatars.json is stale: run go run ./cmd/avatarbake")
	}
}

// The app's final rule: side 140 and headroom 31 mock px, both x unitsPerPx (Standard's
// hatless height / 455, shared); top from THIS fighter's hatless figure; centre halfway
// between the hatless and hatted head centres, or the head alone without hat art.
func TestCropRule(t *testing.T) {
	u := 184.64 / 455
	figure := Rect{-50, -170, 50, 15}
	head := Rect{-16, -170, 14, -133} // midX -1
	hat := Rect{-30, -178, 20, -133}  // midX -5
	got := Crop(figure, head, &hat, u)
	side := 140 * u
	want := Rect{-3 - side/2, -170 - 31*u, -3 + side/2, -170 - 31*u + side}
	if math.Abs(got.MinX-want.MinX) > 1e-9 || math.Abs(got.MinY-want.MinY) > 1e-9 ||
		math.Abs(got.MaxX-want.MaxX) > 1e-9 || math.Abs(got.MaxY-want.MaxY) > 1e-9 {
		t.Errorf("crop %+v, want %+v", got, want)
	}
	if noHat := Crop(figure, head, nil, u); math.Abs(noHat.MinX-(-1-side/2)) > 1e-9 {
		t.Errorf("without hat art the crop centres on the head: %+v", noHat)
	}
	if !Crop(figure, emptyRect(), &hat, u).isEmpty() {
		t.Error("no head, no crop")
	}
}

// Outfit 2 from the kit's cosmetics: its own ten colours, its own markup where it redraws a
// part (here its headpiece), and the shared crop; the kit's helmets fill the head slots on both outfits.
func TestBakeOutfitsAndKitGear(t *testing.T) {
	in := writeFixture(t)
	in.Cosmetics = filepath.Join(in.KitResources, "cosmetics")
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(in.Cosmetics, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	box := func(w, h float64) string {
		return `{"size":[` + ftoa(w) + `,` + ftoa(h) + `],"origin":[` + ftoa(w/2) + `,` + ftoa(h) + `],` +
			`"silhouette":[0,` + ftoa(-w/2) + `,0,1,` + ftoa(w/2) + `,0,2,` + ftoa(w/2) + `,0,1,` + ftoa(-w/2) + `,0,3],` +
			`"layers":[{"fill":"#0A3D2B","opacity":1}]}`
	}
	var colours []string
	for i := 0; i < 10; i++ {
		colours = append(colours, `{"index":`+itoa(i+1)+`,"name":"c","hex":"#0A0B`+string(rune('0'+i))+`0"}`)
	}
	write("catalog.json", `{"schema_version":1,"outfits":[{"id":"outfit-2","ordinal":2,"presentation":"puppet",
	  "resource":"cosmetics/outfit-2/puppet/outfit.json","vectors":"cosmetics/outfit-2/puppet/parts.vec.json",
	  "palette":"cosmetics/outfit-2/puppet/palette.json"}],
	  "gear":[{"id":"knight-helmet","resource":"cosmetics/gear/knight-helmet/gear.json"}]}`)
	write("outfit-2/puppet/outfit.json", `{"replaces":{"head-fill":{"image":"parts/head-fill.png",
	  "image_width":200,"image_height":200,"pivot_x":0.5,"pivot_y":1}}}`)
	write("outfit-2/puppet/parts.vec.json", `{"format":"puppet-part-vectors","schema_version":1,
	  "raster_pixels_per_design_unit":4,"slots":{"parts/head-fill.png":`+box(50, 50)+`}}`)
	write("outfit-2/puppet/palette.json", `{"colors":[`+strings.Join(colours, ",")+`]}`)
	helm := `{"image":"puppet/head-key.png","image_width":200,"image_height":200,"pivot_x":0.5,"pivot_y":1,"bone":"head"}`
	write("gear/knight-helmet/gear.json", `{"replaces":["head-key","head-line","head-fill"],"bodies":{"puppet":{
	  "head-key":`+helm+`,"head-line":`+strings.Replace(helm, "head-key", "head-line", 1)+`,
	  "head-fill":`+strings.Replace(helm, "head-key", "head-fill", 1)+`}},"outfits":{}}`)
	write("gear/knight-helmet/puppet/parts.vec.json", `{"format":"puppet-part-vectors","schema_version":1,
	  "raster_pixels_per_design_unit":4,"slots":{"head-key.png":`+box(50, 50)+`,"head-line.png":`+box(50, 50)+
		`,"head-fill.png":`+box(50, 50)+`}}`)
	out, err := Bake(in)
	if err != nil {
		t.Fatal(err)
	}
	f := out.Fighters["puppet"]
	o, ok := f.Outfits["2"]
	if !ok {
		t.Fatal("outfit 2 not baked")
	}
	if len(o.Palette) != 10 || o.Palette[3] != [3]int{10, 11, 48} {
		t.Errorf("outfit palette %v", o.Palette)
	}
	if strings.Join(o.Head, "|") == strings.Join(f.Head, "|") {
		t.Error("outfit 2 redraws the headpiece, so its head markup differs")
	}
	if len(f.Gear["knight-helmet"]) != len(f.Head) || len(o.Gear["knight-helmet"]) != len(o.Head) {
		t.Error("the helmet fills the head slots on both outfits")
	}
	if len(o.Gear["pirate-hat"]) != len(o.Head) {
		t.Error("the host pirate hat is worn over outfit 2 too")
	}
}

// cosmeticsDir is CT_COSMETICS when set (Outfit 2 from a newer kit export than the bodies),
// else the kit's own cosmetics folder.
func cosmeticsDir(kit string) string {
	if dir := os.Getenv("CT_COSMETICS"); dir != "" {
		return dir
	}
	return filepath.Join(kit, "PuppetPresentation/Resources/PuppetPresentation/cosmetics")
}

// controller-tester-fgc#7853: the Mind Crown (kit fixed_tint) is drawn in its own colour inside its
// mask, over the player-coloured head, with a halo behind the back head slot; the rest of the
// head keeps the fighter's colour variables. Uses the committed file, baked from the real kit.
func TestTheMindCrownDrawsItsOwnColourInsideItsMask(t *testing.T) {
	data, err := os.ReadFile("../server/static/board-avatars.json")
	if err != nil {
		t.Fatal(err)
	}
	var out Output
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	for body, f := range out.Fighters {
		head, ok := f.Gear["mind-crown"]
		if !ok {
			t.Fatalf("%s: no mind crown", body)
		}
		all := strings.Join(head, "")
		if !strings.Contains(all, "rgb(124,242,166)") {
			t.Errorf("%s: the crown is not drawn in #7CF2A6", body)
		}
		if !strings.Contains(all, "feGaussianBlur") {
			t.Errorf("%s: no halo", body)
		}
		if !strings.Contains(all, "var(--m") && !strings.Contains(all, "var(--o") {
			t.Errorf("%s: the head lost the player colour outside the mask", body)
		}
	}
}
