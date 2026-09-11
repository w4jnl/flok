package agent

import "regexp"

// Adapter is what flok knows about one kind of coding agent.
type Adapter interface {
	ID() string
	// ProcessNames are pane_current_command values that identify the agent.
	ProcessNames() []string
	// TitleState classifies the pane title; ok is false when the title carries no signal.
	TitleState(title string) (state State, ok bool)
	// TitleName strips state glyphs from the title; "" when nothing useful remains.
	TitleName(title string) string
}

var registry = map[string]Adapter{}

func Register(a Adapter) { registry[a.ID()] = a }

func Get(id string) Adapter { return registry[id] }

// Enabled returns adapters for the ids in order, skipping unknown ones.
func Enabled(ids []string) []Adapter {
	var out []Adapter
	for _, id := range ids {
		if a := registry[id]; a != nil {
			out = append(out, a)
		}
	}
	return out
}

// versionRe matches the Claude Code native binary when launched by its versioned path
// (~/.local/share/claude/versions/2.1.268), where the process name is the version.
var versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// Match finds the adapter whose process the pane is running, or nil.
func Match(command string, adapters []Adapter) Adapter {
	for _, a := range adapters {
		for _, n := range a.ProcessNames() {
			if command == n {
				return a
			}
		}
	}
	if versionRe.MatchString(command) {
		for _, a := range adapters {
			if a.ID() == "claude" {
				return a
			}
		}
	}
	return nil
}
