package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/claudereg"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/resurrect"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

func runResurrect(cfg config.Config, args []string) int {
	if len(args) != 2 || args[0] != "save" {
		fmt.Fprintln(os.Stderr, "usage: flok resurrect save <tmux-resurrect-state-file>")
		return 2
	}
	inner := tmux.NewLocal(cfg.Inner.Socket)
	snap, err := tmux.TakeSnapshot(inner)
	if err != nil {
		return report(err)
	}
	store := state.New(config.StateDir())
	adapters := agent.Enabled(cfg.Agents.Enabled)
	result, err := resurrect.Rewrite(args[1], resurrect.Inputs{
		Snapshot: snap, Hooks: store.LoadAgents(), Adapters: adapters, Registry: claudeSessions(snap, adapters)})
	if err != nil {
		return report(err)
	}
	if err := writeResurrectDiagnostics(result.Skipped); err != nil {
		fmt.Fprintln(os.Stderr, "flok: tmux-resurrect diagnostics:", err)
	}
	return 0
}

// claudeSessions maps panes to the session IDs Claude Code itself reports for its running
// processes, matched by tty. A save happens rarely, so the registry call (about 0.2 s) is
// affordable here, and it keeps a pane exact when its hooks never fired.
func claudeSessions(snap tmux.Snapshot, adapters []agent.Adapter) map[string]string {
	enabled := false
	for _, ad := range adapters {
		enabled = enabled || ad.ID() == "claude"
	}
	if !enabled {
		return nil
	}
	entries, err := claudereg.List(3 * time.Second)
	if err != nil {
		return nil
	}
	byTTY := claudereg.ByTTY(entries)
	out := map[string]string{}
	for _, p := range snap.Panes {
		if e, ok := byTTY[p.TTY]; ok && e.SessionID != "" {
			out[p.ID] = e.SessionID
		}
	}
	return out
}

func writeResurrectDiagnostics(skipped []resurrect.Skipped) error {
	path := filepath.Join(config.StateDir(), "resurrect.log")
	if len(skipped) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	now := time.Now().Format(time.RFC3339)
	var out strings.Builder
	for _, item := range skipped {
		fmt.Fprintf(&out, "%s %s %s: %s; restored pane will remain a shell\n", now, item.PaneID, item.Kind, item.Reason)
	}
	return os.WriteFile(path, []byte(out.String()), 0o644)
}
