//go:build darwin

package awake

import (
	"errors"
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
)

const (
	cfStringEncodingUTF8 = 0x08000100
	assertionLevelOn     = 255
)

// assertionTypes: the display stays on, and the system does not idle-sleep either (the display
// assertion alone implies it, but the second one survives a user who puts the display to sleep
// by hand, e.g. with a hot corner).
var assertionTypes = []string{"PreventUserIdleDisplaySleep", "PreventUserIdleSystemSleep"}

var (
	loadOnce sync.Once
	loadErr  error

	cfStringCreate func(alloc uintptr, s string, encoding uint32) uintptr
	cfRelease      func(ref uintptr)
	pmCreate       func(assertionType uintptr, level uint32, name uintptr, id *uint32) int32
	pmRelease      func(id uint32) int32
)

// load resolves the CoreFoundation and IOKit symbols on first use, so a sidebar that never
// keeps the Mac awake does not pay for loading the frameworks.
func load() error {
	loadOnce.Do(func() {
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = fmt.Errorf("load CoreFoundation: %w", err)
			return
		}
		iokit, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = fmt.Errorf("load IOKit: %w", err)
			return
		}
		purego.RegisterLibFunc(&cfStringCreate, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&pmCreate, iokit, "IOPMAssertionCreateWithName")
		purego.RegisterLibFunc(&pmRelease, iokit, "IOPMAssertionRelease")
	})
	return loadErr
}

// Supported reports whether Hold can work here.
func Supported() bool { return true }

// Assertion is a set of held power assertions.
type Assertion struct {
	mu  sync.Mutex
	ids []uint32
}

var errCFString = errors.New("CFStringCreateWithCString failed")

// Hold takes the power assertions under the given name.
func Hold(name string) (*Assertion, error) {
	if err := load(); err != nil {
		return nil, err
	}
	cfName := cfStringCreate(0, name, cfStringEncodingUTF8)
	if cfName == 0 {
		return nil, errCFString
	}
	defer cfRelease(cfName)
	a := &Assertion{}
	for _, t := range assertionTypes {
		cfType := cfStringCreate(0, t, cfStringEncodingUTF8)
		if cfType == 0 {
			a.Release()
			return nil, errCFString
		}
		var id uint32
		rc := pmCreate(cfType, assertionLevelOn, cfName, &id)
		cfRelease(cfType)
		if rc != 0 {
			a.Release()
			return nil, fmt.Errorf("IOPMAssertionCreateWithName %s: IOReturn 0x%x", t, uint32(rc))
		}
		a.ids = append(a.ids, id)
	}
	return a, nil
}

// Release drops the assertions; calling it again is a no-op.
func (a *Assertion) Release() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range a.ids {
		pmRelease(id)
	}
	a.ids = nil
}
