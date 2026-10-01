package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/keys"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/nav"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/remote/proto"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/tmux"
)

// Remote hosts in the sidebar. The local poller stays the authority for the local server; every
// remote host reports its own merged view through remote.Manager, and merge.Federate joins them
// above the local merge (never re-running a host's Build for another host's change). The
// servers panel lists local first, then the registry in its order; the sessions panel shows the
// front host (whose work pane sits next to the sidebar); the agents panel shows every host.

// hostView is what the sidebar knows about one remote host.
type hostView struct {
	state   remote.State
	detail  string
	retryAt time.Time
	hello   *proto.Hello
	snap    merge.Snapshot // its last merged view while connected
	hasSnap bool
	gen     uint64 // generation of the connection the view came from (remote.Msg.Gen)
}

type (
	remoteMsg struct{ remote.Msg }
	hostsMsg  struct {
		set hosts.Set
		err error
	}
	// frontMsg reports a finished swap: host's work pane (pane) is now next to the sidebar.
	frontMsg struct {
		host, pane string
		err        error
	}
	hostPanesMsg  struct{ err error }
	hostToggleMsg struct{ err error }
)

const panelHosts = 2

func (m Model) multiHost() bool { return len(m.hostList) > 0 }

// panels is the panel order, top to bottom.
func (m Model) panels() []int {
	if m.multiHost() {
		return []int{panelHosts, panelSpaces, panelAgents}
	}
	return []int{panelSpaces, panelAgents}
}

// cyclePanel moves the keyboard to the next (delta 1) or previous panel.
func (m *Model) cyclePanel(delta int) {
	ps := m.panels()
	cur := 0
	for i, p := range ps {
		if p == m.panel {
			cur = i
		}
	}
	m.panel = ps[((cur+delta)%len(ps)+len(ps))%len(ps)]
}

// hostRowCount is the number of servers-panel rows: local plus the registry.
func (m Model) hostRowCount() int {
	if !m.multiHost() {
		return 0
	}
	return 1 + len(m.hostList)
}

// hostAt is the host of a servers-panel row ("" = local).
func (m Model) hostAt(i int) (string, bool) {
	switch {
	case i == 0:
		return "", true
	case i > 0 && i-1 < len(m.hostList):
		return m.hostList[i-1].Name, true
	}
	return "", false
}

func hostLabel(host string) string {
	if host == "" {
		return agent.LocalHost
	}
	return host
}

// waitRemote delivers the next manager message.
func (m Model) waitRemote() tea.Cmd {
	if m.sink == nil {
		return nil
	}
	ch := m.sink
	return func() tea.Msg { return remoteMsg{<-ch} }
}

// loadHosts reads the registry off the loop; the store watcher fires on hosts.json changes.
func (m Model) loadHosts() tea.Cmd {
	if m.d.Store == nil {
		return nil
	}
	dir := m.d.Store.Dir
	return func() tea.Msg { set, err := hosts.Load(dir); return hostsMsg{set, err} }
}

// applyHosts reconciles the registry: manager connections, parked panes, the views it keeps.
func (m *Model) applyHosts(set hosts.Set) tea.Cmd {
	if m.hostsApplied && reflect.DeepEqual(set.Hosts, m.hostList) {
		return nil
	}
	m.hostsApplied = true
	prev := m.hostSet
	m.hostSet, m.hostList = set, set.Hosts
	for _, h := range set.Hosts { // a host that reconnects reads connecting until the new connection speaks
		if old, ok := prev.Get(h.Name); ok && h.Enabled && hosts.ConnChanged(old, h) {
			m.remotes[h.Name] = hostView{state: remote.Connecting, gen: m.remotes[h.Name].gen}
		}
	}
	if m.remote != nil {
		m.remote.Apply(set)
	}
	for _, h := range set.Hosts { // a changed target/socket/session/term needs a fresh attach pane
		if old, ok := prev.Get(h.Name); ok && hosts.AttachChanged(old, h) {
			m.restartPanes = append(m.restartPanes, h.Name)
		}
	}
	for name := range m.remotes {
		if h, ok := set.Get(name); !ok || !h.Enabled {
			delete(m.remotes, name)
		}
	}
	var cmds []tea.Cmd
	if m.front != "" {
		h, ok := set.Get(m.front)
		if !ok || !h.Enabled || m.paneRestarts(m.front) { // the front host went away or changes its attach: local comes back first
			cmds = append(cmds, m.swapCmd("", nil))
		}
	}
	cmds = append(cmds, m.hostPanesCmd())
	m.syncVisible()
	m.refederate()
	m.clamp()
	return batch(cmds...)
}

