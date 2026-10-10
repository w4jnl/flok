// Package link is the instance's end of flok on the phone: one outbound WebSocket from the
// sidebar to the relay you run ([link] url), through HTTPS_PROXY when set, so an instance behind
// any firewall that lets HTTPS out is reachable. It streams the published snapshot, the
// user-facing events and the screens of panes the phone looks at; it takes answers, seen marks
// and subscriptions back. The connection is kept up with backoff; nothing here blocks the
// sidebar: Publish/Event/Screen queue, the latest snapshot and screen win.
package link

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/w4jnl/flok/internal/answer"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/snapshot"
)

// State of the link.
type State string

const (
	Off         State = "off"         // no [link] url
	Connecting  State = "connecting"  // dialing, or waiting for the hello to be taken
	Connected   State = "connected"   // frames flow
	Unreachable State = "unreachable" // dial failed or the connection dropped; retried with backoff
	Refused     State = "refused"     // the relay rejected the token (401/403) or the protocol; retried slowly
)

// Status is what the sidebar shows and publishes about the link.
type Status struct {
	State   State     `json:"state"`
	Detail  string    `json:"detail,omitempty"`
	Since   time.Time `json:"since"`
	RetryAt time.Time `json:"retry_at,omitzero"`
	URL     string    `json:"url,omitempty"`
}

// Command is what the relay asks of the instance, on behalf of the phone. Reset is the link's
// own: the connection ended, so every subscription is void (the relay subscribes again).
type Command struct {
	ID     string
	Kind   string // answer | seen | subscribe | reset
	Answer answer.Answer
	Pane   string
	On     bool
}

// Msg is delivered on Messages: a status change, or a command.
type Msg struct {
	Status  *Status
	Command *Command
}

// Config of a link.
type Config struct {
	URL      string // wss://flok.example.net/link; https:// and http:// are taken as wss:// and ws://, an empty path as /link
	Token    string
	Name     string // this instance's name on the phone
	Version  string
	Hostname string
	// BackoffMax bounds the retry delay; 0 = 60 s. Dial bounds one handshake; 0 = 20 s.
	BackoffMax time.Duration
	Dial       time.Duration
	Logf       func(string, ...any)
	// HTTP is the client for the handshake; nil = a transport with ProxyFromEnvironment.
	HTTP *http.Client
}

// Client is one link.
type Client struct {
	cfg    Config
	url    string
	msgs   chan Msg
	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	status  Status
	snap    *snapshot.Snapshot // latest, not yet sent
	sent    []byte             // body of the last snapshot sent on this connection
	screens map[string]string  // latest text per pane, not yet sent
	shown   map[string]string  // last text sent per pane on this connection
	queue   []wire.Frame       // events and acks, in order
	held    []wire.Frame       // events waiting for the snapshot they belong to (Publish releases them)
	holdT   *time.Timer
	wake    chan struct{}
	lastAll *snapshot.Snapshot // the latest snapshot ever, resent on every connection
}

// holdFor is how long an event waits for the snapshot that carries its state: the sidebar
// announces a transition, then publishes; the relay counts the badge from the snapshot, so the
// snapshot must go first. Without a Publish the events go anyway after this.
const holdFor = 200 * time.Millisecond

// New prepares a link; nil when cfg.URL is empty. Start runs it.
func New(cfg Config) *Client {
	u := NormalizeURL(cfg.URL)
	if u == "" {
		return nil
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = time.Minute
	}
	if cfg.Dial <= 0 {
		cfg.Dial = 20 * time.Second
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 15 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second}}
	}
	return &Client{cfg: cfg, url: u, msgs: make(chan Msg, 64), done: make(chan struct{}), wake: make(chan struct{}, 1),
		screens: map[string]string{}, shown: map[string]string{}, status: Status{State: Connecting, Since: time.Now(), URL: u}}
}

// NormalizeURL turns what the config says into the WebSocket URL of the relay's /link: the
// https and http schemes become wss and ws, an empty path becomes /link. "" when unusable.
func NormalizeURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ""
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "wss", "ws":
	default:
		return ""
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/link"
	}
	return u.String()
}

// HealthURL is the relay's /healthz for a link URL (doctor probes it over plain HTTPS).
func HealthURL(s string) string {
	u, err := url.Parse(NormalizeURL(s))
	if err != nil || u.Host == "" {
		return ""
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	}
	u.Path, u.RawQuery = "/healthz", ""
	return u.String()
}

// Messages delivers status changes and commands.
func (c *Client) Messages() <-chan Msg { return c.msgs }

