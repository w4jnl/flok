package cli

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/keys"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/tmux"
	"github.com/w4jnl/flok/internal/ui"
)

// runKeys shows the keybinds help: interactive (inside a tmux popup), `--print [--filter q]`,
// or `--open [--client tty]` from a tmux binding, which opens the interactive help in a popup
// where tmux has them (3.2+) and in a new window otherwise.
func runKeys(cfg config.Config, args []string) int {
	print, open, filter, client := false, false, "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--print", "-p":
			print = true
		case "--open":
			open = true
		case "--filter", "-f":
			if i+1 < len(args) {
				filter = args[i+1]
				i++
			}
		case "--client", "-c":
			if i+1 < len(args) {
				client = args[i+1]
				i++
			}
		}
	}
	var c tmux.Client = tmux.NewLocal(cfg.Inner.Socket)
	if env := tmux.FromEnv(); env != nil && os.Getenv("FLOK_OUTER") == "" {
		c = env // running inside the inner server (popup): read its bindings
	}
	if open {
		return openKeys(c, client)
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
	h := ui.NewHelp(ui.NewTheme(launcher.ActivePalette(cfg)), secs, true)
	p := tea.NewProgram(h, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		return report(err)
	}
	return 0
}

// openKeys is what the `prefix ?` binding runs: the interactive help in a popup, or, on a
// tmux without popups, in a new window of the pressing client's session.
func openKeys(c tmux.Client, client string) int {
	feat := tmux.Features{Popup: true, PopupBorder: true}
	if l, ok := c.(*tmux.Local); ok {
		feat = l.Features()
	}
	cmd := shellQuote(binPath()) + " keys"
	if args := keys.PopupArgs(feat, client, cmd); args != nil {
		_, err := c.Run(args...)
		return report(err)
	}
	args := []string{"new-window", "-n", "keybinds"}
	if client != "" {
		if sess, err := c.Run("display-message", "-p", "-c", client, "#{session_id}"); err == nil && strings.TrimSpace(sess) != "" {
			args = append(args, "-t", strings.TrimSpace(sess))
		}
	}
	_, err := c.Run(append(args, cmd)...)
	return report(err)
}
