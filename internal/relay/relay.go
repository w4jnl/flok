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
	"github.com/w4jnl/flok/internal/feed"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/answer"
	"github.com/w4jnl/flok/internal/link"
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
	InstanceTokens []string // static tokens instances dial in with ([link] token), into the relay's own tenant; optional once floks join by code
	AdminTokens    []string // like instance tokens, and a flok linking with one manages the tenants (flok tenant …)
	DeviceTokens   []string // static tokens the API takes besides the paired phones' own (curl, scripts); optional
	DataDir        string   // devices.json, phones.json, instances.json and tenants.json live here; "" = in memory
	Push           Pusher   // nil = no pushes
	Logf           func(string, ...any)
	// AnswerWait bounds how long an answer waits for the instance's ack; 0 = 8 s.
	AnswerWait time.Duration
	// PairTTL is how long a pairing code lives; 0 = 5 min.
	PairTTL time.Duration
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
	tenants   map[string]*tenant // by id: the live side of every tenant something belongs to
	apps      map[*app]struct{}
	devices   *deviceStore
	phones    *phoneStore
	tstore    *tenantStore
	istore    *instanceStore
	pending   map[string]*joining // enrollment id → a flok waiting for its join to be approved
	pairLimit *ipLimiter          // wrong pairing codes and join requests, per address
	codeLimit *ipLimiter          // wrong join codes, per approver
	started   time.Time
	closed    bool

	// Live Activities (live.go), by token, so one map serves every tenant
	liveSent    map[string]string       // activity token → the content last sent
	liveWaiting map[string]int          // activity token → its waiting count last sent (a rise is urgent)
	liveStarted map[string]time.Time    // device token → a start push the phone has not answered yet
	liveAlert   map[string]pendingAlert // device token → a finished agent's push for its overview's next update
}

// tenant is the live side of one tenant: its instances, their pairing codes, and the timers of
// what its phones get as a whole (the sync push, the overview activity).
type tenant struct {
	id        string
	nm        atomic.Pointer[string] // its name: renamed under s.mu, read anywhere (label)
	instances map[string]*instance   // by name
	codes     map[string]pairCode    // instance → its live pairing code
	syncDue   *time.Timer            // a sync push is on its way

	liveDue        *time.Timer // a round of activity pushes is on its way
	liveWasActive  bool        // something worked or waited in the last round
	liveQuietSince time.Time   // since when nothing works, waits or is unseen
}

// Name is the tenant's name as it is now.
func (t *tenant) Name() string { return *t.nm.Load() }

func (t *tenant) setName(name string) { t.nm.Store(&name) }

func (t *tenant) info() wire.TenantInfo {
	return wire.TenantInfo{ID: t.id, Name: t.Name(), Static: t.id == DefaultTenant}
}

type instance struct {
	tenant  *tenant      // set once, read without the lock
	rec     *instanceRec // its record when it joined by code; nil for a token from the relay's config
	name    string       // set once, read without the lock
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
	feeds   map[string]*feed.Feed  // pane → its feed so far (a Reset for the next app that looks), while someone looks at it
	waiting map[string]bool        // pane refs that wait on the user (unseen or blocked), as of the last snapshot
	out     chan wire.Frame
	pending map[string]chan string // answer / seen id → outcome ("" = ok)
	nextID  uint64
}

type app struct {
	tenant *tenant
	out    chan wire.Frame
	subs   map[[2]string]bool // {instance, pane}
	conn   frameConn          // its stream, for a close with a reason; nil for one look (GET /api/screen)
	phone  string             // the paired phone it signed in as; "" = a static device token
}

// label names an instance in the log: its name, with its tenant's when that is not the relay's
// own.
func (in *instance) label() string {
	if in.tenant == nil || in.tenant.id == DefaultTenant {
		return in.name
	}
	return in.tenant.Name() + "/" + in.name
}

