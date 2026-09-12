package agent

import (
	"regexp"
	"strings"
)

// Claude Code sets the terminal title to "<spinner> <name>" while working (braille up to
// 2.1.227, half circles from 2.1.228) and "✳ <name>" when idle.
var (
	claudeSpinner = regexp.MustCompile(`^[\x{2800}-\x{28FF}\x{25D0}-\x{25D3}]\x{FE0E}? `)
	claudeIdle    = regexp.MustCompile(`^\x{2733}\x{FE0E}? `)
)

type Claude struct{}

func (Claude) ID() string             { return "claude" }
func (Claude) ProcessNames() []string { return []string{"claude"} }
func (Claude) TitleState(t string) (State, bool) {
	switch {
	case claudeSpinner.MatchString(t):
		return Working, true
	case claudeIdle.MatchString(t):
		return Idle, true
	}
	return Unknown, false
}
func (Claude) TitleName(t string) string {
	if loc := claudeSpinner.FindStringIndex(t); loc != nil {
		return strings.TrimSpace(t[loc[1]:])
	}
	if loc := claudeIdle.FindStringIndex(t); loc != nil {
		return strings.TrimSpace(t[loc[1]:])
	}
	return ""
}

func init() { Register(Claude{}) }

// MapHook implements HookMapper for Claude Code's hook payloads.
func (Claude) MapHook(raw map[string]any) (Event, bool) {
	if str(raw, "agent_id") != "" { // subagent events never change the pane's state
		return Event{}, false
	}
	name := str(raw, "hook_event_name")
	ev := Event{Name: name, AgentSessionID: str(raw, "session_id"), Cwd: str(raw, "cwd"), ToolUseID: str(raw, "tool_use_id")}
	switch name {
	case "SessionStart":
		ev.Kind = EvSessionStart
		if str(raw, "source") == "compact" {
			ev.Kind = EvSessionRefresh
		}
	case "UserPromptSubmit":
		ev.Kind = EvPrompt
	case "PreToolUse":
		tool := str(raw, "tool_name")
		if tool == "AskUserQuestion" {
			ev.Kind, ev.Reason, ev.Tool = EvBlocked, "question", tool
			break
		}
		ev.Kind = EvToolStart
		ev.Tool, ev.Detail = ToolSummary(tool, sub(raw, "tool_input"))
	case "PostToolUse", "PostToolUseFailure":
		ev.Kind = EvToolEnd
		ev.Tool, _ = ToolSummary(str(raw, "tool_name"), nil)
	case "PermissionRequest":
		ev.Kind, ev.Reason = EvBlocked, "permission"
		ev.Tool, ev.Detail = ToolSummary(str(raw, "tool_name"), sub(raw, "tool_input"))
	case "Notification":
		switch str(raw, "notification_type") {
		case "permission_prompt":
			ev.Kind, ev.Reason, ev.Tool = EvBlocked, "permission", toolFromMessage(str(raw, "message"))
		case "elicitation_dialog", "elicitation_url_dialog":
			ev.Kind, ev.Reason = EvBlocked, "elicitation"
		case "idle_prompt":
			ev.Kind = EvNudge
		default:
			// agent_completed / agent_needs_input describe background *sessions* (agent view),
			// not this pane; auth and quota notices carry no state.
			return Event{}, false
		}
	case "Stop":
		// Claude lists in-flight background work (subagents, shells, workflows) on Stop so a hook
		// can tell "done" from "paused until a background task wakes me": that is a waiting state.
		if tasks := BackgroundTasks(raw); tasks != "" {
			ev.Kind, ev.Reason, ev.Detail = EvWaiting, "waiting", tasks
			break
		}
		ev.Kind = EvStop
	case "StopFailure":
		ev.Kind, ev.Reason = EvStop, "error"
	case "SessionEnd":
		ev.Kind = EvSessionEnd
		if r := str(raw, "reason"); r == "clear" || r == "resume" {
			ev.Kind = EvSessionRefresh // a SessionStart follows immediately
		}
	default:
		return Event{}, false
	}
	return ev, true
}
