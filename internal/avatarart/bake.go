// Package avatarart precomputes the website's board-avatar headshots: for each fighter,
// with and without the pirate hat, ONE compact set of SVG paths clipped to the app's
// headshot crop. It is a build-time tool (cmd/avatarbake); the server only embeds and
// serves its output, so nothing here runs on the Pi.
//
// It ports exactly as much of the private fighter-engine-kit as the headshot needs: the
// shared skeleton, a body's slots, frame 0 of `neutral.idle`, the pose resolve, and the
// `puppet-part-vectors` art format. The app draws the same thing from the same inputs
// (fgc ControllerTester/BoardAvatarView.swift: hatless idle frame 0, design units at one
// point per unit, parts in draw order, tinted by the green channel times the player
// colour, draw layers 0 and 1 optionally in a separate outline colour).
package avatarart

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The headshot crop, in ONE place: fgc CTCore/BoardAvatar.swift CTBoardAvatarFraming on
// branch feat/avatars-match-mock (the approved mock's "Headshot framing"). The mock's px
// convert to kit design units through one scale shared by every fighter: Standard's
// hatless idle-frame-0 height is standardHeightPx on the mock's frame. See Crop.
const (
	cropSidePx       = 140.0
	cropHeadroomPx   = 31.0
	standardHeightPx = 455.0
	referenceBodyID  = "puppet"
	framingHat       = "pirate-hat"
)

// ---------------------------------------------------------------------------------------
// Kit data, decoded only as far as the headshot needs.

type bone struct {
	ID             string   `json:"id"`
	Parent         *string  `json:"parent"`
	RestX          float64  `json:"rest_x"`
	RestY          float64  `json:"rest_y"`
	RestRotation   float64  `json:"rest_rotation_degrees"`
	DefaultVariant *string  `json:"default_variant"`
	_              struct{} // keep field order stable for readers
}

type skeleton struct {
	Bones      []bone            `json:"bones"`
	DrawGroups []json.RawMessage `json:"draw_groups"`
}

type slot struct {
	ID          string  `json:"id"`
	Bone        string  `json:"bone"`
	Image       string  `json:"image"`
	ImageWidth  int     `json:"image_width"`
	ImageHeight int     `json:"image_height"`
	PivotX      float64 `json:"pivot_x"`
	PivotY      float64 `json:"pivot_y"`
	RestScale   float64 `json:"rest_scale"`
	DrawOrder   int     `json:"draw_order"`
	Variant     *string `json:"variant"`
	DrawGroup   *string `json:"draw_group"`
	DrawLayer   *int    `json:"draw_layer"`
}

type character struct {
	RasterPixelsPerDesignUnit float64 `json:"raster_pixels_per_design_unit"`
	Slots                     []slot  `json:"slots"`
}

type keyframe struct {
	Frame       int      `json:"frame"`
	X           *float64 `json:"x"`
	Y           *float64 `json:"y"`
	Rotation    *float64 `json:"rotation_degrees"`
	ScaleX      *float64 `json:"scale_x"`
	ScaleY      *float64 `json:"scale_y"`
	LengthScale *float64 `json:"length_scale"`
	Variant     *string  `json:"variant"`
}

type clip struct {
	EngineFrameCount int  `json:"engine_frame_count"`
	Loop             bool `json:"loop"`
	GroupOrderKeys   []struct {
		Frame      int               `json:"frame"`
		GroupOrder []json.RawMessage `json:"group_order"`
	} `json:"group_order_keys"`
	SlotOrderKeys []json.RawMessage `json:"slot_order_keys"`
	Tracks        []struct {
		Bone      string     `json:"bone"`
		Keyframes []keyframe `json:"keyframes"`
	} `json:"tracks"`
}

// VecLayer and VecSlot are one slot of a `parts.vec.json` (kit PuppetPartVectors.swift).
type VecLayer struct {
	Fill    *string     `json:"fill"`
	Opacity *float64    `json:"opacity"`
	Stroke  *string     `json:"stroke"`
	Width   *float64    `json:"width"`
	Lattice [][]float64 `json:"lattice"`
}

type VecSlot struct {
	Size       []float64  `json:"size"`
	Origin     []float64  `json:"origin"`
	Silhouette []float64  `json:"silhouette"`
	Layers     []VecLayer `json:"layers"`
}

type vecDoc struct {
	Format                    string             `json:"format"`
	SchemaVersion             int                `json:"schema_version"`
	RasterPixelsPerDesignUnit float64            `json:"raster_pixels_per_design_unit"`
	Slots                     map[string]VecSlot `json:"slots"`
}

type gearArt struct {
	Image       string  `json:"image"`
	ImageWidth  int     `json:"image_width"`
	ImageHeight int     `json:"image_height"`
	PivotX      float64 `json:"pivot_x"`
	PivotY      float64 `json:"pivot_y"`
	Bone        string  `json:"bone"`
}

