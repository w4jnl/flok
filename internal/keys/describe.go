package keys

import (
	"path"
	"regexp"
	"strings"
)

var (
	wsRe       = regexp.MustCompile(`\s+`)
	pluginRe   = regexp.MustCompile(`/plugins/([^/]+)/(?:scripts/|bindings/)?([^/\s"']+?)(?:\.sh|\.tmux)?(?:\s|"|'|$)`)
	flokRe     = regexp.MustCompile(`flok\S*\s+(\w+)`)
	resizeRe   = regexp.MustCompile(`^resize-pane -([LRUD]) ?(\d+)?`)
	selWinRe   = regexp.MustCompile(`^select-window -t :?=?(\d+)`)
	layoutRe   = regexp.MustCompile(`^select-layout (\S+)`)
	sendXRe    = regexp.MustCompile(`^send-keys -X ([a-z-]+)`)
	vimAwareRe = regexp.MustCompile(`select-pane -([LRUDl])`)
	sourceRe   = regexp.MustCompile(`^source-file`)
	paneDirs   = map[string]string{"L": "left", "R": "right", "U": "up", "D": "down"}
)

var flokLabels = map[string]string{
	"next": "next agent", "prev": "previous agent", "jump": "jump to agent needing input",
	"toggle": "toggle sidebar rail", "hide": "hide/show sidebar", "keys": "keybinds", "focus": "focus sidebar",
}

