package agent

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type EventKind string

const (
	EvSessionStart   EventKind = "session_start"
	EvSessionRefresh EventKind = "session_refresh"
	EvPrompt         EventKind = "prompt"
	EvToolStart      EventKind = "tool_start"
	EvToolEnd        EventKind = "tool_end"
	EvBlocked        EventKind = "blocked"
	EvNudge          EventKind = "nudge"
	EvStop           EventKind = "stop"
	EvWaiting        EventKind = "waiting" // turn ended, but background tasks will wake the agent again
	EvSessionEnd     EventKind = "session_end"
)

// Event is the agent-neutral form of a hook payload.
type Event struct {
	Kind           EventKind `json:"kind"`
	Agent          string    `json:"agent"`
	Name           string    `json:"name"` // raw hook event name
	AgentSessionID string    `json:"agent_session_id,omitempty"`
	Cwd            string    `json:"cwd,omitempty"`
	Tool           string    `json:"tool,omitempty"`
	Detail         string    `json:"detail,omitempty"`
	Reason         string    `json:"reason,omitempty"` // permission | question | elicitation | error
	Form           *Form     `json:"form,omitempty"`   // question: the form the agent put up, when the hook saw it
	ToolUseID      string    `json:"tool_use_id,omitempty"`
	Message        string    `json:"message,omitempty"`    // Stop: the agent's last message (for the session report)
	Model          string    `json:"model,omitempty"`      // when the payload names it
	Transcript     string    `json:"transcript,omitempty"` // the agent's transcript file, when the payload names it (the phone's feed reads it)
	At             time.Time `json:"at"`
}

// HookMapper converts a raw hook payload into an Event; ok is false for ignored payloads.
type HookMapper interface {
	MapHook(raw map[string]any) (Event, bool)
}

// MapHook dispatches to the adapter's HookMapper.
func MapHook(id string, raw map[string]any) (Event, bool) {
	hm, ok := Get(id).(HookMapper)
	if !ok {
		return Event{}, false
	}
	ev, ok := hm.MapHook(raw)
	if ok {
		ev.Agent = id
	}
	return ev, ok
}

func str(m map[string]any, k string) string { v, _ := m[k].(string); return v }
func sub(m map[string]any, k string) map[string]any {
	v, _ := m[k].(map[string]any)
	return v
}

var spaces = regexp.MustCompile(`\s+`)

// squash collapses whitespace and truncates to n runes with an ellipsis.
func squash(s string, n int) string {
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// ToolSummary returns a short display name and detail for a tool call.
func ToolSummary(tool string, input map[string]any) (string, string) {
	name := tool
	if strings.HasPrefix(tool, "mcp__") {
		if parts := strings.SplitN(tool, "__", 3); len(parts) == 3 {
			name = parts[1] + ":" + parts[2]
		}
	}
	var detail string
	switch tool {
	case "Bash":
		detail = squash(str(input, "command"), 24)
	case "Edit", "Write", "Read", "MultiEdit", "NotebookEdit":
		detail = filepath.Base(str(input, "file_path"))
	case "Grep", "Glob":
		detail = squash(str(input, "pattern"), 24)
	case "Agent", "Task":
		detail = squash(str(input, "description"), 24)
	case "WebFetch":
		if u, err := url.Parse(str(input, "url")); err == nil {
			detail = u.Host
		}
	case "WebSearch":
		detail = squash(str(input, "query"), 24)
	case "Skill":
		detail = str(input, "skill")
	}
	return name, detail
}

var toolInMessage = regexp.MustCompile(`(?:run|use|to use|execute):?\s+([A-Za-z][A-Za-z0-9_:-]*)`)

// toolFromMessage extracts the tool name from a permission notification message such as
// "Claude wants to run: Bash\nnpm test".
func toolFromMessage(msg string) string {
	if m := toolInMessage.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	return ""
}

// BackgroundTasks reads a Stop payload's in-flight tasks. work is the kinds of work that will
// wake the agent again ("2 agents", "agent + shell"), "" when nothing will; what names every
// task for the log and the row ("Explore: nt docs; go test; watch: comments on Plan"). A
// monitor is a watch, not work: the comments on an artifact the session published, a PR watch,
// a log tail, a plugin's monitor. It may never fire, Claude's own registry calls the session
// idle while only monitors run, and a turn that ended behind one is done: it is named, not
// counted.
func BackgroundTasks(raw map[string]any) (work, what string) {
	list, _ := raw["background_tasks"].([]any)
	if len(list) == 0 {
		return "", ""
	}
	counts := map[string]int{}
	var order, names []string
	for _, item := range list {
		m, _ := item.(map[string]any)
		t := str(m, "type")
		switch t {
		case "subagent", "":
			t = "agent"
		case "cloud session":
			t = "session"
		case "MCP task":
			t = "mcp task"
		}
		name := str(m, "description")
		if name == "" {
			for _, k := range []string{"command", "agent_type", "name", "tool"} {
				if name = str(m, k); name != "" {
					break
				}
			}
		} else if at := str(m, "agent_type"); at != "" {
			name = at + ": " + name
		}
		if t == "monitor" {
			names = append(names, strings.TrimSpace("watch: "+squash(name, 40)))
			continue
		}
		if name != "" {
			names = append(names, squash(name, 40))
		}
		if counts[t] == 0 {
			order = append(order, t)
		}
		counts[t]++
	}
	var parts []string
	for _, t := range order {
		n := counts[t]
		if n == 1 {
			parts = append(parts, t)
			continue
		}
		parts = append(parts, fmt.Sprintf("%d %ss", n, t))
	}
	return strings.Join(parts, " + "), squash(strings.Join(names, "; "), 120)
}
