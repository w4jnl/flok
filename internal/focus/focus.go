// Package focus brings the terminal window that hosts flok to the front, from outside tmux.
package focus

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Runner executes a command and returns its stdout; injectable for tests.
type Runner func(name string, args ...string) (string, error)

// LookPath reports whether a binary exists; injectable for tests.
type LookPath func(name string) bool

var bundleIDs = map[string]string{
	"ghostty": "com.mitchellh.ghostty", "iTerm.app": "com.googlecode.iterm2", "WezTerm": "com.github.wez.wezterm",
	"kitty": "net.kovidgoyal.kitty", "Apple_Terminal": "com.apple.Terminal",
}

var appNames = map[string]string{
	"ghostty": "Ghostty", "iTerm.app": "iTerm", "WezTerm": "WezTerm", "kitty": "kitty", "Apple_Terminal": "Terminal",
}

// Options for Terminal.
type Options struct {
	App         string // TERM_PROGRAM value recorded by `flok up` (ghostty, iTerm.app, ...)
	Strategy    string // auto | aerospace | applescript | none | custom command ({app}, {pane} expand)
	TitlePrefix string // the outer's window title prefix, "TMUX"
	Pane        string // for custom commands
	Run         Runner
	Look        LookPath
	GOOS        string // runtime.GOOS unless a test sets it
}

func defaultRun(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

func defaultLook(name string) bool { _, err := exec.LookPath(name); return err == nil }

// Strategy resolves "auto" to what is available on this machine. Both built-in strategies
// are macOS tools, so auto means "none" elsewhere; a custom command still works anywhere.
func Strategy(o Options) string {
	look := o.Look
	if look == nil {
		look = defaultLook
	}
	goos := o.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	switch o.Strategy {
	case "", "auto":
		if goos != "darwin" {
			return "none"
		}
		if look("aerospace") {
			return "aerospace"
		}
		return "applescript"
	}
	return o.Strategy
}

// Terminal focuses the flok window with the chosen strategy.
func Terminal(o Options) error {
	if o.Run == nil {
		o.Run = defaultRun
	}
	if o.TitlePrefix == "" {
		o.TitlePrefix = "TMUX"
	}
	switch s := Strategy(o); s {
	case "none":
		return nil
	case "aerospace":
		if err := aerospace(o); err == nil {
			return nil
		}
		return appleScript(o)
	case "applescript":
		return appleScript(o)
	default:
		cmd := strings.NewReplacer("{app}", o.App, "{pane}", o.Pane).Replace(s)
		_, err := o.Run("/bin/sh", "-c", cmd)
		return err
	}
}

// aerospace picks the flok window (title prefix, then app bundle) and focuses it by id, which
// also switches to its workspace.
func aerospace(o Options) error {
	out, err := o.Run("aerospace", "list-windows", "--all", "--format", "%{window-id}|%{app-bundle-id}|%{window-title}")
	if err != nil {
		return err
	}
	bundle := bundleIDs[o.App]
	var byTitle, byApp string
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimSpace(line), "|", 3)
		if len(f) != 3 {
			continue
		}
		id, app, title := strings.TrimSpace(f[0]), strings.TrimSpace(f[1]), strings.TrimSpace(f[2])
		if bundle != "" && app != bundle {
			continue
		}
		if strings.HasPrefix(title, o.TitlePrefix) && byTitle == "" {
			byTitle = id
		}
		if byApp == "" {
			byApp = id
		}
	}
	id := byTitle
	if id == "" {
		id = byApp
	}
	if id == "" {
		return errors.New("aerospace: no matching window")
	}
	_, err = o.Run("aerospace", "focus", "--window-id", id)
	return err
}

// appleScript activates the terminal app and, best effort, raises the flok window. The raise
// needs Accessibility permission for System Events; without it the activation alone happens.
func appleScript(o Options) error {
	name := appNames[o.App]
	if name == "" {
		if o.App != "" {
			name = o.App
		} else {
			name = "Ghostty"
		}
	}
	if _, err := o.Run("osascript", "-e", fmt.Sprintf(`tell application %q to activate`, name)); err != nil {
		return err
	}
	_, _ = o.Run("osascript", "-e", fmt.Sprintf(`tell application "System Events" to tell process %q to perform action "AXRaise" of (first window whose name starts with %q)`, name, o.TitlePrefix))
	return nil
}