// ErrNothingLinks is New's answer to a relay nothing could ever link in to.
var ErrNothingLinks = errors.New("nothing could link in: set instance_tokens or admin_tokens (flok-relay token prints one); floks then join with flok link join, phones pair with flok phone pair")

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
	if cfg.PairTTL <= 0 {
		cfg.PairTTL = 5 * time.Minute
	}
	ds, err := loadDevices(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	ps, err := loadPhones(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	ts, err := loadTenants(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	is, err := loadInstances(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	if len(cfg.InstanceTokens) == 0 && len(cfg.AdminTokens) == 0 && is.count() == 0 {
		return nil, ErrNothingLinks
	}
	s := &Server{cfg: cfg, tenants: map[string]*tenant{}, apps: map[*app]struct{}{}, devices: ds, phones: ps, tstore: ts, istore: is, started: time.Now(),
		pending: map[string]*joining{}, pairLimit: newIPLimiter(5, 30*time.Second, 15*time.Minute), codeLimit: newIPLimiter(5, 30*time.Second, 15*time.Minute),
		liveSent: map[string]string{}, liveWaiting: map[string]int{}, liveStarted: map[string]time.Time{}, liveAlert: map[string]pendingAlert{}}
	for _, t := range ts.list() { // every tenant is live from the start, so its lists and badge exist before a member links
		s.tenantLocked(t.ID)
	}
	return s, nil
}

// tenantLocked is a tenant's live side, made on first use for an id the store knows (the
// default always). The caller holds s.mu (or is New).
func (s *Server) tenantLocked(id string) *tenant {
	if t := s.tenants[id]; t != nil {
		return t
	}
	rec, ok := s.tstore.get(id)
	if !ok {
		return nil
	}
	t := &tenant{id: rec.ID, instances: map[string]*instance{}, codes: map[string]pairCode{}}
	t.setName(rec.Name)
	s.tenants[id] = t
	return t
}

// Handler is the HTTP surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /link", s.handleLink)
	mux.HandleFunc("GET /link/tls", s.handleLinkTunnel)
	mux.HandleFunc("POST /api/pair", s.handlePair) // the one API call without a token: it is how a phone gets one
	// a flok joining: no key yet, so these take none (the poll secret stands in); its approver
	// signs in as the app does
	mux.HandleFunc("POST /api/enroll", s.handleEnrollStart)
	mux.HandleFunc("GET /api/enroll/{id}", s.handleEnrollPoll)
	mux.HandleFunc("POST /api/enroll/{id}/accept", s.handleEnrollAccept)
	mux.HandleFunc("DELETE /api/enroll/{id}", s.handleEnrollDecline)
	mux.HandleFunc("GET /api/enroll/code/{code}", s.withDevice(s.handleEnrollLookup))
	mux.HandleFunc("POST /api/enroll/approve", s.withDevice(s.handleEnrollApprove))
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

// withDevice lets a request through with a static device token (the relay's own tenant) or a
// paired phone's own key (its tenant); who it is rides in the context (callerOf) so what it
// registers and sees is its tenant's.
func (s *Server) withDevice(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		var who apiCaller
		switch p, ok := s.phones.byToken(tok); {
		case tokenIn(tok, s.cfg.DeviceTokens):
			who = apiCaller{tenant: DefaultTenant}
		case ok:
			who = apiCaller{tenant: p.Tenant, phone: p.ID, name: p.Name}
		default:
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "a device token is needed (Authorization: Bearer …): pair this phone with flok phone pair"})
			return
		}
		s.mu.Lock()
		who.t = s.tenantLocked(who.tenant)
		s.mu.Unlock()
		if who.t == nil { // a phone of a tenant that is gone: its key goes with it
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "this phone's tenant is gone from the relay: pair this phone with flok phone pair"})
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, who)))
	}
}

// apiCaller is who an API request signed in as: a tenant always, a paired phone when it was one.
type apiCaller struct {
	tenant string
	t      *tenant
	phone  string // the paired phone's id; "" for a static device token
	name   string // its name
}

type callerKey struct{}

// callerOf is who a request signed in as (withDevice put it there).
func callerOf(r *http.Request) apiCaller {
	who, _ := r.Context().Value(callerKey{}).(apiCaller)
	return who
}

// phoneOf is the paired phone a request signed in as; "" for a static device token.
func phoneOf(r *http.Request) string { return callerOf(r).phone }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	n, online := 0, 0
	for _, t := range s.tenants {
		n += len(t.instances)
		for _, in := range t.instances {
			if in.online {
				online++
			}
		}
	}
	tenants := len(s.tenants)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "flok-relay", "version": s.cfg.Version, "instances": n, "online": online,
		"tenants": tenants, "push": s.cfg.Push != nil, "uptime_s": int(time.Since(s.started).Seconds())})
}

// --- instances -------------------------------------------------------------------------------

// linkIdentity is what a link's token says: the tenant it links into, the flok's record when it
// joined by code, and whether it may manage tenants.
type linkIdentity struct {
	tenant string
	rec    *instanceRec
	admin  bool
}

