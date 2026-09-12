package merge

import (
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/claudereg"
	"github.com/w4jnl/flok/internal/rules"
	"github.com/w4jnl/flok/internal/tmux"
)

func snap(title string, focusedPane string) tmux.Snapshot {
	return tmux.Snapshot{
		Sessions: []tmux.Session{{ID: "$1", Name: "Claude", Path: "/p"}, {ID: "$2", Name: "Hugo", Path: "/h"}},
		Panes: []tmux.Pane{
			{ID: "%1", SessionID: "$1", SessionName: "Claude", WindowID: "@1", WindowIndex: 1, PaneIndex: 1, WindowActive: true, Active: true, Command: "claude", Path: "/p/x", Title: title},
			{ID: "%2", SessionID: "$1", SessionName: "Claude", WindowID: "@2", WindowIndex: 2, PaneIndex: 1, WindowActive: false, Active: true, Command: "zsh", Title: "✳ stale"},
			{ID: "%3", SessionID: "$2", SessionName: "Hugo", WindowID: "@3", WindowIndex: 1, PaneIndex: 1, WindowActive: true, Active: true, Command: "zsh", Title: "host"},
		},
		Clients: []tmux.ClientInfo{{TTY: "/dev/ttys9", SessionID: focusedPane, SessionName: "x", Activity: 1}},
	}
}

func TestSessionOrder(t *testing.T) {
	snapshot := tmux.Snapshot{Sessions: []tmux.Session{{ID: "$2", Name: "Alpha", Activity: 5}, {ID: "$0", Name: "Zulu", Activity: 9}, {ID: "$1", Name: "Mid", Activity: 1}}}
	want := map[string][]string{"index": {"Zulu", "Mid", "Alpha"}, "name": {"Alpha", "Mid", "Zulu"}, "activity": {"Zulu", "Alpha", "Mid"}, "": {"Zulu", "Mid", "Alpha"}}
	for order, names := range want {
		s := NewTracker().Build(Inputs{Tmux: snapshot, SessionOrder: order, Now: time.Now()})
		var got []string
		for _, sp := range s.Spaces {
			got = append(got, sp.SessionName)
		}
		if strings.Join(got, ",") != strings.Join(names, ",") {
			t.Errorf("order %q: got %v want %v", order, got, names)
		}
	}
}

