package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/w4jnl/flok/internal/link"
)

// The sidebar's brand line, the text form of assets/brand: the [flok] wordmark on top of the
// wide layout, and on the rail the mark's top half — the chevron ⩓ of the brand's ASCII lockup
// (internal/cli/brand.go) under ⌈ ⌉ — above a separator. Like the icon, the frame takes the brand
// accent and the name and chevron the foreground. With [link] url the relay link's state sits at
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
	if lm := m.linkMark(); lm != "" {
		mark = joinLR(mark, lm, m.width)
	}
	return []string{mark, sep}
}

// linkMark is the relay link's state in one cell, no word: the two-way arrow ⇄, green while
// connected, blinking grey and green while it connects (linkBlinks keeps the animation tick
// going), orange while the relay is out of reach, red when it refuses; a grey dash when switched
// off (flok link off): nothing flows. "" without [link] url.
func (m Model) linkMark() string {
	if m.linker == nil {
		return ""
	}
	t := m.theme
	glyph, col := "⇄", t.Green
	switch m.linkStatus.State {
	case link.Off:
		glyph, col = "–", t.Comment
	case link.Connecting:
		if time.Now().UnixMilli()/500%2 == 0 { // half a second grey, half green
			col = t.Comment
		}
	case link.Unreachable:
		col = t.Orange
	case link.Refused:
		col = t.Red
	}
	return lipgloss.NewStyle().Foreground(col).Render(glyph)
}

// linkBlinks reports that the relay link's mark blinks (connecting), so the animation tick runs.
func (m Model) linkBlinks() bool { return m.linker != nil && m.linkStatus.State == link.Connecting }
