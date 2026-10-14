package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	mu     sync.Mutex
	sent   []apns.Notification
	to     []apns.Device
	fail   error
	failTo map[string]error // by device token, over fail
}

func (f *fakePush) Send(_ context.Context, d apns.Device, n apns.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, n)
	f.to = append(f.to, d)
	if err, ok := f.failTo[d.Token]; ok {
		return err
	}
	return f.fail
}

// wsApp is a test app on /api/ws.
type wsApp struct {
	conn *websocket.Conn
	got  chan wire.Frame
}

func dialApp(t *testing.T, srv *httptest.Server, token string) *wsApp {
	t.Helper()
	a, _ := dialAppWith(t, srv, token, websocket.CompressionDisabled)
	return a
}

// dialAppWith is dialApp offering the compression mode, with the upgrade's response.
func dialAppWith(t *testing.T, srv *httptest.Server, token string, mode websocket.CompressionMode) (*wsApp, *http.Response) {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	conn, resp, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
		CompressionMode: mode})
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
	return a, resp
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
	// a device registers (what the app does with its APNs token; an Xcode build's is a sandbox one)
	if code, _ := api(t, srv, "dev-tok", "POST", "/api/devices", map[string]any{"token": "devicetoken123", "name": "jaro's phone", "sandbox": true}); code != 200 {
		t.Fatalf("register: %d", code)
	}

	// the app connects first: nothing yet
	app := dialApp(t, srv, "dev-tok")
	if f := app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeInstances }); len(f.Instances) != 0 {
		t.Fatalf("instances before any link: %+v", f.Instances)
	}

	// the instance links in with the real client
	snap := snapshot.Snapshot{Unseen: 1, Agents: []snapshot.Agent{{PaneID: "%1", SessionName: "Hugo", Name: "api", State: agent.Blocked, Reason: "permission:Bash", Unseen: 1}}}
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
	if len(push.sent) != 1 || push.to[0] != (apns.Device{Token: "devicetoken123", Sandbox: true}) || push.sent[0].Title != "api" || push.sent[0].Subtitle != "home · Hugo" ||
		push.sent[0].Body != "● needs you: perm:Bash" || push.sent[0].Sound != SoundRequest || !push.sent[0].Mutable || push.sent[0].Data["push_kind"] != KindPermission ||
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
	app2.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeInstances })
	app2.send(t, wire.Frame{Type: wire.TypeSubscribe, Instance: "home", Pane: "%1", On: true}) // a second viewer: no second subscribe to the instance …
	if f := app2.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeScreen }); f.Screen.Text != "Allow Bash? [y/n]\n" {
		t.Fatalf("… but the screen the first one has, at once: %+v", f) // the instance resends only a changed screen
	}
	app2.conn.Close(websocket.StatusNormalClosure, "bye")
	// one look without a stream (the notification's expanded view) takes the held screen too
	if code, out := api(t, srv, "dev-tok", "GET", "/api/screen?instance=home&pane=%251", nil); code != 200 || out["text"] != "Allow Bash? [y/n]\n" {
		t.Fatalf("screen while subscribed: %d %v", code, out)
	}
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
	// one look at a pane nobody watches: subscribe, the first screen, unsubscribe
	go func() {
		c := <-cmds
		if c.Kind != "subscribe" || !c.On || c.Pane != "%1" {
			t.Errorf("one look subscribes: %+v", c)
		}
		cl.Screen("%1", "fresh\n")
	}()
	if code, out := api(t, srv, "dev-tok", "GET", "/api/screen?instance=home&pane=%251", nil); code != 200 || out["text"] != "fresh\n" {
		t.Fatalf("one look: %d %v", code, out)
	}
	if c := <-cmds; c.Kind != "subscribe" || c.On {
		t.Fatalf("one look lets go: %+v", c)
	}
	if code, _ := api(t, srv, "dev-tok", "GET", "/api/screen?instance=office&pane=%251", nil); code != http.StatusBadGateway {
		t.Fatalf("one look at an unknown instance: %d", code)
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
	n := Alert("office", wire.Event{Pane: "beta:%3", Host: "beta", Agent: "docs", Kind: "done"}, 0, "Docs")
	if n.Title != "docs" || n.Subtitle != "office / beta · Docs" || n.Body != "✓ done" || n.Category != CategoryDone || n.TimeSensitive || n.Sound != SoundDone {
		t.Fatalf("done: %+v", n)
	}
	n = Alert("home", wire.Event{Pane: "%1", Agent: "api", Kind: "blocked", Reason: "question"}, 2, "")
	if n.Subtitle != "home" || n.Body != "● needs you: question" || n.Category != CategoryQuestion || *n.Badge != 2 || !n.TimeSensitive || n.Sound != SoundRequest {
		t.Fatalf("question: %+v", n)
	}
	if n := Alert("home", wire.Event{Pane: "%1", Agent: "api", Kind: "blocked", Reason: "permission"}, 0, ""); n.Body != "● needs you: perm" || n.Category != CategoryPermission {
		t.Fatalf("a permission without a tool: %+v", n)
	}
	if n := Alert("home", wire.Event{Pane: "%1", Agent: "api", Kind: "error"}, 0, ""); n.Body != "✗ failed" || n.Data["push_kind"] != KindError {
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

func TestDeviceLabelTellsRegistrationsApart(t *testing.T) {
	if got := (device{Name: "iPhone", Token: "04344cb7abcdef00"}).label(); got != "iPhone 04344cb7… production" {
		t.Fatalf("label: %q", got)
	}
	if got := (device{Token: "d14088c4abcdef00", Sandbox: true}).label(); got != "device d14088c4… sandbox" {
		t.Fatalf("label without a name: %q", got)
	}
}

func TestPrefsFilterAndSound(t *testing.T) {
	now := time.Now()
	soon, past := now.Add(time.Hour), now.Add(-time.Minute)
	off, on := false, true
	p := &Prefs{
		Kinds: map[string]KindPrefs{KindDone: {Off: true}, KindQuestion: {Sound: "none", TimeSensitive: &off}, KindError: {Sound: "default", TimeSensitive: &on}},
		Mutes: []Mute{{Instance: "home", Agent: "api", Until: &soon}, {Instance: "home", Host: "beta", Agent: "docs"}, {Instance: "home", Agent: "old", Until: &past}},
	}
	perm := wire.Event{Agent: "web", Kind: "blocked", Reason: "permission:Bash"}
	cases := []struct {
		ev   wire.Event
		want bool
		why  string
	}{
		{perm, true, "a permission prompt of an unmuted agent"},
		{wire.Event{Agent: "web", Kind: "done"}, false, "done is off"},
		{wire.Event{Agent: "api", Kind: "blocked", Reason: "permission:Bash"}, false, "api is muted for an hour"},
		{wire.Event{Agent: "docs", Host: "beta", Kind: "error"}, false, "beta's docs is muted for good"},
		{wire.Event{Agent: "docs", Kind: "error"}, true, "the local docs is another agent"},
		{wire.Event{Agent: "old", Kind: "error"}, true, "an expired mute does not count"},
	}
	for _, c := range cases {
		if got := p.Allows("home", c.ev, now); got != c.want {
			t.Errorf("%s: Allows = %v", c.why, got)
		}
	}
	if p.Allows("office", wire.Event{Agent: "api", Kind: "error"}, now) != true {
		t.Error("a mute is per instance")
	}
	if p.Sound(KindPermission) != SoundRequest || p.Sound(KindQuestion) != "" || p.Sound(KindError) != "default" || (*Prefs)(nil).Sound(KindDone) != SoundDone {
		t.Error("sounds: flok by default, silent and the system sound when chosen")
	}
	if !p.TimeSensitive(KindPermission) || p.TimeSensitive(KindQuestion) || !p.TimeSensitive(KindError) || (*Prefs)(nil).TimeSensitive(KindDone) {
		t.Error("time-sensitive: on for prompts and questions unless switched off, off otherwise unless switched on")
	}
	if (&Prefs{Kinds: map[string]KindPrefs{"bogus": {}}}).valid() || (&Prefs{Kinds: map[string]KindPrefs{KindDone: {Sound: "beep"}}}).valid() || (&Prefs{Mutes: []Mute{{Agent: "x"}}}).valid() {
		t.Error("unknown kinds and sounds, a mute without an instance: refused")
	}
	p.prune(now)
	if len(p.Mutes) != 2 {
		t.Errorf("prune keeps the live mutes: %+v", p.Mutes)
	}
}

// Each phone's settings decide its pushes: a filtered one is not sent, the others carry the
// phone's sound; settings come with the registration and through PUT …/prefs.
func TestRelayPushesByPrefs(t *testing.T) {
	push := &fakePush{}
	s, err := New(Config{InstanceTokens: []string{"inst-tok"}, DeviceTokens: []string{"dev-tok"}, DataDir: t.TempDir(), Push: push, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	if code, _ := api(t, srv, "dev-tok", "POST", "/api/devices", map[string]any{"token": "quietphone01", "name": "quiet",
		"prefs": map[string]any{"kinds": map[string]any{"done": map[string]any{"off": true}}}}); code != 200 {
		t.Fatalf("register with prefs: %d", code)
	}
	if code, _ := api(t, srv, "dev-tok", "POST", "/api/devices", map[string]any{"token": "loudphone001", "name": "loud"}); code != 200 {
		t.Fatalf("register: %d", code)
	}
	if code, _ := api(t, srv, "dev-tok", "PUT", "/api/devices/loudphone001/prefs", map[string]any{"kinds": map[string]any{"done": map[string]any{"sound": "default"}}}); code != 200 {
		t.Fatalf("prefs: %d", code)
	}
	if code, _ := api(t, srv, "dev-tok", "PUT", "/api/devices/nosuchphone1/prefs", map[string]any{}); code != http.StatusNotFound {
		t.Fatalf("prefs for an unknown phone: %d", code)
	}
	if code, _ := api(t, srv, "dev-tok", "PUT", "/api/devices/loudphone001/prefs", map[string]any{"kinds": map[string]any{"done": map[string]any{"sound": "beep"}}}); code != http.StatusBadRequest {
		t.Fatalf("an unknown sound: %d", code)
	}
	// a curl registration without prefs keeps the phone's
	if code, _ := api(t, srv, "dev-tok", "POST", "/api/devices", map[string]any{"token": "quietphone01", "name": "quiet"}); code != 200 {
		t.Fatalf("re-register: %d", code)
	}
	in := &instance{name: "home", subs: map[string]int{}, gen: 1, online: true}
	s.mu.Lock()
	s.instances["home"] = in
	s.mu.Unlock()
	s.event(in, 1, wire.Event{Pane: "%1", Agent: "api", Kind: "done"})
	deadline := time.Now().Add(2 * time.Second)
	for {
		push.mu.Lock()
		n := len(push.sent)
		push.mu.Unlock()
		if n >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // a second push would have been sent by now
	push.mu.Lock()
	defer push.mu.Unlock()
	if len(push.sent) != 1 || push.to[0].Token != "loudphone001" || push.sent[0].Sound != "default" {
		t.Fatalf("only the loud phone, with its sound: %+v → %+v", push.to, push.sent)
	}
}

// An agent that stops waiting on the user (seen, answered) becomes one silent sync push to
// every phone with what still waits and the badge; a burst of changes is one push.
func TestRelaySyncsWhenAgentsStopWaiting(t *testing.T) {
	push := &fakePush{}
	s, err := New(Config{InstanceTokens: []string{"inst-tok"}, DeviceTokens: []string{"dev-tok"}, Push: push, SyncDelay: 30 * time.Millisecond, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.devices.add(device{Token: "phonetoken01", Name: "phone", Added: time.Now()})
	in := &instance{name: "home", subs: map[string]int{}, gen: 1, online: true}
	s.mu.Lock()
	s.instances["home"] = in
	s.mu.Unlock()
	snap := func(unseen int, states ...agent.State) *snapshot.Snapshot {
		sn := &snapshot.Snapshot{Unseen: unseen}
		for i, st := range states {
			a := snapshot.Agent{PaneID: fmt.Sprintf("%%%d", i+1), Name: fmt.Sprint("a", i), State: st}
			if st == agent.Done {
				a.Unseen = 1
			}
			sn.Agents = append(sn.Agents, a)
		}
		return sn
	}
	sent := func() int {
		push.mu.Lock()
		defer push.mu.Unlock()
		return len(push.sent)
	}
	s.setSnap(in, 1, snap(1, agent.Done, agent.Blocked)) // %1 finished unseen, %2 asks
	s.setSnap(in, 1, snap(1, agent.Done, agent.Blocked)) // nothing stopped waiting
	time.Sleep(80 * time.Millisecond)
	if n := sent(); n != 0 {
		t.Fatalf("no sync while everything still waits: %d", n)
	}
	s.setSnap(in, 1, snap(1, agent.Done, agent.Working)) // %2 answered
	s.setSnap(in, 1, snap(0, agent.Idle, agent.Working)) // %1 seen, in the same breath
	deadline := time.Now().Add(2 * time.Second)
	for sent() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)
	push.mu.Lock()
	defer push.mu.Unlock()
	if len(push.sent) != 1 {
		t.Fatalf("one sync for the burst: %+v", push.sent)
	}
	n := push.sent[0]
	if !n.Background || n.Data["flok_sync"] != 1 || n.Data["badge"] != 0 || len(n.Data["waiting"].([]string)) != 0 || n.Title != "" {
		t.Fatalf("sync: %+v", n)
	}
}

func TestOverviewCountsAndRanks(t *testing.T) {
	s, _ := New(Config{InstanceTokens: []string{"i"}, DeviceTokens: []string{"d"}})
	t0 := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	s.instances["home"] = &instance{name: "home", online: true, snap: &snapshot.Snapshot{Agents: []snapshot.Agent{
		{PaneID: "%1", Name: "w-old", State: agent.Working, StateSince: at(0), Tool: "Edit"},
		{PaneID: "%2", Name: "w-new", State: agent.Working, StateSince: at(5)},
		{PaneID: "%3", Name: "asks", State: agent.Blocked, Reason: "permission:Bash", StateSince: at(7)},
		{PaneID: "%4", Name: "done-seen", State: agent.Done, StateSince: at(1)},
		{PaneID: "%5", Name: "done-unseen", State: agent.Done, Unseen: 1, StateSince: at(6)},
		{PaneID: "%6", Name: "idle", State: agent.Idle},
	}}}
	s.instances["office"] = &instance{name: "office", online: false, snap: &snapshot.Snapshot{Agents: []snapshot.Agent{
		{PaneID: "%1", Name: "offline-working", State: agent.Working, StateSince: at(0)},
	}}}
	ov := s.overviewLocked()
	if ov.Working != 2 || ov.Waiting != 1 || ov.Done != 1 {
		t.Fatalf("counts (an offline instance does not count): %+v", ov)
	}
	if len(ov.Rows) != 3 || ov.Rows[0].Agent != "asks" || ov.Rows[0].Label != "needs you: perm:Bash" || ov.Rows[1].Agent != "done-unseen" || ov.Rows[2].Agent != "w-old" || ov.Rows[2].Label != "working" {
		t.Fatalf("rows: asking, finished unseen, longest working; no tool: %+v", ov.Rows)
	}
	if ov.Rows[2].Since != float64(at(0).Unix()) || ov.Rows[0].Pane != "%3" {
		t.Fatalf("since and pane: %+v", ov.Rows[2])
	}
	if w := overviewWords(ov); w != "2 working · 1 needs you · 1 done" {
		t.Fatalf("words: %q", w)
	}
	if !strings.Contains(ov.json(), `"rows":[{"instance":"home","host":"","agent":"asks","pane":"%3","state":"blocked"`) {
		t.Fatalf("every field present, the app's names: %s", ov.json())
	}
}

// The overview starts by push on an idle → busy edge (once, until the phone answers), follows
// changes (a rise in waiting is urgent), and ends after the quiet; a followed agent's activity
// ends a little after it finished; a refused activity token costs only that token.
func TestLiveActivities(t *testing.T) {
	push := &fakePush{}
	s, err := New(Config{InstanceTokens: []string{"i"}, DeviceTokens: []string{"d"}, Push: push, LiveDelay: 10 * time.Millisecond,
		QuietEnd: 150 * time.Millisecond, AgentLinger: time.Minute, SyncDelay: time.Hour, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	_ = s.devices.add(device{Token: "phonetoken01", Name: "phone", Added: time.Now()})
	in := &instance{name: "home", subs: map[string]int{}, gen: 1, online: true}
	s.mu.Lock()
	s.instances["home"] = in
	s.mu.Unlock()
	snap := func(agents ...snapshot.Agent) *snapshot.Snapshot { return &snapshot.Snapshot{Agents: agents} }
	work := snapshot.Agent{PaneID: "%1", Name: "api", State: agent.Working, StateSince: time.Now()}
	ask := snapshot.Agent{PaneID: "%1", Name: "api", State: agent.Blocked, Reason: "question", StateSince: time.Now()}
	idle := snapshot.Agent{PaneID: "%1", Name: "api", State: agent.Idle}
	lives := func() []apns.Notification {
		push.mu.Lock()
		defer push.mu.Unlock()
		var out []apns.Notification
		for _, n := range push.sent {
			if n.Live != nil {
				out = append(out, n)
			}
		}
		return out
	}
	waitFor := func(n int, why string) []apns.Notification {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for len(lives()) < n && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(40 * time.Millisecond)
		got := lives()
		if len(got) != n {
			t.Fatalf("%s: %d live pushes: %+v", why, len(got), got)
		}
		return got
	}

	// no start token yet: busy, nothing to start
	s.setSnap(in, 1, snap(work))
	waitFor(0, "without a start token")
	s.setSnap(in, 1, snap(idle))
	time.Sleep(30 * time.Millisecond)
	if code, _ := api(t, srv, "d", "PUT", "/api/devices/phonetoken01/activity", map[string]any{"start_token": "starttoken01", "activities": []any{}}); code != 200 {
		t.Fatalf("activity: %d", code)
	}
	s.setSnap(in, 1, snap(work)) // idle → busy: one start
	got := waitFor(1, "the start")
	if l := got[0].Live; l.Event != "start" || l.AttributesType != "FlokActivityAttributes" || l.Attributes.(LiveAttributes).Kind != "overview" || got[0].Body != "1 working" {
		t.Fatalf("start: %+v %+v", got[0], l)
	}
	push.mu.Lock()
	startTo := push.to[len(push.to)-1].Token
	push.mu.Unlock()
	if startTo != "starttoken01" {
		t.Fatalf("a start goes to the start token: %s", startTo)
	}
	s.setSnap(in, 1, snap(idle))
	s.setSnap(in, 1, snap(work)) // another edge, but the phone has not answered the first start
	waitFor(1, "no second start while the first is unanswered")

	// the phone reports the activity: it gets the state, then each change; a rise in waiting is urgent
	if code, _ := api(t, srv, "d", "PUT", "/api/devices/phonetoken01/activity", map[string]any{"start_token": "starttoken01",
		"activities": []any{map[string]any{"id": "A1", "token": "overviewtok1", "kind": "overview"}}}); code != 200 {
		t.Fatal("report")
	}
	got = waitFor(2, "the reported activity gets the state")
	if l := got[1].Live; l.Event != "update" || l.ContentState.(LiveState).Working != 1 || l.Urgent {
		t.Fatalf("update: %+v", l)
	}
	s.setSnap(in, 1, snap(work)) // unchanged: nothing
	waitFor(2, "an unchanged state is not pushed again")
	s.setSnap(in, 1, snap(ask))
	got = waitFor(3, "the question")
	if l := got[2].Live; l.ContentState.(LiveState).Waiting != 1 || !l.Urgent || l.ContentState.(LiveState).Rows[0].Label != "needs you: question" {
		t.Fatalf("urgent update: %+v", l)
	}
	s.setSnap(in, 1, snap(idle)) // quiet: an update now, the end after QuietEnd
	got = waitFor(5, "quiet: the update, then the end")
	if got[3].Live.Event != "update" || got[4].Live.Event != "end" || got[4].Live.Dismissal.IsZero() {
		t.Fatalf("end: %+v %+v", got[3].Live, got[4].Live)
	}
	if acts := s.devices.list()[0].Activities; len(acts) != 0 {
		t.Fatalf("an ended activity is forgotten: %+v", acts)
	}

	// a followed agent: updates while it works, ends with a linger once it finished
	if code, _ := api(t, srv, "d", "PUT", "/api/devices/phonetoken01/activity", map[string]any{"start_token": "starttoken01",
		"activities": []any{map[string]any{"id": "F1", "token": "followtoken1", "kind": "agent", "instance": "home", "pane": "%1"}}}); code != 200 {
		t.Fatal("report follow")
	}
	waitFor(6, "an idle followed agent ends at once") // idle → end
	if acts := s.devices.list()[0].Activities; len(acts) != 0 {
		t.Fatalf("ended: %+v", acts)
	}
	s.mu.Lock()
	s.liveStarted["phonetoken01"] = time.Now() // keep the overview's start out of this part
	s.mu.Unlock()
	s.setSnap(in, 1, snap(work))
	time.Sleep(30 * time.Millisecond)
	if code, _ := api(t, srv, "d", "PUT", "/api/devices/phonetoken01/activity", map[string]any{
		"activities": []any{map[string]any{"id": "F2", "token": "followtoken2", "kind": "agent", "instance": "home", "pane": "%1"}}}); code != 200 {
		t.Fatal("report follow 2")
	}
	got = waitFor(7, "the followed agent's state")
	if l := got[6].Live; l.Event != "update" || l.ContentState.(LiveState).Rows[0].State != "working" {
		t.Fatalf("follow update: %+v", l)
	}
	s.setSnap(in, 1, snap(snapshot.Agent{PaneID: "%1", Name: "api", State: agent.Done, Unseen: 1}))
	got = waitFor(8, "finished: the end")
	if l := got[7].Live; l.Event != "end" || l.ContentState.(LiveState).Rows[0].State != "done" || time.Until(l.Dismissal) < 30*time.Second {
		t.Fatalf("follow end lingers: %+v", l)
	}

	// a refused activity token: only that token goes, the phone stays
	push.mu.Lock()
	push.fail = apns.ErrUnregistered
	push.mu.Unlock()
	if code, _ := api(t, srv, "d", "PUT", "/api/devices/phonetoken01/activity", map[string]any{
		"activities": []any{map[string]any{"id": "A2", "token": "overviewtok2", "kind": "overview"}}}); code != 200 {
		t.Fatal("report 3")
	}
	waitFor(9, "the push to the stale token")
	time.Sleep(30 * time.Millisecond)
	if list := s.devices.list(); len(list) != 1 || len(list[0].Activities) != 0 {
		t.Fatalf("the phone stays, the token goes: %+v", list)
	}
	if code, _ := api(t, srv, "d", "PUT", "/api/devices/phonetoken01/activity", map[string]any{"activities": []any{map[string]any{"id": "x", "token": "t", "kind": "agent"}}}); code != http.StatusBadRequest {
		t.Fatalf("an agent activity without instance and pane: %d", code)
	}
}

// A long, multi-line text and a file travel to an instance that takes them (its hello says so,
// and the app can see that); an instance without those features is told what it needs, before
// anything is sent.
func TestRelayLongTextAndFiles(t *testing.T) {
	s, err := New(Config{InstanceTokens: []string{"inst-tok"}, DeviceTokens: []string{"dev-tok"}, DataDir: t.TempDir(), AnswerWait: 2 * time.Second, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	snap := snapshot.Snapshot{Agents: []snapshot.Agent{{PaneID: "%1", Name: "api", State: agent.Blocked}}}
	cl := link.New(link.Config{URL: srv.URL, Token: "inst-tok", Name: "home", Version: "test", Hostname: "mac"})
	cmds := make(chan link.Command, 16)
	go func() {
		for m := range cl.Messages() {
			if m.Command != nil {
				cmds <- *m.Command
				if m.Command.Kind == "answer" {
					cl.Ack(m.Command.ID, "")
				}
			}
		}
	}()
	cl.Publish(snap)
	cl.Start()
	defer cl.Close()
	app := dialApp(t, srv, "dev-tok")
	f := app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeInstance && f.Inst != nil && f.Inst.Snap != nil })
	if !strings.Contains(strings.Join(f.Inst.Features, " "), wire.FeatureFiles) || !strings.Contains(strings.Join(f.Inst.Features, " "), wire.FeatureLongText) {
		t.Fatalf("features: %v", f.Inst.Features)
	}
	// a long text with newlines, through HTTP
	long := strings.Repeat("a line of text\n", 30)
	if code, out := api(t, srv, "dev-tok", "POST", "/api/answer", map[string]any{"instance": "home", "pane": "%1", "text": long, "keys": []string{"Enter"}}); code != 200 {
		t.Fatalf("long answer: %d %v", code, out)
	}
	if c := <-cmds; c.Kind != "answer" || c.Answer.Text != long || c.Answer.Keys[0] != "Enter" {
		t.Fatalf("long command: %q", c.Answer.Text)
	}
	// a file, base64 in JSON
	data := []byte{0x89, 'P', 'N', 'G', 0, 1, 2}
	if code, out := api(t, srv, "dev-tok", "POST", "/api/answer", map[string]any{"instance": "home", "pane": "%1", "text": "see", "keys": []string{"Enter"},
		"file": map[string]any{"name": "Shot 1.PNG", "data": data}}); code != 200 {
		t.Fatalf("file answer: %d %v", code, out)
	}
	if c := <-cmds; c.Answer.File == nil || c.Answer.File.Name != "Shot_1.png" || c.Answer.File.Mime != "image/png" || !bytes.Equal(c.Answer.File.Data, data) || c.Answer.Text != "see" {
		t.Fatalf("file command: %+v", c.Answer)
	}
	if code, out := api(t, srv, "dev-tok", "POST", "/api/answer", map[string]any{"instance": "home", "pane": "%1", "file": map[string]any{"name": "x.exe", "data": data}}); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "not taken") {
		t.Fatalf("exe: %d %v", code, out)
	}
	if code, _ := api(t, srv, "dev-tok", "POST", "/api/answer", map[string]any{"instance": "home", "pane": "%1", "text": strings.Repeat("x", answer.MaxLongText+1)}); code != http.StatusBadRequest {
		t.Fatalf("over the limit: %d", code)
	}
	// an instance from before: its hello has neither feature
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/link"
	old, _, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer inst-tok"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer old.CloseNow()
	hello, _ := json.Marshal(wire.Frame{Type: wire.TypeHello, Hello: &wire.Hello{Proto: wire.Proto, Name: "old", Version: "0.7.1", Features: []string{wire.FeatureAnswer, wire.FeatureScreen}}})
	if err := old.Write(context.Background(), websocket.MessageText, hello); err != nil {
		t.Fatal(err)
	}
	app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeInstance && f.Inst != nil && f.Inst.Name == "old" })
	if code, out := api(t, srv, "dev-tok", "POST", "/api/answer", map[string]any{"instance": "old", "pane": "%1", "text": "one\ntwo"}); code != http.StatusBadRequest ||
		out["error"] != "flok 0.7.1 on old takes at most 200 characters on one line: update flok there for longer text" {
		t.Fatalf("old long: %d %v", code, out)
	}
	if code, out := api(t, srv, "dev-tok", "POST", "/api/answer", map[string]any{"instance": "old", "pane": "%1", "file": map[string]any{"name": "x.png", "data": data}}); code != http.StatusBadRequest ||
		out["error"] != "flok 0.7.1 on old cannot take files: update flok there" {
		t.Fatalf("old file: %d %v", code, out)
	}
	app.send(t, wire.Frame{Type: wire.TypeAnswer, ID: "q1", Instance: "old", Answer: &answer.Answer{Pane: "%1", Text: strings.Repeat("x", answer.MaxText+1)}})
	if f := app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeAck && f.ID == "q1" }); !strings.Contains(f.Error, "takes at most 200 characters") {
		t.Fatalf("ws ack: %+v", f)
	}
}
