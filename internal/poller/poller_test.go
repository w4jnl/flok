package poller

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/rules"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
	"github.com/w4jnl/flok/internal/tmux/tmuxtest"
)

func newTestPoller(t *testing.T) (*Poller, *tmuxtest.Fake) {
	t.Helper()
	cfg := config.Default()
	cfg.Sounds.Enabled = false
	ft := &tmuxtest.Fake{Screens: map[string]string{}}
	return New(Deps{Cfg: cfg, Tmux: ft, Store: state.New(t.TempDir())}), ft
}

func TestCaptureAllSplitsOneInvocation(t *testing.T) {
	f := &tmuxtest.Fake{Screens: map[string]string{"%1": "one a\none b\n", "%2": "\n╭──╮\n│ > │\n╰──╯\n"}}
	got := CaptureAll(f, []string{"%1", "%2"}, 0)
	if len(f.Calls) != 1 {
		t.Fatalf("want one tmux call, got %d", len(f.Calls))
	}
	for pane, want := range f.Screens {
		if got[pane] != want {
			t.Errorf("%s: got %q want %q", pane, got[pane], want)
		}
	}
}

func TestCaptureAllFallsBackForMissingPane(t *testing.T) {
	f := &tmuxtest.Fake{Screens: map[string]string{"%1": "first\n", "%3": "third\n"}}
	got := CaptureAll(f, []string{"%1", "%2", "%3"}, 0)
	if got["%1"] != "first\n" || got["%3"] != "third\n" {
		t.Errorf("got %q", got)
	}
	if _, ok := got["%2"]; ok {
		t.Errorf("vanished pane must be absent, got %q", got["%2"])
	}
	// the batch aborted at %2, so %3 (and %2 itself) were retried individually
	if len(f.Calls) != 3 {
		t.Errorf("want batch + 2 single captures, got %d calls", len(f.Calls))
	}
}

func TestStoreEventWanted(t *testing.T) {
	root := "/s"
	for name, want := range map[string]bool{
		"/s/agents/1.json": true, "/s/seen/1.json": true, "/s/requests/17.json": true, "/s/sidebar-hidden": true, "/s/terminal-theme": true, "/s/terminal-focus": false,
		"/s/keep-awake": true,
		"/s/hosts.json": true, "/s/hosts.json.lock": false, "/s/hosts.json.7.tmp": false,
		"/s/agents/1.json.lock": false, "/s/agents/1.json.123.tmp": false,
		"/s/snapshot.json": false, "/s/snapshot.json.42.tmp": false, "/s/runtime.json": false, "/s/events.log": false,
	} {
		if got := StoreEventWanted(root, name); got != want {
			t.Errorf("%s: got %v want %v", name, got, want)
		}
	}
}

func TestIntervalsAndFloors(t *testing.T) {
	p, _ := newTestPoller(t)
	p.d.Cfg.Sidebar.PollMs, p.d.Cfg.Sidebar.ScreenPollMs, p.d.Cfg.Sidebar.IdlePollMs = 1000, 2000, 3000
	if p.PollInterval(false) != time.Second || p.ScreenInterval(false) != 2*time.Second {
		t.Fatalf("visible: %v %v", p.PollInterval(false), p.ScreenInterval(false))
	}
	if p.PollInterval(true) != 3*time.Second || p.ScreenInterval(true) != 3*time.Second {
		t.Fatalf("idle: %v %v", p.PollInterval(true), p.ScreenInterval(true))
	}
	p.d.Cfg.Sidebar.IdlePollMs = 500 // below the floor: idle never polls faster than the normal cadence
	if p.PollInterval(true) != time.Second || p.ScreenInterval(true) != 2*time.Second {
		t.Fatalf("idle floor: %v %v", p.PollInterval(true), p.ScreenInterval(true))
	}
	p.d.PollFloorMs, p.d.ScreenFloorMs = 1500, 3000 // a remote host driven over ssh polls slower
	if p.PollInterval(false) != 1500*time.Millisecond || p.ScreenInterval(false) != 3*time.Second {
		t.Fatalf("raised floors: %v %v", p.PollInterval(false), p.ScreenInterval(false))
	}
}

func TestScreenResultsRebuildWithoutTmux(t *testing.T) {
	p, ft := newTestPoller(t)
	p.Prime(tmux.Snapshot{}, "raw")
	seq := p.screenSeq
	if p.ApplyScreen(ScreenMsg{Results: nil}) || p.screenSeq != seq {
		t.Fatal("empty results must not count as a sample")
	}
	res := map[string]rules.Result{"%1": {Matched: true, State: agent.Idle, RuleID: "idle"}}
	for i := 1; i <= 2; i++ { // identical samples still count: the merge tallies consecutive idle screens
		if !p.ApplyScreen(ScreenMsg{Results: res}) || p.screenSeq != seq+i {
			t.Fatalf("sample %d: screenSeq %d", i, p.screenSeq)
		}
		msg := p.Rebuild(false)()
		if !msg.Rebuilt {
			t.Fatalf("expected a rebuilt message, got %#v", msg)
		}
		if !p.ApplySnapshot(msg, false) || p.lastScrSeq != p.screenSeq {
			t.Fatal("the rebuilt message must run the merge")
		}
	}
	if len(ft.Calls) != 0 {
		t.Fatalf("screen results must not spawn tmux, got %v", ft.Calls)
	}
}