// paneRestarts says whether host's parked pane must be rebuilt (its attach changed).
func (m Model) paneRestarts(host string) bool {
	for _, n := range m.restartPanes {
		if n == host {
			return true
		}
	}
	return false
}

// hostPanesCmd creates the parked panes of enabled hosts, rebuilds those whose attach changed
// and removes those of the others (a host still in front waits for the swap back to local, see
// frontMsg).
func (m *Model) hostPanesCmd() tea.Cmd {
	outer, sess, bin, front, set := m.d.Outer, m.d.Cfg.Outer.Session, m.d.Bin, m.front, m.hostSet
	if outer == nil || bin == "" || !m.hostsApplied {
		return nil
	}
	var restart, keep []string
	for _, n := range m.restartPanes {
		if n == front {
			keep = append(keep, n) // after the swap back to local
		} else {
			restart = append(restart, n)
		}
	}
	m.restartPanes = keep
	return func() tea.Msg {
		var firstErr error
		for _, n := range restart {
			if err := launcher.KillHostPane(outer, n); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", n, err)
			}
		}
		for _, h := range set.Enabled() {
			if _, err := launcher.EnsureHostPane(outer, sess, bin, h.Name); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", h.Name, err)
			}
		}
		if rt, err := launcher.ReadRuntime(); err == nil {
			for name := range rt.Hosts {
				if h, ok := set.Get(name); (!ok || !h.Enabled) && name != front {
					if err := launcher.KillHostPane(outer, name); err != nil && firstErr == nil {
						firstErr = fmt.Errorf("%s: %w", name, err)
					}
				}
			}
		}
		return hostPanesMsg{firstErr}
	}
}

// syncVisible tells every host whether the user can see its work pane: it is in front and the
// terminal window is focused. Hosts use it for done/idle and sound suppression.
func (m Model) syncVisible() {
	if m.remote == nil {
		return
	}
	for _, h := range m.hostList {
		m.remote.SetVisible(h.Name, h.Name == m.front && !m.unfocused)
	}
}

// refederate joins the local merge and the connected hosts into the view: m.fed is the whole
// (published), m.snap what the panels render, with the sessions of the front host only.
func (m *Model) refederate() {
	var remotes []merge.HostSnapshot
	for _, h := range m.hostList {
		if v, ok := m.remotes[h.Name]; ok && v.hasSnap {
			remotes = append(remotes, merge.HostSnapshot{Host: h.Name, Snap: v.snap})
		}
	}
	m.fed = merge.Federate(m.local, remotes, m.front)
	m.applyNames()
	m.snap = m.fed
	if m.multiHost() {
		var sp []agent.Space
		for _, s := range m.fed.Spaces {
			if s.Host == m.front {
				sp = append(sp, s)
			}
		}
		m.snap.Spaces = sp
		// a host that cannot deliver says why in the footer (the row only has room for the
		// state); one without flok, or with one too old, says how to fix that from here
		for _, h := range m.hostList {
			v, ok := m.remotes[h.Name]
			if !ok || !h.Enabled {
				continue
			}
			switch v.state {
			case remote.Connected, remote.Stale, remote.Connecting, remote.Disabled, "":
			case remote.NoFlok:
				m.snap.Warnings = append(m.snap.Warnings, h.Name+": no flok there · I installs it (flok host install "+h.Name+")")
			case remote.OldFlok, remote.Incompatible:
				d := v.detail
				if d == "" {
					d = v.state.Label()
				}
				m.snap.Warnings = append(m.snap.Warnings, h.Name+": "+d+" · I installs this flok")
			default:
				if v.detail != "" {
					m.snap.Warnings = append(m.snap.Warnings, h.Name+": "+v.detail)
				}
			}
			// a host whose flok cannot relay keys says so too: nothing else would
			if m.d.Cfg.Hosts.Keys && v.state == remote.Connected && v.hello != nil && !v.hello.Has(proto.FeatureKeys) {
				m.snap.Warnings = append(m.snap.Warnings, h.Name+": upgrade flok there, no key relay ("+strings.TrimSpace(versionWord(v.hello.Version))+") · I")
			}
		}
	}
	if len(m.d.Notices) > 0 { // after the hosts' warnings; a fresh slice, m.fed is what gets published
		m.snap.Warnings = append(append([]string(nil), m.snap.Warnings...), m.d.Notices...)
	}
	m.vc.valid = false
}

