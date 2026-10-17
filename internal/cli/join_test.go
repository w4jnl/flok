package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
)

// fakeJoinRelay is the relay's join API as flok link join sees it: one join at a time, approved
// by the test.
type fakeJoinRelay struct {
	srv      *httptest.Server
	mu       sync.Mutex
	secret   string
	approved bool
	declined bool
	accepted int
	gone     bool
	started  []map[string]string
}

func newFakeJoinRelay(t *testing.T) *fakeJoinRelay {
	t.Helper()
	r := &fakeJoinRelay{}
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /api/enroll", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(req.Body).Decode(&body)
		r.mu.Lock()
		r.started = append(r.started, body)
		r.secret, r.approved, r.declined, r.gone = "s3cret", false, false, false
		r.mu.Unlock()
		writeJSON(w, http.StatusCreated, map[string]any{"id": "j1", "secret": "s3cret", "code": "AB7Q-K2MN", "expires_at": time.Now().Add(5 * time.Minute), "poll_s": 1})
	})
	auth := func(req *http.Request) bool { return req.Header.Get("Authorization") == "Bearer s3cret" }
	mux.HandleFunc("GET /api/enroll/{id}", func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		gone, approved, declined := r.gone, r.approved, r.declined
		r.mu.Unlock()
		switch {
		case !auth(req) || gone:
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such join: it expired, or was declined; run flok link join again"})
		case declined:
			writeJSON(w, http.StatusGone, map[string]any{"status": "declined", "error": "the join was declined"})
		case approved:
			writeJSON(w, http.StatusOK, map[string]any{"status": "approved", "tenant": map[string]any{"id": "t1", "name": "jaro"}, "approved_by": "phone Jaro's iPhone"})
		default:
			time.Sleep(50 * time.Millisecond) // a short hold, like the relay's long poll
			writeJSON(w, http.StatusOK, map[string]any{"status": "pending"})
		}
	})
	mux.HandleFunc("POST /api/enroll/{id}/accept", func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if !auth(req) || !r.approved || r.gone {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "the join is not approved yet"})
			return
		}
		r.accepted++
		r.gone = true
		writeJSON(w, http.StatusCreated, map[string]any{"token": strings.Repeat("k", 48), "pin": "sha256:abcd", "tenant": map[string]any{"id": "t1", "name": "jaro"},
			"instance": map[string]any{"id": "i1", "name": "office"}, "approved_by": "phone Jaro's iPhone", "relay": map[string]any{"version": "v9"}})
	})
	mux.HandleFunc("DELETE /api/enroll/{id}", func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.gone = true
		r.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeJoinRelay) approve() {
	r.mu.Lock()
	r.approved = true
	r.mu.Unlock()
}

func newLinkOps(dir string, relay *fakeJoinRelay) (linkOps, *bytes.Buffer, *bytes.Buffer) {
	out, errw := &bytes.Buffer{}, &bytes.Buffer{}
	var cfg config.Config
	cfg.Link.Name = "office"
	return linkOps{relayCmd: relayCmd{name: "link", dir: dir, cfg: cfg, now: time.Now, wait: 2 * time.Second, out: out, errw: errw}, version: "v9",
		joiner: func(origin string) link.Joiner { return link.Joiner{Origin: origin, HTTP: relay.srv.Client()} }}, out, errw
}