// linkAuth tells a link's token: a static instance token or an admin token from the config,
// both of the relay's own tenant, or a flok's own key from a join. An admin token presented
// besides it ([link] admin) gives the link tenant powers whichever way it linked, and links a
// flok that has nothing else (the first flok on a fresh relay, before it joins a tenant).
func (s *Server) linkAuth(tok, admin string) (linkIdentity, bool) {
	var who linkIdentity
	switch rec, ok := s.istore.byKey(tok); {
	case tokenIn(tok, s.cfg.AdminTokens):
		who = linkIdentity{tenant: DefaultTenant, admin: true}
	case tokenIn(tok, s.cfg.InstanceTokens):
		who = linkIdentity{tenant: DefaultTenant}
	case ok:
		who = linkIdentity{tenant: rec.Tenant, rec: &rec}
	case tok == "" && tokenIn(admin, s.cfg.AdminTokens): // [link] admin alone: it links like an instance token, with the powers
		who = linkIdentity{tenant: DefaultTenant, admin: true}
	default:
		return linkIdentity{}, false
	}
	if tokenIn(admin, s.cfg.AdminTokens) {
		who.admin = true
	}
	return who, true
}

// linkRefused is what an instance hears when its token opens nothing.
const linkRefused = "an instance token is needed: one of the relay's, or this flok's own from flok link join"

func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	if s.cfg.TunnelOnly {
		http.Error(w, "this relay takes encrypted links only: set [link] pin (flok-relay pin prints it)", http.StatusForbidden)
		return
	}
	who, ok := s.linkAuth(bearer(r), r.Header.Get(link.AdminHeader))
	if !ok {
		http.Error(w, linkRefused, http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return
	}
	conn.SetReadLimit(wire.MaxFrame) // counts what the frames inflate to
	how := ""
	if deflated(w.Header()) {
		how = ", compressed"
	}
	s.serveLink(r.Context(), wsConn{conn}, r.RemoteAddr, how, who)
}

// deflated reports that an accepted upgrade agreed to permessage-deflate, offered by the other
// end: one deflate window per connection and direction (CompressionContextTakeover), which
// takes a snapshot that changed by a line from a few KB to a few dozen bytes.
func deflated(h http.Header) bool {
	return strings.Contains(h.Get("Sec-WebSocket-Extensions"), "permessage-deflate")
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
	var who linkIdentity
	ok := false
	if err == nil && json.Unmarshal(data, &auth) == nil && auth.Type == wire.TypeAuth {
		who, ok = s.linkAuth(auth.Token, auth.Admin)
	}
	if !ok {
		tc.Close(websocket.StatusPolicyViolation, linkRefused)
		return
	}
	okFrame, _ := json.Marshal(wire.Frame{Type: wire.TypeAuth})
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = t.Write(wctx, okFrame)
	cancel()
	if err != nil {
		conn.CloseNow()
		return
	}
	t.Compress() // flok-link/2: deflated from here, after the token
	how := ", encrypted"
	if t.Compressed() {
		how += ", compressed"
	}
	s.serveLink(ctx, tc, r.RemoteAddr, how, who)
}

