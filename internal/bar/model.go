// Package bar is the presentation logic of flok-bar, kept free of cgo so it can be tested:
// what the menu bar title says and which rows the dropdown lists.
package bar

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/snapshot"
)

var Frames = []string{"◐", "◓", "◑", "◒"}

// Options mirror the [bar] config keys that affect rendering.
type Options struct {
	Animate bool
	Badge   bool
	MaxRows int
}

type Row struct {
	PaneID    string // "%12" here, "beta:%12" on a remote host: what `flok goto` takes
	Label     string // "project · kind", "project · kind @beta" on a remote host
	Detail    string // state detail as in the sidebar
	Attention bool   // blocked or done-unseen
	State     agent.State
}

// HostRow is one server in the dropdown's servers block: local first, then the remote hosts
// (the block is absent without any host).
type HostRow struct {
	Name      string // what `flok host front` takes: "local" or the host's name
	Label     string // "local · 2 agents", "beta · 3 agents · 1 waiting", "beta · needs auth", "beta · off"
	Front     bool   // its work pane is next to the sidebar
	Attention bool   // agents waiting there
}

// Working reports whether any agent is in a turn.
func Working(s snapshot.Snapshot) bool {
	for _, a := range s.Agents {
		if a.State == agent.Working {
			return true
		}
	}
	return false
}

// Pending counts agents that want the user: blocked, or done with unseen notifications.
func Pending(s snapshot.Snapshot) int {
	n := 0
	for _, a := range s.Agents {
		if attention(a) {
			n++
		}
	}
	return n
}

func attention(a snapshot.Agent) bool {
	return a.State == agent.Blocked || (a.State == agent.Done && a.Unseen > 0) || a.Unseen > 0
}

// KeepAwakeSign follows the state glyph while `flok keep-awake` keeps the Mac awake.
const KeepAwakeSign = "⚡"

// Title is the text next to the icon: idle "○", an animation frame while working, then ⚡ while
// keep-awake is on and a badge "● N" for pending agents. "–" when flok is not running.
func Title(s snapshot.Snapshot, f snapshot.Freshness, frame int, o Options) string {
	if f != snapshot.Fresh {
		return "–"
	}
	glyph := "○"
	if Working(s) {
		glyph = "◐"
		if o.Animate {
			glyph = Frames[((frame%len(Frames))+len(Frames))%len(Frames)]
		}
	}
	if s.KeepAwake {
		glyph += " " + KeepAwakeSign
	}
	if n := Pending(s); o.Badge && n > 0 {
		return fmt.Sprintf("%s ● %d", glyph, n)
	}
	return glyph
}

// Header is the disabled first line of the dropdown.
func Header(s snapshot.Snapshot, f snapshot.Freshness) string {
	switch f {
	case snapshot.Gone:
		return "flok not running"
	case snapshot.Stale:
		return "flok · sidebar not responding"
	}
	n := len(s.Agents)
	line := fmt.Sprintf("flok · %d agent", n)
	if n != 1 {
		line += "s"
	}
	if p := Pending(s); p > 0 {
		line += fmt.Sprintf(" · %d waiting", p)
	}
	if n := len(s.Hosts); n > 0 {
		line += fmt.Sprintf(" · %d host", n)
		if n != 1 {
			line += "s"
		}
		if down := hostsDown(s); down > 0 {
			line += fmt.Sprintf(" (%d down)", down)
		}
	}
	return line
}

// hostsDown counts enabled hosts that are not delivering agents right now.
func hostsDown(s snapshot.Snapshot) int {
	n := 0
	for _, h := range s.Hosts {
		switch h.State {
		case "connected", "stale", "disabled":
		default:
			n++
		}
	}
	return n
}

