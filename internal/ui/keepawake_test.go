package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

type fakeAssertion struct{ released *int }

func (f fakeAssertion) Release() { *f.released++ }

type fakeAwake struct {
	holds, releases int
	err             error
}

func (f *fakeAwake) hold() (Releaser, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.holds++
	return fakeAssertion{&f.releases}, nil
}

func newKeepAwakeModel(t *testing.T) (Model, *fakeAwake) {
	t.Helper()
	cfg := config.Default()
	cfg.Sounds.Enabled = false
	fa := &fakeAwake{}
	m := New(Deps{Cfg: cfg, Inner: &fakeTmux{screens: map[string]string{}}, Store: state.New(t.TempDir()), Awake: fa.hold})
	m.tmuxSnap, m.lastRaw = tmux.Snapshot{}, "raw" // rebuilds reuse this instead of polling
	return m, fa
}

// step writes the marker and delivers the rebuild the fsnotify watch would trigger.
func step(t *testing.T, m Model, on bool) Model {
	t.Helper()
	if err := m.d.Store.SetKeepAwake(on); err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(m.rebuild(true)())
	return next.(Model)
}

func publishedKeepAwake(t *testing.T, m Model) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(m.d.Store.Dir, snapshot.FileName))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(data), `"keep_awake": true`)
}

func TestKeepAwakeFollowsTheMarker(t *testing.T) {
	m, fa := newKeepAwakeModel(t)
	m = step(t, m, false)
	if fa.holds != 0 || m.keep.on() || publishedKeepAwake(t, m) {
		t.Fatal("off by default")
	}
	m = step(t, m, true)
	if fa.holds != 1 || !m.keep.on() || !publishedKeepAwake(t, m) {
		t.Fatalf("on: holds=%d on=%v", fa.holds, m.keep.on())
	}
	m = step(t, m, true) // an unchanged marker (fast path, same fingerprint) takes nothing new
	if fa.holds != 1 || fa.releases != 0 {
		t.Fatalf("repeat: holds=%d releases=%d", fa.holds, fa.releases)
	}
	m = step(t, m, false)
	if fa.releases != 1 || m.keep.on() || publishedKeepAwake(t, m) {
		t.Fatalf("off: releases=%d on=%v", fa.releases, m.keep.on())
	}
	m = step(t, m, true)
	m.ReleaseKeepAwake()
	if fa.holds != 2 || fa.releases != 2 {
		t.Fatalf("exit must release: holds=%d releases=%d", fa.holds, fa.releases)
	}
}

func TestKeepAwakeHoldErrorStaysOff(t *testing.T) {
	m, fa := newKeepAwakeModel(t)
	fa.err = errors.New("no IOKit")
	m = step(t, m, true)
	if m.keep.on() || publishedKeepAwake(t, m) {
		t.Fatal("a failed hold is published as off")
	}
	fa.err = nil
	m = step(t, m, true) // not retried every poll
	if fa.holds != 0 {
		t.Fatal("a failed hold is retried only when the marker flips again")
	}
	m = step(t, m, false)
	m = step(t, m, true)
	if fa.holds != 1 || !m.keep.on() {
		t.Fatal("flipping the marker retries")
	}
}

func TestKeepAwakeWithoutHolder(t *testing.T) {
	m, _ := newTestModel(t) // no Deps.Awake: keep-awake unsupported
	m.tmuxSnap, m.lastRaw = tmux.Snapshot{}, "raw"
	m = step(t, m, true)
	if m.keep.on() || publishedKeepAwake(t, m) {
		t.Fatal("without a holder keep-awake stays off")
	}
}

// A rebuild after a screen or registry sample does not read the marker; created before a marker
// change and delivered after it, it must not undo that change.
func TestKeepAwakeStaleRebuildKeepsTheChange(t *testing.T) {
	m, fa := newKeepAwakeModel(t)
	stale := m.rebuild(false) // queued while the marker was still off
	m = step(t, m, true)
	next, _ := m.Update(stale())
	m = next.(Model)
	if !m.keep.on() || fa.releases != 0 || !publishedKeepAwake(t, m) {
		t.Fatalf("a stale rebuild released keep-awake: on=%v releases=%d", m.keep.on(), fa.releases)
	}
}
