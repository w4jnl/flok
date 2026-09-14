package bar

import (
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/snapshot"
)

func TestTitleHeaderRows(t *testing.T) {
	now := time.Now()
	o := Options{Animate: true, Badge: true}
	empty := snapshot.Snapshot{}
	if Title(empty, snapshot.Fresh, 0, o) != "○" || Header(empty, snapshot.Fresh) != "flok · 0 agents" {
		t.Fatalf("empty: %q %q", Title(empty, snapshot.Fresh, 0, o), Header(empty, snapshot.Fresh))
	}
	if Title(empty, snapshot.Gone, 0, o) != "–" || Header(empty, snapshot.Gone) != "flok not running" {
		t.Fatal("gone rendering")
	}
	s := snapshot.Snapshot{Agents: []snapshot.Agent{
		{PaneID: "%1", Name: "flok", Kind: "claude", State: agent.Working, Tool: "Bash", StateSince: now.Add(-42 * time.Second)},
		{PaneID: "%2", Name: "mwrelay", Kind: "claude", State: agent.Blocked, Reason: "permission:Bash", StateSince: now},
		{PaneID: "%3", Name: "journal", Kind: "claude", State: agent.Done, Unseen: 2, StateSince: now.Add(-time.Minute)},
		{PaneID: "%4", Name: "idle-one", Kind: "copilot", State: agent.Idle},
	}}
	if got := Title(s, snapshot.Fresh, 2, o); got != "◑ ● 2" {
		t.Fatalf("title %q", got)
	}
	if got := Title(s, snapshot.Fresh, 2, Options{Animate: false, Badge: false}); got != "◐" {
		t.Fatalf("static title %q", got)
	}
	if got := Header(s, snapshot.Fresh); got != "flok · 4 agents · 2 waiting" {
		t.Fatalf("header %q", got)
	}
	rows := Rows(s, now, 0)
	order := ""
	for _, r := range rows {
		order += r.PaneID
	}
	if order != "%2%3%1%4" {
		t.Fatalf("order %s", order)
	}
	if rows[0].Label != "mwrelay · claude" || rows[0].Detail != "perm:Bash" || !rows[0].Attention {
		t.Fatalf("blocked row %+v", rows[0])
	}
	if rows[1].Detail != "done · 2" || rows[2].Detail != "Bash 0:42" || rows[3].Detail != "idle" || rows[3].Attention {
		t.Fatalf("rows %+v", rows)
	}
	if len(Rows(s, now, 2)) != 2 {
		t.Fatal("max rows")
	}
}

func TestTitleRunsColours(t *testing.T) {
	s := snapshot.Snapshot{Agents: []snapshot.Agent{{PaneID: "%1", State: agent.Working}, {PaneID: "%2", State: agent.Blocked}}}
	runs := TitleRuns(s, snapshot.Fresh, 1, Options{Animate: true, Badge: true})
	if len(runs) != 2 || runs[0].Color != "working" || runs[1].Color != "attention" || runs[1].Text != " ● 1" {
		t.Fatalf("runs: %+v", runs)
	}
	if Join(runs) != Title(s, snapshot.Fresh, 1, Options{Animate: true, Badge: true}) {
		t.Fatalf("runs must spell the plain title: %q vs %q", Join(runs), Title(s, snapshot.Fresh, 1, Options{Animate: true, Badge: true}))
	}
	idle := TitleRuns(snapshot.Snapshot{Agents: []snapshot.Agent{{PaneID: "%1", State: agent.Idle}}}, snapshot.Fresh, 0, Options{Badge: true})
	if len(idle) != 1 || idle[0].Color != "" || idle[0].Text != "○" {
		t.Fatalf("idle: %+v", idle)
	}
	if gone := TitleRuns(snapshot.Snapshot{}, snapshot.Gone, 0, Options{}); len(gone) != 1 || gone[0].Text != "–" {
		t.Fatalf("gone: %+v", gone)
	}
	for _, tok := range []string{"working", "attention", "done", "idle"} {
		if _, ok := Palette[tok]; !ok {
			t.Fatalf("palette lacks %s", tok)
		}
	}
}