// flok link join asks the relay, shows the code (here: prints it), waits for the approval,
// accepts it and keeps the key; the record then wins over [link] in config.toml; --json gives a
// script the code and the outcome; without a terminal and without --yes an approval is declined.
func TestLinkJoin(t *testing.T) {
	dir := t.TempDir()
	relay := newFakeJoinRelay(t)
	c, out, errw := newLinkOps(dir, relay)
	if rc := c.run([]string{"join"}); rc != 2 || !strings.Contains(errw.String(), "the relay's address is needed") {
		t.Fatalf("join without a relay: %d %s", rc, errw)
	}
	errw.Reset()
	if rc := c.run([]string{"join", "ftp://x"}); rc != 1 || !strings.Contains(errw.String(), "not a relay address") {
		t.Fatalf("join with a bad relay: %d %s", rc, errw)
	}
	errw.Reset()
	go func() {
		time.Sleep(300 * time.Millisecond)
		relay.approve()
	}()
	if rc := c.run([]string{"join", relay.srv.URL, "--json"}); rc != 0 {
		t.Fatalf("join --json: %d %s %s", rc, out, errw)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("join --json prints the code and the outcome: %q", out)
	}
	var first struct{ ID, Code, URL string }
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil || first.Code != "AB7Q-K2MN" || first.URL != joinURL(relay.srv.URL, "AB7Q-K2MN") || !strings.Contains(first.URL, "flok://link?code=AB7Q-K2MN&relay=http%3A%2F%2F127.0.0.1") {
		t.Fatalf("first line: %s (%v)", lines[0], err)
	}
	var second struct{ Status, Tenant, Name, Instance string }
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil || second.Status != "joined" || second.Tenant != "jaro" || second.Name != "office" || second.Instance != "i1" {
		t.Fatalf("second line: %s (%v)", lines[1], err)
	}
	if relay.started[len(relay.started)-1]["name"] != "office" || relay.started[len(relay.started)-1]["version"] != "v9" || relay.accepted != 1 {
		t.Fatalf("the relay saw %v, accepted %d", relay.started, relay.accepted)
	}
	l, ok := state.New(dir).LoadLink()
	if !ok || l.Relay != relay.srv.URL || l.Token != strings.Repeat("k", 48) || l.Pin != "sha256:abcd" || l.Tenant != "jaro" || l.Name != "office" || l.ApprovedBy != "phone Jaro's iPhone" {
		t.Fatalf("the record: %+v %v", l, ok)
	}
	// the record is what the link uses now, over the config
	c.cfg.Link.URL, c.cfg.Link.Token = "wss://other.example/link", "cfg-tok"
	if s := c.settings(); s.URL != relay.srv.URL || s.Token != l.Token || s.Joined == nil || s.ConfigURL != "wss://other.example/link" {
		t.Fatalf("settings: %+v", s)
	}
	// flok link status says so
	lc, lout, _ := linkTest(dir, relay.srv.URL)
	lc.joined = &l
	if rc := lc.run([]string{"status"}); rc != 0 || !strings.Contains(lout.String(), "joined jaro as office") || !strings.Contains(lout.String(), "approved by phone Jaro's iPhone") {
		t.Fatalf("status: %d %s", rc, lout)
	}
	// a second join from a plain pipe: approved, but nobody can confirm, so it is declined
	out.Reset()
	go func() {
		time.Sleep(300 * time.Millisecond)
		relay.approve()
	}()
	if rc := c.run([]string{"join", relay.srv.URL}); rc != 1 || !strings.Contains(out.String(), "code   AB7Q-K2MN") || !strings.Contains(out.String(), "approved by phone Jaro's iPhone for the tenant jaro") || !strings.Contains(out.String(), "run flok link join with --yes") {
		t.Fatalf("join from a pipe: %d %s", rc, out)
	}
	if !relay.gone || relay.accepted != 1 {
		t.Fatal("the join was not withdrawn")
	}
	// on a terminal the screen takes it; its accept writes the record
	out.Reset()
	c.tty = true
	old := runCodeScreen
	runCodeScreen = func(s codeScreen) (codeScreen, error) {
		if s.code != "AB7Q-K2MN" || s.accept == nil || s.decline == nil || s.watch == nil || !strings.Contains(s.hint, "flok link approve AB7Q-K2MN") {
			t.Errorf("screen: %+v", s)
		}
		relay.approve()
		deadline := time.Now().Add(3 * time.Second)
		for {
			step, text := s.watch()
			if step == stepApproved {
				if !strings.Contains(text, "approved by phone Jaro's iPhone") {
					t.Errorf("approval words: %q", text)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the screen never saw the approval")
			}
			time.Sleep(20 * time.Millisecond)
		}
		outcome, err := s.accept()
		if err != nil {
			t.Fatal(err)
		}
		s.step, s.outcome = stepDone, outcome
		return s, nil
	}
	defer func() { runCodeScreen = old }()
	if rc := c.run([]string{"join", relay.srv.URL, "--name", "lab"}); rc != 0 || !strings.Contains(out.String(), "joined jaro as office; flok up connects it") {
		t.Fatalf("join on a terminal: %d %s %s", rc, out, errw)
	}
	if relay.started[len(relay.started)-1]["name"] != "lab" {
		t.Fatalf("--name: %v", relay.started)
	}
	// leave removes the record (the relay is asked through the sidebar, which is not running)
	out.Reset()
	errw.Reset()
	if rc := c.run([]string{"leave"}); rc != 0 || !strings.Contains(out.String(), "left jaro") || !strings.Contains(errw.String(), "the relay keeps this flok's key until flok link forget") {
		t.Fatalf("leave: %d %s %s", rc, out, errw)
	}
	if _, ok := state.New(dir).LoadLink(); ok {
		t.Fatal("the record stays after leave")
	}
	errw.Reset()
	if rc := c.run([]string{"leave"}); rc != 1 || !strings.Contains(errw.String(), "has not joined by code") {
		t.Fatalf("leave twice: %d %s", rc, errw)
	}
}

// fakeRelaySidebar answers link and tenant requests the way the sidebar does, from a function.
func fakeRelaySidebar(dir string, reply func(state.Request) wire.Frame) (stop func()) {
	st := state.New(dir)
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			case <-time.After(10 * time.Millisecond):
			}
			for _, r := range st.DrainRequests(time.Now(), time.Minute) {
				f := reply(r)
				f.Type, f.ID = r.Cmd, r.ID
				_ = st.WriteReply(r.ID, f)
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done); <-finished }) }
}

