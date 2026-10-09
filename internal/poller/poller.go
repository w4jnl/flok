// Package poller is the state pipeline behind the sidebar, separated from its rendering: the
// tmux snapshot poll, the Claude registry and screen-rule samplers, the fsnotify watch on the
// store, the merge and its persistence, and the sound transitions. The sidebar drives it from
// Bubble Tea commands; `flok serve` and plain-mode remote hosts drive it with Run.
//
// Sequencing rules (the merge counts samples, so they matter): only Poll spawns tmux; registry,
// screen and store changes go through Rebuild, which re-merges the cached tmux snapshot;
// ApplyScreen advances the screen sequence once per sample with results, identical or not; an
// unchanged poll (same fingerprint and sequences) skips the merge entirely.
package poller

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"hash/fnv"
	"sync"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/claudereg"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/rules"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

// Deps is what the pipeline reads and writes.
type Deps struct {
	Cfg       config.Config
	Tmux      tmux.Client
	Store     *state.Store // hook records, seen marks and markers; nil disables all of them
	Registry  bool         // poll `claude agents --json`
	Rules     *rules.Set   // screen-rule manifests; nil disables capture-pane detection
	Adapters  []agent.Adapter
	BranchOf  func(string) string
	ClientTTY string // the client whose view decides focus; "" = the most recently active one
	// Sound is called for a sound-worthy transition that passed the rate limit; nil = silent.
	// The sidebar plays it, a serve session forwards it.
	Sound func(pane, kind string)
	// SoundHookNotifications replays hook notifications the hook itself left unsounded
	// (sidebar: [sounds] player = "sidebar"; serve: always, the local side plays).
	SoundHookNotifications bool
	// PollFloorMs and ScreenFloorMs raise the cadence floors (defaults 200 and 500 ms); a
	// remote host driven over ssh polls slower.
	PollFloorMs, ScreenFloorMs int
	// OnRequest receives the @flok-request option a key binding set in this tmux server (a
	// plain-mode host); the option is cleared right after. nil ignores it.
	OnRequest func(cmd string)
	// OnStoreEvent runs on the loop's goroutine when the store watcher fires (Run/RunLoop only):
	// a serve session drains its request mailbox there.
	OnStoreEvent func()
	// OnServerRestart runs when the polled tmux server's pid changed between two polls: what
	// lived in the old server (key bindings) is gone.
	OnServerRestart func()
	Debugf          func(format string, args ...any)
}

// SnapshotMsg is one tmux poll (or a Rebuild of the cached one) plus the store's view of the
// world at that moment. Pointer fields are set only when the message actually read them, so a
// stale copy cannot undo a change.
type SnapshotMsg struct {
	FP        uint64
	Snap      tmux.Snapshot
	Raw       string
	Hook      map[string]agent.Agent
	Seen      map[string]time.Time
	Unfocused bool
	Hidden    bool
	KeepAwake *bool
	Theme     string
	Rebuilt   bool // produced by Rebuild from cached tmux data, not by a tmux poll
	Err       error
}

// RegistryMsg is one `claude agents --json` sample; nil Entries means the call failed.
type RegistryMsg struct{ Entries map[string]claudereg.Entry }

// ScreenMsg is one screen-rule sample; empty Results means nothing was evaluated (not a sample).
// ScreenSample is what a screen result was judged from: the capture and the title and progress
// the pane had when the capture was scheduled.
type ScreenSample struct{ Kind, Title, Progress, Text string }

type ScreenMsg struct {
	Results map[string]rules.Result
	Samples map[string]ScreenSample
}

