// Package relay is flok-relay: the small service between flok instances and the phone. Each
// instance dials in over a WebSocket (/link, bearer instance token) and streams its snapshot,
// events and captured screens; the app connects the same way (/api/ws, bearer device token) or
// calls the HTTP API, sees every instance in one place, answers prompts and gets pushes through
// APNs. The relay keeps the last snapshot per instance for an instant first paint and never
// originates a command: everything the app may do is answer, seen, subscribe.
package relay

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/w4jnl/flok/internal/answer"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/relay/apns"
	"github.com/w4jnl/flok/internal/snapshot"
)

// Pusher sends one alert to one device; apns.Client is the real one.
type Pusher interface {
	Send(ctx context.Context, device string, n apns.Notification) error
}

// Config of the relay.
type Config struct {
	InstanceTokens []string // what instances dial in with ([link] token)
	DeviceTokens   []string // what the app (and curl) call the API with
	DataDir        string   // devices.json lives here; "" = in memory
	Push           Pusher   // nil = no pushes
	Logf           func(string, ...any)
	// AnswerWait bounds how long an answer waits for the instance's ack; 0 = 8 s.
	AnswerWait time.Duration
	Version    string
}

// Server is one relay.
type Server struct {
	cfg       Config
	mu        sync.Mutex
	instances map[string]*instance
	apps      map[*app]struct{}
	devices   *deviceStore
	started   time.Time
	closed    bool
}

type instance struct {
	name    string
	hello   wire.Hello
	conn    *websocket.Conn // the current link, while online
	gen     uint64
	online  bool
	since   time.Time
	snap    *snapshot.Snapshot
	subs    map[string]int // pane → how many apps look at it
	out     chan wire.Frame
	pending map[string]chan string // answer / seen id → outcome ("" = ok)
	nextID  uint64
}

type app struct {
	out  chan wire.Frame
	subs map[[2]string]bool // {instance, pane}
}

// New builds a relay.
func New(cfg Config) (*Server, error) {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.AnswerWait <= 0 {
		cfg.AnswerWait = 8 * time.Second
	}
	ds, err := loadDevices(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, instances: map[string]*instance{}, apps: map[*app]struct{}{}, devices: ds, started: time.Now()}, nil
}

// Handler is the HTTP surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /link", s.handleLink)
	mux.HandleFunc("GET /api/ws", s.withDevice(s.handleAppWS))
	mux.HandleFunc("GET /api/instances", s.withDevice(s.handleInstances))
	mux.HandleFunc("POST /api/answer", s.withDevice(s.handleAnswer))
	mux.HandleFunc("POST /api/seen", s.withDevice(s.handleSeen))
	mux.HandleFunc("GET /api/devices", s.withDevice(s.handleDevicesList))
	mux.HandleFunc("POST /api/devices", s.withDevice(s.handleDeviceAdd))
	mux.HandleFunc("DELETE /api/devices/{token}", s.withDevice(s.handleDeviceRemove))
	return mux
}

// Close drops every connection.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func tokenIn(tok string, list []string) bool {
	if tok == "" {
		return false
	}
	ok := false
	for _, t := range list {
		if t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(tok)) == 1 {
			ok = true
		}
	}
	return ok
}

func (s *Server) withDevice(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tokenIn(bearer(r), s.cfg.DeviceTokens) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "a device token is needed (Authorization: Bearer …)"})
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	n, online := len(s.instances), 0
	for _, in := range s.instances {
		if in.online {
			online++
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "flok-relay", "version": s.cfg.Version, "instances": n, "online": online,
		"push": s.cfg.Push != nil, "uptime_s": int(time.Since(s.started).Seconds())})
}

// --- instances -------------------------------------------------------------------------------

