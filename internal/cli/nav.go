package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/nav"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

func clientArg(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--client" || args[i] == "-c" {
			return args[i+1]
		}
	}
	return ""
}

// navSnapshot builds a one-off merge snapshot for the nav commands (no registry: too slow).
func navSnapshot(cfg config.Config, tty string) (merge.Snapshot, tmux.Client, error) {
	d := sidebarDeps(cfg)
	if tty == "" {
		tty = d.ClientTTY
	}
	if tty == "" {
		if rt, err := launcher.ReadRuntime(); err == nil {
			tty = rt.InnerClientTTY
		}
	}
	snap, err := tmux.TakeSnapshot(d.Inner)
	if err != nil {
		return merge.Snapshot{}, d.Inner, err
	}
	in := merge.Inputs{Tmux: snap, ClientTTY: tty, Adapters: d.Adapters, SessionOrder: cfg.Sidebar.SessionOrder}
	if d.Store != nil {
		in.Hook, in.Seen, in.TerminalUnfocused = d.Store.LoadAgents(), d.Store.LoadSeen(), !d.Store.TerminalFocused()
	}
	return merge.NewTracker().Build(in), d.Inner, nil
}

func message(c tmux.Client, tty, text string) {
	args := []string{"display-message"}
	if tty != "" {
		args = append(args, "-c", tty)
	}
	_, _ = c.Run(append(args, "flok: "+text)...)
}

// runNav implements jump / next / prev.
func runNav(cfg config.Config, cmd string, args []string) int {
	if rc, handled := navAcrossHosts(cfg, cmd, args); handled {
		return rc
	}
	s, inner, err := navSnapshot(cfg, clientArg(args))
	if err != nil {
		return report(err)
	}
	if !s.Focus.Found {
		return report(errors.New("no inner tmux client to drive"))
	}
	var target *agent.Agent
	switch cmd {
	case "jump":
		for i := range s.Agents {
			if s.Agents[i].State == agent.Blocked {
				target = &s.Agents[i]
				break
			}
		}
		if target == nil {
			for i := range s.Agents {
				if s.Agents[i].State == agent.Done {
					target = &s.Agents[i]
					break
				}
			}
		}
		if target == nil {
			message(inner, s.Focus.ClientTTY, "nothing pending")
			return 0
		}
	default:
		if len(s.Agents) == 0 {
			message(inner, s.Focus.ClientTTY, "no agents")
			return 0
		}
		cur := -1
		for i, a := range s.Agents {
			if a.PaneID == s.Focus.PaneID {
				cur = i
				break
			}
		}
		n := len(s.Agents)
		idx := 0
		switch {
		case cur < 0 && cmd == "prev":
			idx = n - 1
		case cur < 0:
			idx = 0
		case cmd == "prev":
			idx = (cur - 1 + n) % n
		default:
			idx = (cur + 1) % n
		}
		target = &s.Agents[idx]
	}
	if err := nav.Go(inner, s.Focus.ClientTTY, target.SessionID, target.WindowID, target.PaneID); err != nil {
		return report(err)
	}
	if d := sidebarDeps(cfg); d.Store != nil {
		_ = d.Store.MarkSeen(target.PaneID, time.Now())
	}
	return 0
}

