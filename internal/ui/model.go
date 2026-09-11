// Package ui is the Bubble Tea sidebar: spaces on top, agents below, a rail when narrow.
package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"
	"github.com/muesli/termenv"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/claudereg"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/keys"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/nav"
	"github.com/w4jnl/flok/internal/notify"
	"github.com/w4jnl/flok/internal/rules"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

// Deps is everything the sidebar needs from the outside world.
type Deps struct {
	Cfg         config.Config
	Inner       tmux.Client
	Outer       tmux.Client // nil when not running inside the outer server
	RightPane   string      // outer pane id hosting the inner client
	SidebarPane string      // outer pane id of the sidebar itself ($TMUX_PANE)
	ClientTTY   string      // inner client to drive; resolved from RightPane when empty
	Adapters    []agent.Adapter
	BranchOf    func(string) string
	Store       *state.Store        // hook records and seen marks; nil disables hooks
	Registry    bool                // poll `claude agents --json`
	Rules       *rules.Set          // screen-rule manifests; nil disables capture-pane detection
	OnSwitch    func(paneID string) // called after switching to an agent pane
}

const (
	panelSpaces = 0
	panelAgents = 1
)

type Model struct {
	d         Deps
	theme     Theme
	tracker   *merge.Tracker
	snap      merge.Snapshot
	clientTTY string
	width     int
	height    int
	panel     int
	cursor    [2]int
	offset    [2]int
	frame     int
	animating bool
	errText   string
	registry  map[string]claudereg.Entry
	changes   chan struct{}
	help      *HelpModel
	screen    map[string]rules.Result
	titles    map[string]string
	progress  map[string]string // pane id -> OSC 9;4 payload for the screen rules
	prevState map[string]agent.State
	sounder   notify.Sounder
	started   time.Time
	focused   bool // the outer's active pane is the sidebar: keys arrive here
	// Inner tmux prefix, so chords typed while the sidebar has focus are replayed into the work
	// pane instead of being swallowed (prefixTmux "C-a", prefixKey "ctrl+a").
	prefixTmux    string
	prefixKey     string
	prefixPending bool
	debug         bool
}

type (
	tickMsg     struct{}
	animMsg     struct{}
	snapshotMsg struct {
		sidebarFocused bool
		snap           tmux.Snapshot
		hook           map[string]agent.Agent
		seen           map[string]time.Time
		unfocused      bool
		err            error
	}
	switchedMsg     struct{ err error }
	stateChangedMsg struct{}
	registryTickMsg struct{}
	registryMsg     struct{ entries map[string]claudereg.Entry }
	screenTickMsg   struct{}
	screenMsg       struct{ results map[string]rules.Result }
)

func New(d Deps) Model {
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := Model{d: d, theme: NewTheme(d.Cfg.Theme), tracker: merge.NewTracker(), clientTTY: d.ClientTTY, changes: make(chan struct{}, 1),
		prevState: map[string]agent.State{}, sounder: notify.Noop{}, started: time.Now()}
	if d.Cfg.Sounds.Enabled && d.Cfg.Sounds.Player != "none" {
		m.sounder = notify.Afplay{Files: notify.Resolve(config.StateDir(), map[string]string{"done": d.Cfg.Sounds.Done, "blocked": d.Cfg.Sounds.Blocked, "error": d.Cfg.Sounds.Error}),
			Volume: d.Cfg.Sounds.Volume}
	}
	if m.clientTTY == "" && d.Outer != nil && d.RightPane != "" {
		if tty, err := tmux.Display(d.Outer, d.RightPane, "#{pane_tty}"); err == nil {
			m.clientTTY = tty
		}
	}
	if d.Store != nil {
		go watchStore(d.Store.Dir, m.changes)
	}
	m.debug = os.Getenv("FLOK_DEBUG") != ""
	m.readPrefix()
	return m
}

// readPrefix caches the inner server's prefix key (re-read on `r` and on reload).
func (m *Model) readPrefix() {
	if out, err := m.d.Inner.Run("show-options", "-gv", "prefix"); err == nil {
		m.prefixTmux = strings.TrimSpace(out)
		m.prefixKey = teaKeyFromTmux(m.prefixTmux)
	}
}

