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

// TitleRuns is Title split into coloured runs: the spinner in the working colour, the badge in
// the attention colour, the idle glyph and the not-running dash in the label colour.
func TitleRuns(s snapshot.Snapshot, f snapshot.Freshness, frame int, o Options) []Run {
	if f != snapshot.Fresh {
		return []Run{{Text: "–"}}
	}
	glyph := Run{Text: "○"}
	if Working(s) {
		glyph = Run{Text: "◐", Color: "working"}
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
