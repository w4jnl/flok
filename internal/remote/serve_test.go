package remote

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/poller"
	"github.com/w4jnl/flok/internal/remote/proto"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux/tmuxtest"
)

func TestServeSession(t *testing.T) {
	dir := t.TempDir()
	st := state.New(dir)
	cfg := config.Default()
	cfg.Sounds.Enabled = false
	cfg.Sidebar.PollMs = 200
	ft := &tmuxtest.Fake{Screens: map[string]string{}}
	inR, inW := io.Pipe()   // local → serve
	outR, outW := io.Pipe() // serve → local
	var sound func(pane, kind string)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		done <- Serve(ctx, ServeDeps{
			Hello: proto.Hello{Proto: proto.Version, Hostname: "beta"},
			NewPoller: func(s func(pane, kind string)) *poller.Poller {
				sound = s
				return poller.New(poller.Deps{Cfg: cfg, Tmux: ft, Store: st, Sound: s})
			},
			Inner: ft, Store: st, In: inR, Out: outW, Heartbeat: 300 * time.Millisecond,
		})
	}()
	fromServe := make(chan proto.Frame, 64) // drained continuously, like ssh would
	go func() {
		rd := proto.NewReader(outR)
		for {
			f, err := rd.Next()
			if err != nil {
				return
			}
			fromServe <- f
		}
	}()
	next := func() proto.Frame {
		t.Helper()
		select {
		case f := <-fromServe:
			return f
		case <-time.After(3 * time.Second):
			t.Fatal("serve sent nothing")
		}
		return proto.Frame{}
	}
	if f := next(); f.Type != proto.TypeHello || f.Hello.Hostname != "beta" {
		t.Fatalf("hello first, got %+v", f)
	}
	if f := next(); f.Type != proto.TypeSnap || f.Snap == nil {
		t.Fatalf("then the first merge, got %+v", f)
	}
	if f := next(); f.Type != proto.TypeSnap { // the heartbeat repeats an unchanged view
		t.Fatalf("heartbeat, got %+v", f)
	}
	_ = proto.Write(inW, proto.Frame{Type: proto.TypePing})
	for f := next(); ; f = next() {
		if f.Type == proto.TypePong {
			break
		}
		if f.Type != proto.TypeSnap {
			t.Fatalf("waiting for pong, got %+v", f)
		}
	}
	_ = proto.Write(inW, proto.Frame{Type: proto.TypeVisible, On: false})
	_ = proto.Write(inW, proto.Frame{Type: proto.TypeSeen, Pane: "%7"})
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, seenErr := os.Stat(filepath.Join(dir, "seen", "7.json"))
		if seenErr == nil && !st.TerminalFocused() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("seen mark (%v) and visible=false (focused=%v) must land", seenErr, st.TerminalFocused())
		}
		time.Sleep(20 * time.Millisecond)
	}
	sound("%7", "done")
	_ = proto.Write(inW, proto.Frame{Type: proto.TypeGoto, Goto: &proto.Goto{Session: "$1", Pane: "%7"}})
	var sawEvent, sawGotoErr bool
	for i := 0; i < 20 && !(sawEvent && sawGotoErr); i++ {
		switch f := next(); f.Type {
		case proto.TypeEvent:
			sawEvent = f.Event.Pane == "%7" && f.Event.Kind == "done"
		case proto.TypeError:
			sawGotoErr = f.Error == "goto: no inner tmux client to drive"
		}
	}
	if !sawEvent || !sawGotoErr {
		t.Fatalf("event=%v gotoErr=%v", sawEvent, sawGotoErr)
	}
	_ = inW.Close() // the local side went away
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve must end on EOF")
	}
}
