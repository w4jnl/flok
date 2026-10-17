// Package link is the instance's end of flok on the phone: one outbound WebSocket from the
// sidebar to the relay you run ([link] url), through HTTPS_PROXY when set, so an instance behind
// any firewall that lets HTTPS out is reachable. It streams the published snapshot, the
// user-facing events and the screens of panes the phone looks at; it takes answers, seen marks
// and subscriptions back. The connection is kept up with backoff; nothing here blocks the
// sidebar: Publish/Event/Screen queue, the latest snapshot and screen win. With [link] pin the
// frames travel inside TLS to the relay's own key (internal/link/tunnel, the relay's /link/tls),
// so a proxy that inspects HTTPS reads none of them, the token included. SetOff switches the
// link off (flok link off): a bye tells the relay it is on purpose, and nothing is sent until it
// is switched on again.
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
	"github.com/w4jnl/flok/internal/link/tunnel"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/snapshot"
)

// State of the link.
type State string

const (
	Off         State = "off"         // switched off (flok link off), or closed
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
	// Encrypted: the link runs inside TLS to the relay's pinned key ([link] pin).
	Encrypted bool `json:"encrypted,omitempty"`
	// Compressed: the connected link's frames travel deflated (the relay agreed to it).
	Compressed bool `json:"compressed,omitempty"`
}

// Command is what the relay asks of the instance, on behalf of the phone, or answers it. Reset
// is the link's own: the connection ended, so every subscription is void (the relay subscribes
// again). Phone is the relay's reply to a phone frame (Client.Phone), or its word that a phone
// paired with the code that frame asked for; Error is what the relay said instead.
type Command struct {
	ID     string
	Kind   string // answer | seen | subscribe | phone | enroll | tenant | reset
	Answer answer.Answer
	Pane   string
	On     bool
	Phone  *wire.Phone
	Enroll *wire.Enroll // the relay's reply to flok link approve | list | forget
	Tenant *wire.Tenant // the relay's reply to flok tenant
	Error  string
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
	Pin      string // the relay's key (flok-relay pin): the link goes encrypted to /link/tls; "" = plain /link
	Off      bool   // start switched off (flok link off); SetOff switches
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
	off     bool               // switched off: no connection, nothing queued
	flip    chan struct{}      // SetOff changed off
	abort   context.CancelFunc // ends a dial in progress (SetOff), nil between dials
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
	cfg.Pin = strings.TrimSpace(cfg.Pin)
	st := Connecting
	if cfg.Off {
		st = Off
	}
	return &Client{cfg: cfg, url: u, msgs: make(chan Msg, 64), done: make(chan struct{}), wake: make(chan struct{}, 1),
		flip: make(chan struct{}, 1), off: cfg.Off,
		screens: map[string]string{}, shown: map[string]string{}, status: Status{State: st, Since: time.Now(), URL: u, Encrypted: cfg.Pin != ""}}
}