// approve, list and forget travel as requests through the sidebar, like flok phone; so do the
// tenant commands, whose replies read right.
func TestLinkOpsAndTenantThroughTheSidebar(t *testing.T) {
	dir := t.TempDir()
	livePhoneSidebar(t, dir, &snapshot.Link{State: "connected"})
	when := time.Date(2026, 10, 17, 9, 0, 0, 0, time.Local)
	var mu sync.Mutex
	var asked []state.Request
	stop := fakeRelaySidebar(dir, func(r state.Request) wire.Frame {
		mu.Lock()
		asked = append(asked, r)
		mu.Unlock()
		switch r.Cmd + " " + r.Op {
		case "link approve":
			if r.Code == "BBBB-BBBB" {
				return wire.Frame{Error: "no flok is waiting with that code: it expired, or was mistyped (run flok link join there again)"}
			}
			return wire.Frame{Enroll: &wire.Enroll{Op: "approve", Tenant: "jaro", Pending: &wire.Joining{Name: "office", Hostname: "macmini", Version: "v9"}}}
		case "link list":
			return wire.Frame{Enroll: &wire.Enroll{Op: "list", Tenant: "jaro", Instances: []wire.InstanceInfo{{Name: "home", Static: true, Online: true, Hostname: "mac"},
				{ID: "i1", Name: "office", Hostname: "macmini", Version: "v9", Joined: when, LastSeen: when, ApprovedBy: "phone Jaro's iPhone"}}}}
		case "link forget":
			return wire.Frame{Enroll: &wire.Enroll{Op: "forget", Tenant: "jaro", Instance: &wire.InstanceInfo{Name: "office"}, Instances: []wire.InstanceInfo{{Name: "home"}}}}
		case "tenant list":
			return wire.Frame{Tenant: &wire.Tenant{Op: "list", Tenants: []wire.TenantInfo{{ID: "default", Name: "default", Static: true, Instances: 2, Online: 1, Phones: 1}, {ID: "t2", Name: "acme", Created: when, Instances: 1}}}}
		case "tenant show":
			return wire.Frame{Tenant: &wire.Tenant{Op: "show", Tenant: &wire.TenantInfo{ID: "t2", Name: "acme", Floks: []wire.InstanceInfo{{ID: "i2", Name: "lab", Joined: when}},
				PhoneList: []wire.PhoneInfo{{ID: "p2", Name: "Lab phone", Paired: when, Instance: "lab"}}}}}
		case "tenant approve":
			return wire.Frame{Tenant: &wire.Tenant{Op: "approve", Tenant: &wire.TenantInfo{Name: r.Name}, Pending: &wire.Joining{Name: "lab", Hostname: "lab-box", Version: "v9"}}}
		case "tenant remove":
			return wire.Frame{Tenant: &wire.Tenant{Op: "remove", Tenant: &wire.TenantInfo{Name: "acme", Instances: 1, Phones: 1}}}
		}
		return wire.Frame{Error: "unknown " + r.Cmd + " " + r.Op}
	})
	defer stop()

	c, out, errw := newLinkOps(dir, newFakeJoinRelay(t))
	c.cfg.Link.URL = "wss://flok.example.net/link"
	if rc := c.run([]string{"approve", "AB7Q-K2MN"}); rc != 0 || !strings.Contains(out.String(), "approved office (macmini, flok v9) into jaro") {
		t.Fatalf("approve: %d %s %s", rc, out, errw)
	}
	if rc := c.run([]string{"approve", "BBBB-BBBB"}); rc != 1 || !strings.Contains(errw.String(), "no flok is waiting with that code") {
		t.Fatalf("approve a wrong code: %d %s", rc, errw)
	}
	out.Reset()
	if rc := c.run([]string{"list"}); rc != 0 || !strings.Contains(out.String(), "tenant jaro") || !strings.Contains(out.String(), "a token from the relay's config") || !strings.Contains(out.String(), "phone Jaro's iPhone") || !strings.Contains(out.String(), "online") {
		t.Fatalf("list:\n%s", out)
	}
	out.Reset()
	if rc := c.run([]string{"list", "--json"}); rc != 0 || !strings.Contains(out.String(), `"tenant":"jaro"`) || !strings.Contains(out.String(), `"name":"office"`) {
		t.Fatalf("list --json: %s", out)
	}
	out.Reset()
	if rc := c.run([]string{"forget", "office"}); rc != 0 || !strings.Contains(out.String(), "office forgotten: its key no longer works") || !strings.Contains(out.String(), "still in jaro: home") {
		t.Fatalf("forget: %d %s", rc, out)
	}
	mu.Lock()
	if len(asked) != 5 || asked[0].Cmd != "link" || asked[0].Op != "approve" || asked[0].Code != "AB7Q-K2MN" || asked[4].Op != "forget" || asked[4].Name != "office" {
		t.Fatalf("requests: %+v", asked)
	}
	mu.Unlock()

	tc := tenantCmd{relayCmd{name: "tenant", dir: dir, cfg: c.cfg, now: time.Now, wait: 2 * time.Second, out: out, errw: errw}}
	out.Reset()
	if rc := tc.run([]string{"list"}); rc != 0 || !strings.Contains(out.String(), "default") || !strings.Contains(out.String(), "the relay's own") || !strings.Contains(out.String(), "acme") {
		t.Fatalf("tenant list: %d %s", rc, out)
	}
	out.Reset()
	if rc := tc.run([]string{"show", "acme"}); rc != 0 || !strings.Contains(out.String(), "tenant acme (t2)") || !strings.Contains(out.String(), "lab") || !strings.Contains(out.String(), "Lab phone") {
		t.Fatalf("tenant show: %d %s", rc, out)
	}
	out.Reset()
	if rc := tc.run([]string{"approve", "AB7Q-K2MN", "--tenant", "acme"}); rc != 0 || !strings.Contains(out.String(), "approved lab (lab-box, flok v9) into acme") {
		t.Fatalf("tenant approve: %d %s %s", rc, out, errw)
	}
	errw.Reset()
	if rc := tc.run([]string{"approve", "AB7Q-K2MN"}); rc != 2 || !strings.Contains(errw.String(), "need <code> --tenant <name>") {
		t.Fatalf("tenant approve without a tenant: %d %s", rc, errw)
	}
	out.Reset()
	if rc := tc.run([]string{"remove", "acme"}); rc != 0 || !strings.Contains(out.String(), "acme removed: 1 floks and 1 phones lost their keys") {
		t.Fatalf("tenant remove: %d %s", rc, out)
	}
	mu.Lock()
	last := asked[len(asked)-2]
	mu.Unlock()
	if last.Cmd != "tenant" || last.Op != "approve" || last.Code != "AB7Q-K2MN" || last.Name != "acme" {
		t.Fatalf("the tenant approve request: %+v", last)
	}
}
