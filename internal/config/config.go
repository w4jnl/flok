// Package config holds flok's configuration: defaults, the TOML file under
// ~/.config/flok, and XDG paths.
package config

import (
	"errors"
	"os"

	"github.com/BurntSushi/toml"
)

type Inner struct {
	Socket           string `toml:"socket"`
	Session          string `toml:"session"`
	ReattachOnDetach bool   `toml:"reattach_on_detach"`
}

type Outer struct {
	Socket    string `toml:"socket"`
	Session   string `toml:"session"`
	ExtraConf string `toml:"extra_conf"`
}

type Sidebar struct {
	Width            int     `toml:"width"`
	RailWidth        int     `toml:"rail_width"`
	RailThreshold    int     `toml:"rail_threshold"`
	SessionsMaxRatio float64 `toml:"sessions_max_ratio"`
	SessionOrder     string  `toml:"session_order"` // index (tmux's chooser order) | name | activity
	AgentRows        int     `toml:"agent_rows"`    // lines per agent row: 2 (name + "kind · title") or 1
	ShowBranch       bool    `toml:"show_branch"`
	BranchSource     string  `toml:"branch_source"`
	PollMs           int     `toml:"poll_ms"`
	IdlePollMs       int     `toml:"idle_poll_ms"` // poll interval while the sidebar is hidden (prefix B)
	SpinnerMs        int     `toml:"spinner_ms"`
	FPS              int     `toml:"fps"` // Bubble Tea renderer frame rate cap (1..120)
	RegistryPollMs   int     `toml:"registry_poll_ms"`
	ScreenPollMs     int     `toml:"screen_poll_ms"`
	CaptureLines     int     `toml:"capture_lines"`
	StaleWorkingMin  int     `toml:"stale_working_min"`
}

type Agents struct {
	Enabled       []string `toml:"enabled"`
	ManifestDir   string   `toml:"manifest_dir"`
	UseHerdrCache bool     `toml:"use_herdr_cache"`
	ScreenRules   string   `toml:"screen_rules"`
}

type Keys struct {
	ShowMouse bool              `toml:"show_mouse"`
	Tables    []string          `toml:"tables"`
	Labels    map[string]string `toml:"labels"`
}

type Sounds struct {
	Enabled       bool    `toml:"enabled"`
	Player        string  `toml:"player"`
	Command       string  `toml:"command"` // custom player command; "" = first known player on PATH (see notify.players)
	Bell          string  `toml:"bell"`    // auto (ring the terminal bell when no player exists) | always | never
	Volume        float64 `toml:"volume"`
	MinIntervalMs int     `toml:"min_interval_ms"`
	WhenFocused   bool    `toml:"when_focused"`
	Done          string  `toml:"done"`
	Blocked       string  `toml:"blocked"`
	Error         string  `toml:"error"`
}

// Bar configures the optional macOS menu bar companion (flok-bar).
type Bar struct {
	Enabled   bool   `toml:"enabled"`
	Animate   bool   `toml:"animate"`
	Badge     bool   `toml:"badge"`
	Focus     string `toml:"focus"` // auto | aerospace | applescript | none | custom command
	App       string `toml:"app"`   // overrides the terminal recorded by `flok up` (TERM_PROGRAM value)
	MaxRows   int    `toml:"max_rows"`
	Editor    string `toml:"editor"` // "nvim": Edit config opens it in a new tmux window; "" = macOS `open`
	AnimateMs int    `toml:"animate_ms"`
	Color     bool   `toml:"color"` // colour the spinner and badge in the menu bar (state palette) // spinner frame interval; every frame redraws the status item
}

type Theme struct {
	BG          string `toml:"bg"`
	CurrentLine string `toml:"current_line"`
	FG          string `toml:"fg"`
	Comment     string `toml:"comment"`
	Cyan        string `toml:"cyan"`
	Green       string `toml:"green"`
	Orange      string `toml:"orange"`
	Pink        string `toml:"pink"`
	Purple      string `toml:"purple"`
	Red         string `toml:"red"`
	Yellow      string `toml:"yellow"`
	Working     string `toml:"working"`
	Blocked     string `toml:"blocked"`
	Done        string `toml:"done"`
	Idle        string `toml:"idle"`
}

type Config struct {
	Inner   Inner   `toml:"inner"`
	Outer   Outer   `toml:"outer"`
	Sidebar Sidebar `toml:"sidebar"`
	Agents  Agents  `toml:"agents"`
	Keys    Keys    `toml:"keys"`
	Sounds  Sounds  `toml:"sounds"`
	Bar     Bar     `toml:"bar"`
	Theme   Theme   `toml:"theme"`
}

// Default is the configuration used when no file exists; every key in the file overrides one field.
func Default() Config {
	return Config{
		Inner:   Inner{Socket: "default", ReattachOnDetach: false},
		Outer:   Outer{Socket: "flok", Session: "flok", ExtraConf: "~/.config/flok/outer.extra.conf"},
		Sidebar: Sidebar{Width: 28, RailWidth: 6, RailThreshold: 12, SessionsMaxRatio: 0.4, AgentRows: 2, SessionOrder: "index", ShowBranch: true, BranchSource: "active_pane", PollMs: 1000, IdlePollMs: 3000, SpinnerMs: 250, FPS: 15, RegistryPollMs: 10000, ScreenPollMs: 2000, CaptureLines: 0, StaleWorkingMin: 30},
		Agents:  Agents{Enabled: []string{"claude", "copilot"}, ManifestDir: "~/.config/flok/agents", ScreenRules: "auto"},
		Keys:    Keys{Tables: []string{"prefix", "root", "copy-mode-vi"}},
		Sounds: Sounds{Enabled: true, Player: "hook", Bell: "auto", Volume: 0.6, MinIntervalMs: 750,
			Done: "", Blocked: "", Error: ""}, // empty = the bundled herdr sounds (done.wav / request.wav)
		Bar: Bar{Enabled: false, Animate: true, Badge: true, Focus: "auto", MaxRows: 16, AnimateMs: 500, Color: true},
		Theme: Theme{BG: "#282a36", CurrentLine: "#44475a", FG: "#f8f8f2", Comment: "#6272a4", Cyan: "#8be9fd", Green: "#50fa7b",
			Orange: "#ffb86c", Pink: "#ff79c6", Purple: "#bd93f9", Red: "#ff5555", Yellow: "#f1fa8c",
			Working: "cyan", Blocked: "orange", Done: "green", Idle: "comment"},
	}
}

// Load reads path on top of Default(). A missing file is not an error.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		path = ConfigFile()
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, err
	}
	cfg.Outer.ExtraConf = ExpandHome(cfg.Outer.ExtraConf)
	cfg.Agents.ManifestDir = ExpandHome(cfg.Agents.ManifestDir)
	return cfg, nil
}