// TunnelURL is the relay's encrypted link next to a link URL: /link becomes /link/tls.
func TunnelURL(s string) string {
	u, err := url.Parse(NormalizeURL(s))
	if err != nil || u.Host == "" {
		return ""
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/tls"
	return u.String()
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

// OriginURL is the relay as the app addresses it, for a link URL: https://flok.example.net, with
// a path prefix kept and the link's own path (/link, /link/tls, /api/ws) taken off. It is what
// the pairing QR carries as relay=, and what the app's RelayURL.base makes of what one types
// there. "" when the link URL is not one.
func OriginURL(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Host == "" {
		return ""
	}
	switch u.Scheme {
	case "https", "wss":
		u.Scheme = "https"
	case "http", "ws":
		u.Scheme = "http"
	default:
		return ""
	}
	p := strings.TrimSuffix(u.Path, "/")
	for _, tail := range []string{"/link/tls", "/link", "/api/ws"} {
		p = strings.TrimSuffix(p, tail)
	}
	u.Path, u.RawPath, u.RawQuery, u.Fragment = strings.TrimSuffix(p, "/"), "", "", ""
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

// SetOff switches the link off or on again. Off: the connection ends after a bye, so the relay
// shows the instance as off rather than offline, a dial in progress is given up, and the events,
// acks and screens queued or announced meanwhile are dropped (they would arrive stale). On: it
// connects at once, with the latest snapshot. Switching off and on again reconnects without
// waiting out a backoff.
func (c *Client) SetOff(off bool) {
	c.mu.Lock()
	changed := c.off != off
	c.off = off
	if off {
		c.dropLocked()
		if c.abort != nil {
			c.abort()
		}
	}
	c.mu.Unlock()
	if changed {
		select {
		case c.flip <- struct{}{}:
		default:
		}
	}
}

// dropLocked forgets what waits to be sent but the snapshot; the caller holds c.mu.
func (c *Client) dropLocked() {
	if c.holdT != nil {
		c.holdT.Stop()
		c.holdT = nil
	}
	c.queue, c.held = nil, nil
	c.screens = map[string]string{}
}

func (c *Client) isOff() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.off
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
	if c.off {
		c.mu.Unlock()
		return
	}
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
	if c.off {
		c.mu.Unlock()
		return
	}
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

// Phone asks the relay about phones (flok phone pair | list | forget); the reply comes back as
// a Command of kind phone under the same id, and for pair, later, the word that a phone took
// the code. Dropped while the link is off or down: the caller's wait says so.
func (c *Client) Phone(id string, p wire.Phone) {
	c.request(wire.Frame{Type: wire.TypePhone, ID: id, Phone: &p})
}

// Enroll asks the relay about the floks of this tenant (flok link approve | list | forget); the
// reply comes back as a Command of kind enroll under the same id.
func (c *Client) Enroll(id string, e wire.Enroll) {
	c.request(wire.Frame{Type: wire.TypeEnroll, ID: id, Enroll: &e})
}

// Tenant asks the relay about its tenants (flok tenant, with an admin token in [link] token);
// the reply comes back as a Command of kind tenant under the same id.
func (c *Client) Tenant(id string, t wire.Tenant) {
	c.request(wire.Frame{Type: wire.TypeTenant, ID: id, Tenant: &t})
}

// request queues a frame the relay answers under its id; dropped while the link is off.
func (c *Client) request(f wire.Frame) {
	if f.ID == "" {
		return
	}
	c.mu.Lock()
	if c.off {
		c.mu.Unlock()
		return
	}
	c.queue = append(c.queue, f)
	c.trim()
	c.mu.Unlock()
	c.kick()
}

// Ack reports an answer's outcome to the relay (and through it, the phone).
func (c *Client) Ack(id, errText string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	if c.off {
		c.mu.Unlock()
		return
	}
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
	if st != Connected {
		c.status.Compressed = false
	}
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
		if c.isOff() {
			c.set(Off, "", time.Time{})
			select {
			case <-ctx.Done():
				return
			case <-c.flip:
				continue
			}
		}
		c.set(Connecting, "", time.Time{})
		start := time.Now()
		st, detail := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		c.deliver(Msg{Command: &Command{Kind: "reset"}})
		if c.isOff() {
			attempt = 0
			continue
		}
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
		case <-c.flip: // off, or off and on again: no need to wait out the backoff
			t.Stop()
			attempt = 0
		case <-t.C:
		}
	}
}

// frames is a connection's frame stream: WebSocket text messages, or lines inside the tunnel.
// deflated: the relay agreed to compress them (permessage-deflate on /link, flok-link/2 inside
// the tunnel); an older relay does not, and the frames go as they are.
type frames struct {
	read     func(context.Context) ([]byte, error)
	write    func(context.Context, []byte) error
	deflated bool
}

// session is one connection: dial, hello, then frames both ways until something ends it.
func (c *Client) session(ctx context.Context) (State, string) {
	sctx, stop := context.WithCancel(ctx)
	defer stop()
	var (
		conn   *websocket.Conn
		fr     frames
		st     State
		detail string
	)
	c.mu.Lock()
	if c.off { // switched off between the loop's look and here
		c.mu.Unlock()
		return Off, ""
	}
	c.abort = stop
	c.mu.Unlock()
	if c.cfg.Pin != "" {
		conn, fr, st, detail = c.dialTunnel(sctx)
	} else {
		conn, fr, st, detail = c.dial(sctx)
	}
	c.mu.Lock()
	c.abort = nil
	off := c.off
	c.mu.Unlock()
	if off {
		if conn != nil {
			conn.CloseNow()
		}
		return Off, ""
	}
	if conn == nil {
		return st, detail
	}
	defer conn.CloseNow()
	write := func(f wire.Frame) error {
		data, err := json.Marshal(f)
		if err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(sctx, 15*time.Second)
		defer cancel()
		return fr.write(wctx, data)
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
	c.status.Compressed = fr.deflated
	c.mu.Unlock()
	c.kick()
	c.set(Connected, "", time.Time{})

	type ended struct {
		err  error
		said string // the relay's last error frame: the reason inside the tunnel
	}
	readErr := make(chan ended, 1)
	go func() {
		said := ""
		for {
			data, err := fr.read(sctx)
			if err != nil {
				readErr <- ended{err, said}
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
			case wire.TypePhone:
				if f.ID != "" && (f.Phone != nil || f.Error != "") {
					c.deliver(Msg{Command: &Command{ID: f.ID, Kind: "phone", Phone: f.Phone, Error: f.Error}})
				}
			case wire.TypeEnroll:
				if f.ID != "" && (f.Enroll != nil || f.Error != "") {
					c.deliver(Msg{Command: &Command{ID: f.ID, Kind: "enroll", Enroll: f.Enroll, Error: f.Error}})
				}
			case wire.TypeTenant:
				if f.ID != "" && (f.Tenant != nil || f.Error != "") {
					c.deliver(Msg{Command: &Command{ID: f.ID, Kind: "tenant", Tenant: f.Tenant, Error: f.Error}})
				}
			case wire.TypeError:
				c.cfg.Logf("link: relay says: %s", f.Error)
				said = f.Error
			}
		}
	}()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-c.flip:
			if c.isOff() { // shown at once; the relay hears why before the connection ends
				c.set(Off, "", time.Time{})
				_ = write(wire.Frame{Type: wire.TypeBye, Reason: wire.ByeOff})
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return Off, ""
			}
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
		case e := <-readErr:
			why := closeReason(e.err)
			if e.said != "" {
				why = e.said
			}
			if websocket.CloseStatus(e.err) == websocket.StatusPolicyViolation {
				return Refused, "the relay closed the link: " + why
			}
			return Unreachable, "connection closed: " + why
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusGoingAway, "flok down")
			return Off, ""
		}
	}
}

