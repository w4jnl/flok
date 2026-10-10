package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/answer"
	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/relay/apns"
	"github.com/w4jnl/flok/internal/snapshot"
)

type fakePush struct {
	mu   sync.Mutex
	sent []apns.Notification
	tok  []string
	fail error
}

func (f *fakePush) Send(_ context.Context, device string, n apns.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, n)
	f.tok = append(f.tok, device)
	return f.fail
}

// wsApp is a test app on /api/ws.
type wsApp struct {
	conn *websocket.Conn
	got  chan wire.Frame
}

func dialApp(t *testing.T, srv *httptest.Server, token string) *wsApp {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	conn, _, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	a := &wsApp{conn: conn, got: make(chan wire.Frame, 64)}
	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var f wire.Frame
			_ = json.Unmarshal(data, &f)
			a.got <- f
		}
	}()
	t.Cleanup(func() { conn.CloseNow() })
	return a
}

func (a *wsApp) send(t *testing.T, f wire.Frame) {
	t.Helper()
	data, _ := json.Marshal(f)
	if err := a.conn.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func (a *wsApp) next(t *testing.T, want func(wire.Frame) bool) wire.Frame {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f := <-a.got:
			if want(f) {
				return f
			}
		case <-deadline:
			t.Fatal("the app got nothing of the kind")
		}
	}
}

