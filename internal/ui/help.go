package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/w4jnl/flok/internal/keys"
)

// helpRow is one rendered line of the keybinds overlay: a section header or a binding.
type helpRow struct {
	header  bool
	section string
	key     string
	label   string
}

// HelpModel is the herdr-style keybinds overlay, used standalone in a tmux popup and embedded
// in the sidebar.
type HelpModel struct {
	theme      Theme
	rows       []helpRow
	filter     string
	filtering  bool
	offset     int
	width      int
	height     int
	standalone bool
	closed     bool
}

type helpClosedMsg struct{}

func NewHelp(theme Theme, sections []keys.Section, standalone bool) HelpModel {
	var rows []helpRow
	for _, s := range sections {
		rows = append(rows, helpRow{header: true, section: s.Name})
		for _, b := range s.Bindings {
			key := b.Key
			if b.Repeat {
				key += " (repeat)"
			}
			rows = append(rows, helpRow{section: s.Name, key: key, label: b.Label})
		}
	}
	return HelpModel{theme: theme, rows: rows, standalone: standalone}
}

func (m HelpModel) Init() tea.Cmd { return nil }

// visible applies the filter, keeping headers that still have rows.
func (m HelpModel) visible() []helpRow {
	if m.filter == "" {
		return m.rows
	}
	q := strings.ToLower(m.filter)
	var out []helpRow
	var pending *helpRow
	for i := range m.rows {
		r := m.rows[i]
		if r.header {
			pending = &m.rows[i]
			continue
		}
		if strings.Contains(strings.ToLower(r.key), q) || strings.Contains(strings.ToLower(r.label), q) ||
			strings.Contains(strings.ToLower(r.section), q) {
			if pending != nil {
				out = append(out, *pending)
				pending = nil
			}
			out = append(out, r)
		}
	}
	return out
}

func (m HelpModel) bodyHeight() int {
	h := m.height - 3 // title, hint, footer
	if h < 1 {
		h = 1
	}
	return h
}

func (m HelpModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.offset -= 3
		case tea.MouseButtonWheelDown:
			m.offset += 3
		}
	case tea.KeyMsg:
		// Characters that arrive in one read (fast typing, send-keys, paste) come as a single
		// multi-rune message; handle them one by one so "/kill" works like "/", "k", "i", ...
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste {
			var model tea.Model = m
			var cmd tea.Cmd
			for _, r := range msg.Runes {
				model, cmd = model.(HelpModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				if cmd != nil {
					return model, cmd
				}
			}
			return model, nil
		}
		k := msg.String()
		if m.filtering {
			switch k {
			case "esc":
				m.filter, m.filtering = "", false
			case "enter":
				m.filtering = false
			case "backspace":
				if r := []rune(m.filter); len(r) > 0 {
					m.filter = string(r[:len(r)-1])
				}
			case "ctrl+c":
				return m.close()
			default:
				if len(msg.Runes) > 0 && msg.Type == tea.KeyRunes {
					m.filter += string(msg.Runes)
				}
			}
			m.offset = 0
			return m, nil
		}
		switch k {
		case "esc", "q", "enter", "ctrl+c", "?":
			if k == "esc" && m.filter != "" {
				m.filter = ""
				return m, nil
			}
			return m.close()
		case "/":
			m.filtering = true
		case "j", "down":
			m.offset++
		case "k", "up":
			m.offset--
		case "pgdown", "ctrl+d", " ":
			m.offset += m.bodyHeight()
		case "pgup", "ctrl+u":
			m.offset -= m.bodyHeight()
		case "g", "home":
			m.offset = 0
		case "G", "end":
			m.offset = 1 << 30
		}
	}
	m.clamp()
	return m, nil
}

