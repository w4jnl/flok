package ui

import (
	"time"

	"github.com/w4jnl/flok/internal/awake"
	"github.com/w4jnl/flok/internal/snapshot"
)

// Releaser is a held power assertion (awake.Assertion in production).
type Releaser interface{ Release() }

// presenceReporter is a Releaser that also keeps the user active ([keep_awake] presence).
type presenceReporter interface{ Presence() awake.Presence }

// awakeHold follows the keep-awake marker: it holds at most one assertion while the marker asks
// for it. It lives behind a pointer because Bubble Tea copies the Model on every update.
type awakeHold struct {
	hold  func() (Releaser, error)
	want  bool
	held  Releaser
	shown awake.Presence // presence in the last published snapshot
}

// sync applies the marker; it returns whether the held state changed (so the snapshot must be
// republished) and a hold error, after which the state stays off until the marker flips again.
func (k *awakeHold) sync(want bool) (changed bool, err error) {
	if k == nil || k.hold == nil || want == k.want {
		return false, nil
	}
	k.want = want
	if !want {
		if k.held == nil {
			return false, nil
		}
		k.held.Release()
		k.held = nil
		return true, nil
	}
	r, err := k.hold()
	if err != nil {
		return false, err
	}
	k.held = r
	return true, nil
}

func (k *awakeHold) on() bool { return k != nil && k.held != nil }

// presence is what the held assertion's nudger achieves; it runs on its own goroutine, so the
// value can change between polls (presenceMoved).
func (k *awakeHold) presence() awake.Presence {
	if k == nil || k.held == nil {
		return awake.PresenceOff
	}
	if p, ok := k.held.(presenceReporter); ok {
		return p.Presence()
	}
	return awake.PresenceOff
}

// presenceMoved reports whether the presence state differs from the published one.
func (k *awakeHold) presenceMoved() bool { return k != nil && k.presence() != k.shown }

func (k *awakeHold) release() {
	if k != nil && k.held != nil {
		k.held.Release()
		k.held = nil
	}
}

// ReleaseKeepAwake drops the power assertion when the sidebar exits (macOS would drop it with
// the process anyway).
func (m Model) ReleaseKeepAwake() { m.keep.release() }

// publish writes snapshot.json from the last merge plus the keep-awake state.
func (m Model) publish() {
	presence := m.keep.presence()
	if m.keep != nil {
		m.keep.shown = presence
	}
	if m.publisher == nil {
		return
	}
	s := snapshot.FromMerge(m.snap)
	s.KeepAwake, s.KeepAwakePresence = m.keep.on(), string(presence)
	_, _ = m.publisher.Publish(s, time.Now())
}
