package rules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/w4jnl/flok/internal/agent"
)

func TestBundledManifestsLoad(t *testing.T) {
	set := Load("", false)
	for _, id := range []string{"claude", "copilot", "codex", "gemini", "opencode", "claude-code"} {
		if set.Get(id) == nil {
			t.Errorf("manifest %s missing", id)
		}
	}
	for _, p := range set.Problems {
		t.Errorf("bundled manifest problem: %s", p)
	}
}

func TestOSCProgressRegion(t *testing.T) {
	if got := OSCProgress("hidden", "0"); got != "4;0;0" {
		t.Fatalf("hidden: %q", got)
	}
	if got := OSCProgress("indeterminate", ""); got != "4;3;0" {
		t.Fatalf("indeterminate: %q", got)
	}
	if got := OSCProgress("", ""); got != "" {
		t.Fatalf("unknown state must yield no region: %q", got)
	}
	m := Load("", false).Get("claude")
	// nothing on screen and a plain title: only the cleared progress bar says idle
	res := m.Evaluate(NewScreen("", "plain").WithProgress("hidden", "0"))
	if !res.Matched || res.State != agent.Idle || res.RuleID != "osc_progress_idle" {
		t.Fatalf("progress idle: %+v", res)
	}
	if res := m.Evaluate(NewScreen("", "plain")); res.Matched && res.RuleID == "osc_progress_idle" {
		t.Fatalf("no progress info must not match the progress rule: %+v", res)
	}
}

func TestClaudeFixtures(t *testing.T) {
	m := Load("", false).Get("claude")
	cases := []struct {
		file  string
		title string
		state agent.State
		hold  bool
		rule  string
	}{
		{"claude_permission_bash.txt", "✳ proj", agent.Blocked, false, "bash_permission_prompt"},
		{"claude_askuser.txt", "✳ proj", agent.Blocked, false, "live_blocked_form"},
		{"claude_prompt_box.txt", "✳ proj", agent.Idle, false, "live_prompt_box"},
		{"claude_working.txt", "◑ proj", agent.Working, false, "osc_title_working"},
		{"claude_working.txt", "plain title", agent.Working, false, "live_turn_working"},
		// ✳ is one of Claude Code's screen spinner frames (and its idle title glyph): the working
		// rule must accept it, else one capture in six reads as an idle prompt box
		{"claude_working_star.txt", "✳ proj", agent.Working, false, "live_turn_working"},
		{"claude_working_star.txt", "plain title", agent.Working, false, "live_turn_working"},
		{"claude_model_picker.txt", "✳ proj", agent.Unknown, true, "model_picker_menu"},
	}
	for _, tc := range cases {
		data, err := os.ReadFile(filepath.Join("testdata", tc.file))
		if err != nil {
			t.Fatal(err)
		}
		res := m.Evaluate(NewScreen(string(data), tc.title))
		if !res.Matched || res.State != tc.state || res.Hold != tc.hold || res.RuleID != tc.rule {
			t.Errorf("%s (%s): got %+v, want state=%s hold=%v rule=%s", tc.file, tc.title, res, tc.state, tc.hold, tc.rule)
			for _, r := range m.Explain(NewScreen(string(data), tc.title)) {
				t.Logf("  matched %s p=%d state=%s", r.RuleID, r.Priority, r.State)
			}
		}
	}
}

func TestCopilotFixtures(t *testing.T) {
	m := Load("", false).Get("copilot")
	sel, _ := os.ReadFile("testdata/copilot_selection.txt")
	if res := m.Evaluate(NewScreen(string(sel), "")); !res.Matched || res.State != agent.Blocked {
		t.Errorf("copilot selection: %+v", res)
	}
	work, _ := os.ReadFile("testdata/copilot_working.txt")
	if res := m.Evaluate(NewScreen(string(work), "")); !res.Matched || res.State != agent.Working {
		t.Errorf("copilot working: %+v", res)
	}
	if res := m.Evaluate(NewScreen("$ ls\nfoo bar\n", "")); res.Matched {
		t.Errorf("plain shell text must not match: %+v", res)
	}
}

func TestRegions(t *testing.T) {
	s := NewScreen("a\n\nb\n────\nc\n❯ hi\n────\nfoot\n\n", "T")
	if got := s.Region("bottom_non_empty_lines(2)"); len(got) != 2 || got[0] != "────" || got[1] != "foot" {
		t.Errorf("bottom: %q", got)
	}
	if got := s.Region("prompt_box_body"); len(got) != 2 || got[1] != "❯ hi" {
		t.Errorf("prompt box: %q", got)
	}
	if got := s.Region("last_non_empty_above_prompt_box"); len(got) != 1 || got[0] != "b" {
		t.Errorf("above prompt box: %q", got)
	}
	if got := s.Region("after_last_horizontal_rule"); len(got) != 1 || got[0] != "foot" {
		t.Errorf("after rule: %q", got)
	}
	if got := s.Region("top_non_empty_lines(2)"); len(got) != 2 || got[1] != "b" {
		t.Errorf("top: %q", got)
	}
	if got := s.Region("osc_title"); got[0] != "T" {
		t.Errorf("title: %q", got)
	}
	if got := s.Region("osc_progress"); got != nil {
		t.Errorf("unsupported region should be nil: %q", got)
	}
}

func TestUserOverrideWins(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "claude.toml"), []byte(`id = "claude"
[[rules]]
id = "custom"
state = "blocked"
priority = 5000
region = "whole_recent"
contains = ["magic word"]
`), 0o644)
	set := Load(dir, false)
	res := set.Get("claude").Evaluate(NewScreen("please say the magic word", ""))
	if !res.Matched || res.RuleID != "custom" {
		t.Fatalf("override: %+v", res)
	}
}