// onRemote applies one manager message: a sound to play here, a host's new view, or a
// connection state.
func (m *Model) onRemote(msg remote.Msg) {
	v := m.remotes[msg.Host]
	if msg.Gen < v.gen { // a replaced connection's last words
		return
	}
	v.gen = msg.Gen
	changed := false
	if msg.Event != nil {
		m.p.PlaySound(agent.PaneRef{Host: msg.Host, ID: msg.Event.Pane}.String(), msg.Event.Kind)
	}
	if msg.Request != "" { // a flok key pressed inside that host's tmux
		m.debugf("%s: key %s", msg.Host, msg.Request)
		m.runKeyCommand(msg.Request)
	}
	if msg.Snap != nil {
		v.snap, v.hasSnap, changed = msg.Snap.ToMerge(), true, true
	}
	if msg.Event == nil && msg.Snap == nil && msg.Request == "" { // a state transition
		changed = v.state != msg.State || v.detail != msg.Detail
		v.state, v.detail, v.retryAt = msg.State, msg.Detail, msg.RetryAt
		if msg.Hello != nil {
			v.hello = msg.Hello
		}
		if msg.State != remote.Connected && msg.State != remote.Stale { // its agents leave with it
			v.snap, v.hasSnap = merge.Snapshot{}, false
		}
	}
	m.remotes[msg.Host] = v
	if changed {
		m.refederate()
		m.publish()
		m.clamp()
	}
}

// retryCountdown reports whether a host row shows a countdown, which the 1 s tick must redraw.
func (m Model) retryCountdown() bool {
	for _, v := range m.remotes {
		if !v.retryAt.IsZero() && v.state != remote.Connected && v.state != remote.Stale && v.state != remote.Connecting {
			return true
		}
	}
	return false
}

// swapMu serializes swaps: two in flight (a key and a request, say) must not both act on the
// same idea of which pane is in front.
var swapMu sync.Mutex

// siblingPane picks the other pane of the sidebar's window from list-panes output.
func siblingPane(out, sidebar string) string {
	for _, id := range strings.Fields(out) {
		if id != sidebar {
			return id
		}
	}
	return ""
}

// observedRight asks tmux which pane sits next to the sidebar; fallback when it cannot say.
func observedRight(outer tmux.Client, sidebar, fallback string) string {
	if outer == nil || sidebar == "" {
		return fallback
	}
	out, err := outer.Run("list-panes", "-t", sidebar, "-F", "#{pane_id}")
	if err != nil {
		return fallback
	}
	if sib := siblingPane(out, sidebar); sib != "" {
		return sib
	}
	return fallback
}

