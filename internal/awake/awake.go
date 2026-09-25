// Package awake keeps the machine from idle-sleeping and the display on while an Assertion is
// held (`flok keep-awake`). On macOS it takes the IOKit power assertions caffeinate uses,
// PreventUserIdleDisplaySleep and PreventUserIdleSystemSleep, directly, without cgo. macOS
// releases them by itself when the holding process exits, crashes included. Other platforms
// report ErrUnsupported.
package awake

import "errors"

// ErrUnsupported is returned by Hold on platforms without an implementation.
var ErrUnsupported = errors.New("keep-awake is only available on macOS")

// Name labels flok's assertions in `pmset -g assertions`.
const Name = "flok keep-awake"