func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	if !tokenIn(bearer(r), s.cfg.InstanceTokens) {
		http.Error(w, "an instance token is needed", http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(wire.MaxFrame)
	ctx := r.Context()
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	var hello wire.Frame
	_, data, err := conn.Read(hctx)
	cancel()
	if err != nil || json.Unmarshal(data, &hello) != nil || hello.Type != wire.TypeHello || hello.Hello == nil || hello.Hello.Name == "" {
		conn.Close(websocket.StatusPolicyViolation, "a hello frame with a name comes first")
		return
	}
	if hello.Hello.Proto != wire.Proto {
		conn.Close(websocket.StatusPolicyViolation, fmt.Sprintf("protocol %d, this relay speaks %d", hello.Hello.Proto, wire.Proto))
		return
	}
	in, gen, replaced := s.register(*hello.Hello, conn)
	if replaced != nil { // the same name again: the earlier link is stale (a drop the relay had not noticed yet)
		replaced.Close(websocket.StatusGoingAway, "replaced by a newer link from "+in.name)
	}
	s.cfg.Logf("instance %s online (flok %s on %s, %s)", in.name, hello.Hello.Version, hello.Hello.Hostname, r.RemoteAddr)
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	go s.writer(wctx, conn, in.out, gen, in)
	defer func() {
		conn.CloseNow()
		if s.offline(in, gen) {
			s.cfg.Logf("instance %s offline", in.name)
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var f wire.Frame
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		switch f.Type {
		case wire.TypeSnap:
			if f.Snap != nil {
				s.setSnap(in, gen, f.Snap)
			}
		case wire.TypeEvent:
			if f.Event != nil {
				s.event(in, gen, *f.Event)
			}
		case wire.TypeScreen:
			if f.Screen != nil {
				s.screen(in, gen, *f.Screen)
			}
		case wire.TypeAck:
			s.ack(in, f.ID, f.Error)
		}
	}
}

// writer drains an instance's or an app's outgoing queue onto its connection; a queue that
// stays full (a stalled consumer) ends the connection.
func (s *Server) writer(ctx context.Context, conn *websocket.Conn, out <-chan wire.Frame, gen uint64, in *instance) {
	for {
		select {
		case f := <-out:
			data, err := json.Marshal(f)
			if err != nil {
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err = conn.Write(wctx, websocket.MessageText, data)
			cancel()
			if err != nil {
				conn.CloseNow()
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// register makes an instance current under its name; a link already up under that name is
// replaced (the same instance reconnecting after a drop the relay has not noticed yet) and
// returned for closing.
func (s *Server) register(h wire.Hello, conn *websocket.Conn) (*instance, uint64, *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in := s.instances[h.Name]
	if in == nil {
		in = &instance{name: h.Name, subs: map[string]int{}}
		s.instances[h.Name] = in
	}
	var replaced *websocket.Conn
	if in.online {
		replaced = in.conn
	}
	in.gen++
	in.conn = conn
	in.hello, in.online, in.since = h, true, time.Now()
	in.out = make(chan wire.Frame, 256)
	for id, ch := range in.pending { // whoever waited on the previous connection
		ch <- "the instance reconnected"
		delete(in.pending, id)
	}
	in.pending = map[string]chan string{}
	for pane, n := range in.subs { // the phone is still looking: subscribe again on the new link
		if n > 0 {
			in.out <- wire.Frame{Type: wire.TypeSubscribe, Pane: pane, On: true}
		}
	}
	s.fanout(wire.Frame{Type: wire.TypeInstance, Instance: in.name, Inst: s.view(in)})
	return in, in.gen, replaced
}

// offline marks an instance gone when the connection that ends is still its current one.
func (s *Server) offline(in *instance, gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.gen != gen {
		return false
	}
	in.online, in.since, in.conn = false, time.Now(), nil
	for id, ch := range in.pending {
		ch <- "the instance went offline"
		delete(in.pending, id)
	}
	s.fanout(wire.Frame{Type: wire.TypeInstance, Instance: in.name, Inst: s.view(in)})
	return true
}

func (s *Server) view(in *instance) *wire.Instance {
	return &wire.Instance{Name: in.name, Online: in.online, Since: in.since, Version: in.hello.Version, Hostname: in.hello.Hostname, Snap: in.snap}
}

func (s *Server) setSnap(in *instance, gen uint64, snap *snapshot.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.gen != gen {
		return
	}
	in.snap = snap
	s.fanout(wire.Frame{Type: wire.TypeInstance, Instance: in.name, Inst: s.view(in)})
}

func (s *Server) screen(in *instance, gen uint64, sc wire.Screen) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.gen != gen {
		return
	}
	key := [2]string{in.name, sc.Pane}
	for a := range s.apps {
		if a.subs[key] {
			a.send(wire.Frame{Type: wire.TypeScreen, Instance: in.name, Screen: &sc})
		}
	}
}

func (s *Server) ack(in *instance, id, errText string) {
	s.mu.Lock()
	ch := in.pending[id]
	delete(in.pending, id)
	s.mu.Unlock()
	if ch != nil {
		ch <- errText
	}
}

// fanout sends a frame to every app; the caller holds s.mu.
func (s *Server) fanout(f wire.Frame) {
	for a := range s.apps {
		a.send(f)
	}
}

func (a *app) send(f wire.Frame) {
	select {
	case a.out <- f:
	default: // a stalled app: its writer ends the connection when it cannot keep up
	}
}

// event turns an instance's transition into pushes and tells the apps.
func (s *Server) event(in *instance, gen uint64, ev wire.Event) {
	s.mu.Lock()
	if in.gen != gen { // a replaced link's last words
		s.mu.Unlock()
		return
	}
	s.fanout(wire.Frame{Type: wire.TypeEvent, Instance: in.name, Event: &ev})
	badge := 0
	for _, i := range s.instances {
		if i.snap != nil {
			badge += i.snap.Unseen
		}
	}
	devices := s.devices.list()
	s.mu.Unlock()
	if s.cfg.Push == nil || len(devices) == 0 {
		return
	}
	n := Alert(in.name, ev, badge)
	for _, d := range devices {
		go func(d device) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			err := s.cfg.Push.Send(ctx, d.Token, n)
			switch {
			case errors.Is(err, apns.ErrUnregistered):
				s.cfg.Logf("push: device %s (%s) is gone, forgotten", short(d.Token), d.Name)
				s.devices.remove(d.Token)
			case err != nil:
				s.cfg.Logf("push: %s (%s): %v", short(d.Token), d.Name, err)
			default:
				s.cfg.Logf("push: %s · %s → %s (%s)", in.name, ev.Agent, d.Name, ev.Kind)
			}
		}(d)
	}
}

// Notification categories the app registers actions for.
const (
	CategoryPermission = "FLOK_PERMISSION" // a permission prompt: Approve / Deny act from the lock screen
	CategoryQuestion   = "FLOK_QUESTION"   // a question or another kind of wait: Open
	CategoryDone       = "FLOK_DONE"       // finished or failed: Open
)

// Alert is the push for an event: the same words as [notify], plus what the app needs to act
// (instance, pane, kind, reason) and a category for its actions.
func Alert(instance string, ev wire.Event, badge int) apns.Notification {
	agent := ev.Agent
	if ev.Host != "" {
		agent = ev.Host + "/" + ev.Agent
	}
	n := apns.Notification{Title: instance + " · " + agent, ThreadID: instance, CollapseID: instance + ":" + ev.Pane, Sound: "default", Badge: &badge,
		Data: map[string]any{"instance": instance, "pane": ev.Pane, "kind": ev.Kind, "reason": ev.Reason, "agent": ev.Agent, "host": ev.Host}}
	switch ev.Kind {
	case "blocked":
		n.TimeSensitive = true
		reason := strings.TrimPrefix(ev.Reason, "permission:")
		switch {
		case strings.HasPrefix(ev.Reason, "permission"):
			n.Body, n.Category = "needs you: perm:"+reason, CategoryPermission
		case ev.Reason != "":
			n.Body, n.Category = "needs you: "+ev.Reason, CategoryQuestion
		default:
			n.Body, n.Category = "needs you", CategoryQuestion
		}
	case "error":
		n.Body, n.Category = "failed", CategoryDone
	default:
		n.Body, n.Category = "finished", CategoryDone
	}
	return n
}

// --- the app -----------------------------------------------------------------------------------

func (s *Server) snapshotFrame() wire.Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := wire.Frame{Type: wire.TypeInstances, Instances: []wire.Instance{}}
	for _, in := range s.instances {
		f.Instances = append(f.Instances, *s.view(in))
	}
	sortInstances(f.Instances)
	return f
}

func sortInstances(list []wire.Instance) {
	for i := 1; i < len(list); i++ { // a handful: insertion sort by name
		for j := i; j > 0 && list[j].Name < list[j-1].Name; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

func (s *Server) handleInstances(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.snapshotFrame())
}

// request forwards an answer or a seen mark to an instance and waits for its outcome.
func (s *Server) request(instance string, f wire.Frame) error {
	s.mu.Lock()
	in := s.instances[instance]
	if in == nil || !in.online {
		s.mu.Unlock()
		if in == nil {
			return errors.New("no instance " + instance)
		}
		return errors.New(instance + " is offline")
	}
	in.nextID++
	id := fmt.Sprintf("%d-%d", in.gen, in.nextID)
	ch := make(chan string, 1)
	in.pending[id] = ch
	f.ID = id
	select {
	case in.out <- f:
	default:
		delete(in.pending, id)
		s.mu.Unlock()
		return errors.New(instance + " is not keeping up")
	}
	s.mu.Unlock()
	select {
	case out := <-ch:
		if out != "" {
			return errors.New(out)
		}
		return nil
	case <-time.After(s.cfg.AnswerWait):
		s.mu.Lock()
		delete(in.pending, id)
		s.mu.Unlock()
		return errors.New(instance + " did not answer in time")
	}
}

type answerReq struct {
	Instance string   `json:"instance"`
	Pane     string   `json:"pane"`
	Text     string   `json:"text,omitempty"`
	Keys     []string `json:"keys,omitempty"`
}

func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var req answerReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.Instance == "" || req.Pane == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "instance, pane and text or keys are needed"})
		return
	}
	a, err := answer.Normalize(answer.Answer{Pane: req.Pane, Text: req.Text, Keys: req.Keys})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := s.request(req.Instance, wire.Frame{Type: wire.TypeAnswer, Answer: &a}); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	s.cfg.Logf("answer: %s %s %s", req.Instance, req.Pane, answer.Describe(a))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSeen(w http.ResponseWriter, r *http.Request) {
	var req answerReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.Instance == "" || req.Pane == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "instance and pane are needed"})
		return
	}
	if err := s.request(req.Instance, wire.Frame{Type: wire.TypeSeen, Pane: req.Pane}); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAppWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(wire.MaxAppFrame)
	a := &app{out: make(chan wire.Frame, 256), subs: map[[2]string]bool{}}
	s.mu.Lock()
	s.apps[a] = struct{}{}
	s.mu.Unlock()
	ctx := r.Context()
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	go s.writer(wctx, conn, a.out, 0, nil)
	defer func() {
		conn.CloseNow()
		s.mu.Lock()
		delete(s.apps, a)
		for key := range a.subs {
			s.unsubscribeLocked(key[0], key[1])
		}
		s.mu.Unlock()
	}()
	a.send(s.snapshotFrame())
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var f wire.Frame
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		switch f.Type {
		case wire.TypeSubscribe:
			if f.Instance == "" || f.Pane == "" {
				continue
			}
			s.mu.Lock()
			key := [2]string{f.Instance, f.Pane}
			switch {
			case f.On && !a.subs[key]:
				a.subs[key] = true
				s.subscribeLocked(f.Instance, f.Pane)
			case !f.On && a.subs[key]:
				delete(a.subs, key)
				s.unsubscribeLocked(f.Instance, f.Pane)
			}
			s.mu.Unlock()
		case wire.TypeAnswer, wire.TypeSeen:
			id, inst := f.ID, f.Instance
			fwd := wire.Frame{Type: f.Type, Pane: f.Pane}
			if f.Type == wire.TypeAnswer {
				if f.Answer == nil {
					a.send(wire.Frame{Type: wire.TypeAck, ID: id, Error: "no answer in the frame"})
					continue
				}
				n, err := answer.Normalize(*f.Answer)
				if err != nil {
					a.send(wire.Frame{Type: wire.TypeAck, ID: id, Error: err.Error()})
					continue
				}
				fwd.Answer = &n
				fwd.Pane = ""
			}
			go func() {
				ack := wire.Frame{Type: wire.TypeAck, ID: id, Instance: inst}
				if err := s.request(inst, fwd); err != nil {
					ack.Error = err.Error()
				}
				a.send(ack)
			}()
		}
	}
}

