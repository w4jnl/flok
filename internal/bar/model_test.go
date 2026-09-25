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
	if len(runs) != 2 || runs[0].Color != "" || runs[1].Color != "attention" || runs[1].Text != " ● 1" {
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

func TestTitleKeepAwake(t *testing.T) {
	o := Options{Animate: true, Badge: true}
	s := snapshot.Snapshot{KeepAwake: true, Agents: []snapshot.Agent{{PaneID: "%1", State: agent.Working}, {PaneID: "%2", State: agent.Blocked}}}
	if got := Title(s, snapshot.Fresh, 2, o); got != "◑ ⚡ ● 1" {
		t.Fatalf("title %q", got)
	}
	runs := TitleRuns(s, snapshot.Fresh, 2, o)
	if len(runs) != 3 || runs[1].Text != " "+KeepAwakeSign || runs[1].Color != "" || Join(runs) != Title(s, snapshot.Fresh, 2, o) {
		t.Fatalf("runs %+v", runs)
	}
	if got := Title(snapshot.Snapshot{KeepAwake: true}, snapshot.Fresh, 0, o); got != "○ ⚡" {
		t.Fatalf("idle title %q", got)
	}
	for _, f := range []snapshot.Freshness{snapshot.Stale, snapshot.Gone} { // not running: the dash stays alone
		if got := Title(s, f, 0, o); got != "–" {
			t.Fatalf("freshness %v: %q", f, got)
		}
	}
}

func TestSolidBlink(t *testing.T) {
	if Solid(false, true, false, 0) || Solid(false, true, false, 1) {
		t.Fatal("nothing pending: outline")
	}
	if !Solid(true, false, false, 1) || !Solid(true, true, true, 1) {
		t.Fatal("pending without blink, or with the terminal focused: solid, no motion")
	}
	if !Solid(true, true, false, 0) || Solid(true, true, false, 1) {
		t.Fatal("pending, unfocused, blink: alternate by phase")
	}
}

func TestIconColor(t *testing.T) {
	if IconColor(true, true) != "attention" || IconColor(false, true) != "working" || IconColor(false, false) != "" {
		t.Fatal("icon colour by state")
	}
	for tok := range Palette {
		if _, ok := PaletteLight[tok]; !ok {
			t.Fatalf("light palette lacks %s", tok)
		}
	}
}
