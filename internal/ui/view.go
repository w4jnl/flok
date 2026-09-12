package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/w4jnl/flok/internal/agent"
)

type layout struct {
	rail                  bool
	spacesTop, spacesRows int
	agentsTop, agentsRows int // agentsRows counts agents, each perAgent lines tall
	footerY               int
	perAgent              int
}

func (m Model) isRail() bool { return m.width > 0 && m.width < m.d.Cfg.Sidebar.RailThreshold }

func (m Model) layout() layout {
	h := m.height
	nS, nA := len(m.snap.Spaces), len(m.snap.Agents)
	if m.isRail() {
		avail := h - 1 // separator
		if avail < 2 {
			avail = 2
		}
		s := nS
		if max := avail / 2; s > max {
			s = max
		}
		a := avail - s
		if nA < a {
			extra := a - nA
			if grow := nS - s; grow < extra {
				extra = grow
			}
			s += extra
			a -= extra
		}
		return layout{rail: true, spacesTop: 0, spacesRows: s, agentsTop: s + 1, agentsRows: a, perAgent: 1}
	}
	per := m.agentRows()
	avail := h - 4 // two headers, one blank line, footer
	if avail < 1+per {
		avail = 1 + per
	}
	maxS := int(float64(h) * m.d.Cfg.Sidebar.SessionsMaxRatio)
	if maxS < 1 {
		maxS = 1
	}
	s := nS
	if s > maxS {
		s = maxS
	}
	if s < 1 {
		s = 1
	}
	a := (avail - s) / per
	if nA < a { // spare agent slots: let the sessions list grow into them
		grow := (a - nA) * per
		if nS-s < grow {
			grow = nS - s
		}
		if grow > 0 {
			s += grow
			a = (avail - s) / per
		}
	}
	if a < 1 {
		a = 1
	}
	return layout{spacesTop: 1, spacesRows: s, agentsTop: 1 + s + 2, agentsRows: a, footerY: h - 1, perAgent: per}
}

// agentRows is the configured number of lines per agent row, clamped to 1..2.
func (m Model) agentRows() int {
	per := m.d.Cfg.Sidebar.AgentRows
	if per < 1 {
		per = 1
	}
	if per > 2 {
		per = 2
	}
	return per
}

// rowAt maps a screen row to (panel, index) for mouse clicks.
func (m Model) rowAt(y int) (int, int, bool) {
	lay := m.layout()
	if y >= lay.spacesTop && y < lay.spacesTop+lay.spacesRows {
		i := y - lay.spacesTop + m.offset[panelSpaces]
		return panelSpaces, i, i < len(m.snap.Spaces)
	}
	per := lay.perAgent
	if per < 1 {
		per = 1
	}
	if y >= lay.agentsTop && y < lay.agentsTop+lay.agentsRows*per {
		i := (y-lay.agentsTop)/per + m.offset[panelAgents]
		return panelAgents, i, i < len(m.snap.Agents)
	}
	return 0, 0, false
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if m.help != nil {
		return m.help.View()
	}
	if m.vc != nil && m.vc.valid {
		return m.vc.s
	}
	var out string
	if m.isRail() {
		out = m.viewRail()
	} else {
		out = m.viewFull()
	}
	if m.vc != nil {
		m.vc.s, m.vc.valid = out, true
	}
	return out
}

func pad(s string, w int, base lipgloss.Style) string {
	if gap := w - ansi.StringWidth(s); gap > 0 {
		return s + base.Render(strings.Repeat(" ", gap))
	}
	return ansi.Truncate(s, w, "")
}

func joinLR(left, right string, w int) string {
	lw, rw := ansi.StringWidth(left), ansi.StringWidth(right)
	if lw+rw+1 > w {
		left = ansi.Truncate(left, w-rw-1, "…")
		lw = ansi.StringWidth(left)
	}
	gap := w - lw - rw
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) viewFull() string {
	t, w, lay := m.theme, m.width, m.layout()
	hdr := lipgloss.NewStyle().Foreground(t.Purple).Bold(true)
	dim := lipgloss.NewStyle().Foreground(t.Comment)
	plain := lipgloss.NewStyle()
	blank := strings.Repeat(" ", w)

	lines := make([]string, 0, m.height)
	lines = append(lines, pad(hdr.Render("sessions"), w, plain))
	for r := 0; r < lay.spacesRows; r++ {
		if i := m.offset[panelSpaces] + r; i < len(m.snap.Spaces) {
			lines = append(lines, m.spaceRow(i, w))
		} else {
			lines = append(lines, blank)
		}
	}
	lines = append(lines, blank)
	title := hdr.Render("agents")
	if m.snap.Unseen > 0 {
		title += " " + lipgloss.NewStyle().Foreground(t.Orange).Render(fmt.Sprintf("· %d", m.snap.Unseen))
	}
	lines = append(lines, joinLR(title, dim.Render("priority"), w))
	for r := 0; r < lay.agentsRows; r++ {
		if i := m.offset[panelAgents] + r; i < len(m.snap.Agents) {
			lines = append(lines, m.agentRow(i, w, lay.perAgent)...)
		} else {
			for k := 0; k < lay.perAgent; k++ {
				lines = append(lines, blank)
			}
		}
	}
	for len(lines) < m.height-1 {
		lines = append(lines, blank)
	}
	if len(lines) > m.height-1 {
		lines = lines[:m.height-1]
	}
	lines = append(lines, m.footer(w))
	return strings.Join(lines, "\n")
}