type gearDoc struct {
	Item     string                        `json:"item"`
	Slot     string                        `json:"slot"`
	Replaces []string                      `json:"replaces"`
	Bodies   map[string]map[string]gearArt `json:"bodies"`
}

// ---------------------------------------------------------------------------------------
// Affine math, identical to the kit's PuppetAffine.

// Affine maps (x, y) to (a·x + c·y + tx, b·x + d·y + ty).
type Affine struct{ A, B, C, D, TX, TY float64 }

func trs(x, y, deg, sx, sy float64) Affine {
	r := deg * math.Pi / 180
	cs, sn := math.Cos(r), math.Sin(r)
	return Affine{cs * sx, sn * sx, -sn * sy, cs * sy, x, y}
}

// Mul returns o·i (i applies first).
func (o Affine) Mul(i Affine) Affine {
	return Affine{
		o.A*i.A + o.C*i.B, o.B*i.A + o.D*i.B,
		o.A*i.C + o.C*i.D, o.B*i.C + o.D*i.D,
		o.A*i.TX + o.C*i.TY + o.TX, o.B*i.TX + o.D*i.TY + o.TY,
	}
}

func (m Affine) apply(x, y float64) (float64, float64) {
	return m.A*x + m.C*y + m.TX, m.B*x + m.D*y + m.TY
}

// Rect is an axis-aligned box in canvas design units, y down.
type Rect struct{ MinX, MinY, MaxX, MaxY float64 }

func emptyRect() Rect        { return Rect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)} }
func (r Rect) isEmpty() bool { return r.MinX > r.MaxX }
func (r Rect) union(o Rect) Rect {
	return Rect{math.Min(r.MinX, o.MinX), math.Min(r.MinY, o.MinY), math.Max(r.MaxX, o.MaxX), math.Max(r.MaxY, o.MaxY)}
}
func (r Rect) intersects(o Rect) bool {
	return r.MinX < o.MaxX && o.MinX < r.MaxX && r.MinY < o.MaxY && o.MinY < r.MaxY
}
func (r Rect) midX() float64 { return (r.MinX + r.MaxX) / 2 }

func mapRect(m Affine, w, h float64) Rect {
	out := emptyRect()
	for _, p := range [][2]float64{{0, 0}, {w, 0}, {w, h}, {0, h}} {
		x, y := m.apply(p[0], p[1])
		out = out.union(Rect{x, y, x, y})
	}
	return out
}

// ---------------------------------------------------------------------------------------
// Rig + pose (kit PuppetValidation.validated / PuppetPoseResolver.resolve).

type rig struct {
	bones          []bone
	parent         []int // -1 for the root
	boneIndex      map[string]int
	slots          []slot // ascending draw_order
	slotBone       []int
	slotImageToBon []Affine
	variantNames   [][]string
	defaultVariant []int // -1 none
	slotVariant    []int // -1 none
	groups         []string
	groupSlots     [][]int
	slotLayers     []int
}

