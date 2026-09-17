// Package install wires flok into Claude Code (settings.json hooks), Copilot CLI
// (~/.copilot/hooks) and prints the tmux.conf snippet. Every writer is idempotent.
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ClaudeEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
	"PermissionRequest", "Notification", "Stop", "StopFailure", "SessionEnd"}

var CopilotEvents = []string{"sessionStart", "sessionEnd", "userPromptSubmitted", "preToolUse", "postToolUse",
	"postToolUseFailure", "permissionRequest", "notification", "agentStop", "errorOccurred"}

const claudeMarker = " hook claude"

const legacyCopilotFile = "tmux-herdr.json"

func ClaudeCommand(bin string) string { return bin + " hook claude" }

// ClaudeSettings adds (or repairs) the flok hook entries in a Claude Code settings file.
// Other hooks and settings are preserved; a timestamped backup is written when the file changes.
func ClaudeSettings(path, bin string) (bool, error) {
	settings := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &settings); err != nil {
			return false, fmt.Errorf("parse %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return false, err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	want := ClaudeCommand(bin)
	changed := false
	for _, event := range ClaudeEvents {
		list, _ := hooks[event].([]any)
		found := false
		for _, item := range list {
			entry, _ := item.(map[string]any)
			inner, _ := entry["hooks"].([]any)
			for _, hi := range inner {
				h, _ := hi.(map[string]any)
				cmd, _ := h["command"].(string)
				if !strings.Contains(cmd, claudeMarker) {
					continue
				}
				found = true
				if cmd != want || h["async"] != true {
					h["command"], h["async"], h["timeout"] = want, true, float64(5)
					changed = true
				}
			}
		}
		if !found {
			list = append(list, map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": want, "timeout": float64(5), "async": true}}})
			hooks[event] = list
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	settings["hooks"] = hooks
	if data != nil {
		_ = os.WriteFile(path+".bak-"+time.Now().Format("20060102-150405"), data, 0o600)
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return true, os.WriteFile(path, append(out, '\n'), 0o600)
}

// ClaudeMissing lists events without a flok hook (for doctor).
func ClaudeMissing(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ClaudeEvents
	}
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return ClaudeEvents
	}
	var missing []string
	for _, event := range ClaudeEvents {
		found := false
		for _, entry := range settings.Hooks[event] {
			for _, h := range entry.Hooks {
				if strings.Contains(h.Command, claudeMarker) {
					found = true
				}
			}
		}
		if !found {
			missing = append(missing, event)
		}
	}
	return missing
}

// CopilotHooks writes ~/.copilot/hooks/flok.json. Copilot payloads carry no event name,
// so each entry passes it explicitly. The hook prints nothing and exits 0 (preToolUse and
// permissionRequest are fail-closed on non-zero exit).
func CopilotHooks(path, bin string) (bool, error) {
	hooks := map[string]any{}
	for _, event := range CopilotEvents {
		hooks[event] = []any{map[string]any{"type": "command", "bash": bin + " hook copilot --event " + event, "timeoutSec": 5}}
	}
	doc := map[string]any{"version": 1, "hooks": hooks}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, err
	}
	out = append(out, '\n')
	// The file was called tmux-herdr.json before the rename; Copilot would run both.
	if filepath.Base(path) != legacyCopilotFile {
		_ = os.Remove(filepath.Join(filepath.Dir(path), legacyCopilotFile))
	}
	if old, err := os.ReadFile(path); err == nil && string(old) == string(out) {
		return false, nil
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return true, os.WriteFile(path, out, 0o644)
}

// TmuxSnippet is what the user pastes below the tpm line of the inner tmux.conf.
func TmuxSnippet(bin string) string {
	return fmt.Sprintf(`# >>> flok >>>
unbind a                                                 # was send-prefix; C-a C-a still sends the prefix
bind a run-shell -b "%[1]s next --client '#{client_tty}'"
bind A run-shell -b "%[1]s prev --client '#{client_tty}'"
bind o run-shell -b "%[1]s jump --client '#{client_tty}'"   # replaces select-pane -t :.+
bind b run-shell -b "%[1]s toggle"
bind B run-shell -b "%[1]s hide"
bind g run-shell -b "%[1]s focus"                        # keyboard into the sidebar: j/k, enter, esc back
bind ? run-shell -b "%[1]s keys --open --client '#{client_tty}'"   # popup on tmux 3.2+, a window before
# <<< flok <<<
`, bin)
}

// TmuxResurrectSnippet opts into exact Claude/Copilot conversation restoration. Each process
// is appended only when absent so reloading tmux.conf does not grow the option indefinitely.
func TmuxResurrectSnippet(bin string) string {
	command := shellQuote(bin) + " resurrect save"
	return fmt.Sprintf(`# >>> flok tmux-resurrect >>>
if-shell -F '#{m:*claude*,#{@resurrect-processes}}' '' "set -ag @resurrect-processes ' claude'"
if-shell -F '#{m:*copilot*,#{@resurrect-processes}}' '' "set -ag @resurrect-processes ' copilot'"
set -g @resurrect-hook-post-save-layout %q
# <<< flok tmux-resurrect <<<
`, command)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