// Poller holds the pipeline state. It is not safe for concurrent use: the sidebar touches it
// from Update only, Run from one goroutine.
type Poller struct {
	d                      Deps
	tracker                *merge.Tracker
	snap                   merge.Snapshot
	changes                chan struct{} // nil without a store
	watcher                *fsnotify.Watcher
	serverPID              int // pid of the polled tmux server as of the last poll
	pollMu                 sync.Mutex
	pollErr                error // the last tmux poll's error under Run/RunLoop, nil after a good one
	registry               map[string]claudereg.Entry
	registrySeq            int
	registryAt             time.Time
	screen                 map[string]rules.Result
	screenSeq              int
	screenAt               time.Time
	titles                 map[string]string
	progress               map[string]string // pane id -> OSC 9;4 payload for the screen rules
	prevState              map[string]agent.State
	started                time.Time
	lastFP                 uint64 // fingerprint of the inputs of the last Build
	lastRegSeq, lastScrSeq int
	tmuxSnap               tmux.Snapshot // last tmux snapshot; rebuilds reuse it instead of spawning tmux
	lastRaw                string        // raw tmux output behind tmuxSnap (fingerprint input for rebuilds)
	lastHook               map[string]agent.Agent
	lastSeen               map[string]time.Time
	lastUnfocused          bool // marker values of the last applied poll: what a Rebuild replays
	lastHidden             bool
	lastTheme              string
}

// New builds the pipeline and, with a store, starts watching it for hook and marker changes.
func New(d Deps) *Poller {
	p := &Poller{d: d, tracker: merge.NewTracker(), prevState: map[string]agent.State{}, started: time.Now()}
	if d.Store != nil {
		p.changes = make(chan struct{}, 1)
		if w, err := watchStore(d.Store.Dir); err == nil {
			p.watcher = w
			go watchLoop(w, d.Store.Dir, p.changes)
		}
	}
	return p
}

// PollError is the error of the last tmux poll under Run/RunLoop (nil after a successful one):
// a serve session reports it so the local side can say "no tmux server" instead of nothing.
func (p *Poller) PollError() error {
	p.pollMu.Lock()
	defer p.pollMu.Unlock()
	return p.pollErr
}

func (p *Poller) setPollErr(err error) {
	p.pollMu.Lock()
	p.pollErr = err
	p.pollMu.Unlock()
}

// handleRequest acts on a @flok-request value a binding set in the polled server and clears
// it at once, so a value seen by the next poll is a new key press (the same key twice within a
// poll interval must count twice: hide, hide).
func (p *Poller) handleRequest(s tmux.Snapshot) {
	if s.Request == "" {
		return
	}
	_, _ = p.d.Tmux.Run("set-option", "-gu", tmux.RequestOption)
	p.d.OnRequest(s.Request)
}

// Close stops the store watcher. The sidebar's poller lives as long as the process; a remote
// host's poller is rebuilt on every reconnect and must not leak watchers.
func (p *Poller) Close() {
	if p.watcher != nil {
		_ = p.watcher.Close()
		p.watcher = nil
	}
}

func (p *Poller) debugf(format string, args ...any) {
	if p.d.Debugf != nil {
		p.d.Debugf(format, args...)
	}
}

// Snap is the last merged view.
func (p *Poller) Snap() merge.Snapshot { return p.snap }

// Changes signals a hook record, seen mark or marker change on disk; nil without a store.
func (p *Poller) Changes() <-chan struct{} { return p.changes }

// HasTmuxData reports whether a poll has happened, i.e. Rebuild can work from the cache.
func (p *Poller) HasTmuxData() bool { return p.lastRaw != "" }

// LastFP is the fingerprint of the inputs of the last merge.
func (p *Poller) LastFP() uint64 { return p.lastFP }

// Prime seeds the tmux cache, so the next Rebuild does not poll (tests, resume from a snapshot).
func (p *Poller) Prime(snap tmux.Snapshot, raw string) { p.tmuxSnap, p.lastRaw = snap, raw }

// SetHidden overrides the cached hidden flag when the caller learned better (the outer's
// window_zoomed_flag beat a stale sidebar-hidden marker), so a Rebuild replays the truth.
func (p *Poller) SetHidden(hidden bool) { p.lastHidden = hidden }