func buildRig(sk skeleton, ch character) (*rig, error) {
	r := &rig{boneIndex: map[string]int{}}
	// Parent-first by depth, then file order (the kit's `ordered`).
	depth := map[string]int{}
	byID := map[string]bone{}
	for _, b := range sk.Bones {
		byID[b.ID] = b
	}
	var depthOf func(id string) int
	depthOf = func(id string) int {
		if d, ok := depth[id]; ok {
			return d
		}
		b := byID[id]
		d := 0
		if b.Parent != nil {
			d = depthOf(*b.Parent) + 1
		}
		depth[id] = d
		return d
	}
	idx := make([]int, len(sk.Bones))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		da, db := depthOf(sk.Bones[idx[a]].ID), depthOf(sk.Bones[idx[b]].ID)
		if da != db {
			return da < db
		}
		return idx[a] < idx[b]
	})
	for _, i := range idx {
		r.boneIndex[sk.Bones[i].ID] = len(r.bones)
		r.bones = append(r.bones, sk.Bones[i])
	}
	for _, b := range r.bones {
		p := -1
		if b.Parent != nil {
			p = r.boneIndex[*b.Parent]
		}
		r.parent = append(r.parent, p)
	}

	// Draw groups: a flat list of names; an array entry merges its members by layer.
	var defaultOrder [][]int
	for _, raw := range sk.DrawGroups {
		names, err := groupItem(raw)
		if err != nil {
			return nil, err
		}
		var entry []int
		for _, n := range names {
			entry = append(entry, len(r.groups))
			r.groups = append(r.groups, n)
		}
		defaultOrder = append(defaultOrder, entry)
	}
	_ = defaultOrder

	r.slots = append([]slot(nil), ch.Slots...)
	sort.SliceStable(r.slots, func(a, b int) bool { return r.slots[a].DrawOrder < r.slots[b].DrawOrder })
	r.groupSlots = make([][]int, len(r.groups))
	r.variantNames = make([][]string, len(r.bones))
	for i, s := range r.slots {
		bi, ok := r.boneIndex[s.Bone]
		if !ok {
			return nil, fmt.Errorf("slot %s: unknown bone %s", s.ID, s.Bone)
		}
		r.slotBone = append(r.slotBone, bi)
		rs := s.RestScale
		if rs == 0 {
			rs = 1
		}
		u := rs / ch.RasterPixelsPerDesignUnit
		r.slotImageToBon = append(r.slotImageToBon, Affine{u, 0, 0, -u,
			-s.PivotX * float64(s.ImageWidth) * u, s.PivotY * float64(s.ImageHeight) * u})
		layer := 0
		if s.DrawLayer != nil {
			layer = *s.DrawLayer
		}
		r.slotLayers = append(r.slotLayers, layer)
		if s.DrawGroup != nil {
			for gi, g := range r.groups {
				if g == *s.DrawGroup {
					r.groupSlots[gi] = append(r.groupSlots[gi], i)
				}
			}
		}
		if s.Variant != nil && !contains(r.variantNames[bi], *s.Variant) {
			r.variantNames[bi] = append(r.variantNames[bi], *s.Variant)
		}
	}
	for bi := range r.variantNames {
		sort.Strings(r.variantNames[bi])
		dv := -1
		if d := r.bones[bi].DefaultVariant; d != nil {
			dv = indexOf(r.variantNames[bi], *d)
		}
		r.defaultVariant = append(r.defaultVariant, dv)
	}
	for i, s := range r.slots {
		v := -1
		if s.Variant != nil {
			v = indexOf(r.variantNames[r.slotBone[i]], *s.Variant)
		}
		r.slotVariant = append(r.slotVariant, v)
	}
	return r, nil
}

func groupItem(raw json.RawMessage) ([]string, error) {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, fmt.Errorf("draw group entry %s: %w", raw, err)
	}
	return many, nil
}

type offset struct {
	x, y, rot, sx, sy, length float64
	variant                   int // -1 = bone default
}

// frame0 samples a looping clip at engine frame 0 (kit PuppetResolvedClip.sample). Tracks
// are keyed at frame 0 in every shipped clip; anything else is refused rather than guessed.
func frame0(r *rig, c clip) ([]*offset, [][]int, error) {
	if len(c.SlotOrderKeys) > 0 {
		return nil, nil, fmt.Errorf("slot_order_keys are not supported by this bake")
	}
	offs := make([]*offset, len(r.bones))
	for _, t := range c.Tracks {
		bi, ok := r.boneIndex[t.Bone]
		if !ok {
			return nil, nil, fmt.Errorf("track for unknown bone %s", t.Bone)
		}
		if len(t.Keyframes) == 0 {
			continue
		}
		var k *keyframe
		for i := range t.Keyframes {
			if t.Keyframes[i].Frame == 0 {
				k = &t.Keyframes[i]
			}
		}
		if k == nil && len(t.Keyframes) == 1 {
			k = &t.Keyframes[0]
		}
		if k == nil {
			return nil, nil, fmt.Errorf("track %s has no frame-0 keyframe", t.Bone)
		}
		o := &offset{x: val(k.X, 0), y: val(k.Y, 0), rot: val(k.Rotation, 0),
			sx: val(k.ScaleX, 1), sy: val(k.ScaleY, 1), length: val(k.LengthScale, 1), variant: -1}
		// Variant: the step in force at frame 0 (a looping clip before its first step
		// holds the previous cycle's last one).
		var steps []keyframe
		for _, kf := range t.Keyframes {
			if kf.Variant != nil {
				steps = append(steps, kf)
			}
		}
		if len(steps) > 0 {
			at := -1
			for i, s := range steps {
				if s.Frame <= 0 {
					at = i
				}
			}
			if at < 0 && c.Loop {
				at = len(steps) - 1
			}
			if at >= 0 {
				o.variant = indexOf(r.variantNames[bi], *steps[at].Variant)
			}
		}
		offs[bi] = o
	}
	var order [][]int
	if len(c.GroupOrderKeys) > 0 {
		at := -1
		for i, k := range c.GroupOrderKeys {
			if k.Frame <= 0 {
				at = i
			}
		}
		if at < 0 && c.Loop {
			at = len(c.GroupOrderKeys) - 1
		}
		if at >= 0 {
			for _, raw := range c.GroupOrderKeys[at].GroupOrder {
				names, err := groupItem(raw)
				if err != nil {
					return nil, nil, err
				}
				var entry []int
				for _, n := range names {
					gi := indexOf(r.groups, n)
					if gi < 0 {
						return nil, nil, fmt.Errorf("group order names unknown group %s", n)
					}
					entry = append(entry, gi)
				}
				order = append(order, entry)
			}
		}
	}
	return offs, order, nil
}

