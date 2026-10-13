package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/w4jnl/flok/internal/link"
)

// The sidebar's brand line, the text form of assets/brand: the [flok] wordmark on top of the
// wide layout, and on the rail the mark's top half — the chevron ⩓ of the brand's ASCII lockup
// (internal/cli/brand.go) under ⌈ ⌉ — above a separator. Like the icon, the frame takes the brand
// accent and the name and chevron the foreground. With [link] url the phone link's state sits at
// the right end of the line (linkMark).

// brandRows is how many lines the brand takes: one on the wide layout, two on the rail, none
// when switched off ([sidebar] brand) or when the pane is too short to spare them.
func (m Model) brandRows() int {
	if !m.d.Cfg.Sidebar.Brand {
		return 0
	}
	if m.isRail() {
		if m.height < 8 {
			return 0
		}
		return 2
	}
	if m.height < 10 {
		return 0
	}
	return 1
}

func (m Model) brandStyles() (frame, ink lipgloss.Style) {
	return lipgloss.NewStyle().Foreground(m.theme.Brand).Bold(true), lipgloss.NewStyle().Foreground(m.theme.FG).Bold(true)
}

// brandWordmark is "[flok]" for the wide layout.
func (m Model) brandWordmark() string {
	frame, ink := m.brandStyles()
	return frame.Render("[") + ink.Render("flok") + frame.Render("]")
}

// brandRail is the rail's mark and the separator under it (styled like the one between the
// sessions and the agents); the leading space puts the chevron in the column of the digits.
func (m Model) brandRail() []string {
	frame, ink := m.brandStyles()
	sep := lipgloss.NewStyle().Foreground(m.theme.Comment).Render(strings.Repeat("─", m.width))
	mark := " " + frame.Render("⌈") + ink.Render("⩓") + frame.Render("⌉")
	if dot := m.linkMark(true); dot != "" {
		mark = joinLR(mark, dot, m.width)
	}
	return []string{mark, sep}
}

// linkMark is the phone link's state, quiet unless something is wrong: "phone" and a dot, green
// while connected, a grey ring when switched off (flok link off), a dotted one while connecting
// (orange while the relay is out of reach), a red cross when it refuses; dot alone gives the
// rail's form. "" without [link] url.
func (m Model) linkMark(dotAlone bool) string {
	if m.linker == nil {
		return ""
	}
	t := m.theme
	glyph, col := "◌", t.Comment
	switch m.linkStatus.State {
	case link.Connected:
		glyph, col = "●", t.Green
	case link.Off:
		glyph = "○"
	case link.Unreachable:
		col = t.Orange
	case link.Refused:
		glyph, col = "✗", t.Red
	}
	dot := lipgloss.NewStyle().Foreground(col).Render(glyph)
	if dotAlone {
		return dot
	}
	return lipgloss.NewStyle().Foreground(t.Comment).Render("phone ") + dot
}
