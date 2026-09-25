// Package awake keeps the machine from idle-sleeping and the display on while an Assertion is
// held (`flok keep-awake`). On macOS it takes the IOKit power assertions caffeinate uses,
// PreventUserIdleDisplaySleep and PreventUserIdleSystemSleep, directly, without cgo. macOS
// releases them by itself when the holding process exits, crashes included. Other platforms
// report ErrUnsupported.
//
// With Options.Presence the Assertion also keeps the user "active" for apps that watch the HID
// idle time (time since the last key or mouse event), such as presence in Teams or Slack; power
// assertions do not touch that clock. After PresenceIdle without input it posts an empty
// modifier-key event (flagsChanged with the modifiers already held: no key, no cursor
// movement), which resets it. macOS only lets such events through for processes with
// Accessibility permission.
package awake

import (
	"errors"
	"time"
)

// ErrUnsupported is returned by Hold on platforms without an implementation.
var ErrUnsupported = errors.New("keep-awake is only available on macOS")

// Name labels flok's assertions in `pmset -g assertions`.
const Name = "flok keep-awake"

// Options tune Hold.
type Options struct {
	Presence bool // also keep the user active for apps that watch input idle time (Teams, Slack)
}

// Presence is what the presence nudger achieves.
type Presence string

const (
	PresenceOff     Presence = ""        // not asked for
	PresenceActive  Presence = "active"  // the idle clock is reset after PresenceIdle
	PresenceBlocked Presence = "blocked" // macOS drops flok's events: no Accessibility permission
)

const (
	// PresenceIdle is how long the user may be idle before a nudge; Teams goes away after 5 min,
	// Slack after 10.
	PresenceIdle = 60 * time.Second
	// presenceEvery is how often the idle clock is checked.
	presenceEvery = 30 * time.Second
)