// resolve returns each slot's image-pixel → canvas transform and the draw sequence, with
// the rig root at the canvas origin, one canvas point per design unit, facing right.
func (r *rig) resolve(offs []*offset, order [][]int) ([]Affine, []int) {
	rigToCanvas := Affine{1, 0, 0, -1, 0, 0}
	boneTo := make([]Affine, len(r.bones))
	lengths := make([]float64, len(r.bones))
	for i, b := range r.bones {
		o := offs[i]
		restY := b.RestY
		if p := r.parent[i]; p >= 0 && lengths[p] != 1 {
			restY *= lengths[p]
		}
		var ox, oy, orot, osx, osy, ol float64 = 0, 0, 0, 1, 1, 1
		if o != nil {
			ox, oy, orot, osx, osy, ol = o.x, o.y, o.rot, o.sx, o.sy, o.length
		}
		local := trs(b.RestX+ox, restY+oy, b.RestRotation+orot, osx, osy)
		parent := rigToCanvas
		if p := r.parent[i]; p >= 0 {
			parent = boneTo[p]
		}
		boneTo[i] = parent.Mul(local)
		lengths[i] = ol
	}
	toCanvas := make([]Affine, len(r.slots))
	for s, bi := range r.slotBone {
		if lengths[bi] == 1 {
			toCanvas[s] = boneTo[bi].Mul(r.slotImageToBon[s])
		} else {
			toCanvas[s] = boneTo[bi].Mul(Affine{1, 0, 0, lengths[bi], 0, 0}.Mul(r.slotImageToBon[s]))
		}
	}
	visible := func(s int) bool {
		v := r.slotVariant[s]
		if v < 0 {
			return true
		}
		bi := r.slotBone[s]
		want := r.defaultVariant[bi]
		if o := offs[bi]; o != nil && o.variant >= 0 {
			want = o.variant
		}
		return v == want
	}
	var seq []int
	add := func(s int) {
		if visible(s) {
			seq = append(seq, s)
		}
	}
	switch {
	case order == nil:
		for s := range r.slots {
			add(s)
		}
	default:
		for _, entry := range order {
			if len(entry) == 1 {
				for _, s := range r.groupSlots[entry[0]] {
					add(s)
				}
				continue
			}
			type m struct{ layer, member, slot int }
			var merged []m
			for member, g := range entry {
				for _, s := range r.groupSlots[g] {
					merged = append(merged, m{r.slotLayers[s], member, s})
				}
			}
			sort.Slice(merged, func(a, b int) bool {
				x, y := merged[a], merged[b]
				if x.layer != y.layer {
					return x.layer < y.layer
				}
				if x.member != y.member {
					return x.member < y.member
				}
				return x.slot < y.slot
			})
			for _, e := range merged {
				add(e.slot)
			}
		}
	}
	return toCanvas, seq
}

// ---------------------------------------------------------------------------------------
// The bake.

// Inputs names the source trees. KitResources is the kit's
// PuppetPresentation/Resources/PuppetPresentation; GearDir is fgc's
// Sources/CTDrillsDiagnostic/Gear/pirate-hat; Roster and Colours are fgc's
// CTCore/FighterRoster.swift and CTCore/DrillsColourCatalog.swift.
type Inputs struct {
	KitResources string
	GearDir      string
	Roster       string
	Colours      string
	// Cosmetics is the kit's exported cosmetics folder (KitResources/cosmetics): Outfit 2
	// and the kit's head gear (Knight and Diving Helmets). Empty or absent: neither is baked.
	Cosmetics string
}

// Fighter is one drawable roster entry: its palette, its crop and its headshot paths.
//
// The pirate hat costs only its own paths: gear replaces the head slots in place (same
// bone, same draw position), so Parts holds the shared markup in draw order with an empty
// string at each head slot, filled from Head (bare) or Hat (pirate hat) in order.
type Fighter struct {
	Palette [][3]int `json:"palette"`
	// Bonus looks this fighter may wear (EX01 on Standard).
	Bonus []string `json:"bonus,omitempty"`
	// Box is the crop in idle-frame design units: x, y, side.
	Box [3]float64 `json:"box"`
	// Levels are the distinct green-channel values (0..1) the paths use. A path's colour
	// is level × the player colour: var(--m<i>) on the fill route, var(--o<i>) on the
	// outline route (draw layers 0 and 1, which a two-tone bonus look recolours).
	Levels []float64 `json:"levels"`
	// SVG markup for a <symbol>, one <g transform> per part, back to front.
	Parts []string `json:"parts"`
	Head  []string `json:"head"`
	Hat   []string `json:"hat,omitempty"`
	// Gear is the kit's head gear on Outfit 1 (Knight and Diving Helmets): head-slot markup
	// filled into Parts exactly like Hat. The pirate hat stays in Hat.
	Gear map[string][]string `json:"gear,omitempty"`
	// Outfits are the fighter's other outfits by number ("2"): their own ten colours, their
	// own shared markup (an outfit redraws body parts) and their heads, bare and with gear.
	// The crop (Box) and the levels are the fighter's, shared by every outfit, as in the app.
	Outfits map[string]OutfitArt `json:"outfits,omitempty"`
}

