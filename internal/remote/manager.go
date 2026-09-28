package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/nav"
	"github.com/w4jnl/flok/internal/poller"
	"github.com/w4jnl/flok/internal/remote/proto"
	"github.com/w4jnl/flok/internal/rules"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

// Msg is what a host reports to the sink: every state change (State always set), and while
// connected its snapshots and sound events.
type Msg struct {
	Host    string
	State   State
	Detail  string
	RetryAt time.Time       // when the next attempt starts, for "retry in 8s"
	Hello   *proto.Hello    // with the Connected transition
	Snap    *proto.Snapshot // a new merged view of the host
	Event   *proto.Event    // a sound to play here
	Request string          // a flok key pressed inside the host's tmux: toggle, hide, jump, …
}

// Status is a host's current connection, for `flok host status` and the servers panel.
type Status struct {
	Host    string
	Mode    hosts.Mode
	State   State
	Detail  string
	Since   time.Time
	RetryAt time.Time
	Hello   *proto.Hello
	Agents  int // in the last snapshot
}

// PlainClient is what plain mode drives: tmux.Remote, or a fake in tests.
type PlainClient interface {
	tmux.Client
	SetVersion(tmux.Version)
	Features() tmux.Features
}

// Deps configures a Manager. Only Cfg, StateDir and Sink are required.
type Deps struct {
	Cfg       config.Config
	StateDir  string
	Sink      chan<- Msg
	Dial      Dialer                                        // nil = Exec (ssh)
	NewClient func(argv []string, h hosts.Host) PlainClient // nil = tmux.Remote over Dial's ssh
	Rules     *rules.Set                                    // screen rules for plain mode
	Adapters  []agent.Adapter
	// PlainPollFloorMs and PlainScreenFloorMs are the cadence floors of a plain-mode poller
	// (a fork and a round trip per call); 0 = 1000 and 3000.
	PlainPollFloorMs, PlainScreenFloorMs int
	Now                                  func() time.Time
	Sleep                                func(ctx context.Context, d time.Duration) bool // false when ctx ended; nil = timer
	Debugf                               func(string, ...any)
}

// Manager keeps one connection per enabled host and reconciles them against the registry.
type Manager struct {
	d      Deps
	mu     sync.Mutex
	conns  map[string]*conn
	closed bool
}

func New(d Deps) *Manager {
	if d.Dial == nil {
		d.Dial = Exec
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Sleep == nil {
		d.Sleep = func(ctx context.Context, dur time.Duration) bool {
			t := time.NewTimer(dur)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return false
			case <-t.C:
				return true
			}
		}
	}
	if d.PlainPollFloorMs <= 0 {
		d.PlainPollFloorMs = 1000
	}
	if d.PlainScreenFloorMs <= 0 {
		d.PlainScreenFloorMs = 3000
	}
	return &Manager{d: d, conns: map[string]*conn{}}
}

func (m *Manager) debugf(format string, args ...any) {
	if m.d.Debugf != nil {
		m.d.Debugf(format, args...)
	}
}

// Apply makes the running connections match the registry: enabled hosts connect, removed or
// disabled ones stop, a host whose target/mode/socket/flok changed reconnects.
func (m *Manager) Apply(set hosts.Set) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	want := map[string]hosts.Host{}
	for _, h := range set.Enabled() {
		want[h.Name] = h
	}
	var stop []*conn
	for name, c := range m.conns {
		if h, ok := want[name]; !ok || !sameHost(h, c.configured) {
			stop = append(stop, c)
			delete(m.conns, name)
		}
	}
	for name, h := range want {
		if _, ok := m.conns[name]; !ok {
			m.conns[name] = m.start(h)
		}
	}
	for _, c := range stop {
		c.stop()
	}
}

func sameHost(a, b hosts.Host) bool {
	return a.Target == b.Target && a.Mode == b.Mode && a.Socket == b.Socket && a.Flok == b.Flok
}

func (m *Manager) start(h hosts.Host) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{m: m, host: h, configured: h, cancel: cancel, done: make(chan struct{}),
		status: Status{Host: h.Name, Mode: h.Mode, State: Connecting, Since: m.d.Now()}}
	go c.run(ctx)
	return c
}

func (m *Manager) get(host string) *conn {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.conns[host]
}

// Goto moves the host's attached client to a session/window/pane (full: a goto frame; plain:
// switch-client over ssh from the poller's goroutine).
func (m *Manager) Goto(host, session, window, pane string) error {
	c := m.get(host)
	if c == nil {
		return fmt.Errorf("host %s is not connected", host)
	}
	return c.goTo(session, window, pane)
}