// swapCmd brings host's work pane next to the sidebar. swap-pane moves only the two work
// panes, so the sidebar never resizes or re-pins; a zoomed window (flok hide) is un-zoomed
// around the swap and zoomed again on the new pane. The pane it swaps out is the one tmux
// reports next to the sidebar at that moment, and which host owns it comes from runtime.json,
// so a stale idea of the front never swaps the wrong pane. then runs afterwards with the new
// front pane (a goto on that host, focusing it) and its error is reported.
func (m Model) swapCmd(host string, then func(pane string) error) tea.Cmd {
	outer, right, sess, bin, sidebar, cur := m.d.Outer, m.d.RightPane, m.d.Cfg.Outer.Session, m.d.Bin, m.d.SidebarPane, m.front
	if host == cur {
		if then == nil {
			return nil
		}
		return func() tea.Msg { return switchedMsg{then(right)} }
	}
	if outer == nil || right == "" {
		return nil
	}
	return func() tea.Msg {
		swapMu.Lock()
		defer swapMu.Unlock()
		wp, err := launcher.ScanWorkPanes(outer) // panes by what they run, not by where they sit
		if err != nil {
			return frontMsg{host, "", err}
		}
		right = observedRight(outer, sidebar, right)
		cur = ""
		for name, p := range wp.Hosts {
			if p == right {
				cur = name
			}
		}
		target := wp.Local
		if host != "" {
			target = wp.Hosts[host]
			if target == "" {
				hp, err := launcher.EnsureHostPane(outer, sess, bin, host)
				if err != nil {
					return frontMsg{host, "", err}
				}
				target = hp.Pane
			}
		}
		if target == "" {
			return frontMsg{host, "", errors.New("no work pane for " + hostLabel(host))}
		}
		if target != right {
			zoomed, _ := tmux.Display(outer, right, "#{window_zoomed_flag}")
			var args []string
			if zoomed == "1" {
				args = append(args, "resize-pane", "-Z", "-t", right, ";")
			}
			args = append(args, "swap-pane", "-d", "-s", target, "-t", right)
			if zoomed == "1" {
				args = append(args, ";", "resize-pane", "-Z", "-t", target)
			}
			if _, err := outer.Run(args...); err != nil {
				return frontMsg{host, "", err}
			}
		}
		_ = launcher.UpdateRuntime(func(r *launcher.Runtime) {
			if wp.Local != "" {
				r.LocalPane = wp.Local
			}
			if r.FrontHost != host {
				r.PreviousFront = hostLabel(r.FrontHost)
			}
			r.RightPane, r.FrontHost = target, host
		})
		if then != nil {
			err = then(target)
		}
		return frontMsg{host, target, err}
	}
}

// resyncFront follows the pane tmux really shows next to the sidebar when it is not the one the
// sidebar believes (a raced swap, a stray swap-pane, a work pane that died): the front host and
// runtime.json are corrected, or the local pane is brought back when the sidebar is alone.
func (m *Model) resyncFront(actual string) tea.Cmd {
	if actual == m.d.RightPane || !m.hostsApplied {
		return nil
	}
	var wp launcher.WorkPanes
	err := errors.New("no outer")
	if m.d.Outer != nil {
		wp, err = launcher.ScanWorkPanes(m.d.Outer)
	}
	if err != nil {
		rt, rerr := launcher.ReadRuntime()
		if rerr != nil {
			return nil
		}
		wp = launcher.WorkPanes{Local: rt.LocalPane, Hosts: map[string]string{}}
		for name, hp := range rt.Hosts {
			wp.Hosts[name] = hp.Pane
		}
	}
	if actual == "" { // nothing next to the sidebar: the work pane went away
		return m.recoverCmd(wp.Local)
	}
	host, known := "", actual == wp.Local
	for name, p := range wp.Hosts {
		if p == actual {
			host, known = name, true
		}
	}
	if !known {
		return nil // not one of ours: leave it alone
	}
	m.debugf("front resync: %s (%s) was %s (%s)", hostLabel(host), actual, hostLabel(m.front), m.d.RightPane)
	m.front, m.d.RightPane = host, actual
	_ = launcher.UpdateRuntime(func(r *launcher.Runtime) { r.RightPane, r.FrontHost = actual, host })
	m.syncVisible()
	m.refederate()
	m.publish()
	m.clamp()
	return nil
}

// recoverCmd puts the local work pane back next to a sidebar left alone in its window.
func (m Model) recoverCmd(local string) tea.Cmd {
	outer, sidebar := m.d.Outer, m.d.SidebarPane
	if outer == nil || sidebar == "" || local == "" {
		return nil
	}
	return func() tea.Msg {
		swapMu.Lock()
		defer swapMu.Unlock()
		if observedRight(outer, sidebar, "") != "" {
			return nil // something arrived meanwhile
		}
		if _, err := outer.Run("join-pane", "-d", "-h", "-s", local, "-t", sidebar, ";", "select-layout", "-t", sidebar, "main-vertical"); err != nil {
			return frontMsg{"", "", err}
		}
		_ = launcher.UpdateRuntime(func(r *launcher.Runtime) { r.RightPane, r.FrontHost = local, "" })
		return frontMsg{"", local, nil}
	}
}