// OutfitArt is one outfit of one fighter.
type OutfitArt struct {
	Palette [][3]int            `json:"palette"`
	Parts   []string            `json:"parts"`
	Head    []string            `json:"head"`
	Gear    map[string][]string `json:"gear,omitempty"`
}

// Output is the whole static asset.
type Output struct {
	Format   string             `json:"format"`
	Fighters map[string]Fighter `json:"fighters"`
}

// Bake produces the asset. It is deterministic: same inputs, same bytes.
func Bake(in Inputs) (*Output, error) {
	roster, err := ParseRoster(in.Roster)
	if err != nil {
		return nil, err
	}
	colours, err := ParseColours(in.Colours)
	if err != nil {
		return nil, err
	}
	var sk skeleton
	if err := readJSON(filepath.Join(in.KitResources, "shared/skeleton.json"), &sk); err != nil {
		return nil, err
	}
	var idle clip
	if err := readJSON(filepath.Join(in.KitResources, "shared/animations/neutral.idle.json"), &idle); err != nil {
		return nil, err
	}
	var gear gearDoc
	if err := readJSON(filepath.Join(in.GearDir, "gear.json"), &gear); err != nil {
		return nil, err
	}
	out := &Output{Format: "ct-board-avatars-1", Fighters: map[string]Fighter{}}
	var ref character
	if err := readJSON(filepath.Join(in.KitResources, referenceBodyID, "puppet.json"), &ref); err != nil {
		return nil, err
	}
	refPose, err := pose(sk, ref, idle, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", referenceBodyID, err)
	}
	unitsPerPx := (refPose.bounds.MaxY - refPose.bounds.MinY) / standardHeightPx
	for _, f := range roster {
		var pal [][3]int
		for _, id := range f.Palette {
			c, ok := colours[id]
			if !ok {
				return nil, fmt.Errorf("fighter %s: colour %s not in the catalog", f.BodyID, id)
			}
			pal = append(pal, c)
		}
		bodyDir := filepath.Join(in.KitResources, f.BodyID)
		if _, err := os.Stat(filepath.Join(bodyDir, "puppet.json")); err != nil {
			continue // a planned fighter the kit does not ship yet: the board draws the default
		}
		var ch character
		if err := readJSON(filepath.Join(bodyDir, "puppet.json"), &ch); err != nil {
			return nil, err
		}
		var vec vecDoc
		if err := readJSON(filepath.Join(bodyDir, "parts.vec.json"), &vec); err != nil {
			return nil, err
		}
		if vec.Format != "puppet-part-vectors" || vec.SchemaVersion != 1 {
			return nil, fmt.Errorf("%s: unsupported vector document", f.BodyID)
		}
		bare, err := pose(sk, ch, idle, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.BodyID, err)
		}
		bareArt := func(s slot) (VecSlot, float64, bool) {
			v, ok := vec.Slots[s.Image]
			return v, vec.RasterPixelsPerDesignUnit, ok
		}
		var hat *posed
		var hatHead *Rect
		art, hasHat := gear.Bodies[f.BodyID]
		if hasHat {
			if hat, err = pose(sk, ch, idle, art); err != nil {
				return nil, fmt.Errorf("%s+hat: %w", f.BodyID, err)
			}
			h := hat.headRect()
			hatHead = &h
		}
		box := Crop(bare.bounds, bare.headRect(), hatHead, unitsPerPx)
		if box.isEmpty() {
			return nil, fmt.Errorf("%s: no head slots", f.BodyID)
		}
		w := &writer{box: box, levels: map[float64]int{}}
		fi := Fighter{Palette: pal, Bonus: f.Bonus, Box: [3]float64{round(box.MinX, 2), round(box.MinY, 2), round(box.MaxX-box.MinX, 2)}}
		if fi.Parts, fi.Head, err = w.parts(bare, bareArt); err != nil {
			return nil, fmt.Errorf("%s: %w", f.BodyID, err)
		}
		if hasHat {
			var hatVec vecDoc
			if err := readJSON(filepath.Join(in.GearDir, f.BodyID, "parts.vec.json"), &hatVec); err != nil {
				return nil, err
			}
			hatArt := func(s slot) (VecSlot, float64, bool) {
				if a, ok := art[s.ID]; ok {
					v, ok := hatVec.Slots[filepath.Base(a.Image)]
					return v, hatVec.RasterPixelsPerDesignUnit, ok
				}
				return bareArt(s)
			}
			var shared []string
			if shared, fi.Hat, err = w.parts(hat, hatArt); err != nil {
				return nil, fmt.Errorf("%s+hat: %w", f.BodyID, err)
			}
			if strings.Join(shared, "|") != strings.Join(fi.Parts, "|") || len(fi.Hat) != len(fi.Head) {
				return nil, fmt.Errorf("%s: the hat changed parts other than the head", f.BodyID)
			}
		}
		if in.Cosmetics != "" {
			if err := bakeCosmetics(in, sk, ch, idle, w, &fi, f.BodyID, gear.Bodies[f.BodyID], in.GearDir, bareArt); err != nil {
				return nil, fmt.Errorf("%s: %w", f.BodyID, err)
			}
		}
		fi.Levels = w.levelList
		out.Fighters[f.BodyID] = fi
	}
	return out, nil
}

