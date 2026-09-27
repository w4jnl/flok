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
	"github.com/muesli/termenv"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/keys"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/notify"
	"github.com/w4jnl/flok/internal/poller"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/rules"
	"github.com/w4jnl/flok/internal/snapshot"
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
	Feat        tmux.Features       // what the tmux both servers run on can do (popups, …)
	// Awake takes the power assertion while the keep-awake marker is on; nil disables keep-awake.
	Awake func() (Releaser, error)
	// NewRemote builds the remote-host manager reporting to sink; nil = no remote hosts (tests).
	NewRemote func(sink chan<- remote.Msg) *remote.Manager
	Bin       string // this executable, for the parked host panes
}

const (
	panelSpaces = 0
	panelAgents = 1
)

type Model struct {
	d            Deps
	theme        Theme
	p            *poller.Poller // the state pipeline (polls, merge, sounds); shared by every Model copy
	snap         merge.Snapshot // what the panels render: the federated view, sessions of the front host only
	local        merge.Snapshot // the local poller's merge
	fed          merge.Snapshot // local and every connected host (published)
	remote       *remote.Manager
	sink         chan remote.Msg
	remotes      map[string]hostView // per remote host, keyed by name
	hostSet      hosts.Set           // the registry as last applied
	hostList     []hosts.Host
	hostsApplied bool
	front        string // host whose work pane is next to the sidebar; "" = local
	clientTTY    string
	width        int
	height       int
	panel        int
	cursor       [3]int // per panel: spaces, agents, hosts
	offset       [3]int
	frame        int
	animating    bool
	errText      string
	help         *HelpModel
	publisher    *snapshot.Publisher
	vc           *viewCache // rendered frame, reused while nothing changed
	polls        int        // 1 s polls so far (focus fallback cadence)
	repinPending bool       // a select-layout is scheduled after a resize (tmux < 3.3)
	unfocused    bool       // terminal-focus marker: the terminal window is not focused
	hidden       bool       // sidebar-hidden marker / window_zoomed_flag: the pane is not visible
	focused      bool       // the outer's active pane is the sidebar: keys arrive here
	dark         bool       // the dark palette is in use (see config.Theme.IsDark)
	themeRec     string     // terminal theme recorded by flok up ("dark", "light", "")
	// Inner tmux prefix, so chords typed while the sidebar has focus are replayed into the work
	// pane instead of being swallowed (prefixTmux "C-a", prefixKey "ctrl+a").
	prefixTmux    string
	prefixKey     string
	prefixPending bool
	debug         bool
	keep          *awakeHold // keep-awake: the power assertion, shared by every Model copy
}

type (
	tickMsg struct{}
	animMsg struct{}
	// snapshotMsg is a poll (or rebuild) of the pipeline plus what only the sidebar reads from
	// the outer server, each only when this poll checked it (see focusCheckEvery).
	snapshotMsg struct {
		poller.SnapshotMsg
		focus  *bool // outer active pane == sidebar
		zoomed *bool // outer window zoomed (flok hide)
	}
	switchedMsg     struct{ err error }
	repinMsg        struct{}
	stateChangedMsg struct{}
	registryTickMsg struct{}
	registryMsg     = poller.RegistryMsg
	screenTickMsg   struct{}
	screenMsg       = poller.ScreenMsg
)

// viewCache holds the last rendered frame; Bubble Tea calls View after every Update, and most
// updates (ticks, unchanged polls) change nothing on screen.
type viewCache struct {
	s     string
	valid bool
}

