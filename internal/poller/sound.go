package poller

import (
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/notify"
	"github.com/w4jnl/flok/internal/state"
)

// soundTransitions plays sounds for agents the hooks do not cover (title/screen/registry
// sources) when they turn blocked or done unseen (or, with when_focused, also while watched),
// and, with SoundHookNotifications, for hook notifications the hook left unsounded.
func (p *Poller) soundTransitions() {
	cfg := p.d.Cfg.Sounds
	present := map[string]bool{}
	for _, a := range p.snap.Agents {
		key := agent.PaneRef{Host: a.Host, ID: a.PaneID}.String() // rate-limit key; bare id on a single host
		present[key] = true
		prev := p.prevState[key]
		p.prevState[key] = a.State
		if !cfg.Enabled {
			continue
		}
		focused := a.PaneID == p.snap.Focus.PaneID && a.Host == p.snap.Focus.Host
		if a.Source == "hook" {
			if !p.d.SoundHookNotifications || p.d.Store == nil {
				continue
			}
			for i, n := range a.Notifications {
				if n.Sounded || n.At.Before(p.started) || time.Since(n.At) > time.Minute {
					continue
				}
				p.playIfAllowed(key, n.Kind)
				idx := i
				_, _, _ = p.d.Store.Update(a.PaneID, func(rec *agent.Agent) state.Effects {
					if idx < len(rec.Notifications) {
						rec.Notifications[idx].Sounded = true
					}
					return state.Effects{}
				})
			}
			continue
		}
		if prev == a.State {
			continue
		}
		watched := focused && !p.terminalUnfocused()
		if watched && !cfg.WhenFocused {
			continue
		}
		switch a.State {
		case agent.Blocked:
			p.playIfAllowed(key, "blocked")
		case agent.Done:
			p.playIfAllowed(key, "done")
		case agent.Idle:
			if watched && prev == agent.Working { // a watched turn ends idle, never done
				p.playIfAllowed(key, "done")
			}
		}
	}
	for pane := range p.prevState {
		if !present[pane] {
			delete(p.prevState, pane)
		}
	}
}

func (p *Poller) terminalUnfocused() bool { return p.d.Store != nil && !p.d.Store.TerminalFocused() }

// playIfAllowed passes a sound through the per-pane and global rate limits to Deps.Sound. pane
// is the PaneRef string ("%12", "beta:%12"); notify.Allowed maps it to a safe file name.
func (p *Poller) playIfAllowed(pane, kind string) {
	if p.d.Sound == nil {
		return
	}
	if p.d.Store != nil && !notify.Allowed(p.d.Store.Dir, pane+"-"+kind, 2*time.Second, time.Duration(p.d.Cfg.Sounds.MinIntervalMs)*time.Millisecond, time.Now()) {
		return
	}
	p.d.Sound(pane, kind)
}

// persistCorrection writes an overruled hook state back to the record (under the record's
// lock, and only if no newer hook event replaced it), so the fix outlives this poll.
func (p *Poller) persistCorrection(c merge.Correction) {
	now := time.Now()
	p.debugf("correct %s %s -> idle (%s)", c.PaneID, c.From, c.Reason)
	_, _, _ = p.d.Store.Update(c.PaneID, func(rec *agent.Agent) state.Effects {
		if rec.State != c.From || !rec.StateSince.Equal(c.Since) {
			return state.Effects{} // a hook event arrived meanwhile: it wins
		}
		rec.State, rec.StateSince, rec.Reason = agent.Idle, now, ""
		rec.CurrentTool, rec.ToolDetail, rec.TurnStarted = "", "", time.Time{}
		rec.LastEvent, rec.LastEventAt = "corrected:"+c.Reason, now
		return state.Effects{}
	})
}
