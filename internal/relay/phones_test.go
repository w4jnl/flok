package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/wire"
)

// rawInstance is a test instance on /link speaking frames by hand (the real client learns the
// phone frame in internal/link; the relay's side is pinned here without it).
type rawInstance struct {
	conn *websocket.Conn
	got  chan wire.Frame
	errc chan error // why the reader stopped, once it did
}

func linkRaw(t *testing.T, srv *httptest.Server, token, name string) *rawInstance {
	return linkRawAdmin(t, srv, token, "", name)
}

// linkRawAdmin is linkRaw with an admin token presented besides the instance's own ([link] admin).
func linkRawAdmin(t *testing.T, srv *httptest.Server, token, admin, name string) *rawInstance {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/link"
	hdr := http.Header{}
	if token != "" {
		hdr.Set("Authorization", "Bearer "+token)
	}
	if admin != "" {
		hdr.Set(link.AdminHeader, admin)
	}
	conn, _, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	in := &rawInstance{conn: conn, got: make(chan wire.Frame, 64), errc: make(chan error, 1)}
	in.send(t, wire.Frame{Type: wire.TypeHello, Hello: &wire.Hello{Proto: wire.Proto, Name: name, Version: "test", Hostname: "box", Features: wire.InstanceFeatures}})
	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				in.errc <- err
				return
			}
			var f wire.Frame
			if json.Unmarshal(data, &f) == nil {
				in.got <- f
			}
		}
	}()
	return in
}

func (in *rawInstance) send(t *testing.T, f wire.Frame) {
	t.Helper()
	data, _ := json.Marshal(f)
	if err := in.conn.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func (in *rawInstance) next(t *testing.T, want func(wire.Frame) bool) wire.Frame {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f := <-in.got:
			if want(f) {
				return f
			}
		case <-deadline:
			t.Fatal("the instance got nothing of the kind")
		}
	}
}

// phoneReply is the relay's answer to a phone frame under a request id.
func phoneReply(id string) func(wire.Frame) bool {
	return func(f wire.Frame) bool { return f.Type == wire.TypePhone && f.ID == id && f.Phone != nil }
}

var codeShape = regexp.MustCompile(`^[A-Z2-9]{4}-[A-Z2-9]{4}$`)

// pairThrough mints a code on the instance under the id and redeems it as a phone named name,
// returning the phone's token and id.
func pairThrough(t *testing.T, srv *httptest.Server, in *rawInstance, id, name string) (token, phoneID string) {
	t.Helper()
	in.send(t, wire.Frame{Type: wire.TypePhone, ID: id, Phone: &wire.Phone{Op: "pair"}})
	f := in.next(t, phoneReply(id))
	if f.Phone.Op != "pair" || !codeShape.MatchString(f.Phone.Code) || f.Error != "" {
		t.Fatalf("pair reply: %+v %q", f.Phone, f.Error)
	}
	code, out := api(t, srv, "", "POST", "/api/pair", map[string]any{"code": f.Phone.Code, "name": name})
	if code != http.StatusCreated {
		t.Fatalf("redeem: %d %v", code, out)
	}
	token, _ = out["token"].(string)
	ph, _ := out["phone"].(map[string]any)
	phoneID, _ = ph["id"].(string)
	if len(token) != 48 || phoneID == "" || ph["name"] != name {
		t.Fatalf("redeem gave %v", out)
	}
	return token, phoneID
}

