//go:build !darwin

package awake

// Supported reports whether Hold can work here.
func Supported() bool { return false }

// Trusted reports whether this process may post input events (Accessibility); never here.
func Trusted() bool { return false }

// Assertion is a set of held power assertions (never created on this platform).
type Assertion struct{}

// Hold always fails here.
func Hold(string, Options) (*Assertion, error) { return nil, ErrUnsupported }

// Presence is always off here.
func (a *Assertion) Presence() Presence { return PresenceOff }

// Release is a no-op.
func (a *Assertion) Release() {}
