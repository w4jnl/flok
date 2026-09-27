package remote

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/remote/proto"
	"github.com/w4jnl/flok/internal/tmux"
)

// fakeProc is an in-memory ssh: the test writes the remote's stdout and reads what the manager
// sends to its stdin.
type fakeProc struct {
	argv      []string
	inR, outR *io.PipeReader
	inW, outW *io.PipeWriter
	exitc     chan struct{}
	once      sync.Once
	mu        sync.Mutex
	code      int
	stderr    string
	killed    bool
	fromLocal chan proto.Frame // what the manager wrote, drained like a kernel pipe buffer
}

func newFakeProc(argv []string) *fakeProc {
	p := &fakeProc{argv: argv, exitc: make(chan struct{}), code: -1, fromLocal: make(chan proto.Frame, 64)}
	p.inR, p.inW = io.Pipe()
	p.outR, p.outW = io.Pipe()
	go func() {
		r := proto.NewReader(p.inR)
		for {
			f, err := r.Next()
			if err != nil {
				return
			}
			p.fromLocal <- f
		}
	}()
	return p
}

func (p *fakeProc) Stdin() io.Writer  { return p.inW }
func (p *fakeProc) Stdout() io.Reader { return p.outR }
func (p *fakeProc) Wait() (int, string) {
	<-p.exitc
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.code, p.stderr
}
func (p *fakeProc) Kill() {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	p.exit(-1, "")
}

// exit ends the fake process like ssh would: stdout closes, then Wait returns.
func (p *fakeProc) exit(code int, stderr string) {
	p.once.Do(func() {
		p.mu.Lock()
		if !p.killed || code != -1 {
			p.code, p.stderr = code, stderr
		}
		p.mu.Unlock()
		_ = p.outW.Close()
		_ = p.inR.Close()
		close(p.exitc)
	})
}

func (p *fakeProc) send(t *testing.T, f proto.Frame) {
	t.Helper()
	if err := proto.Write(p.outW, f); err != nil {
		t.Fatalf("fake remote write: %v", err)
	}
}

// next reads the next frame the manager sent, with a timeout.
func (p *fakeProc) next(t *testing.T) proto.Frame {
	t.Helper()
	select {
	case f := <-p.fromLocal:
		return f
	case <-time.After(3 * time.Second):
		t.Fatal("manager sent nothing")
	}
	return proto.Frame{}
}

type harness struct {
	t     *testing.T
	sink  chan Msg
	procs chan *fakeProc
	m     *Manager
	dir   string
	slept chan time.Duration
	wait  time.Duration // how long msg waits
}

