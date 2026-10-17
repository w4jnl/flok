package relay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/tunnel"
	"github.com/w4jnl/flok/internal/link/wire"
)

// startJoin is a flok asking to join: its id, secret and code.
func startJoin(t *testing.T, srv *httptest.Server, name, host string) (id, secret, code string) {
	t.Helper()
	st, out := api(t, srv, "", "POST", "/api/enroll", map[string]any{"name": name, "hostname": host, "version": "v9"})
	if st != http.StatusCreated {
		t.Fatalf("enroll: %d %v", st, out)
	}
	id, _ = out["id"].(string)
	secret, _ = out["secret"].(string)
	code, _ = out["code"].(string)
	if id == "" || len(secret) != 48 || !codeShape.MatchString(code) {
		t.Fatalf("enroll gave %v", out)
	}
	return id, secret, code
}

// enrollReply is the relay's answer to an enroll or tenant frame under a request id.
func enrollReply(id string) func(wire.Frame) bool {
	return func(f wire.Frame) bool { return (f.Type == wire.TypeEnroll || f.Type == wire.TypeTenant) && f.ID == id }
}

// A flok joins: it asks for a code, polls, a paired phone looks the code up and approves, the
// poll says who approved and for which tenant, the flok accepts for its key, links with it,
// shows up for the phone; a flok with an admin token puts another flok into a new tenant, whose
// phone sees that tenant alone; list, forget (the link closed with the reason, the app told it
// is gone), the refusals, and everything after a restart.
func TestJoiningAndTenants(t *testing.T) {
	dir := t.TempDir()
	JoinPoll = 300 * time.Millisecond
	defer func() { JoinPoll = 20 * time.Second }()
	s, err := New(Config{InstanceTokens: []string{"inst-tok"}, AdminTokens: []string{"admin-tok"}, DataDir: dir, Logf: t.Logf, Version: "v9"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	home := linkRaw(t, srv, "admin-tok", "home") // the relay's own tenant, with admin powers
	phoneTok, _ := pairThrough(t, srv, home, "p1", "Jaro's iPhone")
	app := dialApp(t, srv, phoneTok)
	app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeInstances })

	// --- office joins the default tenant, approved by the phone ---------------------------------
	id, secret, code := startJoin(t, srv, "office", "macmini-office")
	if st, out := api(t, srv, "wrong", "GET", "/api/enroll/"+id, nil); st != http.StatusNotFound {
		t.Fatalf("a poll without the secret: %d %v", st, out)
	}
	st, out := api(t, srv, secret, "GET", "/api/enroll/"+id, nil)
	if st != http.StatusOK || out["status"] != "pending" {
		t.Fatalf("poll while pending: %d %v", st, out)
	}
	if st, out := api(t, srv, secret, "POST", "/api/enroll/"+id+"/accept", nil); st != http.StatusConflict {
		t.Fatalf("accept before approval: %d %v", st, out)
	}
	if st, out := api(t, srv, phoneTok, "GET", "/api/enroll/code/AAAA-AAAA", nil); st != http.StatusNotFound || !strings.Contains(out["error"].(string), "no flok is waiting") {
		t.Fatalf("lookup of a wrong code: %d %v", st, out)
	}
	st, out = api(t, srv, phoneTok, "GET", "/api/enroll/code/"+strings.ToLower(code), nil)
	if st != http.StatusOK || out["pending"].(map[string]any)["hostname"] != "macmini-office" || out["tenant"].(map[string]any)["name"] != "default" {
		t.Fatalf("lookup: %d %v", st, out)
	}
	go func() { // the approval lands while the flok polls
		time.Sleep(50 * time.Millisecond)
		api(t, srv, phoneTok, "POST", "/api/enroll/approve", map[string]any{"code": code})
	}()
	st, out = api(t, srv, secret, "GET", "/api/enroll/"+id, nil)
	if st != http.StatusOK || out["status"] != "approved" || out["approved_by"] != "phone Jaro's iPhone" || out["tenant"].(map[string]any)["name"] != "default" {
		t.Fatalf("poll after approval: %d %v", st, out)
	}
	if st, out := api(t, srv, phoneTok, "POST", "/api/enroll/approve", map[string]any{"code": code}); st != http.StatusConflict || !strings.Contains(out["error"].(string), "approved already") {
		t.Fatalf("approving twice: %d %v", st, out)
	}
	st, out = api(t, srv, secret, "POST", "/api/enroll/"+id+"/accept", nil)
	if st != http.StatusCreated {
		t.Fatalf("accept: %d %v", st, out)
	}
	officeKey, _ := out["token"].(string)
	if len(officeKey) != 48 || out["approved_by"] != "phone Jaro's iPhone" || out["instance"].(map[string]any)["name"] != "office" {
		t.Fatalf("accept gave %v", out)
	}
	if st, _ := api(t, srv, secret, "GET", "/api/enroll/"+id, nil); st != http.StatusNotFound {
		t.Fatalf("the join is spent after the accept: %d", st)
	}
	office := linkRaw(t, srv, officeKey, "office")
	f := app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeInstance && f.Instance == "office" && f.Inst.Online })
	if f.Inst.Hostname != "box" {
		t.Fatalf("the phone sees office: %+v", f.Inst)
	}

	// --- list and the name rules ---------------------------------------------------------------
	home.send(t, wire.Frame{Type: wire.TypeEnroll, ID: "l1", Enroll: &wire.Enroll{Op: "list"}})
	f = home.next(t, enrollReply("l1"))
	if f.Error != "" || len(f.Enroll.Instances) != 2 || f.Enroll.Instances[0].Name != "home" || !f.Enroll.Instances[0].Static || f.Enroll.Instances[1].Name != "office" || f.Enroll.Instances[1].ID == "" || !f.Enroll.Instances[1].Online || f.Enroll.Instances[1].ApprovedBy != "phone Jaro's iPhone" {
		t.Fatalf("list: %q %+v", f.Error, f.Enroll.Instances)
	}
	// another key presenting office's name is refused, so is a config token
	_, _, code2 := startJoin(t, srv, "office", "impostor")
	if st, out := api(t, srv, phoneTok, "POST", "/api/enroll/approve", map[string]any{"code": code2}); st != http.StatusConflict || !strings.Contains(out["error"].(string), "office is online in default already") {
		t.Fatalf("approving a name that is online: %d %v", st, out)
	}
	if why := refusedLink(t, srv, "inst-tok", "office"); !strings.Contains(why, "office is a flok that joined by code") {
		t.Fatalf("a config token under office's name: %q", why)
	}
	// the approver's wrong codes are slowed per approver after a few
	for i := 0; i < 6; i++ {
		api(t, srv, phoneTok, "GET", "/api/enroll/code/BBBB-BBBB", nil)
	}
	if st, out := api(t, srv, phoneTok, "GET", "/api/enroll/code/"+code2, nil); st != http.StatusNotFound || !strings.Contains(out["error"].(string), "too many wrong codes") {
		t.Fatalf("after six wrong codes: %d %v", st, out)
	}

	// --- a new tenant, made by the admin flok approving a join into it ------------------------
	id3, secret3, code3 := startJoin(t, srv, "lab", "lab-box")
	office.send(t, wire.Frame{Type: wire.TypeTenant, ID: "t0", Tenant: &wire.Tenant{Op: "list"}})
	if f = office.next(t, enrollReply("t0")); !strings.Contains(f.Error, "not an admin token") {
		t.Fatalf("a plain flok asking about tenants: %q", f.Error)
	}
	home.send(t, wire.Frame{Type: wire.TypeTenant, ID: "t1", Tenant: &wire.Tenant{Op: "approve", Code: code3, Name: "acme"}})
	f = home.next(t, enrollReply("t1"))
	if f.Error != "" || f.Tenant.Tenant.Name != "acme" || f.Tenant.Pending.Hostname != "lab-box" {
		t.Fatalf("admin approve into a new tenant: %q %+v", f.Error, f.Tenant)
	}
	st, out = api(t, srv, secret3, "POST", "/api/enroll/"+id3+"/accept", nil)
	if st != http.StatusCreated || out["tenant"].(map[string]any)["name"] != "acme" || out["approved_by"] != "admin home" {
		t.Fatalf("lab accepts: %d %v", st, out)
	}
	labKey, _ := out["token"].(string)
	lab := linkRaw(t, srv, labKey, "lab")
	labPhoneTok, _ := pairThrough(t, srv, lab, "p2", "Lab phone") // a phone paired through lab is acme's
	if st, out := api(t, srv, labPhoneTok, "GET", "/api/instances", nil); st != 200 || len(out["instances"].([]any)) != 1 || out["instances"].([]any)[0].(map[string]any)["name"] != "lab" {
		t.Fatalf("acme's phone sees acme alone: %d %v", st, out)
	}
	if st, out := api(t, srv, phoneTok, "GET", "/api/instances", nil); st != 200 || len(out["instances"].([]any)) != 2 {
		t.Fatalf("default's phone sees default alone: %d %v", st, out)
	}
	// devices are the tenant's: acme's phone cannot touch default's phone's device
	if st, _ := api(t, srv, phoneTok, "POST", "/api/devices", map[string]any{"token": "defaultdevice01", "name": "Jaro's iPhone"}); st != 200 {
		t.Fatal("register")
	}
	if st, _ := api(t, srv, labPhoneTok, "PUT", "/api/devices/defaultdevice01/prefs", map[string]any{}); st != http.StatusNotFound {
		t.Fatalf("another tenant's device: %d", st)
	}
	if st, out := api(t, srv, labPhoneTok, "GET", "/api/devices", nil); st != 200 || len(out["devices"].([]any)) != 0 {
		t.Fatalf("acme lists no devices of default: %v", out)
	}
	home.send(t, wire.Frame{Type: wire.TypeTenant, ID: "t2", Tenant: &wire.Tenant{Op: "list"}})
	f = home.next(t, enrollReply("t2"))
	if f.Error != "" || len(f.Tenant.Tenants) != 2 || f.Tenant.Tenants[0].Name != "default" || !f.Tenant.Tenants[0].Static || f.Tenant.Tenants[0].Instances != 2 || f.Tenant.Tenants[0].Phones != 1 ||
		f.Tenant.Tenants[1].Name != "acme" || f.Tenant.Tenants[1].Instances != 1 || f.Tenant.Tenants[1].Online != 1 || f.Tenant.Tenants[1].Phones != 1 {
		t.Fatalf("tenant list: %q %+v", f.Error, f.Tenant.Tenants)
	}
	home.send(t, wire.Frame{Type: wire.TypeTenant, ID: "t3", Tenant: &wire.Tenant{Op: "show", Name: "ACME"}})
	f = home.next(t, enrollReply("t3"))
	if f.Error != "" || len(f.Tenant.Tenant.Floks) != 1 || f.Tenant.Tenant.Floks[0].Name != "lab" || len(f.Tenant.Tenant.PhoneList) != 1 || f.Tenant.Tenant.PhoneList[0].Name != "Lab phone" {
		t.Fatalf("tenant show: %q %+v", f.Error, f.Tenant.Tenant)
	}
	home.send(t, wire.Frame{Type: wire.TypeTenant, ID: "t4", Tenant: &wire.Tenant{Op: "remove", Name: "default"}})
	if f = home.next(t, enrollReply("t4")); !strings.Contains(f.Error, "own tenant stays") {
		t.Fatalf("removing the default: %q", f.Error)
	}

	// --- forget: office's link closes with the reason, the phone drops it, its key is dead ------
	office.send(t, wire.Frame{Type: wire.TypeEnroll, ID: "f0", Enroll: &wire.Enroll{Op: "forget", Name: "home"}})
	if f = office.next(t, enrollReply("f0")); !strings.Contains(f.Error, "token from the relay's config") {
		t.Fatalf("forgetting a config-token flok: %q", f.Error)
	}
	home.send(t, wire.Frame{Type: wire.TypeEnroll, ID: "f1", Enroll: &wire.Enroll{Op: "forget", Name: "Office"}})
	f = home.next(t, enrollReply("f1"))
	if f.Error != "" || f.Enroll.Instance.Name != "office" || len(f.Enroll.Instances) != 1 {
		t.Fatalf("forget: %q %+v", f.Error, f.Enroll)
	}
	if why := closeReasonOf(t, office); why != wire.CloseInstanceForgotten {
		t.Fatalf("office's link closed with %q", why)
	}
	f = app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeInstance && f.Instance == "office" && f.Inst.Gone })
	if why := refusedLink(t, srv, officeKey, "office"); why != "401" {
		t.Fatalf("the key after the forget: %q", why)
	}
	// a replaced key: the same name joins again while offline, and the old record goes
	id4, secret4, code4 := startJoin(t, srv, "lab", "lab-box-2")
	home.send(t, wire.Frame{Type: wire.TypeTenant, ID: "t5", Tenant: &wire.Tenant{Op: "approve", Code: code4, Name: "acme"}})
	if f = home.next(t, enrollReply("t5")); !strings.Contains(f.Error, "lab is online in acme already") {
		t.Fatalf("approving a name online in the tenant: %q", f.Error)
	}
	lab.conn.CloseNow()
	time.Sleep(50 * time.Millisecond)
	home.send(t, wire.Frame{Type: wire.TypeTenant, ID: "t6", Tenant: &wire.Tenant{Op: "approve", Code: code4, Name: "acme"}})
	if f = home.next(t, enrollReply("t6")); f.Error != "" {
		t.Fatalf("approving the name once it is offline: %q", f.Error)
	}
	if st, _ := api(t, srv, secret4, "POST", "/api/enroll/"+id4+"/accept", nil); st != http.StatusCreated {
		t.Fatalf("lab joins again: %d", st)
	}
	if why := refusedLink(t, srv, labKey, "lab"); why != "401" {
		t.Fatalf("the old lab key after the rejoin: %q", why)
	}
	if n := s.istore.count(); n != 1 {
		t.Fatalf("%d records, want the new lab alone", n)
	}

	// --- a restart keeps the tenant and the records ---------------------------------------------
	s2, err := New(Config{InstanceTokens: []string{"inst-tok"}, DataDir: dir, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if s2.tstore.count() != 2 || s2.istore.count() != 1 {
		t.Fatalf("after a restart: %d tenants, %d floks", s2.tstore.count(), s2.istore.count())
	}
	if _, ok := s2.phones.byToken(labPhoneTok); !ok {
		t.Fatal("the acme phone's key after a restart")
	}
	tenants, err := ListTenants(dir)
	if err != nil || len(tenants) != 2 || tenants[1].Name != "acme" || tenants[1].Instances != 1 || tenants[1].Phones != 1 {
		t.Fatalf("ListTenants: %v %+v", err, tenants)
	}

	// --- remove the tenant: its flok's key, its phone's key, its device go -------------------
	home.send(t, wire.Frame{Type: wire.TypeTenant, ID: "t7", Tenant: &wire.Tenant{Op: "remove", Name: "acme"}})
	f = home.next(t, enrollReply("t7"))
	if f.Error != "" || f.Tenant.Tenant.Name != "acme" {
		t.Fatalf("remove: %q", f.Error)
	}
	if st, _ := api(t, srv, labPhoneTok, "GET", "/api/instances", nil); st != http.StatusUnauthorized {
		t.Fatalf("acme's phone after the removal: %d", st)
	}
	if n := s.istore.count(); n != 0 || s.tstore.count() != 1 {
		t.Fatalf("after the removal: %d floks, %d tenants", n, s.tstore.count())
	}
}

// refusedLink dials /link with a token under a name and returns why it was refused: the HTTP
// status for a refused upgrade, the close reason for a refused hello; "" when it was taken.
func refusedLink(t *testing.T, srv *httptest.Server, token, name string) string {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/link"
	conn, resp, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		if resp != nil {
			return strings.Fields(resp.Status)[0]
		}
		return err.Error()
	}
	defer conn.CloseNow()
	in := &rawInstance{conn: conn, got: make(chan wire.Frame, 8)}
	in.send(t, wire.Frame{Type: wire.TypeHello, Hello: &wire.Hello{Proto: wire.Proto, Name: name, Version: "test", Hostname: "box", Features: wire.InstanceFeatures}})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			if ce := (websocket.CloseError{}); errors.As(err, &ce) {
				return ce.Reason
			}
			return ""
		}
	}
}