// Status is the link's state.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Start runs the link until Close.
func (c *Client) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go c.run(ctx)
}

// Close ends the link; the relay sees the connection close and marks the instance offline.
func (c *Client) Close() {
	if c.cancel == nil {
		return
	}
	c.cancel()
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
	}
}

// Publish queues the snapshot the sidebar just published (an unchanged one is not sent) and
// releases the events announced since the last one, which follow it.
func (c *Client) Publish(s snapshot.Snapshot) {
	s.UpdatedAt = time.Time{}
	c.mu.Lock()
	c.snap, c.lastAll = &s, &s
	c.releaseLocked()
	c.mu.Unlock()
	c.kick()
}

// Event queues one transition, behind the snapshot that comes with it.
func (c *Client) Event(e wire.Event) {
	c.mu.Lock()
	c.held = append(c.held, wire.Frame{Type: wire.TypeEvent, Event: &e})
	if c.holdT == nil {
		c.holdT = time.AfterFunc(holdFor, func() {
			c.mu.Lock()
			c.releaseLocked()
			c.mu.Unlock()
			c.kick()
		})
	}
	c.mu.Unlock()
}

// releaseLocked moves the held events to the queue; the caller holds c.mu.
func (c *Client) releaseLocked() {
	if c.holdT != nil {
		c.holdT.Stop()
		c.holdT = nil
	}
	if len(c.held) > 0 {
		c.queue = append(c.queue, c.held...)
		c.held = nil
		c.trim()
	}
}

// Screen queues a captured pane's text; an unchanged screen is not sent again.
func (c *Client) Screen(pane, text string) {
	c.mu.Lock()
	c.screens[pane] = text
	c.mu.Unlock()
	c.kick()
}

// Forget drops what the link remembers of a pane's screen, so the next Screen is sent even when
// the text is the same (a new subscription).
func (c *Client) Forget(pane string) {
	c.mu.Lock()
	delete(c.screens, pane)
	delete(c.shown, pane)
	c.mu.Unlock()
}

// Ack reports an answer's outcome to the relay (and through it, the phone).
func (c *Client) Ack(id, errText string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	c.queue = append(c.queue, wire.Frame{Type: wire.TypeAck, ID: id, Error: errText})
	c.trim()
	c.mu.Unlock()
	c.kick()
}

// trim bounds the queue (a relay that is gone for a day must not grow it without end): the
// oldest events go first, acks for answers nobody waits for any more are no loss either.
func (c *Client) trim() {
	const max = 256
	if n := len(c.queue); n > max {
		c.queue = append([]wire.Frame(nil), c.queue[n-max:]...)
	}
}

func (c *Client) kick() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Client) set(st State, detail string, retryAt time.Time) {
	c.mu.Lock()
	changed := c.status.State != st || c.status.Detail != detail
	if changed {
		c.status.Since = time.Now()
	}
	c.status.State, c.status.Detail, c.status.RetryAt = st, detail, retryAt
	s := c.status
	c.mu.Unlock()
	if changed || !retryAt.IsZero() {
		c.cfg.Logf("link: %s %s", st, detail)
		c.deliver(Msg{Status: &s})
	}
}

func (c *Client) deliver(m Msg) {
	select {
	case c.msgs <- m:
	default: // the sidebar is not reading: drop rather than stall the link
	}
}

func backoff(attempt int, max time.Duration) time.Duration {
	if attempt > 10 {
		attempt = 10
	}
	d := time.Second << uint(attempt)
	if d > max {
		d = max
	}
	return d
}

