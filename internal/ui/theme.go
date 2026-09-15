package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
)

type Theme struct {
	BG, CurrentLine, FG, Comment, Cyan, Green, Orange, Pink, Purple, Red, Yellow lipgloss.Color
	Working, Blocked, Done, Idle                                                 lipgloss.Color
	Brand                                                                        lipgloss.Color // wordmark / rail mark accent
}

func NewTheme(c config.Palette) Theme {
	t := Theme{BG: lipgloss.Color(c.BG), CurrentLine: lipgloss.Color(c.CurrentLine), FG: lipgloss.Color(c.FG),
		Comment: lipgloss.Color(c.Comment), Cyan: lipgloss.Color(c.Cyan), Green: lipgloss.Color(c.Green),
		Orange: lipgloss.Color(c.Orange), Pink: lipgloss.Color(c.Pink), Purple: lipgloss.Color(c.Purple),
		Red: lipgloss.Color(c.Red), Yellow: lipgloss.Color(c.Yellow)}
	tok := func(name string, def lipgloss.Color) lipgloss.Color {
		switch name {
		case "":
			return def
		case "cyan":
			return t.Cyan
		case "green":
			return t.Green
		case "orange":
			return t.Orange
		case "pink":
			return t.Pink
		case "purple":
			return t.Purple
		case "red":
			return t.Red
		case "yellow":
			return t.Yellow
		case "comment":
			return t.Comment
		case "fg":
			return t.FG
		}
		return lipgloss.Color(name)
	}
	t.Working = tok(c.Working, t.Cyan)
	t.Blocked = tok(c.Blocked, t.Orange)
	t.Done = tok(c.Done, t.Green)
	t.Idle = tok(c.Idle, t.Comment)
	t.Brand = tok(c.Brand, lipgloss.Color("#3FD0D4"))
	return t
}

var spinnerFrames = []string{"◐", "◓", "◑", "◒"}

func (t Theme) StateColor(s agent.State) lipgloss.Color {
	switch s {
	case agent.Working:
		return t.Working
	case agent.Blocked:
		return t.Blocked
	case agent.Done:
		return t.Done
	case agent.Idle:
		return t.Idle
	}
	return t.Comment
}

func (t Theme) Glyph(s agent.State, frame int) string {
	switch s {
	case agent.Working:
		return spinnerFrames[frame%len(spinnerFrames)]
	case agent.Blocked:
		return "●"
	case agent.Done:
		return "✓"
	case agent.Idle:
		return "○"
	}
	return "◌"
}

// circled returns ①..⑳ for 1..20, "+" beyond.
func circled(n int) string {
	if n >= 1 && n <= 20 {
		return string(rune(0x2460 + n - 1))
	}
	return "+"
}
