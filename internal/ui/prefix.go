package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// teaKeyFromTmux converts a tmux prefix key name ("C-a", "M-x", "F12", "None") to the string
// Bubble Tea's KeyMsg.String() produces for it; "" when there is no usable prefix.
func teaKeyFromTmux(s string) string {
	s = strings.TrimSpace(s)
	switch {
	case s == "" || s == "None":
		return ""
	case s == "C-Space":
		return "ctrl+@"
	case strings.HasPrefix(s, "C-"):
		return "ctrl+" + strings.ToLower(s[2:])
	case strings.HasPrefix(s, "M-"):
		return "alt+" + strings.ToLower(s[2:])
	}
	return strings.ToLower(s)
}

var teaToTmux = map[string]string{
	"enter": "Enter", "tab": "Tab", "shift+tab": "BTab", "esc": "Escape", "up": "Up", "down": "Down",
	"left": "Left", "right": "Right", "backspace": "BSpace", "delete": "DC", "home": "Home", "end": "End",
	"pgup": "PPage", "pgdown": "NPage", " ": "Space", "insert": "IC", ";": `\;`,
}

// tmuxKeyName converts a Bubble Tea key to the name `tmux send-keys` understands.
func tmuxKeyName(msg tea.KeyMsg) string {
	k := msg.String()
	if v, ok := teaToTmux[k]; ok {
		return v
	}
	switch {
	case strings.HasPrefix(k, "ctrl+"):
		return "C-" + k[5:]
	case strings.HasPrefix(k, "alt+"):
		return "M-" + k[4:]
	case len(k) > 1 && k[0] == 'f' && k[1] >= '1' && k[1] <= '9':
		return "F" + k[1:]
	}
	return k
}
