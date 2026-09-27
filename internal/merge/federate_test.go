package merge

import (
	"reflect"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
)

func TestFederateIsIdentityForOneHost(t *testing.T) {
	now := time.Now()
	local := Snapshot{
		Spaces: []agent.Space{{SessionID: "$1", SessionName: "api", Current: true, Rollup: agent.Working, AgentCount: 1}},
		Agents: []agent.Agent{{PaneID: "%1", SessionID: "$1", SessionName: "api", State: agent.Working, StateSince: now}},
		Focus:  Focus{ClientTTY: "/dev/ttys1", SessionID: "$1", PaneID: "%1", Found: true},
		Unseen: 0, Warnings: []string{"driving most recent client"}, TakenAt: now,
		NewlySeen: []string{"%1"},
	}
	got := Federate(local, nil, "")
	want := local
	want.NewlySeen = nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("one host must federate to itself (minus write-back fields):\n got %+v\nwant %+v", got, want)
	}
}

func TestFederateTagsSortsAndFocuses(t *testing.T) {
	now := time.Now()
	local := Snapshot{
		Spaces: []agent.Space{{SessionID: "$1", SessionName: "api", Current: true, Rollup: agent.Idle, AgentCount: 1}},
		Agents: []agent.Agent{{PaneID: "%1", SessionID: "$1", SessionName: "api", State: agent.Idle, StateSince: now}},
		Focus:  Focus{ClientTTY: "/dev/ttys1", SessionID: "$1", PaneID: "%1", Found: true},
	}
	beta := Snapshot{
		Spaces: []agent.Space{{SessionID: "$1", SessionName: "web", Current: true, Rollup: agent.Blocked, AgentCount: 2}},
		Agents: []agent.Agent{
			{PaneID: "%1", SessionID: "$1", SessionName: "web", State: agent.Blocked, StateSince: now},
			{PaneID: "%2", SessionID: "$1", SessionName: "web", State: agent.Working, StateSince: now},
		},
		Focus:    Focus{ClientTTY: "/dev/pts/3", SessionID: "$1", PaneID: "%2", Found: true},
		Unseen:   1,
		Warnings: []string{"no client attached"},
	}
	gamma := Snapshot{
		Agents: []agent.Agent{{PaneID: "%1", SessionID: "$4", SessionName: "web", State: agent.Working, StateSince: now}},
		Spaces: []agent.Space{{SessionID: "$4", SessionName: "web", Current: true, Rollup: agent.Working, AgentCount: 1}},
	}
	got := Federate(local, []HostSnapshot{{"beta", beta}, {"gamma", gamma}}, "beta")

	// attention order across hosts: blocked first, then the two working ones grouped by host, idle last
	var order []string
	for _, a := range got.Agents {
		order = append(order, agent.PaneRef{Host: a.Host, ID: a.PaneID}.String())
	}
	if want := []string{"beta:%1", "beta:%2", "gamma:%1", "%1"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("agent order %v, want %v", order, want)
	}
	// only the front host's current space stays current; focus is the front host's
	var current []string
	for _, sp := range got.Spaces {
		if sp.Current {
			current = append(current, sp.Host)
		}
	}
	if !reflect.DeepEqual(current, []string{"beta"}) || got.Focus.Host != "beta" || got.Focus.PaneID != "%2" || got.Focus.ClientTTY != "/dev/pts/3" {
		t.Fatalf("current=%v focus=%+v", current, got.Focus)
	}
	if got.Unseen != 1 || !reflect.DeepEqual(got.Warnings, []string{"beta: no client attached"}) {
		t.Fatalf("unseen=%d warnings=%v", got.Unseen, got.Warnings)
	}
	if got.NewlySeen != nil || got.Corrections != nil || got.StaleHooks != nil {
		t.Fatal("write-back fields must stay nil")
	}
	// the local host as front keeps the local focus
	if f := Federate(local, []HostSnapshot{{"beta", beta}}, "").Focus; f.Host != "" || f.PaneID != "%1" {
		t.Fatalf("local front focus %+v", f)
	}
}