// serveLink runs an instance's link once it is authenticated: the hello, then its frames until
// the connection ends.
func (s *Server) serveLink(ctx context.Context, conn frameConn, remote, how string, who linkIdentity) {
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
	in, gen, out, replaced, why := s.register(who, *hello.Hello, conn, remote+how)
	if why != "" {
		s.cfg.Logf("instance %s from %s refused: %s", hello.Hello.Name, remote, why)
		conn.Close(websocket.StatusPolicyViolation, why)
		return
	}
	if replaced != nil { // the same name again: the earlier link is stale (a drop the relay had not noticed yet)
		replaced.Close(websocket.StatusGoingAway, "replaced by a newer link from "+in.name)
	}
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	go s.writer(wctx, conn, out, gen, in)
	defer func() {
		conn.CloseNow()
		switch gone, off := s.offline(in, gen); {
		case off:
			s.cfg.Logf("instance %s switched off (flok link off there)", in.label())
		case gone:
			s.cfg.Logf("instance %s offline", in.label())
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
		case wire.TypeFeed:
			if f.Feed != nil {
				s.feed(in, gen, *f.Feed)
			}
		case wire.TypeAck:
			s.ack(in, f.ID, f.Error)
		case wire.TypePhone:
			if f.Phone != nil && hasFeature(hello.Hello.Features, wire.FeaturePhone) {
				s.phoneOp(in, gen, f.ID, *f.Phone)
			}
		case wire.TypeEnroll:
			if f.Enroll != nil && hasFeature(hello.Hello.Features, wire.FeatureEnroll) {
				s.enrollOp(in, gen, f.ID, *f.Enroll)
			}
		case wire.TypeTenant:
			if f.Tenant != nil && hasFeature(hello.Hello.Features, wire.FeatureEnroll) {
				s.tenantOp(in, gen, f.ID, *f.Tenant, who.admin)
			}
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

// register makes an instance current under its name in its tenant; a link already up under that
// name is replaced (the same instance reconnecting after a drop the relay has not noticed yet)
// and returned for closing. The link's frame channel is returned too: a later link swaps in.out.
// Within a tenant the name is what phones address, so a flok that joined by code owns its name:
// another key, or a token from the config, presenting it is refused with the reason. An
// instance's name and tenant never change once made (label reads them without the lock): a
// flok that comes back under a new name is a new instance, the old one goes.
func (s *Server) register(who linkIdentity, h wire.Hello, conn frameConn, from string) (in *instance, gen uint64, out chan wire.Frame, replaced frameConn, why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tenantLocked(who.tenant)
	if t == nil {
		return nil, 0, nil, nil, "this flok's tenant is gone from the relay: flok link join joins another"
	}
	in = t.instances[h.Name]
	switch {
	case in != nil && in.rec != nil && who.rec != nil && in.rec.ID != who.rec.ID:
		return nil, 0, nil, nil, fmt.Sprintf("%s is another flok of yours (joined %s from %s): set [link] name, or flok link forget %s", h.Name, in.rec.Joined.Local().Format("2006-01-02"), in.rec.Hostname, h.Name)
	case in != nil && in.rec != nil && who.rec == nil:
		return nil, 0, nil, nil, fmt.Sprintf("%s is a flok that joined by code: set [link] name, or flok link forget %s", h.Name, h.Name)
	}
	if who.rec != nil { // the record may sit under an older name ([link] name changed): that instance goes
		for name, other := range t.instances {
			if other.rec != nil && other.rec.ID == who.rec.ID && name != h.Name {
				if other.online { // one link per key
					replaced = other.conn
				}
				for id, ch := range other.pending {
					ch <- "the instance reconnected under another name"
					delete(other.pending, id)
				}
				delete(t.instances, name)
				s.fanout(t, wire.Frame{Type: wire.TypeInstance, Instance: name, Inst: &wire.Instance{Name: name, Since: time.Now(), Gone: true}})
				break
			}
		}
	}
	if in == nil {
		in = &instance{tenant: t, name: h.Name, subs: map[string]int{}}
		t.instances[h.Name] = in
	}
	as := ""
	if who.rec != nil {
		rec := *who.rec
		rec.Name, rec.Hostname, rec.Version = h.Name, h.Hostname, h.Version
		in.rec = &rec
		s.istore.linked(rec.ID, h.Name, h.Hostname, h.Version, time.Now())
		as = ", joined by code"
	}
	if who.admin {
		as = ", admin"
	}
	s.cfg.Logf("instance %s online (flok %s on %s, %s%s)", in.label(), h.Version, h.Hostname, from, as)
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
	s.liveSoonLocked(t) // its agents join the overview
	s.fanout(t, wire.Frame{Type: wire.TypeInstance, Instance: in.name, Inst: s.view(in)})
	return in, in.gen, in.out, replaced, ""
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
		s.syncSoonLocked(in.tenant)
	}
	s.liveSoonLocked(in.tenant) // its agents leave the overview
	s.fanout(in.tenant, wire.Frame{Type: wire.TypeInstance, Instance: in.name, Inst: s.view(in)})
	return true, in.off
}

func (s *Server) view(in *instance) *wire.Instance {
	return &wire.Instance{Name: in.name, Online: in.online, Off: in.off, Since: in.since, Version: in.hello.Version, Hostname: in.hello.Hostname,
		Features: in.hello.Features, Snap: in.snap}
}

// info is an instance as flok link list shows it; the caller holds s.mu.
func (in *instance) info() wire.InstanceInfo {
	var i wire.InstanceInfo
	if in.rec != nil {
		i = in.rec.info()
	} else {
		i = wire.InstanceInfo{Name: in.name, Static: true}
	}
	i.Name, i.Online = in.name, in.online
	if in.hello.Hostname != "" {
		i.Hostname, i.Version = in.hello.Hostname, in.hello.Version
	}
	if in.online {
		i.LastSeen = time.Now()
	} else if i.LastSeen.IsZero() {
		i.LastSeen = in.since
	}
	return i
}

// tooOldFor says why an instance's flok cannot take an answer as it is (a long text, a file),
// from the features its hello advertised; "" when it can, or for an instance the relay does
// not know (request says so).
func (s *Server) tooOldFor(t *tenant, instance string, a answer.Answer) string {
	s.mu.Lock()
	in := t.instances[instance]
	var version string
	var features []string
	if in != nil {
		version, features = in.hello.Version, in.hello.Features
	}
	s.mu.Unlock()
	if in == nil {
		return ""
	}
	who := "flok " + version + " on " + instance
	switch {
	case len(a.Files) > 0 && !hasFeature(features, wire.FeatureFiles):
		return who + " cannot take files: update flok there"
	case a.Form != nil && !hasFeature(features, wire.FeatureForm):
		return who + " cannot fill a question form: update flok there, or answer with keys"
	case answer.Long(a.Text) && !hasFeature(features, wire.FeatureLongText):
		return fmt.Sprintf("%s takes at most %d characters on one line: update flok there for longer text", who, answer.MaxText)
	}
	return ""
}

// hasFeature reports whether a hello's features name one.
func hasFeature(features []string, want string) bool {
	for _, f := range features {
		if f == want {
			return true
		}
	}
	return false
}

// gone says why an instance cannot take a request; the caller holds s.mu.
func gone(in *instance) string {
	if in.off {
		return in.name + " is switched off (flok link on there)"
	}
	return in.name + " is offline"
}

// badgeLocked is a tenant's badge on the app: what waits unseen over every instance of it not
// switched off. The caller holds s.mu.
func (s *Server) badgeLocked(t *tenant) int {
	n := 0
	for _, in := range t.instances {
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
			s.syncSoonLocked(in.tenant)
			break
		}
	}
	in.waiting = waiting
	s.liveSoonLocked(in.tenant)
	s.fanout(in.tenant, wire.Frame{Type: wire.TypeInstance, Instance: in.name, Inst: s.view(in)})
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

// syncSoonLocked schedules one silent sync push to every phone of a tenant (a run of changes is
// one push; iOS throttles background pushes anyway, and each sync carries the whole state). The
// caller holds s.mu.
func (s *Server) syncSoonLocked(t *tenant) {
	if s.cfg.Push == nil || t.syncDue != nil {
		return
	}
	t.syncDue = time.AfterFunc(s.cfg.SyncDelay, func() { s.sync(t) })
}

// sync tells a tenant's phones which agents still wait on the user and the badge: the app
// removes the notifications of the others and sets the badge, without showing anything.
func (s *Server) sync(t *tenant) {
	s.mu.Lock()
	t.syncDue = nil
	badge, waiting := s.badgeLocked(t), []string{}
	for _, in := range t.instances {
		for pane := range in.waiting {
			waiting = append(waiting, in.name+":"+pane)
		}
	}
	devices := s.devices.listTenant(t.id)
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
		if a.tenant == in.tenant && a.subs[key] {
			a.send(wire.Frame{Type: wire.TypeScreen, Instance: in.name, Screen: &sc})
		}
	}
}

// feed forwards a pane's feed frame to the apps that look at the pane and keeps the feed so
// far (a Reset replaces it, items are added up to feed.MaxItems), so the next app that looks
// gets it at once as a Reset.
func (s *Server) feed(in *instance, gen uint64, f feed.Feed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.gen != gen {
		return
	}
	if in.subs[f.Pane] > 0 {
		if in.feeds == nil {
			in.feeds = map[string]*feed.Feed{}
		}
		kept := in.feeds[f.Pane]
		if f.Reset || kept == nil {
			c := f
			c.Reset = true
			c.Items = append([]feed.Item(nil), f.Items...)
			in.feeds[f.Pane] = &c
		} else {
			kept.Items = append(kept.Items, f.Items...)
			if n := len(kept.Items) - feed.MaxItems; n > 0 {
				kept.Items = append([]feed.Item(nil), kept.Items[n:]...)
			}
			if f.Note != "" {
				kept.Note = f.Note
			}
		}
	}
	key := [2]string{in.name, f.Pane}
	for a := range s.apps {
		if a.tenant == in.tenant && a.subs[key] {
			a.send(wire.Frame{Type: wire.TypeFeed, Instance: in.name, Feed: &f})
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

// fanout sends a frame to every app of a tenant; the caller holds s.mu.
func (s *Server) fanout(t *tenant, f wire.Frame) {
	for a := range s.apps {
		if a.tenant == t {
			a.send(f)
		}
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
	s.fanout(in.tenant, wire.Frame{Type: wire.TypeEvent, Instance: in.name, Event: &ev})
	badge := s.badgeLocked(in.tenant)
	devices := s.devices.listTenant(in.tenant.id)
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
			s.liveSoonLocked(in.tenant)
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
	if ev.Question != "" {
		n.Data["question"] = ev.Question
	}
	switch kind {
	case KindPermission:
		n.Body, n.Category = "● needs you: perm", CategoryPermission
		if tool := strings.TrimPrefix(strings.TrimPrefix(ev.Reason, "permission"), ":"); tool != "" {
			n.Body += ":" + tool
		}
	case KindQuestion:
		n.Body, n.Category = "● needs you", CategoryQuestion
		switch {
		case ev.Question != "": // the question itself, as the form's first tab asks it
			n.Body += ": " + ev.Question
		case ev.Reason != "":
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

func (s *Server) snapshotFrame(t *tenant) wire.Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotFrameLocked(t)
}

// snapshotFrameLocked is snapshotFrame for a caller that holds s.mu.
func (s *Server) snapshotFrameLocked(t *tenant) wire.Frame {
	info := t.info()
	f := wire.Frame{Type: wire.TypeInstances, Instances: []wire.Instance{}, TenantInfo: &info}
	for _, in := range t.instances {
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
	writeJSON(w, http.StatusOK, s.snapshotFrame(callerOf(r).t))
}

// request forwards an answer or a seen mark to an instance and waits for its outcome.
func (s *Server) request(t *tenant, instance string, f wire.Frame) error {
	s.mu.Lock()
	in := t.instances[instance]
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
	Instance string             `json:"instance"`
	Pane     string             `json:"pane"`
	Text     string             `json:"text,omitempty"`
	Keys     []string           `json:"keys,omitempty"`
	Files    []answer.File      `json:"files,omitempty"`
	File     *answer.File       `json:"file,omitempty"` // one file, as the first app build sent it
	Form     *answer.FormAnswer `json:"form,omitempty"` // the agent's question form, answered
}

func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var req answerReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, answer.MaxBody)).Decode(&req); err != nil || req.Instance == "" || req.Pane == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "instance, pane and text, keys or a file are needed"})
		return
	}
	a, err := answer.Normalize(answer.Answer{Pane: req.Pane, Text: req.Text, Keys: req.Keys, Files: req.Files, File: req.File, Form: req.Form})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	t := callerOf(r).t
	if why := s.tooOldFor(t, req.Instance, a); why != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": why})
		return
	}
	if err := s.request(t, req.Instance, wire.Frame{Type: wire.TypeAnswer, Answer: &a}); err != nil {
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
	if err := s.request(callerOf(r).t, req.Instance, wire.Frame{Type: wire.TypeSeen, Pane: req.Pane}); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAppWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return
	}
	conn.SetReadLimit(wire.MaxAppFrame)
	who := callerOf(r)
	a := &app{tenant: who.t, out: make(chan wire.Frame, 256), subs: map[[2]string]bool{}, conn: wsConn{conn}, phone: who.phone}
	s.mu.Lock()
	s.apps[a] = struct{}{}
	s.mu.Unlock()
	if a.phone != "" {
		s.phones.touch(a.phone, time.Now())
	}
	ctx := r.Context()
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	go s.writer(wctx, wsConn{conn}, a.out, 0, nil)
	defer func() {
		conn.CloseNow()
		s.mu.Lock()
		delete(s.apps, a)
		for key := range a.subs {
			s.unsubscribeLocked(a.tenant, key[0], key[1])
		}
		s.mu.Unlock()
	}()
	a.send(s.snapshotFrame(a.tenant))
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
				s.subscribeLocked(a.tenant, f.Instance, f.Pane)
				if sc, ok := a.tenant.instances[f.Instance].screens[f.Pane]; ok { // someone else looks already
					a.send(wire.Frame{Type: wire.TypeScreen, Instance: f.Instance, Screen: &sc})
				}
				if fd, ok := a.tenant.instances[f.Instance].feeds[f.Pane]; ok {
					c := *fd
					c.Items = append([]feed.Item(nil), fd.Items...)
					a.send(wire.Frame{Type: wire.TypeFeed, Instance: f.Instance, Feed: &c})
				}
			case !f.On && a.subs[key]:
				delete(a.subs, key)
				s.unsubscribeLocked(a.tenant, f.Instance, f.Pane)
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
				if why := s.tooOldFor(a.tenant, inst, n); why != "" {
					a.send(wire.Frame{Type: wire.TypeAck, ID: id, Instance: inst, Error: why})
					continue
				}
				fwd.Answer = &n
				fwd.Pane = ""
			}
			go func() {
				ack := wire.Frame{Type: wire.TypeAck, ID: id, Instance: inst}
				if err := s.request(a.tenant, inst, fwd); err != nil {
					ack.Error = err.Error()
				}
				a.send(ack)
			}()
		}
	}
}