// debugf appends to sidebar.log in the state dir when FLOK_DEBUG is set.
func (m Model) debugf(format string, args ...any) {
	if !m.debug || m.d.Store == nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(m.d.Store.Dir, "sidebar.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format("15:04:05.000")+" "+format+"\n", args...)
}

// forwardChord replays "<prefix> <key>" into the work pane through the outer server. The
// work pane also takes keyboard focus, except for `prefix b` (rail toggle), so the user can
// keep navigating the bar after collapsing it.
func (m *Model) forwardChord(msg tea.KeyMsg) tea.Cmd {
	outer, right, prefix := m.d.Outer, m.d.RightPane, m.prefixTmux
	name := tmuxKeyName(msg)
	stay := name == "b"
	m.debugf("chord %s %s (stay=%v)", prefix, name, stay)
	if outer == nil || right == "" || prefix == "" {
		return nil
	}
	if !stay {
		m.focused = false
	}
	return func() tea.Msg {
		if !stay {
			_, _ = outer.Run("select-pane", "-t", right)
		}
		_, err := outer.Run("send-keys", "-t", right, prefix, name)
		return switchedMsg{err}
	}
}

// watchStore pushes a (coalesced) signal whenever a hook record or seen mark changes.
func watchStore(dir string, ch chan struct{}) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	_ = w.Add(filepath.Join(dir, "agents"))
	_ = w.Add(filepath.Join(dir, "seen"))
	for ev := range w.Events {
		if !strings.HasSuffix(ev.Name, ".json") {
			continue
		}
		select {
		case ch <- struct{}{}:
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (m Model) waitChange() tea.Cmd {
	ch := m.changes
	if m.d.Store == nil {
		return nil
	}
	return func() tea.Msg { <-ch; return stateChangedMsg{} }
}

func (m Model) registryTick() tea.Cmd {
	if !m.d.Registry {
		return nil
	}
	ms := m.d.Cfg.Sidebar.RegistryPollMs
	if ms < 1000 {
		ms = 1000
	}
	return tea.Tick(time.Duration(ms)*time.Millisecond, func(time.Time) tea.Msg { return registryTickMsg{} })
}

func (m Model) pollRegistry() tea.Cmd {
	return func() tea.Msg {
		entries, err := claudereg.List(3 * time.Second)
		if err != nil {
			return registryMsg{}
		}
		return registryMsg{claudereg.ByTTY(entries)}
	}
}

// ClientTTY is the inner client the sidebar drives (for runtime.json).
func (m Model) ClientTTY() string { return m.clientTTY }

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.poll(), m.tick(), m.waitChange(), m.pollRegistryIfEnabled(), m.registryTick(), m.screenTick())
}

func (m Model) screenTick() tea.Cmd {
	if m.d.Rules == nil || m.d.Cfg.Agents.ScreenRules == "never" {
		return nil
	}
	ms := m.d.Cfg.Sidebar.ScreenPollMs
	if ms < 500 {
		ms = 500
	}
	return tea.Tick(time.Duration(ms)*time.Millisecond, func(time.Time) tea.Msg { return screenTickMsg{} })
}

// needsScreen decides which agents get a capture-pane evaluation: hook-less ones in "auto",
// everyone in "always".
func (m Model) needsScreen(a agent.Agent) bool {
	switch m.d.Cfg.Agents.ScreenRules {
	case "never":
		return false
	case "always":
		return true
	}
	return a.Source != "hook"
}

// pollScreen captures the relevant panes and evaluates their manifests in the background.
func (m Model) pollScreen() tea.Cmd {
	type target struct{ pane, kind, title, progress string }
	var targets []target
	for _, a := range m.snap.Agents {
		if m.needsScreen(a) && m.d.Rules.Get(a.Kind) != nil {
			targets = append(targets, target{a.PaneID, a.Kind, m.titles[a.PaneID], m.progress[a.PaneID]})
		}
	}
	inner, set, n := m.d.Inner, m.d.Rules, m.d.Cfg.Sidebar.CaptureLines
	return func() tea.Msg {
		res := map[string]rules.Result{}
		for _, t := range targets {
			out, err := inner.Run(CaptureArgs(t.pane, n)...)
			if err != nil {
				continue
			}
			sc := rules.NewScreen(out, t.title)
			sc.Progress = t.progress
			res[t.pane] = set.Get(t.kind).Evaluate(sc)
		}
		return screenMsg{res}
	}
}

// captureArgs captures the visible screen of a pane (agents redraw in place, so scrollback
// would resurrect dismissed prompts); extra > 0 adds that many scrollback lines.
func CaptureArgs(pane string, extra int) []string {
	args := []string{"capture-pane", "-p", "-J", "-t", pane}
	if extra > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", extra))
	}
	return args
}

