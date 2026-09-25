//go:build darwin

package awake

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/purego"
)

const (
	cfStringEncodingUTF8 = 0x08000100
	assertionLevelOn     = 255

	cgHIDSystemState = 1          // kCGEventSourceStateHIDSystemState
	cgAnyInputEvent  = ^uint32(0) // kCGAnyInputEventType
	cgHIDEventTap    = 0          // kCGHIDEventTap
	cgFlagsChanged   = 12         // kCGEventFlagsChanged
	cgMouseMoved     = 5          // kCGEventMouseMoved
	nudgeSettle      = 100 * time.Millisecond
	nudgeResetEnough = 1.0 // seconds of idle time left after a nudge that still count as a reset
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

	inputOnce sync.Once
	inputErr  error

	axIsProcessTrusted func() bool
	cgIdleSeconds      func(state int32, eventType uint32) float64
	cgFlagsState       func(state int32) uint64
	cgEventCreate      func(source uintptr) uintptr
	cgEventSetType     func(event uintptr, eventType uint32)
	cgEventSetFlags    func(event uintptr, flags uint64)
	cgEventPost        func(tap uint32, event uintptr)
	cgMainDisplay      func() uint32
	cgDisplayIsAsleep  func(display uint32) uint32
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

// loadInput resolves the CoreGraphics and Accessibility symbols the presence nudger needs, on
// first use.
func loadInput() error {
	if err := load(); err != nil {
		return err
	}
	inputOnce.Do(func() {
		cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			inputErr = fmt.Errorf("load CoreGraphics: %w", err)
			return
		}
		as, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			inputErr = fmt.Errorf("load ApplicationServices: %w", err)
			return
		}
		purego.RegisterLibFunc(&axIsProcessTrusted, as, "AXIsProcessTrusted")
		purego.RegisterLibFunc(&cgIdleSeconds, cg, "CGEventSourceSecondsSinceLastEventType")
		purego.RegisterLibFunc(&cgFlagsState, cg, "CGEventSourceFlagsState")
		purego.RegisterLibFunc(&cgEventCreate, cg, "CGEventCreate")
		purego.RegisterLibFunc(&cgEventSetType, cg, "CGEventSetType")
		purego.RegisterLibFunc(&cgEventSetFlags, cg, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgEventPost, cg, "CGEventPost")
		purego.RegisterLibFunc(&cgMainDisplay, cg, "CGMainDisplayID")
		purego.RegisterLibFunc(&cgDisplayIsAsleep, cg, "CGDisplayIsAsleep")
	})
	return inputErr
}

// Supported reports whether Hold can work here.
func Supported() bool { return true }

// Trusted reports whether this process may post input events: Accessibility permission, which
// macOS grants to the app a process runs under (the terminal, for flok).
func Trusted() bool { return loadInput() == nil && axIsProcessTrusted() }

// Assertion is a set of held power assertions, plus the presence nudger when asked for.
type Assertion struct {
	mu       sync.Mutex
	ids      []uint32
	stop     chan struct{}
	presence atomic.Value // Presence
}

var errCFString = errors.New("CFStringCreateWithCString failed")

// Hold takes the power assertions under the given name.
func Hold(name string, o Options) (*Assertion, error) {
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
	if o.Presence {
		if err := loadInput(); err != nil {
			a.Release()
			return nil, err
		}
		a.presence.Store(trustState())
		a.stop = make(chan struct{})
		go a.keepPresent(a.stop)
	}
	return a, nil
}

// Presence reports what the nudger achieves (PresenceOff without Options.Presence).
func (a *Assertion) Presence() Presence {
	if a == nil {
		return PresenceOff
	}
	p, _ := a.presence.Load().(Presence)
	return p
}

func trustState() Presence {
	if axIsProcessTrusted() {
		return PresenceActive
	}
	return PresenceBlocked
}

func (a *Assertion) keepPresent(stop <-chan struct{}) {
	t := time.NewTicker(presenceEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		a.presence.Store(nudge(stop))
	}
}

func idleSeconds() float64 { return cgIdleSeconds(cgHIDSystemState, cgAnyInputEvent) }

// nudge resets the HID idle clock once the user has been idle for PresenceIdle, unless the
// display sleeps (someone put it to sleep on purpose; an event would wake it). It tries an
// empty modifier event first, a zero-distance mouse move second, and reports blocked when
// macOS drops both.
func nudge(stop <-chan struct{}) Presence {
	if !axIsProcessTrusted() {
		return PresenceBlocked
	}
	if idleSeconds() < PresenceIdle.Seconds() || cgDisplayIsAsleep(cgMainDisplay()) != 0 {
		return PresenceActive
	}
	for _, t := range []uint32{cgFlagsChanged, cgMouseMoved} {
		select {
		case <-stop:
			return PresenceActive
		default:
		}
		post(t)
		time.Sleep(nudgeSettle)
		if idleSeconds() < nudgeResetEnough {
			return PresenceActive
		}
	}
	return PresenceBlocked
}

// post sends an input event of the given type at the cursor's position with the modifiers
// already held, so no app sees a key or a movement.
func post(eventType uint32) {
	ev := cgEventCreate(0)
	if ev == 0 {
		return
	}
	defer cfRelease(ev)
	cgEventSetType(ev, eventType)
	cgEventSetFlags(ev, cgFlagsState(cgHIDSystemState))
	cgEventPost(cgHIDEventTap, ev)
}

// Release drops the assertions; calling it again is a no-op.
func (a *Assertion) Release() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stop != nil {
		close(a.stop)
		a.stop = nil
	}
	for _, id := range a.ids {
		pmRelease(id)
	}
	a.ids = nil
}