// MarkSeen tells the host the user looked at a pane (its done becomes idle).
func (m *Manager) MarkSeen(host, pane string) {
	if c := m.get(host); c != nil {
		c.markSeen(pane)
	}
}

// SetVisible tells the host whether its work pane is on screen and the terminal focused, so
// done/idle and sound suppression follow what the user actually sees. Sent on change and on
// every (re)connect.
func (m *Manager) SetVisible(host string, on bool) {
	if c := m.get(host); c != nil {
		c.setVisible(on)
	}
}

// Reconnect drops a host's connection and starts over at once (a key after the user fixed
// something; the states with a slow retry would otherwise wait up to a minute).
func (m *Manager) Reconnect(host string) {
	m.mu.Lock()
	c := m.conns[host]
	if c == nil || m.closed {
		m.mu.Unlock()
		return
	}
	delete(m.conns, host)
	m.mu.Unlock()
	c.stop()
	m.mu.Lock()
	if _, running := m.conns[host]; !running && !m.closed {
		m.conns[host] = m.start(c.configured)
	}
	m.mu.Unlock()
}

// Notify shows a short message on the host's status line (the client the local side drives):
// a notify frame in full mode, display-message over ssh in plain mode.
func (m *Manager) Notify(host, text string) {
	c := m.get(host)
	if c == nil || text == "" {
		return
	}
	c.mu.Lock()
	send, cmds, client, p := c.send, c.cmds, c.client, c.poller
	c.mu.Unlock()
	switch {
	case send != nil:
		_ = send(proto.Frame{Type: proto.TypeNotify, Text: text})
	case cmds != nil:
		_ = c.enqueue(cmds, func() {
			args := []string{"display-message"}
			if tty := p.Snap().Focus.ClientTTY; tty != "" {
				args = append(args, "-c", tty)
			}
			_, _ = client.Run(append(args, text)...)
		})
	}
}

// Client is the plain-mode tmux client of a connected host, nil otherwise.
func (m *Manager) Client(host string) tmux.Client {
	c := m.get(host)
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == nil {
		return nil
	}
	return c.client
}

// Status reports every managed host.
func (m *Manager) Status() map[string]Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]Status{}
	for name, c := range m.conns {
		c.mu.Lock()
		out[name] = c.status
		c.mu.Unlock()
	}
	return out
}

// Close stops every connection (serve sessions end on EOF) and refuses further Apply calls.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	conns := m.conns
	m.conns = map[string]*conn{}
	m.mu.Unlock()
	for _, c := range conns {
		c.stop()
	}
}

func (m *Manager) emit(ctx context.Context, msg Msg) {
	if m.d.Sink == nil {
		return
	}
	select {
	case m.d.Sink <- msg:
	case <-ctx.Done():
	}
}

// conn is one host's connection loop.
type conn struct {
	m          *Manager
	host       hosts.Host // may gain a probed Flok path
	configured hosts.Host // as Apply saw it
	cancel     context.CancelFunc
	done       chan struct{}
	probed     bool
	oldVersion string // version of a remote flok that has no serve, once asked

	mu      sync.Mutex
	status  Status
	visible bool
	sent    *bool                   // last visible value sent this connection
	send    func(proto.Frame) error // full mode, while connected
	client  PlainClient             // plain mode, while connected
	cmds    chan func()             // plain mode poller loop
	poller  *poller.Poller
}

func (c *conn) stop() {
	c.cancel()
	<-c.done
}

func (c *conn) set(ctx context.Context, st State, detail string, retryAt time.Time, hello *proto.Hello) {
	c.mu.Lock()
	if c.status.State != st {
		c.status.Since = c.m.d.Now()
	}
	c.status.State, c.status.Detail, c.status.RetryAt = st, detail, retryAt
	if hello != nil {
		c.status.Hello = hello
	}
	c.mu.Unlock()
	c.m.debugf("%s: %s %s", c.host.Name, st, detail)
	c.m.emit(ctx, Msg{Host: c.host.Name, State: st, Detail: detail, RetryAt: retryAt, Hello: hello})
}

