package avatarart

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The kit's exported cosmetics (fighter-engine-kit PuppetPresentation/Resources/
// PuppetPresentation/cosmetics), decoded only as far as the headshot needs: Outfit 2's
// replacement slots, vectors and ten colours, and the kit's head gear with its
// outfit-specific variants. The app dresses a body the same way (fgc CTDrillsGearRigCache:
// the outfit's slots first, then the gear's head slots over them; gear drawn for the
// outfit replaces the gear's own art).

type cosmeticCatalog struct {
	Outfits []struct {
		ID           string `json:"id"`
		Ordinal      int    `json:"ordinal"`
		Presentation string `json:"presentation"`
		Resource     string `json:"resource"`
		Vectors      string `json:"vectors"`
		Palette      string `json:"palette"`
	} `json:"outfits"`
	Gear []struct {
		ID       string `json:"id"`
		Resource string `json:"resource"`
	} `json:"gear"`
}

type outfitDoc struct {
	Replaces map[string]gearArt `json:"replaces"`
}

type paletteDoc struct {
	Colors []struct {
		Hex string `json:"hex"`
	} `json:"colors"`
}

type kitGearDoc struct {
	Replaces []string                                 `json:"replaces"`
	Bodies   map[string]map[string]gearArt            `json:"bodies"`
	Outfits  map[string]map[string]map[string]gearArt `json:"outfits"`
}

// headGear is one item's head art on one body for one outfit, with the vectors it reads.
type headGear struct {
	art map[string]gearArt
	vec vecDoc
	// key reads a slot's vector key from its image path (the kit keys gear by file name).
	key func(image string) string
}

func hexRGB(s string) ([3]int, error) {
	v, err := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
	if err != nil || len(s) != 7 {
		return [3]int{}, fmt.Errorf("bad colour %q", s)
	}
	return [3]int{int(v >> 16 & 0xFF), int(v >> 8 & 0xFF), int(v & 0xFF)}, nil
}

