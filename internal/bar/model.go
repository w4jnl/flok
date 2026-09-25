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
	PaneID    string
	Label     string // "project · kind"
	Detail    string // state detail as in the sidebar
	Attention bool   // blocked or done-unseen
	State     agent.State
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
	return line
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
		rows = append(rows, Row{PaneID: a.PaneID, Label: label, Detail: detail(a, now), Attention: attention(a), State: a.State})
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
