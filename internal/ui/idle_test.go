package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/poller"
	"github.com/w4jnl/flok/internal/rules"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
	"github.com/w4jnl/flok/internal/tmux/tmuxtest"
)

func newTestModel(t *testing.T) (Model, *tmuxtest.Fake) {
	t.Helper()
	cfg := config.Default()
	cfg.Sounds.Enabled = false
	ft := &tmuxtest.Fake{Screens: map[string]string{}}
	m := New(Deps{Cfg: cfg, Inner: ft, Store: state.New(t.TempDir())})
	ft.Calls = nil // New reads the prefix through the client
	return m, ft
}

// The sidebar decides what idle means (hidden, not merely unfocused); with the default cadences
// (poll 1 s, screen 2 s, idle 3 s) the intervals follow.
func TestIdleIntervals(t *testing.T) {
	m, _ := newTestModel(t)
	if m.pollInterval() != time.Second || m.screenInterval() != 2*time.Second {
		t.Fatalf("visible: %v %v", m.pollInterval(), m.screenInterval())
	}
	m.unfocused = true // an unfocused window is still on screen: no slowdown
	if m.pollInterval() != time.Second || m.screenInterval() != 2*time.Second {
		t.Fatalf("unfocused: %v %v", m.pollInterval(), m.screenInterval())
	}
	m.unfocused, m.hidden = false, true
	if m.pollInterval() != 3*time.Second || m.screenInterval() != 3*time.Second {
		t.Fatalf("hidden: %v %v", m.pollInterval(), m.screenInterval())
	}
	// the floors themselves are the pipeline's business: see poller.TestIntervalsAndFloors
}

// A screen sample delivered through Update rebuilds from the cached tmux data (the pipeline's
// own rules are tested in package poller).
func TestScreenResultsRebuildWithoutTmux(t *testing.T) {
	m, ft := newTestModel(t)
	m.p.Prime(tmux.Snapshot{}, "raw")
	next, cmd := m.Update(screenMsg{Results: nil})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("empty results must not count as a sample")
	}
	res := map[string]rules.Result{"%1": {Matched: true, State: agent.Idle, RuleID: "idle"}}
	next, cmd = m.Update(screenMsg{Results: res})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("expected a rebuild command")
	}
	msg, ok := cmd().(snapshotMsg)
	if !ok || !msg.Rebuilt {
		t.Fatalf("expected a rebuilt snapshotMsg, got %#v", msg)
	}
	next, _ = m.Update(msg)
	m = next.(Model)
	if m.p.LastFP() != msg.FP {
		t.Fatal("the rebuilt message must run the merge")
	}
	if len(ft.Calls) != 0 {
		t.Fatalf("screen results must not spawn tmux, got %v", ft.Calls)
	}
}

func TestSpinnerPausesWhileIdle(t *testing.T) {
	m, _ := newTestModel(t)
	m.snap = merge.Snapshot{Agents: []agent.Agent{{PaneID: "%1", State: agent.Working}}}
	m.animating, m.frame, m.hidden = true, 3, true
	m.vc.valid = true
	next, cmd := m.Update(animMsg{})
	m = next.(Model)
	if cmd != nil || m.animating || m.frame != 3 || !m.vc.valid {
		t.Fatalf("hidden: the spinner must stop without redrawing (cmd=%v animating=%v frame=%d valid=%v)", cmd != nil, m.animating, m.frame, m.vc.valid)
	}
	if m.animCmd() != nil {
		t.Fatal("hidden: no spinner")
	}
	m.unfocused = true // unfocused but visible: spins
	m.hidden = false
	m.p.Prime(tmux.Snapshot{}, "raw")
	next, cmd = m.Update(snapshotMsg{SnapshotMsg: poller.SnapshotMsg{FP: m.p.LastFP(), Raw: "raw", Rebuilt: true}})
	m = next.(Model)
	if cmd == nil || !m.animating {
		t.Fatal("visible again: the next snapshot restarts the spinner")
	}
}

// What the start found worth saying (an older tmux snippet) rides in the footer after the
// hosts' warnings, single-host too.
func TestStartNoticesReachTheFooter(t *testing.T) {
	cfg := config.Default()
	cfg.Sounds.Enabled = false
	m := New(Deps{Cfg: cfg, Inner: &tmuxtest.Fake{Screens: map[string]string{}}, Store: state.New(t.TempDir()),
		Notices: []string{"older tmux snippet: A → prev (rebound for now) · flok install --tmux prints the current one"}})
	m.refederate()
	if view := strings.Join(render(m, 40, 30), "\n"); !strings.Contains(view, "older tmux snippet: A → prev") {
		t.Fatalf("notice missing:\n%s", view)
	}
	if len(m.fed.Warnings) != 0 {
		t.Fatalf("the published snapshot keeps its own warnings: %v", m.fed.Warnings)
	}
}