// subscribeLocked counts one more app looking at a pane; the first one asks the instance for
// the screen. The caller holds s.mu.
func (s *Server) subscribeLocked(t *tenant, name, pane string) {
	in := t.instances[name]
	if in == nil {
		in = &instance{tenant: t, name: name, subs: map[string]int{}, pending: map[string]chan string{}} // not seen yet: remembered for when it comes
		t.instances[name] = in
	}
	in.subs[pane]++
	if in.subs[pane] == 1 && in.online {
		select {
		case in.out <- wire.Frame{Type: wire.TypeSubscribe, Pane: pane, On: true}:
		default:
		}
	}
}

func (s *Server) unsubscribeLocked(t *tenant, name, pane string) {
	in := t.instances[name]
	if in == nil || in.subs[pane] == 0 {
		return
	}
	in.subs[pane]--
	if in.subs[pane] == 0 {
		delete(in.subs, pane)
		delete(in.screens, pane)
		delete(in.feeds, pane)
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
	list := s.devices.listTenant(callerOf(r).tenant)
	out := make([]map[string]any, 0, len(list))
	for _, d := range list {
		out = append(out, map[string]any{"token": short(d.Token), "name": d.Name, "sandbox": d.Sandbox, "added": d.Added, "prefs": d.Prefs, "phone": d.Phone})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out, "push": s.cfg.Push != nil})
}

