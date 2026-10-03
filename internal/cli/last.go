package cli

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

const lastUsage = `usage: flok last session|window|pane|agent|server

Back to the previous session or server, wherever it was (the running sidebar keeps one focus
history across every server, so the key that left a session on beta for one here brings you back
to beta), and to the previous window within this session or the previous pane within this window,
as tmux's own last-window and last-pane do; last agent returns to the previous agent pane on any
server. Bind your keys to these with [keys] map in config.toml (Tab = "last session", l = "last
window", ";" = "last pane", O = "last server", "'" = "last agent"); they work inside a remote
session too. Without a running sidebar the command does what tmux would on this server.`

// runLast hands `flok last <kind>` to the sidebar, which owns the focus history; without one it
// falls back to tmux's own last-* on the inner server.
func runLast(cfg config.Config, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, lastUsage)
		return 2
	}
	kind := args[0]
	switch kind {
	case "session", "window", "pane", "agent", "server":
	default:
		fmt.Fprintf(os.Stderr, "flok last: unknown kind %q\n\n%s\n", kind, lastUsage)
		return 2
	}
	if _, f := snapshot.Load(config.StateDir(), time.Now()); f == snapshot.Fresh {
		return report(state.New(config.StateDir()).WriteRequest(state.Request{Cmd: "last", Kind: kind}))
	}
	if kind == "server" || kind == "agent" {
		return report(errors.New("the sidebar is not running (flok up)"))
	}
	d := sidebarDeps(cfg)
	snap, err := tmux.TakeSnapshot(d.Inner)
	if err != nil {
		return report(err)
	}
	tty := ""
	if rt, err := launcher.ReadRuntime(); err == nil {
		tty = rt.InnerClientTTY
	}
	if tty == "" {
		tty, _ = resolveFocusTTY(snap)
	}
	if tty == "" {
		return report(errors.New("no tmux client to drive"))
	}
	sess := ""
	for _, c := range snap.Clients {
		if c.TTY == tty {
			sess = c.SessionID
		}
	}
	_, err = d.Inner.Run(lastFallback(kind, tty, sess)...)
	return report(err)
}

// lastFallback is tmux's own version of `last <kind>` for the client on tty, whose session is
// sess: what the key does when no sidebar keeps the history.
func lastFallback(kind, tty, sess string) []string {
	switch kind {
	case "window":
		return []string{"last-window", "-t", sess}
	case "pane":
		return []string{"last-pane", "-t", sess + ":"}
	}
	return []string{"switch-client", "-l", "-c", tty}
}
