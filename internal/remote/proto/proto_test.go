package proto

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/merge"
)

func TestRoundTripAndNoise(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("Welcome to beta!\n\n")                          // a chatty profile
	buf.WriteString(`{"type":"future","extra":{"nested":1}}` + "\n") // unknown type and fields
	buf.WriteString("not json {\n")
	frames := []Frame{
		{Type: TypeHello, Hello: &Hello{Proto: 1, Version: "0.5.0", Hostname: "beta", PID: 7, TmuxVersion: "3.4"}},
		{Type: TypeSnap, Snap: &Snapshot{Agents: []agent.Agent{{PaneID: "%3", State: agent.Blocked}}, Unseen: 1}},
		{Type: TypeEvent, Event: &Event{Pane: "%3", Kind: "blocked"}},
		{Type: TypeGoto, Goto: &Goto{Session: "$1", Pane: "%3"}},
		{Type: TypeSeen, Pane: "%3"},
		{Type: TypeVisible, On: true},
		{Type: TypePing},
		{Type: TypeError, Error: "already served (pid 9)"},
	}
	for _, f := range frames {
		if err := Write(&buf, f); err != nil {
			t.Fatal(err)
		}
	}
	var noise []string
	r := NewReader(&buf)
	r.Noise = func(l string) { noise = append(noise, l) }
	if f, err := r.Next(); err != nil || f.Type != "future" {
		t.Fatalf("unknown types pass through: %+v %v", f, err)
	}
	for i, want := range frames {
		got, err := r.Next()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("frame %d:\n got %+v\nwant %+v", i, got, want)
		}
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("clean end: %v", err)
	}
	if !reflect.DeepEqual(noise, []string{"Welcome to beta!", "not json {"}) {
		t.Fatalf("noise %q", noise)
	}
}

func TestOversizedLine(t *testing.T) {
	r := NewReader(strings.NewReader("{" + strings.Repeat("x", MaxLine+1) + "}\n"))
	if _, err := r.Next(); err != ErrLineTooLong {
		t.Fatalf("got %v", err)
	}
}

func TestMergeConversion(t *testing.T) {
	now := time.Now()
	in := merge.Snapshot{
		Spaces:   []agent.Space{{SessionID: "$1", SessionName: "api", Path: "/p", Branch: "main", Attached: true, Current: true, Rollup: agent.Working, AgentCount: 1}},
		Agents:   []agent.Agent{{PaneID: "%1", SessionID: "$1", State: agent.Working, StateSince: now}},
		Focus:    merge.Focus{ClientTTY: "/dev/pts/1", SessionID: "$1", SessionName: "api", WindowID: "@1", PaneID: "%1", Found: true},
		Unseen:   2,
		Warnings: []string{"w"},
		TakenAt:  now,
		// write-back fields never travel
		NewlySeen: []string{"%1"},
	}
	out := FromMerge(in).ToMerge()
	in.NewlySeen = nil
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", out, in)
	}
}
