package ui

import (
	"strings"
	"testing"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/merge"
)

// The agent in the pane in front wears ▶ and a bold name; the keyboard cursor › wins on the
// selected row; the rail marks it too. The agents header names the order only when it moves.
func TestCurrentAgentMarker(t *testing.T) {
	m, _ := newTestModel(t)
	m.local = merge.Snapshot{
		Spaces: []agent.Space{{SessionID: "$1", SessionName: "Alpha", Current: true, AgentCount: 2}},
		Agents: []agent.Agent{
			{PaneID: "%1", SessionID: "$1", SessionName: "Alpha", Name: "one", Kind: "claude", State: agent.Idle, HasHooks: true},
			{PaneID: "%2", SessionID: "$1", SessionName: "Alpha", Name: "two", Kind: "claude", State: agent.Working, HasHooks: true},
		},
		Focus: merge.Focus{Found: true, SessionID: "$1", PaneID: "%2"},
	}
	m.refederate()
	lines := render(m, 28, 30)
	var hdr, cur, other string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "agents"):
			hdr = l
		case strings.Contains(l, " two"):
			cur = l
		case strings.Contains(l, " one"):
			other = l
		}
	}
	if !strings.HasPrefix(cur, "▶") || strings.HasPrefix(other, "▶") || !strings.HasPrefix(other, " ") {
		t.Fatalf("marker: %q / %q", cur, other)
	}
	if strings.TrimSpace(hdr) != "agents" {
		t.Fatalf("stable order: no order word in the header, got %q", hdr)
	}
	m.d.Cfg.Sidebar.AgentOrder = "priority"
	for _, l := range render(m, 28, 30) {
		if strings.HasPrefix(l, "agents") && !strings.HasSuffix(strings.TrimRight(l, " "), "priority") {
			t.Fatalf("priority order says so: %q", l)
		}
	}
	// the cursor on the current row replaces the marker
	m.focused, m.panel, m.cursor[panelAgents] = true, panelAgents, 1
	for _, l := range render(m, 28, 30) {
		if strings.Contains(l, " two") && !strings.HasPrefix(l, "›") {
			t.Fatalf("cursor wins: %q", l)
		}
	}
	// the rail: ▶ before the index of the current agent (the cursor, shown whenever a row is
	// selected there, sits on another panel)
	m.focused, m.panel = false, panelSpaces
	rail := render(m, 6, 30)
	found := false
	for _, l := range rail {
		if strings.HasPrefix(l, "▶ 2 ") {
			found = true
		}
	}
	if !found {
		t.Fatalf("rail marker missing:\n%s", strings.Join(rail, "\n"))
	}
}
