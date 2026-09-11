package cli

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/git"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/rules"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
	"github.com/w4jnl/flok/internal/ui"
)

func sidebarDeps(cfg config.Config) ui.Deps {
	d := ui.Deps{Cfg: cfg, Inner: tmux.NewLocal(cfg.Inner.Socket), Adapters: agent.Enabled(cfg.Agents.Enabled), BranchOf: git.Branch,
		Store: state.New(config.StateDir())}
	for _, id := range cfg.Agents.Enabled {
		if id == "claude" {
			d.Registry = true
		}
	}
	if cfg.Agents.ScreenRules != "never" {
		d.Rules = rules.Load(cfg.Agents.ManifestDir, cfg.Agents.UseHerdrCache)
	}
	if o := tmux.FromEnv(); o != nil && os.Getenv("FLOK_OUTER") != "" {
		d.Outer = o
	}
	d.SidebarPane = os.Getenv("TMUX_PANE") // set by the outer server
	d.RightPane = os.Getenv("FLOK_RIGHT_PANE")
	if d.RightPane == "" {
		if rt, err := launcher.ReadRuntime(); err == nil {
			d.RightPane = rt.RightPane
			d.ClientTTY = rt.InnerClientTTY
		}
	}
	return d
}

func runSidebar(cfg config.Config) int {
	m := ui.New(sidebarDeps(cfg))
	_ = launcher.UpdateRuntime(func(r *launcher.Runtime) {
		r.SidebarPID = os.Getpid()
		if tty := m.ClientTTY(); tty != "" {
			r.InnerClientTTY = tty
		}
	})
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "flok sidebar:", err)
		return 1
	}
	return 0
}