// gotoCmd moves to a session/pane on host: through the local client, a goto frame (full) or
// switch-client over ssh (plain), after bringing that host's work pane to the front when
// another one is there. The pane is marked seen on its host.
func (m Model) gotoCmd(host, sess, win, pane string, keepFocus bool) tea.Cmd {
	inner, tty, outer, onSwitch, rem := m.d.Inner, m.local.Focus.ClientTTY, m.d.Outer, m.d.OnSwitch, m.remote
	if pane != "" {
		if host == "" {
			m.p.MarkSeen(pane)
		} else if rem != nil {
			rem.MarkSeen(host, pane)
		}
	}
	remoteTTY, mode := "", hosts.ModeFull
	if v, ok := m.remotes[host]; ok {
		remoteTTY = v.snap.Focus.ClientTTY
	}
	if h, ok := m.hostSet.Get(host); ok {
		mode = h.Mode
	}
	do := func(front string) error {
		var err error
		switch {
		case host == "":
			err = nav.Go(inner, tty, sess, win, pane)
		case rem == nil:
			err = errors.New("no remote hosts here")
		case mode == hosts.ModePlain:
			if c := rem.Client(host); c == nil {
				err = errors.New(host + " is not connected")
			} else {
				err = nav.Go(c, remoteTTY, sess, win, pane)
			}
		default:
			err = rem.Goto(host, sess, win, pane)
		}
		if err == nil && !keepFocus && outer != nil && front != "" {
			_, _ = outer.Run("select-pane", "-t", front)
		}
		if err == nil && onSwitch != nil && pane != "" && host == "" {
			onSwitch(pane)
		}
		return err
	}
	if host != m.front {
		return m.swapCmd(host, do)
	}
	right := m.d.RightPane
	return func() tea.Msg { return switchedMsg{do(right)} }
}

// hostKey runs a servers-panel key on the selected row: c connect, d disconnect, r reconnect
// now, m flip the mode, x remove (after y/n), i details. The registry keys go through
// hosts.json like the CLI; the file change comes back as a hostsMsg and the manager follows.
func (m Model) hostKey(k, host string) (tea.Model, tea.Cmd) {
	if host == "" && k != "i" {
		return m, nil
	}
	switch k {
	case "c":
		return m, m.hostEnabledCmd(host, true)
	case "d":
		return m, m.hostEnabledCmd(host, false)
	case "r":
		if m.remote == nil {
			return m, nil
		}
		rem := m.remote
		return m, func() tea.Msg { rem.Reconnect(host); return nil }
	case "m":
		return m, m.hostEditCmd(host, func(h *hosts.Host) {
			if h.Mode == hosts.ModePlain {
				h.Mode = hosts.ModeFull
			} else {
				h.Mode = hosts.ModePlain
			}
		})
	case "x":
		m.confirmRemove = host
		return m, nil
	case "i":
		m.openHostInfo(host)
		return m, nil
	case "I": // put this flok on the host, or upgrade it: a popup over the outer shows the progress
		return m, m.runFlok("host", "install", host, "--open")
	}
	return m, nil
}

// runFlok runs `flok <args…>` detached from this process, against this outer; a start error
// goes to the footer.
func (m Model) runFlok(args ...string) tea.Cmd {
	if m.d.Bin == "" {
		return nil
	}
	bin := m.d.Bin
	return func() tea.Msg {
		if err := startDetached(bin, args...); err != nil {
			return hostToggleMsg{err}
		}
		return nil
	}
}

// startDetached starts a command in its own session (it must not hold the sidebar's terminal or
// die with it); tests replace it.
var startDetached = func(bin string, args ...string) error {
	c := exec.Command(bin, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }()
	return nil
}

func (m Model) hostEnabledCmd(host string, on bool) tea.Cmd {
	if m.d.Store == nil {
		return nil
	}
	dir := m.d.Store.Dir
	return func() tea.Msg {
		_, err := hosts.Update(dir, func(s *hosts.Set) error {
			if !s.SetEnabled(host, on) {
				return errors.New("no host " + host)
			}
			return nil
		})
		return hostToggleMsg{err}
	}
}

// hostEditCmd applies a validated edit to a host in the registry.
func (m Model) hostEditCmd(host string, edit func(*hosts.Host)) tea.Cmd {
	if m.d.Store == nil {
		return nil
	}
	dir := m.d.Store.Dir
	return func() tea.Msg {
		_, err := hosts.Update(dir, func(s *hosts.Set) error { return s.Set(host, edit) })
		return hostToggleMsg{err}
	}
}