// Crop is the app's headshot square: side cropSidePx and top edge cropHeadroomPx above
// the hatless figure's top (both × unitsPerPx), centred halfway between the hatless
// head's centre and the pirate-hatted head's centre (the head alone when the fighter has
// no hat art). Rects are idle frame 0 in design units, one point per unit; a head rect is
// the union of the drawn head-* slots' image rects, the figure the union of every part's.
func Crop(bareFigure, head Rect, hatHead *Rect, unitsPerPx float64) Rect {
	if head.isEmpty() || bareFigure.isEmpty() || unitsPerPx <= 0 {
		return emptyRect()
	}
	centre := head.midX()
	if hatHead != nil && !hatHead.isEmpty() {
		centre = (head.midX() + hatHead.midX()) / 2
	}
	side := cropSidePx * unitsPerPx
	top := bareFigure.MinY - cropHeadroomPx*unitsPerPx
	return Rect{centre - side/2, top, centre + side/2, top + side}
}

type posed struct {
	r        *rig
	toCanvas []Affine
	seq      []int
	bounds   Rect // union of every drawn part's image rect (the app's clip.bounds)
}

// pose resolves idle frame 0, with `gear` (slot id → replacement art) dressed in the way
// fgc CTDrillsGearRigCache does: image, size and pivot change; bone, order and layer stay.
func pose(sk skeleton, ch character, idle clip, gear map[string]gearArt) (*posed, error) {
	dressed := ch
	if gear != nil {
		dressed.Slots = append([]slot(nil), ch.Slots...)
		for i, s := range dressed.Slots {
			a, ok := gear[s.ID]
			if !ok {
				continue
			}
			if a.Bone != "" && a.Bone != s.Bone {
				return nil, fmt.Errorf("gear slot %s is drawn for bone %s, rig has %s", s.ID, a.Bone, s.Bone)
			}
			s.ImageWidth, s.ImageHeight, s.PivotX, s.PivotY = a.ImageWidth, a.ImageHeight, a.PivotX, a.PivotY
			dressed.Slots[i] = s
		}
	}
	r, err := buildRig(sk, dressed)
	if err != nil {
		return nil, err
	}
	offs, order, err := frame0(r, idle)
	if err != nil {
		return nil, err
	}
	toCanvas, seq := r.resolve(offs, order)
	p := &posed{r: r, toCanvas: toCanvas, seq: seq, bounds: emptyRect()}
	for _, s := range seq {
		p.bounds = p.bounds.union(p.partRect(s))
	}
	return p, nil
}

func (p *posed) partRect(s int) Rect {
	sl := p.r.slots[s]
	return mapRect(p.toCanvas[s], float64(sl.ImageWidth), float64(sl.ImageHeight))
}

// headRect is the union of the drawn `head-*` slots' image rects (the app's headRect).
func (p *posed) headRect() Rect {
	out := emptyRect()
	for _, s := range p.seq {
		if isHead(p.r.slots[s].ID) {
			out = out.union(p.partRect(s))
		}
	}
	return out
}

func isHead(id string) bool { return strings.HasPrefix(id, "head-") }

type artFunc func(slot) (VecSlot, float64, bool)

// writer turns posed parts into symbol markup for one fighter; levels are shared by the
// fighter's bare and hatted looks so one set of CSS variables colours both.
type writer struct {
	box       Rect
	levels    map[float64]int
	levelList []float64
	// clip ids: "s<n>" for shared parts, "h<n>" for head slots, so a hat never shifts
	// the shared markup.
	clipPrefix string
	clipN      map[string]int
	// fixed is the gear being drawn when it has its own colour (the Mind Crown): its head
	// slots get a second, mask-clipped copy in that colour on top, and a halo behind.
	fixed *fixedSpec
}

