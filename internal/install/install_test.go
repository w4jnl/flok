package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeSettingsIdempotentAndPreserving(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	os.WriteFile(p, []byte(`{"model":"x","hooks":{"SessionStart":[{"matcher":"*","hooks":[{"type":"command","command":"bash herdr.sh session","timeout":10}]}]}}`), 0o600)
	changed, err := ClaudeSettings(p, "/usr/local/bin/flok")
	if err != nil || !changed {
		t.Fatalf("first install: changed=%v err=%v", changed, err)
	}
	changed, err = ClaudeSettings(p, "/usr/local/bin/flok")
	if err != nil || changed {
		t.Fatalf("second install should be a no-op: changed=%v err=%v", changed, err)
	}
	data, _ := os.ReadFile(p)
	var s map[string]any
	json.Unmarshal(data, &s)
	if s["model"] != "x" {
		t.Fatal("foreign setting lost")
	}
	hooks := s["hooks"].(map[string]any)
	ss := hooks["SessionStart"].([]any)
	if len(ss) != 2 {
		t.Fatalf("herdr hook must be preserved next to ours: %v", ss)
	}
	if len(ClaudeMissing(p)) != 0 {
		t.Fatalf("missing: %v", ClaudeMissing(p))
	}
	if m := ClaudeMissing(filepath.Join(dir, "nope.json")); len(m) != len(ClaudeEvents) {
		t.Fatal("missing file should report all events")
	}
	// binary moved -> repaired
	changed, _ = ClaudeSettings(p, "/opt/flok")
	if !changed {
		t.Fatal("moved binary should update commands")
	}
	cp := filepath.Join(dir, "copilot", "flok.json")
	if ch, err := CopilotHooks(cp, "/opt/flok"); err != nil || !ch {
		t.Fatalf("copilot: %v %v", ch, err)
	}
	legacy := filepath.Join(filepath.Dir(cp), "tmux-herdr.json")
	os.WriteFile(legacy, []byte("{}"), 0o644)
	if ch, _ := CopilotHooks(cp, "/opt/flok"); ch {
		t.Fatal("copilot second write should be a no-op")
	}
	if _, err := os.Stat(legacy); err == nil {
		t.Fatalf("legacy tmux-herdr.json must be removed")
	}
}

// The snippet lives in the user's inner tmux.conf, which must load on every tmux flok supports:
// the help binding goes through `flok keys --open`, which picks a popup or a window itself.
func TestTmuxSnippetIsVersionIndependent(t *testing.T) {
	snip := TmuxSnippet("/usr/local/bin/flok")
	if strings.Contains(snip, "display-popup") || !strings.Contains(snip, `keys --open --client '#{client_tty}'`) {
		t.Fatalf("snippet: %s", snip)
	}
}
