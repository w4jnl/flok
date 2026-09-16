// Package state applies hook events to an agent record and persists records as files.
package state

import (
	"time"

	"github.com/w4jnl/flok/internal/agent"
)

// Effects tells the caller what to do after Apply besides saving the record.
type Effects struct {
	Sound  string // "" | done | blocked | error
	Delete bool
}

// Apply updates a with ev. focused is evaluated lazily, only for transitions where it matters.
func Apply(a *agent.Agent, ev agent.Event, focused func() bool, now time.Time) Effects {
	var fx Effects
	if a.Kind == "" {
		a.Kind = ev.Agent
	}
	if ev.AgentSessionID != "" {
		a.AgentSessionID = ev.AgentSessionID
	}
	if ev.Cwd != "" {
		a.Cwd = ev.Cwd
	}
	a.LastEvent, a.LastEventAt, a.HasHooks, a.Source = ev.Name, now, true, "hook"
	set := func(s agent.State) {
		if a.State != s {
			a.State, a.StateSince = s, now
		}
	}
	switch ev.Kind {
	case agent.EvSessionStart:
		a.State, a.StateSince = agent.Idle, now
		a.Reason, a.CurrentTool, a.ToolDetail, a.LastToolUseID = "", "", "", ""
		a.TurnStarted, a.Notifications = time.Time{}, nil
	case agent.EvSessionRefresh:
	case agent.EvPrompt:
		set(agent.Working)
		a.TurnStarted, a.Reason, a.CurrentTool, a.ToolDetail, a.Notifications = now, "", "", "", nil
	case agent.EvToolStart:
		set(agent.Working)
		a.CurrentTool, a.ToolDetail, a.Reason = ev.Tool, ev.Detail, ""
		if a.TurnStarted.IsZero() {
			a.TurnStarted = now
		}
	case agent.EvToolEnd:
		set(agent.Working)
		a.CurrentTool, a.ToolDetail, a.Reason = "", "", ""
		if a.TurnStarted.IsZero() {
			a.TurnStarted = now
		}
	case agent.EvBlocked:
		reason := ev.Reason
		if reason == "permission" && ev.Tool != "" {
			reason = "permission:" + ev.Tool
		}
		dup := a.State == agent.Blocked && ((ev.ToolUseID != "" && ev.ToolUseID == a.LastToolUseID) || (ev.ToolUseID == "" && sameBlock(a.Reason, reason)))
		set(agent.Blocked)
		a.Reason = reason
		if ev.ToolUseID != "" {
			a.LastToolUseID = ev.ToolUseID
		}
		if !dup {
			addNotification(a, "blocked", now)
			if !focused() {
				fx.Sound = "blocked"
			}
		}
	case agent.EvNudge:
		if a.State == agent.Working && a.Reason != "waiting" { // idle_prompt also fires while paused
			fx = stop(a, "", focused, now)
		}
	case agent.EvWaiting:
		// The main turn ended but background tasks will wake the agent: keep it working, show
		// what it waits for, no sound and nothing unseen until the real end of the work.
		set(agent.Working)
		a.Reason, a.CurrentTool, a.ToolDetail = "waiting", ev.Detail, ""
		if a.TurnStarted.IsZero() {
			a.TurnStarted = now
		}
	case agent.EvStop:
		switch a.State {
		case agent.Working, agent.Blocked:
			fx = stop(a, ev.Reason, focused, now)
		case "", agent.Unknown:
			set(agent.Idle)
		}
	case agent.EvSessionEnd:
		fx.Delete = true
	}
	return fx
}

// sameBlock treats "permission" and "permission:Bash" as the same pending block.
func sameBlock(have, want string) bool {
	if have == want {
		return true
	}
	return len(have) >= 10 && len(want) >= 10 && have[:10] == "permission" && want[:10] == "permission"
}

func stop(a *agent.Agent, reason string, focused func() bool, now time.Time) Effects {
	a.CurrentTool, a.ToolDetail, a.TurnStarted, a.Reason = "", "", time.Time{}, reason
	if focused() {
		a.State, a.StateSince = agent.Idle, now
		return Effects{}
	}
	a.State, a.StateSince = agent.Done, now
	kind := "done"
	if reason == "error" {
		kind = "error"
	}
	addNotification(a, kind, now)
	return Effects{Sound: kind}
}

func addNotification(a *agent.Agent, kind string, now time.Time) {
	a.Notifications = append(a.Notifications, agent.Notification{Kind: kind, At: now})
	if n := len(a.Notifications); n > 20 {
		a.Notifications = a.Notifications[n-20:]
	}
}

// UnseenSince counts notifications newer than seenAt.
func UnseenSince(a agent.Agent, seenAt time.Time) int {
	n := 0
	for _, x := range a.Notifications {
		if x.At.After(seenAt) {
			n++
		}
	}
	return n
}
