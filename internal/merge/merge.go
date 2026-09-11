// Package merge turns raw inputs (tmux snapshot, hook records, Claude registry, seen marks)
// into the one Snapshot the UI renders. Authority: hook > registry > pane title.
package merge

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/claudereg"
	"github.com/w4jnl/flok/internal/rules"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

// Focus is where the driven inner client currently looks.
type Focus struct {
	ClientTTY, SessionID, SessionName, WindowID, PaneID string
	Found                                               bool
}

type Snapshot struct {
	Spaces    []agent.Space
	Agents    []agent.Agent
	Focus     Focus
	Unseen    int
	Warnings  []string
	NewlySeen []string // panes the user is looking at whose seen mark should be persisted
	TakenAt   time.Time
}

type Inputs struct {
	Tmux                  tmux.Snapshot
	ClientTTY             string
	Adapters              []agent.Adapter
	BranchOf              func(path string) string
	BranchFromSessionPath bool
	Hook                  map[string]agent.Agent     // pane id -> hook-owned record
	Seen                  map[string]time.Time       // pane id -> last seen
	Registry              map[string]claudereg.Entry // pane tty -> registry entry
	Screen                map[string]rules.Result    // pane id -> screen-rule result (only evaluated panes)
	TerminalUnfocused     bool                       // the terminal window itself is not focused
	SessionOrder          string                     // index | name | activity (see sortSpaces)
	Now                   time.Time
}

type track struct {
	titleState  agent.State
	since       time.Time
	done        bool
	doneAt      time.Time
	unseen      int
	idleTitle   int         // consecutive polls with an idle title
	spinnerPoll int         // consecutive polls with a working title
	screenIdle  int         // consecutive polls where screen rules saw an idle prompt while hooks said blocked
	lastRaw     agent.State // last raw (pre-done) state, kept across skip_state_update holds
}

// Tracker keeps per-pane history between builds.
type Tracker struct {
	mu    sync.Mutex
	panes map[string]*track
}

func NewTracker() *Tracker { return &Tracker{panes: map[string]*track{}} }

// MarkSeen clears the title-derived done/unseen marks of a pane.
func (t *Tracker) MarkSeen(paneID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if tr := t.panes[paneID]; tr != nil {
		tr.done, tr.unseen = false, 0
	}
}

// ResolveFocus finds the client with the given tty, or the most recently active one.
func ResolveFocus(s tmux.Snapshot, tty string) (Focus, string) {
	var cl *tmux.ClientInfo
	if tty != "" {
		for i := range s.Clients {
			if s.Clients[i].TTY == tty {
				cl = &s.Clients[i]
				break
			}
		}
	}
	warn := ""
	if cl == nil {
		if len(s.Clients) == 0 {
			return Focus{ClientTTY: tty}, "no client attached to inner tmux"
		}
		best := 0
		for i := range s.Clients {
			if s.Clients[i].Activity > s.Clients[best].Activity {
				best = i
			}
		}
		cl = &s.Clients[best]
		if tty != "" {
			warn = "driving most recent client " + cl.TTY
		}
	}
	f := Focus{ClientTTY: cl.TTY, SessionID: cl.SessionID, SessionName: cl.SessionName, Found: true}
	if p, ok := s.ActivePane(cl.SessionID); ok {
		f.WindowID, f.PaneID = p.WindowID, p.ID
	}
	return f, warn
}

var shells = map[string]bool{"zsh": true, "bash": true, "fish": true, "sh": true, "login": true, "nu": true, "tcsh": true, "ksh": true}