// A relay without device tokens: the instance asks for a code, the phone redeems it once for a
// key of its own, the terminal hears that it paired, the key opens the API and the stream, the
// phone's device is known as its, and forgetting it closes the stream, drops the device and
// revokes the key.
func TestPairingEndToEnd(t *testing.T) {
	s, err := New(Config{InstanceTokens: []string{"inst-tok"}, DataDir: t.TempDir(), Logf: t.Logf, Version: "v9"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	in := linkRaw(t, srv, "inst-tok", "home")

	in.send(t, wire.Frame{Type: wire.TypePhone, ID: "r1", Phone: &wire.Phone{Op: "pair"}})
	f := in.next(t, phoneReply("r1"))
	if f.Phone.Op != "pair" || !codeShape.MatchString(f.Phone.Code) {
		t.Fatalf("pair reply: %+v", f.Phone)
	}
	if left := time.Until(f.Phone.ExpiresAt); left < 4*time.Minute || left > 5*time.Minute+time.Second {
		t.Fatalf("expires in %s, not about five minutes", left)
	}
	code, out := api(t, srv, "", "POST", "/api/pair", map[string]any{"code": "AAAA-AAAA", "name": "x"})
	if code != http.StatusBadRequest || out["error"] != "that code is not valid any more: run flok phone pair again" {
		t.Fatalf("a wrong code: %d %v", code, out)
	}
	if code, out := api(t, srv, "", "POST", "/api/pair", map[string]any{"code": "abc"}); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "eight letters") {
		t.Fatalf("a short code: %d %v", code, out)
	}
	typed := strings.ToLower(strings.ReplaceAll(f.Phone.Code, "-", " ")) // as a thumb types it
	code, out = api(t, srv, "", "POST", "/api/pair", map[string]any{"code": typed, "name": "  Jaro's\tiPhone  "})
	if code != http.StatusCreated {
		t.Fatalf("redeem: %d %v", code, out)
	}
	token, _ := out["token"].(string)
	ph, _ := out["phone"].(map[string]any)
	id, _ := ph["id"].(string)
	if len(token) != 48 || id == "" || ph["name"] != "Jaro's iPhone" || out["relay"].(map[string]any)["version"] != "v9" {
		t.Fatalf("redeem gave %v", out)
	}
	paired := in.next(t, phoneReply("r1"))
	if paired.Phone.Op != "paired" || paired.Phone.Phone == nil || paired.Phone.Phone.ID != id || paired.Phone.Phone.Name != "Jaro's iPhone" || paired.Phone.Phone.Instance != "home" {
		t.Fatalf("the terminal was not told it paired: %+v", paired.Phone)
	}
	if code, _ := api(t, srv, "", "POST", "/api/pair", map[string]any{"code": f.Phone.Code}); code != http.StatusBadRequest {
		t.Fatalf("a code is good once: %d", code)
	}

	if code, _ := api(t, srv, token, "GET", "/api/instances", nil); code != http.StatusOK {
		t.Fatalf("the key on the API: %d", code)
	}
	if code, out := api(t, srv, "nope", "GET", "/api/instances", nil); code != http.StatusUnauthorized || !strings.Contains(out["error"].(string), "flok phone pair") {
		t.Fatalf("a wrong key: %d %v", code, out)
	}
	app := dialApp(t, srv, token)
	app.next(t, func(f wire.Frame) bool { return f.Type == wire.TypeInstances })
	if code, _ := api(t, srv, token, "POST", "/api/devices", map[string]any{"token": "apns0001", "name": "Jaro's iPhone"}); code != http.StatusOK {
		t.Fatalf("register: %d", code)
	}
	_, out = api(t, srv, token, "GET", "/api/devices", nil)
	devs, _ := out["devices"].([]any)
	if len(devs) != 1 || devs[0].(map[string]any)["phone"] != id {
		t.Fatalf("the device is not the phone's: %v", out)
	}

	in.send(t, wire.Frame{Type: wire.TypePhone, ID: "r2", Phone: &wire.Phone{Op: "list"}})
	f = in.next(t, phoneReply("r2"))
	if len(f.Phone.Phones) != 1 || f.Phone.Phones[0].ID != id || f.Phone.Phones[0].LastSeen.IsZero() || f.Phone.Phones[0].Instance != "home" {
		t.Fatalf("list: %+v", f.Phone.Phones)
	}
	in.send(t, wire.Frame{Type: wire.TypePhone, ID: "r3", Phone: &wire.Phone{Op: "forget", Name: "nobody"}})
	if f = in.next(t, phoneReply("r3")); !strings.HasPrefix(f.Error, "no phone nobody") {
		t.Fatalf("forget nobody: %q", f.Error)
	}
	in.send(t, wire.Frame{Type: wire.TypePhone, ID: "r4", Phone: &wire.Phone{Op: "forget", Name: "jaro's iphone"}})
	f = in.next(t, phoneReply("r4"))
	if f.Error != "" || f.Phone.Phone == nil || f.Phone.Phone.ID != id || len(f.Phone.Phones) != 0 {
		t.Fatalf("forget: %+v %q", f.Phone, f.Error)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		app.mu.Lock()
		err := app.err
		app.mu.Unlock()
		if err != nil {
			var ce websocket.CloseError
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation || !errors.As(err, &ce) || ce.Reason != wire.CloseForgotten {
				t.Fatalf("the stream ended with %v, not the forgotten close", err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the phone's stream stayed open")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code, _ := api(t, srv, token, "GET", "/api/instances", nil); code != http.StatusUnauthorized {
		t.Fatalf("the key after forget: %d", code)
	}
	if list := s.devices.list(); len(list) != 0 {
		t.Fatalf("the device stayed: %+v", list)
	}
}

// The same push token registered under a new pairing is the same phone paired again: the old
// pairing goes with no device left to it.
func TestPairAgainMovesTheDevice(t *testing.T) {
	s, _ := New(Config{InstanceTokens: []string{"inst-tok"}, DataDir: t.TempDir(), Logf: t.Logf})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	in := linkRaw(t, srv, "inst-tok", "home")
	tokA, idA := pairThrough(t, srv, in, "a", "iPhone")
	if code, _ := api(t, srv, tokA, "POST", "/api/devices", map[string]any{"token": "apns0001", "name": "iPhone"}); code != http.StatusOK {
		t.Fatalf("register A: %d", code)
	}
	tokB, idB := pairThrough(t, srv, in, "b", "iPhone")
	if list := s.phones.list(); len(list) != 2 {
		t.Fatalf("before B registers both pairings stand: %+v", list)
	}
	if code, _ := api(t, srv, tokB, "POST", "/api/devices", map[string]any{"token": "apns0001", "name": "iPhone"}); code != http.StatusOK {
		t.Fatalf("register B: %d", code)
	}
	if list := s.phones.list(); len(list) != 1 || list[0].ID != idB {
		t.Fatalf("after B registers only B stands: %+v (A was %s)", list, idA)
	}
	if code, _ := api(t, srv, tokA, "GET", "/api/instances", nil); code != http.StatusUnauthorized {
		t.Fatalf("A's key still works: %d", code)
	}
	d, _ := s.devices.get("apns0001")
	if d.Phone != idB {
		t.Fatalf("the device belongs to %q, not B", d.Phone)
	}
	// a phone forgotten by name when two share it needs the id
	tokC, idC := pairThrough(t, srv, in, "c", "iPhone")
	in.send(t, wire.Frame{Type: wire.TypePhone, ID: "f1", Phone: &wire.Phone{Op: "forget", Name: "iPhone"}})
	if f := in.next(t, phoneReply("f1")); !strings.Contains(f.Error, "two of one name") {
		t.Fatalf("forget an ambiguous name: %q", f.Error)
	}
	in.send(t, wire.Frame{Type: wire.TypePhone, ID: "f2", Phone: &wire.Phone{Op: "forget", Name: idC}})
	if f := in.next(t, phoneReply("f2")); f.Error != "" || len(f.Phone.Phones) != 1 || f.Phone.Phones[0].ID != idB {
		t.Fatalf("forget by id: %+v %q", f.Phone, f.Error)
	}
	if code, _ := api(t, srv, tokC, "GET", "/api/instances", nil); code != http.StatusUnauthorized {
		t.Fatalf("C's key after forget: %d", code)
	}
}

func TestPairCodeExpiryAndReplacement(t *testing.T) {
	s, _ := New(Config{InstanceTokens: []string{"inst-tok"}, PairTTL: 60 * time.Millisecond, Logf: t.Logf})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	in := linkRaw(t, srv, "inst-tok", "home")
	in.send(t, wire.Frame{Type: wire.TypePhone, ID: "r1", Phone: &wire.Phone{Op: "pair"}})
	old := in.next(t, phoneReply("r1")).Phone.Code
	time.Sleep(100 * time.Millisecond)
	if code, _ := api(t, srv, "", "POST", "/api/pair", map[string]any{"code": old}); code != http.StatusBadRequest {
		t.Fatalf("an expired code: %d", code)
	}
	in.send(t, wire.Frame{Type: wire.TypePhone, ID: "r2", Phone: &wire.Phone{Op: "pair"}})
	first := in.next(t, phoneReply("r2")).Phone.Code
	in.send(t, wire.Frame{Type: wire.TypePhone, ID: "r3", Phone: &wire.Phone{Op: "pair"}})
	second := in.next(t, phoneReply("r3")).Phone.Code
	if code, _ := api(t, srv, "", "POST", "/api/pair", map[string]any{"code": first}); code != http.StatusBadRequest {
		t.Fatalf("a code a newer one replaced: %d", code)
	}
	if code, _ := api(t, srv, "", "POST", "/api/pair", map[string]any{"code": second, "name": "p"}); code != http.StatusCreated {
		t.Fatalf("the live code: %d", code)
	}
	if paired := in.next(t, phoneReply("r3")); paired.Phone.Op != "paired" {
		t.Fatalf("paired under the live code's request: %+v", paired.Phone)
	}
}

func TestPairThrottle(t *testing.T) {
	s, _ := New(Config{InstanceTokens: []string{"inst-tok"}, Logf: t.Logf})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	for i := 1; i <= 6; i++ {
		if code, _ := api(t, srv, "", "POST", "/api/pair", map[string]any{"code": "AAAA-AAAA"}); code != http.StatusBadRequest {
			t.Fatalf("wrong code %d: %d", i, code)
		}
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/pair", strings.NewReader(`{"code":"AAAA-AAAA"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "30" {
		t.Fatalf("the seventh: %d Retry-After %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}

	l := newIPLimiter(5, 30*time.Second, 15*time.Minute)
	now := time.Unix(1_000_000, 0)
	for i := 0; i < 5; i++ {
		l.failed("a", now)
	}
	if ok, _ := l.allow("a", now); !ok {
		t.Fatal("five failures are free")
	}
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for i, w := range want {
		l.failed("a", now)
		ok, retry := l.allow("a", now)
		if ok || retry != w {
			t.Fatalf("lock %d: %v %s, want %s", i, ok, retry, w)
		}
		if ok, _ := l.allow("b", now); !ok {
			t.Fatal("another address is free")
		}
		now = now.Add(w)
		if ok, _ := l.allow("a", now); !ok {
			t.Fatalf("lock %d did not lift", i)
		}
	}
	l.succeeded("a")
	l.failed("a", now)
	if ok, _ := l.allow("a", now); !ok {
		t.Fatal("a right code resets the count")
	}
}

func TestDeviceTokensStayOptional(t *testing.T) {
	s, _ := New(Config{InstanceTokens: []string{"inst-tok"}, DeviceTokens: []string{"dev-tok"}, Logf: t.Logf})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	if code, _ := api(t, srv, "dev-tok", "GET", "/api/instances", nil); code != http.StatusOK {
		t.Fatalf("a static token: %d", code)
	}
	if code, _ := api(t, srv, "dev-tok", "POST", "/api/devices", map[string]any{"token": "apns0001"}); code != http.StatusOK {
		t.Fatal("register with a static token")
	}
	if d, _ := s.devices.get("apns0001"); d.Phone != "" {
		t.Fatalf("a static token's device has no phone, got %q", d.Phone)
	}
	s2, _ := New(Config{InstanceTokens: []string{"inst-tok"}, Logf: t.Logf})
	srv2 := httptest.NewServer(s2.Handler())
	defer srv2.Close()
	if code, _ := api(t, srv2, NewToken(), "GET", "/api/instances", nil); code != http.StatusUnauthorized {
		t.Fatalf("no tokens at all: %d", code)
	}
}

func TestPhonesSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(Config{InstanceTokens: []string{"inst-tok"}, DataDir: dir, Logf: t.Logf})
	srv := httptest.NewServer(s.Handler())
	in := linkRaw(t, srv, "inst-tok", "home")
	tok, id := pairThrough(t, srv, in, "r", "iPhone")
	srv.Close()
	s2, err := New(Config{InstanceTokens: []string{"inst-tok"}, DataDir: dir, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	srv2 := httptest.NewServer(s2.Handler())
	defer srv2.Close()
	if code, _ := api(t, srv2, tok, "GET", "/api/instances", nil); code != http.StatusOK {
		t.Fatalf("the key after a restart: %d", code)
	}
	list, err := ListPhones(dir)
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].Name != "iPhone" {
		t.Fatalf("ListPhones: %+v %v", list, err)
	}
	if s2.PhoneCount() != 1 {
		t.Fatal("count")
	}
}

func TestPairCodeWords(t *testing.T) {
	for in, want := range map[string]string{"ab7q-k2mn": "AB7QK2MN", " AB7Q K2MN ": "AB7QK2MN", "AB7Q–K2MN": "AB7QK2MN", "": ""} {
		if got := NormalizeCode(in); got != want {
			t.Errorf("NormalizeCode(%q) = %q", in, got)
		}
	}
	if FormatCode("AB7QK2MN") != "AB7Q-K2MN" || FormatCode("short") != "short" {
		t.Fatal("FormatCode")
	}
	for i := 0; i < 200; i++ {
		c := newCode()
		if len(c) != PairCodeLen || strings.ContainsAny(c, "01OI") || !codeShape.MatchString(FormatCode(c)) {
			t.Fatalf("code %q", c)
		}
	}
	if newCode() == newCode() {
		t.Fatal("codes repeat")
	}
	long := strings.Repeat("é", 70)
	if phoneName("") != "phone" || phoneName(" a \n b ") != "a b" || len([]rune(phoneName(long))) != 64 {
		t.Fatal("phoneName")
	}
	r, _ := http.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.9:4000"
	if clientIP(r) != "10.0.0.9" {
		t.Fatal("remote addr")
	}
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if clientIP(r) != "5.6.7.8" {
		t.Fatal("the last forwarded entry")
	}
	r.Header.Set("X-Real-IP", "9.9.9.9")
	if clientIP(r) != "9.9.9.9" {
		t.Fatal("X-Real-IP wins")
	}
}
