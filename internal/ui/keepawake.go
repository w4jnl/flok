package ui

import (
	"time"

	"github.com/w4jnl/flok/internal/snapshot"
)

// Releaser is a held power assertion (awake.Assertion in production).
type Releaser interface{ Release() }

// awakeHold follows the keep-awake marker: it holds at most one assertion while the marker asks
// for it. It lives behind a pointer because Bubble Tea copies the Model on every update.
type awakeHold struct {
	hold func() (Releaser, error)
	want bool
	held Releaser
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
	if m.publisher == nil {
		return
	}
	s := snapshot.FromMerge(m.snap)
	s.KeepAwake = m.keep.on()
	_, _ = m.publisher.Publish(s, time.Now())
}
