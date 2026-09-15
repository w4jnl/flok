package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The sidebar's brand line, the text form of assets/brand: the [flok] wordmark on top of the
// wide layout, and on the rail the mark's top half — the chevron ⩓ of the brand's ASCII lockup
// (internal/cli/brand.go) under ⌈ ⌉ — above a separator. Like the icon, the frame takes the brand
// accent and the name and chevron the foreground.

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
	return []string{" " + frame.Render("⌈") + ink.Render("⩓") + frame.Render("⌉"), sep}
}
