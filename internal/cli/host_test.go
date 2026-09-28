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
	run(2, "add")                                 // missing target
	run(2, "add", "delta", "d", "extra")          // one positional too many
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
	run(0, "set", "gamma", "--mode", "full", "--socket", "", "--flok", "/opt/homebrew/bin/flok")
	run(1, "set", "gamma", "--mode", "ssh") // invalid, nothing written
	run(1, "set", "nope", "--mode", "plain")
	run(2, "set", "gamma")           // nothing to change
	run(2, "set", "gamma", "--mode") // flag without value
	run(2, "set", "gamma", "--port", "22")
	set, _ = hosts.Load(dir)
	if g := set.Hosts[1]; g.Mode != hosts.ModeFull || g.Socket != "" || g.Flok != "/opt/homebrew/bin/flok" || g.Target != "gamma" {
		t.Fatalf("set: %+v", g)
	}
	run(0, "set", "gamma", "--mode", "plain")
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
	// rotation: local, then the enabled hosts; the front comes from the snapshot, "last" from runtime.json
	t.Setenv("FLOK_STATE", dir)
	run(0, "connect", "beta")
	run(0, "front", "2")
	run(1, "front", "3")
	run(0, "next")
	run(0, "prev")
	run(1, "last") // nothing recorded yet
	_ = os.WriteFile(filepath.Join(dir, "runtime.json"), []byte(`{"previous_front":"beta"}`), 0o644)
	run(0, "last")
	reqs = state.New(dir).DrainRequests(time.Now(), time.Minute)
	var hostsSeen []string
	for _, r := range reqs {
		hostsSeen = append(hostsSeen, r.Cmd+":"+r.Host)
	}
	if strings.Join(hostsSeen, " ") != "front:beta front:beta front:beta front:beta" {
		t.Fatalf("rotation requests %v", hostsSeen)
	}
}

// An ssh alias is usable as the name in its own spelling; every command finds the host in any
// case and answers with the registered spelling, and the target reaches ssh as typed.
func TestHostNamesAnyCase(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	c := hostCmd{dir: dir, now: time.Now, out: &out, errw: &errw}
	run := func(want int, args ...string) string {
		t.Helper()
		out.Reset()
		errw.Reset()
		if rc := c.run(args); rc != want {
			t.Fatalf("flok host %v: rc %d want %d\nstdout: %s\nstderr: %s", args, rc, want, out.String(), errw.String())
		}
		return out.String() + errw.String()
	}
	if s := run(0, "add", "dockerAMS", "--mode", "plain"); !strings.HasPrefix(s, "added dockerAMS (dockerAMS, plain)") {
		t.Fatalf("one-argument add names the host after the target: %q", s)
	}
	if s := run(1, "add", "dockerams", "other"); !strings.Contains(s, `"dockerAMS" exists`) {
		t.Fatalf("case-only duplicate: %q", s)
	}
	if s := run(0, "add", "jaro@beta.example.org"); !strings.HasPrefix(s, "added beta (jaro@beta.example.org, full)") {
		t.Fatalf("derived from a DNS name: %q", s)
	}
	if s := run(1, "add", "docker.ams", "x"); !strings.Contains(s, "e.g. docker-ams") {
		t.Fatalf("invalid name suggestion: %q", s)
	}
	if s := run(1, "add", "user@2001:db8::1"); !strings.Contains(s, "give it a name") {
		t.Fatalf("IPv6 target without a name: %q", s)
	}
	run(1, "add", "Local", "x")
	if s := run(0, "disconnect", "DOCKERams"); !strings.HasPrefix(s, "dockerAMS disabled") {
		t.Fatalf("disconnect: %q", s)
	}
	if s := run(0, "connect", "dockerams"); !strings.HasPrefix(s, "dockerAMS enabled") {
		t.Fatalf("connect: %q", s)
	}
	if s := run(0, "set", "DockerAms", "--session", "work"); s != "dockerAMS: dockerAMS, plain\n" {
		t.Fatalf("set: %q", s)
	}
	if s := run(0, "list", "--names"); s != "dockerAMS\nbeta\n" {
		t.Fatalf("names: %q", s)
	}
	set, _ := hosts.Load(dir)
	if h := set.Hosts[0]; h.Name != "dockerAMS" || h.Target != "dockerAMS" || !h.Enabled || h.Session != "work" {
		t.Fatalf("registered: %+v", h)
	}
	for arg, want := range map[string]string{"DOCKERAMS": "dockerAMS", "Beta": "beta", "LOCAL": "", "2": "dockerAMS"} {
		if got, err := c.resolveServer(arg); err != nil || got != want {
			t.Errorf("resolveServer(%q) = %q, %v; want %q", arg, got, err, want)
		}
	}
	cache := hosts.Dir(dir, "dockerAMS")
	_ = os.MkdirAll(filepath.Join(cache, "agents"), 0o755)
	if s := run(0, "remove", "dockerams"); s != "removed dockerAMS\n" {
		t.Fatalf("remove: %q", s)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("remove prunes the registered spelling's cache dir, whatever the case typed")
	}
}
