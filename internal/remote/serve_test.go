package remote

import (
	"context"
	"github.com/w4jnl/flok/internal/feed"
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
	"github.com/w4jnl/flok/internal/notes"
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

// The notes claim: a claimed serve streams the sessions' records (new and changed), refuses a
// second importer while the first is fresh, and yields to --take.
func TestServeNotes(t *testing.T) {
	dir := t.TempDir()
	st := state.New(dir)
	cfg := config.Default()
	cfg.Sounds.Enabled = false
	cfg.Sidebar.PollMs = 200
	ft := &tmuxtest.Fake{Screens: map[string]string{}}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeDeps{
			Hello: proto.Hello{Proto: proto.Version, Hostname: "beta", Features: proto.ServeFeatures},
			NewPoller: func(s func(pane, kind string), onStore, onRestart func()) *poller.Poller {
				return poller.New(poller.Deps{Cfg: cfg, Tmux: ft, Store: st, Sound: s, OnStoreEvent: onStore, OnServerRestart: onRestart})
			},
			Inner: ft, Store: st, In: inR, Out: outW, Heartbeat: time.Minute,
		})
	}()
	frames := make(chan proto.Frame, 64)
	go func() {
		rd := proto.NewReader(outR)
		for {
			f, err := rd.Next()
			if err != nil {
				return
			}
			frames <- f
		}
	}()
	next := func(typ string) proto.Frame {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case f := <-frames:
				if f.Type == typ {
					return f
				}
			case <-deadline:
				t.Fatalf("no %s frame", typ)
			}
		}
	}
	now := time.Now()
	write := func(session string, at time.Time) {
		t.Helper()
		if _, err := notes.Update(dir, session, func(j *notes.Journal) bool {
			j.Record.Version, j.Record.Agent, j.Record.Session, j.Record.Created, j.Record.Updated = notes.Version, "claude-code", session, at, at
			return true
		}); err != nil {
			t.Fatal(err)
		}
	}
	write("s1", now.Add(-time.Hour))
	write("s2", now)
	next(proto.TypeHello)
	// a claim with a cursor: only what changed after it comes, and the claim is answered
	_ = proto.Write(inW, proto.Frame{Type: proto.TypeNotes, Name: "jaromac", On: true, Since: now.Add(-time.Minute)})
	if f := next(proto.TypeNotes); !f.On || f.Name != "jaromac" {
		t.Fatalf("claim answer %+v", f)
	}
	if f := next(proto.TypeReport); f.Report.Session != "s2" {
		t.Fatalf("first report %+v", f.Report)
	}
	select {
	case f := <-frames:
		if f.Type == proto.TypeReport {
			t.Fatalf("a record older than the cursor was sent: %+v", f.Report)
		}
	case <-time.After(300 * time.Millisecond):
	}
	// a change is sent by the ticker; the same record is not sent twice
	write("s1", now.Add(time.Second))
	if f := next(proto.TypeReport); f.Report.Session != "s1" {
		t.Fatalf("changed report %+v", f.Report)
	}
	// another machine: refused while the claim is fresh, granted with take
	_ = proto.Write(inW, proto.Frame{Type: proto.TypeNotes, Name: "work", On: true})
	if f := next(proto.TypeNotes); f.On || f.Name != "jaromac" {
		t.Fatalf("second importer must be refused: %+v", f)
	}
	_ = proto.Write(inW, proto.Frame{Type: proto.TypeNotes, Name: "work", On: true, Take: true})
	if f := next(proto.TypeNotes); !f.On || f.Name != "work" {
		t.Fatalf("take must be granted: %+v", f)
	}
	if h := notes.Holder(dir, time.Now()); h != "work" {
		t.Fatalf("holder %q", h)
	}
	// the importer lets go: the claim is free again, and a stale name releases nothing
	_ = proto.Write(inW, proto.Frame{Type: proto.TypeNotes, Name: "jaromac", On: false})
	_ = proto.Write(inW, proto.Frame{Type: proto.TypeNotes, Name: "work", On: false})
	_ = proto.Write(inW, proto.Frame{Type: proto.TypePing})
	next(proto.TypePong)
	if h := notes.Holder(dir, time.Now()); h != "" {
		t.Fatalf("released, holder %q", h)
	}
	_ = inW.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("serve must end on EOF")
	}
}