// MarkSeen records that the user looked at a pane: in the tracker (this poll) and the store.
func (p *Poller) MarkSeen(pane string) {
	p.tracker.MarkSeen(pane)
	if p.d.Store != nil {
		_ = p.d.Store.MarkSeen(pane, time.Now())
	}
}

// Poll takes a tmux snapshot and the store's markers; the returned function does the work.
func (p *Poller) Poll() func() SnapshotMsg {
	c, store := p.d.Tmux, p.d.Store
	return func() SnapshotMsg {
		s, raw, err := tmux.TakeSnapshotRaw(c)
		msg := SnapshotMsg{Snap: s, Raw: raw, Err: err}
		if store != nil {
			msg.Hook, msg.Seen = store.LoadAgents(), store.LoadSeen()
			keepAwake := store.KeepAwake()
			msg.Unfocused, msg.Hidden, msg.KeepAwake = !store.TerminalFocused(), store.SidebarHidden(), &keepAwake
			msg.Theme, _ = store.TerminalTheme()
		}
		msg.FP = Fingerprint(raw, msg.Hook, msg.Seen, msg.Unfocused)
		return msg
	}
}

// Rebuild re-runs the merge on the cached tmux snapshot without spawning tmux: after a screen
// or registry sample, or when hook records or the focus/hidden markers changed on disk
// (reloadStore). Before the first poll nothing is cached, so it polls.
func (p *Poller) Rebuild(reloadStore bool) func() SnapshotMsg {
	if p.lastRaw == "" {
		return p.Poll()
	}
	store := p.d.Store
	snap, raw := p.tmuxSnap, p.lastRaw
	hook, seen := p.lastHook, p.lastSeen
	unfocused, hidden, theme := p.lastUnfocused, p.lastHidden, p.lastTheme
	return func() SnapshotMsg {
		var keepAwake *bool
		if reloadStore && store != nil {
			hook, seen = store.LoadAgents(), store.LoadSeen()
			k := store.KeepAwake()
			unfocused, hidden, keepAwake = !store.TerminalFocused(), store.SidebarHidden(), &k
			theme, _ = store.TerminalTheme()
		}
		return SnapshotMsg{Snap: snap, Raw: raw, Hook: hook, Seen: seen, Unfocused: unfocused, Hidden: hidden, Theme: theme, KeepAwake: keepAwake,
			FP: Fingerprint(raw, hook, seen, unfocused), Rebuilt: true}
	}
}

// Fingerprint covers everything Build looks at, so an unchanged poll costs no rebuild.
func Fingerprint(raw string, hook map[string]agent.Agent, seen map[string]time.Time, unfocused bool) uint64 {
	h := fnv.New64a()
	h.Write([]byte(raw))
	if b, err := json.Marshal(hook); err == nil {
		h.Write(b)
	}
	if b, err := json.Marshal(seen); err == nil {
		h.Write(b)
	}
	fmt.Fprintf(h, "%v", unfocused)
	return h.Sum64()
}

