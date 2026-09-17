package agent

import (
	"encoding/json"
	"testing"
)

func raw(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestClaudeMapHook(t *testing.T) {
	cases := []struct {
		in        string
		kind      EventKind
		ok        bool
		tool, det string
		reason    string
	}{
		{`{"hook_event_name":"SessionStart","source":"startup","session_id":"s1","cwd":"/p"}`, EvSessionStart, true, "", "", ""},
		{`{"hook_event_name":"SessionStart","source":"compact"}`, EvSessionRefresh, true, "", "", ""},
		{`{"hook_event_name":"UserPromptSubmit","user_prompt":"hi"}`, EvPrompt, true, "", "", ""},
		{`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm   test --watch=false please"},"tool_use_id":"t1"}`, EvToolStart, true, "Bash", "npm test --watch=false …", ""},
		{`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/a/b/main.go"}}`, EvToolStart, true, "Edit", "main.go", ""},
		{`{"hook_event_name":"PreToolUse","tool_name":"mcp__memory__search","tool_input":{}}`, EvToolStart, true, "memory:search", "", ""},
		{`{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{}}`, EvBlocked, true, "AskUserQuestion", "", "question"},
		{`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf x"}}`, EvBlocked, true, "Bash", "rm -rf x", "permission"},
		{`{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude wants to run: Bash\nnpm test"}`, EvBlocked, true, "Bash", "", "permission"},
		{`{"hook_event_name":"Notification","notification_type":"idle_prompt"}`, EvNudge, true, "", "", ""},
		{`{"hook_event_name":"Notification","notification_type":"auth_success"}`, "", false, "", "", ""},
		{`{"hook_event_name":"PostToolUse","tool_name":"Bash"}`, EvToolEnd, true, "Bash", "", ""},
		{`{"hook_event_name":"Stop","stop_hook_active":false}`, EvStop, true, "", "", ""},
		{`{"hook_event_name":"Stop","background_tasks":[]}`, EvStop, true, "", "", ""},
		{`{"hook_event_name":"Stop","background_tasks":[{"id":"1","type":"subagent","status":"running","agent_type":"Explore"},{"id":"2","type":"subagent"},{"id":"3","type":"shell","command":"go test"}]}`, EvWaiting, true, "", "2 agents + shell", "waiting"},
		{`{"hook_event_name":"Notification","notification_type":"agent_completed"}`, "", false, "", "", ""},
		{`{"hook_event_name":"StopFailure"}`, EvStop, true, "", "", "error"},
		{`{"hook_event_name":"SessionEnd","reason":"clear"}`, EvSessionRefresh, true, "", "", ""},
		{`{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`, EvSessionEnd, true, "", "", ""},
		{`{"hook_event_name":"Stop","agent_id":"sub1"}`, "", false, "", "", ""},
		{`{"hook_event_name":"SubagentStop"}`, "", false, "", "", ""},
	}
	for _, tc := range cases {
		ev, ok := MapHook("claude", raw(t, tc.in))
		if ok != tc.ok || ev.Kind != tc.kind || ev.Tool != tc.tool || ev.Detail != tc.det || ev.Reason != tc.reason {
			t.Errorf("%s\n got kind=%q ok=%v tool=%q detail=%q reason=%q", tc.in, ev.Kind, ok, ev.Tool, ev.Detail, ev.Reason)
		}
	}
}

func TestCopilotMapHook(t *testing.T) {
	ev, ok := MapHook("copilot", raw(t, `{"hook_event_name":"preToolUse","sessionId":"c1","toolName":"bash","toolArgs":{"command":"go test"}}`))
	if !ok || ev.Kind != EvToolStart || ev.Tool != "bash" || ev.AgentSessionID != "c1" {
		t.Fatalf("copilot preToolUse: %+v %v", ev, ok)
	}
	if ev, ok := MapHook("copilot", raw(t, `{"hook_event_name":"permissionRequest","toolName":"bash"}`)); ok {
		t.Fatalf("pre-decision permission request should be ignored: %+v", ev)
	}
	for _, payload := range []string{
		`{"hook_event_name":"notification","notificationType":"permission_prompt"}`,
		`{"hook_event_name":"notification","notification_type":"permission_prompt"}`,
	} {
		if ev, ok := MapHook("copilot", raw(t, payload)); !ok || ev.Kind != EvBlocked || ev.Reason != "permission" {
			t.Fatalf("permission prompt notification %s: %+v %v", payload, ev, ok)
		}
	}
	if ev, ok := MapHook("copilot", raw(t, `{"hook_event_name":"errorOccurred","recoverable":true}`)); ok {
		t.Fatalf("recoverable error should be ignored: %+v", ev)
	}
	if ev, ok := MapHook("copilot", raw(t, `{"hook_event_name":"agentStop"}`)); !ok || ev.Kind != EvStop {
		t.Fatalf("agentStop: %+v", ev)
	}
}
