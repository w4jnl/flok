package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/tmux"
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
	if len(lines) != 30 || !strings.Contains(lines[0], "⌈⩓⌉") || lines[1] != strings.Repeat("─", 6) || !strings.Contains(lines[2], "①") {
		t.Fatalf("rail: want the mark and a separator above ①, got %q", lines[:3])
	}
	m.width, m.height = 6, 30
	if p, i, ok := m.rowAt(2); !ok || p != panelSpaces || i != 0 {
		t.Fatalf("a click on line 2 of the rail must hit the first session: %d %d %v", p, i, ok)
	}
	if short := render(m, 6, 7); !strings.Contains(short[0], "①") {
		t.Fatalf("a short rail drops the mark: %q", short[0])
	}
}

func TestThemeFollowsTheTerminalRecord(t *testing.T) {
	m, _ := newTestModel(t)
	def := config.Default().Theme
	if !m.dark || m.theme.Brand != lipgloss.Color(def.Brand) {
		t.Fatal("no record: the dark palette")
	}
	m.p.Prime(tmux.Snapshot{}, "raw")
	if err := m.d.Store.SetTerminalTheme("light", "#fffbeb"); err != nil {
		t.Fatal(err)
	}
	m.vc.valid = true
	next, _ := m.Update(m.rebuild(true)().(snapshotMsg))
	m = next.(Model)
	if m.dark || m.theme.Brand != lipgloss.Color(def.Light.Brand) || m.theme.FG != lipgloss.Color(def.Light.FG) || m.vc.valid {
		t.Fatalf("a light record switches the palette and repaints: dark=%v brand=%v valid=%v", m.dark, m.theme.Brand, m.vc.valid)
	}
	m.d.Cfg.Theme.Mode = "dark" // config wins over the record
	next, _ = m.Update(m.rebuild(true)().(snapshotMsg))
	if m = next.(Model); !m.dark {
		t.Fatal("[theme] mode = dark overrides a light terminal")
	}
}

func TestFooterWrapsMessagesAboveTheStatusLine(t *testing.T) {
	m := brandModel(t)
	m.focused = false
	m.snap.Warnings = []string{"macmini: upgrade flok there, no key relay (HEAD-e70d458)"}
	lines := render(m, 28, 24)
	if len(lines) != 24 {
		t.Fatalf("%d lines, want the panel height", len(lines))
	}
	if got := []string{strings.TrimRight(lines[20], " "), strings.TrimRight(lines[21], " "), strings.TrimRight(lines[22], " "), strings.TrimRight(lines[23], " ")}; !reflect.DeepEqual(got,
		[]string{"macmini: upgrade flok there,", "no key relay (HEAD-e70d458)", "", "click or prefix g to focus"}) {
		t.Fatalf("a long warning wraps, a blank line, then the status line: %q", got)
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 28 {
			t.Fatalf("overrun: %q", l)
		}
	}
	// three lines fit whole; longer than that ends with an ellipsis; a second warning is counted
	m.snap.Warnings = []string{"beta: the quick brown fox jumps over the lazy dog again and again and again"}
	lines = render(m, 28, 24)
	if !strings.HasPrefix(lines[19], "beta: the quick brown fox") || !strings.HasPrefix(lines[20], "jumps over the lazy dog") || strings.TrimRight(lines[21], " ") != "again and again and again" ||
		strings.TrimRight(lines[22], " ") != "" || !strings.HasPrefix(lines[23], "click or prefix g") {
		t.Fatalf("three lines, a blank, the status line: %q", lines[19:24])
	}
	m.snap.Warnings = append(m.snap.Warnings, "gamma: down")
	if lines = render(m, 60, 24); !strings.HasSuffix(strings.TrimRight(lines[21], " "), "(+1)") || strings.TrimRight(lines[22], " ") != "" {
		t.Fatalf("further warnings are counted: %q", lines[21:24])
	}
	m.snap.Warnings = []string{"beta: " + strings.Repeat("word ", 30)}
	if lines = render(m, 28, 24); !strings.HasSuffix(strings.TrimRight(lines[21], " "), "…") || !strings.HasPrefix(lines[23], "click or prefix g") {
		t.Fatalf("capped at three lines, the cut marked: %q", lines[19:24])
	}
	// no message: one line, the status alone; a short panel keeps the lines together, a very
	// short one shows the message alone
	m.snap.Warnings = nil
	if lines = render(m, 28, 24); strings.TrimRight(lines[22], " ") != "" || !strings.HasPrefix(lines[23], "click or prefix g") {
		t.Fatalf("no message: %q", lines[22:])
	}
	m.snap.Warnings = []string{"beta: Connection refused"}
	if lines = render(m, 28, 14); len(lines) != 14 || !strings.HasPrefix(lines[12], "beta: Connection refused") || !strings.HasPrefix(lines[13], "click or prefix g") {
		t.Fatalf("short panel, no blank line: %q", lines[12:])
	}
	if lines = render(m, 28, 8); len(lines) != 8 || !strings.HasPrefix(lines[7], "beta: Connection refused") {
		t.Fatalf("very short panel: %q", lines[len(lines)-1])
	}
	// the hints never overrun either: joinLR drops the right side, hintLines wraps instead
	if got := joinLR("⏎ front · c d r m x i", "? help", 24); got != "⏎ front · c d r m x i   " {
		t.Fatalf("joinLR narrow: %q", got)
	}
	dim := lipgloss.NewStyle()
	if got := hintLines("⏎ front · c d r m x i I", "? help", 40, dim); len(got) != 1 || got[0] != "⏎ front · c d r m x i I           ? help" {
		t.Fatalf("hints that fit stay on one line: %q", got)
	}
	if got := hintLines("⏎ front · c d r m x i I", "? help", 28, dim); !reflect.DeepEqual(got, []string{"⏎ front · c d r m x i I     ", "                      ? help"}) {
		t.Fatalf("hints too wide wrap, the help right-aligned on its own line: %q", got)
	}
	if got := hintLines("⏎ front · c d r m x i I", "? help", 12, dim); !reflect.DeepEqual(got, []string{"⏎ front · c ", "d r m x i I ", "      ? help"}) {
		t.Fatalf("a narrow panel wraps the keys too: %q", got)
	}
	if got := hintLines("⏎ front · c d r m x i I", "? help", 20, dim); !reflect.DeepEqual(got, []string{"⏎ front · c d r m x ", "i I           ? help"}) {
		t.Fatalf("the help shares the last line when it fits: %q", got)
	}
	m.focused = true
	m.snap.Warnings = nil
	if lines = render(m, 20, 24); !strings.HasPrefix(lines[22], "j/k ⏎ ⇥ 1-9") || strings.TrimRight(lines[23], " ") != "        esc · ? help" {
		t.Fatalf("a narrow panel gets the agent hints on two lines: %q", lines[22:])
	}
	if got := joinLR("j/k ⏎ ⇥ 1-9", "esc · ? help", 28); got != "j/k ⏎ ⇥ 1-9     esc · ? help" {
		t.Fatalf("joinLR fits: %q", got)
	}
	if got := wrapWords("a bb ccc dddddddd ee", 5, 0); !reflect.DeepEqual(got, []string{"a bb", "ccc", "ddddd", "ddd", "ee"}) {
		t.Fatalf("wrapWords: %q", got)
	}
	if got := wrapWords("one two three four five six", 9, 2); !reflect.DeepEqual(got, []string{"one two", "three fo…"}) {
		t.Fatalf("wrapWords capped: %q", got)
	}
}