// ApplySnapshot caches the poll and, unless nothing changed since the last merge (force
// overrides that, e.g. after an error), merges it, persists what the merge decided (seen marks,
// corrections, stale hook records) and plays sound transitions. It reports whether the merge
// ran. Messages carrying Err must not be applied.
func (p *Poller) ApplySnapshot(msg SnapshotMsg, force bool) (merged bool) {
	p.tmuxSnap, p.lastRaw, p.lastHook, p.lastSeen = msg.Snap, msg.Raw, msg.Hook, msg.Seen
	if p.d.OnRequest != nil && !msg.Rebuilt {
		p.handleRequest(msg.Snap)
	}
	if pid := msg.Snap.ServerPID; pid != 0 && !msg.Rebuilt {
		if p.serverPID != 0 && pid != p.serverPID && p.d.OnServerRestart != nil {
			p.d.OnServerRestart()
		}
		p.serverPID = pid
	}
	p.lastUnfocused, p.lastHidden, p.lastTheme = msg.Unfocused, msg.Hidden, msg.Theme
	if msg.FP == p.lastFP && p.registrySeq == p.lastRegSeq && p.screenSeq == p.lastScrSeq && !force {
		return false // identical inputs: the caller keeps its frame
	}
	p.lastFP, p.lastRegSeq, p.lastScrSeq = msg.FP, p.registrySeq, p.screenSeq
	titles := make(map[string]string, len(msg.Snap.Panes))
	progress := make(map[string]string, len(msg.Snap.Panes))
	for _, pane := range msg.Snap.Panes {
		titles[pane.ID] = pane.Title
		progress[pane.ID] = rules.OSCProgress(pane.PBState, pane.PBProgress)
	}
	p.titles, p.progress = titles, progress
	p.snap = p.tracker.Build(merge.Inputs{Tmux: msg.Snap, ClientTTY: p.d.ClientTTY, Adapters: p.d.Adapters, SessionOrder: p.d.Cfg.Sidebar.SessionOrder,
		BranchOf: p.d.BranchOf, BranchFromSessionPath: p.d.Cfg.Sidebar.BranchSource == "session_path",
		Hook: msg.Hook, Seen: msg.Seen, Registry: p.registry, RegistrySeq: p.registrySeq, RegistryAt: p.registryAt,
		Screen: p.screen, ScreenSeq: p.screenSeq, ScreenAt: p.screenAt, TerminalUnfocused: msg.Unfocused, AgentOrder: p.d.Cfg.Sidebar.AgentOrder,
		StaleWorking: time.Duration(p.d.Cfg.Sidebar.StaleWorkingMin) * time.Minute})
	if p.d.Store != nil {
		for _, pane := range p.snap.NewlySeen {
			_ = p.d.Store.MarkSeen(pane, time.Now())
		}
		for _, c := range p.snap.Corrections {
			p.persistCorrection(c)
		}
		for _, stale := range p.snap.StaleHooks {
			_ = p.d.Store.DeleteAgentIfUnchanged(stale.PaneID, stale.AgentSessionID, stale.LastEventAt)
		}
	}
	p.soundTransitions()
	return true
}

// ApplyRegistry stores a registry sample; a failed call (nil Entries) is not a sample. It
// reports whether a Rebuild is due.
func (p *Poller) ApplyRegistry(msg RegistryMsg) bool {
	if msg.Entries == nil {
		return false
	}
	p.registry = msg.Entries
	p.registrySeq++
	p.registryAt = time.Now()
	return true
}

// ApplyScreen stores a screen sample. Empty results mean nothing was evaluated, not a sample;
// otherwise the sequence advances, identical results or not, because the merge counts samples.
// It reports whether a Rebuild is due.
func (p *Poller) ApplyScreen(msg ScreenMsg) bool {
	// A capture is scheduled with the titles of the last tmux poll and comes back later. When the
	// title moved in between (the spinner appeared), the result was judged against the old title
	// and, applied now, would undo what the newer poll saw: a hook-less row would read idle, and
	// unfocused, done. Judge such a sample again with the title known now.
	if p.d.Rules != nil {
		for pane, s := range msg.Samples {
			title, ok := p.titles[pane]
			if !ok || (title == s.Title && p.progress[pane] == s.Progress) {
				continue
			}
			if m := p.d.Rules.Get(s.Kind); m != nil {
				sc := rules.NewScreen(s.Text, title)
				sc.Progress = p.progress[pane]
				msg.Results[pane] = m.Evaluate(sc)
			}
		}
	}
	p.screen = msg.Results
	if len(msg.Results) == 0 {
		return false
	}
	p.screenSeq++
	p.screenAt = time.Now()
	return true
}

// RegistryEnabled reports whether registry polling is configured.
func (p *Poller) RegistryEnabled() bool { return p.d.Registry }

// RegistryInterval is the registry tick (registry_poll_ms, floor 1 s).
func (p *Poller) RegistryInterval() time.Duration {
	ms := p.d.Cfg.Sidebar.RegistryPollMs
	if ms < 1000 {
		ms = 1000
	}
	return time.Duration(ms) * time.Millisecond
}

