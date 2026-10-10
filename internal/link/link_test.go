package link

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/answer"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/snapshot"
)

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"https://flok.example.net":         "wss://flok.example.net/link",
		"https://flok.example.net/":        "wss://flok.example.net/link",
		"wss://flok.example.net/link":      "wss://flok.example.net/link",
		"http://127.0.0.1:8080":            "ws://127.0.0.1:8080/link",
		"https://flok.example.net/x/link":  "wss://flok.example.net/x/link",
		"ftp://flok.example.net":           "",
		"flok.example.net":                 "",
		"":                                 "",
		"  https://flok.example.net/link ": "wss://flok.example.net/link",
	}
	for in, want := range cases {
		if got := NormalizeURL(in); got != want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
	if got := HealthURL("wss://flok.example.net/link"); got != "https://flok.example.net/healthz" {
		t.Errorf("HealthURL: %q", got)
	}
}

// relay is a stand-in for the relay's /link: it records what it gets and can speak back.
type relay struct {
	mu     sync.Mutex
	frames []wire.Frame
	conns  int
	tokens []string
	conn   *websocket.Conn
	srv    *httptest.Server
	got    chan wire.Frame
}

func newRelay(t *testing.T, refuse bool) *relay {
	r := &relay{got: make(chan wire.Frame, 64)}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/link" {
			http.NotFound(w, req)
			return
		}
		r.mu.Lock()
		r.tokens = append(r.tokens, req.Header.Get("Authorization"))
		r.conns++
		r.mu.Unlock()
		if refuse {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, req, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		r.mu.Lock()
		r.conn = c
		r.mu.Unlock()
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var f wire.Frame
			_ = json.Unmarshal(data, &f)
			r.got <- f
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *relay) next(t *testing.T, typ string) wire.Frame {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f := <-r.got:
			if f.Type == typ {
				return f
			}
		case <-deadline:
			t.Fatalf("no %s frame from the link", typ)
		}
	}
}

func (r *relay) send(t *testing.T, f wire.Frame) {
	t.Helper()
	r.mu.Lock()
	c := r.conn
	r.mu.Unlock()
	data, _ := json.Marshal(f)
	if err := c.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func waitMsg(t *testing.T, c *Client, want func(Msg) bool) Msg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-c.Messages():
			if want(m) {
				return m
			}
		case <-deadline:
			t.Fatal("the link delivered nothing of the kind")
		}
	}
}

func TestLinkSession(t *testing.T) {
	r := newRelay(t, false)
	c := New(Config{URL: r.srv.URL, Token: "t0k", Name: "home", Version: "test", Hostname: "mac", BackoffMax: 300 * time.Millisecond})
	snap := snapshot.Snapshot{Agents: []snapshot.Agent{{PaneID: "%1", Name: "api", State: agent.Working}}}
	c.Publish(snap) // before the link is up: sent as soon as it is
	c.Start()
	defer c.Close()
	hello := r.next(t, wire.TypeHello)
	if hello.Hello.Name != "home" || hello.Hello.Proto != wire.Proto || len(hello.Hello.Features) == 0 {
		t.Fatalf("hello: %+v", hello.Hello)
	}
	if r.tokens[0] != "Bearer t0k" {
		t.Fatalf("token header: %q", r.tokens[0])
	}
	waitMsg(t, c, func(m Msg) bool { return m.Status != nil && m.Status.State == Connected })
	if f := r.next(t, wire.TypeSnap); len(f.Snap.Agents) != 1 || f.Snap.Agents[0].Name != "api" {
		t.Fatalf("snap: %+v", f.Snap)
	}
	c.Publish(snap) // unchanged: not sent again
	c.Event(wire.Event{Pane: "%1", Agent: "api", Kind: "blocked", Reason: "permission:Bash"})
	if f := r.next(t, wire.TypeEvent); f.Event.Kind != "blocked" || f.Event.Agent != "api" {
		t.Fatalf("event: %+v", f.Event)
	}
	select {
	case f := <-r.got:
		t.Fatalf("an unchanged snapshot was sent: %+v", f)
	case <-time.After(150 * time.Millisecond):
	}
	// the relay asks for an answer and a subscription; the sidebar acks the answer
	r.send(t, wire.Frame{Type: wire.TypeAnswer, ID: "a1", Answer: &answer.Answer{Pane: "%1", Text: "y", Keys: []string{"Enter"}}})
	m := waitMsg(t, c, func(m Msg) bool { return m.Command != nil && m.Command.Kind == "answer" })
	if m.Command.ID != "a1" || m.Command.Pane != "%1" || m.Command.Answer.Text != "y" {
		t.Fatalf("answer command: %+v", m.Command)
	}
	c.Ack("a1", "")
	if f := r.next(t, wire.TypeAck); f.ID != "a1" || f.Error != "" {
		t.Fatalf("ack: %+v", f)
	}
	r.send(t, wire.Frame{Type: wire.TypeSubscribe, Pane: "%1", On: true})
	m = waitMsg(t, c, func(m Msg) bool { return m.Command != nil && m.Command.Kind == "subscribe" })
	if !m.Command.On || m.Command.Pane != "%1" {
		t.Fatalf("subscribe: %+v", m.Command)
	}
	c.Screen("%1", "hello\n")
	c.Screen("%1", "hello\n") // same text: once
	if f := r.next(t, wire.TypeScreen); f.Screen.Text != "hello\n" {
		t.Fatalf("screen: %+v", f.Screen)
	}
	select {
	case f := <-r.got:
		t.Fatalf("an unchanged screen was sent: %+v", f)
	case <-time.After(150 * time.Millisecond):
	}
	// the relay drops the connection: a reset command, unreachable, then a reconnect that
	// starts with the hello and the latest snapshot again
	r.mu.Lock()
	r.conn.Close(websocket.StatusGoingAway, "restart")
	r.mu.Unlock()
	waitMsg(t, c, func(m Msg) bool { return m.Command != nil && m.Command.Kind == "reset" })
	waitMsg(t, c, func(m Msg) bool { return m.Status != nil && m.Status.State == Unreachable })
	r.next(t, wire.TypeHello)
	if f := r.next(t, wire.TypeSnap); len(f.Snap.Agents) != 1 {
		t.Fatalf("snapshot after reconnect: %+v", f.Snap)
	}
	waitMsg(t, c, func(m Msg) bool { return m.Status != nil && m.Status.State == Connected })
	if r.conns < 2 {
		t.Fatalf("expected a reconnect, %d connections", r.conns)
	}
}

func TestLinkRefused(t *testing.T) {
	r := newRelay(t, true)
	c := New(Config{URL: r.srv.URL, Token: "bad", BackoffMax: 200 * time.Millisecond})
	c.Start()
	defer c.Close()
	m := waitMsg(t, c, func(m Msg) bool { return m.Status != nil && m.Status.State == Refused })
	if m.Status.Detail == "" || m.Status.RetryAt.IsZero() {
		t.Fatalf("refused: %+v", m.Status)
	}
	if New(Config{URL: ""}) != nil {
		t.Fatal("no url: no link")
	}
}
