package ui

import (
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/push"
	"github.com/w4jnl/flok/internal/remote"
)

type recorder struct{ got []push.Event }

func (r *recorder) Send(ev push.Event) { r.got = append(r.got, ev) }

// Blocked, done and error transitions on any server become one event each; a start announces
// nothing, a repeat within the gap is dropped, an unwanted kind is skipped.
func TestNotifyTransitions(t *testing.T) {
	m, _ := newTestModel(t)
	rec := &recorder{}
	m.pusher = rec
	m.d.Cfg.Link.Name = "home"
	agents := func(local, beta agent.State) {
		m.local = merge.Snapshot{Agents: []agent.Agent{{PaneID: "%1", Name: "api", State: local, Reason: map[bool]string{true: "permission:Bash", false: ""}[local == agent.Blocked]}}}
		m.hostList = m.hostList[:0]
		m.remotes = map[string]hostView{}
		if beta != "" {
			m.hostList = append(m.hostList, hosts.Host{Name: "beta", Enabled: true})
			m.remotes["beta"] = hostView{state: remote.Connected, hasSnap: true, snap: merge.Snapshot{Agents: []agent.Agent{{PaneID: "%3", Name: "docs", State: beta}}}}
		}
		m.refederate()
	}
	agents(agent.Blocked, agent.Working) // the first view only primes
	if len(rec.got) != 0 {
		t.Fatalf("start announces nothing, got %+v", rec.got)
	}
	agents(agent.Working, agent.Done) // beta's docs finished
	if len(rec.got) != 1 || rec.got[0].Kind != "done" || rec.got[0].Host != "beta" || rec.got[0].Pane != "beta:%3" || rec.got[0].Instance != "home" {
		t.Fatalf("done on beta: %+v", rec.got)
	}
	agents(agent.Blocked, agent.Done) // api asks; docs unchanged
	if len(rec.got) != 2 || rec.got[1].Kind != "blocked" || rec.got[1].Reason != "permission:Bash" || rec.got[1].Agent != "api" {
		t.Fatalf("blocked: %+v", rec.got)
	}
	agents(agent.Working, agent.Done)
	agents(agent.Blocked, agent.Done) // the same block again within the gap: dropped
	if len(rec.got) != 2 {
		t.Fatalf("repeat within the gap: %+v", rec.got)
	}
	m.d.Cfg.Notify.Events = []string{"blocked"}
	m.pushLast = map[string]time.Time{}
	agents(agent.Working, agent.Working)
	agents(agent.Working, agent.Done) // done is not wanted now
	if len(rec.got) != 2 {
		t.Fatalf("unwanted kind: %+v", rec.got)
	}
}
