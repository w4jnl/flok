package agent

// Copilot CLI publishes no state in its title; states come from hooks and screen rules (M4).
type Copilot struct{}

func (Copilot) ID() string                      { return "copilot" }
func (Copilot) ProcessNames() []string          { return []string{"copilot"} }
func (Copilot) TitleState(string) (State, bool) { return Unknown, false }
func (Copilot) TitleName(string) string         { return "" }

// MapHook implements HookMapper for Copilot CLI. Its payloads carry no event name, so the
// installer passes `--event <name>` and the CLI stores it under "hook_event_name".
func (Copilot) MapHook(raw map[string]any) (Event, bool) {
	name := str(raw, "hook_event_name")
	ev := Event{Name: name, AgentSessionID: str(raw, "sessionId"), Cwd: str(raw, "cwd")}
	switch name {
	case "sessionStart":
		ev.Kind = EvSessionStart
	case "sessionEnd":
		ev.Kind = EvSessionEnd
	case "userPromptSubmitted":
		ev.Kind = EvPrompt
	case "preToolUse":
		ev.Kind = EvToolStart
		ev.Tool, ev.Detail = ToolSummary(str(raw, "toolName"), sub(raw, "toolArgs"))
	case "postToolUse", "postToolUseFailure":
		ev.Kind = EvToolEnd
		ev.Tool = str(raw, "toolName")
	case "permissionRequest":
		ev.Kind, ev.Reason = EvBlocked, "permission"
		ev.Tool, ev.Detail = ToolSummary(str(raw, "toolName"), sub(raw, "toolInput"))
	case "notification":
		switch str(raw, "notification_type") {
		case "permission_prompt":
			ev.Kind, ev.Reason = EvBlocked, "permission"
		case "elicitation_dialog":
			ev.Kind, ev.Reason = EvBlocked, "elicitation"
		case "agent_idle":
			ev.Kind = EvNudge
		case "agent_completed":
			ev.Kind = EvStop
		default:
			return Event{}, false
		}
	case "agentStop":
		ev.Kind = EvStop
	case "errorOccurred":
		if rec, ok := raw["recoverable"].(bool); ok && !rec {
			ev.Kind, ev.Reason = EvStop, "error"
			break
		}
		return Event{}, false
	default:
		return Event{}, false
	}
	return ev, true
}

func init() { Register(Copilot{}) }