func api(t *testing.T, srv *httptest.Server, token, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// An instance links in, the app sees it (REST and WebSocket), an event becomes a push, an
// answer travels to the instance and its ack back, a subscription streams the screen, and the
// instance going away is reported.
func TestRelayEndToEnd(t *testing.T) {
	push := &fakePush{}
	s, err := New(Config{InstanceTokens: []string{"inst-tok"}, DeviceTokens: []string{"dev-tok"}, DataDir: t.TempDir(), Push: push, AnswerWait: 2 * time.Second,
		Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// auth
	if code, _ := api(t, srv, "", "GET", "/api/instances", nil); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := api(t, srv, "inst-tok", "GET", "/api/instances", nil); code != http.StatusUnauthorized {
		t.Fatalf("an instance token is not a device token: %d", code)
	}
	if code, out := api(t, srv, "", "GET", "/healthz", nil); code != 200 || out["ok"] != true {
		t.Fatalf("healthz: %d %v", code, out)
	}
	// a device registers (what the app does with its APNs token)
	if code, _ := api(t, srv, "dev-tok", "POST", "/api/devices", map[string]any{"token": "devicetoken123", "name": "jaro's phone"}); code != 200 {
		t.Fatalf("register: %d", code)
	}

	// the app connects first: nothing yet
	app := dialApp(t, srv, "dev-tok")
	if f := app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeInstances }); len(f.Instances) != 0 {
		t.Fatalf("instances before any link: %+v", f.Instances)
	}

	// the instance links in with the real client
	snap := snapshot.Snapshot{Unseen: 1, Agents: []snapshot.Agent{{PaneID: "%1", Name: "api", State: agent.Blocked, Reason: "permission:Bash", Unseen: 1}}}
	cl := link.New(link.Config{URL: srv.URL, Token: "inst-tok", Name: "home", Version: "test", Hostname: "mac"})
	cmds := make(chan link.Command, 16)
	go func() {
		for m := range cl.Messages() {
			if m.Command != nil {
				cmds <- *m.Command
			}
		}
	}()
	cl.Publish(snap)
	cl.Start()
	defer cl.Close()
	f := app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeInstance && f.Inst != nil && f.Inst.Snap != nil })
	if f.Inst.Name != "home" || !f.Inst.Online || f.Inst.Snap.Agents[0].Name != "api" || f.Inst.Version != "test" {
		t.Fatalf("instance frame: %+v", f.Inst)
	}
	if code, out := api(t, srv, "dev-tok", "GET", "/api/instances", nil); code != 200 || len(out["instances"].([]any)) != 1 {
		t.Fatalf("instances: %d %v", code, out)
	}
	if code, out := api(t, srv, "", "GET", "/healthz", nil); code != 200 || out["online"].(float64) != 1 {
		t.Fatalf("healthz online: %v", out)
	}

	// an event: the app hears it, the phone gets a push with the category and the badge
	cl.Event(wire.Event{Pane: "%1", Agent: "api", Kind: "blocked", Reason: "permission:Bash", At: time.Now()})
	app.next(t, func(f wire.Frame) bool {
		return f.Type == wire.TypeEvent && f.Instance == "home" && f.Event.Kind == "blocked"
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		push.mu.Lock()
		n := len(push.sent)
		push.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	push.mu.Lock()
	if len(push.sent) != 1 || push.tok[0] != "devicetoken123" || push.sent[0].Title != "home · api" || push.sent[0].Body != "needs you: perm:Bash" ||
		push.sent[0].Category != CategoryPermission || *push.sent[0].Badge != 1 || push.sent[0].CollapseID != "home:%1" || !push.sent[0].TimeSensitive {
		t.Fatalf("push: %+v", push.sent)
	}
	push.mu.Unlock()

	// the app answers over HTTP: the instance gets the frame, acks it, the app sees ok
	go func() {
		c := <-cmds
		if c.Kind != "answer" || c.Answer.Text != "y" || c.Answer.Keys[0] != "Enter" || c.Pane != "%1" {
			t.Errorf("answer command: %+v", c)
		}
		cl.Ack(c.ID, "")
	}()
	if code, out := api(t, srv, "dev-tok", "POST", "/api/answer", map[string]any{"instance": "home", "pane": "%1", "text": "y", "keys": []string{"enter"}}); code != 200 || out["ok"] != true {
		t.Fatalf("answer: %d %v", code, out)
	}
	// … and one the instance refuses
	go func() {
		c := <-cmds
		cl.Ack(c.ID, "no agent in pane %9")
	}()
	if code, out := api(t, srv, "dev-tok", "POST", "/api/answer", map[string]any{"instance": "home", "pane": "%9", "text": "y"}); code != http.StatusBadGateway || out["error"] != "no agent in pane %9" {
		t.Fatalf("refused answer: %d %v", code, out)
	}
	// a bad key never reaches the instance
	if code, _ := api(t, srv, "dev-tok", "POST", "/api/answer", map[string]any{"instance": "home", "pane": "%1", "keys": []string{"F12"}}); code != http.StatusBadRequest {
		t.Fatalf("bad key: %d", code)
	}
	if code, out := api(t, srv, "dev-tok", "POST", "/api/answer", map[string]any{"instance": "office", "pane": "%1", "text": "y"}); code != http.StatusBadGateway || out["error"] != "no instance office" {
		t.Fatalf("unknown instance: %d %v", code, out)
	}
	// over the WebSocket, with an id
	go func() {
		c := <-cmds
		cl.Ack(c.ID, "")
	}()
	app.send(t, wire.Frame{Type: wire.TypeAnswer, ID: "q1", Instance: "home", Answer: &answer.Answer{Pane: "%1", Keys: []string{"Escape"}}})
	if f := app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeAck && f.ID == "q1" }); f.Error != "" {
		t.Fatalf("ws ack: %+v", f)
	}

	// a subscription: the instance is asked once, screens flow to the app, and stop when it leaves
	app.send(t, wire.Frame{Type: wire.TypeSubscribe, Instance: "home", Pane: "%1", On: true})
	c := <-cmds
	if c.Kind != "subscribe" || !c.On || c.Pane != "%1" {
		t.Fatalf("subscribe command: %+v", c)
	}
	cl.Screen("%1", "Allow Bash? [y/n]\n")
	if f := app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeScreen }); f.Screen.Text != "Allow Bash? [y/n]\n" || f.Instance != "home" {
		t.Fatalf("screen: %+v", f)
	}
	app2 := dialApp(t, srv, "dev-tok")
	app2.send(t, wire.Frame{Type: wire.TypeSubscribe, Instance: "home", Pane: "%1", On: true}) // a second viewer: no second subscribe to the instance
	app2.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeInstances })
	app2.conn.Close(websocket.StatusNormalClosure, "bye")
	app.send(t, wire.Frame{Type: wire.TypeSubscribe, Instance: "home", Pane: "%1", On: false})
	c = <-cmds
	if c.Kind != "subscribe" || c.On {
		t.Fatalf("expected the unsubscribe, got %+v", c)
	}
	select {
	case c := <-cmds:
		t.Fatalf("unexpected command: %+v", c)
	case <-time.After(100 * time.Millisecond):
	}

	// the instance goes away: offline to the app, an answer is refused
	cl.Close()
	f = app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeInstance && f.Inst != nil && !f.Inst.Online })
	if f.Inst.Snap == nil {
		t.Fatal("the last snapshot is kept while offline")
	}
	if code, out := api(t, srv, "dev-tok", "POST", "/api/answer", map[string]any{"instance": "home", "pane": "%1", "text": "y"}); code != http.StatusBadGateway || out["error"] != "home is offline" {
		t.Fatalf("offline answer: %d %v", code, out)
	}
	// devices: listed short, removable
	if code, out := api(t, srv, "dev-tok", "GET", "/api/devices", nil); code != 200 || len(out["devices"].([]any)) != 1 {
		t.Fatalf("devices: %v", out)
	}
	if code, _ := api(t, srv, "dev-tok", "DELETE", "/api/devices/devicetoken123", nil); code != 200 {
		t.Fatalf("device remove: %d", code)
	}
}