// Label returns a short human description for a binding: the tmux note when present, an
// override, a translation of common commands, or the raw command.
func Label(b Binding, overrides map[string]string) string {
	cmd := strings.TrimSpace(wsRe.ReplaceAllString(b.Command, " "))
	for pfx, lab := range overrides {
		if strings.HasPrefix(cmd, pfx) {
			return lab
		}
	}
	if m := flokRe.FindStringSubmatch(cmd); m != nil {
		if l := flokLabels[m[1]]; l != "" {
			return l
		}
	}
	if b.Note != "" {
		return b.Note
	}
	if m := pluginRe.FindStringSubmatch(cmd); m != nil {
		return m[1] + ": " + strings.ReplaceAll(m[2], "_", " ")
	}
	if strings.Contains(cmd, "is_vim") || strings.Contains(cmd, "vim") && strings.HasPrefix(cmd, "if-shell") {
		if m := vimAwareRe.FindStringSubmatch(cmd); m != nil {
			if m[1] == "l" {
				return "last pane (vim-aware)"
			}
			return "pane " + paneDirs[m[1]] + " (vim-aware)"
		}
	}
	switch {
	case cmd == "send-prefix":
		return "send prefix"
	case strings.HasPrefix(cmd, "select-pane -L"), strings.HasPrefix(cmd, "select-pane -R"),
		strings.HasPrefix(cmd, "select-pane -U"), strings.HasPrefix(cmd, "select-pane -D"):
		return "pane " + paneDirs[cmd[13:14]]
	case strings.HasPrefix(cmd, "select-pane -l"), strings.HasPrefix(cmd, "last-pane"):
		return "last pane"
	case strings.HasPrefix(cmd, "swap-pane -U"):
		return "swap pane up"
	case strings.HasPrefix(cmd, "swap-pane -D"):
		return "swap pane down"
	case strings.HasPrefix(cmd, "list-buffers"):
		return "list buffers"
	case strings.HasPrefix(cmd, "show-messages"):
		return "show messages"
	case strings.HasPrefix(cmd, "select-pane -t :.+"):
		return "next pane"
	case strings.HasPrefix(cmd, "select-pane -t :.-"):
		return "previous pane"
	case strings.HasPrefix(cmd, "split-window -hb"), strings.HasPrefix(cmd, "split-window -bh"):
		return "split left"
	case strings.HasPrefix(cmd, "split-window -vb"), strings.HasPrefix(cmd, "split-window -bv"):
		return "split above"
	case strings.HasPrefix(cmd, "split-window -h"), strings.HasPrefix(cmd, "splitw -h"):
		return "split right"
	case strings.HasPrefix(cmd, "split-window"), strings.HasPrefix(cmd, "splitw"):
		return "split below"
	case strings.HasPrefix(cmd, "switch-client -l"):
		return "last session"
	case strings.HasPrefix(cmd, "switch-client -n"):
		return "next session"
	case strings.HasPrefix(cmd, "switch-client -p"):
		return "previous session"
	case strings.HasPrefix(cmd, "last-window"):
		return "last window"
	case strings.HasPrefix(cmd, "next-window"):
		return "next window"
	case strings.HasPrefix(cmd, "previous-window"):
		return "previous window"
	case strings.HasPrefix(cmd, "new-window"):
		return "new window"
	case strings.HasPrefix(cmd, "kill-window"):
		return "kill window"
	case strings.HasPrefix(cmd, "kill-pane"):
		return "kill pane"
	case strings.HasPrefix(cmd, "rename-window"), strings.HasPrefix(cmd, "command-prompt -I \"#W\""):
		return "rename window"
	case strings.HasPrefix(cmd, "rename-session"):
		return "rename session"
	case strings.HasPrefix(cmd, "resize-pane -Z"):
		return "zoom pane"
	case strings.HasPrefix(cmd, "copy-mode"):
		return "copy mode"
	case strings.HasPrefix(cmd, "paste-buffer"):
		return "paste"
	case strings.HasPrefix(cmd, "choose-buffer"):
		return "choose buffer"
	case strings.HasPrefix(cmd, "choose-tree"):
		return "choose session / window"
	case strings.HasPrefix(cmd, "detach-client"):
		return "detach"
	case strings.HasPrefix(cmd, "next-layout"):
		return "next layout"
	case strings.HasPrefix(cmd, "list-keys"):
		return "list keys"
	case strings.HasPrefix(cmd, "command-prompt"):
		return "command prompt"
	case strings.HasPrefix(cmd, "display-panes"):
		return "show pane numbers"
	case strings.HasPrefix(cmd, "display-menu"):
		// tmux's stock < and > bindings; only tmux 3.5+ ships notes for them
		switch {
		case strings.Contains(cmd, "window_index"):
			return "window menu"
		case strings.Contains(cmd, "pane_index"):
			return "pane menu"
		case strings.Contains(cmd, "session_name"):
			return "session menu"
		}
		return "menu"
	case strings.HasPrefix(cmd, "rotate-window"):
		return "rotate panes"
	case strings.HasPrefix(cmd, "break-pane"):
		return "break pane to window"
	case strings.HasPrefix(cmd, "clock-mode"):
		return "clock"
	case sourceRe.MatchString(cmd), strings.Contains(cmd, "source-file"):
		return "reload config"
	case strings.Contains(cmd, "synchronize-panes"):
		return "toggle sync panes"
	}
	if m := resizeRe.FindStringSubmatch(cmd); m != nil {
		if m[2] != "" {
			return "resize " + paneDirs[m[1]] + " " + m[2]
		}
		return "resize " + paneDirs[m[1]]
	}
	if m := selWinRe.FindStringSubmatch(cmd); m != nil {
		return "window " + m[1]
	}
	if m := layoutRe.FindStringSubmatch(cmd); m != nil {
		return "layout: " + m[1]
	}
	if m := sendXRe.FindStringSubmatch(cmd); m != nil {
		l := strings.ReplaceAll(m[1], "-", " ")
		if strings.HasPrefix(m[1], "copy-pipe") || strings.HasPrefix(m[1], "copy-selection") {
			l = "copy selection"
		}
		return l
	}
	if strings.HasPrefix(cmd, "run-shell") {
		f := strings.Fields(strings.TrimPrefix(cmd, "run-shell"))
		for _, x := range f {
			if strings.HasPrefix(x, "-") {
				continue
			}
			return "run " + path.Base(strings.Trim(x, `"'`))
		}
	}
	return cmd
}

// SectionOf picks the group for a binding: "flok", "plugin:<name>", or by table.
func SectionOf(b Binding, prefix string) string {
	if strings.Contains(b.Command, "flok") {
		return "flok"
	}
	if m := pluginRe.FindStringSubmatch(b.Command); m != nil {
		return "plugin:" + m[1]
	}
	switch b.Table {
	case "prefix":
		return "prefix " + prefix
	case "root":
		return "no prefix"
	}
	return b.Table
}
