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

// Correction says a hook record's state was overruled by other evidence.
type Correction struct {
	PaneID string
	From   agent.State // the hook state that was overruled
	Since  time.Time   // its StateSince, so a newer hook event is never overwritten
	Reason string      // registry idle | screen idle | prompt gone
}

// StaleHook identifies a hook record whose pane no longer runs that agent.
type StaleHook struct {
	PaneID         string
	AgentSessionID string
	LastEventAt    time.Time
}

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
	// Corrections are hook states the fallbacks overruled (stale working/blocked). The caller
	// writes them back to the hook record so the correction sticks instead of flapping.
	Corrections []Correction
	// StaleHooks are filtered immediately; the long-lived UI removes them from the store if
	// no newer hook event arrived after this snapshot.
	StaleHooks []StaleHook
	TakenAt    time.Time
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
	RegistrySeq           int                        // bumps on every fresh registry poll (for per-sample counting)
	RegistryAt            time.Time                  // when that registry sample was taken
	Screen                map[string]rules.Result    // pane id -> screen-rule result (only evaluated panes)
	ScreenSeq             int                        // bumps on every screen poll
	ScreenAt              time.Time                  // when that screen sample was taken
	TerminalUnfocused     bool                       // the terminal window itself is not focused
	StaleWorking          time.Duration              // a "waiting" state older than this may be overruled again (0 = 30 min)
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
	screenIdle  int         // consecutive screen samples showing the idle prompt box
	screenSeq   int         // screen sample already counted
	regSeq      int         // registry sample already counted
	regIdle     int         // consecutive registry samples saying idle
	hookSince   time.Time   // StateSince of the hook record the counters above refer to
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

// evidenceGrace is how much newer than a hook state a registry/screen sample must be before it
// may overrule it; both sources lag a new turn by a moment.
const evidenceGrace = 3 * time.Second

// screenIdleNeed is how many consecutive screen samples must show a bare idle prompt box before
// they end a hook "working" state (~6 s at the default cadence). screenIdleNeedBusy applies while
// a registry sample younger than registryBusyTrust still reports the agent busy: a misread
// screen (a spinner frame the rules do not know, a redraw caught halfway) then needs twice the
// evidence, and an interrupted turn still clears within a registry interval or two.
const (
	screenIdleNeed     = 3
	screenIdleNeedBusy = 6
	registryBusyTrust  = 30 * time.Second
)

var shells = map[string]bool{"zsh": true, "bash": true, "fish": true, "sh": true, "login": true, "nu": true, "tcsh": true, "ksh": true}

// Build merges the inputs into a Snapshot, updating the tracker's history.
func (t *Tracker) Build(in Inputs) Snapshot {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	if in.StaleWorking <= 0 {
		in.StaleWorking = 30 * time.Minute
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
			if hasHook && !hasReg {
				out.StaleHooks = append(out.StaleHooks, staleHook(p.ID, h))
			}
			continue
		}
		if ad == nil && hasHook && !hasReg && (h.State == agent.Idle || h.State == agent.Done) {
			// At rest, the agent itself must be tmux's foreground process. A different command
			// (ssh, nvim, etc.) means the pane was reused after the agent exited without a
			// sessionEnd hook. While working/blocked, keep trusting hooks because an agent may
			// legitimately put any tool in the foreground.
			out.StaleHooks = append(out.StaleHooks, staleHook(p.ID, h))
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

		var a agent.Agent
		if hasHook && h.HasHooks {
			a = h
			a.Source = "hook"
			// Evidence that overrules a hook state must be newer than that state: a new prompt
			// resets the counters, and samples taken before StateSince (plus a grace for the
			// registry and the screen to catch up) are not counted.
			if !h.StateSince.Equal(tr.hookSince) {
				tr.hookSince = h.StateSince
				tr.regIdle, tr.screenIdle = 0, 0
			}
			fresh := func(at time.Time) bool { return at.IsZero() || at.After(h.StateSince.Add(evidenceGrace)) }
			if in.RegistrySeq != tr.regSeq {
				tr.regSeq = in.RegistrySeq
				switch {
				case hasReg && reg.Status == "idle" && fresh(in.RegistryAt):
					tr.regIdle++
				case hasReg && reg.Status != "idle":
					tr.regIdle = 0
				}
			}
			if in.ScreenSeq != tr.screenSeq {
				tr.screenSeq = in.ScreenSeq
				// "idle prompt visible" counts only when a screen region said so; the title-based
				// idle rule is no evidence (Claude keeps the idle glyph while busy inside tmux).
				idleScreen := screened && scr.Matched && scr.State == agent.Idle && scr.Region != "osc_title"
				switch {
				case idleScreen && fresh(in.ScreenAt):
					tr.screenIdle++
				case screened && !idleScreen:
					tr.screenIdle = 0
				}
			}
			switch a.State {
			case agent.Blocked:
				// Only hook events clear a block; a permission prompt shows the idle title, so the
				// title is never trusted here. Screen rules are: an idle prompt box for two polls
				// means the prompt is gone (dismissed with Esc).
				if tr.screenIdle >= 2 {
					out.Corrections = append(out.Corrections, Correction{PaneID: p.ID, From: a.State, Since: a.StateSince, Reason: "prompt gone"})
					a.State, a.Reason = agent.Idle, ""
				}
			case agent.Working:
				// An interrupted turn (Esc), a usage-limit cut-off or an errored turn emits no hook.
				// The title is NOT a signal: Claude Code keeps the idle "✳" title while busy inside
				// tmux. Trust Claude's own registry (idle in two consecutive samples, ~10-20 s at
				// the default cadence) or the screen rules showing a bare idle prompt box for three
				// polls (~6 s; herdr's rules rank the working status line above the prompt box, and
				// panes in copy mode are not captured), six while a recent registry sample still
				// says busy. Whichever comes first wins.
				// While paused for background tasks the registry and the prompt box both look idle
				// by design; only a very old waiting state (stale_working) is questioned.
				waiting := a.Reason == "waiting" && now.Sub(a.StateSince) < in.StaleWorking
				needScreen := screenIdleNeed
				if hasReg && reg.Status == "busy" && !in.RegistryAt.IsZero() && now.Sub(in.RegistryAt) <= registryBusyTrust {
					needScreen = screenIdleNeedBusy
				}
				if !waiting && (tr.regIdle >= 2 || tr.screenIdle >= needScreen) {
					why := "screen idle"
					if tr.regIdle >= 2 {
						why = "registry idle"
					}
					out.Corrections = append(out.Corrections, Correction{PaneID: p.ID, From: a.State, Since: a.StateSince, Reason: why})
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

func staleHook(pane string, a agent.Agent) StaleHook {
	return StaleHook{PaneID: pane, AgentSessionID: a.AgentSessionID, LastEventAt: a.LastEventAt}
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