func (c *conn) run(ctx context.Context) {
	defer close(c.done)
	attempt := 0
	for {
		c.set(ctx, Connecting, "", time.Time{}, nil)
		start := c.m.d.Now()
		var st State
		var detail string
		if c.host.Mode == hosts.ModePlain {
			st, detail = c.attemptPlain(ctx)
		} else {
			st, detail = c.attemptFull(ctx)
		}
		if ctx.Err() != nil {
			return
		}
		if st == NoFlok && c.host.Flok == "" && !c.probed {
			c.probed = true
			if path := c.probeFlok(ctx); path != "" {
				c.host.Flok = path
				c.persist(func(h *hosts.Host) { h.Flok = path })
				continue
			}
		}
		if st == OldFlok { // say which version sits there, so the row explains itself
			if c.oldVersion == "" {
				c.oldVersion = c.probeVersion(ctx)
			}
			detail = "flok there is too old (no serve)"
			if c.oldVersion != "" {
				detail = "flok " + c.oldVersion + " there is too old (no serve)"
			}
		}
		if c.m.d.Now().Sub(start) > time.Minute {
			attempt = 0 // it held for a while: a fresh outage starts the backoff over
		}
		delay := Backoff(attempt, c.m.d.Cfg.Hosts.BackoffMaxS)
		switch {
		case st == Busy: // the other serve is often a one-shot (flok host status) or on its way out
			delay = busyRetry
		case st.SlowRetry():
			delay = time.Minute
		}
		attempt++
		c.set(ctx, st, detail, c.m.d.Now().Add(delay), nil)
		if !c.m.d.Sleep(ctx, delay) {
			return
		}
	}
}

// persist updates this host's registry entry (last_connected, a probed flok path); a host
// removed meanwhile is left alone.
func (c *conn) persist(fn func(*hosts.Host)) {
	_, err := hosts.Update(c.m.d.StateDir, func(s *hosts.Set) error {
		for i := range s.Hosts {
			if s.Hosts[i].Name == c.host.Name {
				fn(&s.Hosts[i])
			}
		}
		return nil
	})
	if err != nil {
		c.m.debugf("%s: hosts.json: %v", c.host.Name, err)
	}
}

func (c *conn) serveCommand() string {
	cmd := "flok serve --stdio"
	switch {
	case c.host.Flok != "":
		cmd = tmux.ShellQuote(c.host.Flok) + " serve --stdio"
	case c.m.d.Cfg.Hosts.ServeCommand != "":
		cmd = c.m.d.Cfg.Hosts.ServeCommand
	}
	return WithPath(c.m.d.Cfg.Hosts, cmd)
}

// probeScript finds a flok the non-interactive ssh PATH misses (Homebrew, ~/.local/bin).
const probeScript = `for p in "$HOME/.local/bin/flok" /opt/homebrew/bin/flok /usr/local/bin/flok; do if [ -x "$p" ]; then echo "$p"; exit 0; fi; done; exit 1`

func (c *conn) probeFlok(ctx context.Context) string {
	pctx, cancel := context.WithTimeout(ctx, time.Duration(c.m.d.Cfg.Hosts.ConnectTimeoutS+10)*time.Second)
	defer cancel()
	proc, err := c.m.d.Dial(pctx, Argv(c.m.d.Cfg.Hosts, c.m.d.StateDir, c.host, false, WithPath(c.m.d.Cfg.Hosts, probeScript)))
	if err != nil {
		return ""
	}
	out, _ := io.ReadAll(io.LimitReader(proc.Stdout(), 4096))
	exit, _ := proc.Wait()
	path := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if exit != 0 || !strings.HasPrefix(path, "/") {
		return ""
	}
	c.m.debugf("%s: flok found at %s", c.host.Name, path)
	return path
}

// flokBinary is the remote flok the serve command runs: the probed or configured path, else
// the first word of [hosts] serve_command.
func (c *conn) flokBinary() string {
	if c.host.Flok != "" {
		return tmux.ShellQuote(c.host.Flok)
	}
	if f := strings.Fields(c.m.d.Cfg.Hosts.ServeCommand); len(f) > 0 {
		return f[0]
	}
	return "flok"
}

// probeVersion asks a remote flok for its version ("flok 0.4.4" on a non-tty), "" when that fails.
func (c *conn) probeVersion(ctx context.Context) string {
	pctx, cancel := context.WithTimeout(ctx, time.Duration(c.m.d.Cfg.Hosts.ConnectTimeoutS+10)*time.Second)
	defer cancel()
	proc, err := c.m.d.Dial(pctx, Argv(c.m.d.Cfg.Hosts, c.m.d.StateDir, c.host, false, WithPath(c.m.d.Cfg.Hosts, c.flokBinary()+" version")))
	if err != nil {
		return ""
	}
	out, _ := io.ReadAll(io.LimitReader(proc.Stdout(), 4096))
	exit, _ := proc.Wait()
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if exit != 0 || !strings.HasPrefix(line, "flok ") {
		return ""
	}
	return strings.TrimPrefix(strings.TrimPrefix(line, "flok "), "v")
}

