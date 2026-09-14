//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
void setTitleRuns(const char **texts, const double *rgb, const double *rgbLight, int n);
void setTemplateIconSized(const void *bytes, int length, double points, const double *rgb, const double *rgbLight);
void refreshIconAppearance(void);
*/
import "C"

import (
	"unsafe"

	"github.com/w4jnl/flok/internal/bar"
)

// rgbFor resolves a palette token for the dark and the light menu bar; an unknown or empty
// token means the label colour (a negative red).
func rgbFor(token string) (dark, light [3]C.double) {
	dark, light = [3]C.double{-1, -1, -1}, [3]C.double{-1, -1, -1}
	if c, ok := bar.Palette[token]; ok {
		dark = [3]C.double{C.double(c[0]), C.double(c[1]), C.double(c[2])}
		light = dark
		if l, ok := bar.PaletteLight[token]; ok {
			light = [3]C.double{C.double(l[0]), C.double(l[1]), C.double(l[2])}
		}
	}
	return dark, light
}

// setTemplateIcon installs a PNG at pts points (see title_darwin.m), tinted with a palette
// token or left as a template when the token is empty.
func setTemplateIcon(png []byte, pts int, color string) {
	if pts < 12 || pts > 22 {
		pts = 18
	}
	dark, light := rgbFor(color)
	C.setTemplateIconSized(unsafe.Pointer(&png[0]), C.int(len(png)), C.double(pts), &dark[0], &light[0])
}

// refreshIconAppearance re-tints the icon if the menu bar changed between light and dark.
func refreshIconAppearance() { C.refreshIconAppearance() }

// setColoredTitle pushes the title as coloured runs (see title_darwin.m); a run whose colour
// token is unknown or empty uses the system label colour.
func setColoredTitle(runs []bar.Run) {
	if len(runs) == 0 {
		runs = []bar.Run{{Text: ""}}
	}
	texts := make([]*C.char, len(runs))
	rgb := make([]C.double, len(runs)*3)
	rgbLight := make([]C.double, len(runs)*3)
	for i, r := range runs {
		texts[i] = C.CString(r.Text)
		dark, light := rgbFor(r.Color)
		copy(rgb[i*3:], dark[:])
		copy(rgbLight[i*3:], light[:])
	}
	C.setTitleRuns((**C.char)(unsafe.Pointer(&texts[0])), &rgb[0], &rgbLight[0], C.int(len(runs)))
	for _, t := range texts {
		C.free(unsafe.Pointer(t))
	}
}