func New(d Deps) Model {
	lipgloss.SetColorProfile(termenv.TrueColor)
	themeRec := ""
	if d.Store != nil {
		themeRec, _ = d.Store.TerminalTheme()
	}
	dark := d.Cfg.Theme.IsDark(themeRec)
	m := Model{d: d, dark: dark, themeRec: themeRec, theme: NewTheme(d.Cfg.Theme.Resolve(dark)), clientTTY: d.ClientTTY, vc: &viewCache{},
		remotes: map[string]hostView{}}
	if d.Outer != nil { // a remote host may be in front (flok reload keeps the layout)
		if rt, err := launcher.ReadRuntime(); err == nil && rt.FrontHost != "" && rt.RightPane == d.RightPane {
			m.front = rt.FrontHost
			if rt.LocalPane != "" {
				d.RightPane = rt.LocalPane // the local client's tty comes from the local pane
			}
		}
	}
	if m.clientTTY == "" && d.Outer != nil && d.RightPane != "" {
		if tty, err := tmux.Display(d.Outer, d.RightPane, "#{pane_tty}"); err == nil {
			m.clientTTY = tty
		}
	}
	if d.NewRemote != nil {
		m.sink = make(chan remote.Msg, 256)
		m.remote = d.NewRemote(m.sink)
	}
	var sounder notify.Sounder = notify.Noop{}
	if d.Cfg.Sounds.Enabled && d.Cfg.Sounds.Player != "none" {
		player := notify.Player{Files: notify.Resolve(config.StateDir(), map[string]string{"done": d.Cfg.Sounds.Done, "blocked": d.Cfg.Sounds.Blocked, "error": d.Cfg.Sounds.Error}),
			Volume: d.Cfg.Sounds.Volume, Command: d.Cfg.Sounds.Command}
		tty := m.clientTTY // the outer's work pane: the bell travels through the outer server to the terminal
		sounder = notify.Compose(player, notify.Bell{Resolve: func() string { return tty }}, d.Cfg.Sounds.Bell)
	}
	m.debug = os.Getenv("FLOK_DEBUG") != ""
	m.p = poller.New(poller.Deps{Cfg: d.Cfg, Tmux: d.Inner, Store: d.Store, Registry: d.Registry, Rules: d.Rules,
		Adapters: d.Adapters, BranchOf: d.BranchOf, ClientTTY: m.clientTTY,
		Sound:                  func(_, kind string) { _ = sounder.Play(kind) },
		SoundHookNotifications: d.Cfg.Sounds.Player == "sidebar", Debugf: m.debugf})
	if d.Store != nil {
		m.publisher = &snapshot.Publisher{Dir: d.Store.Dir}
		m.keep = &awakeHold{hold: d.Awake}
	}
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

// repinAfterResize keeps the sidebar at its pinned width on tmux versions without the
// window-resized hook (< 3.3, RHEL 9): tmux scales every pane on a window resize, so the
// sidebar re-applies the main-vertical layout itself, debounced, when its width is neither the
// full nor the rail width. Newer servers do this in the outer config's hook.
func (m *Model) repinAfterResize(width int) tea.Cmd {
	if m.d.Feat.ResizedHook || m.d.Outer == nil || m.d.SidebarPane == "" || m.hidden || m.repinPending {
		return nil
	}
	if width == m.d.Cfg.Sidebar.Width || width == m.d.Cfg.Sidebar.RailWidth {
		return nil
	}
	m.repinPending = true
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return repinMsg{} })
}

// setTheme switches between the dark and the light palette and repaints (help overlay included).
func (m *Model) setTheme(dark bool) {
	m.dark = dark
	m.theme = NewTheme(m.d.Cfg.Theme.Resolve(dark))
	if m.help != nil {
		m.help.theme = m.theme
	}
	m.vc.valid = false
	m.debugf("theme dark=%v", dark)
}

// borderCmd recolours the outer server's pane border, which `flok up` rendered from the palette
// of the moment.
func (m Model) borderCmd() tea.Cmd {
	outer, col := m.d.Outer, "fg="+string(m.theme.CurrentLine)
	if outer == nil {
		return nil
	}
	return func() tea.Msg {
		_, _ = outer.Run("set", "-g", "pane-border-style", col, ";", "set", "-g", "pane-active-border-style", col)
		return nil
	}
}

func (m Model) waitChange() tea.Cmd {
	ch := m.p.Changes()
	if ch == nil {
		return nil
	}
	return func() tea.Msg { <-ch; return stateChangedMsg{} }
}

func (m Model) registryTick() tea.Cmd {
	if !m.p.RegistryEnabled() {
		return nil
	}
	return tea.Tick(m.p.RegistryInterval(), func(time.Time) tea.Msg { return registryTickMsg{} })
}

// registryCmd wraps a registry call of the pipeline as a command; nil when there is none.
func registryCmd(f func() poller.RegistryMsg) tea.Cmd {
	if f == nil {
		return nil
	}
	return func() tea.Msg { return f() }
}

func (m Model) pollRegistryIfDue() tea.Cmd { return registryCmd(m.p.PollRegistryIfDue()) }

// ClientTTY is the inner client the sidebar drives (for runtime.json).
func (m Model) ClientTTY() string { return m.clientTTY }