const (
	pingEvery  = 5 * time.Second
	staleAfter = 15 * time.Second
	deadAfter  = 60 * time.Second
	busyRetry  = 10 * time.Second
)

// attemptFull runs one serve session: dial, hello, then frames until the stream ends.
func (c *conn) attemptFull(ctx context.Context) (State, string) {
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	proc, err := c.m.d.Dial(actx, Argv(c.m.d.Cfg.Hosts, c.m.d.StateDir, c.host, false, c.serveCommand()))
	if err != nil {
		return Unreachable, Detail(err.Error())
	}
	defer proc.Kill()
	frames := make(chan proto.Frame, 32)
	readDone := make(chan struct{})
	var readErr error
	go func() {
		defer close(readDone)
		r := proto.NewReader(proc.Stdout())
		r.Noise = func(l string) { c.m.debugf("%s: noise: %s", c.host.Name, l) }
		for {
			f, err := r.Next()
			if err != nil {
				readErr = err
				return
			}
			select {
			case frames <- f:
			case <-actx.Done():
				return
			}
		}
	}()
	exited := make(chan struct{})
	var exit int
	var stderr string
	go func() { exit, stderr = proc.Wait(); close(exited) }()
	// frames to the remote go through a queue and one writer goroutine, so the sidebar's
	// Goto/SetVisible never block on a stalled network; a full queue is a dead connection
	outc := make(chan proto.Frame, 64)
	writeErr := make(chan error, 1)
	go func() {
		for {
			select {
			case f := <-outc:
				if err := proto.Write(proc.Stdin(), f); err != nil {
					writeErr <- err
					return
				}
			case <-actx.Done():
				return
			}
		}
	}()
	send := func(f proto.Frame) error {
		select {
		case outc <- f:
			return nil
		default:
			return errors.New("send queue full")
		}
	}
	// ended classifies the process exit, preferring an error frame the remote sent first
	// (the reader is given a moment to queue what it read before the pipe closed)
	ended := func() (State, string) {
		select {
		case <-readDone:
		case <-time.After(2 * time.Second):
		}
		for {
			select {
			case f := <-frames:
				if f.Type == proto.TypeError {
					return classifyRemoteError(f.Error)
				}
				continue
			default:
			}
			break
		}
		return Classify(exit, stderr), Detail(stderr)
	}
	timeout := time.NewTimer(time.Duration(c.m.d.Cfg.Hosts.ConnectTimeoutS)*time.Second + 5*time.Second)
	defer timeout.Stop()
	var hello *proto.Hello
	for hello == nil {
		select {
		case f := <-frames:
			switch f.Type {
			case proto.TypeHello:
				hello = f.Hello
				if hello == nil {
					return Incompatible, "empty hello"
				}
			case proto.TypeError:
				return classifyRemoteError(f.Error)
			}
		case <-exited:
			return ended()
		case <-readDone:
			if readErr == io.EOF {
				<-exited
				return ended()
			}
			return Unreachable, Detail(readErr.Error())
		case <-timeout.C:
			return Unreachable, "no hello from flok serve"
		case <-actx.Done():
			return Unreachable, "closed"
		}
	}
	if hello.Proto != proto.Version {
		return Incompatible, fmt.Sprintf("remote flok %s speaks protocol %d, this one %d", hello.Version, hello.Proto, proto.Version)
	}
	c.mu.Lock()
	c.send, c.sent = send, nil
	visible := c.visible
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.send = nil
		c.mu.Unlock()
	}()
	c.set(actx, Connected, "", time.Time{}, hello)
	now := c.m.d.Now()
	c.persist(func(h *hosts.Host) { h.LastConnected = now })
	c.setVisible(visible)
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	lastFrame := time.Now()
	stale, noServer := false, false
	for {
		select {
		case f := <-frames:
			lastFrame = time.Now()
			if stale {
				stale = false
				c.set(actx, Connected, "", time.Time{}, nil)
			}
			switch f.Type {
			case proto.TypeSnap:
				if f.Snap != nil {
					c.mu.Lock()
					c.status.Agents = len(f.Snap.Agents)
					c.mu.Unlock()
					if noServer { // the host's tmux is back
						noServer = false
						c.set(actx, Connected, "", time.Time{}, nil)
					}
					c.m.emit(actx, Msg{Host: c.host.Name, State: Connected, Snap: f.Snap})
				}
			case proto.TypeEvent:
				if f.Event != nil {
					c.m.emit(actx, Msg{Host: c.host.Name, State: Connected, Event: f.Event})
				}
			case proto.TypeRequest:
				if IsKeyCommand(f.Cmd) {
					c.m.emit(actx, Msg{Host: c.host.Name, State: Connected, Request: f.Cmd})
				}
			case proto.TypeError:
				switch st, detail := classifyRemoteError(f.Error); st {
				case Busy:
					return st, detail
				case NoServer: // serve runs, its tmux does not: say so until a snapshot arrives
					noServer = true
					c.set(actx, NoServer, detail, time.Time{}, nil)
				default:
					c.m.debugf("%s: remote error: %s", c.host.Name, f.Error)
				}
			}
		case err := <-writeErr:
			return Unreachable, Detail(err.Error())
		case <-ping.C:
			if err := send(proto.Frame{Type: proto.TypePing}); err != nil {
				return Unreachable, Detail(err.Error())
			}
			switch quiet := time.Since(lastFrame); {
			case quiet > deadAfter:
				return Unreachable, "no data for a minute"
			case quiet > staleAfter && !stale:
				stale = true
				c.set(actx, Stale, "no data for 15 s", time.Time{}, nil)
			}
		case <-exited:
			return ended()
		case <-readDone:
			if readErr == io.EOF {
				<-exited
				return ended()
			}
			return Unreachable, Detail(readErr.Error())
		case <-actx.Done():
			return Unreachable, "closed"
		}
	}
}

