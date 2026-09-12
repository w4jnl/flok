package ui

import (
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/rules"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

func newTestModel(t *testing.T) (Model, *fakeTmux) {
	t.Helper()
	cfg := config.Default()
	cfg.Sounds.Enabled = false
	ft := &fakeTmux{screens: map[string]string{}}
	m := New(Deps{Cfg: cfg, Inner: ft, Store: state.New(t.TempDir())})
	ft.calls = nil // New reads the prefix through the client
	return m, ft
}

func TestIdleIntervals(t *testing.T) {
	m, _ := newTestModel(t)
	m.d.Cfg.Sidebar.PollMs, m.d.Cfg.Sidebar.ScreenPollMs, m.d.Cfg.Sidebar.IdlePollMs = 1000, 2000, 3000
	if m.pollInterval() != time.Second || m.screenInterval() != 2*time.Second {
		t.Fatalf("visible: %v %v", m.pollInterval(), m.screenInterval())
	}
	m.unfocused = true
	if m.pollInterval() != 3*time.Second || m.screenInterval() != 3*time.Second {
		t.Fatalf("unfocused: %v %v", m.pollInterval(), m.screenInterval())
	}
	m.unfocused, m.hidden = false, true
	if m.pollInterval() != 3*time.Second {
		t.Fatalf("hidden: %v", m.pollInterval())
	}
	m.d.Cfg.Sidebar.IdlePollMs = 500 // below the floor: idle never polls faster than the normal cadence
	if m.pollInterval() != time.Second || m.screenInterval() != 2*time.Second {
		t.Fatalf("idle floor: %v %v", m.pollInterval(), m.screenInterval())
	}
}

func TestScreenResultsRebuildWithoutTmux(t *testing.T) {
	m, ft := newTestModel(t)
	m.tmuxSnap, m.lastRaw = tmux.Snapshot{}, "raw"
	seq := m.screenSeq
	next, cmd := m.Update(screenMsg{results: nil})
	m = next.(Model)
	if m.screenSeq != seq || cmd != nil {
		t.Fatal("empty results must not count as a sample")
	}
	res := map[string]rules.Result{"%1": {Matched: true, State: agent.Idle, RuleID: "idle"}}
	for i := 1; i <= 2; i++ { // identical samples still count: the merge tallies consecutive idle screens
		next, cmd = m.Update(screenMsg{results: res})
		m = next.(Model)
		if m.screenSeq != seq+i {
			t.Fatalf("sample %d: screenSeq %d", i, m.screenSeq)
		}
		if cmd == nil {
			t.Fatal("expected a rebuild command")
		}
		msg, ok := cmd().(snapshotMsg)
		if !ok || !msg.rebuilt {
			t.Fatalf("expected a rebuilt snapshotMsg, got %#v", msg)
		}
		next, _ = m.Update(msg)
		m = next.(Model)
		if m.lastScrSeq != m.screenSeq {
			t.Fatal("the rebuilt message must run the merge")
		}
	}
	if len(ft.calls) != 0 {
		t.Fatalf("screen results must not spawn tmux, got %v", ft.calls)
	}
}

func TestRebuildReloadsStoreWithoutTmux(t *testing.T) {
	m, ft := newTestModel(t)
	if msg := m.rebuild(true)().(snapshotMsg); msg.rebuilt || len(ft.calls) != 1 {
		t.Fatalf("before the first poll rebuild must poll tmux (rebuilt=%v calls=%d)", msg.rebuilt, len(ft.calls))
	}
	ft.calls = nil
	m.tmuxSnap, m.lastRaw = tmux.Snapshot{}, "raw"
	_, _, _ = m.d.Store.Update("%7", func(a *agent.Agent) state.Effects { a.State = agent.Working; return state.Effects{} })
	msg := m.rebuild(true)().(snapshotMsg)
	if !msg.rebuilt || len(ft.calls) != 0 {
		t.Fatalf("rebuild must not spawn tmux (rebuilt=%v calls=%v)", msg.rebuilt, ft.calls)
	}
	if msg.hook["%7"].State != agent.Working {
		t.Fatal("rebuild(true) must reload hook records")
	}
}

func TestSpinnerPausesWhileIdle(t *testing.T) {
	m, _ := newTestModel(t)
	m.snap = merge.Snapshot{Agents: []agent.Agent{{PaneID: "%1", State: agent.Working}}}
	m.animating, m.frame, m.unfocused = true, 3, true
	m.vc.valid = true
	next, cmd := m.Update(animMsg{})
	m = next.(Model)
	if cmd != nil || m.animating || m.frame != 3 || !m.vc.valid {
		t.Fatalf("idle: the spinner must stop without redrawing (cmd=%v animating=%v frame=%d valid=%v)", cmd != nil, m.animating, m.frame, m.vc.valid)
	}
	m.hidden = true
	if m.animCmd() != nil {
		t.Fatal("hidden: no spinner")
	}
	m.unfocused, m.hidden, m.lastRaw = false, false, "raw"
	next, cmd = m.Update(snapshotMsg{fp: m.lastFP, raw: "raw", rebuilt: true})
	m = next.(Model)
	if cmd == nil || !m.animating {
		t.Fatal("visible again: the next snapshot restarts the spinner")
	}
}

func TestStoreEventWanted(t *testing.T) {
	root := "/s"
	for name, want := range map[string]bool{
		"/s/agents/1.json": true, "/s/seen/1.json": true, "/s/terminal-focus": true, "/s/sidebar-hidden": true,
		"/s/agents/1.json.lock": false, "/s/agents/1.json.123.tmp": false,
		"/s/snapshot.json": false, "/s/snapshot.json.42.tmp": false, "/s/runtime.json": false, "/s/events.log": false,
	} {
		if got := storeEventWanted(root, name); got != want {
			t.Errorf("%s: got %v want %v", name, got, want)
		}
	}
}

func TestRegistryNeededIgnoresNodePanes(t *testing.T) {
	m, _ := newTestModel(t)
	m.tmuxSnap = tmux.Snapshot{Panes: []tmux.Pane{{ID: "%1", Command: "node"}}}
	if m.registryNeeded() {
		t.Fatal("a node pane alone must not keep the registry hot")
	}
	m.snap = merge.Snapshot{Agents: []agent.Agent{{PaneID: "%2", Kind: "claude", Source: "hook", State: agent.Working}}}
	if !m.registryNeeded() {
		t.Fatal("an open Claude turn needs registry samples")
	}
}