func (m Model) Init() tea.Cmd {
	return batch(m.poll(), m.tick(), m.waitChange(), m.pollRegistryIfEnabled(), m.registryTick(), m.screenTick(), m.loadHosts(), m.waitRemote())
}

func (m Model) screenTick() tea.Cmd {
	if !m.p.ScreenEnabled() {
		return nil
	}
	return tea.Tick(m.screenInterval(), func(time.Time) tea.Msg { return screenTickMsg{} })
}

// pollScreen captures the relevant panes and evaluates their manifests in the background.
func (m Model) pollScreen() tea.Cmd {
	f := m.p.PollScreen()
	return func() tea.Msg { return f() }
}

func (m Model) pollRegistryIfEnabled() tea.Cmd { return registryCmd(m.p.PollRegistry()) }

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.pollInterval(), func(time.Time) tea.Msg { return tickMsg{} })
}

// idle reports that nobody can see the sidebar: the work pane is zoomed over it (flok hide).
// While idle the spinner pauses and the polls stretch to idle_poll_ms; hook writes still
// rebuild immediately, so the menu bar stays current. An unfocused terminal window does not
// count: it is usually still on screen next to whatever has focus, and a frozen spinner there
// reads as a stall.
func (m Model) idle() bool { return m.hidden }

// pollInterval is the tmux snapshot cadence: poll_ms, stretched to idle_poll_ms while idle.
func (m Model) pollInterval() time.Duration { return m.p.PollInterval(m.idle()) }

// screenInterval is the capture-pane cadence, stretched the same way while idle.
func (m Model) screenInterval() time.Duration { return m.p.ScreenInterval(m.idle()) }

func (m Model) anim() tea.Cmd {
	ms := m.d.Cfg.Sidebar.SpinnerMs
	if ms < 100 {
		ms = 250
	}
	return tea.Tick(time.Duration(ms)*time.Millisecond, func(time.Time) tea.Msg { return animMsg{} })
}

// focusCheckEvery is how many 1 s polls pass between focus checks on the outer server. Focus
// normally arrives as tea.FocusMsg/BlurMsg (the outer has focus-events on, so tmux tells the
// pane when it becomes active); the exec is a fallback for headless or exotic setups.
const focusCheckEvery = 5

func (m Model) poll() tea.Cmd {
	outer, sb := m.d.Outer, m.d.SidebarPane
	// while hidden, check every poll (they are idle_poll_ms apart) so an un-hide that raced a poll
	// is noticed within one interval
	checkFocus := m.polls%focusCheckEvery == 0 || m.hidden
	inner := m.p.Poll()
	return func() tea.Msg {
		msg := snapshotMsg{SnapshotMsg: inner()}
		if checkFocus && outer != nil && sb != "" {
			// pane_active: keyboard-focus fallback; window_zoomed_flag: the work pane is zoomed over
			// us (flok hide), which overrules a stale sidebar-hidden marker
			v, e := outer.Run("display-message", "-p", "-t", sb, "#{pane_active} #{window_zoomed_flag}")
			f := strings.Fields(v)
			focus := e == nil && len(f) == 2 && f[0] == "1"
			msg.focus = &focus
			if e == nil && len(f) == 2 {
				zoomed := f[1] == "1"
				msg.zoomed = &zoomed
			}
		}
		return msg
	}
}

// rebuild re-runs the merge on the cached tmux snapshot without spawning tmux (see
// poller.Rebuild); before the first poll it polls.
func (m Model) rebuild(reloadStore bool) tea.Cmd {
	f := m.p.Rebuild(reloadStore)
	return func() tea.Msg { return snapshotMsg{SnapshotMsg: f()} }
}

// animCmd starts the spinner when an agent works and someone can see it.
func (m *Model) animCmd() tea.Cmd {
	if m.anyWorking() && !m.animating && !m.idle() {
		m.animating = true
		return m.anim()
	}
	return nil
}