func classifyRemoteError(msg string) (State, string) {
	switch {
	case strings.Contains(msg, "already served"):
		return Busy, msg
	case strings.Contains(msg, "no server running"), strings.Contains(msg, "no sessions"):
		return NoServer, Detail(strings.TrimPrefix(msg, "poll: "))
	}
	return Unreachable, Detail(msg)
}

// attemptPlain probes the remote tmux, then runs a local poller over it until the client
// fails three calls in a row.
func (c *conn) attemptPlain(ctx context.Context) (State, string) {
	prefix := Argv(c.m.d.Cfg.Hosts, c.m.d.StateDir, c.host, false, "")
	var client PlainClient
	if c.m.d.NewClient != nil {
		client = c.m.d.NewClient(prefix, c.host)
	} else {
		client = &tmux.Remote{Argv: prefix, Prefix: PathPrefix(c.m.d.Cfg.Hosts), Socket: c.host.Socket, Name: c.host.Name,
			Timeout: time.Duration(c.m.d.Cfg.Hosts.ConnectTimeoutS+10) * time.Second}
	}
	out, err := client.Run("-V")
	if err != nil {
		st, detail := ClassifyErr(err)
		if st == NoFlok {
			st, detail = Unreachable, "no tmux on the host's PATH ([hosts] remote_path): "+detail
		}
		return st, detail
	}
	ver := tmux.ParseVersion(out)
	client.SetVersion(ver)
	hello := &proto.Hello{Proto: proto.Version, Version: "plain", Hostname: c.host.Target, TmuxVersion: ver.String()}
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	var keys *Keys
	if c.m.d.Cfg.Hosts.Keys { // flok's keys inside that tmux, for as long as this session lasts
		keys = InstallKeys(client, BindOptionArgs())
		defer keys.Restore()
	}
	var lastErr error
	var errMu sync.Mutex
	cc := &countingClient{PlainClient: client, limit: 3, onFail: func(err error) {
		errMu.Lock()
		lastErr = err
		errMu.Unlock()
		cancel()
	}}

	store := state.New(hosts.Dir(c.m.d.StateDir, c.host.Name))
	p := poller.New(poller.Deps{Cfg: c.m.d.Cfg, Tmux: cc, Store: store, Rules: c.m.d.Rules, Adapters: c.m.d.Adapters,
		Sound: func(pane, kind string) {
			c.m.emit(actx, Msg{Host: c.host.Name, State: Connected, Event: &proto.Event{Pane: pane, Kind: kind}})
		},
		OnRequest: func(cmd string) {
			if IsKeyCommand(cmd) {
				c.m.emit(actx, Msg{Host: c.host.Name, State: Connected, Request: cmd})
			}
		},
		OnServerRestart: func() { // bindings live in the server: the new one needs them too
			if keys != nil {
				keys.Rebind()
			}
		},
		PollFloorMs: c.m.d.PlainPollFloorMs, ScreenFloorMs: c.m.d.PlainScreenFloorMs, Debugf: c.m.d.Debugf})
	defer p.Close()
	cmds := make(chan func(), 16)
	c.mu.Lock()
	c.client, c.cmds, c.poller = client, cmds, p
	visible := c.visible
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.client, c.cmds, c.poller = nil, nil, nil
		c.mu.Unlock()
	}()
	c.set(actx, Connected, "", time.Time{}, hello)
	now := c.m.d.Now()
	c.persist(func(h *hosts.Host) { h.LastConnected = now })
	_ = store.SetTerminalFocus(visible)
	_ = poller.RunLoop(actx, p, func(s merge.Snapshot) {
		ps := proto.FromMerge(s)
		c.mu.Lock()
		c.status.Agents = len(ps.Agents)
		c.mu.Unlock()
		c.m.emit(actx, Msg{Host: c.host.Name, State: Connected, Snap: &ps})
	}, cmds)
	if ctx.Err() != nil {
		return Unreachable, "closed"
	}
	errMu.Lock()
	defer errMu.Unlock()
	return ClassifyErr(lastErr)
}

