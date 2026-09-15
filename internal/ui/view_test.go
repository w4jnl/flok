package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/merge"
)

func brandModel(t *testing.T) Model {
	t.Helper()
	m, _ := newTestModel(t)
	m.snap = merge.Snapshot{
		Spaces: []agent.Space{{SessionID: "$1", SessionName: "Alpha", AgentCount: 1, Rollup: agent.Working}, {SessionID: "$2", SessionName: "Beta"}},
		Agents: []agent.Agent{{PaneID: "%1", SessionID: "$1", SessionName: "Alpha", Kind: "claude", Name: "proj", State: agent.Working}},
	}
	return m
}

// render draws the sidebar at w x h and returns its lines without styling.
func render(m Model, w, h int) []string {
	m.width, m.height, m.vc = w, h, &viewCache{}
	return strings.Split(ansi.Strip(m.View()), "\n")
}

func TestBrandWordmarkOnTheWideLayout(t *testing.T) {
	m := brandModel(t)
	lines := render(m, 28, 30)
	if len(lines) != 30 || strings.TrimSpace(lines[0]) != "[flok]" || !strings.HasPrefix(lines[1], "sessions") {
		t.Fatalf("wide: want [flok] then sessions in 30 lines, got %d lines: %q", len(lines), lines[:3])
	}
	m.width, m.height = 28, 30
	if p, i, ok := m.rowAt(2); !ok || p != panelSpaces || i != 0 {
		t.Fatalf("a click on line 2 must hit the first session: %d %d %v", p, i, ok)
	}
	if _, _, ok := m.rowAt(0); ok {
		t.Fatal("the brand line is not a row")
	}
	if short := render(m, 28, 9); !strings.HasPrefix(short[0], "sessions") {
		t.Fatalf("a short pane drops the brand line first: %q", short[0])
	}
	m.d.Cfg.Sidebar.Brand = false
	if off := render(m, 28, 30); !strings.HasPrefix(off[0], "sessions") {
		t.Fatalf("brand = false: %q", off[0])
	}
}

func TestBrandMarkOnTheRail(t *testing.T) {
	m := brandModel(t)
	lines := render(m, 6, 30)
	if len(lines) != 30 || !strings.Contains(lines[0], "⌈⩓⌉") || !strings.Contains(lines[1], "⌊▁⌋") || !strings.Contains(lines[2], "①") {
		t.Fatalf("rail: want the two-line mark above ①, got %q", lines[:3])
	}
	m.width, m.height = 6, 30
	if p, i, ok := m.rowAt(2); !ok || p != panelSpaces || i != 0 {
		t.Fatalf("a click on line 2 of the rail must hit the first session: %d %d %v", p, i, ok)
	}
	if short := render(m, 6, 7); !strings.Contains(short[0], "①") {
		t.Fatalf("a short rail drops the mark: %q", short[0])
	}
}
