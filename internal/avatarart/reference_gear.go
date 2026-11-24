package avatarart

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Accessory is one app-owned ReferenceGear cosmetic (vector parts.vec.json or vectorized
// artwork PNG) shared across all fighters and outfits. Per-fighter placements live in
// Fighter.RefFits and OutfitArt.RefFits so the art is stored once instead of 28x.
type Accessory struct {
	Slot       string               `json:"slot"`
	Components []AccessoryComponent `json:"components"`
	Tintable   bool                 `json:"tintable,omitempty"`
	Companion  bool                 `json:"companion,omitempty"`
	Tall       bool                 `json:"tall,omitempty"`
}

// AccessoryComponent is one drawable piece of an Accessory (most items have one "art"
// component; mushroom-mode, witching-hour and lava-lamp have extra rear/bat/blobs pieces).
type AccessoryComponent struct {
	ID    string `json:"id"`
	Layer string `json:"layer"`
	Size  [2]int `json:"size"`
	SVG   string `json:"svg"`
}

type refCatalogEntry struct {
	Width      int `json:"width"`
	Height     int `json:"height"`
	Components map[string]struct {
		X      int `json:"x"`
		Y      int `json:"y"`
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"components"`
}

type refFitsDoc struct {
	SchemaVersion int                             `json:"schema_version"`
	Fits          map[string]map[string][]float64 `json:"fits"`
}

type refItemSpec struct {
	slot       string
	artwork    bool
	tintable   bool
	behindBody bool
	neck       bool
	companion  bool
	tall       bool
}

// ReferenceGear metadata matching controller-tester-fgc
// Sources/CTDrillsDiagnostic/CTDrillsReferenceGear.swift, CTCore/ReferenceCosmetics.swift,
// CTCore/ReservedCosmetics.swift and ControllerTester/BoardAvatarView.swift.
var (
	refVectorHead = []string{
		"mushroom-mode", "under-construction", "witching-hour", "banana-guard", "fish-out-of-water",
		"light-bulb", "top-hat", "santa-hat", "propeller-hat", "flame-crown", "lightning-crown",
		"cat-ears", "headphones", "crescent-moon-sunglasses", "heart-sunglasses",
	}
	refArtworkHead = []string{
		"origami-boat-hat", "chef-hat", "feathered-fedora", "bowler-hat",
		"snail-shell-helmet", "pixel-shades", "shaman-mask",
	}
	refVectorShoulder = []string{
		"ufo", "mantis", "lunch-break", "dumpling-buddy", "pocket-ghost",
		"jellybean-jellyfish", "lava-lamp", "scarf", "tie", "chains",
	}
	refArtworkShoulder = []string{
		"axolotl", "rook", "rubber-duck", "pixel-frog", "origami-crane", "fire-spirit",
		"tiny-owl", "chibi-sloth", "chibi-monkey", "baby-dragon", "floating-ghost",
		"spooked-ghost", "tiny-beaver", "spark-spirit", "capybara", "sushi",
		"voodoo-pillow", "manta-ray", "pufferfish", "chameleon", "shrimp",
		"bookworm", "pixel-slug",
	}
	refVectorBack = []string{
		"fishbone-saber", "candy-cane-sword", "trident-sword", "bouquet",
	}
	refArtworkBack = []string{
		"giant-hammer", "giant-scythe", "battle-axe", "glowing-staff",
		"8-bit-key", "plunger", "bowling-pin", "wrench",
	}
	refVectorHand  = []string{"hook-glove"}
	refArtworkHand = []string{
		"pizza-slice", "burger", "tacos", "ice-cream", "burrito",
		"hot-dog", "fries", "candy-bar", "ping-pong-paddle",
	}

	// CTDrillsReferenceGear.swift behindBodyFrontFits: outfits whose silhouette would hide
	// a behind-body shoulder piece, so it draws in front instead.
	refBehindBodyFrontFits = map[string]bool{
		"puppet-beak": true, "puppet-beak-o2": true, "puppet-brunt-o2": true,
		"puppet-eclipse-king-sun": true, "puppet-frost-o2": true,
		"puppet-luchador#outfit-2": true, "puppet-mainframe-core": true,
		"puppet-mainframe-core-o2": true, "puppet-moth": true,
		"puppet-octopus": true, "puppet-octopus-o2": true,
		"puppet-oni#outfit-2": true, "puppet-scarecrow": true,
		"puppet-talon": true, "puppet-tempest#outfit-2": true,
		"puppet-warlord-ascended": true,
	}

	// CTReferenceCosmetics.arcadeGearReward + CTDrillsGearPack.items: vector reference
	// accessories whose neutral art takes v3 gear colour variants (ReservedCosmetics.swift).
	refTintableItems = map[string]bool{
		"ufo": true, "mantis": true, "lunch-break": true, "banana-guard": true,
		"under-construction": true, "trident-sword": true,
		"light-bulb": true, "top-hat": true, "santa-hat": true, "propeller-hat": true,
		"flame-crown": true, "lightning-crown": true, "cat-ears": true, "headphones": true,
		"crescent-moon-sunglasses": true, "heart-sunglasses": true, "lava-lamp": true,
		"bouquet": true, "scarf": true, "tie": true, "chains": true,
		"candy-cane-sword": true, "hook-glove": true,
	}

	refNeckItems = map[string]bool{"scarf": true, "tie": true, "chains": true}
)

func refItemCatalog() map[string]refItemSpec {
	m := map[string]refItemSpec{}
	for _, id := range refVectorHead {
		m[id] = refItemSpec{slot: "head", tintable: refTintableItems[id], tall: true}
	}
	for _, id := range refArtworkHead {
		m[id] = refItemSpec{slot: "head", artwork: true, tall: true}
	}
	for _, id := range refVectorShoulder {
		m[id] = refItemSpec{
			slot:       "shoulder",
			tintable:   refTintableItems[id],
			behindBody: id == "lunch-break",
			neck:       refNeckItems[id],
			companion:  !refNeckItems[id],
		}
	}
	for _, id := range refArtworkShoulder {
		m[id] = refItemSpec{slot: "shoulder", artwork: true, behindBody: true, companion: true}
	}
	for _, id := range refVectorBack {
		m[id] = refItemSpec{slot: "back", tintable: refTintableItems[id], companion: true, tall: true}
	}
	for _, id := range refArtworkBack {
		m[id] = refItemSpec{slot: "back", artwork: true, companion: true, tall: true}
	}
	for _, id := range refVectorHand {
		m[id] = refItemSpec{slot: "hand", tintable: refTintableItems[id]}
	}
	for _, id := range refArtworkHand {
		m[id] = refItemSpec{slot: "hand", artwork: true}
	}
	return m
}

type refCompDef struct {
	id    string
	key   string
	layer string
	bone  string
}

func refComponentsFor(id string, spec refItemSpec) []refCompDef {
	bone := "torso"
	if spec.slot == "head" {
		bone = "head"
	} else if spec.slot == "hand" {
		if spec.artwork {
			bone = "hand-l"
		} else {
			bone = "hand-r"
		}
	}
	layer := "front"
	switch {
	case spec.slot == "head":
		layer = "head"
	case spec.slot == "back":
		layer = "behind"
	case spec.behindBody:
		layer = "behind_body"
	case spec.neck:
		layer = "neck"
	case spec.slot == "hand" && spec.artwork:
		layer = "neck" // rear hand sits behind the head and near guard arm
	}
	comps := []refCompDef{{id: id, key: "art", layer: layer, bone: bone}}
	if id == "mushroom-mode" || id == "witching-hour" {
		comps = append(comps, refCompDef{id: id + "-rear", key: "rear", layer: "behind", bone: "head"})
	}
	if id == "witching-hour" {
		comps = append(comps, refCompDef{id: id + "-bat", key: "bat", layer: "head", bone: "head"})
	}
	if id == "lava-lamp" {
		comps = append(comps, refCompDef{id: id + "-blobs", key: "blobs", layer: "front", bone: "torso"})
	}
	return comps
}

func slotVarPrefix(slot string) string {
	switch slot {
	case "head":
		return "rh"
	case "back":
		return "rb"
	case "shoulder":
		return "rs"
	case "hand":
		return "rn"
	default:
		return ""
	}
}

// bakeReferenceGear reads ReferenceGear/catalog.json, fits.json and each accessory's
// vector document or artwork PNG, populating out.Accessories and per-fighter/outfit RefFits.
func bakeReferenceGear(dir string, out *Output, poses map[string]*posed, boxes map[string]Rect) error {
	var cat map[string]refCatalogEntry
	if err := readJSON(filepath.Join(dir, "catalog.json"), &cat); err != nil {
		return err
	}
	var fits refFitsDoc
	if err := readJSON(filepath.Join(dir, "fits.json"), &fits); err != nil {
		return err
	}
	if fits.SchemaVersion != 1 {
		return fmt.Errorf("reference gear fits.json: unsupported schema_version %d", fits.SchemaVersion)
	}

	specs := refItemCatalog()
	itemIDs := make([]string, 0, len(specs))
	for id := range specs {
		itemIDs = append(itemIDs, id)
	}
	sort.Strings(itemIDs)

	// Compile each accessory's pure-SVG artwork once.
	accessories := map[string]Accessory{}
	compDefsByItem := map[string][]refCompDef{}
	for _, id := range itemIDs {
		entry, ok := cat[id]
		if !ok {
			continue
		}
		spec := specs[id]
		defs := refComponentsFor(id, spec)
		var vdoc *vecDoc
		if !spec.artwork {
			var doc vecDoc
			if err := readJSON(filepath.Join(dir, id, "parts.vec.json"), &doc); err != nil {
				return fmt.Errorf("reference gear %s: %w", id, err)
			}
			if doc.Format != "puppet-part-vectors" || doc.SchemaVersion != 1 {
				return fmt.Errorf("reference gear %s: unsupported vector format", id)
			}
			vdoc = &doc
		}

		prefix := ""
		if spec.tintable {
			prefix = slotVarPrefix(spec.slot)
		}
		var comps []AccessoryComponent
		for _, cd := range defs {
			pixels, ok := entry.Components[cd.key]
			if !ok {
				return fmt.Errorf("reference gear %s: missing component %q in catalog.json", id, cd.key)
			}
			var svg string
			var err error
			if spec.artwork {
				svg, err = tracePNGToSVG(filepath.Join(dir, id, cd.key+".png"), id == "chef-hat")
			} else {
				artKey := cd.key + ".png"
				neutralKey := "neutral.png"
				if cd.key != "art" {
					neutralKey = cd.key + "-neutral.png"
				}
				artSlot, ok := vdoc.Slots[artKey]
				if !ok {
					return fmt.Errorf("reference gear %s: missing slot %q in parts.vec.json", id, artKey)
				}
				var neutralSlot *VecSlot
				if ns, has := vdoc.Slots[neutralKey]; has {
					neutralSlot = &ns
				}
				svg, err = compileRefVectorSlot(artSlot, neutralSlot, vdoc.RasterPixelsPerDesignUnit, prefix)
			}
			if err != nil {
				return fmt.Errorf("reference gear %s/%s: %w", id, cd.key, err)
			}
			comps = append(comps, AccessoryComponent{
				ID:    cd.id,
				Layer: cd.layer,
				Size:  [2]int{pixels.Width, pixels.Height},
				SVG:   svg,
			})
		}
		accessories[id] = Accessory{
			Slot:       spec.slot,
			Components: comps,
			Tintable:   spec.tintable,
			Companion:  spec.companion,
			Tall:       spec.tall,
		}
		compDefsByItem[id] = defs
	}
	if len(accessories) == 0 {
		return nil
	}
	out.Accessories = accessories

	// Resolve per-fighter and per-outfit idle-frame-0 placements in crop-relative design units.
	bodyIDs := make([]string, 0, len(out.Fighters))
	for id := range out.Fighters {
		bodyIDs = append(bodyIDs, id)
	}
	sort.Strings(bodyIDs)

	resolveFits := func(fitKey string, p *posed, box Rect) map[string][6]float64 {
		record := fits.Fits[fitKey]
		if len(record) == 0 {
			return nil
		}
		outFits := map[string][6]float64{}
		for _, id := range itemIDs {
			defs := compDefsByItem[id]
			if len(defs) == 0 {
				continue
			}
			entry := cat[id]
			// All-or-nothing per item, matching CTDrillsReferenceGear.prepare.
			allReady := true
			pending := make(map[string][6]float64, len(defs))
			for _, cd := range defs {
				raw := record[cd.id]
				if len(raw) != 6 {
					allReady = false
					break
				}
				valid := true
				for _, v := range raw {
					if math.IsNaN(v) || math.IsInf(v, 0) {
						valid = false
						break
					}
				}
				bi, hasBone := p.r.boneIndex[cd.bone]
				pixels, hasPixels := entry.Components[cd.key]
				if !valid || !hasBone || !hasPixels {
					allReady = false
					break
				}
				mount := Affine{raw[0], raw[1], raw[2], raw[3], raw[4], raw[5]}
				cropOff := Affine{1, 0, 0, 1, float64(pixels.X), float64(pixels.Y)}
				local := mount.Mul(cropOff)
				if math.Abs(local.A*local.D-local.B*local.C) <= 1e-6 {
					allReady = false
					break
				}
				length := 1.0
				if bi < len(p.boneLengthScales) && p.boneLengthScales[bi] != 0 {
					length = p.boneLengthScales[bi]
				}
				along := Affine{1, 0, 0, length, 0, 0}
				m := p.boneToCanvas[bi].Mul(along.Mul(local))
				pending[cd.id] = [6]float64{
					round(m.A, 4), round(m.B, 4), round(m.C, 4), round(m.D, 4),
					round(m.TX-box.MinX, 2), round(m.TY-box.MinY, 2),
				}
			}
			if allReady {
				for k, v := range pending {
					outFits[k] = v
				}
			}
		}
		if len(outFits) == 0 {
			return nil
		}
		return outFits
	}

	for _, bodyID := range bodyIDs {
		fi := out.Fighters[bodyID]
		p := poses[bodyID]
		box := boxes[bodyID]
		if p == nil {
			continue
		}
		fi.RefFits = resolveFits(bodyID, p, box)
		fi.FrontBehind = refBehindBodyFrontFits[bodyID]
		if len(fi.Outfits) > 0 {
			outfitKeys := make([]string, 0, len(fi.Outfits))
			for k := range fi.Outfits {
				outfitKeys = append(outfitKeys, k)
			}
			sort.Strings(outfitKeys)
			for _, ord := range outfitKeys {
				o := fi.Outfits[ord]
				fk := bodyID + "#outfit-" + ord
				o.RefFits = resolveFits(fk, p, box)
				o.FrontBehind = refBehindBodyFrontFits[fk]
				fi.Outfits[ord] = o
			}
		}
		out.Fighters[bodyID] = fi
	}
	return nil
}

// ---------------------------------------------------------------------------------------
// Vector ReferenceGear compiler (parts.vec.json -> pure SVG in [0..w, 0..h] y-down space).

func compileRefVectorSlot(art VecSlot, neutral *VecSlot, ppu float64, varPrefix string) (string, error) {
	if len(art.Origin) != 2 || len(art.Size) != 2 || ppu <= 0 {
		return "", fmt.Errorf("invalid vector slot header")
	}
	w := art.Size[0] * ppu
	h := art.Size[1] * ppu
	ox := art.Origin[0]
	oy := art.Origin[1]
	toImg := func(x, y float64) (float64, float64) {
		return ppu * (x + ox), ppu * (oy - y)
	}

	neutralMatches := neutral != nil && len(neutral.Layers) == len(art.Layers)
	paint := func(authored string, neutralColor *string) string {
		if varPrefix == "" || !neutralMatches || neutralColor == nil {
			return authored
		}
		switch strings.ToUpper(*neutralColor) {
		case "#001600":
			return fmt.Sprintf("var(--%s0,%s)", varPrefix, authored)
		case "#00FF00":
			return fmt.Sprintf("var(--%s1,%s)", varPrefix, authored)
		default:
			return authored
		}
	}

	// Check if any lattice stroke extends beyond the slot rect [0..w, 0..h] so we only emit
	// a clipPath when PuppetPartRasterizer would actually clip the strokes.
	needsClip := false
	for _, l := range art.Layers {
		if l.Lattice == nil || l.Width == nil {
			continue
		}
		hw := (*l.Width * ppu) / 2
		for _, line := range l.Lattice {
			for k := 0; k+1 < len(line); k += 2 {
				ix, iy := toImg(line[k], line[k+1])
				if ix-hw < -0.5 || ix+hw > w+0.5 || iy-hw < -0.5 || iy+hw > h+0.5 {
					needsClip = true
					break
				}
			}
			if needsClip {
				break
			}
		}
		if needsClip {
			break
		}
	}

	var b strings.Builder
	if needsClip {
		fmt.Fprintf(&b, `<clipPath id="{id}"><rect width="%s" height="%s"/></clipPath><g clip-path="url(#{id})">`,
			num(w, 1), num(h, 1))
	}

	silPath, err := refPathData(art.Silhouette, ppu, ox, oy, 0.45)
	if err != nil {
		return "", err
	}

	for i := 0; i < len(art.Layers); i++ {
		l := art.Layers[i]
		if l.Opacity != nil && *l.Opacity == 0 {
			continue
		}
		// Skip a fill-only casing layer that is immediately re-filled identically after a
		// same-color lattice stroke (L1 + L2 + L3 == L2 + L3 with zero visual change).
		if i+2 < len(art.Layers) && isRedundantCasingFill(art.Layers, neutral, i) {
			continue
		}
		var nl *VecLayer
		if neutralMatches {
			nl = &neutral.Layers[i]
		}
		if l.Lattice != nil {
			if l.Stroke == nil || l.Width == nil {
				return "", fmt.Errorf("lattice without stroke")
			}
			var d strings.Builder
			for _, line := range l.Lattice {
				if len(line) < 4 {
					continue
				}
				pts := make([][2]float64, 0, len(line)/2)
				for k := 0; k+1 < len(line); k += 2 {
					ix, iy := toImg(line[k], line[k+1])
					pts = append(pts, [2]float64{ix, iy})
				}
				pts = simplifyPolyline(pts, 0.45)
				if len(pts) < 2 {
					continue
				}
				d.WriteString("M" + num(pts[0][0], 1) + " " + num(pts[0][1], 1))
				for k := 1; k < len(pts); k++ {
					d.WriteString("L" + num(pts[k][0], 1) + " " + num(pts[k][1], 1))
				}
			}
			if d.Len() == 0 {
				continue
			}
			var ns *string
			if nl != nil {
				ns = nl.Stroke
			}
			fmt.Fprintf(&b, `<path d="%s" style="fill:none;stroke:%s" stroke-width="%s" stroke-linecap="round" stroke-linejoin="round"/>`,
				d.String(), paint(*l.Stroke, ns), num(*l.Width*ppu, 2))
			continue
		}
		if l.Fill == nil {
			continue
		}
		d := silPath
		if len(l.Shape) > 0 {
			d, err = refPathData(l.Shape, ppu, ox, oy, 0.45)
			if err != nil {
				return "", err
			}
		}
		if d == "" {
			continue
		}
		var nf, ns *string
		if nl != nil {
			nf, ns = nl.Fill, nl.Stroke
		}
		opacity := ""
		if l.Opacity != nil && *l.Opacity != 1 {
			opacity = ` fill-opacity="` + num(*l.Opacity, 3) + `"`
		}
		if l.Stroke != nil && l.Width != nil {
			fmt.Fprintf(&b, `<path d="%s" style="fill:%s;stroke:%s"%s stroke-width="%s" stroke-linejoin="round"/>`,
				d, paint(*l.Fill, nf), paint(*l.Stroke, ns), opacity, num(*l.Width*ppu, 2))
		} else {
			fmt.Fprintf(&b, `<path d="%s" style="fill:%s"%s/>`, d, paint(*l.Fill, nf), opacity)
		}
	}
	if needsClip {
		b.WriteString(`</g>`)
	}
	return b.String(), nil
}

func isRedundantCasingFill(layers []VecLayer, neutral *VecSlot, i int) bool {
	a, mid, c := layers[i], layers[i+1], layers[i+2]
	if a.Lattice != nil || a.Fill == nil || a.Stroke != nil || (a.Opacity != nil && *a.Opacity != 1) {
		return false
	}
	if mid.Lattice == nil || mid.Stroke == nil || *mid.Stroke != *a.Fill {
		return false
	}
	if c.Lattice != nil || c.Fill == nil || c.Stroke != nil || (c.Opacity != nil && *c.Opacity != 1) {
		return false
	}
	if *c.Fill != *a.Fill || !floatSlicesEqual(a.Shape, c.Shape) {
		return false
	}
	if neutral != nil && len(neutral.Layers) == len(layers) {
		na, nmid, nc := neutral.Layers[i], neutral.Layers[i+1], neutral.Layers[i+2]
		if na.Fill == nil || nmid.Stroke == nil || nc.Fill == nil {
			return false
		}
		if *na.Fill != *nmid.Stroke || *na.Fill != *nc.Fill {
			return false
		}
	}
	return true
}

func floatSlicesEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// refPathData converts a bone-local y-up op stream into image-pixel y-down SVG path data,
// simplifying pure polygon runs with RDP tolerance eps so dense traced curves stay compact.
func refPathData(ops []float64, ppu, ox, oy, eps float64) (string, error) {
	hasArc := false
	for i := 0; i < len(ops); {
		switch ops[i] {
		case 0, 1:
			if i+2 >= len(ops) {
				return "", fmt.Errorf("truncated op stream")
			}
			i += 3
		case 2:
			if i+5 >= len(ops) {
				return "", fmt.Errorf("truncated arc")
			}
			hasArc = true
			i += 6
		case 3:
			i++
		default:
			return "", fmt.Errorf("unknown op %v", ops[i])
		}
	}
	if hasArc {
		var b strings.Builder
		for i := 0; i < len(ops); {
			switch ops[i] {
			case 0, 1:
				cmd := "M"
				if ops[i] == 1 {
					cmd = "L"
				}
				x, y := ppu*(ops[i+1]+ox), ppu*(oy-ops[i+2])
				b.WriteString(cmd + num(x, 1) + " " + num(y, 1))
				i += 3
			case 2:
				// Flipping Y mirrors the plane and flips the SVG arc sweep flag.
				r := num(ops[i+1]*ppu, 1)
				sweep := 1 - flag(ops[i+3])
				x, y := ppu*(ops[i+4]+ox), ppu*(oy-ops[i+5])
				fmt.Fprintf(&b, "A%s %s 0 %d %d %s %s", r, r, flag(ops[i+2]), sweep, num(x, 1), num(y, 1))
				i += 6
			case 3:
				b.WriteString("Z")
				i++
			}
		}
		return b.String(), nil
	}

	var b strings.Builder
	var ring [][2]float64
	flush := func(closed bool) {
		if len(ring) == 0 {
			return
		}
		pts := ring
		if closed {
			pts = simplifyClosedRing(pts, eps)
		} else {
			pts = simplifyPolyline(pts, eps)
		}
		if len(pts) >= 2 {
			b.WriteString("M" + num(pts[0][0], 1) + " " + num(pts[0][1], 1))
			for k := 1; k < len(pts); k++ {
				b.WriteString("L" + num(pts[k][0], 1) + " " + num(pts[k][1], 1))
			}
			if closed {
				b.WriteString("Z")
			}
		}
		ring = ring[:0]
	}
	for i := 0; i < len(ops); {
		switch ops[i] {
		case 0:
			flush(false)
			ring = append(ring, [2]float64{ppu * (ops[i+1] + ox), ppu * (oy - ops[i+2])})
			i += 3
		case 1:
			ring = append(ring, [2]float64{ppu * (ops[i+1] + ox), ppu * (oy - ops[i+2])})
			i += 3
		case 3:
			flush(true)
			i++
		}
	}
	flush(false)
	return b.String(), nil
}

// ---------------------------------------------------------------------------------------
// Pure-Go PNG artwork vectorizer (CTDrillsArtworkGear transparent PNGs -> pure SVG paths).
// Zero raster data leaves the baker: every output byte is inline <path> geometry.

type rgbSample struct {
	r, g, b float64
	weight  float64
}

func tracePNGToSVG(path string, softenBlack bool) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return "", err
	}
	bounds := img.Bounds()
	origW, origH := bounds.Dx(), bounds.Dy()
	if origW <= 0 || origH <= 0 {
		return "", fmt.Errorf("empty image")
	}

	// Unpack original RGBA once so palette discovery runs at native resolution (where neon
	// strokes are 8–20 px thick and anti-aliased fringes are only 1–2 px wide).
	srcR := make([]float64, origW*origH)
	srcG := make([]float64, origW*origH)
	srcB := make([]float64, origW*origH)
	srcOpaque := make([]bool, origW*origH)
	opaqueSrc := 0
	for y := 0; y < origH; y++ {
		for x := 0; x < origW; x++ {
			r16, g16, b16, a16 := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			a := float64(a16) / 257.0
			if a >= 128 && a16 > 0 {
				idx := y*origW + x
				r := clamp255(float64(r16) * 255.0 / float64(a16))
				g := clamp255(float64(g16) * 255.0 / float64(a16))
				b := clamp255(float64(b16) * 255.0 / float64(a16))
				if softenBlack && r < 40 && g < 40 && b < 40 {
					// Match CTDrillsFixedTintBake.swift: soften chef-hat's black fill to #383B42.
					r, g, b = 56, 59, 66
				}
				srcOpaque[idx] = true
				srcR[idx], srcG[idx], srcB[idx] = r, g, b
				opaqueSrc++
			}
		}
	}
	if opaqueSrc == 0 {
		return "", nil
	}

	// Sample native-resolution interior pixels whose ±step neighbours match closely; this
	// isolates pure flat colours and ignores 1–2 px anti-aliasing transitions.
	step := 2
	nbr := 1
	if origW > 360 || origH > 360 {
		step = 3
		nbr = 2
	}
	var uniformSamples, fallbackSamples []rgbSample
	for y := 0; y < origH; y += step {
		for x := 0; x < origW; x += step {
			idx := y*origW + x
			if !srcOpaque[idx] {
				continue
			}
			s := rgbSample{r: srcR[idx], g: srcG[idx], b: srcB[idx], weight: 1}
			fallbackSamples = append(fallbackSamples, s)
			if x < nbr || x+nbr >= origW || y < nbr || y+nbr >= origH {
				continue
			}
			uniform := true
			for _, d := range [][2]int{{nbr, 0}, {-nbr, 0}, {0, nbr}, {0, -nbr}} {
				ni := (y+d[1])*origW + (x + d[0])
				if !srcOpaque[ni] || rgbDistSq(s.r, s.g, s.b, srcR[ni], srcG[ni], srcB[ni]) > 20*20 {
					uniform = false
					break
				}
			}
			if uniform {
				uniformSamples = append(uniformSamples, s)
			}
		}
	}
	paletteInput := fallbackSamples
	if len(uniformSamples) >= 24 {
		paletteInput = uniformSamples
	}
	palette := quantizePalette(paletteInput, 8, 44)
	if len(palette) < 2 && len(fallbackSamples) > len(uniformSamples) {
		palette = quantizePalette(fallbackSamples, 8, 44)
	}
	if len(palette) == 0 {
		return "", nil
	}

	// Identify the darkest palette entry so downsampling preserves thin bright neon lines
	// against the dark interior instead of blurring them out.
	darkIdx := 0
	darkLuma := math.Inf(1)
	for ci, c := range palette {
		luma := 0.299*c[0] + 0.587*c[1] + 0.114*c[2]
		if luma < darkLuma {
			darkLuma = luma
			darkIdx = ci
		}
	}

	const maxGrid = 80
	gw, gh := origW, origH
	if gw > maxGrid || gh > maxGrid {
		if gw >= gh {
			gh = int(math.Max(1, math.Round(float64(gh)*float64(maxGrid)/float64(gw))))
			gw = maxGrid
		} else {
			gw = int(math.Max(1, math.Round(float64(gw)*float64(maxGrid)/float64(gh))))
			gh = maxGrid
		}
	}

	// Quantize at native resolution and aggregate per grid cell by label vote (preserving
	// thin non-dark strokes whenever they cover >= 28% of a cell's opaque pixels).
	labels := make([]int, gw*gh)
	for i := range labels {
		labels[i] = -1
	}
	counts := make([]int, len(palette))
	for gy := 0; gy < gh; gy++ {
		sy0 := gy * origH / gh
		sy1 := (gy + 1) * origH / gh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for gx := 0; gx < gw; gx++ {
			sx0 := gx * origW / gw
			sx1 := (gx + 1) * origW / gw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			for i := range counts {
				counts[i] = 0
			}
			totalPix := (sy1 - sy0) * (sx1 - sx0)
			opPix := 0
			for sy := sy0; sy < sy1; sy++ {
				row := sy * origW
				for sx := sx0; sx < sx1; sx++ {
					idx := row + sx
					if !srcOpaque[idx] {
						continue
					}
					opPix++
					best, bestD := 0, math.Inf(1)
					for ci, c := range palette {
						d := rgbDistSq(srcR[idx], srcG[idx], srcB[idx], c[0], c[1], c[2])
						if d < bestD {
							best, bestD = ci, d
						}
					}
					counts[best]++
				}
			}
			if opPix*2 < totalPix {
				continue
			}
			bestC, bestN := darkIdx, counts[darkIdx]
			bestStrokeC, bestStrokeN := -1, 0
			for ci, n := range counts {
				if n > bestN {
					bestC, bestN = ci, n
				}
				if ci != darkIdx && n > bestStrokeN {
					bestStrokeC, bestStrokeN = ci, n
				}
			}
			if bestC == darkIdx && bestStrokeC >= 0 && bestStrokeN*100 >= opPix*28 {
				bestC = bestStrokeC
			}
			labels[gy*gw+gx] = bestC
		}
	}

	// Single-pass despeckle on isolated single-pixel specks.
	nextLabels := append([]int(nil), labels...)
	for gy := 0; gy < gh; gy++ {
		for gx := 0; gx < gw; gx++ {
			idx := gy*gw + gx
			cur := labels[idx]
			if cur < 0 {
				continue
			}
			nc := map[int]int{}
			same := 0
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := gx+d[0], gy+d[1]
				if nx < 0 || nx >= gw || ny < 0 || ny >= gh {
					continue
				}
				nl := labels[ny*gw+nx]
				if nl < 0 {
					continue
				}
				if nl == cur {
					same++
				} else {
					nc[nl]++
				}
			}
			if same == 0 {
				bestC, bestN := -1, 0
				for c, n := range nc {
					if n > bestN || (n == bestN && c < bestC) {
						bestC, bestN = c, n
					}
				}
				if bestN >= 3 {
					nextLabels[idx] = bestC
				}
			}
		}
	}
	labels = nextLabels

	// Choose the seam-free base silhouette colour: if one colour owns >= 65% of the outer
	// perimeter (neon-outline items like axolotl, snail-shell-helmet, chef-hat), use it so
	// the outer rim has zero dark bleed; otherwise use the darkest colour (multi-colour
	// outlines over dark interior like shaman-mask).
	borderCounts := make([]int, len(palette))
	totalBorder := 0
	colorCounts := make([]int, len(palette))
	for gy := 0; gy < gh; gy++ {
		for gx := 0; gx < gw; gx++ {
			idx := gy*gw + gx
			c := labels[idx]
			if c < 0 {
				continue
			}
			colorCounts[c]++
			onBorder := gx == 0 || gx == gw-1 || gy == 0 || gy == gh-1 ||
				labels[idx-1] < 0 || labels[idx+1] < 0 || labels[idx-gw] < 0 || labels[idx+gw] < 0
			if onBorder {
				borderCounts[c]++
				totalBorder++
			}
		}
	}
	baseColor := -1
	bestBorder, bestBorderN := 0, 0
	for c, n := range borderCounts {
		if n > bestBorderN {
			bestBorder, bestBorderN = c, n
		}
	}
	if totalBorder > 0 && float64(bestBorderN)/float64(totalBorder) >= 0.65 {
		baseColor = bestBorder
	} else {
		minLuma := math.Inf(1)
		for c, col := range palette {
			if colorCounts[c] == 0 {
				continue
			}
			luma := 0.299*col[0] + 0.587*col[1] + 0.114*col[2]
			if luma < minLuma {
				minLuma = luma
				baseColor = c
			}
		}
	}
	if baseColor < 0 {
		return "", nil
	}

	sx := float64(origW) / float64(gw)
	sy := float64(origH) / float64(gh)
	hexOf := func(c [3]float64) string {
		return fmt.Sprintf("#%02X%02X%02X", int(math.Round(c[0])), int(math.Round(c[1])), int(math.Round(c[2])))
	}

	var b strings.Builder
	// Base full silhouette prevents hairline anti-aliasing seams between adjacent regions.
	if d := traceMaskContours(gw, gh, sx, sy, 0.65, func(x, y int) bool { return labels[y*gw+x] >= 0 }); d != "" {
		fmt.Fprintf(&b, `<path d="%s" fill="%s" fill-rule="evenodd"/>`, d, hexOf(palette[baseColor]))
	}

	order := make([]int, 0, len(palette))
	for c := range palette {
		if c != baseColor && colorCounts[c] > 0 {
			order = append(order, c)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if colorCounts[order[i]] != colorCounts[order[j]] {
			return colorCounts[order[i]] > colorCounts[order[j]]
		}
		return hexOf(palette[order[i]]) < hexOf(palette[order[j]])
	})
	for _, c := range order {
		col := c
		d := traceMaskContours(gw, gh, sx, sy, 0.65, func(x, y int) bool { return labels[y*gw+x] == col })
		if d == "" {
			continue
		}
		fmt.Fprintf(&b, `<path d="%s" fill="%s" fill-rule="evenodd"/>`, d, hexOf(palette[col]))
	}
	return b.String(), nil
}

func quantizePalette(samples []rgbSample, maxColors int, mergeDist float64) [][3]float64 {
	if len(samples) == 0 {
		return nil
	}
	type bucket struct {
		items []rgbSample
	}
	bucketRange := func(items []rgbSample) (int, float64) {
		minR, maxR := 255.0, 0.0
		minG, maxG := 255.0, 0.0
		minB, maxB := 255.0, 0.0
		for _, s := range items {
			if s.r < minR {
				minR = s.r
			}
			if s.r > maxR {
				maxR = s.r
			}
			if s.g < minG {
				minG = s.g
			}
			if s.g > maxG {
				maxG = s.g
			}
			if s.b < minB {
				minB = s.b
			}
			if s.b > maxB {
				maxB = s.b
			}
		}
		dr, dg, db := maxR-minR, maxG-minG, maxB-minB
		if dr >= dg && dr >= db {
			return 0, dr
		}
		if dg >= db {
			return 1, dg
		}
		return 2, db
	}

	buckets := []bucket{{items: append([]rgbSample(nil), samples...)}}
	for len(buckets) < maxColors {
		bestIdx, bestAxis, bestScore := -1, 0, 0.0
		for i, bk := range buckets {
			if len(bk.items) < 2 {
				continue
			}
			axis, span := bucketRange(bk.items)
			if span < 22 {
				continue
			}
			var wSum float64
			for _, s := range bk.items {
				wSum += s.weight
			}
			score := span * math.Pow(wSum, 0.25)
			if score > bestScore {
				bestIdx, bestAxis, bestScore = i, axis, score
			}
		}
		if bestIdx < 0 {
			break
		}
		items := buckets[bestIdx].items
		sort.SliceStable(items, func(i, j int) bool {
			switch bestAxis {
			case 0:
				return items[i].r < items[j].r
			case 1:
				return items[i].g < items[j].g
			default:
				return items[i].b < items[j].b
			}
		})
		var totalW float64
		for _, s := range items {
			totalW += s.weight
		}
		half, acc, split := totalW/2, 0.0, len(items)/2
		for i, s := range items {
			acc += s.weight
			if acc >= half && i > 0 && i < len(items) {
				split = i
				break
			}
		}
		if split <= 0 || split >= len(items) {
			break
		}
		left := bucket{items: items[:split]}
		right := bucket{items: items[split:]}
		buckets[bestIdx] = left
		buckets = append(buckets, right)
	}

	centroids := make([][3]float64, 0, len(buckets))
	for _, bk := range buckets {
		var sr, sg, sb, sw float64
		for _, s := range bk.items {
			sr += s.r * s.weight
			sg += s.g * s.weight
			sb += s.b * s.weight
			sw += s.weight
		}
		if sw > 0 {
			centroids = append(centroids, [3]float64{sr / sw, sg / sw, sb / sw})
		}
	}

	// Deterministic weighted k-means refinement.
	weights := make([]float64, len(centroids))
	for iter := 0; iter < 8; iter++ {
		sums := make([][3]float64, len(centroids))
		for i := range weights {
			weights[i] = 0
		}
		for _, s := range samples {
			best, bestD := 0, math.Inf(1)
			for ci, c := range centroids {
				d := rgbDistSq(s.r, s.g, s.b, c[0], c[1], c[2])
				if d < bestD {
					best, bestD = ci, d
				}
			}
			sums[best][0] += s.r * s.weight
			sums[best][1] += s.g * s.weight
			sums[best][2] += s.b * s.weight
			weights[best] += s.weight
		}
		for ci := range centroids {
			if weights[ci] > 0 {
				centroids[ci] = [3]float64{sums[ci][0] / weights[ci], sums[ci][1] / weights[ci], sums[ci][2] / weights[ci]}
			}
		}
	}

	// Merge close centroids greedily (heaviest survives) so anti-aliased fringe tones vanish.
	type wc struct {
		col [3]float64
		w   float64
	}
	var active []wc
	for i, c := range centroids {
		if weights[i] > 0 {
			active = append(active, wc{col: c, w: weights[i]})
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].w > active[j].w })
	mergeSq := mergeDist * mergeDist
	var kept []wc
	for _, cand := range active {
		merged := false
		for i := range kept {
			if rgbDistSq(cand.col[0], cand.col[1], cand.col[2], kept[i].col[0], kept[i].col[1], kept[i].col[2]) < mergeSq {
				merged = true
				break
			}
		}
		if !merged {
			kept = append(kept, cand)
		}
	}
	out := make([][3]float64, len(kept))
	for i, k := range kept {
		out[i] = k.col
	}
	return out
}

func rgbDistSq(r1, g1, b1, r2, g2, b2 float64) float64 {
	dr, dg, db := r1-r2, g1-g2, b1-b2
	return dr*dr + dg*dg + db*db
}

func clamp255(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// traceMaskContours traces closed pixel-boundary loops (outer rings + holes) from a
// boolean mask on a w×h grid, simplifies them with RDP, and scales to [0..w*sx, 0..h*sy].
func traceMaskContours(w, h int, sx, sy, tol float64, inside func(x, y int) bool) string {
	type pt struct{ x, y int }
	edges := map[pt][]pt{}
	add := func(a, b pt) { edges[a] = append(edges[a], b) }
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !inside(x, y) {
				continue
			}
			if y == 0 || !inside(x, y-1) {
				add(pt{x, y}, pt{x + 1, y})
			}
			if x == w-1 || !inside(x+1, y) {
				add(pt{x + 1, y}, pt{x + 1, y + 1})
			}
			if y == h-1 || !inside(x, y+1) {
				add(pt{x + 1, y + 1}, pt{x, y + 1})
			}
			if x == 0 || !inside(x-1, y) {
				add(pt{x, y + 1}, pt{x, y})
			}
		}
	}
	if len(edges) == 0 {
		return ""
	}
	// Deterministic iteration order.
	starts := make([]pt, 0, len(edges))
	for k := range edges {
		starts = append(starts, k)
	}
	sort.Slice(starts, func(i, j int) bool {
		if starts[i].y != starts[j].y {
			return starts[i].y < starts[j].y
		}
		return starts[i].x < starts[j].x
	})

	var b strings.Builder
	for _, start := range starts {
		for len(edges[start]) > 0 {
			ring := []pt{start}
			prev := pt{start.x - 1, start.y}
			cur := start
			for {
				outs := edges[cur]
				if len(outs) == 0 {
					break
				}
				pick := 0
				if len(outs) > 1 {
					// At a saddle corner, prefer turning right to avoid figure-8 crossings.
					dx, dy := cur.x-prev.x, cur.y-prev.y
					want := pt{cur.x - dy, cur.y + dx}
					for i, cand := range outs {
						if cand == want {
							pick = i
							break
						}
					}
				}
				next := outs[pick]
				outs = append(outs[:pick], outs[pick+1:]...)
				if len(outs) == 0 {
					delete(edges, cur)
				} else {
					edges[cur] = outs
				}
				if next == start {
					break
				}
				prev = cur
				cur = next
				ring = append(ring, cur)
			}
			if len(ring) < 3 {
				continue
			}
			// Drop collinear grid steps before RDP.
			fpts := make([][2]float64, 0, len(ring))
			n := len(ring)
			for i := 0; i < n; i++ {
				p0 := ring[(i+n-1)%n]
				p1 := ring[i]
				p2 := ring[(i+1)%n]
				if (p1.x-p0.x)*(p2.y-p1.y)-(p1.y-p0.y)*(p2.x-p1.x) != 0 {
					fpts = append(fpts, [2]float64{float64(p1.x), float64(p1.y)})
				}
			}
			if len(fpts) < 3 || math.Abs(polygonArea(fpts)) < 1.25 {
				continue
			}
			simp := simplifyClosedRing(fpts, tol)
			if len(simp) < 3 {
				continue
			}
			b.WriteString("M" + num(simp[0][0]*sx, 0) + " " + num(simp[0][1]*sy, 0))
			for k := 1; k < len(simp); k++ {
				b.WriteString("L" + num(simp[k][0]*sx, 0) + " " + num(simp[k][1]*sy, 0))
			}
			b.WriteString("Z")
		}
	}
	return b.String()
}

func polygonArea(pts [][2]float64) float64 {
	sum := 0.0
	for i := range pts {
		j := (i + 1) % len(pts)
		sum += pts[i][0]*pts[j][1] - pts[j][0]*pts[i][1]
	}
	return sum / 2
}

func simplifyClosedRing(pts [][2]float64, eps float64) [][2]float64 {
	if len(pts) <= 4 || eps <= 0 {
		return pts
	}
	// Drop duplicated closing point if present.
	if len(pts) > 1 && pts[0] == pts[len(pts)-1] {
		pts = pts[:len(pts)-1]
	}
	split := 1
	bestD := -1.0
	for i := 1; i < len(pts); i++ {
		dx, dy := pts[i][0]-pts[0][0], pts[i][1]-pts[0][1]
		d := dx*dx + dy*dy
		if d > bestD {
			bestD = d
			split = i
		}
	}
	half1 := make([][2]float64, split+1)
	copy(half1, pts[:split+1])
	half2 := make([][2]float64, 0, len(pts)-split+1)
	half2 = append(half2, pts[split:]...)
	half2 = append(half2, pts[0])
	s1 := simplifyPolyline(half1, eps)
	s2 := simplifyPolyline(half2, eps)
	out := make([][2]float64, 0, len(s1)+len(s2)-2)
	out = append(out, s1[:len(s1)-1]...)
	out = append(out, s2[:len(s2)-1]...)
	if len(out) < 3 {
		return pts
	}
	return out
}

func simplifyPolyline(pts [][2]float64, eps float64) [][2]float64 {
	if len(pts) < 3 || eps <= 0 {
		return pts
	}
	a, b := pts[0], pts[len(pts)-1]
	dx, dy := b[0]-a[0], b[1]-a[1]
	denom := math.Hypot(dx, dy)
	maxIdx, maxDist := 0, -1.0
	for i := 1; i < len(pts)-1; i++ {
		var d float64
		if denom > 0 {
			d = math.Abs(dy*pts[i][0]-dx*pts[i][1]+b[0]*a[1]-b[1]*a[0]) / denom
		} else {
			d = math.Hypot(pts[i][0]-a[0], pts[i][1]-a[1])
		}
		if d > maxDist {
			maxDist = d
			maxIdx = i
		}
	}
	if maxDist <= eps {
		return [][2]float64{a, b}
	}
	left := simplifyPolyline(pts[:maxIdx+1], eps)
	right := simplifyPolyline(pts[maxIdx:], eps)
	out := make([][2]float64, 0, len(left)+len(right)-1)
	out = append(out, left[:len(left)-1]...)
	out = append(out, right...)
	return out
}