// countingClient ends a plain-mode session after limit consecutive failures.
type countingClient struct {
	PlainClient
	limit  int
	fails  int
	onFail func(error)
}

func (c *countingClient) Run(args ...string) (string, error) {
	out, err := c.PlainClient.Run(args...)
	if err == nil {
		c.fails = 0
		return out, nil
	}
	c.fails++
	if c.fails >= c.limit {
		c.onFail(err)
	}
	return out, err
}

func (c *conn) goTo(session, window, pane string) error {
	c.mu.Lock()
	send, cmds, client, p := c.send, c.cmds, c.client, c.poller
	c.mu.Unlock()
	switch {
	case send != nil:
		return send(proto.Frame{Type: proto.TypeGoto, Goto: &proto.Goto{Session: session, Window: window, Pane: pane}})
	case cmds != nil:
		return c.enqueue(cmds, func() {
			tty := p.Snap().Focus.ClientTTY
			if err := nav.Go(client, tty, session, window, pane); err != nil {
				c.m.debugf("%s: goto: %v", c.host.Name, err)
			} else if pane != "" {
				p.MarkSeen(pane)
			}
		})
	}
	return errors.New("host " + c.host.Name + " is not connected")
}

func (c *conn) markSeen(pane string) {
	c.mu.Lock()
	send, cmds, p := c.send, c.cmds, c.poller
	c.mu.Unlock()
	switch {
	case send != nil:
		_ = send(proto.Frame{Type: proto.TypeSeen, Pane: pane})
	case cmds != nil:
		_ = c.enqueue(cmds, func() { p.MarkSeen(pane) })
	}
}

func (c *conn) setVisible(on bool) {
	c.mu.Lock()
	c.visible = on
	send, client := c.send, c.client
	dup := c.sent != nil && *c.sent == on
	if send != nil && !dup {
		v := on
		c.sent = &v
	}
	c.mu.Unlock()
	switch {
	case send != nil && !dup:
		_ = send(proto.Frame{Type: proto.TypeVisible, On: on})
	case client != nil:
		_ = state.New(hosts.Dir(c.m.d.StateDir, c.host.Name)).SetTerminalFocus(on)
	}
}

func (c *conn) enqueue(cmds chan func(), f func()) error {
	select {
	case cmds <- f:
		return nil
	default:
		return errors.New("host " + c.host.Name + " is busy")
	}
}

// statusJSON is Status for `flok host status --json`.
func (s Status) MarshalJSON() ([]byte, error) {
	type out struct {
		Host    string       `json:"host"`
		Mode    hosts.Mode   `json:"mode"`
		State   State        `json:"state"`
		Detail  string       `json:"detail,omitempty"`
		Since   time.Time    `json:"since"`
		RetryAt time.Time    `json:"retry_at,omitzero"`
		Hello   *proto.Hello `json:"hello,omitempty"`
		Agents  int          `json:"agents"`
	}
	return json.Marshal(out{s.Host, s.Mode, s.State, s.Detail, s.Since, s.RetryAt, s.Hello, s.Agents})
}
