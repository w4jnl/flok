package notes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
)

// fakeNT writes what it gets on stdin to dir/import.json, or fails when dir/fail exists.
func fakeNT(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "nt")
	script := "#!/bin/sh\n[ \"$1\" = agent ] && [ \"$2\" = import ] || exit 3\n[ \"$3\" = --probe ] && { echo 1; exit 0; }\n[ -e \"" + dir + "/fail\" ] && { echo 'the notes repository is locked' >&2; exit 1; }\ncat > \"" + dir + "/import.json\"\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestObserveImports(t *testing.T) {
	dir, state := t.TempDir(), t.TempDir()
	nt := fakeNT(t, dir)
	var logged []string
	logf := func(f string, a ...any) { logged = append(logged, f) }
	now := time.Now()
	spawned := 0
	Spawn = func(session, nt string) error { // the worker, in-process and at once
		spawned++
		Finish(state, session, nt, now, logf)
		return nil
	}
	defer func() { Spawn = nil }()
	repo, _ := os.Getwd() // a git repository: the worker's branch and head come from it
	ev := agent.Event{Kind: agent.EvSessionStart, Name: "SessionStart", Agent: "claude", AgentSessionID: "s1", Cwd: repo}
	Observe(state, "%1", ev, nt, now, logf)
	data, err := os.ReadFile(filepath.Join(dir, "import.json"))
	if err != nil {
		t.Fatal("nothing imported:", err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil || rec.Session != "s1" || rec.Agent != "claude-code" || rec.Version != Version {
		t.Fatalf("record %s %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(state, "sessions", "s1.json")); err != nil {
		t.Fatal("no journal:", err)
	}
	if j, _ := Load(state, "s1"); j.ImportedAt.IsZero() || j.Pane != "%1" {
		t.Fatalf("journal after import: %+v", j)
	}
	os.Remove(filepath.Join(dir, "import.json"))
	Observe(state, "%1", agent.Event{Kind: agent.EvToolStart, Name: "PreToolUse", Agent: "claude", AgentSessionID: "s1", Tool: "Bash"}, nt, now.Add(time.Second), logf)
	if _, err := os.Stat(filepath.Join(dir, "import.json")); err == nil {
		t.Fatal("a tool call imported")
	}
	// a failure is logged once
	os.WriteFile(filepath.Join(dir, "fail"), nil, 0o644)
	Observe(state, "%1", agent.Event{Kind: agent.EvStop, Name: "Stop", Agent: "claude", AgentSessionID: "s1", Message: "hi"}, nt, now.Add(2*time.Second), logf)
	Observe(state, "%1", agent.Event{Kind: agent.EvStop, Name: "Stop", Agent: "claude", AgentSessionID: "s1", Message: "hi"}, nt, now.Add(3*time.Second), logf)
	if len(logged) != 1 {
		t.Fatalf("logged %d times: %v", len(logged), logged)
	}
	os.Remove(filepath.Join(dir, "fail"))
	Observe(state, "%1", agent.Event{Kind: agent.EvSessionEnd, Name: "SessionEnd", Agent: "claude", AgentSessionID: "s1"}, nt, now.Add(4*time.Second), logf)
	data, _ = os.ReadFile(filepath.Join(dir, "import.json"))
	if err := json.Unmarshal(data, &rec); err != nil || rec.Ended.IsZero() || rec.Turns != 2 {
		t.Fatalf("final record %s", data)
	}
	if last := LastImport(state); last.IsZero() {
		t.Fatal("LastImport")
	}
	if spawned != 4 { // start, stop, stop, end
		t.Fatalf("spawned %d workers", spawned)
	}
	if rec.Head == "" { // the worker read the git state of the session's directory (this repository; CI checks out a detached HEAD, so no branch)
		t.Fatalf("git state missing: %+v", rec)
	}
	// off: the journal is kept, nothing runs
	Observe(state, "%2", agent.Event{Kind: agent.EvSessionStart, Name: "SessionStart", Agent: "claude", AgentSessionID: "s2"}, "", now, logf)
	if j, _ := Load(state, "s2"); j.Record.Created.IsZero() || !j.ImportedAt.IsZero() || !j.GitProbed {
		t.Fatalf("off: %+v", j)
	}
	if spawned != 5 { // the worker still runs for the git state, it just does not import
		t.Fatalf("spawned %d workers", spawned)
	}
	if v, err := Probe(nt); err != nil || v != 1 {
		t.Fatalf("probe %d %v", v, err)
	}
}

func TestPrune(t *testing.T) {
	state := t.TempDir()
	now := time.Now()
	Update(state, "old", func(j *Journal) bool {
		j.Record = Record{Created: now.Add(-20 * 24 * time.Hour), Updated: now.Add(-16 * 24 * time.Hour), Ended: now.Add(-16 * 24 * time.Hour)}
		return true
	})
	Update(state, "stale", func(j *Journal) bool {
		j.Record = Record{Created: now.Add(-40 * 24 * time.Hour), Updated: now.Add(-30 * 24 * time.Hour)}
		return true
	})
	Update(state, "live", func(j *Journal) bool {
		j.Record = Record{Created: now.Add(-20 * 24 * time.Hour), Updated: now.Add(-time.Hour)}
		return true
	})
	Prune(state, now)
	if got := len(All(state)); got != 1 {
		t.Fatalf("%d journals left", got)
	}
}

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	nt := fakeNT(t, dir)
	t.Setenv("PATH", dir)
	if Resolve("off") != "" || Resolve("") != "" {
		t.Fatal("off")
	}
	if Resolve("auto") != nt {
		t.Fatalf("auto: %q", Resolve("auto"))
	}
	if Resolve(nt) != nt || Resolve(filepath.Join(dir, "none")) != "" {
		t.Fatal("path")
	}
}