// removeHostCmd forgets a host and prunes its local cache, like `flok host remove`.
func (m Model) removeHostCmd(host string) tea.Cmd {
	if m.d.Store == nil {
		return nil
	}
	dir := m.d.Store.Dir
	return func() tea.Msg {
		_, err := hosts.Update(dir, func(s *hosts.Set) error {
			if !s.Remove(host) {
				return errors.New("no host " + host)
			}
			return nil
		})
		if err == nil {
			_ = os.RemoveAll(hosts.Dir(dir, host))
		}
		return hostToggleMsg{err}
	}
}

// openHostInfo shows a host's details in the help overlay.
func (m *Model) openHostInfo(host string) {
	h := NewHelp(m.theme, []keys.Section{m.hostInfoSection(host)}, false)
	h.width, h.height = m.width, m.height
	m.help = &h
}

// hostInfoSection is what `i` shows on a servers row.
func (m Model) hostInfoSection(host string) keys.Section {
	row := func(k, v string) keys.Binding { return keys.Binding{Key: k, Label: v} }
	if host == "" {
		return keys.Section{Name: "local", Bindings: []keys.Binding{
			row("tmux", m.d.Cfg.Inner.Socket+" (this machine)"),
			row("sessions", strconv.Itoa(len(m.local.Spaces))),
			row("agents", fmt.Sprintf("%d · %d waiting", len(m.local.Agents), m.local.Unseen)),
		}}
	}
	h, _ := m.hostSet.Get(host)
	v := m.remotes[host]
	st := v.state
	switch {
	case !h.Enabled:
		st = remote.Disabled
	case st == "":
		st = remote.Connecting
	}
	state := st.Label()
	if v.detail != "" {
		state += " · " + v.detail
	}
	rows := []keys.Binding{row("target", h.Target), row("mode", string(h.Mode)), row("state", state)}
	if hint := st.Hint(h); hint != "" {
		rows = append(rows, row("hint", hint))
	}
	if v.hello != nil {
		if v.hello.Version != "" && v.hello.Version != "plain" {
			rows = append(rows, row("flok", strings.TrimPrefix(v.hello.Version, "v")+" (protocol "+strconv.Itoa(v.hello.Proto)+")"))
		}
		if v.hello.TmuxVersion != "" {
			rows = append(rows, row("tmux", v.hello.TmuxVersion))
		}
		if v.hello.Hostname != "" && v.hello.Hostname != h.Target {
			rows = append(rows, row("hostname", v.hello.Hostname))
		}
	}
	if v.hasSnap {
		rows = append(rows, row("sessions", strconv.Itoa(len(v.snap.Spaces))),
			row("agents", fmt.Sprintf("%d · %d waiting", len(v.snap.Agents), v.snap.Unseen)))
	}
	for _, kv := range [][2]string{{"socket", h.Socket}, {"session", h.Session}, {"term", h.Term}, {"flok path", h.Flok}} {
		if kv[1] != "" {
			rows = append(rows, row(kv[0], kv[1]))
		}
	}
	if m.d.Cfg.Hosts.Keys {
		keysRow, prefixRow := "prefix b B g o a A u N P O S F1-F9 bound there while connected", ""
		if m.d.Cfg.Hosts.Prefix && m.prefixTmux != "" {
			prefixRow = m.prefixTmux + " there too while connected (its own moves to prefix2)"
		}
		if v.hello != nil {
			ver := versionWord(v.hello.Version)
			switch {
			case !v.hello.Has(proto.FeatureKeys):
				keysRow, prefixRow = "none: flok"+ver+" there predates the key relay, upgrade it", ""
			case prefixRow != "" && !v.hello.Has(proto.FeaturePrefix):
				prefixRow = "the host's own: flok" + ver + " there predates prefix mirroring, upgrade it"
			}
		}
		rows = append(rows, row("keys", keysRow))
		if prefixRow != "" {
			rows = append(rows, row("prefix", prefixRow))
		}
	}
	if !h.AddedAt.IsZero() {
		rows = append(rows, row("added", h.AddedAt.Local().Format("2006-01-02 15:04")))
	}
	if !h.LastConnected.IsZero() {
		rows = append(rows, row("last connected", h.LastConnected.Local().Format("2006-01-02 15:04")))
	}
	return keys.Section{Name: "host " + host, Bindings: rows}
}

