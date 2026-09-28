package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/state"
)

// A remote goto files a request with the host's registered spelling, whatever case was typed:
// the sidebar compares hosts exactly.
func TestGotoResolvesHostSpelling(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLOK_STATE", dir)
	if err := hosts.Save(dir, hosts.Set{Hosts: []hosts.Host{{Name: "dockerAMS", Target: "dockerAMS", Mode: hosts.ModeFull, Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	stop := fakeSidebar(t, dir, false)
	defer stop()
	if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte(`{"tmux_version":"3.4"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	for _, arg := range []string{"dockerams:%3", "DOCKERAMS:%4", "dockerAMS:%5"} {
		if rc := runGoto(config.Config{}, []string{arg, "--no-focus"}); rc != 0 {
			t.Fatalf("goto %s: rc %d", arg, rc)
		}
	}
	reqs := state.New(dir).DrainRequests(time.Now(), time.Minute)
	if len(reqs) != 3 {
		t.Fatalf("requests %+v", reqs)
	}
	for i, r := range reqs {
		if r.Cmd != "goto" || r.Host != "dockerAMS" || r.Pane != []string{"%3", "%4", "%5"}[i] {
			t.Errorf("request %d: %+v", i, r)
		}
	}
}
