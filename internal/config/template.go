package config

import (
	"errors"
	"os"
	"path/filepath"
)

// Template is the commented default configuration `flok install` writes when none exists.
// Every key is optional; the values shown are the built-in defaults.
const Template = `# flok configuration - https://github.com/w4jnl/flok
# Every key is optional; the values shown are the defaults. Uncomment to change.
# After editing: flok reload (restarts only the sidebar pane).

[inner]
# socket = "default"          # your tmux server (-L name)
# session = ""                # attach a specific session; empty = most recent
# reattach_on_detach = false  # true: prefix d re-attaches at once instead of closing flok

[outer]
# socket = "flok"
# session = "flok"
# extra_conf = "~/.config/flok/outer.extra.conf"   # sourced by the generated outer tmux config

[sidebar]
# width = 28                  # columns; pinned across window resizes
# rail_width = 6              # collapsed rail (prefix b)
# rail_threshold = 12         # narrower than this renders the rail
# sessions_max_ratio = 0.4    # at most this share of the height for the sessions list
# session_order = "index"     # index (tmux's chooser order) | name | activity
# agent_rows = 2              # 2: project + "kind · title"; 1: single line per agent
# show_branch = true
# branch_source = "active_pane"   # or "session_path"
# poll_ms = 1000
# registry_poll_ms = 5000
# screen_poll_ms = 2000
# capture_lines = 0           # extra scrollback lines for screen rules (0 = visible screen only)

[agents]
# enabled = ["claude", "copilot"]
# manifest_dir = "~/.config/flok/agents"   # per-agent TOML overrides of the bundled manifests
# use_herdr_cache = false     # also read herdr's manifest cache when herdr is installed
# screen_rules = "auto"       # auto | always | never

[keys]
# show_mouse = false
# tables = ["prefix", "root", "copy-mode-vi"]
# [keys.labels]               # command prefix -> label in the help popup
# "select-pane -L" = "west"

[sounds]
# enabled = true
# player = "hook"             # hook | sidebar | none
# volume = 0.6
# min_interval_ms = 750
# when_focused = false
# done = ""                   # empty = bundled sound; or e.g. "/System/Library/Sounds/Glass.aiff"
# blocked = ""
# error = ""

[bar]                         # macOS menu bar companion (flok-bar), started by flok up
# enabled = false
# animate = true              # spin ◐◓◑◒ in the menu bar while an agent works
# badge = true                # show "● N" for agents waiting for you
# focus = "auto"              # click-to-return: auto | aerospace | applescript | none | "shell command"
# app = ""                    # terminal to focus; empty = the one flok up ran in (TERM_PROGRAM)
# max_rows = 16

[theme]                       # Dracula; state tokens may name a colour or a hex value
# working = "cyan"
# blocked = "orange"
# done = "green"
# idle = "comment"
`

// WriteTemplate creates the config file with Template when it does not exist yet.
// It returns true when a file was written.
func WriteTemplate(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(Template), 0o644)
}
