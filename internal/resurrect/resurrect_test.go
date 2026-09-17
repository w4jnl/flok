package resurrect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/tmux"
)

func TestRewriteExactAgentSessions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.txt")
	input := strings.Join([]string{
		"pane\twork\t1\t1\t:*\t1\tCopilot\t:/p/copilot\t1\tcopilot\t:copilot --continue",
		"pane\twork\t1\t1\t:*\t2\tClaude\t:/p/claude\t0\tclaude\t:claude",
		"pane\twork\t1\t1\t:*\t3\tTool\t:/p/tool\t0\tbash\t:long-running-tool",
		"pane\twork\t1\t1\t:*\t4\tNo hooks\t:/p/new\t0\tcopilot\t:copilot",
		"pane\twork\t1\t1\t:*\t5\tSSH\t:/p/ssh\t0\tssh\t:ssh host",
		"window\twork\t1\t:main\t1\t:*\tlayout\t:",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(input), 0o640); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	snap := tmux.Snapshot{Panes: []tmux.Pane{
		{ID: "%1", SessionName: "work", WindowIndex: 1, PaneIndex: 1, Command: "copilot"},
		{ID: "%2", SessionName: "work", WindowIndex: 1, PaneIndex: 2, Command: "claude"},
		{ID: "%3", SessionName: "work", WindowIndex: 1, PaneIndex: 3, Command: "bash"},
		{ID: "%4", SessionName: "work", WindowIndex: 1, PaneIndex: 4, Command: "copilot"},
		{ID: "%5", SessionName: "work", WindowIndex: 1, PaneIndex: 5, Command: "ssh"},
	}}
	hooks := map[string]agent.Agent{
		"%1": {Kind: "copilot", HasHooks: true, AgentSessionID: "copilot-id", State: agent.Idle},
		"%2": {Kind: "claude", HasHooks: true, AgentSessionID: "claude-id", State: agent.Done},
		"%3": {Kind: "claude", HasHooks: true, AgentSessionID: "tool-id", State: agent.Working, LastEventAt: now},
		"%5": {Kind: "copilot", HasHooks: true, AgentSessionID: "stale-id", State: agent.Done, LastEventAt: now},
	}
	report, err := Rewrite(path, Inputs{Snapshot: snap, Hooks: hooks, Adapters: agent.Enabled([]string{"claude", "copilot"})})
	if err != nil {
		t.Fatal(err)
	}
	if report.Rewritten != 3 || len(report.Skipped) != 1 || report.Skipped[0].PaneID != "%4" || report.Skipped[0].Reason != "no hook record" {
		t.Fatalf("report: %+v", report)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"pane\twork\t1\t1\t:*\t1\tCopilot\t:/p/copilot\t1\tcopilot\t:copilot --resume='copilot-id'",
		"pane\twork\t1\t1\t:*\t2\tClaude\t:/p/claude\t0\tclaude\t:claude --resume 'claude-id'",
		"pane\twork\t1\t1\t:*\t3\tTool\t:/p/tool\t0\tbash\t:claude --resume 'tool-id'",
		"pane\twork\t1\t1\t:*\t4\tNo hooks\t:/p/new\t0\tcopilot\t:",
		"pane\twork\t1\t1\t:*\t5\tSSH\t:/p/ssh\t0\tssh\t:ssh host",
		"window\twork\t1\t:main\t1\t:*\tlayout\t:",
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("rewritten state:\n%s\nwant:\n%s", got, want)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestRewriteRejectsMalformedPaneWithoutChangingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.txt")
	input := "pane\ttoo\tshort\n"
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Rewrite(path, Inputs{})
	if err == nil || !strings.Contains(err.Error(), "want at least 11") {
		t.Fatalf("error = %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != input {
		t.Fatalf("malformed file changed: %q", got)
	}
}

func TestRewriteRejectsUnsafeSessionID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.txt")
	input := "pane\twork\t1\t1\t:*\t1\tCopilot\t:/p\t1\tcopilot\t:copilot\n"
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := tmux.Snapshot{Panes: []tmux.Pane{{ID: "%1", SessionName: "work", WindowIndex: 1, PaneIndex: 1, Command: "copilot"}}}
	hooks := map[string]agent.Agent{"%1": {Kind: "copilot", HasHooks: true, AgentSessionID: "bad\nid", State: agent.Idle}}
	report, err := Rewrite(path, Inputs{Snapshot: snap, Hooks: hooks, Adapters: agent.Enabled([]string{"copilot"})})
	if err != nil {
		t.Fatal(err)
	}
	if report.Rewritten != 0 || len(report.Skipped) != 1 {
		t.Fatalf("report: %+v", report)
	}
	got, _ := os.ReadFile(path)
	if string(got) != strings.Replace(input, ":copilot\n", ":\n", 1) {
		t.Fatalf("unsafe session did not fall back to shell: %q", got)
	}
}

// The registry describes the live process: it saves panes whose hooks never fired (hooks
// installed after the session started, or calling a binary that was removed), covers a pane
// whose foreground process is a tool the agent runs, and beats a hook record that may be stale.
func TestRewritePrefersClaudeRegistryOverHooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.txt")
	input := strings.Join([]string{
		"pane\twork\t1\t1\t:*\t1\tNo hooks\t:/p/a\t1\t2.1.274\t:claude -c",
		"pane\twork\t1\t1\t:*\t2\tStale hook\t:/p/b\t0\tclaude\t:claude",
		"pane\twork\t1\t1\t:*\t3\tTool running\t:/p/c\t0\tgit\t:git log",
		"pane\twork\t1\t1\t:*\t4\tCopilot\t:/p/d\t0\tcopilot\t:copilot",
		"pane\twork\t1\t1\t:*\t5\tNothing known\t:/p/e\t0\tclaude\t:claude",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := tmux.Snapshot{Panes: []tmux.Pane{
		{ID: "%1", SessionName: "work", WindowIndex: 1, PaneIndex: 1, Command: "2.1.274"},
		{ID: "%2", SessionName: "work", WindowIndex: 1, PaneIndex: 2, Command: "claude"},
		{ID: "%3", SessionName: "work", WindowIndex: 1, PaneIndex: 3, Command: "git"},
		{ID: "%4", SessionName: "work", WindowIndex: 1, PaneIndex: 4, Command: "copilot"},
		{ID: "%5", SessionName: "work", WindowIndex: 1, PaneIndex: 5, Command: "claude"},
	}}
	hooks := map[string]agent.Agent{
		"%2": {Kind: "claude", HasHooks: true, AgentSessionID: "old-id", State: agent.Idle},
		"%4": {Kind: "copilot", HasHooks: true, AgentSessionID: "copilot-id", State: agent.Idle},
	}
	registry := map[string]string{"%1": "reg-a", "%2": "reg-b", "%3": "reg-c", "%4": "ignored-for-copilot"}
	report, err := Rewrite(path, Inputs{Snapshot: snap, Hooks: hooks, Registry: registry, Adapters: agent.Enabled([]string{"claude", "copilot"})})
	if err != nil {
		t.Fatal(err)
	}
	if report.Rewritten != 4 || len(report.Skipped) != 1 || report.Skipped[0].PaneID != "%5" ||
		!strings.Contains(report.Skipped[0].Reason, "claude agents") {
		t.Fatalf("report: %+v", report)
	}
	got, _ := os.ReadFile(path)
	want := strings.Join([]string{
		"pane\twork\t1\t1\t:*\t1\tNo hooks\t:/p/a\t1\t2.1.274\t:claude --resume 'reg-a'",
		"pane\twork\t1\t1\t:*\t2\tStale hook\t:/p/b\t0\tclaude\t:claude --resume 'reg-b'",
		"pane\twork\t1\t1\t:*\t3\tTool running\t:/p/c\t0\tgit\t:claude --resume 'reg-c'",
		"pane\twork\t1\t1\t:*\t4\tCopilot\t:/p/d\t0\tcopilot\t:copilot --resume='copilot-id'",
		"pane\twork\t1\t1\t:*\t5\tNothing known\t:/p/e\t0\tclaude\t:",
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("rewritten state:\n%s\nwant:\n%s", got, want)
	}
}
