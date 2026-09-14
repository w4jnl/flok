// Command gen renders flok-bar's menu bar template icons (black + alpha PNGs, macOS tints them
// per appearance) from the S3 mark: a terminal bracket holding one chevron and a cursor. The
// bracket is the tmux server, the chevron is the flock, the cursor is what an agent is waiting
// on. A thin frame (2.9) against a heavy bird (5.3) makes the bracket read as a container, not
// a second subject. flok.png and flok-dot.png differ only in the cursor: a bar at rest, the
// "4" dot from w4j when an agent is blocked or finished unseen — a shape change, which is what
// survives monochrome rendering at 16px.
//
// Geometry is on a 44px box (22pt @2x) and scaled for the 1x (22px) and 3x (66px) files. The
// committed PNGs are the designer's export of the same geometry (assets/brand/flok-mark.svg);
// this renderer reproduces them to within anti-aliasing, so `make icons` is for tweaking the
// geometry, not something the build needs.
//
//	go run ./assets/icons/gen assets/icons
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const (
	box    = 44.0 // reference box: 22pt @2x
	wFrame = 2.9  // thin frame — recedes
	wBird  = 5.3  // heavy bird — dominates (1.8x contrast)
	frameR = 4.0  // W4J corner radius
)

type canvas struct {
	*image.NRGBA
	n int     // pixels per side
	s float64 // scale from the 44px reference box
}

func newCanvas(px int) canvas {
	return canvas{image.NewNRGBA(image.Rect(0, 0, px, px)), px, float64(px) / box}
}

// paint blends coverage into the alpha channel (union of shapes).
func (c canvas) paint(x, y int, cov float64) {
	if cov <= 0 || x < 0 || y < 0 || x >= c.n || y >= c.n {
		return
	}
	old := c.NRGBAAt(x, y)
	a := 1 - (1-float64(old.A)/255)*(1-cov)
	c.SetNRGBA(x, y, color.NRGBA{0, 0, 0, uint8(math.Round(a * 255))})
}

// segment strokes an anti-aliased line with round caps; coordinates are in reference units.
func (c canvas) segment(x0, y0, x1, y1, width float64) {
	x0, y0, x1, y1, width = x0*c.s, y0*c.s, x1*c.s, y1*c.s, width*c.s
	r := width / 2
	minX, maxX := int(math.Floor(math.Min(x0, x1)-r-1)), int(math.Ceil(math.Max(x0, x1)+r+1))
	minY, maxY := int(math.Floor(math.Min(y0, y1)-r-1)), int(math.Ceil(math.Max(y0, y1)+r+1))
	dx, dy := x1-x0, y1-y0
	len2 := dx*dx + dy*dy
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			t := 0.0
			if len2 > 0 {
				t = math.Max(0, math.Min(1, ((px-x0)*dx+(py-y0)*dy)/len2))
			}
			d := math.Hypot(px-(x0+t*dx), py-(y0+t*dy))
			c.paint(x, y, math.Max(0, math.Min(1, r+0.5-d)))
		}
	}
}

// arc strokes a circular arc from angle a0 to a1 (radians, y down) as a chain of short segments.
func (c canvas) arc(cx, cy, r, a0, a1, width float64) {
	const steps = 12
	for i := 0; i < steps; i++ {
		t0, t1 := a0+(a1-a0)*float64(i)/steps, a0+(a1-a0)*float64(i+1)/steps
		c.segment(cx+r*math.Cos(t0), cy+r*math.Sin(t0), cx+r*math.Cos(t1), cy+r*math.Sin(t1), width)
	}
}

// bracket draws one C-shaped terminal bracket: a stem on x = stemX between yTop and yBot whose
// rounded corners turn into short returns reaching x = returnTo. The returns point right when
// returnTo > stemX (a "[") and left otherwise (a "]").
func (c canvas) bracket(stemX, returnTo, yTop, yBot, r, w float64) {
	dir := 1.0
	if returnTo < stemX {
		dir = -1
	}
	c.segment(stemX, yTop+r, stemX, yBot-r, w)
	cx := stemX + dir*r
	// corner arcs run the short way from the stem's angle to the return's: a "[" stem sits at
	// 180° of its corner centres, its top return at 270° (3π/2) and bottom return at 90°; a "]"
	// stem sits at 0°, returns at -90° and 90°.
	stemAngle, topAngle := math.Pi, 3*math.Pi/2
	if dir < 0 {
		stemAngle, topAngle = 0, -math.Pi/2
	}
	c.arc(cx, yTop+r, r, stemAngle, topAngle, w)
	c.arc(cx, yBot-r, r, stemAngle, math.Pi/2, w)
	c.segment(cx, yTop, returnTo, yTop, w)
	c.segment(cx, yBot, returnTo, yBot, w)
}

func (c canvas) chevron(apexX, apexY, halfW, h, width float64) {
	c.segment(apexX-halfW, apexY+h, apexX, apexY, width)
	c.segment(apexX, apexY, apexX+halfW, apexY+h, width)
}

// bar is a pill: a rounded rectangle whose corner radius is half its height.
func (c canvas) bar(x, y, w, h float64) {
	c.segment(x+h/2, y+h/2, x+w-h/2, y+h/2, h)
}

func (c canvas) dot(cx, cy, r float64) {
	cx, cy, r = cx*c.s, cy*c.s, r*c.s
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			c.paint(x, y, math.Max(0, math.Min(1, r+0.5-d)))
		}
	}
}

// mark renders the S3 mark at px pixels per side; waiting swaps the cursor bar for the dot.
func mark(px int, waiting bool) canvas {
	c := newCanvas(px)
	c.bracket(6.97, 14.67, 5.9, 38.1, frameR, wFrame)  // [
	c.bracket(37.03, 29.33, 5.9, 38.1, frameR, wFrame) // ]
	c.chevron(22, 14.7, 8.6, 8.3, wBird)               // the bird, centred on x=22
	if waiting {
		c.dot(22, 31.7, 4.1) // the "4" dot
	} else {
		c.bar(17.6, 29.7, 8.8, 4.0) // cursor at rest
	}
	return c
}

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	for suffix, px := range map[string]int{"": 22, "@2x": 44, "@3x": 66} {
		for name, waiting := range map[string]bool{"flok": false, "flok-dot": true} {
			f, err := os.Create(filepath.Join(dir, name+suffix+".png"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			if err := png.Encode(f, mark(px, waiting)); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			f.Close()
		}
	}
}
