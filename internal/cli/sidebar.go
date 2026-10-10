package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/awake"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/git"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/rules"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
	"github.com/w4jnl/flok/internal/ui"
)

func sidebarDeps(cfg config.Config) ui.Deps {
	ver := tmux.DetectVersion("")
	if rt, err := launcher.ReadRuntime(); err == nil && rt.TmuxVersion != "" {
		ver = tmux.ParseVersion(rt.TmuxVersion) // recorded by `flok up`: same binary, no fork
	}
	d := ui.Deps{Cfg: cfg, Inner: tmux.NewLocal(cfg.Inner.Socket).SetVersion(ver), Feat: tmux.FeaturesFor(ver),
		Adapters: agent.Enabled(cfg.Agents.Enabled), BranchOf: git.Branch, Store: state.New(config.StateDir())}
	if awake.Supported() {
		opts := awake.Options{Presence: cfg.KeepAwake.Presence}
		d.Awake = func() (ui.Releaser, error) { return awake.Hold(awake.Name, opts) }
	}
	for _, id := range cfg.Agents.Enabled {
		if id == "claude" {
			d.Registry = true
		}
	}
	if cfg.Agents.ScreenRules != "never" {
		d.Rules = rules.Load(cfg.Agents.ManifestDir, cfg.Agents.UseHerdrCache)
	}
	if o := tmux.FromEnv(); o != nil && os.Getenv("FLOK_OUTER") != "" {
		d.Outer = o.SetVersion(ver)
	}
	d.SidebarPane = os.Getenv("TMUX_PANE") // set by the outer server
	d.RightPane = os.Getenv("FLOK_RIGHT_PANE")
	if d.RightPane == "" {
		if rt, err := launcher.ReadRuntime(); err == nil {
			d.RightPane = rt.RightPane
			d.ClientTTY = rt.InnerClientTTY
		}
	}
	d.Bin = binPath()
	d.Version = Version
	rules, adapters := d.Rules, d.Adapters
	d.NewRemote = func(sink chan<- remote.Msg) *remote.Manager {
		var debugf func(string, ...any)
		if os.Getenv("FLOK_DEBUG") != "" {
			debugf = func(format string, args ...any) { appendLog("remote.log", format, args...) }
		}
		extra, _ := remote.KeyMap(cfg.Keys.Map)
		return remote.New(remote.Deps{Cfg: cfg, StateDir: config.StateDir(), Sink: sink, Rules: rules, Adapters: adapters, Debugf: debugf,
			LocalPrefix: localPrefix(cfg, d.Inner), ExtraKeys: extra})
	}
	return d
}

// appendLog appends a line to a log file in the state dir (FLOK_DEBUG).
func appendLog(name, format string, args ...any) {
	f, err := os.OpenFile(filepath.Join(config.StateDir(), name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format("15:04:05.000")+" "+format+"\n", args...)
}

func runSidebar(cfg config.Config) int {
	// FLOK_CPUPROFILE=<file>: write a 30 s CPU profile after start (go tool pprof -top <file>).
	if p := os.Getenv("FLOK_CPUPROFILE"); p != "" {
		if f, err := os.Create(p); err == nil && pprof.StartCPUProfile(f) == nil {
			time.AfterFunc(30*time.Second, func() { pprof.StopCPUProfile(); f.Close() })
		}
	}
	deps := sidebarDeps(cfg)
	// flok's keys in the inner server for this session (the snippet makes them permanent), and
	// the keys of [keys] map, which take whatever they held
	extra, badMap := remote.KeyMap(cfg.Keys.Map)
	installed, conflicts, stale := remote.InstallLocalKeys(deps.Inner, config.StateDir(), deps.Bin, cfg.Keys.Bind, extra)
	if len(badMap) > 0 {
		deps.Notices = append(deps.Notices, "[keys] map: not a flok command: "+strings.Join(badMap, ", "))
	}
	if os.Getenv("FLOK_DEBUG") != "" {
		appendLog("sidebar.log", "keys bound: %v; left alone: %v; older snippet: %v", installed, conflicts, stale)
	}
	if len(stale) > 0 { // the snippet in tmux.conf predates a key change: rebound for now, a reload brings the old one back
		var keys []string
		for k := range stale {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			keys[i] = k + " → " + stale[k]
		}
		deps.Notices = append(deps.Notices, "older tmux snippet: "+strings.Join(keys, ", ")+" (rebound for now) · flok install --tmux prints the current one")
	}
	m := ui.New(deps)
	_ = launcher.UpdateRuntime(func(r *launcher.Runtime) {
		r.SidebarPID = os.Getpid()
		if tty := m.ClientTTY(); tty != "" {
			r.InnerClientTTY = tty
		}
	})
	fps := cfg.Sidebar.FPS
	if fps < 1 || fps > 120 {
		fps = 15
	}
	// The renderer wakes fps times per second to compare the frame buffer; 60 (Bubble Tea's
	// default) is far more than a sidebar animating at 4 Hz needs and costs idle wakeups.
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithFPS(fps), tea.WithReportFocus())
	_, err := p.Run()
	m.ReleaseKeepAwake()
	m.Close() // remote hosts: their serve sessions end on EOF
	_ = remote.RestoreLocalKeys(deps.Inner, config.StateDir())
	if err != nil {
		fmt.Fprintln(os.Stderr, "flok sidebar:", err)
		return 1
	}
	return 0
}

// localPrefix is the inner server's prefix key for the hosts to take while connected, "" when
// [hosts] prefix is off or the server does not answer.
func localPrefix(cfg config.Config, inner tmux.Client) string {
	if !cfg.Hosts.Prefix || inner == nil {
		return ""
	}
	out, err := inner.Run("show-options", "-gv", "prefix")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