func (w *writer) level(hex string) (int, error) {
	v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil || len(hex) != 7 {
		return 0, fmt.Errorf("bad colour %q", hex)
	}
	g := round(float64((v>>8)&0xFF)/255, 3)
	if i, ok := w.levels[g]; ok {
		return i, nil
	}
	w.levels[g] = len(w.levelList)
	w.levelList = append(w.levelList, g)
	return w.levels[g], nil
}

// parts returns the markup of the parts that reach the crop, in draw order, with an
// empty placeholder at each head slot, plus the head slots' own markup in that order.
func (w *writer) parts(p *posed, art artFunc) (shared, head []string, err error) {
	var run strings.Builder
	w.clipN, w.clipPrefix = map[string]int{}, "s"
	for _, s := range p.seq {
		sl := p.r.slots[s]
		if isHead(sl.ID) {
			var hb strings.Builder
			if p.partRect(s).intersects(w.box) {
				w.clipPrefix = "h"
				err := w.part(&hb, p, s, art)
				w.clipPrefix = "s"
				if err != nil {
					return nil, nil, fmt.Errorf("slot %s: %w", sl.ID, err)
				}
			}
			shared = append(shared, run.String(), "")
			run.Reset()
			head = append(head, hb.String())
			continue
		}
		if !p.partRect(s).intersects(w.box) {
			continue // nothing of this part reaches the headshot
		}
		if err := w.part(&run, p, s, art); err != nil {
			return nil, nil, fmt.Errorf("slot %s: %w", sl.ID, err)
		}
	}
	shared = append(shared, run.String())
	return shared, head, nil
}

func (w *writer) part(b *strings.Builder, p *posed, s int, art artFunc) error {
	sl := p.r.slots[s]
	v, ppu, ok := art(sl)
	if !ok {
		return fmt.Errorf("no vector art for %s", sl.Image)
	}
	if len(v.Origin) != 2 || len(v.Size) != 2 ||
		v.Size[0]*ppu != float64(sl.ImageWidth) || v.Size[1]*ppu != float64(sl.ImageHeight) {
		return fmt.Errorf("vector size does not match the rig")
	}
	// Bone-local art (y up, origin at the pivot) → image pixels → canvas, then shifted so
	// the crop's top-left is the symbol's origin.
	local := Affine{ppu, 0, 0, -ppu, ppu * v.Origin[0], ppu * v.Origin[1]}
	m := p.toCanvas[s].Mul(local)
	route := "m"
	if l := p.r.slotLayers[s]; l == 0 || l == 1 {
		route = "o"
	}
	sil, err := pathData(v.Silhouette)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, `<g transform="matrix(%s %s %s %s %s %s)">`,
		num(m.A, 4), num(m.B, 4), num(m.C, 4), num(m.D, 4), num(m.TX-w.box.MinX, 2), num(m.TY-w.box.MinY, 2))
	var fixedMask string
	if w.fixed != nil && isHead(sl.ID) {
		if fixedMask, err = pathData(w.fixed.mask.Silhouette); err != nil {
			return err
		}
		// The halo: the mask, blurred, behind the back head slot (the app bakes it into head-key).
		if w.fixed.glow && sl.ID == "head-key" {
			gid := w.nextID()
			fmt.Fprintf(b, `<filter id="%s" x="-50%%" y="-50%%" width="200%%" height="200%%"><feGaussianBlur stdDeviation="2.5"/></filter>`, gid)
			fmt.Fprintf(b, `<path d="%s" style="fill:%s" fill-opacity="0.75" filter="url(#%s)"/>`, fixedMask, rgbCSS(w.fixed.rgb, 1), gid)
		}
	}
	if err := w.layers(b, m, v, sil, func(i int) string { return fmt.Sprintf("var(--%s%d)", route, i) }); err != nil {
		return err
	}
	if fixedMask != "" {
		// The gear's own colour inside the mask, over the player-coloured art (same layers).
		mid := w.nextID()
		fmt.Fprintf(b, `<clipPath id="%s"><path d="%s"/></clipPath><g clip-path="url(#%s)">`, mid, fixedMask, mid)
		levels := w.levelList
		colour := func(i int) string { return rgbCSS(w.fixed.rgb, levels[i]) }
		if err := w.layers(b, m, v, sil, colour); err != nil {
			return err
		}
		b.WriteString(`</g>`)
	}
	b.WriteString(`</g>`)
	return nil
}

// nextID is the next per-symbol id ("{id}" is replaced per fighter symbol by the page).
func (w *writer) nextID() string {
	id := fmt.Sprintf("{id}%s%d", w.clipPrefix, w.clipN[w.clipPrefix])
	w.clipN[w.clipPrefix]++
	return id
}

// rgbCSS is level × a fixed colour, as the app's matrix draws a green level in that colour.
func rgbCSS(c [3]int, level float64) string {
	return fmt.Sprintf("rgb(%d,%d,%d)", int(float64(c[0])*level+0.5), int(float64(c[1])*level+0.5), int(float64(c[2])*level+0.5))
}