// batch drops nil commands; an all-nil tea.Batch would still schedule an empty message.
func batch(cmds ...tea.Cmd) tea.Cmd {
	var out []tea.Cmd
	for _, c := range cmds {
		if c != nil {
			out = append(out, c)
		}
	}
	switch len(out) {
	case 0:
		return nil
	case 1:
		return out[0]
	}
	return tea.Batch(out...)
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
	switch msg.(type) { // timers only schedule work: nothing to redraw
	case tickMsg:
		m.polls++
		if m.retryCountdown() { // a host row counts down to its next attempt
			m.vc.valid = false
		}
		return m, tea.Batch(m.poll(), m.tick())
	case registryTickMsg:
		return m, tea.Batch(m.pollRegistryIfDue(), m.registryTick())
	case screenTickMsg:
		return m, tea.Batch(m.pollScreen(), m.screenTick())
	case animMsg:
		if !m.anyWorking() || m.idle() { // nothing spins, or nobody can see it: stop until the next poll
			m.animating = false
			return m, nil
		}
		m.frame++
		m.vc.valid = false
		return m, m.anim()
	}
	m.vc.valid = false
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clamp()
		return m, m.repinAfterResize(msg.Width)
	case repinMsg:
		m.repinPending = false
		outer, sb := m.d.Outer, m.d.SidebarPane
		if outer == nil || sb == "" || m.hidden {
			return m, nil
		}
		return m, func() tea.Msg { _, _ = outer.Run("select-layout", "-t", sb, "main-vertical"); return nil }
	case snapshotMsg:
		var keepChanged bool
		var keepErr error
		if msg.KeepAwake != nil {
			keepChanged, keepErr = m.keep.sync(*msg.KeepAwake)
		}
		if keepErr != nil {
			m.debugf("keep-awake: %v", keepErr)
		} else if keepChanged {
			m.debugf("keep-awake=%v", m.keep.on())
		}
		if m.keep.presenceMoved() {
			keepChanged = true
			m.debugf("keep-awake presence=%q", m.keep.presence())
		}
		if msg.Err != nil {
			m.errText = msg.Err.Error()
			if keepChanged {
				m.publish()
			}
			return m, nil
		}
		focusChanged := false
		if msg.focus != nil && *msg.focus != m.focused {
			m.focused, focusChanged = *msg.focus, true
		}
		themeChanged := false
		if dark := m.d.Cfg.Theme.IsDark(msg.Theme); dark != m.dark {
			m.setTheme(dark)
			themeChanged = true
		}
		m.themeRec = msg.Theme
		wasIdle := m.idle()
		if m.unfocused != msg.Unfocused {
			m.unfocused = msg.Unfocused
			m.syncVisible() // hosts stop marking done/idle for a pane nobody looks at
		}
		m.hidden = msg.Hidden
		if msg.zoomed != nil {
			m.hidden = *msg.zoomed
			if m.d.Store != nil && *msg.zoomed != msg.Hidden { // marker went stale (crash between zoom and write)
				_ = m.d.Store.SetSidebarHidden(*msg.zoomed)
			}
		}
		if wasIdle != m.idle() {
			m.debugf("idle=%v (unfocused=%v hidden=%v)", m.idle(), m.unfocused, m.hidden)
		}
		var cmds []tea.Cmd
		if themeChanged {
			cmds = append(cmds, m.borderCmd())
		}
		if wasIdle && !m.idle() && msg.Rebuilt { // someone is looking again: cached tmux data may be idle_poll_ms old
			cmds = append(cmds, m.poll())
		}
		merged := m.p.ApplySnapshot(msg.SnapshotMsg, m.errText != "") // a shown error forces a merge that clears it
		if msg.zoomed != nil {
			m.p.SetHidden(m.hidden) // ApplySnapshot cached the marker value; keep the corrected one
		}
		if !merged {
			m.vc.valid = !focusChanged && !themeChanged // identical inputs: keep the frame, skip the merge
			if keepChanged {
				m.publish()
			} else if m.publisher != nil { // the merge did not run, so keep flok-bar's liveness signal going
				_, _ = m.publisher.Heartbeat(time.Now())
			}
			return m, batch(append(cmds, m.animCmd())...)
		}
		m.errText = ""
		m.local = m.p.Snap()
		m.refederate()
		m.publish() // for flok-bar and other out-of-process readers
		m.clamp()
		return m, batch(append(cmds, m.animCmd())...)
	case switchedMsg:
		if msg.err != nil {
			m.errText = msg.err.Error()
		}
		return m, m.poll()
	case frontMsg: // a host's work pane came to the front
		if msg.err != nil {
			m.errText = msg.err.Error()
		}
		if msg.pane != "" {
			m.front, m.d.RightPane = msg.host, msg.pane
			m.debugf("front %s (%s)", hostLabel(msg.host), msg.pane)
			m.syncVisible()
			m.refederate()
			m.publish()
			m.clamp()
		}
		return m, batch(m.hostPanesCmd(), m.poll()) // a host that left while in front loses its pane now
	case hostsMsg: // hosts.json (re)read
		if msg.err != nil {
			m.errText = msg.err.Error()
			return m, nil
		}
		return m, m.applyHosts(msg.set)
	case remoteMsg:
		m.onRemote(msg.Msg)
		return m, m.waitRemote()
	case hostPanesMsg, hostToggleMsg:
		var err error
		switch v := msg.(type) {
		case hostPanesMsg:
			err = v.err
		case hostToggleMsg:
			err = v.err
		}
		if err != nil {
			m.errText = err.Error()
		}
		return m, nil
	case stateChangedMsg: // hook record, seen mark, hosts.json or a marker changed on disk
		return m, batch(m.rebuild(true), m.waitChange(), m.loadHosts())
	case registryTickMsg:
		return m, tea.Batch(m.pollRegistryIfDue(), m.registryTick())
	case registryMsg:
		if m.p.ApplyRegistry(msg) {
			return m, m.rebuild(false)
		}
		return m, nil
	case screenTickMsg:
		return m, tea.Batch(m.pollScreen(), m.screenTick())
	case screenMsg:
		if !m.p.ApplyScreen(msg) { // nothing was evaluated: not a sample
			return m, nil
		}
		return m, m.rebuild(false)
	case tea.FocusMsg: // tmux forwards pane focus (focus-events on in the outer)
		m.debugf("focus in")
		m.focused = true
		return m, nil
	case tea.BlurMsg:
		m.debugf("focus out")
		m.focused = false
		return m, nil
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
	case "tab", "l", "right":
		m.cyclePanel(1)
	case "shift+tab", "h", "left":
		m.cyclePanel(-1)
	case "enter", " ":
		m.focused = false
		return m, m.activate(m.panel, m.cursor[m.panel], false)
	case "c": // servers panel: connect / disconnect the selected host
		if m.panel == panelHosts {
			if host, ok := m.hostAt(m.cursor[panelHosts]); ok {
				return m, m.toggleHostCmd(host)
			}
		}
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

// activate opens the selected row: a servers row brings that host's work pane to the front, a
// session or agent row moves its host's client there (bringing the host to the front first when
// needed); unless keepFocus, outer focus then moves to the work pane.
func (m Model) activate(panel, idx int, keepFocus bool) tea.Cmd {
	switch panel {
	case panelHosts:
		host, ok := m.hostAt(idx)
		if !ok {
			return nil
		}
		outer := m.d.Outer
		return m.swapCmd(host, func(pane string) error {
			if !keepFocus && outer != nil && pane != "" {
				_, _ = outer.Run("select-pane", "-t", pane)
			}
			return nil
		})
	case panelSpaces:
		if idx < 0 || idx >= len(m.snap.Spaces) {
			return nil
		}
		sp := m.snap.Spaces[idx]
		return m.gotoCmd(sp.Host, sp.SessionID, "", "", keepFocus)
	default:
		if idx < 0 || idx >= len(m.snap.Agents) {
			return nil
		}
		a := m.snap.Agents[idx]
		return m.gotoCmd(a.Host, a.SessionID, a.WindowID, a.PaneID, keepFocus)
	}
}

type helpPopupFailedMsg struct{ err error }

// openHelp shows the keybinds help. Inside the outer server it is a tmux popup over the whole
// window (the same one `prefix ?` opens); without an outer it is drawn inline in the pane.
func (m *Model) openHelp() tea.Cmd {
	outer := m.d.Outer
	exe, err := os.Executable()
	// FLOK_OUTER makes `flok keys` read the inner server's bindings, not the outer's
	args := keys.PopupArgs(m.d.Feat, "", "env FLOK_OUTER=1 '"+exe+"' keys")
	if outer == nil || err != nil || args == nil { // headless, or a tmux without popups (< 3.2)
		m.openHelpInline()
		return nil
	}
	return func() tea.Msg {
		if _, err := outer.Run(args...); err != nil {
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
	lens := [3]int{len(m.snap.Spaces), len(m.snap.Agents), m.hostRowCount()}
	lay := m.layout()
	rows := [3]int{lay.spacesRows, lay.agentsRows, lay.hostsRows}
	if !m.multiHost() && m.panel == panelHosts {
		m.panel = panelSpaces
	}
	for p := 0; p < 3; p++ {
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
