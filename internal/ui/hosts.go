package ui

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/hosts"
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
	m.hostSet, m.hostList = set, set.Hosts
	if m.remote != nil {
		m.remote.Apply(set)
	}
	for name := range m.remotes {
		if h, ok := set.Get(name); !ok || !h.Enabled {
			delete(m.remotes, name)
		}
	}
	var cmds []tea.Cmd
	if m.front != "" {
		if h, ok := set.Get(m.front); !ok || !h.Enabled { // the front host went away: local comes back
			cmds = append(cmds, m.swapCmd("", nil))
		}
	}
	cmds = append(cmds, m.hostPanesCmd())
	m.syncVisible()
	m.refederate()
	m.clamp()
	return batch(cmds...)
}

// hostPanesCmd creates the parked panes of enabled hosts and removes those of the others (a
// host still in front is removed once the swap back to local landed, see frontMsg).
func (m Model) hostPanesCmd() tea.Cmd {
	outer, sess, bin, front, set := m.d.Outer, m.d.Cfg.Outer.Session, m.d.Bin, m.front, m.hostSet
	if outer == nil || bin == "" || !m.hostsApplied {
		return nil
	}
	return func() tea.Msg {
		var firstErr error
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
	m.snap = m.fed
	if m.multiHost() {
		var sp []agent.Space
		for _, s := range m.fed.Spaces {
			if s.Host == m.front {
				sp = append(sp, s)
			}
		}
		m.snap.Spaces = sp
	}
	m.vc.valid = false
}

// onRemote applies one manager message: a sound to play here, a host's new view, or a
// connection state.
func (m *Model) onRemote(msg remote.Msg) {
	v := m.remotes[msg.Host]
	changed := false
	if msg.Event != nil {
		m.p.PlaySound(agent.PaneRef{Host: msg.Host, ID: msg.Event.Pane}.String(), msg.Event.Kind)
	}
	if msg.Snap != nil {
		v.snap, v.hasSnap, changed = msg.Snap.ToMerge(), true, true
	}
	if msg.Event == nil && msg.Snap == nil { // a state transition
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

// swapCmd brings host's work pane next to the sidebar. swap-pane moves only the two work
// panes, so the sidebar never resizes or re-pins; a zoomed window (flok hide) is un-zoomed
// around the swap and zoomed again on the new pane. then runs afterwards with the new front
// pane (a goto on that host, focusing it) and its error is reported.
func (m Model) swapCmd(host string, then func(pane string) error) tea.Cmd {
	outer, right, sess, bin, cur := m.d.Outer, m.d.RightPane, m.d.Cfg.Outer.Session, m.d.Bin, m.front
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
		rt, err := launcher.ReadRuntime()
		if err != nil {
			return frontMsg{host, "", err}
		}
		target := rt.LocalPane
		if host != "" {
			hp, ok := rt.Hosts[host]
			if !ok {
				if hp, err = launcher.EnsureHostPane(outer, sess, bin, host); err != nil {
					return frontMsg{host, "", err}
				}
			}
			target = hp.Pane
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
			if r.LocalPane == "" && cur == "" {
				r.LocalPane = right // hosts added after flok up: the local pane is the one leaving
			}
			r.RightPane, r.FrontHost = target, host
		})
		if then != nil {
			err = then(target)
		}
		return frontMsg{host, target, err}
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

// toggleHostCmd flips a host's enabled flag in the registry; the file change comes back as a
// hostsMsg and the manager connects or disconnects.
func (m Model) toggleHostCmd(host string) tea.Cmd {
	if m.d.Store == nil || host == "" {
		return nil
	}
	dir := m.d.Store.Dir
	return func() tea.Msg {
		_, err := hosts.Update(dir, func(s *hosts.Set) error {
			h, ok := s.Get(host)
			if !ok {
				return errors.New("no host " + host)
			}
			s.SetEnabled(host, !h.Enabled)
			return nil
		})
		return hostToggleMsg{err}
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

// hostRow renders one servers-panel row with the geometry of a session row.
func (m Model) hostRow(i, w int) string {
	t := m.theme
	name, glyph, col, detail, dcol := m.hostRowParts(i)
	host, _ := m.hostAt(i)
	sel := m.panel == panelHosts && m.cursor[panelHosts] == i
	base := lipgloss.NewStyle()
	if host == m.front {
		base = base.Background(t.CurrentLine)
	}
	nameStyle := base.Foreground(t.FG)
	if sel && m.focused {
		nameStyle = nameStyle.Foreground(t.Pink).Bold(true)
	} else if host == m.front {
		nameStyle = nameStyle.Bold(true)
	}
	avail := w - 3
	dw := 0
	if detail != "" {
		if max := avail / 2; ansi.StringWidth(detail) > max {
			detail = ansi.Truncate(detail, max, "…")
		}
		dw = ansi.StringWidth(detail) + 1
		if avail-dw < 4 {
			detail, dw = "", 0
		}
	}
	name = ansi.Truncate(name, avail-dw, "…")
	gap := avail - dw - ansi.StringWidth(name)
	if gap < 0 {
		gap = 0
	}
	row := base.Render(" ") + base.Foreground(col).Render(glyph) + base.Render(" ") +
		nameStyle.Render(name) + base.Render(strings.Repeat(" ", gap))
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