// soundTransitions plays sounds for agents the hooks do not cover (title/screen/registry
// sources) when they turn blocked or done unseen, and, with player = "sidebar", for hook
// notifications the hook left unsounded.
func (m *Model) soundTransitions() {
	cfg := m.d.Cfg.Sounds
	present := map[string]bool{}
	for _, a := range m.snap.Agents {
		present[a.PaneID] = true
		prev := m.prevState[a.PaneID]
		m.prevState[a.PaneID] = a.State
		if !cfg.Enabled {
			continue
		}
		focused := a.PaneID == m.snap.Focus.PaneID
		if a.Source == "hook" {
			if cfg.Player != "sidebar" || m.d.Store == nil {
				continue
			}
			for i, n := range a.Notifications {
				if n.Sounded || n.At.Before(m.started) || time.Since(n.At) > time.Minute {
					continue
				}
				m.playIfAllowed(a.PaneID, n.Kind)
				idx := i
				_, _, _ = m.d.Store.Update(a.PaneID, func(rec *agent.Agent) state.Effects {
					if idx < len(rec.Notifications) {
						rec.Notifications[idx].Sounded = true
					}
					return state.Effects{}
				})
			}
			continue
		}
		if prev == a.State || focused && !m.lastUnfocused() {
			continue
		}
		switch a.State {
		case agent.Blocked:
			m.playIfAllowed(a.PaneID, "blocked")
		case agent.Done:
			m.playIfAllowed(a.PaneID, "done")
		}
	}
	for pane := range m.prevState {
		if !present[pane] {
			delete(m.prevState, pane)
		}
	}
}

func (m Model) lastUnfocused() bool { return m.d.Store != nil && !m.d.Store.TerminalFocused() }

func (m Model) playIfAllowed(pane, kind string) {
	dir := ""
	if m.d.Store != nil {
		dir = m.d.Store.Dir
	}
	if dir != "" && !notify.Allowed(dir, pane+"-"+kind, 2*time.Second, time.Duration(m.d.Cfg.Sounds.MinIntervalMs)*time.Millisecond, time.Now()) {
		return
	}
	_ = m.sounder.Play(kind)
}

func (m Model) pollRegistryIfEnabled() tea.Cmd {
	if !m.d.Registry {
		return nil
	}
	return m.pollRegistry()
}

func (m Model) tick() tea.Cmd {
	ms := m.d.Cfg.Sidebar.PollMs
	if ms < 200 {
		ms = 200
	}
	return tea.Tick(time.Duration(ms)*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) anim() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return animMsg{} })
}

func (m Model) poll() tea.Cmd {
	inner, store, outer, sb := m.d.Inner, m.d.Store, m.d.Outer, m.d.SidebarPane
	return func() tea.Msg {
		s, err := tmux.TakeSnapshot(inner)
		msg := snapshotMsg{snap: s, err: err, sidebarFocused: true}
		if outer != nil && sb != "" {
			v, e := outer.Run("display-message", "-p", "-t", sb, "#{pane_active}")
			msg.sidebarFocused = e == nil && strings.TrimSpace(v) == "1"
		}
		if store != nil {
			msg.hook, msg.seen, msg.unfocused = store.LoadAgents(), store.LoadSeen(), !store.TerminalFocused()
		}
		return msg
	}
}