// ownDevice is the caller's tenant's device under a path token; false (and a 404 written) when
// there is none, so a tenant never touches another's phones.
func (s *Server) ownDevice(w http.ResponseWriter, r *http.Request) (device, bool) {
	d, ok := s.devices.get(r.PathValue("token"))
	if !ok || d.Tenant != callerOf(r).tenant {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such device"})
		return device{}, false
	}
	return d, true
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
	who := callerOf(r)
	prev, had := s.devices.get(req.Token)
	if had && prev.Tenant != who.tenant && who.phone == "" { // a static token never takes a phone from another tenant
		writeJSON(w, http.StatusConflict, map[string]any{"error": "this device is registered with another tenant"})
		return
	}
	d := device{Token: req.Token, Name: req.Name, Sandbox: req.Sandbox, Added: time.Now(), Prefs: req.Prefs, Phone: who.phone, Tenant: who.tenant}
	if had && prev.Tenant != who.tenant { // the same phone paired into another tenant: it moves, with nothing of the old
		d.Prefs, d.ActivityStart, d.Activities = req.Prefs, "", nil
		s.devices.remove(req.Token)
		had = false
		if prev.Phone != "" && !s.devices.anyFor(prev.Phone) {
			if old, ok := s.forgetPhone(prev.Phone); ok {
				s.cfg.Logf("phone %s (%s) paired again in another tenant: the old pairing is forgotten", old.Name, old.ID)
			}
		}
	}
	if err := s.devices.add(d); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.cfg.Logf("device %s registered", d.label())
	// the same push token under a new pairing is the same phone paired again: the old pairing,
	// with no device left to it, is forgotten, so its key stops working
	if had && prev.Phone != "" && d.Phone != "" && prev.Phone != d.Phone && !s.devices.anyFor(prev.Phone) {
		if old, ok := s.forgetPhone(prev.Phone); ok {
			s.cfg.Logf("phone %s (%s) paired again as %s: the old pairing is forgotten", old.Name, old.ID, d.Phone)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "push": s.cfg.Push != nil})
}

