package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/focus"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/nav"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

// runGoto implements `goto [pane] [--no-focus]`: switch to the pane (when given: "%12" here,
// "beta:%12" on a remote host), mark it seen, and bring the terminal window hosting flok to the
// front. Used by the menu bar app, usable from any script. A remote pane goes through the
// sidebar's request mailbox: it owns the ssh channels.
func runGoto(cfg config.Config, args []string) int {
	pane, host, doFocus := "", "", true
	for _, a := range args {
		switch {
		case a == "--no-focus":
			doFocus = false
		case strings.HasPrefix(a, "-"):
			return report(fmt.Errorf("goto: unexpected argument %q (want a pane id like %%12 or beta:%%12)", a))
		default:
			ref, err := agent.ParsePaneRef(a)
			if err != nil {
				return report(fmt.Errorf("goto: unexpected argument %q (want a pane id like %%12 or beta:%%12)", a))
			}
			pane, host = ref.ID, ref.Host
		}
	}
	if host != "" { // the sidebar matches the registered spelling: dockerams:%3 → dockerAMS
		if set, err := hosts.Load(config.StateDir()); err == nil {
			if h, ok := set.Get(host); ok {
				host = h.Name
			}
		}
	}
	rt, err := launcher.ReadRuntime()
	if err != nil {
		return report(errors.New("flok is not running (no runtime.json)"))
	}
	d := sidebarDeps(cfg)
	if pane != "" && host != "" {
		if _, f := snapshot.Load(config.StateDir(), time.Now()); f != snapshot.Fresh {
			return report(errors.New("the sidebar is not running: a pane on " + host + " needs it (flok up)"))
		}
		if err := d.Store.WriteRequest(state.Request{Cmd: "goto", Host: host, Pane: pane}); err != nil {
			return report(err)
		}
		pane = "" // the focus strategy knows local panes only
	}
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
	return report(focusTerminal(cfg, rt, pane))
}

// focusTerminal brings the terminal window that runs flok to the front ([bar] focus strategy).
func focusTerminal(cfg config.Config, rt launcher.Runtime, pane string) error {
	app := cfg.Bar.App
	if app == "" {
		app = rt.TerminalApp
	}
	if app == "" {
		app = os.Getenv("TERM_PROGRAM")
	}
	return focus.Terminal(focus.Options{App: app, Strategy: cfg.Bar.Focus, Pane: pane})
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
