package tmux

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Version is a parsed `tmux -V`. Suffix letters ("3.2a"), "next-" and vendor prefixes are
// ignored; a string without a major.minor number counts as newer than anything we gate on.
type Version struct {
	Major, Minor int
	Raw          string // as printed by tmux, without the "tmux " prefix
	Known        bool   // false when Raw could not be parsed
}

var versionNumRe = regexp.MustCompile(`(\d+)\.(\d+)`)

// Floor is the oldest tmux flok runs on: RHEL 8's.
var Floor = Version{Major: 2, Minor: 7, Raw: "2.7", Known: true}

// ParseVersion reads "tmux 3.2a", "3.7c", "next-3.6", "openbsd-7.4" (unparseable = newest).
func ParseVersion(s string) Version {
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "tmux "))
	m := versionNumRe.FindStringSubmatch(raw)
	if m == nil {
		return Version{Major: 99, Raw: raw}
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return Version{Major: major, Minor: minor, Raw: raw, Known: true}
}

// DetectVersion runs `tmux -V` (bin "" = tmux on PATH); a failure counts as newest.
func DetectVersion(bin string) Version {
	if bin == "" {
		bin = "tmux"
	}
	out, err := exec.Command(bin, "-V").Output()
	if err != nil {
		return Version{Major: 99, Raw: "unknown"}
	}
	return ParseVersion(string(out))
}

func (v Version) AtLeast(major, minor int) bool {
	return v.Major > major || (v.Major == major && v.Minor >= minor)
}

func (v Version) String() string {
	if v.Raw != "" {
		return v.Raw
	}
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// Features are the version-dependent tmux capabilities flok uses; everything else it needs
// exists since 2.7 (RHEL 8) or earlier. Keep the thresholds in one place: never call a
// gated option or flag without checking here first (an unknown option in the outer config is
// not fatal, but tmux dumps the errors into a view-mode overlay the user has to dismiss).
type Features struct {
	PaneOptions        bool // 3.0  set-option -p (remain-on-exit for the sidebar pane)
	WindowSizeLatest   bool // 3.1  window-size latest
	KeyNotes           bool // 3.1  list-keys -N
	Popup              bool // 3.2  display-popup
	ExtendedKeys       bool // 3.2  extended-keys (CSI-u passthrough)
	TerminalFeatures   bool // 3.2  terminal-features
	BorderLines        bool // 3.2  pane-border-lines
	Passthrough        bool // 3.3  allow-passthrough
	PopupBorder        bool // 3.3  display-popup -b/-T/-e
	BorderIndicators   bool // 3.3  pane-border-indicators
	FocusHooks         bool // 3.3  client-focus-in/out hooks
	ResizedHook        bool // 3.3  window-resized hook (client-resized before)
	EscapedOutput      bool // 3.4+ list-* output vis(3)-escapes non-printable bytes
	ExtendedKeysFormat bool // 3.5  extended-keys-format
}

// FeaturesFor maps a version to its capabilities.
func FeaturesFor(v Version) Features {
	return Features{
		PaneOptions:        v.AtLeast(3, 0),
		WindowSizeLatest:   v.AtLeast(3, 1),
		KeyNotes:           v.AtLeast(3, 1),
		Popup:              v.AtLeast(3, 2),
		ExtendedKeys:       v.AtLeast(3, 2),
		TerminalFeatures:   v.AtLeast(3, 2),
		BorderLines:        v.AtLeast(3, 2),
		Passthrough:        v.AtLeast(3, 3),
		PopupBorder:        v.AtLeast(3, 3),
		BorderIndicators:   v.AtLeast(3, 3),
		FocusHooks:         v.AtLeast(3, 3),
		ResizedHook:        v.AtLeast(3, 3),
		EscapedOutput:      v.AtLeast(3, 4),
		ExtendedKeysFormat: v.AtLeast(3, 5),
	}
}

// Degraded lists, for doctor and the docs, what this tmux cannot give flok.
func (f Features) Degraded() []string {
	var out []string
	if !f.PaneOptions {
		out = append(out, "the sidebar pane closes if the sidebar crashes (pane options need tmux 3.0); `flok up` recreates it")
	}
	switch {
	case !f.Popup:
		out = append(out, "keybinds help is drawn inline instead of a popup (display-popup needs tmux 3.2)")
	case !f.PopupBorder:
		out = append(out, "the keybinds popup has no border or title (tmux 3.3)")
	}
	if !f.ExtendedKeys {
		out = append(out, "no extended keys through the outer server (tmux 3.2): shift+enter-style bindings inside agents do not work")
	}
	if !f.Passthrough {
		out = append(out, "OSC passthrough from the inner server is off (allow-passthrough needs tmux 3.3)")
	}
	if !f.FocusHooks {
		out = append(out, "terminal focus is not tracked (client-focus hooks need tmux 3.3): the terminal is assumed focused")
	}
	if !f.TerminalFeatures {
		out = append(out, "RGB/clipboard terminal features are not advertised to the outer server (tmux 3.2)")
	}
	return out
}
