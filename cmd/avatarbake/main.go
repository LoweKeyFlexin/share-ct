// Command avatarbake regenerates internal/server/static/board-avatars.json, the website's
// board-avatar headshots, from the fighter art the app ships. Run it whenever the kit pin,
// the pirate-hat art, the kit's cosmetics (Outfit 2, helmets), the roster or the colour catalog
// changes:
//
//	go run ./cmd/avatarbake -kit <fighter-engine-kit checkout> -fgc <controller-tester-fgc checkout>
//
// Both sources are private repositories; only the baked headshot paths land here.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/LoweKeyFlexin/share-ct/internal/avatarart"
)

func main() {
	kit := flag.String("kit", "", "fighter-engine-kit checkout (the revision fgc's Package.resolved pins)")
	fgc := flag.String("fgc", "", "controller-tester-fgc checkout")
	out := flag.String("out", "internal/server/static/board-avatars.json", "output file")
	flag.Parse()
	if *kit == "" || *fgc == "" {
		flag.Usage()
		os.Exit(2)
	}
	data, err := avatarart.BakeJSON(avatarart.Inputs{
		KitResources: filepath.Join(*kit, "PuppetPresentation/Resources/PuppetPresentation"),
		GearDir:      filepath.Join(*fgc, "Sources/CTDrillsDiagnostic/Gear/pirate-hat"),
		Roster:       filepath.Join(*fgc, "CTCore/FighterRoster.swift"),
		Colours:      filepath.Join(*fgc, "CTCore/DrillsColourCatalog.swift"),
		Cosmetics:    filepath.Join(*kit, "PuppetPresentation/Resources/PuppetPresentation/cosmetics"),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "avatarbake:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "avatarbake:", err)
		os.Exit(1)
	}
	var o avatarart.Output
	_ = json.Unmarshal(data, &o)
	fmt.Printf("wrote %s: %d bytes, %d fighters\n", *out, len(data), len(o.Fighters))
}
