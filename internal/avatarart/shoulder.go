package avatarart

import (
	"fmt"
	"math"
)

// shoulderCat bakes a single fixed-paint, frame-zero companion behind the body.
// Placement uses the bare idle pose so hats and outfits cannot move the seat.
// Everything here runs at authoring time; the website only reads static SVG.
func shoulderCat(p *posed, crop Rect) string {
	torso, ok := p.r.boneIndex["torso"]
	if !ok || len(p.boneToCanvas) <= torso {
		return ""
	}
	head := -1
	for i, slot := range p.r.slots {
		if slot.ID == "head-fill" {
			head = i
			break
		}
	}
	if head < 0 || p.bounds.isEmpty() {
		return ""
	}
	slot, ht := p.r.slots[head], p.toCanvas[head]
	center, top := ht.apply(float64(slot.ImageWidth)/2, 0)
	_, otherTop := ht.apply(float64(slot.ImageWidth), 0)
	_, firstTop := ht.apply(0, 0)
	top = math.Min(firstTop, otherTop)
	height := p.bounds.MaxY - p.bounds.MinY
	scale := height * 1.45
	bone := p.boneToCanvas[torso]
	// Same bone-relative seat and signed scales as the native static headshot.
	transform := Affine{-scale * bone.A, -scale * bone.B, scale * bone.C, scale * bone.D,
		center - 0.09*height - crop.MinX, top + 0.13*height - crop.MinY}
	return catSVG(transform)
}

func catSVG(m Affine) string {
	// The two-second idle starts with a slight breath rise and head tilt. No clock
	// is needed on a score row; every avatar uses this same stable pose.
	const edge = "#6e6878"
	const fill = "#14121a"
	const key = "#08070c"
	const eye = "#8fe36b"
	shape := func(tag, geometry string) string {
		return fmt.Sprintf(`<%s %s fill="%s" stroke="%s" stroke-width=".0093"/><%s %s fill="none" stroke="%s" stroke-width=".0043"/>`, tag, geometry, fill, key, tag, geometry, edge)
	}
	ellipse := func(x, y, rx, ry, angle float64) string {
		return `<g transform="translate(` + num(x, 7) + ` ` + num(y, 7) + `) rotate(` + num(angle, 7) + `)">` + shape("ellipse", `cx="0" cy="0" rx="`+num(rx, 7)+`" ry="`+num(ry, 7)+`"`) + `</g>`
	}
	tail := `d="M-.024 .020 C-.030 .034 -.022 .050 -.008 .046 C0 .044 -.002 .036 -.008 .037"`
	markup := fmt.Sprintf(`<g transform="matrix(%s %s %s %s %s %s)" stroke-linecap="round" stroke-linejoin="round">`, num(m.A, 7), num(m.B, 7), num(m.C, 7), num(m.D, 7), num(m.TX, 7), num(m.TY, 7))
	markup += `<path ` + tail + ` fill="none" stroke="` + key + `" stroke-width=".017"/><path ` + tail + ` fill="none" stroke="` + edge + `" stroke-width=".0108"/><path ` + tail + ` fill="none" stroke="` + fill + `" stroke-width=".0042"/>`
	markup += ellipse(.002, .01775, .028, .015, -.05*180/math.Pi) + ellipse(.018, .004, .007, .0045, 0) + ellipse(.030, .004, .007, .0045, 0)
	markup += `<g transform="translate(.030 .02775) rotate(` + num(.16*math.Sin(.8)*180/math.Pi, 7) + `)">`
	markup += shape("path", `d="M-.012 .009 L-.009 .026 L-.001 .013 Z"`) + shape("path", `d="M.003 .013 L.011 .026 L.014 .008 Z"`) + ellipse(0, 0, .017, .014, 0)
	markup += `<defs><radialGradient id="{id}pet-eye"><stop stop-color="` + eye + `" stop-opacity=".45"/><stop offset="1" stop-color="` + eye + `" stop-opacity="0"/></radialGradient></defs>`
	for _, x := range []float64{-.005, .007} {
		markup += `<circle cx="` + num(x, 7) + `" cy=".002" r=".0072" fill="url(#{id}pet-eye)"/><circle cx="` + num(x, 7) + `" cy=".002" r=".003" fill="` + eye + `"/>`
	}
	return markup + `</g></g>`
}