// --- phones ------------------------------------------------------------------------------------

type pairReq struct {
	Code string `json:"code"`
	Name string `json:"name,omitempty"`
}

// handlePair is how a phone gets its key: the code the terminal shows (flok phone pair), scanned
// or typed, for a token of its own. One answer covers a code unknown, used or expired, and an
// address that keeps guessing is slowed (ipLimiter); the code is 40 bits and lives five minutes.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	ip, now := clientIP(r), time.Now()
	if ok, retry := s.pairLimit.allow(ip, now); !ok {
		retry = retry.Round(time.Second)
		if retry < time.Second {
			retry = time.Second
		}
		w.Header().Set("Retry-After", fmt.Sprint(int(retry.Seconds())))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many attempts: try again in " + retry.String()})
		return
	}
	var req pairReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "a pairing code is needed (code, and the phone's name)"})
		return
	}
	code := NormalizeCode(req.Code)
	if len(code) != PairCodeLen {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "a pairing code is eight letters and digits, as flok phone pair shows it"})
		return
	}
	t, instance, reqID, ok := s.redeemCode(code, now)
	if !ok {
		s.pairLimit.failed(ip, now)
		s.cfg.Logf("pair: a code from %s was not valid", ip)
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "that code is not valid any more: run flok phone pair again"})
		return
	}
	s.pairLimit.succeeded(ip)
	tok := NewToken()
	p := phone{ID: newPhoneID(), Tenant: t.id, Name: phoneName(req.Name), TokenSHA256: hashToken(tok), Paired: now, LastSeen: now, Instance: instance}
	if err := s.phones.add(p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	info := p.info()
	s.mu.Lock()
	if in := t.instances[instance]; in != nil && in.online { // the terminal that shows the code hears it was taken
		s.cfg.Logf("phone %s (%s) paired through %s", p.Name, p.ID, in.label())
		select {
		case in.out <- wire.Frame{Type: wire.TypePhone, ID: reqID, Phone: &wire.Phone{Op: "paired", Phone: &info}}:
		default:
		}
	} else {
		s.cfg.Logf("phone %s (%s) paired through %s", p.Name, p.ID, instance)
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"token": tok, "phone": map[string]any{"id": p.ID, "name": p.Name},
		"tenant": map[string]any{"id": t.id, "name": t.Name()}, "relay": map[string]any{"version": s.cfg.Version}})
}

