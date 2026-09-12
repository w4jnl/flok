package cli

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/focus"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/nav"
	"github.com/w4jnl/flok/internal/tmux"
)

// runGoto implements `goto [pane-id] [--no-focus]`: switch the inner client to the pane (when
// given), mark it seen, and bring the terminal window hosting flok to the front. Used by the menu
// bar app, usable from any script.
func runGoto(cfg config.Config, args []string) int {
	pane, doFocus := "", true
	for _, a := range args {
		switch {
		case a == "--no-focus":
			doFocus = false
		case len(a) > 0 && a[0] == '%':
			pane = a
		default:
			return report(fmt.Errorf("goto: unexpected argument %q (want a pane id like %%12)", a))
		}
	}
	rt, err := launcher.ReadRuntime()
	if err != nil {
		return report(errors.New("flok is not running (no runtime.json)"))
	}
	d := sidebarDeps(cfg)
	if pane != "" {
		snap, err := tmux.TakeSnapshot(d.Inner)
		if err != nil {
			return report(err)
		}
		var target *tmux.Pane
		for i := range snap.Panes {
			if snap.Panes[i].ID == pane {
				target = &snap.Panes[i]
				break
			}
		}
		if target == nil {
			return report(fmt.Errorf("pane %s not found", pane))
		}
		tty := rt.InnerClientTTY
		if tty == "" {
			f, _ := resolveFocusTTY(snap)
			tty = f
		}
		if err := nav.Go(d.Inner, tty, target.SessionID, target.WindowID, target.ID); err != nil {
			return report(err)
		}
		if d.Store != nil {
			_ = d.Store.MarkSeen(pane, time.Now())
		}
	}
	if !doFocus {
		return 0
	}
	app := cfg.Bar.App
	if app == "" {
		app = rt.TerminalApp
	}
	if app == "" {
		app = os.Getenv("TERM_PROGRAM")
	}
	return report(focus.Terminal(focus.Options{App: app, Strategy: cfg.Bar.Focus, Pane: pane}))
}

// resolveFocusTTY picks the most recently active inner client when runtime.json has none.
func resolveFocusTTY(snap tmux.Snapshot) (string, bool) {
	best := ""
	var act int64 = -1
	for _, c := range snap.Clients {
		if c.Activity > act {
			best, act = c.TTY, c.Activity
		}
	}
	return best, best != ""
}