func (m Model) anyWorking() bool {
	for _, a := range m.snap.Agents {
		if a.State == agent.Working {
			return true
		}
	}
	return false
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.help != nil {
		switch msg.(type) {
		case tea.KeyMsg, tea.MouseMsg, tea.WindowSizeMsg:
			hm, cmd := m.help.Update(msg)
			h := hm.(HelpModel)
			if ws, ok := msg.(tea.WindowSizeMsg); ok {
				m.width, m.height = ws.Width, ws.Height
			}
			if h.closed {
				m.help = nil
			} else {
				m.help = &h
			}
			return m, cmd
		case helpClosedMsg:
			m.help = nil
			return m, nil
		}
	}
	if _, ok := msg.(helpPopupFailedMsg); ok { // no outer client (headless): draw it in the pane
		m.openHelpInline()
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clamp()
	case tickMsg:
		return m, tea.Batch(m.poll(), m.tick())
	case animMsg:
		if m.anyWorking() {
			m.frame++
			return m, m.anim()
		}
		m.animating = false
	case snapshotMsg:
		if msg.err != nil {
			m.errText = msg.err.Error()
			return m, nil
		}
		m.errText = ""
		m.focused = msg.sidebarFocused
		titles := make(map[string]string, len(msg.snap.Panes))
		progress := make(map[string]string, len(msg.snap.Panes))
		for _, p := range msg.snap.Panes {
			titles[p.ID] = p.Title
			progress[p.ID] = rules.OSCProgress(p.PBState, p.PBProgress)
		}
		m.titles, m.progress = titles, progress
		m.snap = m.tracker.Build(merge.Inputs{Tmux: msg.snap, ClientTTY: m.clientTTY, Adapters: m.d.Adapters, SessionOrder: m.d.Cfg.Sidebar.SessionOrder,
			BranchOf: m.d.BranchOf, BranchFromSessionPath: m.d.Cfg.Sidebar.BranchSource == "session_path",
			Hook: msg.hook, Seen: msg.seen, Registry: m.registry, Screen: m.screen, TerminalUnfocused: msg.unfocused})
		if m.d.Store != nil {
			for _, pane := range m.snap.NewlySeen {
				_ = m.d.Store.MarkSeen(pane, time.Now())
			}
		}
		m.soundTransitions()
		m.clamp()
		if m.anyWorking() && !m.animating {
			m.animating = true
			return m, m.anim()
		}
	case switchedMsg:
		if msg.err != nil {
			m.errText = msg.err.Error()
		}
		return m, m.poll()
	case stateChangedMsg:
		return m, tea.Batch(m.poll(), m.waitChange())
	case registryTickMsg:
		return m, tea.Batch(m.pollRegistry(), m.registryTick())
	case registryMsg:
		if msg.entries != nil {
			m.registry = msg.entries
		}
		return m, nil
	case screenTickMsg:
		return m, tea.Batch(m.pollScreen(), m.screenTick())
	case screenMsg:
		m.screen = msg.results
		return m, m.poll()
	case tea.KeyMsg:
		return m.onKey(msg)
	case tea.MouseMsg:
		return m.onMouse(msg)
	}
	return m, nil
}

func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste {
		var model tea.Model = m
		var cmd tea.Cmd
		for _, r := range msg.Runes {
			model, cmd = model.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			if cmd != nil {
				return model, cmd
			}
		}
		return model, nil
	}
	k := msg.String()
	m.debugf("key %q focused=%v panel=%d cursor=%v rail=%v", k, m.focused, m.panel, m.cursor, m.isRail())
	if m.prefixPending {
		m.prefixPending = false
		if k == "esc" {
			return m, nil
		}
		return m, m.forwardChord(msg)
	}
	if m.prefixKey != "" && k == m.prefixKey {
		m.prefixPending = true
		return m, nil
	}
	switch k {
	case "ctrl+c":
		return m, tea.Quit
	case "q", "esc":
		m.focused = false
		return m, m.focusRight()
	case "j", "down":
		m.cursor[m.panel]++
	case "k", "up":
		m.cursor[m.panel]--
	case "g", "home":
		m.cursor[m.panel] = 0
	case "G", "end":
		m.cursor[m.panel] = 1 << 30
	case "tab", "shift+tab", "h", "l", "left", "right":
		m.panel ^= 1
	case "enter", " ":
		m.focused = false
		return m, m.activate(m.panel, m.cursor[m.panel], false)
	case "r":
		m.readPrefix()
		return m, m.poll()
	case "?":
		return m, m.openHelp()
	}
	if len(k) == 1 {
		if k[0] >= '1' && k[0] <= '9' { // hotkeys also park the cursor so the rail shows what was picked
			m.focused = false
			m.panel, m.cursor[panelAgents] = panelAgents, int(k[0]-'1')
			m.clamp()
			return m, m.activate(panelAgents, int(k[0]-'1'), false)
		}
		if i := strings.IndexByte("!@#$%^&*(", k[0]); i >= 0 {
			m.focused = false
			m.panel, m.cursor[panelSpaces] = panelSpaces, i
			m.clamp()
			return m, m.activate(panelSpaces, i, false)
		}
	}
	m.clamp()
	return m, nil
}

