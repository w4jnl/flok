// Package relay is flok-relay: the small service between flok instances and the phone. Each
// instance dials in over a WebSocket (/link, bearer instance token; or /link/tls, the same frames
// inside TLS to the relay's own key, for a network whose proxy reads HTTPS) and streams its snapshot,
// events and captured screens; the app connects the same way (/api/ws, bearer device token) or
// calls the HTTP API, sees every instance in one place, answers prompts and gets pushes through
// APNs. The relay keeps the last snapshot per instance for an instant first paint and never
// originates a command: everything the app may do is answer, seen, subscribe.
package relay

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/answer"
	"github.com/w4jnl/flok/internal/link/tunnel"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/relay/apns"
	"github.com/w4jnl/flok/internal/snapshot"
)

// Pusher sends one alert to one device; apns.Client is the real one.
type Pusher interface {
	Send(ctx context.Context, d apns.Device, n apns.Notification) error
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
	// SyncDelay gathers the agents that stopped waiting into one silent sync push; 0 = 3 s.
	SyncDelay time.Duration
	// LiveDelay gathers snapshot changes into one round of Live Activity pushes; 0 = 1.5 s.
	LiveDelay time.Duration
	// QuietEnd ends the overview activity after nothing works, waits or is unseen; 0 = 2 min.
	QuietEnd time.Duration
	// AgentLinger keeps a followed agent's finished activity on the Lock Screen; 0 = 10 min.
	AgentLinger time.Duration
	// LinkKey is the relay's own key for the encrypted link (/link/tls, tunnel.LoadOrCreate);
	// nil = no encrypted link. TunnelOnly refuses the plain /link.
	LinkKey    *tls.Certificate
	TunnelOnly bool
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
	syncDue   *time.Timer // a sync push is on its way

	// Live Activities (live.go)
	liveDue        *time.Timer             // a round is on its way
	liveWasActive  bool                    // something worked or waited in the last round
	liveQuietSince time.Time               // since when nothing works, waits or is unseen
	liveSent       map[string]string       // activity token → the content last sent
	liveWaiting    map[string]int          // activity token → its waiting count last sent (a rise is urgent)
	liveStarted    map[string]time.Time    // device token → a start push the phone has not answered yet
	liveAlert      map[string]pendingAlert // device token → a finished agent's push for its overview's next update
}

type instance struct {
	name    string
	hello   wire.Hello
	conn    frameConn // the current link, while online
	gen     uint64
	online  bool
	bye     bool // the current link said bye: switched off there (flok link off)
	off     bool // offline because it was switched off: out of the badge, its alerts cleared
	since   time.Time
	snap    *snapshot.Snapshot
	subs    map[string]int         // pane → how many apps look at it
	screens map[string]wire.Screen // pane → its last screen, while someone looks at it
	waiting map[string]bool        // pane refs that wait on the user (unseen or blocked), as of the last snapshot
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
	if cfg.SyncDelay <= 0 {
		cfg.SyncDelay = 3 * time.Second
	}
	if cfg.LiveDelay <= 0 {
		cfg.LiveDelay = 1500 * time.Millisecond
	}
	if cfg.QuietEnd <= 0 {
		cfg.QuietEnd = 2 * time.Minute
	}
	if cfg.AgentLinger <= 0 {
		cfg.AgentLinger = 10 * time.Minute
	}
	ds, err := loadDevices(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, instances: map[string]*instance{}, apps: map[*app]struct{}{}, devices: ds, started: time.Now(),
		liveSent: map[string]string{}, liveWaiting: map[string]int{}, liveStarted: map[string]time.Time{}, liveAlert: map[string]pendingAlert{}}, nil
}