// bakeCosmetics adds the kit's helmets (on Outfit 1) and every outfit of `body` to `fi`.
// `pirate` is the host pirate hat's Outfit 1 art for this body (nil = none), read from
// `pirateDir` (fgc's Gear/pirate-hat).
func bakeCosmetics(in Inputs, sk skeleton, ch character, idle clip, w *writer, fi *Fighter, body string,
	pirate map[string]gearArt, pirateDir string, bareArt artFunc) error {
	var cat cosmeticCatalog
	if err := readJSON(filepath.Join(in.Cosmetics, "catalog.json"), &cat); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	root := filepath.Dir(in.Cosmetics) // catalog paths are relative to the resource root
	byName := func(image string) string { return filepath.Base(image) }

	// item -> outfit ("" = Outfit 1) -> this body's head gear.
	gearFor := map[string]map[string]*headGear{}
	for _, g := range cat.Gear {
		var doc kitGearDoc
		if err := readJSON(filepath.Join(root, g.Resource), &doc); err != nil {
			return err
		}
		dir := filepath.Dir(filepath.Join(root, g.Resource))
		load := func(art map[string]gearArt) (*headGear, error) {
			if len(art) == 0 {
				return nil, nil
			}
			var any gearArt
			for _, a := range art {
				any = a
				break
			}
			var vec vecDoc
			if err := readJSON(filepath.Join(dir, filepath.Dir(any.Image), "parts.vec.json"), &vec); err != nil {
				return nil, err
			}
			return &headGear{art: art, vec: vec, key: byName}, nil
		}
		gearFor[g.ID] = map[string]*headGear{}
		h, err := load(doc.Bodies[body])
		if err != nil {
			return err
		}
		if h != nil {
			gearFor[g.ID][""] = h
		}
		for outfit, bodies := range doc.Outfits {
			if h, err = load(bodies[body]); err != nil {
				return err
			}
			if h != nil {
				gearFor[g.ID][outfit] = h
			}
		}
	}
	if pirate != nil {
		var vec vecDoc
		if err := readJSON(filepath.Join(pirateDir, body, "parts.vec.json"), &vec); err != nil {
			return err
		}
		if gearFor[framingHat] == nil {
			gearFor[framingHat] = map[string]*headGear{}
		}
		gearFor[framingHat][""] = &headGear{art: pirate, vec: vec, key: byName}
	}
	items := make([]string, 0, len(gearFor))
	for id := range gearFor {
		items = append(items, id)
	}
	sort.Strings(items)

	// dressed poses `dress` (outfit slots, then gear head slots) and returns its markup.
	dressed := func(outfitArt map[string]gearArt, outfitVec *vecDoc, g *headGear) (shared, head []string, err error) {
		dress := map[string]gearArt{}
		for k, v := range outfitArt {
			dress[k] = v
		}
		if g != nil {
			for k, v := range g.art {
				dress[k] = v
			}
		}
		p, err := pose(sk, ch, idle, dress)
		if err != nil {
			return nil, nil, err
		}
		art := func(s slot) (VecSlot, float64, bool) {
			if g != nil {
				if a, ok := g.art[s.ID]; ok {
					v, ok := g.vec.Slots[g.key(a.Image)]
					return v, g.vec.RasterPixelsPerDesignUnit, ok
				}
			}
			if a, ok := outfitArt[s.ID]; ok {
				v, ok := outfitVec.Slots[a.Image]
				return v, outfitVec.RasterPixelsPerDesignUnit, ok
			}
			return bareArt(s)
		}
		return w.parts(p, art)
	}

	// Outfit 1 + the kit's helmets (the pirate hat is already Hat).
	for _, id := range items {
		g := gearFor[id][""]
		if id == framingHat || g == nil {
			continue
		}
		shared, head, err := dressed(nil, nil, g)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		if strings.Join(shared, "|") != strings.Join(fi.Parts, "|") || len(head) != len(fi.Head) {
			return fmt.Errorf("%s changed parts other than the head", id)
		}
		if fi.Gear == nil {
			fi.Gear = map[string][]string{}
		}
		fi.Gear[id] = head
	}

	for _, o := range cat.Outfits {
		if o.Presentation != body {
			continue
		}
		var doc outfitDoc
		if err := readJSON(filepath.Join(root, o.Resource), &doc); err != nil {
			return err
		}
		var vec vecDoc
		if err := readJSON(filepath.Join(root, o.Vectors), &vec); err != nil {
			return err
		}
		if vec.Format != "puppet-part-vectors" || vec.SchemaVersion != 1 {
			return fmt.Errorf("%s: unsupported vector document", o.ID)
		}
		var pal paletteDoc
		if err := readJSON(filepath.Join(root, o.Palette), &pal); err != nil {
			return err
		}
		if len(pal.Colors) != 10 {
			return fmt.Errorf("%s: %d colours, want 10", o.ID, len(pal.Colors))
		}
		art := OutfitArt{}
		for _, c := range pal.Colors {
			rgb, err := hexRGB(c.Hex)
			if err != nil {
				return err
			}
			art.Palette = append(art.Palette, rgb)
		}
		var err error
		if art.Parts, art.Head, err = dressed(doc.Replaces, &vec, nil); err != nil {
			return fmt.Errorf("%s: %w", o.ID, err)
		}
		for _, id := range items {
			g := gearFor[id][o.ID]
			if g == nil {
				g = gearFor[id][""]
			}
			if g == nil {
				continue
			}
			shared, head, err := dressed(doc.Replaces, &vec, g)
			if err != nil {
				return fmt.Errorf("%s+%s: %w", o.ID, id, err)
			}
			if strings.Join(shared, "|") != strings.Join(art.Parts, "|") || len(head) != len(art.Head) {
				return fmt.Errorf("%s+%s changed parts other than the head", o.ID, id)
			}
			if art.Gear == nil {
				art.Gear = map[string][]string{}
			}
			art.Gear[id] = head
		}
		if fi.Outfits == nil {
			fi.Outfits = map[string]OutfitArt{}
		}
		fi.Outfits[strconv.Itoa(o.Ordinal)] = art
	}
	return nil
}
