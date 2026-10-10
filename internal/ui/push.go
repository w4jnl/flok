package ui

import (
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/push"
)

// pushMinGap drops a repeat of the same kind for the same pane that follows within it.
const pushMinGap = 2 * time.Second

// notePush sends a notification for every agent that just turned blocked, done or failed, on
// any server in the federated view. refederate calls it; the first call only primes the
// previous states, so a start does not announce what is already waiting.
func (m *Model) notePush() {
	if m.pusher == nil {
		return
	}
	now := time.Now()
	seen := make(map[string]bool, len(m.fed.Agents))
	for _, a := range m.fed.Agents {
		key := agent.PaneRef{Host: a.Host, ID: a.PaneID}.String()
		seen[key] = true
		prev, had := m.pushPrev[key]
		m.pushPrev[key] = a.State
		if !m.pushPrimed || (had && prev == a.State) || (!had && m.pushPrimed && a.State != agent.Blocked) {
			continue
		}
		kind := ""
		switch a.State {
		case agent.Blocked:
			kind = "blocked"
		case agent.Done:
			kind = "done"
			if a.Reason == "error" {
				kind = "error"
			}
		}
		if kind == "" || !m.d.Cfg.Notify.Wants(kind) {
			continue
		}
		if last, ok := m.pushLast[key+" "+kind]; ok && now.Sub(last) < pushMinGap {
			continue
		}
		m.pushLast[key+" "+kind] = now
		m.debugf("push %s %s (%s)", kind, key, a.Reason)
		m.pusher.Send(push.Event{Instance: m.d.Cfg.InstanceName(), Host: a.Host, Agent: a.Name, Pane: key, Kind: kind, Reason: a.Reason, At: now})
	}
	for key := range m.pushPrev {
		if !seen[key] {
			delete(m.pushPrev, key)
		}
	}
	m.pushPrimed = true
}
