package ui

import (
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/state"
)

// requestsMsg carries the one-shot commands other flok processes filed for the sidebar.
type requestsMsg struct{ reqs []state.Request }

// requestMaxAge drops requests filed while no sidebar was there to act on them.
const requestMaxAge = 10 * time.Second

// drainRequests picks up the requests/ mailbox (the store watcher fires when a file lands).
func (m Model) drainRequests() tea.Cmd {
	if m.d.Store == nil {
		return nil
	}
	st := m.d.Store
	return func() tea.Msg {
		reqs := st.DrainRequests(time.Now(), requestMaxAge)
		if len(reqs) == 0 {
			return nil
		}
		return requestsMsg{reqs}
	}
}

// runRequest performs one request: goto moves to an agent pane on any host (bringing the host
// to the front first), front brings a host's work pane next to the sidebar.
func (m *Model) runRequest(r state.Request) tea.Cmd {
	if h, ok := m.hostSet.Get(r.Host); ok && r.Host != "" {
		r.Host = h.Name // requests may spell a host in any case
	}
	switch r.Cmd {
	case "goto":
		if r.Pane == "" && r.Session != "" { // the sessions menu
			for _, s := range m.fed.Spaces {
				if s.Host == r.Host && s.SessionID == r.Session {
					return m.gotoCmd(r.Host, r.Session, "", "", false)
				}
			}
			m.errText = "goto: no session " + r.Session + " on " + hostLabel(r.Host)
			return nil
		}
		for _, a := range m.fed.Agents {
			if a.Host == r.Host && a.PaneID == r.Pane {
				return m.gotoCmd(a.Host, a.SessionID, a.WindowID, a.PaneID, false)
			}
		}
		m.errText = "goto: no agent in pane " + r.Pane + " on " + hostLabel(r.Host)
	case "front":
		if _, ok := m.hostSet.Get(r.Host); !ok && r.Host != "" {
			m.errText = "no host " + r.Host
			return nil
		}
		return m.frontCmd(r.Host)
	case "last": // `flok last session|window|pane|server`: back along the focus history
		return m.lastCmd(r.Kind)
	case "reconnect": // `flok host reconnect`, or `flok host install` after an upgrade in place
		if _, ok := m.hostSet.Get(r.Host); !ok || r.Host == "" {
			m.errText = "no host " + r.Host
			return nil
		}
		if m.remote == nil {
			return nil
		}
		rem, host := m.remote, r.Host
		return func() tea.Msg { rem.Reconnect(host); return nil }
	default:
		if remote.IsKeyCommand(r.Cmd) { // `flok relay <cmd>` on this machine
			m.runKeyCommand(r.Cmd)
			return nil
		}
		m.debugf("request %q ignored", r.Cmd)
	}
	return nil
}

// frontCmd brings a server's work pane next to the sidebar and the keyboard to it.
func (m Model) frontCmd(host string) tea.Cmd {
	outer := m.d.Outer
	return m.swapCmd(host, func(pane string) error {
		if outer != nil && pane != "" {
			_, _ = outer.Run("select-pane", "-t", pane)
		}
		return nil
	})
}

var errNoSidebar = errors.New("the sidebar is not running (flok up)")