func (m Model) onMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		m.cursor[m.panel]--
	case msg.Button == tea.MouseButtonWheelDown:
		m.cursor[m.panel]++
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		if p, i, ok := m.rowAt(msg.Y); ok {
			// tmux made this pane active before delivering the click; a click opens the row but
			// keeps the keyboard here so j/k work next. Enter (or esc) hands it to the work pane.
			m.focused = true
			m.panel, m.cursor[p] = p, i
			return m, m.activate(p, i, true)
		}
		return m, nil
	default:
		return m, nil
	}
	m.clamp()
	return m, nil
}

// activate switches the inner client to the selected row; unless keepFocus, outer focus then
// moves to the work pane.
func (m Model) activate(panel, idx int, keepFocus bool) tea.Cmd {
	inner, tty, outer, right, onSwitch := m.d.Inner, m.snap.Focus.ClientTTY, m.d.Outer, m.d.RightPane, m.d.OnSwitch
	var sess, win, pane string
	switch panel {
	case panelSpaces:
		if idx < 0 || idx >= len(m.snap.Spaces) {
			return nil
		}
		sess = m.snap.Spaces[idx].SessionID
	default:
		if idx < 0 || idx >= len(m.snap.Agents) {
			return nil
		}
		a := m.snap.Agents[idx]
		sess, win, pane = a.SessionID, a.WindowID, a.PaneID
		m.tracker.MarkSeen(pane)
		if m.d.Store != nil {
			_ = m.d.Store.MarkSeen(pane, time.Now())
		}
	}
	return func() tea.Msg {
		err := nav.Go(inner, tty, sess, win, pane)
		if err == nil && !keepFocus && outer != nil && right != "" {
			_, _ = outer.Run("select-pane", "-t", right)
		}
		if err == nil && onSwitch != nil && pane != "" {
			onSwitch(pane)
		}
		return switchedMsg{err}
	}
}

type helpPopupFailedMsg struct{ err error }

// openHelp shows the keybinds help. Inside the outer server it is a tmux popup over the whole
// window (the same one `prefix ?` opens); without an outer it is drawn inline in the pane.
func (m *Model) openHelp() tea.Cmd {
	outer := m.d.Outer
	exe, err := os.Executable()
	if outer == nil || err != nil {
		m.openHelpInline()
		return nil
	}
	return func() tea.Msg {
		_, err := outer.Run("display-popup", "-E", "-w", "80%", "-h", "85%", "-b", "rounded", "-T", " keybinds ",
			"-e", "FLOK_OUTER=1", "'"+exe+"' keys")
		if err != nil {
			return helpPopupFailedMsg{err}
		}
		return nil
	}
}

// openHelpInline builds the keybinds overlay from the inner server's live bindings.
func (m *Model) openHelpInline() {
	bindings, prefix, err := keys.Collect(m.d.Inner, m.d.Cfg.Keys.Tables)
	if err != nil {
		m.errText = err.Error()
		return
	}
	secs := keys.Organize(bindings, prefix, "", m.d.Cfg.Keys.Labels, m.d.Cfg.Keys.ShowMouse)
	h := NewHelp(m.theme, secs, false)
	h.width, h.height = m.width, m.height
	m.help = &h
}

func (m Model) focusRight() tea.Cmd {
	outer, right := m.d.Outer, m.d.RightPane
	if outer == nil || right == "" {
		return nil
	}
	return func() tea.Msg { _, err := outer.Run("select-pane", "-t", right); return switchedMsg{err} }
}

// clamp keeps cursors inside their lists and offsets such that the cursor row is visible.
func (m *Model) clamp() {
	lens := [2]int{len(m.snap.Spaces), len(m.snap.Agents)}
	lay := m.layout()
	rows := [2]int{lay.spacesRows, lay.agentsRows}
	for p := 0; p < 2; p++ {
		if m.cursor[p] >= lens[p] {
			m.cursor[p] = lens[p] - 1
		}
		if m.cursor[p] < 0 {
			m.cursor[p] = 0
		}
		if rows[p] <= 0 {
			continue
		}
		if m.cursor[p] < m.offset[p] {
			m.offset[p] = m.cursor[p]
		}
		if m.cursor[p] >= m.offset[p]+rows[p] {
			m.offset[p] = m.cursor[p] - rows[p] + 1
		}
		if m.offset[p] < 0 {
			m.offset[p] = 0
		}
	}
}

func elapsed(since time.Time, now time.Time) string {
	if since.IsZero() {
		return ""
	}
	d := now.Sub(since).Round(time.Second)
	if d < 0 {
		d = 0
	}
	h, mnt, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, mnt, s)
	}
	return fmt.Sprintf("%d:%02d", mnt, s)
}