// closeReasonOf waits for an instance's connection to be closed by the relay and returns the reason.
func closeReasonOf(t *testing.T, in *rawInstance) string {
	t.Helper()
	select {
	case err := <-in.errc:
		if ce := (websocket.CloseError{}); errors.As(err, &ce) {
			return ce.Reason
		}
		return err.Error()
	case <-time.After(3 * time.Second):
		t.Fatal("the link was not closed")
		return ""
	}
}

// Joins expire and can be withdrawn; nobody can start more than a few per address; a relay with
// neither tokens nor joined floks refuses to start, one with admin tokens alone starts.
func TestJoinLimitsAndStart(t *testing.T) {
	if _, err := New(Config{DataDir: t.TempDir()}); err != ErrNothingLinks {
		t.Fatalf("a relay nothing can link in to: %v", err)
	}
	s, err := New(Config{AdminTokens: []string{"a"}, PairTTL: 80 * time.Millisecond, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	id, secret, _ := startJoin(t, srv, "one", "")
	startJoin(t, srv, "two", "")
	startJoin(t, srv, "three", "")
	if st, out := api(t, srv, "", "POST", "/api/enroll", map[string]any{"name": "four"}); st != http.StatusTooManyRequests {
		t.Fatalf("a fourth join from one address: %d %v", st, out)
	}
	if st, out := api(t, srv, "", "POST", "/api/enroll", map[string]any{}); st != http.StatusBadRequest {
		t.Fatalf("a join without a name: %d %v", st, out)
	}
	if st, _ := api(t, srv, secret, "DELETE", "/api/enroll/"+id, nil); st != 200 {
		t.Fatal("withdraw")
	}
	if st, _ := api(t, srv, secret, "GET", "/api/enroll/"+id, nil); st != http.StatusNotFound {
		t.Fatalf("a withdrawn join: %d", st)
	}
	id, secret, _ = startJoin(t, srv, "four", "") // room again
	time.Sleep(100 * time.Millisecond)
	if st, out := api(t, srv, secret, "GET", "/api/enroll/"+id, nil); st != http.StatusNotFound {
		t.Fatalf("an expired join: %d %v", st, out)
	}
}

// The real join client (internal/link) against the relay: start, a pending poll, the approval
// (a config device token approves into the relay's own tenant), accept with the key and pin,
// and the key links.
func TestJoinWithTheRealClient(t *testing.T) {
	JoinPoll = 200 * time.Millisecond
	defer func() { JoinPoll = 20 * time.Second }()
	key, err := tunnel.LoadOrCreate(filepath.Join(t.TempDir(), "k.pem"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{InstanceTokens: []string{"inst-tok"}, DeviceTokens: []string{"dev-tok"}, DataDir: t.TempDir(), Logf: t.Logf, LinkKey: &key})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	j := link.Joiner{Origin: srv.URL}
	ctx := context.Background()
	start, err := j.Start(ctx, "office", "macmini", "v9")
	if err != nil || !codeShape.MatchString(start.Code) {
		t.Fatalf("start: %v %+v", err, start)
	}
	st, err := j.Wait(ctx, start)
	if err != nil || st.Status != "pending" {
		t.Fatalf("wait while pending: %v %+v", err, st)
	}
	if _, err := j.Accept(ctx, start); err == nil || !strings.Contains(err.Error(), "not approved yet") {
		t.Fatalf("accept before the approval: %v", err)
	}
	if code, out := api(t, srv, "dev-tok", "POST", "/api/enroll/approve", map[string]any{"code": start.Code}); code != 200 {
		t.Fatalf("approve: %d %v", code, out)
	}
	st, err = j.Wait(ctx, start)
	if err != nil || st.Status != "approved" || st.Tenant.Name != "default" || st.ApprovedBy != "a device token" {
		t.Fatalf("wait after the approval: %v %+v", err, st)
	}
	joined, err := j.Accept(ctx, start)
	if err != nil || len(joined.Token) != 48 || joined.Tenant.Name != "default" || joined.Instance.Name != "office" || !strings.HasPrefix(joined.Pin, "sha256:") {
		t.Fatalf("accept: %v %+v", err, joined)
	}
	if _, err := j.Wait(ctx, start); !errors.Is(err, link.ErrJoinGone) {
		t.Fatalf("after the accept the join is gone: %v", err)
	}
	in := linkRaw(t, srv, joined.Token, "office")
	in.send(t, wire.Frame{Type: wire.TypeEnroll, ID: "l1", Enroll: &wire.Enroll{Op: "list"}})
	f := in.next(t, enrollReply("l1"))
	if f.Error != "" || len(f.Enroll.Instances) != 1 || f.Enroll.Instances[0].ApprovedBy != "a device token" {
		t.Fatalf("the joined flok lists itself: %q %+v", f.Error, f.Enroll)
	}
	// an older relay has no join route: the client says so
	old := httptest.NewServer(http.NotFoundHandler())
	defer old.Close()
	if _, err := (link.Joiner{Origin: old.URL}).Start(ctx, "x", "", ""); err == nil || !strings.Contains(err.Error(), "predates joining by code") {
		t.Fatalf("an old relay: %v", err)
	}
}
