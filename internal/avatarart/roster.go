package avatarart

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// RosterEntry is one fighter as fgc CTCore/FighterRoster.swift declares it.
type RosterEntry struct {
	BodyID  string
	Palette []string
	Bonus   []string
}

var (
	// `.init("puppet", "STANDARD", …, palette: [ … ], bonus: [CTCharacterBonusLook.ex01.id]),`
	paletteRef   = regexp.MustCompile(`palette:\s*\[([^\]]*)\]`)
	quoted       = regexp.MustCompile(`"([a-z0-9-]+)"`)
	bonusRef     = regexp.MustCompile(`bonus:\s*\[([^\]]*)\]`)
	bonusLookDef = regexp.MustCompile(`static let (\w+) = Self\(id: "([a-z0-9-]+)"`)
	// `.init(id: "default-blue", name: "Player One Cyan", rgb: CTRGB(r: 100, g: 210, b: 255)),`
	colourInit = regexp.MustCompile(`\.init\(id:\s*"([a-z0-9-]+)",[^)]*?rgb:\s*CTRGB\(r:\s*([0-9.]+),\s*g:\s*([0-9.]+),\s*b:\s*([0-9.]+)\)`)
)

// ParseRoster reads the fighters, their palettes (colour ids, in order) and bonus looks.
func ParseRoster(path string) ([]RosterEntry, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(src)
	start := strings.Index(text, "static let all: [CTFighterDefinition] = [")
	if start < 0 {
		return nil, fmt.Errorf("%s: roster list not found", path)
	}
	text = text[start:]
	bonusIDs := map[string]string{}
	for _, m := range bonusLookDef.FindAllStringSubmatch(string(src), -1) {
		bonusIDs[m[1]] = m[2]
	}
	if end := strings.Index(text, "\n    ]\n"); end > 0 {
		text = text[:end]
	}
	var out []RosterEntry
	// One chunk per `.init("<body>", …)` entry.
	for _, chunk := range strings.Split(text, `.init("`)[1:] {
		id := chunk[:strings.Index(chunk, `"`)]
		p := paletteRef.FindStringSubmatch(chunk)
		if p == nil {
			return nil, fmt.Errorf("%s: fighter %s has no palette", path, id)
		}
		e := RosterEntry{BodyID: id}
		for _, q := range quoted.FindAllStringSubmatch(p[1], -1) {
			e.Palette = append(e.Palette, q[1])
		}
		if b := bonusRef.FindStringSubmatch(chunk); b != nil {
			for _, ref := range strings.Split(b[1], ",") {
				ref = strings.TrimSpace(ref)
				name := strings.TrimSuffix(strings.TrimPrefix(ref, "CTCharacterBonusLook."), ".id")
				if id, ok := bonusIDs[name]; ok {
					e.Bonus = append(e.Bonus, id)
				} else if ref != "" {
					return nil, fmt.Errorf("%s: unknown bonus look %q", e.BodyID, ref)
				}
			}
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no fighters parsed", path)
	}
	return out, nil
}

// ParseColours reads the named-colour catalog: id → RGB.
func ParseColours(path string) (map[string][3]int, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string][3]int{}
	for _, m := range colourInit.FindAllStringSubmatch(string(src), -1) {
		var rgb [3]int
		for i := 0; i < 3; i++ {
			f, err := strconv.ParseFloat(m[2+i], 64)
			if err != nil {
				return nil, err
			}
			rgb[i] = int(math.Round(f))
		}
		out[m[1]] = rgb
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no colours parsed", path)
	}
	return out, nil
}
