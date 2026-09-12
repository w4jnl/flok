package cli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/focus"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/tmux"
)

// runEditConfig implements `edit-config [--no-focus]`, the menu bar's "Edit config…": make sure
// the file exists, then open it. With [bar] editor set and flok running, the editor runs in a new
// window of the inner tmux server (in the session you are looking at) and the terminal comes to
// the front; otherwise the file opens with the desktop's default application, as mwrelay does.
func runEditConfig(cfg config.Config, args []string) int {
	doFocus := true
	for _, a := range args {
		if a == "--no-focus" {
			doFocus = false
		}
	}
	path := config.ConfigFile()
	if _, err := config.WriteTemplate(path); err != nil {
		return report(err)
	}
	if cfg.Bar.Editor != "" {
		err := editInTmux(cfg, path, doFocus)
		if err == nil {
			return 0
		}
		fmt.Fprintln(os.Stderr, "flok edit-config:", err, "(falling back to the default app)")
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	return report(exec.Command(opener, path).Start())
}

// shellQuote single-quotes a string for tmux's shell command.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func editInTmux(cfg config.Config, path string, doFocus bool) error {
	rt, err := launcher.ReadRuntime()
	if err != nil {
		return fmt.Errorf("flok is not running")
	}
	inner := tmux.NewLocal(cfg.Inner.Socket)
	snap, err := tmux.TakeSnapshot(inner)
	if err != nil {
		return err
	}
	target := ""
	for _, c := range snap.Clients {
		if c.TTY == rt.InnerClientTTY {
			target = c.SessionID
		}
	}
	if target == "" {
		if tty, ok := resolveFocusTTY(snap); ok {
			for _, c := range snap.Clients {
				if c.TTY == tty {
					target = c.SessionID
				}
			}
		}
	}
	args := []string{"new-window", "-n", "flok-config"}
	if target != "" {
		args = append(args, "-t", target+":")
	}
	args = append(args, cfg.Bar.Editor+" "+shellQuote(path))
	if _, err := inner.Run(args...); err != nil {
		return err
	}
	if !doFocus {
		return nil
	}
	app := cfg.Bar.App
	if app == "" {
		app = rt.TerminalApp
	}
	return focus.Terminal(focus.Options{App: app, Strategy: cfg.Bar.Focus})
}
