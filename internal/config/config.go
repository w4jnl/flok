// Package config holds flok's configuration: defaults, the TOML file under
// ~/.config/flok, and XDG paths.
package config

import (
	"errors"
	"os"
	"strings"

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
	AgentOrder       string  `toml:"agent_order"`   // stable: session order, window, pane (rows never move) | priority: blocked, done, working, idle
	ShowBranch       bool    `toml:"show_branch"`
	Brand            bool    `toml:"brand"` // [flok] wordmark on top (the mark on the rail)
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
	Bind      string            `toml:"bind"` // missing: bind flok's keys that are unbound at start | all: override | off
	Map       map[string]string `toml:"map"`  // your keys for flok commands: Tab = "last session" (bound here and on hosts)
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

// Notify posts a short message to an HTTP endpoint (an ntfy topic, or anything taking the JSON
// form) when an agent needs the user or finishes: the phone side of flok.
type Notify struct {
	URL    string   `toml:"url"`    // "" = off
	Token  string   `toml:"token"`  // Authorization: Bearer <token>
	Format string   `toml:"format"` // ntfy (title/priority/tags headers, text body) | json
	Events []string `toml:"events"` // blocked, done, error
}

// Link is this flok instance on the phone: its name, and the relay it dials out to (flok-relay,
// the small service you run; the app talks to the relay).
type Link struct {
	Name  string `toml:"name"`  // "" = the machine's short hostname
	URL   string `toml:"url"`   // wss://flok.example.net/link (https://flok.example.net works too); "" = off
	Token string `toml:"token"` // one of the relay's instance_tokens
}

// KeepAwake tunes `flok keep-awake` (macOS).
type KeepAwake struct {
	Presence bool `toml:"presence"` // also keep you active in Teams, Slack & co. (needs Accessibility)
}

// Bar configures the optional macOS menu bar companion (flok-bar).
type Bar struct {
	Enabled   bool   `toml:"enabled"`
	Animate   bool   `toml:"animate"`
	Badge     bool   `toml:"badge"`
	Focus     string `toml:"focus"` // auto | aerospace | applescript | none | custom command
	App       string `toml:"app"`   // overrides the terminal recorded by `flok up` (TERM_PROGRAM value)
	MaxRows   int    `toml:"max_rows"`
	Editor    string `toml:"editor"`     // "nvim": Edit config opens it in a new tmux window; "" = macOS `open`
	AnimateMs int    `toml:"animate_ms"` // spinner frame interval; 0 follows [sidebar] spinner_ms; every frame redraws the status item
	Color     bool   `toml:"color"`      // colour the icon by state and the badge in the menu bar (state palette)
	IconSize  int    `toml:"icon_size"`  // menu bar icon height in points (systray's default is 16)
	Blink     bool   `toml:"blink"`      // alternate outline and solid icon every ~1.15 s while an agent waits, until the terminal is focused
}

// Hosts holds the defaults for reaching remote tmux servers over ssh. The hosts themselves live
// in $FLOK_STATE/hosts.json (`flok host add|remove|connect|disconnect|list`), not in the config.
type Hosts struct {
	SSH             string   `toml:"ssh"`               // ssh binary; aliases, jump hosts and keys come from ~/.ssh/config
	SSHOptions      []string `toml:"ssh_options"`       // extra arguments before the target
	ConnectTimeoutS int      `toml:"connect_timeout_s"` // ssh ConnectTimeout
	BackoffMaxS     int      `toml:"backoff_max_s"`     // reconnect backoff 1, 2, 4 … up to this
	Multiplex       bool     `toml:"multiplex"`         // one ControlMaster connection per host
	ServeCommand    string   `toml:"serve_command"`     // what mode full runs on the host
	RemotePath      string   `toml:"remote_path"`       // appended to PATH for every command run on a host
	Session         string   `toml:"session"`           // session the work pane creates on a host whose tmux is not running
	Keys            bool     `toml:"keys"`              // bind flok's keys (prefix b B g o a A u …) in a host's tmux while connected
	Prefix          bool     `toml:"prefix"`            // a connected host's tmux also takes the local prefix (its own moves to prefix2)
}

// Palette is one set of sidebar colours.
type Palette struct {
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
	Brand       string `toml:"brand"` // wordmark / rail mark accent
}

// Theme is the sidebar's colours: the dark palette under [theme] (Dracula by default), the light
// one under [theme.light] (Dracula's Alucard), and which of the two applies.
type Theme struct {
	Palette
	Mode  string  `toml:"mode"` // auto (follow the terminal background, detected at flok up) | dark | light
	Light Palette `toml:"light"`
}

// IsDark says whether the dark palette applies, given the theme detected at flok up ("dark",
// "light", or "" when unknown, which counts as dark).
func (t Theme) IsDark(detected string) bool {
	switch t.Mode {
	case "dark":
		return true
	case "light":
		return false
	}
	return detected != "light"
}

// Resolve returns the dark or the light palette.
func (t Theme) Resolve(dark bool) Palette {
	if dark {
		return t.Palette
	}
	return t.Light
}

type Config struct {
	Inner     Inner     `toml:"inner"`
	Outer     Outer     `toml:"outer"`
	Sidebar   Sidebar   `toml:"sidebar"`
	Agents    Agents    `toml:"agents"`
	Keys      Keys      `toml:"keys"`
	Sounds    Sounds    `toml:"sounds"`
	Bar       Bar       `toml:"bar"`
	KeepAwake KeepAwake `toml:"keep_awake"`
	Theme     Theme     `toml:"theme"`
	Hosts     Hosts     `toml:"hosts"`
	Notify    Notify    `toml:"notify"`
	Link      Link      `toml:"link"`
}

// InstanceName is what this flok calls itself in notifications: [link] name, else the short
// hostname.
func (c Config) InstanceName() string {
	if n := strings.TrimSpace(c.Link.Name); n != "" {
		return n
	}
	h, _ := os.Hostname()
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	if h == "" {
		return "flok"
	}
	return h
}

// Wants says whether [notify] events includes kind (blocked, done, error).
func (n Notify) Wants(kind string) bool {
	for _, e := range n.Events {
		if e == kind {
			return true
		}
	}
	return false
}

// Default is the configuration used when no file exists; every key in the file overrides one field.
func Default() Config {
	return Config{
		Notify:  Notify{Format: "ntfy", Events: []string{"blocked", "done", "error"}},
		Inner:   Inner{Socket: "default", ReattachOnDetach: false},
		Outer:   Outer{Socket: "flok", Session: "flok", ExtraConf: "~/.config/flok/outer.extra.conf"},
		Sidebar: Sidebar{Width: 28, RailWidth: 6, RailThreshold: 12, SessionsMaxRatio: 0.4, AgentRows: 2, SessionOrder: "index", AgentOrder: "stable", ShowBranch: true, Brand: true, BranchSource: "active_pane", PollMs: 1000, IdlePollMs: 3000, SpinnerMs: 250, FPS: 15, RegistryPollMs: 10000, ScreenPollMs: 2000, CaptureLines: 0, StaleWorkingMin: 30},
		Agents:  Agents{Enabled: []string{"claude", "copilot"}, ManifestDir: "~/.config/flok/agents", ScreenRules: "auto"},
		Keys:    Keys{Tables: []string{"prefix", "root", "copy-mode-vi"}, Bind: "missing"},
		Sounds: Sounds{Enabled: true, Player: "hook", Bell: "auto", Volume: 0.6, MinIntervalMs: 750,
			Done: "", Blocked: "", Error: ""}, // empty = the bundled herdr sounds (done.wav / request.wav)
		Bar: Bar{Enabled: false, Animate: true, Badge: true, Focus: "auto", MaxRows: 16, AnimateMs: 0, Color: true, IconSize: 18, Blink: true},
		Hosts: Hosts{SSH: "ssh", ConnectTimeoutS: 10, BackoffMaxS: 30, Multiplex: true, ServeCommand: "flok serve --stdio",
			RemotePath: "/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:/opt/local/bin", Session: "main", Keys: true, Prefix: true},
		Theme: Theme{Mode: "auto",
			Palette: Palette{BG: "#282a36", CurrentLine: "#44475a", FG: "#f8f8f2", Comment: "#6272a4", Cyan: "#8be9fd", Green: "#50fa7b",
				Orange: "#ffb86c", Pink: "#ff79c6", Purple: "#bd93f9", Red: "#ff5555", Yellow: "#f1fa8c",
				Working: "cyan", Blocked: "orange", Done: "green", Idle: "comment", Brand: "#3FD0D4"},
			Light: Palette{BG: "#fffbeb", CurrentLine: "#cfcfde", FG: "#1f1f1f", Comment: "#635d97", Cyan: "#036a96", Green: "#14710a",
				Orange: "#a34d14", Pink: "#a3144d", Purple: "#644ac9", Red: "#cb3a2a", Yellow: "#846e15",
				Working: "cyan", Blocked: "orange", Done: "green", Idle: "comment", Brand: "#12999D"}},
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