func TestRebuildReloadsStoreWithoutTmux(t *testing.T) {
	p, ft := newTestPoller(t)
	if msg := p.Rebuild(true)(); msg.Rebuilt || len(ft.Calls) != 1 {
		t.Fatalf("before the first poll rebuild must poll tmux (rebuilt=%v calls=%d)", msg.Rebuilt, len(ft.Calls))
	}
	ft.Calls = nil
	p.Prime(tmux.Snapshot{}, "raw")
	_, _, _ = p.d.Store.Update("%7", func(a *agent.Agent) state.Effects { a.State = agent.Working; return state.Effects{} })
	msg := p.Rebuild(true)()
	if !msg.Rebuilt || len(ft.Calls) != 0 {
		t.Fatalf("rebuild must not spawn tmux (rebuilt=%v calls=%v)", msg.Rebuilt, ft.Calls)
	}
	if msg.Hook["%7"].State != agent.Working {
		t.Fatal("rebuild(true) must reload hook records")
	}
}

// An unchanged poll skips the merge; force (the sidebar showed an error) runs it anyway.
func TestApplySnapshotSkipsUnchangedInputs(t *testing.T) {
	p, _ := newTestPoller(t)
	msg := p.Poll()()
	if !p.ApplySnapshot(msg, false) {
		t.Fatal("the first poll must merge")
	}
	if p.ApplySnapshot(msg, false) {
		t.Fatal("an identical poll must not merge again")
	}
	if !p.ApplySnapshot(msg, true) {
		t.Fatal("force must merge")
	}
}

func TestRegistryNeededIgnoresNodePanes(t *testing.T) {
	p, _ := newTestPoller(t)
	p.d.Registry = true
	p.tmuxSnap = tmux.Snapshot{Panes: []tmux.Pane{{ID: "%1", Command: "node"}}}
	if p.registryNeeded() {
		t.Fatal("a node pane alone must not keep the registry hot")
	}
	p.snap = merge.Snapshot{Agents: []agent.Agent{{PaneID: "%2", Kind: "claude", Source: "hook", State: agent.Working}}}
	if !p.registryNeeded() {
		t.Fatal("an open Claude turn needs registry samples")
	}
	p.d.Registry = false
	if p.PollRegistryIfDue() != nil || p.PollRegistry() != nil {
		t.Fatal("a disabled registry is never polled")
	}
}

// Hook-less agents (title/screen/registry): a watched pane is silent unless when_focused, and a
// watched turn that ends idle (never done) then sounds like a finished one.
func TestSoundTransitionsWhenFocused(t *testing.T) {
	for _, tc := range []struct {
		name        string
		whenFocused bool
		focus       string
		states      []agent.State
		want        []string
	}{
		{"watched, default", false, "%1", []agent.State{agent.Working, agent.Blocked, agent.Working, agent.Idle}, nil},
		{"watched, when_focused", true, "%1", []agent.State{agent.Working, agent.Blocked, agent.Working, agent.Idle}, []string{"blocked", "done"}},
		{"unwatched, default", false, "%2", []agent.State{agent.Working, agent.Blocked, agent.Working, agent.Done}, []string{"blocked", "done"}},
		{"looking at a done pane is silent", true, "%1", []agent.State{agent.Done, agent.Idle}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newTestPoller(t)
			p.d.Cfg.Sounds.Enabled, p.d.Cfg.Sounds.WhenFocused, p.d.Cfg.Sounds.MinIntervalMs = true, tc.whenFocused, 0
			var played []string
			p.d.Sound = func(_, kind string) { played = append(played, kind) }
			for i, st := range tc.states {
				if i == 0 {
					p.prevState["%1"] = st // the first sample is the starting point, not a transition
				}
				p.snap = merge.Snapshot{Focus: merge.Focus{PaneID: tc.focus}, Agents: []agent.Agent{{PaneID: "%1", State: st, Source: "title"}}}
				p.soundTransitions()
			}
			if !reflect.DeepEqual(played, tc.want) {
				t.Fatalf("played %v, want %v", played, tc.want)
			}
		})
	}
}

// Run polls at once, merges, and stops with the context.
func TestRunMergesAndStops(t *testing.T) {
	p, ft := newTestPoller(t)
	p.d.Cfg.Sidebar.PollMs = 200
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	merges := 0
	err := Run(ctx, p, func(merge.Snapshot) { merges++ })
	if err != context.DeadlineExceeded {
		t.Fatalf("Run returned %v", err)
	}
	if merges != 1 {
		t.Fatalf("an unchanged server merges once, got %d", merges)
	}
	if len(ft.Calls) < 2 {
		t.Fatalf("Run must keep polling, got %d calls", len(ft.Calls))
	}
}
