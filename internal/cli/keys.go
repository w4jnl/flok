package cli

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/keys"
	"github.com/w4jnl/flok/internal/tmux"
	"github.com/w4jnl/flok/internal/ui"
)

// runKeys shows the keybinds help: interactive (inside a tmux popup) or `--print [--filter q]`.
func runKeys(cfg config.Config, args []string) int {
	print, filter := false, ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--print", "-p":
			print = true
		case "--filter", "-f":
			if i+1 < len(args) {
				filter = args[i+1]
				i++
			}
		}
	}
	var c tmux.Client = tmux.NewLocal(cfg.Inner.Socket)
	if env := tmux.FromEnv(); env != nil && os.Getenv("FLOK_OUTER") == "" {
		c = env // running inside the inner server (popup): read its bindings
	}
	bindings, prefix, err := keys.Collect(c, cfg.Keys.Tables)
	if err != nil {
		return report(err)
	}
	secs := keys.Organize(bindings, prefix, binPath(), cfg.Keys.Labels, cfg.Keys.ShowMouse)
	if print {
		fmt.Print(ui.Dump(secs, filter))
		return 0
	}
	h := ui.NewHelp(ui.NewTheme(cfg.Theme), secs, true)
	p := tea.NewProgram(h, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		return report(err)
	}
	return 0
}
