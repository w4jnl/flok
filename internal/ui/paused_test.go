package ui

import (
	"strings"
	"testing"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/merge"
)

// A paused agent (answered, background work still running) is a still grey ◍ with what it
// waits for, never the spinner; the session's dot follows.
func TestPausedAgentRendersStill(t *testing.T) {
	m, _ := newTestModel(t)
	m.local = merge.Snapshot{
		Spaces: []agent.Space{{SessionID: "$1", SessionName: "Alpha", Rollup: agent.Paused, AgentCount: 1}},
		Agents: []agent.Agent{{PaneID: "%1", SessionID: "$1", SessionName: "Alpha", Name: "api", Kind: "claude", State: agent.Paused, Reason: "waiting", CurrentTool: "2 shells", HasHooks: true}},
	}
	m.refederate()
	var row, sess string
	for _, l := range render(m, 40, 30) {
		if strings.Contains(l, " api") {
			row = l
		}
		if strings.Contains(l, " Alpha") {
			sess = l
		}
	}
	if !strings.Contains(row, "◍ api") || !strings.HasSuffix(strings.TrimRight(row, " "), "bg 2 shells") {
		t.Fatalf("paused row %q", row)
	}
	if !strings.Contains(sess, "◍ Alpha") {
		t.Fatalf("session dot %q", sess)
	}
	if right, col := m.agentRight(m.snap.Agents[0]); right != "bg 2 shells" || col != m.theme.Comment {
		t.Fatalf("detail %q %v", right, col)
	}
	if agent.Priority(agent.Paused) <= agent.Priority(agent.Working) || agent.Priority(agent.Paused) >= agent.Priority(agent.Idle) {
		t.Fatal("paused ranks between working and idle")
	}
}