func newHarness(t *testing.T, mode hosts.Mode) *harness {
	t.Helper()
	h := &harness{t: t, sink: make(chan Msg, 256), procs: make(chan *fakeProc, 16), dir: t.TempDir(), slept: make(chan time.Duration, 64), wait: 3 * time.Second}
	cfg := config.Default()
	cfg.Sounds.Enabled = false
	cfg.Sidebar.PollMs, cfg.Sidebar.ScreenPollMs = 20, 20
	cfg.Hosts.ConnectTimeoutS = 1
	if _, err := hosts.Update(h.dir, func(s *hosts.Set) error {
		return s.Add(hosts.Host{Name: "beta", Target: "beta", Mode: mode, Enabled: true})
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) manager(extra func(*Deps)) *Manager {
	d := Deps{Cfg: config.Default(), StateDir: h.dir, Sink: h.sink,
		Dial: func(ctx context.Context, argv []string) (Proc, error) {
			p := newFakeProc(argv)
			h.procs <- p
			return p, nil
		},
		Sleep: func(ctx context.Context, d time.Duration) bool {
			h.slept <- d
			select {
			case <-ctx.Done():
				return false
			case <-time.After(5 * time.Millisecond):
				return true
			}
		},
		PlainPollFloorMs: 20, PlainScreenFloorMs: 20,
	}
	d.Cfg.Sounds.Enabled = false
	d.Cfg.Sidebar.PollMs, d.Cfg.Sidebar.ScreenPollMs = 20, 20
	d.Cfg.Hosts.ConnectTimeoutS = 1
	if extra != nil {
		extra(&d)
	}
	h.m = New(d)
	set, _ := hosts.Load(h.dir)
	h.m.Apply(set)
	h.t.Cleanup(h.m.Close)
	return h.m
}

func (h *harness) proc() *fakeProc {
	h.t.Helper()
	select {
	case p := <-h.procs:
		return p
	case <-time.After(3 * time.Second):
		h.t.Fatal("manager did not dial")
	}
	return nil
}

// msg waits for a message matching pred.
func (h *harness) msg(pred func(Msg) bool, what string) Msg {
	h.t.Helper()
	deadline := time.After(h.wait)
	for {
		select {
		case m := <-h.sink:
			if pred(m) {
				return m
			}
		case <-deadline:
			h.t.Fatalf("no message: %s", what)
		}
	}
}

func (h *harness) state(st State) Msg {
	h.t.Helper()
	return h.msg(func(m Msg) bool { return m.State == st && m.Snap == nil && m.Event == nil }, string(st))
}

func TestFullModeSession(t *testing.T) {
	h := newHarness(t, hosts.ModeFull)
	m := h.manager(nil)
	h.state(Connecting)
	p := h.proc()
	if last := p.argv[len(p.argv)-1]; last != `export PATH="$PATH:/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:/opt/local/bin"; flok serve --stdio` || p.argv[len(p.argv)-2] != "beta" {
		t.Fatalf("dial argv %q", p.argv)
	}
	p.send(t, proto.Frame{Type: proto.TypeHello, Hello: &proto.Hello{Proto: proto.Version, Version: "0.5.0", Hostname: "beta", TmuxVersion: "3.4"}})
	if msg := h.state(Connected); msg.Hello == nil || msg.Hello.Hostname != "beta" {
		t.Fatalf("connected without hello: %+v", msg)
	}
	if f := p.next(t); f.Type != proto.TypeVisible || f.On {
		t.Fatalf("first frame must be visible=false, got %+v", f)
	}
	set, _ := hosts.Load(h.dir)
	if set.Hosts[0].LastConnected.IsZero() {
		t.Fatal("connect must record last_connected")
	}
	p.send(t, proto.Frame{Type: proto.TypeSnap, Snap: &proto.Snapshot{Unseen: 2}})
	if msg := h.msg(func(m Msg) bool { return m.Snap != nil }, "snap"); msg.Snap.Unseen != 2 || msg.Host != "beta" {
		t.Fatalf("snap %+v", msg)
	}
	p.send(t, proto.Frame{Type: proto.TypeEvent, Event: &proto.Event{Pane: "%3", Kind: "blocked"}})
	if msg := h.msg(func(m Msg) bool { return m.Event != nil }, "event"); msg.Event.Pane != "%3" {
		t.Fatalf("event %+v", msg)
	}
	// serve's tmux goes away: the row says so while the session stays up, a snapshot ends it
	p.send(t, proto.Frame{Type: proto.TypeError, Error: "poll: no server running on /tmp/tmux-501/default"})
	if msg := h.state(NoServer); msg.Detail != "no server running on /tmp/tmux-501/default" {
		t.Fatalf("no server %+v", msg)
	}
	p.send(t, proto.Frame{Type: proto.TypeSnap, Snap: &proto.Snapshot{}})
	h.state(Connected)
	m.SetVisible("beta", true)
	m.SetVisible("beta", true) // deduplicated
	if err := m.Goto("beta", "$1", "@2", "%3"); err != nil {
		t.Fatal(err)
	}
	m.MarkSeen("beta", "%3")
	if f := p.next(t); f.Type != proto.TypeVisible || !f.On {
		t.Fatalf("visible on: %+v", f)
	}
	if f := p.next(t); f.Type != proto.TypeGoto || f.Goto.Pane != "%3" {
		t.Fatalf("goto: %+v", f)
	}
	if f := p.next(t); f.Type != proto.TypeSeen || f.Pane != "%3" {
		t.Fatalf("seen (visible must not repeat): %+v", f)
	}
	if st := m.Status()["beta"]; st.State != Connected || st.Agents != 0 || st.Hello == nil {
		t.Fatalf("status %+v", st)
	}
	// the connection drops: unreachable, backoff 1 s, reconnect with visible=true replayed
	p.exit(255, "client_loop: send disconnect: Broken pipe")
	if msg := h.state(Unreachable); msg.Detail != "client_loop: send disconnect: Broken pipe" || msg.RetryAt.IsZero() {
		t.Fatalf("drop %+v", msg)
	}
	if d := <-h.slept; d != time.Second {
		t.Fatalf("first backoff %v", d)
	}
	p2 := h.proc()
	p2.send(t, proto.Frame{Type: proto.TypeHello, Hello: &proto.Hello{Proto: proto.Version}})
	h.state(Connected)
	if f := p2.next(t); f.Type != proto.TypeVisible || !f.On {
		t.Fatalf("reconnect replays visible: %+v", f)
	}
	// the registry disables the host: the session ends
	set.Hosts[0].Enabled = false
	m.Apply(set)
	p2.mu.Lock()
	killed := p2.killed
	p2.mu.Unlock()
	if !killed || m.Status()["beta"].Host != "" {
		t.Fatal("disabling must kill the session and drop the status")
	}
}

func TestFullModeFailures(t *testing.T) {
	h := newHarness(t, hosts.ModeFull)
	h.manager(nil)
	// auth: retried in a minute
	h.proc().exit(255, "jaro@beta: Permission denied (publickey).")
	if msg := h.state(Auth); !strings.Contains(msg.Detail, "Permission denied") {
		t.Fatalf("auth %+v", msg)
	}
	if d := <-h.slept; d != time.Minute {
		t.Fatalf("auth backoff %v", d)
	}
	// no flok: the probe finds one and the retry uses it
	h.proc().exit(127, "bash: flok: command not found")
	probe := h.proc()
	if !strings.Contains(probe.argv[len(probe.argv)-1], "/opt/homebrew/bin/flok") {
		t.Fatalf("expected the probe script, got %q", probe.argv)
	}
	_, _ = io.WriteString(probe.outW, "/opt/homebrew/bin/flok\n")
	probe.exit(0, "")
	p := h.proc()
	if last := p.argv[len(p.argv)-1]; !strings.HasSuffix(last, "; /opt/homebrew/bin/flok serve --stdio") {
		t.Fatalf("retry must use the probed path, got %q", last)
	}
	if set, _ := hosts.Load(h.dir); set.Hosts[0].Flok != "/opt/homebrew/bin/flok" {
		t.Fatal("the probed path must be persisted")
	}
	// incompatible hello
	p.send(t, proto.Frame{Type: proto.TypeHello, Hello: &proto.Hello{Proto: proto.Version + 1, Version: "9.0"}})
	if msg := h.state(Incompatible); !strings.Contains(msg.Detail, "protocol 2") {
		t.Fatalf("incompatible %+v", msg)
	}
	<-h.slept
	// busy: an error frame instead of a hello
	p = h.proc()
	p.send(t, proto.Frame{Type: proto.TypeError, Error: "already served (pid 9)"})
	p.exit(1, "")
	if msg := h.state(Busy); msg.Detail != "already served (pid 9)" {
		t.Fatalf("busy %+v", msg)
	}
	if d := <-h.slept; d != busyRetry {
		t.Fatalf("busy backoff %v", d)
	}
	// a flok from before serve: the version is asked once and named in the detail
	h.proc().exit(2, "flok: unknown command \"serve\"\n\nusage: flok <command>\n")
	vp := h.proc()
	if last := vp.argv[len(vp.argv)-1]; !strings.HasSuffix(last, "; /opt/homebrew/bin/flok version") {
		t.Fatalf("expected a version probe with the probed path, got %q", last)
	}
	_, _ = io.WriteString(vp.outW, "flok 0.4.4\n")
	vp.exit(0, "")
	if msg := h.state(OldFlok); msg.Detail != "flok 0.4.4 there is too old (no serve)" {
		t.Fatalf("old flok %+v", msg)
	}
	if d := <-h.slept; d != time.Minute {
		t.Fatalf("old flok backoff %v", d)
	}
	h.proc().exit(2, "flok: unknown command \"serve\"") // the retry does not ask again
	if msg := h.state(OldFlok); msg.Detail != "flok 0.4.4 there is too old (no serve)" {
		t.Fatalf("old flok again %+v", msg)
	}
	<-h.slept
	// hello timeout: connect timeout (1 s here) plus five seconds of grace
	p = h.proc()
	h.wait = 10 * time.Second
	if msg := h.state(Unreachable); msg.Detail != "no hello from flok serve" {
		t.Fatalf("timeout %+v", msg)
	}
	p.mu.Lock()
	killed := p.killed
	p.mu.Unlock()
	if !killed {
		t.Fatal("a silent process is killed")
	}
}

// fakeTmux answers -V and, while healthy, empty snapshots; when broken every call fails.
type fakeTmux struct {
	mu       sync.Mutex
	broken   bool
	noServer bool
	calls    int
	feat     tmux.Features
}

func (f *fakeTmux) Label() string             { return "fake" }
func (f *fakeTmux) SetVersion(v tmux.Version) { f.feat = tmux.FeaturesFor(v) }
func (f *fakeTmux) Features() tmux.Features   { return f.feat }
func (f *fakeTmux) Run(args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.broken {
		return "", &tmux.ExitError{Code: 255, Stderr: "ssh: connect to host beta port 22: Connection refused", Cmd: "beta: tmux"}
	}
	if len(args) == 1 && args[0] == "-V" {
		return "tmux 3.2a\n", nil
	}
	if f.noServer {
		return "", &tmux.ExitError{Code: 1, Stderr: "no server running on /tmp/tmux-0/default", Cmd: "beta: tmux list-sessions"}
	}
	return "", nil
}

func TestPlainMode(t *testing.T) {
	h := newHarness(t, hosts.ModePlain)
	ft := &fakeTmux{}
	var gotArgv []string
	m := h.manager(func(d *Deps) {
		d.NewClient = func(argv []string, host hosts.Host) PlainClient { gotArgv = argv; return ft }
	})
	if msg := h.state(Connected); msg.Hello == nil || msg.Hello.TmuxVersion != "3.2a" || msg.Hello.Version != "plain" {
		t.Fatalf("connected %+v", msg)
	}
	if gotArgv[len(gotArgv)-1] != "beta" {
		t.Fatalf("plain prefix ends in the target: %q", gotArgv)
	}
	if msg := h.msg(func(m Msg) bool { return m.Snap != nil }, "snap"); msg.Snap.Focus.Found {
		t.Fatalf("empty server snap %+v", msg.Snap)
	}
	if m.Client("beta") == nil {
		t.Fatal("plain mode exposes its client")
	}
	m.SetVisible("beta", false)
	m.MarkSeen("beta", "%1")
	// the host answers but its tmux server is down for that user: a state of its own, normal backoff
	ft.mu.Lock()
	ft.noServer = true
	ft.mu.Unlock()
	if msg := h.state(NoServer); msg.Detail != "no server running on /tmp/tmux-0/default" {
		t.Fatalf("no server %+v", msg)
	}
	if d := <-h.slept; d != time.Second {
		t.Fatalf("no-server backoff %v", d)
	}
	ft.mu.Lock()
	ft.noServer = false
	ft.mu.Unlock()
	h.state(Connected)
	ft.mu.Lock()
	ft.broken = true
	ft.mu.Unlock()
	if msg := h.state(Unreachable); msg.Detail != "Connection refused" {
		t.Fatalf("plain drop %+v", msg)
	}
	if m.Client("beta") != nil {
		t.Fatal("a dropped host has no client")
	}
	<-h.slept
	// the retry probes -V, which now fails too
	if msg := h.state(Unreachable); msg.Detail != "Connection refused" {
		t.Fatalf("retry %+v", msg)
	}
}

func TestExecDialer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := Exec(ctx, []string{"sh", "-c", "read x; echo got $x; echo warn >&2; exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(p.Stdin(), "hi\n")
	out, _ := io.ReadAll(p.Stdout())
	code, stderr := p.Wait()
	if string(out) != "got hi\n" || code != 3 || strings.TrimSpace(stderr) != "warn" {
		t.Fatalf("out=%q code=%d stderr=%q", out, code, stderr)
	}
	if _, err := Exec(ctx, nil); err == nil {
		t.Fatal("empty argv")
	}
	p, _ = Exec(ctx, []string{"sleep", "10"})
	cancel()
	if code, _ := p.Wait(); code != -1 {
		t.Fatalf("cancel must kill: %d", code)
	}
	var ee *tmux.ExitError
	if errors.As(errors.New("x"), &ee) {
		t.Fatal("sanity")
	}
}