func (m Model) spaceRow(i, w int) string {
	t := m.theme
	s := m.snap.Spaces[i]
	sel := m.panel == panelSpaces && m.cursor[panelSpaces] == i
	base := lipgloss.NewStyle()
	if s.Current {
		base = base.Background(t.CurrentLine)
	}
	glyph, col := "○", t.Comment
	if s.AgentCount > 0 {
		glyph, col = t.Glyph(s.Rollup, m.frame), t.StateColor(s.Rollup)
	}
	nameStyle := base.Foreground(t.FG)
	if sel && m.focused {
		nameStyle = nameStyle.Foreground(t.Pink).Bold(true)
	} else if s.Current {
		nameStyle = nameStyle.Bold(true)
	}
	branch := ""
	if m.d.Cfg.Sidebar.ShowBranch {
		branch = s.Branch
	}
	avail := w - 3
	bw := 0
	if branch != "" {
		if max := avail / 2; ansi.StringWidth(branch) > max {
			branch = ansi.Truncate(branch, max, "…")
		}
		bw = ansi.StringWidth(branch) + 1
		if avail-bw < 6 {
			branch, bw = "", 0
		}
	}
	name := ansi.Truncate(s.SessionName, avail-bw, "…")
	gap := avail - bw - ansi.StringWidth(name)
	if gap < 0 {
		gap = 0
	}
	row := base.Render(" ") + base.Foreground(col).Render(glyph) + base.Render(" ") +
		nameStyle.Render(name) + base.Render(strings.Repeat(" ", gap))
	if branch != "" {
		row += base.Render(" ") + base.Foreground(t.Comment).Render(branch)
	}
	return pad(row, w, base)
}

func (m Model) agentRight(a agent.Agent) (string, lipgloss.Color) {
	t := m.theme
	now := time.Now()
	switch a.State {
	case agent.Working:
		el := elapsed(a.TurnStarted, now)
		if el == "" {
			el = elapsed(a.StateSince, now)
		}
		if a.CurrentTool != "" {
			return a.CurrentTool + " " + el, t.Working
		}
		return el, t.Working
	case agent.Blocked:
		r := a.Reason
		if r == "" {
			r = "input"
		}
		return strings.Replace(r, "permission:", "perm:", 1), t.Blocked
	case agent.Done:
		s := "done"
		if a.Unseen > 0 {
			s += fmt.Sprintf(" · %d", a.Unseen)
		}
		return s, t.Done
	case agent.Idle:
		return a.Kind, t.Comment
	}
	return a.Kind + " ?", t.Comment
}

// agentRow renders one agent as per lines: state glyph + project label + state detail, and
// (per == 2) a dim "kind · title" line, like herdr's workspace/agent rows.
func (m Model) agentRow(i, w, per int) []string {
	t := m.theme
	a := m.snap.Agents[i]
	sel := m.panel == panelAgents && m.cursor[panelAgents] == i
	focused := a.PaneID != "" && a.PaneID == m.snap.Focus.PaneID
	base := lipgloss.NewStyle()
	if focused {
		base = base.Background(t.CurrentLine)
	}
	label := a.Name
	if label == "" {
		label = a.SessionName
	}
	if !a.HasHooks {
		label = "~" + label
	}
	right, rcol := m.agentRight(a)
	if per > 1 { // the kind moves to the second line
		switch a.State {
		case agent.Idle:
			right = ""
		case agent.Unknown:
			right = "?"
		}
	}
	avail := w - 3
	rw := 0
	if right != "" {
		if max := avail / 2; ansi.StringWidth(right) > max {
			right = ansi.Truncate(right, max, "…")
		}
		rw = ansi.StringWidth(right) + 1
		if avail-rw < 6 {
			right, rw = "", 0
		}
	}
	label = ansi.Truncate(label, avail-rw, "…")
	gap := avail - rw - ansi.StringWidth(label)
	if gap < 0 {
		gap = 0
	}
	nameStyle := base.Foreground(t.FG)
	if sel && m.focused {
		nameStyle = nameStyle.Foreground(t.Pink).Bold(true)
	}
	row := base.Render(" ") + base.Foreground(t.StateColor(a.State)).Render(t.Glyph(a.State, m.frame)) + base.Render(" ") +
		nameStyle.Render(label) + base.Render(strings.Repeat(" ", gap))
	if right != "" {
		row += base.Render(" ") + base.Foreground(rcol).Render(right)
	}
	if per < 2 {
		return []string{pad(row, w, base)}
	}
	sub := a.Kind
	if a.Title != "" && !strings.EqualFold(a.Title, a.Name) && !genericTitle(a.Title) {
		sub += " · " + a.Title
	}
	sub = ansi.Truncate(sub, w-3, "…")
	line2 := base.Render("   ") + base.Foreground(t.Comment).Render(sub)
	return []string{pad(row, w, base), pad(line2, w, base)}
}

