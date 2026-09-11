package state

import (
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
)

func TestTurnUnfocused(t *testing.T) {
	unfocused := func() bool { return false }
	now := time.Now()
	var a agent.Agent
	var sounds []string
	step := func(ev agent.Event, f func() bool) {
		now = now.Add(time.Second)
		fx := Apply(&a, ev, f, now)
		if fx.Sound != "" {
			sounds = append(sounds, fx.Sound)
		}
	}
	step(agent.Event{Kind: agent.EvSessionStart, Agent: "claude", Name: "SessionStart", AgentSessionID: "s1"}, unfocused)
	if a.State != agent.Idle || !a.HasHooks || a.Kind != "claude" {
		t.Fatalf("after start: %+v", a)
	}
	step(agent.Event{Kind: agent.EvPrompt}, unfocused)
	step(agent.Event{Kind: agent.EvToolStart, Tool: "Bash", Detail: "npm test", ToolUseID: "t1"}, unfocused)
	if a.State != agent.Working || a.CurrentTool != "Bash" || a.TurnStarted.IsZero() {
		t.Fatalf("after tool start: %+v", a)
	}
	step(agent.Event{Kind: agent.EvBlocked, Reason: "permission", Tool: "Bash", ToolUseID: "t1"}, unfocused)
	step(agent.Event{Kind: agent.EvBlocked, Reason: "permission", Tool: "Bash"}, unfocused) // Notification duplicate
	if a.State != agent.Blocked || a.Reason != "permission:Bash" || len(a.Notifications) != 1 {
		t.Fatalf("after block: %+v", a)
	}
	step(agent.Event{Kind: agent.EvToolEnd, Tool: "Bash"}, unfocused)
	if a.State != agent.Working || a.CurrentTool != "" {
		t.Fatalf("after tool end: %+v", a)
	}
	step(agent.Event{Kind: agent.EvStop}, unfocused)
	step(agent.Event{Kind: agent.EvStop}, unfocused) // agent_completed duplicate
	if a.State != agent.Done || len(a.Notifications) != 2 || UnseenSince(a, time.Time{}) != 2 {
		t.Fatalf("after stop: %+v", a)
	}
	if got := len(sounds); got != 2 || sounds[0] != "blocked" || sounds[1] != "done" {
		t.Fatalf("sounds %v", sounds)
	}
	seenAt := now
	if UnseenSince(a, seenAt) != 0 {
		t.Fatal("seen should clear unseen")
	}
	step(agent.Event{Kind: agent.EvPrompt}, unfocused)
	if a.State != agent.Working || a.Notifications != nil {
		t.Fatalf("new prompt should reset: %+v", a)
	}
	step(agent.Event{Kind: agent.EvBlocked, Reason: "question", Tool: "AskUserQuestion"}, func() bool { return true })
	if a.State != agent.Blocked || a.Reason != "question" || len(sounds) != 2 {
		t.Fatalf("question while focused must not beep: %+v %v", a, sounds)
	}
	step(agent.Event{Kind: agent.EvStop, Reason: "error"}, unfocused)
	if a.State != agent.Done || a.Reason != "error" || sounds[len(sounds)-1] != "error" {
		t.Fatalf("error stop: %+v %v", a, sounds)
	}
	if fx := Apply(&a, agent.Event{Kind: agent.EvSessionEnd}, unfocused, now); !fx.Delete {
		t.Fatal("session end should delete")
	}
}

func TestStopFocusedIsIdleAndNudge(t *testing.T) {
	focused := func() bool { return true }
	now := time.Now()
	a := agent.Agent{State: agent.Working, TurnStarted: now}
	if fx := Apply(&a, agent.Event{Kind: agent.EvStop}, focused, now); fx.Sound != "" || a.State != agent.Idle {
		t.Fatalf("focused stop: %+v %+v", a, fx)
	}
	a = agent.Agent{State: agent.Working}
	if fx := Apply(&a, agent.Event{Kind: agent.EvNudge}, func() bool { return false }, now); fx.Sound != "done" || a.State != agent.Done {
		t.Fatalf("nudge while working: %+v %+v", a, fx)
	}
	if fx := Apply(&a, agent.Event{Kind: agent.EvNudge}, func() bool { return false }, now); fx.Sound != "" {
		t.Fatalf("nudge while done must be silent: %+v", fx)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	now := time.Now()
	a, fx, err := s.Update("%7", func(a *agent.Agent) Effects {
		return Apply(a, agent.Event{Kind: agent.EvSessionStart, Agent: "claude", Name: "SessionStart"}, nil, now)
	})
	if err != nil || fx.Delete || a.State != agent.Idle {
		t.Fatalf("update: %v %+v", err, a)
	}
	all := s.LoadAgents()
	if len(all) != 1 || all["%7"].Kind != "claude" {
		t.Fatalf("load: %+v", all)
	}
	if err := s.MarkSeen("%7", now); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadSeen()["%7"]; !got.Equal(now) {
		t.Fatalf("seen %v want %v", got, now)
	}
	if _, fx, _ := s.Update("%7", func(a *agent.Agent) Effects { return Effects{Delete: true} }); !fx.Delete {
		t.Fatal("delete")
	}
	if len(s.LoadAgents()) != 0 || len(s.LoadSeen()) != 0 {
		t.Fatal("delete should remove both files")
	}
}