func TestTitleTransitionsAndSeen(t *testing.T) {
	tr := NewTracker()
	ads := agent.Enabled([]string{"claude"})
	now := time.Now()
	// user looks at Hugo ($2); claude works in Claude ($1)
	s := tr.Build(Inputs{Tmux: snap("◑ job", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Now: now})
	if len(s.Agents) != 1 || s.Agents[0].State != agent.Working || s.Agents[0].Name != "x" || s.Agents[0].Title != "job" {
		t.Fatalf("working expected: %+v", s.Agents)
	}
	if len(s.Spaces) != 2 || s.Spaces[0].Rollup != agent.Working || s.Spaces[1].Current != true || s.Spaces[1].Rollup != "" {
		t.Fatalf("spaces: %+v", s.Spaces)
	}
	// turn ends while unfocused -> done, unseen 1
	s = tr.Build(Inputs{Tmux: snap("✳ job", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Now: now.Add(time.Second)})
	if s.Agents[0].State != agent.Done || s.Agents[0].Unseen != 1 || s.Unseen != 1 {
		t.Fatalf("done expected: %+v", s.Agents[0])
	}
	// user switches to the Claude session -> seen -> idle
	s = tr.Build(Inputs{Tmux: snap("✳ job", "$1"), ClientTTY: "/dev/ttys9", Adapters: ads, Now: now.Add(2 * time.Second)})
	if s.Agents[0].State != agent.Idle || s.Agents[0].Unseen != 0 || !s.Spaces[0].Current {
		t.Fatalf("idle expected: %+v", s.Agents[0])
	}
	// stale title on a zsh pane never becomes an agent
	for _, a := range s.Agents {
		if a.PaneID == "%2" {
			t.Fatal("stale zsh pane listed as agent")
		}
	}
}

func TestFocusFallback(t *testing.T) {
	f, warn := ResolveFocus(snap("✳ j", "$1"), "/dev/nope")
	if !f.Found || f.SessionID != "$1" || warn == "" || f.PaneID != "%1" {
		t.Fatalf("fallback focus: %+v %q", f, warn)
	}
	f, warn = ResolveFocus(tmux.Snapshot{}, "/dev/nope")
	if f.Found || warn == "" {
		t.Fatalf("no clients: %+v %q", f, warn)
	}
}

func TestSortAgents(t *testing.T) {
	now := time.Now()
	as := []agent.Agent{
		{PaneID: "a", State: agent.Idle, SessionName: "b"},
		{PaneID: "b", State: agent.Done, StateSince: now.Add(-time.Minute)},
		{PaneID: "c", State: agent.Blocked, StateSince: now.Add(-time.Hour)},
		{PaneID: "d", State: agent.Blocked, StateSince: now},
		{PaneID: "e", State: agent.Working},
		{PaneID: "f", State: agent.Idle, SessionName: "a"},
	}
	SortAgents(as)
	got := ""
	for _, a := range as {
		got += a.PaneID
	}
	if got != "dcbefa" {
		t.Fatalf("order %q", got)
	}
}

func TestHookAuthorityAndSeen(t *testing.T) {
	tr := NewTracker()
	ads := agent.Enabled([]string{"claude"})
	now := time.Now()
	hook := map[string]agent.Agent{"%1": {PaneID: "%1", Kind: "claude", State: agent.Done, StateSince: now.Add(-time.Minute), HasHooks: true,
		Notifications: []agent.Notification{{Kind: "done", At: now.Add(-time.Minute)}}}}
	// unfocused: done with 1 unseen although the title says idle
	s := tr.Build(Inputs{Tmux: snap("✳ job", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Now: now})
	if a := s.Agents[0]; a.State != agent.Done || a.Unseen != 1 || a.Source != "hook" {
		t.Fatalf("hook done: %+v", a)
	}
	// user looks at it -> newly seen, idle
	s = tr.Build(Inputs{Tmux: snap("✳ job", "$1"), ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Now: now})
	if a := s.Agents[0]; a.State != agent.Idle || a.Unseen != 0 || len(s.NewlySeen) != 1 || s.NewlySeen[0] != "%1" {
		t.Fatalf("seen: %+v newly=%v", a, s.NewlySeen)
	}
	// persisted seen mark keeps it idle when unfocused again
	s = tr.Build(Inputs{Tmux: snap("✳ job", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Seen: map[string]time.Time{"%1": now}, Now: now})
	if a := s.Agents[0]; a.State != agent.Idle || a.Unseen != 0 {
		t.Fatalf("after seen mark: %+v", a)
	}
	// hook says blocked: an idle title (what a permission prompt shows) must never clear it
	hook["%1"] = agent.Agent{PaneID: "%1", Kind: "claude", State: agent.Blocked, Reason: "permission:Bash", HasHooks: true, StateSince: now}
	for i := 0; i < 8; i++ {
		s = tr.Build(Inputs{Tmux: snap("✳ job", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Now: now})
	}
	if a := s.Agents[0]; a.State != agent.Blocked {
		t.Fatalf("blocked must stick: %+v", a)
	}
	// hook says working while the title shows the idle glyph (what Claude does inside tmux): stays working
	hook["%1"] = agent.Agent{PaneID: "%1", Kind: "claude", State: agent.Working, CurrentTool: "Bash", HasHooks: true, StateSince: now}
	for i := 0; i < 6; i++ {
		s = tr.Build(Inputs{Tmux: snap("✳ job", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Now: now})
	}
	if a := s.Agents[0]; a.State != agent.Working || a.CurrentTool != "Bash" {
		t.Fatalf("idle title must not clear a hook working state: %+v", a)
	}
	// Claude's registry says idle in two fresh samples (Esc interrupted the turn) -> idle
	reg := map[string]claudereg.Entry{"/dev/ttyagent": {PID: 42, Status: "idle", Name: "job"}}
	tm := snap("✳ job", "$2")
	tm.Panes[0].TTY = "/dev/ttyagent"
	later := now.Add(10 * time.Second) // samples clearly newer than the hook state
	s = tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: reg, RegistrySeq: 1, RegistryAt: later, Now: now})
	s = tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: reg, RegistrySeq: 1, RegistryAt: later, Now: now}) // same sample: not counted twice
	if a := s.Agents[0]; a.State != agent.Working {
		t.Fatalf("one registry sample must not clear working: %+v", a)
	}
	s = tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: reg, RegistrySeq: 2, RegistryAt: later, Now: now})
	if a := s.Agents[0]; a.State != agent.Idle || a.CurrentTool != "" {
		t.Fatalf("two idle registry samples should clear working: %+v", a)
	}
	if len(s.Corrections) != 1 || s.Corrections[0].PaneID != "%1" || s.Corrections[0].From != agent.Working ||
		s.Corrections[0].Reason != "registry idle" || !s.Corrections[0].Since.Equal(hook["%1"].StateSince) {
		t.Fatalf("correction not reported: %+v", s.Corrections)
	}
	// hook says idle but the spinner shows for 2 polls -> working
	hook["%1"] = agent.Agent{PaneID: "%1", Kind: "claude", State: agent.Idle, HasHooks: true, StateSince: now}
	s = tr.Build(Inputs{Tmux: snap("◑ job", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Now: now})
	s = tr.Build(Inputs{Tmux: snap("◑ job", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Now: now})
	if a := s.Agents[0]; a.State != agent.Working {
		t.Fatalf("spinner override: %+v", a)
	}
}

func TestScreenRules(t *testing.T) {
	tr := NewTracker()
	ads := agent.Enabled([]string{"claude"})
	now := time.Now()
	scr := map[string]rules.Result{"%1": {Matched: true, State: agent.Blocked, RuleID: "x"}}
	// no hooks, plain title: the screen rule decides
	s := tr.Build(Inputs{Tmux: snap("plain", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Screen: scr, Now: now})
	if a := s.Agents[0]; a.State != agent.Blocked || a.Source != "screen" || a.Reason != "prompt" {
		t.Fatalf("screen blocked: %+v", a)
	}
	// hold keeps the last raw state
	scr["%1"] = rules.Result{Matched: true, Hold: true, State: agent.Unknown}
	s = tr.Build(Inputs{Tmux: snap("plain", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Screen: scr, Now: now})
	if a := s.Agents[0]; a.State != agent.Blocked {
		t.Fatalf("hold: %+v", a)
	}
	// evaluated, nothing matched, no title signal: idle fallback; working -> idle transition = done
	scr["%1"] = rules.Result{Matched: true, State: agent.Working}
	tr.Build(Inputs{Tmux: snap("plain", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Screen: scr, Now: now})
	scr["%1"] = rules.Result{}
	s = tr.Build(Inputs{Tmux: snap("plain", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Screen: scr, Now: now})
	if a := s.Agents[0]; a.State != agent.Done || a.Unseen != 1 {
		t.Fatalf("idle fallback + done: %+v", a)
	}
	// hooks say blocked, screen shows the idle prompt box in two fresh samples -> idle
	hook := map[string]agent.Agent{"%1": {PaneID: "%1", Kind: "claude", State: agent.Blocked, Reason: "permission:Bash", HasHooks: true, StateSince: now}}
	scr["%1"] = rules.Result{Matched: true, State: agent.Idle}
	later := now.Add(10 * time.Second)
	tr.Build(Inputs{Tmux: snap("✳ j", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Screen: scr, ScreenSeq: 1, ScreenAt: later, Now: now})
	s = tr.Build(Inputs{Tmux: snap("✳ j", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Screen: scr, ScreenSeq: 2, ScreenAt: later, Now: now})
	if a := s.Agents[0]; a.State != agent.Idle {
		t.Fatalf("screen clears stale block: %+v", a)
	}
	// hooks say working, screen shows a blocker -> blocked
	hook["%1"] = agent.Agent{PaneID: "%1", Kind: "claude", State: agent.Working, HasHooks: true, StateSince: now}
	scr["%1"] = rules.Result{Matched: true, State: agent.Blocked}
	s = tr.Build(Inputs{Tmux: snap("◑ j", "$2"), ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Screen: scr, Now: now})
	if a := s.Agents[0]; a.State != agent.Blocked || a.Reason != "prompt" {
		t.Fatalf("screen blocker over hook working: %+v", a)
	}
	// hooks say working (a turn interrupted with Esc emits no Stop), the registry still says busy,
	// but the screen shows a bare idle prompt box in three fresh samples -> idle, without waiting
	// for two 10 s registry samples
	tr = NewTracker()
	hook["%1"] = agent.Agent{PaneID: "%1", Kind: "claude", State: agent.Working, CurrentTool: "Bash", HasHooks: true, StateSince: now}
	tm := snap("✳ j", "$2")
	tm.Panes[0].TTY = "/dev/ttyagent"
	reg := map[string]claudereg.Entry{"/dev/ttyagent": {PID: 42, Status: "busy", Name: "j"}}
	scr["%1"] = rules.Result{Matched: true, State: agent.Idle, RuleID: "live_prompt_box", Region: "prompt_box_body"}
	for i := 1; i <= 2; i++ {
		s = tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: reg, RegistrySeq: 1, RegistryAt: later, Screen: scr, ScreenSeq: i, ScreenAt: later, Now: now})
		if a := s.Agents[0]; a.State != agent.Working {
			t.Fatalf("sample %d: two idle screens must not clear working yet: %+v", i, a)
		}
	}
	s = tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: reg, RegistrySeq: 1, RegistryAt: later, Screen: scr, ScreenSeq: 3, ScreenAt: later, Now: now})
	if a := s.Agents[0]; a.State != agent.Idle || a.CurrentTool != "" {
		t.Fatalf("three idle screens should clear working even with a registry: %+v", a)
	}
	if len(s.Corrections) != 1 || s.Corrections[0].Reason != "screen idle" {
		t.Fatalf("screen correction not reported: %+v", s.Corrections)
	}
	// the title-based idle rule is no evidence
	tr = NewTracker()
	scr["%1"] = rules.Result{Matched: true, State: agent.Idle, RuleID: "osc_title_idle", Region: "osc_title"}
	for i := 1; i <= 3; i++ {
		s = tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: reg, RegistrySeq: 1, RegistryAt: later, Screen: scr, ScreenSeq: i, ScreenAt: later, Now: now})
	}
	if a := s.Agents[0]; a.State != agent.Working {
		t.Fatalf("osc_title idle must not clear working: %+v", a)
	}
}

// A fresh turn must show as working at once even after a long idle period full of "idle"
// registry samples, and old samples must never overrule a newer hook state.
func TestStaleEvidenceDoesNotHideANewTurn(t *testing.T) {
	tr := NewTracker()
	ads := agent.Enabled([]string{"claude"})
	t0 := time.Now()
	tm := snap("✳ job", "$2")
	tm.Panes[0].TTY = "/dev/ttyagent"
	idle := map[string]claudereg.Entry{"/dev/ttyagent": {PID: 42, Status: "idle"}}
	hook := map[string]agent.Agent{"%1": {PaneID: "%1", Kind: "claude", State: agent.Idle, HasHooks: true, StateSince: t0.Add(-time.Hour)}}
	for i := 1; i <= 20; i++ { // an hour of idle samples
		tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: idle, RegistrySeq: i, RegistryAt: t0.Add(-time.Duration(20-i) * time.Minute), Now: t0})
	}
	// prompt submitted: the hook record turns working now
	hook["%1"] = agent.Agent{PaneID: "%1", Kind: "claude", State: agent.Working, HasHooks: true, StateSince: t0, TurnStarted: t0}
	s := tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: idle, RegistrySeq: 20, RegistryAt: t0.Add(-time.Minute), Now: t0})
	if a := s.Agents[0]; a.State != agent.Working || len(s.Corrections) != 0 {
		t.Fatalf("stale idle samples must not hide the new turn: %+v corrections=%v", a, s.Corrections)
	}
	// the registry still says idle for a moment after the prompt (within the grace): not evidence
	s = tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: idle, RegistrySeq: 21, RegistryAt: t0.Add(time.Second), Now: t0.Add(time.Second)})
	s = tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: idle, RegistrySeq: 22, RegistryAt: t0.Add(2 * time.Second), Now: t0.Add(2 * time.Second)})
	if a := s.Agents[0]; a.State != agent.Working {
		t.Fatalf("samples within the grace must not count: %+v", a)
	}
	// then Claude's registry reports busy: counters reset, still working
	busy := map[string]claudereg.Entry{"/dev/ttyagent": {PID: 42, Status: "busy"}}
	s = tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: busy, RegistrySeq: 23, RegistryAt: t0.Add(6 * time.Second), Now: t0.Add(6 * time.Second)})
	if a := s.Agents[0]; a.State != agent.Working {
		t.Fatalf("busy registry keeps working: %+v", a)
	}
	// a genuine interruption: two fresh idle samples well after the turn started
	for i, at := range []time.Duration{20 * time.Second, 25 * time.Second} {
		s = tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: idle, RegistrySeq: 24 + i, RegistryAt: t0.Add(at), Now: t0.Add(at)})
	}
	if a := s.Agents[0]; a.State != agent.Idle || len(s.Corrections) != 1 {
		t.Fatalf("fresh idle samples should clear the interrupted turn: %+v", a)
	}
}

