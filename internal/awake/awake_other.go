//go:build !darwin

package awake

// Supported reports whether Hold can work here.
func Supported() bool { return false }

// Assertion is a set of held power assertions (never created on this platform).
type Assertion struct{}

// Hold always fails here.
func Hold(string) (*Assertion, error) { return nil, ErrUnsupported }

// Release is a no-op.
func (a *Assertion) Release() {}