// phoneOp answers an instance's phone frame: pair mints its code, list says what is paired,
// forget revokes a phone. The reply goes back under the frame's id.
func (s *Server) phoneOp(in *instance, gen uint64, id string, p wire.Phone) {
	reply := wire.Frame{Type: wire.TypePhone, ID: id, Phone: &wire.Phone{Op: p.Op}}
	now := time.Now()
	switch p.Op {
	case "pair":
		s.mu.Lock()
		code, expires := s.mintCodeLocked(in, id, now)
		s.mu.Unlock()
		reply.Phone.Code, reply.Phone.ExpiresAt = FormatCode(code), expires
		s.cfg.Logf("pair: %s shows a code, good for %s", in.label(), s.cfg.PairTTL)
	case "list":
		reply.Phone.Phones = s.phoneInfos(in.tenant.id)
	case "forget":
		ph, ok := s.phones.find(in.tenant.id, p.Name)
		switch {
		case strings.TrimSpace(p.Name) == "":
			reply.Error = "a phone's name or id is needed (flok phone list)"
		case !ok:
			reply.Error = "no phone " + strings.TrimSpace(p.Name) + " (flok phone list; two of one name go by id)"
		default:
			old, _ := s.forgetPhone(ph.ID)
			info := old.info()
			reply.Phone.Phone = &info
			reply.Phone.Phones = s.phoneInfos(in.tenant.id)
			s.cfg.Logf("phone %s (%s) forgotten by %s", old.Name, old.ID, in.label())
		}
	default:
		reply.Error = "unknown phone request " + p.Op
	}
	s.replyTo(in, gen, reply)
}

// replyTo sends a reply frame to an instance on the link the request came in on.
func (s *Server) replyTo(in *instance, gen uint64, reply wire.Frame) {
	s.mu.Lock()
	if in.gen == gen && in.online {
		select {
		case in.out <- reply:
		default:
		}
	}
	s.mu.Unlock()
}

// forgetPhone revokes a phone: its key stops working, the devices it registered go (its pushes
// stop) and its open streams are closed with wire.CloseForgotten, outside s.mu (a close shakes
// hands). False when there is no such phone.
func (s *Server) forgetPhone(id string) (phone, bool) {
	p, ok := s.phones.remove(id)
	if !ok {
		return phone{}, false
	}
	for _, d := range s.devices.removeByPhone(id) {
		s.cfg.Logf("device %s removed with its phone", d.label())
	}
	s.mu.Lock()
	var conns []frameConn
	for a := range s.apps {
		if a.phone == id && a.conn != nil {
			conns = append(conns, a.conn)
		}
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Close(websocket.StatusPolicyViolation, wire.CloseForgotten)
	}
	return p, true
}

// handleDevicePrefs replaces a phone's notification settings (the app sends them whole).
func (s *Server) handleDevicePrefs(w http.ResponseWriter, r *http.Request) {
	var p Prefs
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&p); err != nil || !p.valid() {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prefs: not JSON, an unknown kind or sound, or a mute without instance and agent"})
		return
	}
	p.prune(time.Now())
	if _, ok := s.ownDevice(w, r); !ok {
		return
	}
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
	if _, ok := s.ownDevice(w, r); !ok {
		return
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
	delete(s.liveStarted, tok)      // the phone answered: what it runs is known now
	s.liveSoonLocked(callerOf(r).t) // a new activity gets the current state at once
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
	t := callerOf(r).t
	a := &app{tenant: t, out: make(chan wire.Frame, 16), subs: map[[2]string]bool{key: true}}
	s.mu.Lock()
	in := t.instances[inst]
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
	s.subscribeLocked(t, inst, pane)
	cached, ok := in.screens[pane]
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.apps, a)
		s.unsubscribeLocked(t, inst, pane)
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
	if _, ok := s.ownDevice(w, r); !ok {
		return
	}
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