// notifyFront flashes the name of the server that just came to the front on its own status
// line (the one visible in the work pane), so a key-driven switch is confirmed where the eyes are.
func (m Model) notifyFront(host string) tea.Cmd {
	text := "flok: now on " + hostLabel(host)
	if host != "" {
		if m.remote == nil {
			return nil
		}
		rem := m.remote
		return func() tea.Msg { rem.Notify(host, text); return nil }
	}
	inner, tty := m.d.Inner, m.local.Focus.ClientTTY
	if inner == nil {
		return nil
	}
	return func() tea.Msg {
		args := []string{"display-message"}
		if tty != "" {
			args = append(args, "-c", tty)
		}
		_, _ = inner.Run(append(args, text)...)
		return nil
	}
}

// runKeyCommand runs a flok key pressed inside a host's tmux (or filed with `flok relay`) as
// the local binding would: `flok <cmd>` against this outer, detached.
func (m Model) runKeyCommand(cmd string) {
	if m.d.Bin == "" || !remote.IsKeyCommand(cmd) {
		return
	}
	if err := startDetached(m.d.Bin, strings.Fields(cmd)...); err != nil {
		m.debugf("key %s: %v", cmd, err)
	}
}

// hostRowParts describes a servers-panel row: name, state glyph and its colour, the detail
// column (agent count with pending, or the connection state) and its colour.
func (m Model) hostRowParts(i int) (name, glyph string, col lipgloss.Color, detail string, dcol lipgloss.Color) {
	t := m.theme
	host, _ := m.hostAt(i)
	name = hostLabel(host)
	counts := func(s merge.Snapshot) {
		best := agent.Unknown
		for _, a := range s.Agents {
			if best == agent.Unknown || agent.Priority(a.State) < agent.Priority(best) {
				best = a.State
			}
		}
		glyph, col = "○", t.Comment
		if len(s.Agents) > 0 {
			glyph, col = t.Glyph(best, m.frame), t.StateColor(best)
		}
		detail, dcol = strconv.Itoa(len(s.Agents)), t.Comment
		if s.Unseen > 0 {
			detail, dcol = fmt.Sprintf("%d · %d", len(s.Agents), s.Unseen), t.Orange
		}
	}
	if host == "" {
		counts(m.local)
		return
	}
	h, _ := m.hostSet.Get(host)
	v := m.remotes[host]
	st := v.state
	switch {
	case !h.Enabled:
		st = remote.Disabled
	case st == "":
		st = remote.Connecting
	}
	switch st {
	case remote.Connected:
		counts(v.snap)
	case remote.Stale:
		counts(v.snap)
		detail, dcol = "stale", t.Orange
	case remote.Connecting:
		glyph, col, detail, dcol = "◌", t.Comment, "connecting", t.Comment
	case remote.NoServer:
		glyph, col, detail, dcol = "○", t.Comment, st.Label(), t.Orange // "no tmux server"
	case remote.Disabled:
		glyph, col, detail, dcol = "○", t.Comment, "off", t.Comment
	default:
		glyph, col, detail, dcol = "✗", t.Red, st.Label(), t.Red
		if st == remote.Unreachable && !v.retryAt.IsZero() {
			if left := time.Until(v.retryAt).Round(time.Second); left > 0 {
				detail = fmt.Sprintf("retry in %ds", int(left.Seconds()))
			}
		}
	}
	return
}

// hostMode is the dim word after a remote host's name: "full" or "plain" ("" for local).
func (m Model) hostMode(host string) string {
	if h, ok := m.hostSet.Get(host); ok && host != "" {
		return string(h.Mode)
	}
	return ""
}