// A paused turn ("waiting" for background tasks) looks idle to the registry and the screen; the
// interrupted-turn fallback must leave it alone until it is older than StaleWorking.
func TestWaitingIsNotAnInterruptedTurn(t *testing.T) {
	tr := NewTracker()
	ads := agent.Enabled([]string{"claude"})
	now := time.Now()
	tm := snap("✳ job", "$2")
	tm.Panes[0].TTY = "/dev/ttyagent"
	idle := map[string]claudereg.Entry{"/dev/ttyagent": {PID: 42, Status: "idle"}}
	hook := map[string]agent.Agent{"%1": {PaneID: "%1", Kind: "claude", State: agent.Working, Reason: "waiting", CurrentTool: "2 agents", HasHooks: true, StateSince: now.Add(-time.Minute)}}
	var s Snapshot
	for i := 1; i <= 4; i++ {
		s = tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: idle, RegistrySeq: i, RegistryAt: now.Add(time.Duration(i) * 10 * time.Second), Now: now.Add(time.Duration(i) * 10 * time.Second)})
	}
	if a := s.Agents[0]; a.State != agent.Working || a.CurrentTool != "2 agents" || len(s.Corrections) != 0 {
		t.Fatalf("waiting must survive idle registry samples: %+v", a)
	}
	// older than StaleWorking: the fallback applies again
	old := now.Add(2 * time.Hour)
	s = tr.Build(Inputs{Tmux: tm, ClientTTY: "/dev/ttys9", Adapters: ads, Hook: hook, Registry: idle, RegistrySeq: 5, RegistryAt: old, Now: old, StaleWorking: time.Hour})
	if a := s.Agents[0]; a.State != agent.Idle {
		t.Fatalf("stale waiting should be overruled: %+v", a)
	}
}