func TestServeFeed(t *testing.T) {
	dir := t.TempDir()
	st := state.New(dir)
	cfg := config.Default()
	cfg.Sounds.Enabled = false
	cfg.Sidebar.PollMs = 200
	ft := &tmuxtest.Fake{Screens: map[string]string{"%1": "hello\n"}}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeDeps{
			Hello: proto.Hello{Proto: proto.Version, Hostname: "beta", Features: proto.ServeFeatures},
			NewPoller: func(s func(pane, kind string), onStore, onRestart func()) *poller.Poller {
				return poller.New(poller.Deps{Cfg: cfg, Tmux: ft, Store: st, Sound: s, OnStoreEvent: onStore, OnServerRestart: onRestart})
			},
			Inner: ft, Store: st, In: inR, Out: outW, Heartbeat: time.Minute, Debugf: func(string, ...any) {},
		})
	}()
	frames := make(chan proto.Frame, 64)
	go func() {
		rd := proto.NewReader(outR)
		for {
			f, err := rd.Next()
			if err != nil {
				return
			}
			frames <- f
		}
	}()
	next := func(typ string) proto.Frame {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case f := <-frames:
				if f.Type == typ {
					return f
				}
			case <-deadline:
				t.Fatalf("no %s frame", typ)
			}
		}
	}
	// %1 is a Claude pane whose hook named its transcript; %2 a Copilot pane
	transcript := filepath.Join(dir, "s1.jsonl")
	line := func(id, text string) string {
		return `{"type":"assistant","uuid":"` + id + `","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
	}
	if err := os.WriteFile(transcript, []byte(line("a1", "first")), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, _, err := st.Update("%1", func(a *agent.Agent) state.Effects {
		a.Kind, a.AgentSessionID, a.State, a.StateSince, a.HasHooks, a.Transcript = "claude", "s1", agent.Working, now, true, transcript
		return state.Effects{}
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Update("%2", func(a *agent.Agent) state.Effects {
		a.Kind, a.AgentSessionID, a.State, a.StateSince, a.HasHooks = "copilot", "c1", agent.Working, now, true
		return state.Effects{}
	}); err != nil {
		t.Fatal(err)
	}
	next(proto.TypeHello)
	_ = proto.Write(inW, proto.Frame{Type: proto.TypeCapture, Pane: "%1", On: true})
	_ = proto.Write(inW, proto.Frame{Type: proto.TypeCapture, Pane: "%2", On: true})
	got := map[string]*feed.Feed{}
	for len(got) < 2 {
		f := next(proto.TypeFeed)
		got[f.Feed.Pane] = f.Feed
	}
	if f := got["%1"]; !f.Reset || len(f.Items) != 1 || f.Items[0].Text != "first" {
		t.Fatalf("the Claude pane's feed starts with a Reset of what the transcript holds: %+v", f)
	}
	if f := got["%2"]; !f.Reset || f.Note == "" || len(f.Items) != 0 {
		t.Fatalf("the Copilot pane gets a note: %+v", f)
	}
	fh, _ := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	fh.WriteString(line("a2", "second"))
	fh.Close()
	if f := next(proto.TypeFeed); f.Feed.Pane != "%1" || f.Feed.Reset || len(f.Feed.Items) != 1 || f.Feed.Items[0].ID != "a2" {
		t.Fatalf("what was added comes alone: %+v", f.Feed)
	}
	// let go and look again: a Reset with both
	_ = proto.Write(inW, proto.Frame{Type: proto.TypeCapture, Pane: "%1", On: false})
	time.Sleep(50 * time.Millisecond)
	_ = proto.Write(inW, proto.Frame{Type: proto.TypeCapture, Pane: "%1", On: true})
	for {
		f := next(proto.TypeFeed)
		if f.Feed.Pane != "%1" {
			continue
		}
		if !f.Feed.Reset || len(f.Feed.Items) != 2 {
			t.Fatalf("a new capture starts over: %+v", f.Feed)
		}
		break
	}
	inW.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not end on EOF")
	}
}