// The registry (`claude agents --json`) costs ~0.2 s of CPU per call in a node process, so on the
// registry tick it is queried only while something can use the answer: a Claude pane without
// hook records (the registry is its state source) or a hook agent with a turn or prompt open
// (registry idle samples are what clear an interrupted turn or a dismissed prompt). With every
// agent idle it is refreshed once per registrySlowEvery, which still discovers a Claude the
// process name does not reveal (npm installs run as node) before its first hook.
const registrySlowEvery = 60 * time.Second

// PollRegistryIfDue returns the registry call when it is worth making, else nil.
func (p *Poller) PollRegistryIfDue() func() RegistryMsg {
	if !p.d.Registry {
		return nil
	}
	if p.registryNeeded() || time.Since(p.registryAt) >= registrySlowEvery {
		return p.PollRegistry()
	}
	return nil
}

func (p *Poller) registryNeeded() bool {
	for _, a := range p.snap.Agents {
		if a.Kind != "claude" {
			continue
		}
		if a.Source != "hook" || a.State == agent.Working || a.State == agent.Blocked || a.State == agent.Paused {
			return true
		}
	}
	return false
}

// PollRegistry returns the registry call, nil when the registry is disabled.
func (p *Poller) PollRegistry() func() RegistryMsg {
	if !p.d.Registry {
		return nil
	}
	return func() RegistryMsg {
		entries, err := claudereg.List(3 * time.Second)
		if err != nil {
			return RegistryMsg{}
		}
		return RegistryMsg{claudereg.ByTTY(entries)}
	}
}

// ScreenEnabled reports whether screen rules run at all.
func (p *Poller) ScreenEnabled() bool {
	return p.d.Rules != nil && p.d.Cfg.Agents.ScreenRules != "never"
}

// needsScreen decides which agents get a capture-pane evaluation: hook-less ones in "auto",
// everyone in "always".
func (p *Poller) needsScreen(a agent.Agent) bool {
	switch p.d.Cfg.Agents.ScreenRules {
	case "never":
		return false
	case "always":
		return true
	}
	// hook-less agents always; hook agents while a turn or prompt is open, so an interrupted
	// turn or a dismissed prompt (neither emits a hook) is noticed from the screen
	return a.Source != "hook" || a.State == agent.Working || a.State == agent.Blocked || a.State == agent.Paused
}

// PollScreen captures the relevant panes and evaluates their manifests; the returned function
// does the work (one tmux call for every pane).
func (p *Poller) PollScreen() func() ScreenMsg {
	type target struct{ pane, kind, title, progress string }
	var targets []target
	inMode := map[string]bool{} // copy/view mode shows scrollback: an old prompt box is no evidence
	for _, pane := range p.tmuxSnap.Panes {
		if pane.InMode {
			inMode[pane.ID] = true
		}
	}
	if p.d.Rules != nil {
		for _, a := range p.snap.Agents {
			if p.needsScreen(a) && !inMode[a.PaneID] && p.d.Rules.Get(a.Kind) != nil {
				targets = append(targets, target{a.PaneID, a.Kind, p.titles[a.PaneID], p.progress[a.PaneID]})
			}
		}
	}
	c, set, n := p.d.Tmux, p.d.Rules, p.d.Cfg.Sidebar.CaptureLines
	return func() ScreenMsg {
		res, samples := map[string]rules.Result{}, map[string]ScreenSample{}
		if len(targets) == 0 {
			return ScreenMsg{Results: res}
		}
		panes := make([]string, 0, len(targets))
		for _, t := range targets {
			panes = append(panes, t.pane)
		}
		screens := CaptureAll(c, panes, n)
		for _, t := range targets {
			out, ok := screens[t.pane]
			if !ok {
				continue
			}
			sc := rules.NewScreen(out, t.title)
			sc.Progress = t.progress
			res[t.pane] = set.Get(t.kind).Evaluate(sc)
			samples[t.pane] = ScreenSample{Kind: t.kind, Title: t.title, Progress: t.progress, Text: out}
		}
		return ScreenMsg{Results: res, Samples: samples}
	}
}

