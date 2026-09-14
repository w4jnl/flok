//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
void setTitleRuns(const char **texts, const double *rgb, int n);
void setTemplateIconSized(const void *bytes, int length, double points);
*/
import "C"

import (
	"unsafe"

	"github.com/w4jnl/flok/internal/bar"
)

// setTemplateIcon installs a template PNG at pts points (see title_darwin.m).
func setTemplateIcon(png []byte, pts int) {
	if pts < 12 || pts > 22 {
		pts = 18
	}
	C.setTemplateIconSized(unsafe.Pointer(&png[0]), C.int(len(png)), C.double(pts))
}

// setColoredTitle pushes the title as coloured runs (see title_darwin.m); a run whose colour
// token is unknown or empty uses the system label colour.
func setColoredTitle(runs []bar.Run) {
	if len(runs) == 0 {
		runs = []bar.Run{{Text: ""}}
	}
	texts := make([]*C.char, len(runs))
	rgb := make([]C.double, len(runs)*3)
	for i, r := range runs {
		texts[i] = C.CString(r.Text)
		rgb[i*3], rgb[i*3+1], rgb[i*3+2] = -1, -1, -1
		if c, ok := bar.Palette[r.Color]; ok {
			rgb[i*3], rgb[i*3+1], rgb[i*3+2] = C.double(c[0]), C.double(c[1]), C.double(c[2])
		}
	}
	C.setTitleRuns((**C.char)(unsafe.Pointer(&texts[0])), &rgb[0], C.int(len(runs)))
	for _, t := range texts {
		C.free(unsafe.Pointer(t))
	}
}
