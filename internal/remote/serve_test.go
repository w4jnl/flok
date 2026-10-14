package remote

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/answer"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/merge"
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
			NewPoller: func(s func(pane, kind string), onStore, onRestart func()) *poller.Poller {
				sound = s
				return poller.New(poller.Deps{Cfg: cfg, Tmux: ft, Store: st, Sound: s, OnStoreEvent: onStore, OnServerRestart: onRestart})
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
	_ = st.WriteRequest(state.Request{Cmd: "toggle"}) // `flok relay toggle` on the host
	var sawEvent, sawGotoErr, sawRequest bool
	for i := 0; i < 30 && !(sawEvent && sawGotoErr && sawRequest); i++ {
		switch f := next(); f.Type {
		case proto.TypeEvent:
			sawEvent = f.Event.Pane == "%7" && f.Event.Kind == "done"
		case proto.TypeError:
			sawGotoErr = f.Error == "goto: no inner tmux client to drive"
		case proto.TypeRequest:
			sawRequest = f.Cmd == "toggle"
		}
	}
	if !sawEvent || !sawGotoErr || !sawRequest {
		t.Fatalf("event=%v gotoErr=%v request=%v", sawEvent, sawGotoErr, sawRequest)
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

// A file in an answer is saved in the attach directory and typed as its path (Copilot's
// @path); a long text goes through a buffer; without a saver files are refused.
func TestTypeAnswerFilesAndLongText(t *testing.T) {
	ft := &tmuxtest.Fake{Screens: map[string]string{}}
	snap := merge.Snapshot{Agents: []agent.Agent{{PaneID: "%1", Kind: "claude"}, {PaneID: "%2", Kind: "copilot"}}}
	dir := filepath.Join(t.TempDir(), "attach")
	f := answer.File{Name: "shot.png", Data: []byte("PNG...")}
	if err := TypeAnswer(ft, snap, answer.Answer{Pane: "%1", Text: "look", Keys: []string{"Enter"}, Files: []answer.File{f}}, answer.DirSaver(dir)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), "-shot.png") {
		t.Fatalf("attach dir: %v", entries)
	}
	path := filepath.Join(dir, entries[0].Name())
	if typed := strings.Join(ft.Calls[len(ft.Calls)-2], " "); typed != "send-keys -t %1 -l -- look "+path {
		t.Fatalf("typed: %q", typed)
	}
	if last := strings.Join(ft.Calls[len(ft.Calls)-1], " "); last != "send-keys -t %1 Enter" {
		t.Fatalf("keys: %q", last)
	}
	if err := TypeAnswer(ft, snap, answer.Answer{Pane: "%2", Files: []answer.File{f}}, answer.DirSaver(dir)); err != nil {
		t.Fatal(err)
	}
	if typed := strings.Join(ft.Calls[len(ft.Calls)-1], " "); !strings.HasPrefix(typed, "send-keys -t %2 -l -- @"+dir+"/") || !strings.HasSuffix(typed, "-shot.png") {
		t.Fatalf("copilot: %q", typed)
	}
	n := len(ft.Calls)
	if err := TypeAnswer(ft, snap, answer.Answer{Pane: "%1", Files: []answer.File{f}}, nil); err == nil || len(ft.Calls) != n {
		t.Fatalf("no saver: %v, %d calls", err, len(ft.Calls)-n)
	}
	if err := TypeAnswer(ft, snap, answer.Answer{Pane: "%9", Files: []answer.File{f}}, answer.DirSaver(dir)); err == nil || err.Error() != "no agent in pane %9" {
		t.Fatalf("no agent: %v", err)
	}
	if entries, _ = os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("a refused answer saved a file: %v", entries)
	}
	// two files: both saved, both paths typed, in order
	if err := TypeAnswer(ft, snap, answer.Answer{Pane: "%1", Text: "both", Files: []answer.File{f, {Name: "page.pdf", Data: []byte("%PDF")}}}, answer.DirSaver(dir)); err != nil {
		t.Fatal(err)
	}
	if entries, _ = os.ReadDir(dir); len(entries) != 4 {
		t.Fatalf("two more files: %v", entries)
	}
	// (typed as keys or pasted: two temp paths may or may not make the text long)
	if typed := strings.Join(ft.Calls[len(ft.Calls)-1], " "); !strings.Contains(typed, "-- both "+dir+"/") || !strings.Contains(typed, "-shot.png "+dir+"/") || !strings.Contains(typed, "-page.pdf") {
		t.Fatalf("two paths: %q", typed)
	}
	if err := TypeAnswer(ft, snap, answer.Answer{Pane: "%1", Text: "a\nb"}, nil); err != nil {
		t.Fatal(err)
	}
	if c := ft.Calls[len(ft.Calls)-1]; c[0] != "set-buffer" || c[4] != "a\nb" || c[6] != "paste-buffer" {
		t.Fatalf("long text: %q", c)
	}
}