func (p *Poller) idlePollMs() int {
	ms := p.d.Cfg.Sidebar.IdlePollMs
	if ms <= 0 {
		ms = 3000
	}
	if ms < 1000 {
		ms = 1000
	}
	return ms
}

// PollInterval is the tmux snapshot cadence: poll_ms (floor 200 ms or PollFloorMs), stretched
// to idle_poll_ms while idle.
func (p *Poller) PollInterval(idle bool) time.Duration {
	floor := p.d.PollFloorMs
	if floor < 200 {
		floor = 200
	}
	ms := p.d.Cfg.Sidebar.PollMs
	if ms < floor {
		ms = floor
	}
	if idle && p.idlePollMs() > ms {
		ms = p.idlePollMs()
	}
	return time.Duration(ms) * time.Millisecond
}

// ScreenInterval is the capture-pane cadence (floor 500 ms or ScreenFloorMs), stretched the same
// way while idle.
func (p *Poller) ScreenInterval(idle bool) time.Duration {
	floor := p.d.ScreenFloorMs
	if floor < 500 {
		floor = 500
	}
	ms := p.d.Cfg.Sidebar.ScreenPollMs
	if ms < floor {
		ms = floor
	}
	if idle && p.idlePollMs() > ms {
		ms = p.idlePollMs()
	}
	return time.Duration(ms) * time.Millisecond
}

// Run drives the pipeline without Bubble Tea until ctx ends: the poll timer, the registry and
// screen tickers and the store watch, each applied the way the sidebar applies them. onChange
// runs after every merge with the merged view.
func Run(ctx context.Context, p *Poller, onChange func(merge.Snapshot)) error {
	return RunLoop(ctx, p, onChange, nil)
}

// RunLoop is Run with a command channel: each function received on cmds runs on the loop's
// goroutine (the poller is not safe for concurrent use) and is followed by a rebuild, so a
// serve session can apply seen marks and read the focus from other goroutines.
func RunLoop(ctx context.Context, p *Poller, onChange func(merge.Snapshot), cmds <-chan func()) error {
	apply := func(msg SnapshotMsg, force bool) {
		if msg.Err != nil {
			p.debugf("poll: %v", msg.Err)
			if !msg.Rebuilt {
				p.setPollErr(msg.Err)
			}
			return
		}
		if !msg.Rebuilt {
			p.setPollErr(nil)
		}
		if p.ApplySnapshot(msg, force) && onChange != nil {
			onChange(p.snap)
		}
	}
	apply(p.Poll()(), true)
	if f := p.PollRegistry(); f != nil && p.ApplyRegistry(f()) {
		apply(p.Rebuild(false)(), false)
	}
	poll := time.NewTimer(p.PollInterval(false))
	defer poll.Stop()
	var regC, scrC <-chan time.Time
	if p.RegistryEnabled() {
		t := time.NewTicker(p.RegistryInterval())
		defer t.Stop()
		regC = t.C
	}
	if p.ScreenEnabled() {
		t := time.NewTicker(p.ScreenInterval(false))
		defer t.Stop()
		scrC = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-poll.C:
			apply(p.Poll()(), false)
			poll.Reset(p.PollInterval(false))
		case <-regC:
			if f := p.PollRegistryIfDue(); f != nil && p.ApplyRegistry(f()) {
				apply(p.Rebuild(false)(), false)
			}
		case <-scrC:
			if p.ApplyScreen(p.PollScreen()()) {
				apply(p.Rebuild(false)(), false)
			}
		case <-p.changes:
			if p.d.OnStoreEvent != nil {
				p.d.OnStoreEvent()
			}
			apply(p.Rebuild(true)(), false)
		case f := <-cmds:
			f()
			apply(p.Rebuild(false)(), false)
		}
	}
}
