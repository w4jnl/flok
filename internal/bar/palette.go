package bar

import (
	"fmt"

	"github.com/w4jnl/flok/internal/snapshot"
)

// Run is one coloured piece of the status-item title. Color is a palette token ("" = the
// system's label colour), resolved by the platform code; the pure functions here never see RGB.
type Run struct {
	Text  string
	Color string
}

// Palette is flok's state palette from assets/brand/README.md, as sRGB in [0,1].
var Palette = map[string][3]float64{
	"working":   {0x3f / 255.0, 0xd0 / 255.0, 0xd4 / 255.0}, // #3FD0D4
	"attention": {0xe8 / 255.0, 0x96 / 255.0, 0x3c / 255.0}, // #E8963C blocked / waiting for you
	"done":      {0x5f / 255.0, 0xc4 / 255.0, 0x7a / 255.0}, // #5FC47A
	"idle":      {0x8a / 255.0, 0x99 / 255.0, 0x9c / 255.0}, // #8A999C
}

// TitleRuns is Title split into coloured runs: only the badge is coloured (attention); the
// spinner, the idle glyph and the not-running dash stay in the label colour — the icon already
// carries the working colour, and a settled state deserves the accent more than motion does.
func TitleRuns(s snapshot.Snapshot, f snapshot.Freshness, frame int, o Options) []Run {
	if f != snapshot.Fresh {
		return []Run{{Text: "–"}}
	}
	glyph := Run{Text: "○"}
	if Working(s) {
		glyph = Run{Text: "◐"}
		if o.Animate {
			glyph.Text = Frames[((frame%len(Frames))+len(Frames))%len(Frames)]
		}
	}
	runs := []Run{glyph}
	if n := Pending(s); o.Badge && n > 0 {
		runs = append(runs, Run{Text: fmt.Sprintf(" ● %d", n), Color: "attention"})
	}
	return runs
}

// Join is the plain text of the runs (what Title returns; tests and non-colour platforms).
func Join(runs []Run) string {
	out := ""
	for _, r := range runs {
		out += r.Text
	}
	return out
}

// PaletteLight is the same palette for a light menu bar, where the bright working teal washes
// out: W4J's teal takes its place (assets/brand/README.md); the others carry.
var PaletteLight = map[string][3]float64{
	"working":   {0x12 / 255.0, 0x99 / 255.0, 0x9d / 255.0}, // #12999D
	"attention": Palette["attention"],
	"done":      Palette["done"],
	"idle":      Palette["idle"],
}

// IconColor is the palette token the icon is tinted with: attention while an agent waits on
// the user, working while one works, "" (the template image, in the menu bar's own colour) at
// rest.
func IconColor(pending, working bool) string {
	switch {
	case pending:
		return "attention"
	case working:
		return "working"
	}
	return ""
}

// Solid says which icon to show: the outline at rest, the solid inversion while an agent waits.
// With blink on and the terminal unfocused the two alternate by phase (~1150 ms per step):
// motion outranks any static difference at menu bar size, and the mark contains a terminal
// cursor, so blinking is the one animation it has a right to. Focus stops it.
func Solid(pending, blink, terminalFocused bool, phase int) bool {
	if !pending {
		return false
	}
	if !blink || terminalFocused {
		return true
	}
	return phase%2 == 0
}