// Handler is the HTTP surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /link", s.handleLink)
	mux.HandleFunc("GET /link/tls", s.handleLinkTunnel)
	mux.HandleFunc("GET /api/ws", s.withDevice(s.handleAppWS))
	mux.HandleFunc("GET /api/instances", s.withDevice(s.handleInstances))
	mux.HandleFunc("POST /api/answer", s.withDevice(s.handleAnswer))
	mux.HandleFunc("POST /api/seen", s.withDevice(s.handleSeen))
	mux.HandleFunc("GET /api/devices", s.withDevice(s.handleDevicesList))
	mux.HandleFunc("POST /api/devices", s.withDevice(s.handleDeviceAdd))
	mux.HandleFunc("DELETE /api/devices/{token}", s.withDevice(s.handleDeviceRemove))
	mux.HandleFunc("PUT /api/devices/{token}/prefs", s.withDevice(s.handleDevicePrefs))
	mux.HandleFunc("PUT /api/devices/{token}/activity", s.withDevice(s.handleDeviceActivity))
	mux.HandleFunc("GET /api/screen", s.withDevice(s.handleScreen))
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
	if s.cfg.TunnelOnly {
		http.Error(w, "this relay takes encrypted links only: set [link] pin (flok-relay pin prints it)", http.StatusForbidden)
		return
	}
	if !tokenIn(bearer(r), s.cfg.InstanceTokens) {
		http.Error(w, "an instance token is needed", http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(wire.MaxFrame)
	s.serveLink(r.Context(), wsConn{conn}, r.RemoteAddr, "")
}

// handleLinkTunnel is the encrypted link: the WebSocket carries TLS to the relay's own key, and
// everything, the token included, travels inside it. The upgrade itself needs no token, so a
// proxy that reads the outer HTTPS sees none.
func (s *Server) handleLinkTunnel(w http.ResponseWriter, r *http.Request) {
	if s.cfg.LinkKey == nil {
		http.Error(w, "this relay has no encrypted link (no data dir for its key)", http.StatusNotFound)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	ctx := r.Context()
	t, err := tunnel.Server(ctx, conn, *s.cfg.LinkKey, wire.MaxFrame)
	if err != nil {
		conn.CloseNow()
		s.cfg.Logf("link: encrypted handshake from %s failed: %v", r.RemoteAddr, err)
		return
	}
	tc := tunnelConn{conn, t}
	actx, cancel := context.WithTimeout(ctx, 10*time.Second)
	data, err := t.Read(actx)
	cancel()
	var auth wire.Frame
	if err != nil || json.Unmarshal(data, &auth) != nil || auth.Type != wire.TypeAuth || !tokenIn(auth.Token, s.cfg.InstanceTokens) {
		tc.Close(websocket.StatusPolicyViolation, "an instance token is needed")
		return
	}
	ok, _ := json.Marshal(wire.Frame{Type: wire.TypeAuth})
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = t.Write(wctx, ok)
	cancel()
	if err != nil {
		conn.CloseNow()
		return
	}
	s.serveLink(ctx, tc, r.RemoteAddr, ", encrypted")
}

// serveLink runs an instance's link once it is authenticated: the hello, then its frames until
// the connection ends.
func (s *Server) serveLink(ctx context.Context, conn frameConn, remote, how string) {
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	var hello wire.Frame
	data, err := conn.Read(hctx)
	cancel()
	if err != nil || json.Unmarshal(data, &hello) != nil || hello.Type != wire.TypeHello || hello.Hello == nil || hello.Hello.Name == "" {
		conn.Close(websocket.StatusPolicyViolation, "a hello frame with a name comes first")
		return
	}
	if hello.Hello.Proto != wire.Proto {
		conn.Close(websocket.StatusPolicyViolation, fmt.Sprintf("protocol %d, this relay speaks %d", hello.Hello.Proto, wire.Proto))
		return
	}
	in, gen, out, replaced := s.register(*hello.Hello, conn)
	if replaced != nil { // the same name again: the earlier link is stale (a drop the relay had not noticed yet)
		replaced.Close(websocket.StatusGoingAway, "replaced by a newer link from "+in.name)
	}
	s.cfg.Logf("instance %s online (flok %s on %s, %s%s)", in.name, hello.Hello.Version, hello.Hello.Hostname, remote, how)
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	go s.writer(wctx, conn, out, gen, in)
	defer func() {
		conn.CloseNow()
		switch gone, off := s.offline(in, gen); {
		case off:
			s.cfg.Logf("instance %s switched off (flok link off there)", in.name)
		case gone:
			s.cfg.Logf("instance %s offline", in.name)
		}
	}()
	for {
		data, err := conn.Read(ctx)
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
		case wire.TypeBye:
			if f.Reason == wire.ByeOff {
				s.mu.Lock()
				if in.gen == gen {
					in.bye = true
				}
				s.mu.Unlock()
			}
		}
	}
}

// writer drains an instance's or an app's outgoing queue onto its connection; a queue that
// stays full (a stalled consumer) ends the connection.
func (s *Server) writer(ctx context.Context, conn frameConn, out <-chan wire.Frame, gen uint64, in *instance) {
	for {
		select {
		case f := <-out:
			data, err := json.Marshal(f)
			if err != nil {
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err = conn.Write(wctx, data)
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
// returned for closing. The link's frame channel is returned too: a later link swaps in.out.
func (s *Server) register(h wire.Hello, conn frameConn) (*instance, uint64, chan wire.Frame, frameConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in := s.instances[h.Name]
	if in == nil {
		in = &instance{name: h.Name, subs: map[string]int{}}
		s.instances[h.Name] = in
	}
	var replaced frameConn
	if in.online {
		replaced = in.conn
	}
	in.gen++
	in.conn = conn
	in.hello, in.online, in.since = h, true, time.Now()
	in.bye, in.off = false, false
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
	s.liveSoonLocked() // its agents join the overview
	s.fanout(wire.Frame{Type: wire.TypeInstance, Instance: in.name, Inst: s.view(in)})
	return in, in.gen, in.out, replaced
}

// offline marks an instance gone when the connection that ends is still its current one; off
// when it said bye first. Switched off, its agents stop counting on the phone: they leave the
// badge, and a sync push clears the notifications of those that waited.
func (s *Server) offline(in *instance, gen uint64) (gone, off bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.gen != gen {
		return false, false
	}
	in.online, in.since, in.conn, in.off = false, time.Now(), nil, in.bye
	for id, ch := range in.pending {
		ch <- "the instance went offline"
		delete(in.pending, id)
	}
	if in.off && len(in.waiting) > 0 {
		in.waiting = nil
		s.syncSoonLocked()
	}
	s.liveSoonLocked() // its agents leave the overview
	s.fanout(wire.Frame{Type: wire.TypeInstance, Instance: in.name, Inst: s.view(in)})
	return true, in.off
}

func (s *Server) view(in *instance) *wire.Instance {
	return &wire.Instance{Name: in.name, Online: in.online, Off: in.off, Since: in.since, Version: in.hello.Version, Hostname: in.hello.Hostname,
		Snap: in.snap}
}

// gone says why an instance cannot take a request; the caller holds s.mu.
func gone(in *instance) string {
	if in.off {
		return in.name + " is switched off (flok link on there)"
	}
	return in.name + " is offline"
}

// badgeLocked is the app's badge: what waits unseen over every instance not switched off. The
// caller holds s.mu.
func (s *Server) badgeLocked() int {
	n := 0
	for _, in := range s.instances {
		if in.snap != nil && !in.off {
			n += in.snap.Unseen
		}
	}
	return n
}

func (s *Server) setSnap(in *instance, gen uint64, snap *snapshot.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.gen != gen {
		return
	}
	in.snap = snap
	waiting := waitingIn(snap)
	for pane := range in.waiting {
		if !waiting[pane] { // seen or answered, here or on the phone: the phones clear its alert
			s.syncSoonLocked()
			break
		}
	}
	in.waiting = waiting
	s.liveSoonLocked()
	s.fanout(wire.Frame{Type: wire.TypeInstance, Instance: in.name, Inst: s.view(in)})
}

// waitingIn is the set of agents in a snapshot that wait on the user: unseen, or blocked.
func waitingIn(snap *snapshot.Snapshot) map[string]bool {
	out := map[string]bool{}
	if snap == nil {
		return out
	}
	for _, a := range snap.Agents {
		if a.Unseen > 0 || a.State == agent.Blocked {
			ref := a.PaneID
			if a.Host != "" {
				ref = a.Host + ":" + a.PaneID
			}
			out[ref] = true
		}
	}
	return out
}

// syncSoonLocked schedules one silent sync push to every phone (a run of changes is one push;
// iOS throttles background pushes anyway, and each sync carries the whole state). The caller
// holds s.mu.
func (s *Server) syncSoonLocked() {
	if s.cfg.Push == nil || s.syncDue != nil {
		return
	}
	s.syncDue = time.AfterFunc(s.cfg.SyncDelay, s.sync)
}

// sync tells every phone which agents still wait on the user and the badge: the app removes
// the notifications of the others and sets the badge, without showing anything.
func (s *Server) sync() {
	s.mu.Lock()
	s.syncDue = nil
	badge, waiting := s.badgeLocked(), []string{}
	for _, in := range s.instances {
		for pane := range in.waiting {
			waiting = append(waiting, in.name+":"+pane)
		}
	}
	devices := s.devices.list()
	s.mu.Unlock()
	sort.Strings(waiting)
	if len(waiting) > 200 { // the payload stays far below APNs' 4 KB; the app keeps what it cannot match
		waiting = waiting[:200]
	}
	n := apns.Notification{Background: true, Data: map[string]any{"flok_sync": 1, "waiting": waiting, "badge": badge}}
	for _, d := range devices {
		go func(d device) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			switch err := s.cfg.Push.Send(ctx, apns.Device{Token: d.Token, Sandbox: d.Sandbox}, n); {
			case errors.Is(err, apns.ErrUnregistered):
				s.cfg.Logf("push: %s is gone, forgotten", d.label())
				s.devices.remove(d.Token)
			case err != nil:
				s.cfg.Logf("push: sync → %s: %v", d.label(), err)
			default:
				s.cfg.Logf("push: sync → %s (%d waiting, badge %d)", d.label(), len(waiting), badge)
			}
		}(d)
	}
}

func (s *Server) screen(in *instance, gen uint64, sc wire.Screen) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.gen != gen {
		return
	}
	if in.subs[sc.Pane] > 0 { // kept for the next app that looks: an unchanged screen is not resent
		if in.screens == nil {
			in.screens = map[string]wire.Screen{}
		}
		in.screens[sc.Pane] = sc
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
	badge := s.badgeLocked()
	devices := s.devices.list()
	s.mu.Unlock()
	session := sessionOf(in.snap, ev.Pane)
	if s.cfg.Push == nil || len(devices) == 0 {
		return
	}
	n := Alert(in.name, ev, badge, session)
	kind, now := PushKind(ev), time.Now()
	for _, d := range devices {
		if why := d.Prefs.filtered(in.name, ev, now); why != "" {
			s.cfg.Logf("push: %s · %s → %s: not sent, %s", in.name, ev.Agent, d.label(), why)
			continue
		}
		n := n // the phone's own sound and interruption level
		n.Sound, n.TimeSensitive = d.Prefs.Sound(kind), d.Prefs.TimeSensitive(kind)
		what := fmt.Sprintf("%s · %s → %s", in.name, ev.Agent, d.label())
		if d.Prefs.finishedInIsland(kind, d.hasOverview()) {
			// the overview's next update carries it as an alert (live); a badge-only push, which
			// shows nothing, keeps the badge where the notification would have put it
			s.mu.Lock()
			s.liveAlert[d.Token] = pendingAlert{n: n, what: what, kind: ev.Kind}
			s.liveSoonLocked()
			s.mu.Unlock()
			go s.sendAlert(d, apns.Notification{Badge: n.Badge}, what+": badge", "")
			continue
		}
		go s.sendAlert(d, n, what, ev.Kind)
	}
}

// pendingAlert is a finished agent's push waiting for the overview's next update to carry it.
type pendingAlert struct {
	n    apns.Notification
	what string
	kind string
}

// sendAlert sends one notification and logs how it went; a device Apple no longer knows is
// forgotten.
func (s *Server) sendAlert(d device, n apns.Notification, what, kind string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := s.cfg.Push.Send(ctx, apns.Device{Token: d.Token, Sandbox: d.Sandbox}, n)
	switch {
	case errors.Is(err, apns.ErrUnregistered):
		s.cfg.Logf("push: %s is gone, forgotten", d.label())
		s.devices.remove(d.Token)
	case err != nil:
		s.cfg.Logf("push: %s: %v", what, err)
	case kind != "":
		s.cfg.Logf("push: %s (%s)", what, kind)
	}
}

// sessionOf is the session name of the agent in a pane ("beta:%3" for a host's), from the
// instance's last snapshot; "" when it is not there.
func sessionOf(snap *snapshot.Snapshot, pane string) string {
	if snap == nil {
		return ""
	}
	for _, a := range snap.Agents {
		ref := a.PaneID
		if a.Host != "" {
			ref = a.Host + ":" + a.PaneID
		}
		if ref == pane {
			return a.SessionName
		}
	}
	return ""
}

// Notification categories the app registers actions for.
const (
	CategoryPermission = "FLOK_PERMISSION" // a permission prompt: Approve / Deny act from the lock screen
	CategoryQuestion   = "FLOK_QUESTION"   // a question or another kind of wait: Open
	CategoryDone       = "FLOK_DONE"       // finished or failed: Open
)

// Alert is the push for an event, worded like the sidebar: the agent as the title, where it runs
// as the subtitle (instance, host, session), its state with the sidebar's glyph as the body. The
// data carries what the app and its extensions act on (instance, pane, kind, reason, push_kind,
// session); the category picks the actions. Sound and interruption level are the defaults
// here; event sets each phone's own (Prefs).
func Alert(instance string, ev wire.Event, badge int, session string) apns.Notification {
	where := instance
	if ev.Host != "" {
		where += " / " + ev.Host
	}
	if session != "" {
		where += " · " + session
	}
	title := ev.Agent
	if title == "" {
		title = ev.Pane
	}
	kind := PushKind(ev)
	var defaults *Prefs
	n := apns.Notification{Title: title, Subtitle: where, ThreadID: instance, CollapseID: instance + ":" + ev.Pane, Badge: &badge, Mutable: true,
		Sound: defaults.Sound(kind), TimeSensitive: defaults.TimeSensitive(kind),
		Data: map[string]any{"instance": instance, "pane": ev.Pane, "kind": ev.Kind, "reason": ev.Reason, "agent": ev.Agent, "host": ev.Host,
			"session": session, "push_kind": kind}}
	switch kind {
	case KindPermission:
		n.Body, n.Category = "● needs you: perm", CategoryPermission
		if tool := strings.TrimPrefix(strings.TrimPrefix(ev.Reason, "permission"), ":"); tool != "" {
			n.Body += ":" + tool
		}
	case KindQuestion:
		n.Body, n.Category = "● needs you", CategoryQuestion
		if ev.Reason != "" {
			n.Body += ": " + ev.Reason
		}
	case KindError:
		n.Body, n.Category = "✗ failed", CategoryDone
	default:
		n.Body, n.Category = "✓ done", CategoryDone
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
		why := "no instance " + instance
		if in != nil {
			why = gone(in)
		}
		s.mu.Unlock()
		return errors.New(why)
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
	go s.writer(wctx, wsConn{conn}, a.out, 0, nil)
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
				if sc, ok := s.instances[f.Instance].screens[f.Pane]; ok { // someone else looks already
					a.send(wire.Frame{Type: wire.TypeScreen, Instance: f.Instance, Screen: &sc})
				}
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
		delete(in.screens, pane)
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
	Prefs   *Prefs `json:"prefs,omitempty"`
}

func (s *Server) handleDevicesList(w http.ResponseWriter, r *http.Request) {
	list := s.devices.list()
	out := make([]map[string]any, 0, len(list))
	for _, d := range list {
		out = append(out, map[string]any{"token": short(d.Token), "name": d.Name, "sandbox": d.Sandbox, "added": d.Added, "prefs": d.Prefs})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out, "push": s.cfg.Push != nil})
}

func (s *Server) handleDeviceAdd(w http.ResponseWriter, r *http.Request) {
	var req deviceReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || len(req.Token) < 8 || len(req.Token) > 512 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "a device token is needed"})
		return
	}
	if !req.Prefs.valid() {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prefs: unknown kind or sound, or a mute without instance and agent"})
		return
	}
	req.Prefs.prune(time.Now())
	d := device{Token: req.Token, Name: req.Name, Sandbox: req.Sandbox, Added: time.Now(), Prefs: req.Prefs}
	if err := s.devices.add(d); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.cfg.Logf("device %s registered", d.label())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "push": s.cfg.Push != nil})
}

