package cli

import (
	"fmt"
	"os"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/rules"
	"github.com/w4jnl/flok/internal/tmux"
	"github.com/w4jnl/flok/internal/ui"
)

// runExplain shows which screen rules match a pane: `explain [pane-id ...]` (default: all agent panes).
func runExplain(cfg config.Config, args []string) int {
	d := sidebarDeps(cfg)
	set := rules.Load(cfg.Agents.ManifestDir, cfg.Agents.UseHerdrCache)
	for _, p := range set.Problems {
		fmt.Fprintln(os.Stderr, "manifest:", p)
	}
	snap, err := tmux.TakeSnapshot(d.Inner)
	if err != nil {
		return report(err)
	}
	want := map[string]bool{}
	for _, a := range args {
		want[a] = true
	}
	shown := 0
	for _, p := range snap.Panes {
		ad := agent.Match(p.Command, d.Adapters)
		if len(want) > 0 && !want[p.ID] {
			continue
		}
		if len(want) == 0 && ad == nil {
			continue
		}
		kind := "claude"
		if ad != nil {
			kind = ad.ID()
		}
		m := set.Get(kind)
		if m == nil {
			fmt.Printf("%s: no manifest for %s\n", p.ID, kind)
			continue
		}
		out, err := d.Inner.Run(ui.CaptureArgs(p.ID, cfg.Sidebar.CaptureLines)...)
		if err != nil {
			fmt.Printf("%s: %v\n", p.ID, err)
			continue
		}
		screen := rules.NewScreen(out, p.Title).WithProgress(p.PBState, p.PBProgress)
		res := m.Evaluate(screen)
		fmt.Printf("%s %s:%d.%d %s (%s) title=%q\n", p.ID, p.SessionName, p.WindowIndex, p.PaneIndex, kind, m.Version, p.Title)
		if !res.Matched {
			fmt.Println("  no rule matched -> idle fallback")
		}
		for _, r := range m.Explain(screen) {
			mark := " "
			if r.RuleID == res.RuleID {
				mark = "*"
			}
			hold := ""
			if r.Hold {
				hold = " (hold)"
			}
			fmt.Printf("  %s %-32s p=%-5d %s%s\n", mark, r.RuleID, r.Priority, r.State, hold)
		}
		shown++
	}
	if shown == 0 {
		fmt.Println("no agent panes found")
	}
	return 0
}