// hostRow renders one servers-panel row with the geometry of a session row: glyph, name, a dim
// mode word, then the state. When room runs out the mode word goes first, then the name is
// shortened; the state on the right is never cut before those.
func (m Model) hostRow(i, w int) string {
	t := m.theme
	name, glyph, col, detail, dcol := m.hostRowParts(i)
	host, _ := m.hostAt(i)
	tag := m.hostMode(host)
	sel := m.panel == panelHosts && m.cursor[panelHosts] == i
	base, lead := m.rowFrame(sel, host == m.front)
	nameStyle := base.Foreground(t.FG)
	if sel && m.focused {
		nameStyle = nameStyle.Foreground(t.Pink).Bold(true)
	} else if host == m.front {
		nameStyle = nameStyle.Bold(true)
	}
	avail := w - 3
	keep := ansi.StringWidth(name) // the name keeps at most 8 cells when something has to give
	if keep > 8 {
		keep = 8
	}
	tw := 0
	if tag != "" {
		tw = ansi.StringWidth(tag) + 1
	}
	dw := 0
	if detail != "" {
		if tw > 0 && ansi.StringWidth(detail) > avail-1-keep-tw { // no room for the mode word: the state wins
			tag, tw = "", 0
		}
		if max := avail - 1 - keep - tw; ansi.StringWidth(detail) > max {
			detail = ansi.Truncate(detail, max, "…")
		}
		dw = ansi.StringWidth(detail) + 1
		if avail-dw-tw < 4 {
			detail, dw = "", 0
		}
	}
	name = ansi.Truncate(name, avail-dw-tw, "…")
	gap := avail - dw - tw - ansi.StringWidth(name)
	if gap < 0 {
		gap = 0
	}
	row := lead + base.Foreground(col).Render(glyph) + base.Render(" ") + nameStyle.Render(name)
	if tag != "" {
		row += base.Render(" ") + base.Foreground(t.Comment).Render(tag)
	}
	row += base.Render(strings.Repeat(" ", gap))
	if detail != "" {
		row += base.Render(" ") + base.Foreground(dcol).Render(detail)
	}
	return pad(row, w, base)
}

// hostAbbrevs are the two-letter rail names of the servers rows (local first).
func (m Model) hostAbbrevs() []string {
	names := make([]string, 0, m.hostRowCount())
	for i := 0; i < m.hostRowCount(); i++ {
		host, _ := m.hostAt(i)
		names = append(names, hostLabel(host))
	}
	return abbrev(names)
}

// abbrev shortens names to two letters: the first two, on a collision the first and the last,
// and failing that the first letter and the row number.
func abbrev(names []string) []string {
	out := make([]string, len(names))
	used := map[string]int{}
	short := func(n string, mode int) string {
		r := []rune(strings.ToLower(n))
		switch {
		case len(r) == 0:
			return "??"
		case len(r) == 1:
			return string(r) + " "
		case mode == 0:
			return string(r[:2])
		}
		return string(r[0]) + string(r[len(r)-1])
	}
	for i, n := range names {
		out[i] = short(n, 0)
		used[out[i]]++
	}
	collide := make([]bool, len(names)) // decided before any label moves
	for i := range names {
		collide[i] = used[out[i]] > 1
	}
	for i, n := range names {
		if alt := short(n, 1); collide[i] && used[alt] == 0 {
			used[out[i]]--
			out[i], used[alt] = alt, 1
		}
	}
	for i := range names {
		collide[i] = used[out[i]] > 1
	}
	for i, n := range names {
		if collide[i] {
			used[out[i]]--
			out[i] = string([]rune(strings.ToLower(n))[0]) + strconv.Itoa(i%10)
		}
	}
	return out
}

// hostSummaries is the hosts block of snapshot.json.
func (m Model) hostSummaries() []snapshot.Host {
	if !m.multiHost() {
		return nil
	}
	var out []snapshot.Host
	for _, h := range m.hostList {
		v := m.remotes[h.Name]
		st := v.state
		switch {
		case !h.Enabled:
			st = remote.Disabled
		case st == "":
			st = remote.Connecting
		}
		out = append(out, snapshot.Host{Name: h.Name, Mode: string(h.Mode), State: string(st), Detail: v.detail,
			Agents: len(v.snap.Agents), Pending: v.snap.Unseen, Front: h.Name == m.front})
	}
	return out
}

// publishedSnapshot is what flok-bar and `flok status` read: every host's agents, tagged.
func (m Model) publishedSnapshot() snapshot.Snapshot {
	s := snapshot.FromMerge(m.fed)
	s.Hosts, s.FrontHost = m.hostSummaries(), m.front
	return s
}

// Close ends the remote connections (their serve sessions end on EOF).
func (m Model) Close() {
	if m.remote != nil {
		m.remote.Close()
	}
}

// versionWord is " 0.4.6" for a version, "" when the host did not say.
func versionWord(v string) string {
	if v = strings.TrimPrefix(v, "v"); v != "" {
		return " " + v
	}
	return ""
}
