package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/state"
)

func TestHostCommandLifecycle(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	var out, errw bytes.Buffer
	c := hostCmd{dir: dir, now: func() time.Time { return at }, out: &out, errw: &errw}
	run := func(want int, args ...string) string {
		t.Helper()
		out.Reset()
		errw.Reset()
		if rc := c.run(args); rc != want {
			t.Fatalf("flok host %v: rc %d want %d\nstdout: %s\nstderr: %s", args, rc, want, out.String(), errw.String())
		}
		return out.String() + errw.String()
	}
	if s := run(0, "list"); !strings.Contains(s, "no remote hosts") {
		t.Fatalf("empty list: %q", s)
	}
	run(0, "add", "beta", "jaro@beta", "--session", "work")
	run(0, "add", "gamma", "gamma", "--mode", "plain", "--socket", "agents", "--disabled")
	run(1, "add", "beta", "other")                // duplicate
	run(1, "add", "local", "x")                   // reserved name
	run(2, "add", "delta", "-oProxyCommand=evil") // an option-like target never reaches the registry
	run(1, "add", "delta", "beta;rm")             // the validator refuses the rest
	run(2, "add", "delta")                        // missing target
	run(2, "add", "delta", "d", "--mode")         // flag without value
	run(2, "add", "delta", "d", "--port", "22")   // unknown flag
	run(2, "bogus")                               // unknown subcommand
	if s := run(0, "list", "--names"); s != "beta\ngamma\n" {
		t.Fatalf("names: %q", s)
	}
	var set hosts.Set
	if err := json.Unmarshal([]byte(run(0, "list", "--json")), &set); err != nil || len(set.Hosts) != 2 {
		t.Fatalf("json: %v %+v", err, set)
	}
	if b := set.Hosts[0]; b.Target != "jaro@beta" || b.Mode != hosts.ModeFull || !b.Enabled || b.Session != "work" || !b.AddedAt.Equal(at) {
		t.Fatalf("beta: %+v", b)
	}
	if g := set.Hosts[1]; g.Mode != hosts.ModePlain || g.Enabled || g.Socket != "agents" {
		t.Fatalf("gamma: %+v", g)
	}
	if s := run(0, "list"); !strings.Contains(s, "beta") || !strings.Contains(s, "never") || !strings.Contains(s, "no") {
		t.Fatalf("table: %q", s)
	}
	run(0, "disconnect", "beta")
	run(0, "connect", "gamma")
	run(1, "connect", "nope")
	set, _ = hosts.Load(dir)
	if set.Hosts[0].Enabled || !set.Hosts[1].Enabled {
		t.Fatalf("connect/disconnect: %+v", set.Hosts)
	}
	cache := hosts.Dir(dir, "gamma")
	_ = os.MkdirAll(filepath.Join(cache, "agents"), 0o755)
	run(0, "remove", "gamma")
	run(1, "remove", "gamma")
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("remove must prune the host's local cache")
	}
	if s := run(0, "list", "--names"); s != "beta\n" {
		t.Fatalf("after remove: %q", s)
	}
	if s := run(0, "--help"); !strings.Contains(s, "usage: flok host") {
		t.Fatalf("help: %q", s)
	}
	// front needs a live sidebar; with one it files a request the sidebar drains
	run(1, "front", "beta")
	run(1, "front", "nope")
	run(2, "front")
	stop := fakeSidebar(t, dir, false)
	defer stop()
	time.Sleep(30 * time.Millisecond)
	run(0, "front", "beta")
	run(0, "front", "local")
	reqs := state.New(dir).DrainRequests(time.Now(), time.Minute)
	if len(reqs) != 2 || reqs[0].Cmd != "front" || reqs[0].Host != "beta" || reqs[1].Host != "" {
		t.Fatalf("requests %+v", reqs)
	}
}