// Build merges the inputs into a Snapshot, updating the tracker's history.
func (t *Tracker) Build(in Inputs) Snapshot {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	focus, warn := ResolveFocus(in.Tmux, in.ClientTTY)
	out := Snapshot{Focus: focus, TakenAt: now}
	if warn != "" {
		out.Warnings = append(out.Warnings, warn)
	}

	present := map[string]bool{}
	var agents []agent.Agent
	for _, p := range in.Tmux.Panes {
		if p.Dead {
			continue
		}
		ad := agent.Match(p.Command, in.Adapters)
		h, hasHook := in.Hook[p.ID]
		reg, hasReg := in.Registry[p.TTY]
		if ad == nil && !hasHook && !hasReg {
			continue
		}
		if ad == nil && shells[p.Command] { // the agent exited; record or registry entry is stale
			continue
		}
		kind := "claude"
		switch {
		case ad != nil:
			kind = ad.ID()
		case hasHook && h.Kind != "":
			kind = h.Kind
		}
		if ad == nil {
			ad = agent.Get(kind)
		}
		present[p.ID] = true
		titleState, tOK := agent.Unknown, false
		if ad != nil {
			titleState, tOK = ad.TitleState(p.Title)
		}
		tr := t.panes[p.ID]
		if tr == nil {
			tr = &track{titleState: titleState, since: now}
			t.panes[p.ID] = tr
		}
		switch {
		case tOK && titleState == agent.Idle:
			tr.idleTitle, tr.spinnerPoll = tr.idleTitle+1, 0
		case tOK && titleState == agent.Working:
			tr.idleTitle, tr.spinnerPoll = 0, tr.spinnerPoll+1
		default:
			tr.idleTitle, tr.spinnerPoll = 0, 0
		}
		focused := focus.PaneID == p.ID && !in.TerminalUnfocused
		seenAt := in.Seen[p.ID]
		scr, screened := in.Screen[p.ID]
		if screened && scr.Matched && scr.State == agent.Idle {
			tr.screenIdle++
		} else {
			tr.screenIdle = 0
		}

		var a agent.Agent
		if hasHook && h.HasHooks {
			a = h
			a.Source = "hook"
			switch a.State {
			case agent.Blocked:
				// Only hook events clear a block; a permission prompt shows the idle title, so the
				// title is never trusted here. Screen rules are: an idle prompt box for two polls
				// means the prompt is gone (dismissed with Esc).
				if tr.screenIdle >= 2 {
					a.State, a.Reason = agent.Idle, ""
				}
			case agent.Working: // interrupted turn (Esc): a working Claude always shows the spinner
				if tr.idleTitle >= 3 {
					a.State, a.CurrentTool, a.ToolDetail, a.TurnStarted = agent.Idle, "", "", time.Time{}
				}
			default: // hooks missed a prompt (resumed session): trust the spinner
				if tr.spinnerPoll >= 2 {
					a.State, a.StateSince = agent.Working, tr.since
					if a.TurnStarted.IsZero() {
						a.TurnStarted = tr.since
					}
				}
			}
			if screened && scr.Matched && !scr.Hold && scr.State == agent.Blocked && a.State == agent.Working {
				a.State, a.Reason = agent.Blocked, "prompt" // a visible prompt the hooks did not report
			}
			if a.State == agent.Done && !seenAt.Before(a.StateSince) {
				a.State = agent.Idle
			}
			a.Unseen = state.UnseenSince(a, seenAt)
			if focused && (a.Unseen > 0 || a.State == agent.Done) {
				out.NewlySeen = append(out.NewlySeen, p.ID)
				a.Unseen = 0
				if a.State == agent.Done {
					a.State = agent.Idle
				}
			}
		} else {
			st := titleState
			if !tOK {
				st = agent.Unknown
				if hasReg {
					st = reg.AgentState()
				}
			}
			reason := ""
			switch {
			case screened && scr.Matched && scr.Hold:
				if tr.lastRaw != "" {
					st = tr.lastRaw // e.g. model picker open: keep whatever we knew
				}
			case screened && scr.Matched:
				st = scr.State
				if st == agent.Blocked {
					reason = "prompt"
				}
			case screened && !tOK && !hasReg:
				st = agent.Idle // known agent, nothing visible: herdr's default_known_agent_idle_fallback
			}
			tr.lastRaw = st
			if tr.titleState != st {
				if tr.titleState == agent.Working && st == agent.Idle && !focused {
					tr.done, tr.doneAt = true, now
					tr.unseen++
				}
				tr.titleState, tr.since = st, now
			}
			if st == agent.Working || focused {
				tr.done, tr.unseen = false, 0
			}
			eff, since := st, tr.since
			if tr.done && st == agent.Idle {
				eff, since = agent.Done, tr.doneAt
			}
			a = agent.Agent{State: eff, StateSince: since, Unseen: tr.unseen, Source: "title", Reason: reason}
			if eff == agent.Working {
				a.TurnStarted = tr.since
			}
			if hasReg {
				a.Source, a.AgentSessionID = "registry", reg.SessionID
			}
			if screened && scr.Matched {
				a.Source = "screen"
			}
		}
		a.PaneID, a.SessionID, a.SessionName = p.ID, p.SessionID, p.SessionName
		a.WindowID, a.WindowIndex, a.PaneIndex, a.Kind = p.WindowID, p.WindowIndex, p.PaneIndex, kind
		if a.Cwd == "" {
			a.Cwd = p.Path
		}
		title := ""
		if ad != nil {
			title = ad.TitleName(p.Title)
		}
		if title == "" && hasReg {
			title = reg.Name
		}
		if title == "" {
			title = a.Name
		}
		a.Title = title
		a.Name = projectLabel(a.Cwd, title, p.SessionName)
		agents = append(agents, a)
	}
	for id := range t.panes {
		if !present[id] {
			delete(t.panes, id)
		}
	}
	SortAgents(agents)

	bySession := map[string][]agent.Agent{}
	for _, a := range agents {
		bySession[a.SessionID] = append(bySession[a.SessionID], a)
		out.Unseen += a.Unseen
	}
	for _, s := range in.Tmux.Sessions {
		sp := agent.Space{SessionID: s.ID, SessionName: s.Name, Path: s.Path, Attached: s.Attached > 0,
			Current: focus.SessionID == s.ID}
		if !in.BranchFromSessionPath {
			if ap, ok := in.Tmux.ActivePane(s.ID); ok && ap.Path != "" {
				sp.Path = ap.Path
			}
		}
		if in.BranchOf != nil {
			sp.Branch = in.BranchOf(sp.Path)
		}
		as := bySession[s.ID]
		sp.AgentCount = len(as)
		if len(as) > 0 {
			sp.Rollup = as[0].State // agents are priority-sorted, so the first is the rollup
		}
		out.Spaces = append(out.Spaces, sp)
	}
	sortSpaces(out.Spaces, in.Tmux.Sessions, in.SessionOrder)
	out.Agents = agents
	return out
}