// dial opens the plain link: the token in the upgrade's Authorization header, and an offer to
// deflate the frames with one window for the whole connection (successive snapshots and screens
// differ by a line or two, so each one after the first costs a few dozen bytes).
func (c *Client) dial(ctx context.Context) (*websocket.Conn, frames, State, string) {
	dctx, cancel := context.WithTimeout(ctx, c.cfg.Dial)
	defer cancel()
	hdr := http.Header{}
	if c.cfg.Token != "" {
		hdr.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	hdr.Set("User-Agent", "flok/"+c.cfg.Version)
	conn, resp, err := websocket.Dial(dctx, c.url, &websocket.DialOptions{HTTPClient: c.cfg.HTTP, HTTPHeader: hdr,
		CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		st, detail := dialFailed(c.url, resp, err)
		return nil, frames{}, st, detail
	}
	conn.SetReadLimit(wire.MaxRelayFrame) // an answer with a file
	return conn, frames{
		read: func(ctx context.Context) ([]byte, error) {
			_, data, err := conn.Read(ctx)
			return data, err
		},
		write:    func(ctx context.Context, data []byte) error { return conn.Write(ctx, websocket.MessageText, data) },
		deflated: deflated(resp.Header),
	}, "", ""
}

// deflated reports that a WebSocket upgrade's response agreed to permessage-deflate.
func deflated(h http.Header) bool {
	return strings.Contains(strings.ToLower(h.Get("Sec-WebSocket-Extensions")), "permessage-deflate")
}

// dialTunnel opens the encrypted link: an upgrade without a token, TLS to the pinned key inside
// it, then the token as the first frame, and the frames deflated inside the TLS from there on
// when the relay speaks flok-link/2 (the WebSocket itself carries ciphertext, which does not
// compress). ctx is the session's: it ends the tunnel.
func (c *Client) dialTunnel(ctx context.Context) (*websocket.Conn, frames, State, string) {
	if !tunnel.ValidPin(c.cfg.Pin) {
		return nil, frames{}, Refused, "[link] pin is not a pin: sha256:… as flok-relay pin prints it"
	}
	u := TunnelURL(c.url)
	dctx, cancel := context.WithTimeout(ctx, c.cfg.Dial)
	defer cancel()
	hdr := http.Header{}
	hdr.Set("User-Agent", "flok/"+c.cfg.Version)
	conn, resp, err := websocket.Dial(dctx, u, &websocket.DialOptions{HTTPClient: c.cfg.HTTP, HTTPHeader: hdr})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, frames{}, Refused, "the relay has no encrypted link (" + u + "): upgrade flok-relay, or remove [link] pin"
		}
		st, detail := dialFailed(u, resp, err)
		return nil, frames{}, st, detail
	}
	t, err := tunnel.Client(ctx, conn, c.cfg.Pin, wire.MaxRelayFrame)
	if err != nil {
		conn.CloseNow()
		if errors.Is(err, tunnel.ErrPinMismatch) {
			return nil, frames{}, Refused, err.Error() + ": a proxy that replaces it, or the relay's key changed (flok-relay pin)"
		}
		return nil, frames{}, Unreachable, "encrypted handshake: " + shortErr(err)
	}
	auth, _ := json.Marshal(wire.Frame{Type: wire.TypeAuth, Token: c.cfg.Token})
	if err := t.Write(dctx, auth); err != nil {
		conn.CloseNow()
		return nil, frames{}, Unreachable, shortErr(err)
	}
	data, err := t.Read(dctx)
	var f wire.Frame
	switch {
	case err == nil && json.Unmarshal(data, &f) == nil && f.Type == wire.TypeAuth:
		t.Compress()
		return conn, frames{read: t.Read, write: t.Write, deflated: t.Compressed()}, "", ""
	case err == nil && f.Type == wire.TypeError:
		conn.CloseNow()
		return nil, frames{}, Refused, "the relay refused the token ([link] token)"
	case err != nil:
		conn.CloseNow()
		return nil, frames{}, Unreachable, shortErr(err)
	}
	conn.CloseNow()
	return nil, frames{}, Refused, "the relay answered the token with " + string(f.Type)
}

// dialFailed names a failed upgrade.
func dialFailed(u string, resp *http.Response, err error) (State, string) {
	if resp != nil {
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return Refused, "the relay refused the token ([link] token)"
		case http.StatusForbidden:
			return Refused, "refused with 403 Forbidden: a relay that takes encrypted links only wants [link] pin (or a proxy said no)"
		case http.StatusNotFound:
			return Refused, "nothing answers at " + u + " (not a flok relay?)"
		}
		return Unreachable, fmt.Sprintf("relay answered %s", resp.Status)
	}
	return Unreachable, shortErr(err)
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