// HostRows lists the servers for the block under the agents: local, then the remote hosts in
// registry order. Nothing without a host: a single-server flok has no front to switch.
func HostRows(s snapshot.Snapshot) []HostRow {
	if len(s.Hosts) == 0 {
		return nil
	}
	n, waiting := 0, 0
	for _, a := range s.Agents {
		if a.Host == "" {
			n++
			if attention(a) {
				waiting++
			}
		}
	}
	label := fmt.Sprintf("local · %d agent", n)
	if n != 1 {
		label += "s"
	}
	if waiting > 0 {
		label += fmt.Sprintf(" · %d waiting", waiting)
	}
	rows := []HostRow{{Name: "local", Label: label, Front: s.FrontHost == "", Attention: waiting > 0}}
	for _, h := range s.Hosts {
		label := h.Name + " · "
		if h.Mode != "" {
			label += h.Mode + " · "
		}
		switch h.State {
		case "connected", "stale":
			label += fmt.Sprintf("%d agent", h.Agents)
			if h.Agents != 1 {
				label += "s"
			}
			if h.Pending > 0 {
				label += fmt.Sprintf(" · %d waiting", h.Pending)
			}
			if h.State == "stale" {
				label += " · stale"
			}
		case "disabled":
			label += "off"
		case "auth":
			label += "needs auth"
		case "hostkey":
			label += "host key"
		case "noflok":
			label += "no flok"
		case "oldflok":
			label += "old flok"
		case "noserver":
			label += "no tmux server"
		default:
			label += h.State
		}
		rows = append(rows, HostRow{Name: h.Name, Label: label, Front: h.Front, Attention: h.Pending > 0})
	}
	return rows
}

func priority(a snapshot.Agent) int {
	switch {
	case a.State == agent.Blocked:
		return 0
	case a.State == agent.Done, a.Unseen > 0:
		return 1
	case a.State == agent.Working:
		return 2
	case a.State == agent.Idle:
		return 3
	}
	return 4
}

// Rows lists agents in attention order, at most max (0 = all).
func Rows(s snapshot.Snapshot, now time.Time, max int) []Row {
	agents := append([]snapshot.Agent(nil), s.Agents...)
	sort.SliceStable(agents, func(i, j int) bool {
		pi, pj := priority(agents[i]), priority(agents[j])
		if pi != pj {
			return pi < pj
		}
		if pi <= 1 && !agents[i].StateSince.Equal(agents[j].StateSince) {
			return agents[i].StateSince.After(agents[j].StateSince)
		}
		return agents[i].Name < agents[j].Name
	})
	var rows []Row
	for _, a := range agents {
		if max > 0 && len(rows) >= max {
			break
		}
		label := a.Name
		if label == "" {
			label = a.SessionName
		}
		if a.Kind != "" {
			label += " · " + a.Kind
		}
		if a.Host != "" {
			label += " @" + a.Host
		}
		rows = append(rows, Row{PaneID: agent.PaneRef{Host: a.Host, ID: a.PaneID}.String(), Label: label, Detail: detail(a, now), Attention: attention(a), State: a.State})
	}
	return rows
}

func detail(a snapshot.Agent, now time.Time) string {
	switch a.State {
	case agent.Working:
		el := elapsed(a.StateSince, now)
		if a.Tool != "" {
			return strings.TrimSpace(a.Tool + " " + el)
		}
		return strings.TrimSpace("working " + el)
	case agent.Blocked:
		r := a.Reason
		if r == "" {
			r = "input"
		}
		return strings.Replace(r, "permission:", "perm:", 1)
	case agent.Done:
		if a.Unseen > 0 {
			return fmt.Sprintf("done · %d", a.Unseen)
		}
		return "done"
	case agent.Idle:
		if a.Unseen > 0 {
			return fmt.Sprintf("idle · %d", a.Unseen)
		}
		return "idle"
	}
	return "?"
}

func elapsed(since, now time.Time) string {
	if since.IsZero() {
		return ""
	}
	d := now.Sub(since).Round(time.Second)
	if d < 0 {
		d = 0
	}
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
