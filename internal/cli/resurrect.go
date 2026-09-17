package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/w4jnl/flok/internal/agent"
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
	result, err := resurrect.Rewrite(args[1], snap, store.LoadAgents(), agent.Enabled(cfg.Agents.Enabled))
	if err != nil {
		return report(err)
	}
	if err := writeResurrectDiagnostics(result.Skipped); err != nil {
		fmt.Fprintln(os.Stderr, "flok: tmux-resurrect diagnostics:", err)
	}
	return 0
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
