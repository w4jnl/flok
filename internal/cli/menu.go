package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/bar"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/tmux"
)

const menuUsage = `usage: flok menu agents|sessions|servers

A tmux menu over the work pane (tmux 3.0+): the agents in the sidebar's order (1-9 open the
pane, remote ones tagged with their host), the sessions of the server in front (1-9 switch to
one), or the servers (1-9 bring one to the front). Each menu links to the other two. The tmux
snippet binds them to prefix A, S and R; on a tmux without menus the key moves the keyboard
into the sidebar instead.`

// menuItem is one display-menu triple; an empty Name is a separator line.
type menuItem struct{ Name, Key, Cmd string }

func runMenu(cfg config.Config, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, menuUsage)
		return 2
	}
	which := args[0]
	switch which {
	case "agents", "sessions", "servers":
	default:
		fmt.Fprintf(os.Stderr, "flok menu: unknown menu %q\n\n%s\n", which, menuUsage)
		return 2
	}
	rt, err := launcher.ReadRuntime()
	if err != nil || rt.SidebarPane == "" {
		return report(errors.New("the sidebar is not running (flok up)"))
	}
	snap, f := snapshot.Load(config.StateDir(), time.Now())
	if f != snapshot.Fresh {
		return report(errors.New("the sidebar is not running (flok up)"))
	}
	bin := binPath()
	var title string
	var items []menuItem
	switch which {
	case "agents":
		title, items = "agents", agentMenuItems(snap, time.Now(), bin)
	case "sessions":
		title, items = "sessions · "+hostLabel(snap.FrontHost), sessionMenuItems(snap, bin)
	case "servers":
		order, err := hostCmd{dir: config.StateDir(), cfg: cfg, now: time.Now}.servers()
		if err != nil {
			return report(err)
		}
		title, items = "servers", serverMenuItems(snap, order, bin)
	}
	return showMenu(rt, title, items)
}

// showMenu draws the menu over the work pane of the outer (whichever server is in front, the
// outer is what the terminal shows). display-menu holds its caller until the menu closes, so
// it is started and left alone. Older tmux (before 3.0) has no menus: the keyboard goes to the
// sidebar instead.
func showMenu(rt launcher.Runtime, title string, items []menuItem) int {
	outer := tmux.NewLocal(rt.OuterSocket).SetVersion(rt.Version())
	if !outer.Features().Menu {
		_, err := outer.Run("select-pane", "-t", rt.SidebarPane)
		return report(err)
	}
	if clients, err := outer.Run("list-clients", "-F", "#{client_tty}"); err != nil || strings.TrimSpace(clients) == "" {
		return report(errors.New("no terminal is attached to flok (the menu needs one)"))
	}
	args := []string{"display-menu", "-t", rt.RightPane, "-T", " " + title + " ", "-x", "C", "-y", "C"}
	for _, it := range items {
		if it.Name == "" {
			args = append(args, "") // a separator takes one word
			continue
		}
		args = append(args, it.Name, it.Key, it.Cmd)
	}
	cmd := exec.Command("tmux", outer.Argv(args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return report(err)
	}
	go func() { _ = cmd.Wait() }()
	return 0
}

// runFlok is the menu command that runs flok detached from the menu.
func runFlok(bin string, args ...string) string {
	return "run-shell -b " + tmux.ShellQuote(bin+" "+strings.Join(args, " "))
}

// hotkey numbers the first nine entries.
func hotkey(i int) string {
	if i < 9 {
		return strconv.Itoa(i + 1)
	}
	return ""
}

func hostLabel(host string) string {
	if host == "" {
		return agent.LocalHost
	}
	return host
}

var rollupGlyph = map[agent.State]string{agent.Working: "◐", agent.Blocked: "●", agent.Done: "✓", agent.Idle: "○"}

// agentMenuItems lists the agents as the sidebar and the menu bar do: attention first, remote
// ones tagged with their host; an entry opens the pane.
func agentMenuItems(s snapshot.Snapshot, now time.Time, bin string) []menuItem {
	var items []menuItem
	for i, r := range bar.Rows(s, now, 30) {
		mark := "  "
		if r.Attention {
			mark = "● "
		}
		name := mark + r.Label
		if r.Detail != "" {
			name += "    " + r.Detail
		}
		items = append(items, menuItem{Name: name, Key: hotkey(i), Cmd: runFlok(bin, "goto", r.PaneID, "--no-focus")})
	}
	if len(items) == 0 {
		items = append(items, menuItem{Name: "  no agents", Key: "", Cmd: ""})
	}
	return append(items, menuItem{},
		menuItem{Name: "sessions…", Key: "S", Cmd: runFlok(bin, "menu", "sessions")},
		menuItem{Name: "servers…", Key: "R", Cmd: runFlok(bin, "menu", "servers")})
}

// sessionMenuItems lists the sessions of the server in front, the current one marked; an entry
// switches to it (locally, or through the sidebar for a remote host).
func sessionMenuItems(s snapshot.Snapshot, bin string) []menuItem {
	var items []menuItem
	n := 0
	for _, se := range s.Sessions {
		if se.Host != s.FrontHost {
			continue
		}
		mark := "  "
		if se.Host == s.Focus.Host && se.Name == s.Focus.SessionName {
			mark = "▸ "
		}
		glyph := rollupGlyph[se.Rollup]
		if glyph == "" {
			glyph = " "
		}
		label := mark + glyph + " " + se.Name
		switch se.Agents {
		case 0:
		case 1:
			label += " · 1 agent"
		default:
			label += fmt.Sprintf(" · %d agents", se.Agents)
		}
		ref := se.ID
		if se.Host != "" {
			ref = se.Host + ":" + se.ID
		}
		items = append(items, menuItem{Name: label, Key: hotkey(n), Cmd: runFlok(bin, "goto", ref, "--no-focus")})
		n++
	}
	if n == 0 {
		items = append(items, menuItem{Name: "  no sessions", Key: "", Cmd: ""})
	}
	return append(items, menuItem{},
		menuItem{Name: "agents…", Key: "A", Cmd: runFlok(bin, "menu", "agents")},
		menuItem{Name: "servers…", Key: "R", Cmd: runFlok(bin, "menu", "servers")})
}

// serverMenuItems lists local and the enabled hosts in order with their state; an entry brings
// that server's work pane to the front.
func serverMenuItems(s snapshot.Snapshot, order []string, bin string) []menuItem {
	states := map[string]snapshot.Host{}
	for _, h := range s.Hosts {
		states[h.Name] = h
	}
	var items []menuItem
	for i, host := range order {
		label := agent.LocalHost
		if host != "" {
			label = host
			if h, ok := states[host]; ok {
				switch h.State {
				case "connected", "stale":
					label += fmt.Sprintf(" · %s · %d", h.Mode, h.Agents)
				default:
					label += " · " + h.State
				}
			}
		}
		mark := "  "
		if host == s.FrontHost {
			mark = "▸ "
		}
		items = append(items, menuItem{Name: mark + label, Key: hotkey(i), Cmd: runFlok(bin, "host", "front", strconv.Itoa(i+1))})
	}
	return append(items, menuItem{},
		menuItem{Name: "agents…", Key: "A", Cmd: runFlok(bin, "menu", "agents")},
		menuItem{Name: "sessions…", Key: "S", Cmd: runFlok(bin, "menu", "sessions")})
}