func (c *Client) run(ctx context.Context) {
	defer close(c.done)
	attempt := 0
	for {
		c.set(Connecting, "", time.Time{})
		start := time.Now()
		st, detail := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		c.deliver(Msg{Command: &Command{Kind: "reset"}})
		if time.Since(start) > time.Minute {
			attempt = 0
		}
		delay := backoff(attempt, c.cfg.BackoffMax)
		if st == Refused { // a wrong token does not fix itself quickly; a relay upgrade might
			delay = c.cfg.BackoffMax
		}
		attempt++
		c.set(st, detail, time.Now().Add(delay))
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// session is one connection: dial, hello, then frames both ways until something ends it.
func (c *Client) session(ctx context.Context) (State, string) {
	dctx, cancel := context.WithTimeout(ctx, c.cfg.Dial)
	hdr := http.Header{}
	if c.cfg.Token != "" {
		hdr.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	hdr.Set("User-Agent", "flok/"+c.cfg.Version)
	conn, resp, err := websocket.Dial(dctx, c.url, &websocket.DialOptions{HTTPClient: c.cfg.HTTP, HTTPHeader: hdr})
	cancel()
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				return Refused, "the relay refused the token ([link] token)"
			case http.StatusNotFound:
				return Refused, "nothing answers at " + c.url + " (not a flok relay?)"
			}
			return Unreachable, fmt.Sprintf("relay answered %s", resp.Status)
		}
		return Unreachable, shortErr(err)
	}
	conn.SetReadLimit(wire.MaxAppFrame * 4)
	defer conn.CloseNow()
	sctx, stop := context.WithCancel(ctx)
	defer stop()
	write := func(f wire.Frame) error {
		data, err := json.Marshal(f)
		if err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(sctx, 15*time.Second)
		defer cancel()
		return conn.Write(wctx, websocket.MessageText, data)
	}
	hello := wire.Frame{Type: wire.TypeHello, Hello: &wire.Hello{Proto: wire.Proto, Name: c.cfg.Name, Version: c.cfg.Version,
		Hostname: c.cfg.Hostname, Features: wire.InstanceFeatures}}
	if err := write(hello); err != nil {
		return Unreachable, shortErr(err)
	}
	// a fresh connection knows nothing: the latest snapshot goes first, every screen again
	c.mu.Lock()
	c.sent = nil
	c.shown = map[string]string{}
	if c.lastAll != nil {
		c.snap = c.lastAll
	}
	c.mu.Unlock()
	c.kick()
	c.set(Connected, "", time.Time{})

	readErr := make(chan error, 1)
	go func() {
		for {
			_, data, err := conn.Read(sctx)
			if err != nil {
				readErr <- err
				return
			}
			var f wire.Frame
			if json.Unmarshal(data, &f) != nil {
				continue
			}
			switch f.Type {
			case wire.TypeAnswer:
				if f.Answer != nil {
					c.deliver(Msg{Command: &Command{ID: f.ID, Kind: "answer", Answer: *f.Answer, Pane: f.Answer.Pane}})
				}
			case wire.TypeSeen:
				if f.Pane != "" {
					c.deliver(Msg{Command: &Command{ID: f.ID, Kind: "seen", Pane: f.Pane}})
				}
			case wire.TypeSubscribe:
				if f.Pane != "" {
					c.deliver(Msg{Command: &Command{Kind: "subscribe", Pane: f.Pane, On: f.On}})
				}
			case wire.TypeError:
				c.cfg.Logf("link: relay says: %s", f.Error)
			}
		}
	}()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-c.wake:
			if err := c.flush(write); err != nil {
				return Unreachable, shortErr(err)
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(sctx, 10*time.Second)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return Unreachable, "no pong from the relay"
			}
		case err := <-readErr:
			if websocket.CloseStatus(err) == websocket.StatusPolicyViolation {
				return Refused, "the relay closed the link: " + closeReason(err)
			}
			return Unreachable, "connection closed: " + closeReason(err)
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusGoingAway, "flok down")
			return Off, ""
		}
	}
}

// flush sends what is queued: the snapshot when it changed, then events and acks in order,
// then every screen that changed.
func (c *Client) flush(write func(wire.Frame) error) error {
	for {
		c.mu.Lock()
		var f *wire.Frame
		switch {
		case c.snap != nil:
			s := c.snap
			c.snap = nil
			body, _ := json.Marshal(s)
			if !bytes.Equal(body, c.sent) {
				c.sent = body
				stamped := *s
				stamped.UpdatedAt = time.Now()
				f = &wire.Frame{Type: wire.TypeSnap, Snap: &stamped}
			}
		case len(c.queue) > 0:
			q := c.queue[0]
			c.queue = c.queue[1:]
			f = &q
		default:
			for pane, text := range c.screens {
				delete(c.screens, pane)
				if c.shown[pane] == text {
					continue
				}
				c.shown[pane] = text
				f = &wire.Frame{Type: wire.TypeScreen, Screen: &wire.Screen{Pane: pane, Text: text}}
				break
			}
		}
		c.mu.Unlock()
		if f == nil {
			return nil
		}
		if err := write(*f); err != nil {
			return err
		}
	}
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s)-i > 8 {
		s = s[i+2:]
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func closeReason(err error) string {
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		if ce.Reason != "" {
			return ce.Reason
		}
		return fmt.Sprintf("status %d", ce.Code)
	}
	return shortErr(err)
}