// runLayout implements toggle / hide / focus against the outer server described by runtime.json.
func runLayout(cfg config.Config, cmd string, args []string) int {
	rt, err := launcher.ReadRuntime()
	inner := tmux.NewLocal(cfg.Inner.Socket)
	if err != nil || rt.SidebarPane == "" {
		message(inner, clientArg(args), "sidebar not running (flok up)")
		return 0
	}
	inner.SetVersion(rt.Version())
	outer := tmux.NewLocal(rt.OuterSocket).SetVersion(rt.Version())
	if _, err := tmux.Display(outer, rt.SidebarPane, "#{pane_id}"); err != nil {
		message(inner, clientArg(args), "sidebar not running (flok up)")
		return 0
	}
	zoomed, _ := tmux.Display(outer, rt.RightPane, "#{window_zoomed_flag}")
	switch cmd {
	case "focus": // toggle keyboard focus between the sidebar and the work pane
		if active, _ := tmux.Display(outer, rt.SidebarPane, "#{pane_active}"); active == "1" {
			_, err = outer.Run("select-pane", "-t", rt.RightPane)
		} else {
			_, err = outer.Run("select-pane", "-t", rt.SidebarPane)
		}
	case "reload": // restart only the sidebar pane (re-reads config.toml); the work pane is untouched
		_, err = outer.Run("respawn-pane", "-k", "-t", rt.SidebarPane, launcher.SidebarCommand(binPath(), rt.RightPane))
	case "hide":
		if zoomed == "1" { // un-hide: resizes while hidden scaled the layout underneath, re-pin it
			_, err = outer.Run("resize-pane", "-Z", "-t", rt.RightPane, ";", "select-layout", "-t", rt.RightPane, "main-vertical")
			if err == nil {
				_ = launcher.SetSidebarHidden(false)
			}
			break
		}
		_, err = outer.Run("resize-pane", "-Z", "-t", rt.RightPane)
		if err == nil {
			_ = launcher.SetSidebarHidden(true) // the sidebar pauses its spinner and slows its polls
		}
	case "toggle":
		if zoomed == "1" {
			_, err = outer.Run("resize-pane", "-Z", "-t", rt.RightPane, ";", "select-layout", "-t", rt.RightPane, "main-vertical")
			if err == nil {
				_ = launcher.SetSidebarHidden(false)
			}
			break
		}
		width, _ := tmux.Display(outer, rt.SidebarPane, "#{pane_width}")
		full, rail := rt.FullWidth, rt.RailWidth
		if full == 0 {
			full = cfg.Sidebar.Width
		}
		if rail == 0 {
			rail = cfg.Sidebar.RailWidth
		}
		w := atoi(width)
		target := rail
		if w < cfg.Sidebar.RailThreshold {
			target = full
		}
		// main-pane-width is what the outer's resize hook re-applies; set it and relayout
		_, err = outer.Run("set-option", "-w", "-t", rt.SidebarPane, "main-pane-width", fmt.Sprint(target), ";",
			"select-layout", "-t", rt.SidebarPane, "main-vertical")
	}
	return report(err)
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// navAcrossHosts is jump / next / prev when remote hosts are configured: the sidebar's
// published view is the authority (it knows every host and which one is in front). jump picks
// the first agent needing attention across hosts; next and prev walk the agents of the front
// host, so a step never swaps the work pane. A target away from the local front goes through
// the sidebar's request mailbox. Not handled (false): no hosts, or no running sidebar.
func navAcrossHosts(cfg config.Config, cmd string, args []string) (int, bool) {
	dir := config.StateDir()
	s, f := snapshot.Load(dir, time.Now())
	if f != snapshot.Fresh || len(s.Hosts) == 0 {
		return 0, false
	}
	inner := tmux.NewLocal(cfg.Inner.Socket)
	var target *snapshot.Agent
	switch cmd {
	case "jump":
		for _, want := range []agent.State{agent.Blocked, agent.Done} {
			for i := range s.Agents {
				if s.Agents[i].State == want {
					target = &s.Agents[i]
					break
				}
			}
			if target != nil {
				break
			}
		}
		if target == nil {
			message(inner, clientArg(args), "nothing pending")
			return 0, true
		}
	default:
		var list []snapshot.Agent
		for _, a := range s.Agents {
			if a.Host == s.FrontHost {
				list = append(list, a)
			}
		}
		if len(list) == 0 {
			message(inner, clientArg(args), "no agents on "+hostName(s.FrontHost))
			return 0, true
		}
		cur := -1
		for i, a := range list {
			if a.PaneID == s.Focus.PaneID && a.Host == s.Focus.Host {
				cur = i
				break
			}
		}
		n, idx := len(list), 0
		switch {
		case cur < 0 && cmd == "prev":
			idx = n - 1
		case cur < 0:
			idx = 0
		case cmd == "prev":
			idx = (cur - 1 + n) % n
		default:
			idx = (cur + 1) % n
		}
		target = &list[idx]
	}
	if target.Host == "" && s.FrontHost == "" {
		return 0, false // local target, local front: the direct path below drives the client itself
	}
	if err := state.New(dir).WriteRequest(state.Request{Cmd: "goto", Host: target.Host, Pane: target.PaneID}); err != nil {
		return report(err), true
	}
	return 0, true
}

func hostName(host string) string {
	if host == "" {
		return agent.LocalHost
	}
	return host
}
