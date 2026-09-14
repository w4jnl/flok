// Command gen renders flok-bar's menu bar template icons (black + alpha PNGs, macOS tints them
// per appearance) from the S3 mark: a terminal bracket holding one chevron and a cursor. The
// bracket is the tmux server, the chevron is the flock, the cursor is what an agent is waiting
// on. A thin frame (2.9) against a heavy bird (5.3) makes the bracket read as a container, not
// a second subject. The waiting icon (flok-dot.png, the name is historical) is a solid
// inversion: one filled tile with the mark knocked out in negative. At menu bar size the eye
// reliably catches value, not detail, so the state lives in the whole icon rather than in a
// cursor swap (ink coverage x3, nothing thinner than 2.3px).
//
// Geometry is on a 44px box (22pt @2x) and scaled for the 1x (22px) and 3x (66px) files. The
// committed PNGs come from this renderer (`make icons`), which follows the designer's masters
// (assets/brand/flok-mark.svg, flok-mark-dot.svg).
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

// roundRect fills a rounded rectangle (anti-aliased through its signed distance).
func (c canvas) roundRect(x, y, w, h, r float64) {
	x, y, w, h, r = x*c.s, y*c.s, w*c.s, h*c.s, r*c.s
	cx, cy, hw, hh := x+w/2, y+h/2, w/2-r, h/2-r
	for py := int(math.Floor(y - 1)); py <= int(math.Ceil(y+h+1)); py++ {
		for px := int(math.Floor(x - 1)); px <= int(math.Ceil(x+w+1)); px++ {
			qx, qy := math.Abs(float64(px)+0.5-cx)-hw, math.Abs(float64(py)+0.5-cy)-hh
			d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
			c.paint(px, py, math.Max(0, math.Min(1, 0.5-d)))
		}
	}
}

// erase knocks a mask out of the canvas: coverage becomes coverage x (1 - mask).
func (c canvas) erase(mask canvas) {
	for y := 0; y < c.n; y++ {
		for x := 0; x < c.n; x++ {
			if m := mask.NRGBAAt(x, y).A; m > 0 {
				old := c.NRGBAAt(x, y)
				c.SetNRGBA(x, y, color.NRGBA{0, 0, 0, uint8(math.Round(float64(old.A) * (1 - float64(m)/255)))})
			}
		}
	}
}

// mark renders the S3 mark at px pixels per side: the outline at rest, the solid inversion
// while an agent waits on the user.
func mark(px int, waiting bool) canvas {
	c := newCanvas(px)
	if waiting {
		c.roundRect(4.8, 4.0, 34.4, 35.9, 9.5) // one filled tile in place of the brackets
		knock := newCanvas(px)
		knock.chevron(22, 15.6, 8.6, 8.3, 5.7) // the bird and the cursor, in negative
		knock.bar(15.4, 30.1, 13.2, 4.2)       // cursor widened like the outline's, see below
		c.erase(knock)
		return c
	}
	// The master puts the stems at 6.97 / 37.03, 2.2 units inside the solid tile's edges, which
	// makes the outline read taller than wide at menu bar size. The icons put the stems' outer
	// edge on the tile's edge (4.8 / 39.2) so both states share one footprint; returns keep
	// their 7.7-unit length.
	c.bracket(6.25, 13.95, 5.9, 38.1, frameR, wFrame)  // [
	c.bracket(37.75, 30.05, 5.9, 38.1, frameR, wFrame) // ]
	c.chevron(22, 14.7, 8.6, 8.3, wBird)               // the bird, centred on x=22
	// cursor at rest. The master draws it 8.8 x 4.0 with 2.0 corners, a 4.8-unit straight run
	// that renders as a 7 x 3 px blob at 18pt and reads as a dot; 13.2 wide (3.3:1) reads as
	// the bar it is. Same height and centre line as the master.
	c.bar(15.4, 29.7, 13.2, 4.0)
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
