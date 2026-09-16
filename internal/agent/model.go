// Package agent defines the agent/space model shared by the hook receiver, the merge layer
// and the UI, plus the per-agent adapters (Claude Code, Copilot CLI).
package agent

import "time"

type State string

const (
	Working State = "working"
	Blocked State = "blocked"
	Done    State = "done"
	Idle    State = "idle"
	Unknown State = "unknown"
)

type Notification struct {
	Kind    string    `json:"kind"` // done | blocked | error
	At      time.Time `json:"at"`
	Sounded bool      `json:"sounded"`
}

type Agent struct {
	PaneID         string         `json:"pane_id"`
	SessionID      string         `json:"session_id,omitempty"`
	SessionName    string         `json:"session_name,omitempty"`
	WindowID       string         `json:"window_id,omitempty"`
	WindowIndex    int            `json:"window_index,omitempty"`
	PaneIndex      int            `json:"pane_index,omitempty"`
	Kind           string         `json:"kind"`
	AgentSessionID string         `json:"agent_session_id,omitempty"`
	Name           string         `json:"name,omitempty"`  // project label: base name of Cwd
	Title          string         `json:"title,omitempty"` // the agent's own session name (Claude title / registry name)
	State          State          `json:"state"`
	StateSince     time.Time      `json:"state_since"`
	Reason         string         `json:"reason,omitempty"`
	LastEvent      string         `json:"last_event,omitempty"`
	LastEventAt    time.Time      `json:"last_event_at,omitempty"`
	CurrentTool    string         `json:"current_tool,omitempty"`
	ToolDetail     string         `json:"tool_detail,omitempty"`
	LastToolUseID  string         `json:"last_tool_use_id,omitempty"`
	TurnStarted    time.Time      `json:"turn_started,omitempty"`
	Notifications  []Notification `json:"notifications,omitempty"`
	Unseen         int            `json:"unseen"`
	Cwd            string         `json:"cwd,omitempty"`
	Branch         string         `json:"branch,omitempty"`
	Source         string         `json:"source,omitempty"` // hook | registry | title | screen
	HasHooks       bool           `json:"has_hooks"`
}

// Space is one tmux session; the sidebar header calls the panel "sessions".
type Space struct {
	SessionID   string
	SessionName string
	Path        string
	Branch      string
	Attached    bool
	Current     bool
	Rollup      State // "" when the space has no agents
	AgentCount  int
}

// Priority orders states for the agents panel: lower sorts first.
func Priority(s State) int {
	switch s {
	case Blocked:
		return 0
	case Done:
		return 1
	case Working:
		return 2
	case Idle:
		return 3
	}
	return 4
}
