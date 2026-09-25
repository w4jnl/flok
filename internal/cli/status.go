package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/w4jnl/flok/internal/awake"
	"github.com/w4jnl/flok/internal/claudereg"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/tmux"
)

func runStatus(cfg config.Config, args []string) int {
	d := sidebarDeps(cfg)
	snap, err := tmux.TakeSnapshot(d.Inner)
	if err != nil {
		return report(err)
	}
	tty := d.ClientTTY
	if tty == "" && d.Outer != nil && d.RightPane != "" {
		tty, _ = tmux.Display(d.Outer, d.RightPane, "#{pane_tty}")
	}
	in := merge.Inputs{Tmux: snap, ClientTTY: tty, Adapters: d.Adapters, BranchOf: d.BranchOf, SessionOrder: cfg.Sidebar.SessionOrder,
		BranchFromSessionPath: cfg.Sidebar.BranchSource == "session_path"}
	if d.Store != nil {
		in.Hook, in.Seen, in.TerminalUnfocused = d.Store.LoadAgents(), d.Store.LoadSeen(), !d.Store.TerminalFocused()
	}
	if d.Registry {
		if entries, err := claudereg.List(3 * time.Second); err == nil {
			in.Registry = claudereg.ByTTY(entries)
		}
	}
	s := merge.NewTracker().Build(in)
	keep := readKeepAwake(config.StateDir())
	if len(args) > 0 && args[0] == "--json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return report(enc.Encode(statusJSON{Snapshot: s, KeepAwake: keep.on, KeepAwakePresence: string(keep.presence)}))
	}
	printStatus(os.Stdout, s, keep)
	return 0
}

// statusJSON is `flok status --json`: the merge snapshot, plus KeepAwake while keep-awake is on
// and KeepAwakePresence ("active"/"blocked") while it also keeps the user active.
type statusJSON struct {
	merge.Snapshot
	KeepAwake         bool   `json:",omitempty"`
	KeepAwakePresence string `json:",omitempty"`
}

func printStatus(w io.Writer, s merge.Snapshot, keep keepAwakeState) {
	fmt.Fprintln(w, "sessions")
	for _, sp := range s.Spaces {
		cur := " "
		if sp.Current {
			cur = "*"
		}
		fmt.Fprintf(w, "  %s %-32s %-20s %-8s agents=%d\n", cur, sp.SessionName, sp.Branch, sp.Rollup, sp.AgentCount)
	}
	fmt.Fprintf(w, "agents (%d unseen)\n", s.Unseen)
	for _, a := range s.Agents {
		fmt.Fprintf(w, "  %-8s %-8s %-20s %-28s %s:%d.%d %s since %s src=%s tool=%s unseen=%d\n", a.State, a.Kind, a.Name, a.Title, a.SessionName,
			a.WindowIndex, a.PaneIndex, a.PaneID, a.StateSince.Format(time.Kitchen), a.Source, a.CurrentTool, a.Unseen)
	}
	if !s.Focus.Found {
		fmt.Fprintln(w, "focus: no inner client")
	} else {
		fmt.Fprintf(w, "focus: client %s session %s window %s pane %s\n", s.Focus.ClientTTY, s.Focus.SessionName, s.Focus.WindowID, s.Focus.PaneID)
	}
	switch {
	case keep.on && keep.presence == awake.PresenceActive:
		fmt.Fprintln(w, "keep-awake: on (display and idle sleep blocked, you stay active)")
	case keep.on && keep.presence == awake.PresenceBlocked:
		fmt.Fprintln(w, "keep-awake: on (display and idle sleep blocked; presence blocked, see flok doctor)")
	case keep.on:
		fmt.Fprintln(w, "keep-awake: on (display and idle sleep blocked)")
	}
	for _, wa := range s.Warnings {
		fmt.Fprintln(w, "warning:", wa)
	}
}