// handleDevicePrefs replaces a phone's notification settings (the app sends them whole).
func (s *Server) handleDevicePrefs(w http.ResponseWriter, r *http.Request) {
	var p Prefs
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&p); err != nil || !p.valid() {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prefs: not JSON, an unknown kind or sound, or a mute without instance and agent"})
		return
	}
	p.prune(time.Now())
	tok := r.PathValue("token")
	found, err := s.devices.setPrefs(tok, &p)
	switch {
	case !found:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such device"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.cfg.Logf("device %s: notification settings, %d kinds set, %d mutes", short(tok), len(p.Kinds), len(p.Mutes))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type activityReq struct {
	Start      string     `json:"start_token,omitempty"`
	Activities []Activity `json:"activities"`
}

// handleDeviceActivity takes a phone's Live Activity tokens (sent whole): its push-to-start
// token and the activities running, overview or agent, with their update tokens.
func (s *Server) handleDeviceActivity(w http.ResponseWriter, r *http.Request) {
	var req activityReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || len(req.Activities) > 20 || len(req.Start) > 512 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "activity: not JSON, or too many"})
		return
	}
	for _, a := range req.Activities {
		if a.ID == "" || a.Token == "" || len(a.Token) > 512 || (a.Kind != liveOverview && a.Kind != liveAgent) || (a.Kind == liveAgent && (a.Instance == "" || a.Pane == "")) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "activity: id, token and kind (overview, or agent with instance and pane) are needed"})
			return
		}
	}
	tok := r.PathValue("token")
	found, err := s.devices.setActivities(tok, req.Start, req.Activities)
	switch {
	case !found:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such device"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.mu.Lock()
	delete(s.liveStarted, tok) // the phone answered: what it runs is known now
	s.liveSoonLocked()         // a new activity gets the current state at once
	s.mu.Unlock()
	s.cfg.Logf("device %s: %d live activities, start token %v", short(tok), len(req.Activities), req.Start != "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ScreenWait bounds how long GET /api/screen waits for an instance to send the screen.
var ScreenWait = 4 * time.Second

// handleScreen is one look at a pane, for a caller without a live stream (the notification's
// expanded view): it subscribes like an app, answers with the first screen (or the one another
// app's subscription already holds) and lets go.
func (s *Server) handleScreen(w http.ResponseWriter, r *http.Request) {
	inst, pane := r.URL.Query().Get("instance"), r.URL.Query().Get("pane")
	if inst == "" || pane == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "instance and pane are needed"})
		return
	}
	key := [2]string{inst, pane}
	a := &app{out: make(chan wire.Frame, 16), subs: map[[2]string]bool{key: true}}
	s.mu.Lock()
	in := s.instances[inst]
	if in == nil || !in.online {
		why := inst + " is offline"
		if in != nil {
			why = gone(in)
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": why})
		return
	}
	s.apps[a] = struct{}{}
	s.subscribeLocked(inst, pane)
	cached, ok := in.screens[pane]
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.apps, a)
		s.unsubscribeLocked(inst, pane)
		s.mu.Unlock()
	}()
	if ok {
		writeJSON(w, http.StatusOK, map[string]any{"pane": pane, "text": cached.Text})
		return
	}
	timeout := time.NewTimer(ScreenWait)
	defer timeout.Stop()
	for {
		select {
		case f := <-a.out:
			if f.Type == wire.TypeScreen && f.Screen != nil && f.Instance == inst && f.Screen.Pane == pane {
				writeJSON(w, http.StatusOK, map[string]any{"pane": pane, "text": f.Screen.Text})
				return
			}
		case <-timeout.C:
			writeJSON(w, http.StatusGatewayTimeout, map[string]any{"error": "no screen from " + inst + " in time"})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleDeviceRemove(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	if !s.devices.remove(tok) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such device"})
		return
	}
	s.cfg.Logf("device %s removed", short(tok))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// label names a device in the log: its name, the start of its token and its APNs service, so
// two registrations of one phone (an Xcode and a TestFlight build) tell apart.
func (d device) label() string {
	name := d.Name
	if name == "" {
		name = "device"
	}
	return name + " " + short(d.Token) + " " + service(d.Sandbox)
}

// service names the APNs service a device's token belongs to, for the log.
func service(sandbox bool) string {
	if sandbox {
		return "sandbox"
	}
	return "production"
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
