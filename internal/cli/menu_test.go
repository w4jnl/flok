package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/snapshot"
)

func TestMenuItems(t *testing.T) {
	now := time.Now()
	s := snapshot.Snapshot{FrontHost: "beta", Focus: snapshot.Focus{Host: "beta", SessionName: "api"},
		Agents: []snapshot.Agent{
			{PaneID: "%1", Name: "proj", Kind: "claude", State: agent.Blocked, Reason: "permission:Bash", Unseen: 1, StateSince: now},
			{PaneID: "%7", Host: "beta", Name: "api", Kind: "claude", State: agent.Working, StateSince: now},
		},
		Sessions: []snapshot.Session{
			{ID: "$1", Name: "Alpha", Rollup: agent.Idle, Agents: 1},
			{ID: "$3", Host: "beta", Name: "api", Rollup: agent.Working, Agents: 2},
			{ID: "$4", Host: "beta", Name: "scratch"},
		},
		Hosts: []snapshot.Host{{Name: "beta", Mode: "full", State: "connected", Agents: 1, Front: true}, {Name: "gamma", Mode: "plain", State: "unreachable"}},
	}
	ag := agentMenuItems(s, now, "/x/flok")
	if len(ag) != 5 || ag[0].Key != "1" || !strings.HasPrefix(ag[0].Name, "● proj · claude") || ag[0].Cmd != "run-shell -b '/x/flok goto %1 --no-focus'" ||
		ag[1].Key != "2" || !strings.Contains(ag[1].Name, "api · claude @beta") || ag[1].Cmd != "run-shell -b '/x/flok goto beta:%7 --no-focus'" ||
		ag[2].Name != "" || ag[3].Name != "sessions…" || ag[3].Key != "S" || ag[4].Name != "servers…" || ag[4].Key != "H" {
		t.Fatalf("agents: %+v", ag)
	}
	se := sessionMenuItems(s, "/x/flok")
	if len(se) != 5 || se[0].Name != "▸ ◐ api · 2 agents" || se[0].Key != "1" || se[0].Cmd != "run-shell -b '/x/flok goto beta:$3 --no-focus'" ||
		se[1].Name != "    scratch" || se[1].Key != "2" || se[2].Name != "" || se[3].Name != "agents…" || se[4].Name != "servers…" {
		t.Fatalf("sessions (front host only, current marked): %+v", se)
	}
	s.FrontHost, s.Focus = "", snapshot.Focus{SessionName: "Alpha"}
	if se = sessionMenuItems(s, "/x/flok"); len(se) != 4 || se[0].Name != "▸ ○ Alpha · 1 agent" || se[0].Cmd != "run-shell -b '/x/flok goto $1 --no-focus'" {
		t.Fatalf("local sessions: %+v", se)
	}
	sv := serverMenuItems(s, []string{"", "beta", "gamma"}, "/x/flok")
	if len(sv) != 6 || sv[0].Name != "▸ local" || sv[0].Key != "1" || sv[1].Name != "  beta · full · 1" || sv[1].Cmd != "run-shell -b '/x/flok host front 2'" ||
		sv[2].Name != "  gamma · unreachable" || sv[3].Name != "" || sv[4].Key != "A" || sv[5].Key != "S" {
		t.Fatalf("servers: %+v", sv)
	}
	if e := agentMenuItems(snapshot.Snapshot{}, now, "/x/flok"); len(e) != 4 || e[0].Name != "  no agents" {
		t.Fatalf("empty: %+v", e)
	}
}