// A link with a wrong token is turned away at the handshake, a hello with another protocol is
// closed with a policy violation.
func TestRelayRefuses(t *testing.T) {
	s, _ := New(Config{InstanceTokens: []string{"inst-tok"}, DeviceTokens: []string{"dev-tok"}})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/link"
	_, resp, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer nope"}}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: %v %v", err, resp)
	}
	conn, _, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer inst-tok"}}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(wire.Frame{Type: wire.TypeHello, Hello: &wire.Hello{Proto: 99, Name: "x"}})
	_ = conn.Write(context.Background(), websocket.MessageText, data)
	_, _, err = conn.Read(context.Background())
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("protocol 99: %v", err)
	}
}

func TestAlertWords(t *testing.T) {
	n := Alert("office", wire.Event{Pane: "beta:%3", Host: "beta", Agent: "docs", Kind: "done"}, 0)
	if n.Title != "office · beta/docs" || n.Body != "finished" || n.Category != CategoryDone || n.TimeSensitive {
		t.Fatalf("done: %+v", n)
	}
	n = Alert("home", wire.Event{Pane: "%1", Agent: "api", Kind: "blocked", Reason: "question"}, 2)
	if n.Body != "needs you: question" || n.Category != CategoryQuestion || *n.Badge != 2 {
		t.Fatalf("question: %+v", n)
	}
	if n := Alert("home", wire.Event{Pane: "%1", Agent: "api", Kind: "error"}, 0); n.Body != "failed" {
		t.Fatalf("error: %+v", n)
	}
	if NewToken() == NewToken() || len(NewToken()) != 48 {
		t.Fatal("tokens must be random hex")
	}
}

// A second link under the same name replaces the first: the old connection is closed, its
// frames are ignored, the instance stays online.
func TestRelayReplacesALink(t *testing.T) {
	s, _ := New(Config{InstanceTokens: []string{"inst-tok"}, DeviceTokens: []string{"dev-tok"}})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/link"
	dial := func() *websocket.Conn {
		c, _, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer inst-tok"}}})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(wire.Frame{Type: wire.TypeHello, Hello: &wire.Hello{Proto: wire.Proto, Name: "home", Version: "a"}})
		if err := c.Write(context.Background(), websocket.MessageText, data); err != nil {
			t.Fatal(err)
		}
		return c
	}
	first := dial()
	app := dialApp(t, srv, "dev-tok")
	app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeInstances })
	second := dial()
	defer second.CloseNow()
	_, _, err := first.Read(context.Background())
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("the first link should be closed as replaced: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if code, out := api(t, srv, "", "GET", "/healthz", nil); code != 200 || out["online"].(float64) != 1 {
		t.Fatalf("still one instance online: %v", out)
	}
	// the app saw it come (twice) and never go
	for {
		select {
		case f := <-app.got:
			if f.Type == wire.TypeInstance && f.Inst != nil && !f.Inst.Online {
				t.Fatal("the instance was reported offline across the replacement")
			}
			continue
		case <-time.After(100 * time.Millisecond):
		}
		break
	}
}
