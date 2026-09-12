// Command gen renders flok-bar's menu bar template icons (black + alpha PNGs, macOS tints them
// per appearance): a flock of three monoline chevrons in the spirit of the W4J mark (its
// inverted-V "W", round caps), and a variant with the "4"'s dot for pending attention.
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

const size = 44 // 22pt @2x

type canvas struct{ *image.NRGBA }

func newCanvas() canvas { return canvas{image.NewNRGBA(image.Rect(0, 0, size, size))} }

// paint blends coverage into the alpha channel (union of shapes).
func (c canvas) paint(x, y int, cov float64) {
	if cov <= 0 || x < 0 || y < 0 || x >= size || y >= size {
		return
	}
	old := c.NRGBAAt(x, y)
	a := 1 - (1-float64(old.A)/255)*(1-cov)
	c.SetNRGBA(x, y, color.NRGBA{0, 0, 0, uint8(math.Round(a * 255))})
}

// segment strokes an anti-aliased line with round caps of the given width.
func (c canvas) segment(x0, y0, x1, y1, width float64) {
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

func (c canvas) chevron(apexX, apexY, halfW, h, width float64) {
	c.segment(apexX-halfW, apexY+h, apexX, apexY, width)
	c.segment(apexX, apexY, apexX+halfW, apexY+h, width)
}

func (c canvas) dot(cx, cy, r float64) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			c.paint(x, y, math.Max(0, math.Min(1, r+0.5-d)))
		}
	}
}

func (c canvas) clearDisc(cx, cy, r float64) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			if x < 0 || y < 0 || x >= size || y >= size {
				continue
			}
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			if keep := math.Max(0, math.Min(1, d-r+0.5)); keep < 1 {
				old := c.NRGBAAt(x, y)
				c.SetNRGBA(x, y, color.NRGBA{0, 0, 0, uint8(float64(old.A) * keep)})
			}
		}
	}
}

// flock: a large chevron leading, two smaller ones trailing right, like birds in formation.
func flock() canvas {
	c := newCanvas()
	const w = 3.6
	c.chevron(12, 12, 9, 17, w) // leader
	c.chevron(31, 20, 6, 11, w) // second, lower right
	c.chevron(37, 6, 5, 9, w)   // third, high right
	return c
}

// flockDot: the third bird gives way to the W4J "4" dot = agents waiting for you.
func flockDot() canvas {
	c := newCanvas()
	const w = 3.6
	c.chevron(12, 12, 9, 17, w)
	c.chevron(31, 20, 6, 11, w)
	c.dot(37, 9, 4.5)
	return c
}

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	for name, cv := range map[string]canvas{"flok.png": flock(), "flok-dot.png": flockDot()} {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := png.Encode(f, cv); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		f.Close()
	}
}