// subscribeLocked counts one more app looking at a pane; the first one asks the instance for
// the screen. The caller holds s.mu.
func (s *Server) subscribeLocked(name, pane string) {
	in := s.instances[name]
	if in == nil {
		in = &instance{name: name, subs: map[string]int{}, pending: map[string]chan string{}} // not seen yet: remembered for when it comes
		s.instances[name] = in
	}
	in.subs[pane]++
	if in.subs[pane] == 1 && in.online {
		select {
		case in.out <- wire.Frame{Type: wire.TypeSubscribe, Pane: pane, On: true}:
		default:
		}
	}
}

func (s *Server) unsubscribeLocked(name, pane string) {
	in := s.instances[name]
	if in == nil || in.subs[pane] == 0 {
		return
	}
	in.subs[pane]--
	if in.subs[pane] == 0 {
		delete(in.subs, pane)
		if in.online {
			select {
			case in.out <- wire.Frame{Type: wire.TypeSubscribe, Pane: pane, On: false}:
			default:
			}
		}
	}
}

// --- devices -----------------------------------------------------------------------------------

type deviceReq struct {
	Token   string `json:"token"`
	Name    string `json:"name,omitempty"`
	Sandbox bool   `json:"sandbox,omitempty"`
}

func (s *Server) handleDevicesList(w http.ResponseWriter, r *http.Request) {
	list := s.devices.list()
	out := make([]map[string]any, 0, len(list))
	for _, d := range list {
		out = append(out, map[string]any{"token": short(d.Token), "name": d.Name, "sandbox": d.Sandbox, "added": d.Added})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out, "push": s.cfg.Push != nil})
}

func (s *Server) handleDeviceAdd(w http.ResponseWriter, r *http.Request) {
	var req deviceReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || len(req.Token) < 8 || len(req.Token) > 512 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "a device token is needed"})
		return
	}
	if err := s.devices.add(device{Token: req.Token, Name: req.Name, Sandbox: req.Sandbox, Added: time.Now()}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.cfg.Logf("device %s (%s) registered", short(req.Token), req.Name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "push": s.cfg.Push != nil})
}

func (s *Server) handleDeviceRemove(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	if !s.devices.remove(tok) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such device"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func short(tok string) string {
	if len(tok) > 8 {
		return tok[:8] + "…"
	}
	return tok
}

// NewToken is a fresh random token for the config (flok-relay token).
func NewToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