func (m *HelpModel) clamp() {
	max := len(m.visible()) - m.bodyHeight()
	if m.offset > max {
		m.offset = max
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m HelpModel) close() (tea.Model, tea.Cmd) {
	m.closed = true
	if m.standalone {
		return m, tea.Quit
	}
	return m, func() tea.Msg { return helpClosedMsg{} }
}

func (m HelpModel) View() string {
	if m.width == 0 {
		return ""
	}
	t, w := m.theme, m.width
	plain := lipgloss.NewStyle()
	dim := lipgloss.NewStyle().Foreground(t.Comment)
	title := lipgloss.NewStyle().Foreground(t.FG).Bold(true).Render("keybinds")
	badge := lipgloss.NewStyle().Foreground(t.BG).Background(t.Purple).Bold(true).Render(" esc close ")
	lines := []string{joinLR(title, badge, w)}
	if m.filtering || m.filter != "" {
		cur := ""
		if m.filtering {
			cur = "▏"
		}
		lines = append(lines, pad(dim.Render("search: ")+lipgloss.NewStyle().Foreground(t.FG).Render(m.filter+cur), w, plain))
	} else {
		lines = append(lines, pad(dim.Render("press / to filter by command or shortcut"), w, plain))
	}
	rows := m.visible()
	keyW := 10
	for _, r := range rows {
		if !r.header && ansi.StringWidth(r.key) > keyW {
			keyW = ansi.StringWidth(r.key)
		}
	}
	if keyW > 30 {
		keyW = 30
	}
	stacked := w < keyW+22
	body := m.bodyHeight()
	for i := m.offset; i < len(rows) && len(lines) < body+2; i++ {
		r := rows[i]
		if r.header {
			lines = append(lines, pad(lipgloss.NewStyle().Foreground(t.Purple).Bold(true).Render(r.section), w, plain))
			continue
		}
		key := lipgloss.NewStyle().Foreground(t.Pink).Bold(true).Render(ansi.Truncate(r.key, keyW, "…"))
		if stacked {
			lines = append(lines, pad(key, w, plain))
			if len(lines) < body+2 {
				lines = append(lines, pad("  "+dim.Render(ansi.Truncate(r.label, w-2, "…")), w, plain))
			}
			continue
		}
		gap := keyW - ansi.StringWidth(r.key) + 2
		if gap < 1 {
			gap = 1
		}
		label := ansi.Truncate(r.label, w-keyW-2, "…")
		lines = append(lines, pad(key+strings.Repeat(" ", gap)+lipgloss.NewStyle().Foreground(t.FG).Render(label), w, plain))
	}
	for len(lines) < body+2 {
		lines = append(lines, strings.Repeat(" ", w))
	}
	footer := dim.Render("search ") + lipgloss.NewStyle().Foreground(t.Cyan).Render("/") + dim.Render(" · scroll ") +
		lipgloss.NewStyle().Foreground(t.FG).Render("j/k/↑↓/pgup/pgdn") + dim.Render(" · close ") +
		lipgloss.NewStyle().Foreground(t.FG).Render("esc/enter")
	lines = append(lines, pad(ansi.Truncate(footer, w, ""), w, plain))
	return strings.Join(lines, "\n")
}

// Dump renders the sections as plain text (for `keys --print` and tests).
func Dump(sections []keys.Section, filter string) string {
	m := NewHelp(Theme{}, sections, true)
	m.filter = filter
	var b strings.Builder
	for _, r := range m.visible() {
		if r.header {
			b.WriteString("\n" + r.section + "\n")
			continue
		}
		b.WriteString("  " + r.key + strings.Repeat(" ", max(1, 26-len(r.key))) + r.label + "\n")
	}
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// WithSidebarSections slots the sidebar's own key sections into a help: after the flok
// bindings (the first section, when tmux has any), before the tmux tables.
func WithSidebarSections(secs []keys.Section, multiHost bool) []keys.Section {
	side := SidebarKeySections(multiHost)
	if len(secs) > 0 && secs[0].Name == keys.FlokSection {
		return append(append([]keys.Section{secs[0]}, side...), secs[1:]...)
	}
	return append(side, secs...)
}

// SidebarKeySections describes the sidebar's own keys for the help overlay (the rest of it
// lists the tmux bindings); the servers section appears with remote hosts.
func SidebarKeySections(multiHost bool) []keys.Section {
	row := func(k, l string) keys.Binding { return keys.Binding{Key: k, Label: l} }
	secs := []keys.Section{{Name: "sidebar", Bindings: []keys.Binding{
		row("j / k", "move the cursor"),
		row("Tab / Shift-Tab", "next / previous panel"),
		row("Enter", "open the row and hand the keyboard to the work pane"),
		row("1-9 · ! @ # …", "agent N · session N"),
		row("g / G", "first / last row"),
		row("n", "name the agent row (Enter saves; an empty name restores its directory)"),
		row("r", "refresh"),
		row("Esc / q", "keyboard back to the work pane"),
	}}}
	if multiHost {
		secs = append(secs, keys.Section{Name: "sidebar · servers", Bindings: []keys.Binding{
			row("Enter", "bring the host's work pane to the front"),
			row("Space", "the same, the keyboard stays in the sidebar"),
			row("c / d", "connect / disconnect (persisted: off until c)"),
			row("r", "reconnect now"),
			row("m", "flip the mode: full ↔ plain"),
			row("x", "remove the host (asks y/n)"),
			row("i", "host details"),
			row("I", "install or upgrade flok on the host from here (a popup shows it)"),
		}})
	}
	return secs
}