// genericTitle reports the placeholder titles agents show before they name a session.
func genericTitle(title string) bool {
	switch strings.ToLower(strings.TrimSpace(title)) {
	case "claude code", "claude", "copilot", "copilot cli", "github copilot":
		return true
	}
	return false
}

func (m Model) footer(w int) string {
	t := m.theme
	dim := lipgloss.NewStyle().Foreground(t.Comment)
	if m.errText != "" {
		return pad(lipgloss.NewStyle().Foreground(t.Red).Render(ansi.Truncate(m.errText, w, "…")), w, lipgloss.NewStyle())
	}
	if len(m.snap.Warnings) > 0 {
		return pad(lipgloss.NewStyle().Foreground(t.Orange).Render(ansi.Truncate(m.snap.Warnings[0], w, "…")), w, lipgloss.NewStyle())
	}
	if !m.focused { // keys go to the work pane until the sidebar is clicked or `prefix g` is pressed
		return pad(dim.Render("click or prefix g to focus"), w, lipgloss.NewStyle())
	}
	if m.prefixPending {
		return pad(lipgloss.NewStyle().Foreground(t.Pink).Render(m.prefixTmux+" …"), w, lipgloss.NewStyle())
	}
	return joinLR(dim.Render("j/k ⏎ ⇥ 1-9"), dim.Render("esc · ? help"), w)
}

func (m Model) viewRail() string {
	t, w, lay := m.theme, m.width, m.layout()
	plain := lipgloss.NewStyle()
	dim := lipgloss.NewStyle().Foreground(t.Comment)
	cursor := lipgloss.NewStyle().Foreground(t.Pink).Bold(true)
	if !m.focused { // keys are not arriving here: keep the position visible, but quietly
		cursor = lipgloss.NewStyle().Foreground(t.Comment)
	}
	lines := make([]string, 0, m.height)
	// marker shows the keyboard cursor of the active panel: "›" on the selected row.
	marker := func(sel bool, base lipgloss.Style) string {
		if sel {
			return cursor.Background(base.GetBackground()).Render("›")
		}
		return base.Render(" ")
	}
	overflow := func(n int) string { return pad(dim.Render(fmt.Sprintf("+%d", n)), w, plain) }

	for r := 0; r < lay.spacesRows; r++ {
		i := m.offset[panelSpaces] + r
		if i >= len(m.snap.Spaces) {
			lines = append(lines, strings.Repeat(" ", w))
			continue
		}
		s := m.snap.Spaces[i]
		sel := m.panel == panelSpaces && m.cursor[panelSpaces] == i
		hidden := len(m.snap.Spaces) - (m.offset[panelSpaces] + lay.spacesRows)
		if r == lay.spacesRows-1 && hidden > 0 && !sel {
			lines = append(lines, overflow(hidden+1))
			continue
		}
		base := lipgloss.NewStyle()
		if s.Current {
			base = base.Background(t.CurrentLine)
		}
		col := t.Comment
		if s.AgentCount > 0 {
			col = t.StateColor(s.Rollup)
		}
		digit := base.Foreground(col).Bold(sel).Render(circled(i + 1))
		lines = append(lines, pad(marker(sel, base)+base.Render(" ")+digit, w, base))
	}
	lines = append(lines, dim.Render(strings.Repeat("─", w)))
	for r := 0; r < lay.agentsRows; r++ {
		i := m.offset[panelAgents] + r
		if i >= len(m.snap.Agents) {
			lines = append(lines, strings.Repeat(" ", w))
			continue
		}
		a := m.snap.Agents[i]
		sel := m.panel == panelAgents && m.cursor[panelAgents] == i
		hidden := len(m.snap.Agents) - (m.offset[panelAgents] + lay.agentsRows)
		if r == lay.agentsRows-1 && hidden > 0 && !sel {
			lines = append(lines, overflow(hidden+1))
			continue
		}
		base := lipgloss.NewStyle()
		if a.PaneID != "" && a.PaneID == m.snap.Focus.PaneID {
			base = base.Background(t.CurrentLine)
		}
		mark := ""
		if a.Unseen > 0 {
			mark = base.Foreground(t.Orange).Render("•")
		}
		// like herdr's collapsed rail: cursor, the agent index in its state colour, the status glyph
		col := base.Foreground(t.StateColor(a.State)).Bold(sel)
		lines = append(lines, pad(marker(sel, base)+col.Render(fmt.Sprintf("%2d ", i+1))+col.Render(t.Glyph(a.State, m.frame))+mark, w, base))
	}
	for len(lines) < m.height {
		lines = append(lines, strings.Repeat(" ", w))
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}
