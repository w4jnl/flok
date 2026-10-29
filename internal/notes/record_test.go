package notes

import (
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
)

func TestApplySession(t *testing.T) {
	now := time.Date(2026, 10, 29, 13, 0, 0, 0, time.UTC)
	var j Journal
	imports := 0
	step := func(ev agent.Event, secs int) (bool, bool) {
		now = now.Add(time.Duration(secs) * time.Second)
		ev.Agent, ev.AgentSessionID = "claude", "s1"
		changed, imp := Apply(&j, ev, now)
		if imp {
			imports++
		}
		return changed, imp
	}
	if _, imp := step(agent.Event{Kind: agent.EvSessionStart, Name: "SessionStart", Cwd: t.TempDir(), Model: "m1"}, 0); !imp {
		t.Fatal("a start imports")
	}
	r := &j.Record
	if r.Version != Version || r.Agent != "claude-code" || r.Session != "s1" || r.Model != "m1" || r.State != "idle" || r.Created != now {
		t.Fatalf("start: %+v", *r)
	}
	step(agent.Event{Kind: agent.EvPrompt, Name: "UserPromptSubmit"}, 5)
	if _, imp := step(agent.Event{Kind: agent.EvToolStart, Name: "PreToolUse", Tool: "Bash", Detail: "go test"}, 1); imp {
		t.Fatal("a tool call must not import")
	}
	step(agent.Event{Kind: agent.EvToolEnd, Name: "PostToolUse", Tool: "Bash"}, 1)
	step(agent.Event{Kind: agent.EvToolStart, Name: "PreToolUse", Tool: "Bash"}, 1)
	step(agent.Event{Kind: agent.EvBlocked, Name: "PermissionRequest", Reason: "permission", Tool: "Bash", ToolUseID: "t2"}, 1)
	if _, imp := step(agent.Event{Kind: agent.EvBlocked, Name: "Notification", Reason: "permission", Tool: "Bash"}, 1); imp {
		t.Fatal("the same block announced again must not import again")
	}
	step(agent.Event{Kind: agent.EvToolEnd, Name: "PostToolUse", Tool: "Bash"}, 9) // approved after 10 s
	form := &agent.Form{Questions: []agent.Question{{Text: "Which branch?", Options: []agent.Option{{Label: "main"}, {Label: "dev"}}}}}
	step(agent.Event{Kind: agent.EvBlocked, Name: "PreToolUse", Reason: "question", Tool: "AskUserQuestion", Form: form, ToolUseID: "q1"}, 1)
	step(agent.Event{Kind: agent.EvToolEnd, Name: "PostToolUse", Tool: "AskUserQuestion"}, 20)
	if _, imp := step(agent.Event{Kind: agent.EvWaiting, Name: "Stop", Reason: "waiting", Tool: "2 agents", Detail: "Explore; Plan", Message: "\n\nWaiting for the explorers.\n\nMore."}, 2); !imp {
		t.Fatal("a wait imports")
	}
	if r.State != "waiting" || r.Turns != 1 || r.Log[0].Text != "Waiting for the explorers." {
		t.Fatalf("wait: %+v", *r)
	}
	step(agent.Event{Kind: agent.EvToolStart, Name: "PreToolUse", Tool: "Read", Detail: "x.go"}, 60) // woken
	step(agent.Event{Kind: agent.EvSessionRefresh, Name: "SessionStart"}, 1)                         // a compaction
	if _, imp := step(agent.Event{Kind: agent.EvStop, Name: "Stop", Message: "- **Done.** All green."}, 3); !imp {
		t.Fatal("a stop imports")
	}
	if r.State != "done" || r.Turns != 2 || r.Tools != 3 || r.BlockedS != 30 || r.WaitedS != 60 {
		t.Fatalf("after two turns: %+v", *r)
	}
	t0 := r.Log[0]
	if t0.Tools["Bash"] != 2 || len(t0.Events) != 3 || t0.Events[0].Kind != "blocked" || t0.Events[0].Text != "permission: Bash" ||
		t0.Events[1].Kind != "question" || !strings.Contains(t0.Events[1].Text, "Which branch?") || t0.Events[2].Kind != "wait" || t0.Events[2].Seconds != 60 {
		t.Fatalf("turn 1: %+v", t0)
	}
	t1 := r.Log[1]
	if t1.Tools["Read"] != 1 || t1.Text != "Done. All green." || len(t1.Events) != 1 || t1.Events[0].Kind != "compaction" {
		t.Fatalf("turn 2: %+v", t1)
	}
	step(agent.Event{Kind: agent.EvPrompt, Name: "UserPromptSubmit"}, 5)
	step(agent.Event{Kind: agent.EvStop, Name: "StopFailure", Reason: "error", Message: "boom"}, 5)
	if r.Log[2].Events[0].Kind != "error" || r.Turns != 3 {
		t.Fatalf("error turn: %+v", r.Log[2])
	}
	if _, imp := step(agent.Event{Kind: agent.EvSessionEnd, Name: "SessionEnd", Reason: "prompt_input_exit"}, 1); !imp {
		t.Fatal("an end imports")
	}
	if r.Ended != now || r.EndReason != "error" || r.State != "done" || r.Updated != now {
		t.Fatalf("end: %+v", *r)
	}
	ended := r.Ended
	step(agent.Event{Kind: agent.EvStop, Name: "Stop"}, 1)
	if r.Ended != ended {
		t.Fatalf("a late stop keeps the end: %+v", *r)
	}
	if imports != 8 {
		t.Fatalf("imports %d", imports)
	}
}

func TestApplyClearEndsTheSession(t *testing.T) {
	now := time.Now()
	var j Journal
	Apply(&j, agent.Event{Kind: agent.EvSessionStart, Agent: "claude", AgentSessionID: "s1", Cwd: "/x"}, now)
	Apply(&j, agent.Event{Kind: agent.EvPrompt, Agent: "claude", AgentSessionID: "s1"}, now.Add(time.Second))
	changed, imp := Apply(&j, agent.Event{Kind: agent.EvSessionRefresh, Name: "SessionEnd", Reason: "clear", Agent: "claude", AgentSessionID: "s1"}, now.Add(2*time.Second))
	if !changed || !imp || j.Record.Ended.IsZero() || j.Record.EndReason != "clear" || j.Record.Turns != 1 {
		t.Fatalf("clear: %+v", j.Record)
	}
}

func TestFirstLine(t *testing.T) {
	cases := map[string]string{
		"":                                "",
		"\n\n## Done\n\n- **Fixed** it\n": "Fixed it",
		"## Done":                         "Done",
		"```go\nx\n```\nAfter the code":   "After the code",
		"   lots    of   space  ":         "lots of space",
		strings.Repeat("a", 300):          strings.Repeat("a", 199) + "…",
	}
	for in, want := range cases {
		if got := firstLine(in); got != want {
			t.Errorf("firstLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBounds(t *testing.T) {
	now := time.Now()
	var j Journal
	Apply(&j, agent.Event{Kind: agent.EvSessionStart, Agent: "claude", AgentSessionID: "s1"}, now)
	for i := 0; i < MaxTurns+10; i++ {
		now = now.Add(time.Second)
		Apply(&j, agent.Event{Kind: agent.EvPrompt, Agent: "claude", AgentSessionID: "s1"}, now)
		Apply(&j, agent.Event{Kind: agent.EvStop, Agent: "claude", AgentSessionID: "s1", Message: "t"}, now)
	}
	if len(j.Record.Log) != MaxTurns || j.Record.Turns != MaxTurns+10 {
		t.Fatalf("log %d turns %d", len(j.Record.Log), j.Record.Turns)
	}
}