// layers writes one part's layers; colour names the paint for a level index.
func (w *writer) layers(b *strings.Builder, m Affine, v VecSlot, sil string, colour func(int) string) error {
	clipID := ""
	for _, l := range v.Layers {
		if l.Lattice != nil {
			if l.Stroke == nil || l.Width == nil {
				return fmt.Errorf("lattice without stroke")
			}
			var d strings.Builder
			for _, line := range l.Lattice {
				if len(line) < 4 || !w.reaches(m, line) {
					continue
				}
				d.WriteString("M" + num(line[0], 1) + " " + num(line[1], 1))
				for k := 2; k+1 < len(line); k += 2 {
					d.WriteString("L" + num(line[k], 1) + " " + num(line[k+1], 1))
				}
			}
			if d.Len() == 0 {
				continue
			}
			if clipID == "" {
				// "{id}" is replaced per fighter symbol by the page, so ids never collide.
				clipID = w.nextID()
				fmt.Fprintf(b, `<clipPath id="%s"><path d="%s"/></clipPath>`, clipID, sil)
			}
			li, err := w.level(*l.Stroke)
			if err != nil {
				return err
			}
			fmt.Fprintf(b, `<path clip-path="url(#%s)" d="%s" style="fill:none;stroke:%s" stroke-width="%s" stroke-linecap="round" stroke-linejoin="round"/>`,
				clipID, d.String(), colour(li), num(*l.Width, 2))
			continue
		}
		if l.Fill == nil {
			continue
		}
		fi, err := w.level(*l.Fill)
		if err != nil {
			return err
		}
		opacity := ""
		if l.Opacity != nil && *l.Opacity != 1 {
			opacity = ` fill-opacity="` + num(*l.Opacity, 3) + `"`
		}
		if l.Stroke != nil && l.Width != nil {
			si, err := w.level(*l.Stroke)
			if err != nil {
				return err
			}
			fmt.Fprintf(b, `<path d="%s" style="fill:%s;stroke:%s"%s stroke-width="%s" stroke-linejoin="round"/>`,
				sil, colour(fi), colour(si), opacity, num(*l.Width, 2))
		} else {
			fmt.Fprintf(b, `<path d="%s" style="fill:%s"%s/>`, sil, colour(fi), opacity)
		}
	}
	return nil
}

// reaches reports whether a polyline's canvas bounds touch the crop (with a stroke margin).
func (w *writer) reaches(m Affine, line []float64) bool {
	r := emptyRect()
	for k := 0; k+1 < len(line); k += 2 {
		x, y := m.apply(line[k], line[k+1])
		r = r.union(Rect{x, y, x, y})
	}
	r = Rect{r.MinX - 2, r.MinY - 2, r.MaxX + 2, r.MaxY + 2}
	return r.intersects(w.box)
}

// pathData converts the kit's op stream (0 move · 1 line · 2 r large sweep x y arc ·
// 3 close) to SVG path data in the same bone-local space. SVG arcs use the same endpoint
// parameterisation the kit converts from (SVG 1.1 F.6.5), so the flags carry over as-is.
func pathData(ops []float64) (string, error) {
	var b strings.Builder
	for i := 0; i < len(ops); {
		switch ops[i] {
		case 0, 1:
			if i+2 >= len(ops) {
				return "", fmt.Errorf("truncated op stream")
			}
			cmd := "M"
			if ops[i] == 1 {
				cmd = "L"
			}
			b.WriteString(cmd + num(ops[i+1], 1) + " " + num(ops[i+2], 1))
			i += 3
		case 2:
			if i+5 >= len(ops) {
				return "", fmt.Errorf("truncated arc")
			}
			r := num(ops[i+1], 1)
			fmt.Fprintf(&b, "A%s %s 0 %d %d %s %s", r, r, flag(ops[i+2]), flag(ops[i+3]), num(ops[i+4], 1), num(ops[i+5], 1))
			i += 6
		case 3:
			b.WriteString("Z")
			i++
		default:
			return "", fmt.Errorf("unknown op %v", ops[i])
		}
	}
	return b.String(), nil
}

func flag(v float64) int {
	if v != 0 {
		return 1
	}
	return 0
}

// num formats with at most `places` decimals and no trailing zeros, so output is stable.
func num(v float64, places int) string {
	s := strconv.FormatFloat(round(v, places), 'f', places, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

func val(p *float64, rest float64) float64 {
	if p == nil {
		return rest
	}
	return *p
}

func contains(list []string, s string) bool { return indexOf(list, s) >= 0 }

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func readJSON(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// BakeJSON is Bake encoded the one way the server embeds it: sorted keys (encoding/json
// sorts maps), no HTML escaping, a trailing newline.
func BakeJSON(in Inputs) ([]byte, error) {
	out, err := Bake(in)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}
