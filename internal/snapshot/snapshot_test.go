package snapshot

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/merge"
)

func TestPublishLoadAndFreshness(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "runtime.json"), []byte("{}"), 0o644)
	p := &Publisher{Dir: dir}
	now := time.Now()
	ms := merge.Snapshot{Unseen: 1, Focus: merge.Focus{SessionName: "Alpha", PaneID: "%1"},
		Agents: []agent.Agent{{PaneID: "%1", SessionName: "Alpha", Name: "proj", Kind: "claude", State: agent.Blocked, Reason: "permission:Bash", Unseen: 1, StateSince: now}},
		Spaces: []agent.Space{{SessionID: "$1", SessionName: "Alpha", Rollup: agent.Blocked, AgentCount: 1}}}
	if w, err := p.Publish(FromMerge(ms), now); err != nil || !w {
		t.Fatalf("first publish: %v %v", w, err)
	}
	if w, _ := p.Publish(FromMerge(ms), now.Add(time.Second)); w {
		t.Fatal("unchanged content within the heartbeat must not be rewritten")
	}
	if w, _ := p.Publish(FromMerge(ms), now.Add(6*time.Second)); !w {
		t.Fatal("heartbeat rewrite expected")
	}
	s, f := Load(dir, now.Add(7*time.Second))
	if f != Fresh || len(s.Agents) != 1 || s.Agents[0].State != agent.Blocked || s.Agents[0].Reason != "permission:Bash" || s.Sessions[0].Rollup != agent.Blocked {
		t.Fatalf("load: %+v fresh=%v", s, f)
	}
	if _, f := Load(dir, now.Add(30*time.Second)); f != Stale {
		t.Fatalf("stale expected, got %v", f)
	}
	os.Remove(filepath.Join(dir, "runtime.json"))
	if _, f := Load(dir, now.Add(7*time.Second)); f != Gone {
		t.Fatalf("gone expected without runtime.json, got %v", f)
	}
	Remove(dir)
	if _, f := Load(dir, now); f != Gone {
		t.Fatal("gone expected after Remove")
	}
}

func TestHeartbeatRepublishesLastSnapshot(t *testing.T) {
	p := &Publisher{Dir: t.TempDir()}
	now := time.Now()
	if ok, err := p.Publish(Snapshot{Unseen: 1}, now); err != nil || !ok {
		t.Fatalf("first publish: %v %v", ok, err)
	}
	if ok, _ := p.Heartbeat(now.Add(time.Second)); ok {
		t.Fatal("no heartbeat before the interval")
	}
	if ok, err := p.Heartbeat(now.Add(heartbeat + time.Second)); err != nil || !ok {
		t.Fatalf("heartbeat after the interval: %v %v", ok, err)
	}
	if s, _ := Load(p.Dir, now.Add(heartbeat+time.Second)); s.Unseen != 1 {
		t.Fatalf("heartbeat must rewrite the last snapshot, got %+v", s)
	}
}