// sortSpaces orders the sessions like tmux's choose-tree -O so the sidebar matches `prefix s`:
// "index" (session id, tmux's default; list-sessions itself is by name), "name", or "activity"
// (most recent first).
func sortSpaces(sp []agent.Space, sessions []tmux.Session, order string) {
	byID := make(map[string]tmux.Session, len(sessions))
	for _, s := range sessions {
		byID[s.ID] = s
	}
	idx := func(id string) int { n, _ := strconv.Atoi(strings.TrimPrefix(id, "$")); return n }
	sort.SliceStable(sp, func(i, j int) bool {
		a, b := sp[i], sp[j]
		switch order {
		case "name":
			return a.SessionName < b.SessionName
		case "activity":
			return byID[a.SessionID].Activity > byID[b.SessionID].Activity
		}
		return idx(a.SessionID) < idx(b.SessionID)
	})
}

// SortAgents orders by attention priority, newest first among blocked/done, then by position.
func SortAgents(as []agent.Agent) {
	sort.SliceStable(as, func(i, j int) bool {
		pi, pj := agent.Priority(as[i].State), agent.Priority(as[j].State)
		if pi != pj {
			return pi < pj
		}
		if pi <= 1 && !as[i].StateSince.Equal(as[j].StateSince) {
			return as[i].StateSince.After(as[j].StateSince)
		}
		if as[i].SessionName != as[j].SessionName {
			return as[i].SessionName < as[j].SessionName
		}
		if as[i].WindowIndex != as[j].WindowIndex {
			return as[i].WindowIndex < as[j].WindowIndex
		}
		return as[i].PaneIndex < as[j].PaneIndex
	})
}

// projectLabel is the primary agent label: the base name of the working directory (what herdr
// shows as the workspace), else the agent's own title, else the tmux session name.
func projectLabel(cwd, title, session string) string {
	if b := filepath.Base(filepath.Clean(cwd)); cwd != "" && b != "." && b != "/" {
		return b
	}
	if title != "" {
		return title
	}
	return session
}
