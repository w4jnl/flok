package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/merge"
)

// historyMax bounds the focus history; a few dozen moves back is more than anyone retraces.
const historyMax = 64

// noteFocus appends the front server's focus to the history when it moved to another pane.
// refederate calls it, and refederate runs after every merged poll, every host snapshot and
// every front change, so tmux's own moves inside the work pane, flok's jumps and server swaps
// all land here, each entry stamped with its server.
func (m *Model) noteFocus() {
	f := m.fed.Focus
	if !f.Found || f.PaneID == "" {
		return
	}
	if len(m.history) > 0 && m.history[0].Host == f.Host && m.history[0].PaneID == f.PaneID {
		return
	}
	m.history = append([]merge.Focus{f}, m.history...)
	if len(m.history) > historyMax {
		m.history = m.history[:historyMax]
	}
}

// lastTarget is the most recent place before the current one at the given granularity, scoped
// the way tmux scopes its own: the previous pane within the current window, the previous window
// within the current session (both on the server in front), the previous session, the previous
// agent pane and the previous server anywhere. A session that is gone is skipped.
func (m Model) lastTarget(kind string) (merge.Focus, bool) {
	if len(m.history) == 0 {
		return merge.Focus{}, false
	}
	cur := m.history[0]
	for _, e := range m.history[1:] {
		var fits bool
		switch kind {
		case "pane":
			fits = e.Host == cur.Host && e.SessionID == cur.SessionID && e.WindowID == cur.WindowID && e.PaneID != cur.PaneID
		case "window":
			fits = e.Host == cur.Host && e.SessionID == cur.SessionID && e.WindowID != cur.WindowID
		case "session":
			fits = e.Host != cur.Host || e.SessionID != cur.SessionID
		case "agent":
			fits = (e.Host != cur.Host || e.PaneID != cur.PaneID) && m.isAgentPane(e.Host, e.PaneID)
		case "server":
			fits = e.Host != cur.Host
		}
		if fits && m.sessionExists(e.Host, e.SessionID) {
			return e, true
		}
	}
	return merge.Focus{}, false
}

// isAgentPane says whether a pane is one of the agents in the federated view.
func (m Model) isAgentPane(host, paneID string) bool {
	for _, a := range m.fed.Agents {
		if a.Host == host && a.PaneID == paneID {
			return true
		}
	}
	return false
}

// sessionExists says whether a server still has the session, from its latest merge.
func (m Model) sessionExists(host, sessionID string) bool {
	spaces := m.local.Spaces
	if host != "" {
		v, ok := m.remotes[host]
		if !ok || !v.hasSnap {
			return false
		}
		spaces = v.snap.Spaces
	}
	for _, s := range spaces {
		if s.SessionID == sessionID {
			return true
		}
	}
	return false
}

// lastCmd runs `flok last <kind>`: back to the previous pane, window, session or server along
// the history; a server that has to come to the front first is swapped in by gotoCmd.
func (m *Model) lastCmd(kind string) tea.Cmd {
	switch kind {
	case "pane", "window", "session", "agent", "server":
	default:
		m.errText = "last: session, window, pane, agent or server"
		return nil
	}
	e, ok := m.lastTarget(kind)
	if kind == "server" {
		host := e.Host
		if !ok { // no other server in the history yet: the one the last swap left, as `host last` does
			rt, err := launcher.ReadRuntime()
			if err != nil || rt.PreviousFront == "" {
				m.errText = "last server: no previous server yet"
				return nil
			}
			if host = rt.PreviousFront; host == agent.LocalHost {
				host = ""
			}
		}
		if host == m.front {
			m.errText = "last server: already on " + hostLabel(host)
			return nil
		}
		if _, known := m.hostSet.Get(host); !known && host != "" {
			m.errText = "last server: no host " + host
			return nil
		}
		return m.frontCmd(host)
	}
	if !ok {
		// nothing flok saw in this window or session: tmux's own memory of the local server
		// still knows (moves from before flok started); a host's does not reach us
		if cur := m.fed.Focus; (kind == "window" || kind == "pane") && cur.Found && cur.Host == "" && m.d.Inner != nil {
			inner, sess := m.d.Inner, cur.SessionID
			return func() tea.Msg {
				args := []string{"last-window", "-t", sess}
				if kind == "pane" {
					args = []string{"last-pane", "-t", sess + ":"}
				}
				_, err := inner.Run(args...)
				return switchedMsg{err}
			}
		}
		m.errText = fmt.Sprintf("last %s: nowhere to go back to yet", kind)
		return nil
	}
	return m.gotoCmd(e.Host, e.SessionID, e.WindowID, e.PaneID, false)
}
