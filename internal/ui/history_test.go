package ui

import (
	"strings"
	"testing"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/remote"
)

// The focus history follows the front server's focus across local and remote moves; `last`
// walks it at four granularities and skips a session that is gone.
func TestFocusHistoryAcrossServers(t *testing.T) {
	m, _ := newTestModel(t)
	focus := func(sess, win, pane string) merge.Focus {
		return merge.Focus{Found: true, SessionID: sess, SessionName: "s" + sess, WindowID: win, PaneID: pane}
	}
	m.local = merge.Snapshot{Spaces: []agent.Space{{SessionID: "$1"}, {SessionID: "$2"}}, Focus: focus("$1", "@1", "%1"),
		Agents: []agent.Agent{{PaneID: "%1", SessionID: "$1", WindowID: "@1"}, {PaneID: "%3", SessionID: "$2", WindowID: "@3"}}}
	m.hostList = []hosts.Host{{Name: "beta", Enabled: true}}
	m.hostSet = hosts.Set{Hosts: m.hostList}
	m.remotes = map[string]hostView{"beta": {state: remote.Connected, hasSnap: true,
		snap: merge.Snapshot{Spaces: []agent.Space{{SessionID: "$7"}}, Focus: focus("$7", "@7", "%7")}}}
	m.refederate()
	m.refederate() // the same focus twice is one entry
	if len(m.history) != 1 || m.history[0].PaneID != "%1" || m.history[0].Host != "" {
		t.Fatalf("history %+v", m.history)
	}
	m.local.Focus = focus("$1", "@1", "%2") // another pane, same window
	m.refederate()
	m.local.Focus = focus("$2", "@3", "%3") // another session
	m.refederate()
	m.front = "beta" // the host comes to the front: its focus is current
	m.refederate()
	if len(m.history) != 4 || m.history[0].Host != "beta" || m.history[0].PaneID != "%7" {
		t.Fatalf("history %+v", m.history)
	}
	// from beta: session and server cross back to local; window and pane stay on beta's server and
	// session, where nothing precedes
	for kind, want := range map[string]string{"session": "%3", "server": "%3"} {
		if e, ok := m.lastTarget(kind); !ok || e.PaneID != want || e.Host != "" {
			t.Errorf("%s from beta: %+v %v", kind, e, ok)
		}
	}
	for _, kind := range []string{"window", "pane"} {
		if e, ok := m.lastTarget(kind); ok {
			t.Errorf("%s from beta stays within beta's session: %+v", kind, e)
		}
	}
	m.front = ""
	m.local.Focus = focus("$1", "@1", "%1")
	m.refederate() // back on local, in $1's window @1 on pane %1: history %1 ← beta %7 ← %3 ← %2 ← %1
	if e, ok := m.lastTarget("session"); !ok || e.Host != "beta" || e.PaneID != "%7" {
		t.Fatalf("session: %+v %v", e, ok)
	}
	if e, ok := m.lastTarget("server"); !ok || e.Host != "beta" {
		t.Fatalf("server: %+v %v", e, ok)
	}
	if e, ok := m.lastTarget("pane"); !ok || e.PaneID != "%2" { // the previous pane of this window, past the other session and server
		t.Fatalf("pane: %+v %v", e, ok)
	}
	if _, ok := m.lastTarget("window"); ok { // $1 has one window in the history
		t.Fatal("window stays within the session")
	}
	m.local.Focus = focus("$1", "@2", "%4")
	m.refederate()
	if e, ok := m.lastTarget("window"); !ok || e.WindowID != "@1" || e.PaneID != "%1" {
		t.Fatalf("window: %+v %v", e, ok)
	}
	if e, ok := m.lastTarget("agent"); !ok || e.PaneID != "%1" || e.Host != "" { // from %4: %7 on beta is no agent, %3 … wait, %1 is the latest agent pane before %4
		t.Fatalf("agent: %+v %v", e, ok)
	}
	m.remotes["beta"] = hostView{state: remote.Connected, hasSnap: true, snap: merge.Snapshot{Focus: focus("$7", "@7", "%7")}} // beta lost session $7
	if e, ok := m.lastTarget("session"); !ok || e.SessionID != "$2" || e.Host != "" {
		t.Fatalf("a gone session is skipped: %+v %v", e, ok)
	}
	if _, ok := m.lastTarget("server"); ok {
		t.Fatal("no other server with a live session")
	}
	// lastCmd: a target gives a command, nothing to go back to says so
	if cmd := m.lastCmd("session"); cmd == nil || m.errText != "" {
		t.Fatalf("lastCmd session: %v %q", cmd, m.errText)
	}
	m.history = m.history[:1]
	if cmd := m.lastCmd("session"); cmd != nil || !strings.Contains(m.errText, "nowhere to go back to") {
		t.Fatalf("lastCmd session: %v %q", cmd, m.errText)
	}
	m.errText = ""
	if cmd := m.lastCmd("window"); cmd == nil || m.errText != "" { // locally tmux's own last-window still knows
		t.Fatalf("lastCmd window falls back to tmux: %v %q", cmd, m.errText)
	}
	if cmd := m.lastCmd("sideways"); cmd != nil || !strings.Contains(m.errText, "session, window, pane, agent or server") {
		t.Fatalf("lastCmd bad kind: %v %q", cmd, m.errText)
	}
}
