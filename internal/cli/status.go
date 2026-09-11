package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

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
	if len(args) > 0 && args[0] == "--json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return report(enc.Encode(s))
	}
	fmt.Println("sessions")
	for _, sp := range s.Spaces {
		cur := " "
		if sp.Current {
			cur = "*"
		}
		fmt.Printf("  %s %-32s %-20s %-8s agents=%d\n", cur, sp.SessionName, sp.Branch, sp.Rollup, sp.AgentCount)
	}
	fmt.Printf("agents (%d unseen)\n", s.Unseen)
	for _, a := range s.Agents {
		fmt.Printf("  %-8s %-8s %-20s %-28s %s:%d.%d %s since %s src=%s tool=%s unseen=%d\n", a.State, a.Kind, a.Name, a.Title, a.SessionName,
			a.WindowIndex, a.PaneIndex, a.PaneID, a.StateSince.Format(time.Kitchen), a.Source, a.CurrentTool, a.Unseen)
	}
	if !s.Focus.Found {
		fmt.Println("focus: no inner client")
	} else {
		fmt.Printf("focus: client %s session %s window %s pane %s\n", s.Focus.ClientTTY, s.Focus.SessionName, s.Focus.WindowID, s.Focus.PaneID)
	}
	for _, w := range s.Warnings {
		fmt.Println("warning:", w)
	}
	return 0
}
