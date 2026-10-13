package relay

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/tunnel"
	"github.com/w4jnl/flok/internal/snapshot"
)

// tap sits between the link and the relay and keeps every byte that passes, both ways, the way
// a proxy that inspects HTTPS sees the connection after it has decrypted the outer TLS.
type tap struct {
	ln  net.Listener
	mu  sync.Mutex
	buf bytes.Buffer
}

func newTap(t *testing.T, upstream string) *tap {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tp := &tap{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			u, err := net.Dial("tcp", upstream)
			if err != nil {
				c.Close()
				continue
			}
			pipe := func(dst, src net.Conn) {
				_, _ = io.Copy(io.MultiWriter(dst, tp), src)
				dst.Close()
			}
			go pipe(u, c)
			go pipe(c, u)
		}
	}()
	return tp
}

func (tp *tap) Write(p []byte) (int, error) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.buf.Write(p)
}

func (tp *tap) saw(s string) bool {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return bytes.Contains(tp.buf.Bytes(), []byte(s))
}

func (tp *tap) url() string { return "http://" + tp.ln.Addr().String() }

func linkKey(t *testing.T) (*tls.Certificate, string) {
	t.Helper()
	k, err := tunnel.LoadOrCreate(filepath.Join(t.TempDir(), "link-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pin, _ := tunnel.PinOf(k)
	return &k, pin
}

// startLink runs a link and returns it with a channel of its states and one of its commands.
func startLink(t *testing.T, cfg link.Config) (*link.Client, chan link.Status, chan link.Command) {
	t.Helper()
	cl := link.New(cfg)
	states := make(chan link.Status, 64)
	cmds := make(chan link.Command, 16)
	go func() {
		for m := range cl.Messages() {
			if m.Status != nil {
				states <- *m.Status
			}
			if m.Command != nil {
				cmds <- *m.Command
			}
		}
	}()
	cl.Publish(snapshot.Snapshot{Agents: []snapshot.Agent{{PaneID: "%1", SessionName: "secret-session", Name: "secret-agent", State: agent.Blocked, Reason: "permission:Bash", Unseen: 1}}})
	cl.Start()
	t.Cleanup(cl.Close)
	return cl, states, cmds
}

func waitState(t *testing.T, states chan link.Status, want link.State) link.Status {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case st := <-states:
			if st.State == want {
				return st
			}
		case <-deadline:
			t.Fatalf("link never got %s", want)
		}
	}
}

// waitOnline waits until the relay has the instance office online (the link says connected once
// its hello is out, before the relay has taken it) or, with want false, offline again.
func waitOnline(t *testing.T, srv *httptest.Server, want bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, out := api(t, srv, "dev-tok", "GET", "/api/instances", nil)
		insts, _ := out["instances"].([]any)
		online := len(insts) == 1 && strings.Contains(stringOf(insts[0]), `"online":true`)
		if online == want && (!want || strings.Contains(stringOf(insts[0]), "secret-agent")) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the relay never had office online=%v: %v", want, out)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// answerThrough answers the agent from the app's side and waits for the link to get it.
func answerThrough(t *testing.T, srv *httptest.Server, cl *link.Client, cmds chan link.Command, text string) {
	t.Helper()
	go func() {
		c := <-cmds
		if c.Kind != "answer" || c.Answer.Text != text {
			t.Errorf("command: %+v", c)
		}
		cl.Ack(c.ID, "")
	}()
	if code, out := api(t, srv, "dev-tok", "POST", "/api/answer", map[string]any{"instance": "office", "pane": "%1", "text": text}); code != 200 {
		t.Fatalf("answer: %d %v", code, out)
	}
}

// The encrypted link carries what the plain one does, and a proxy in the middle that reads the
// plain one (the token, the answers, the agents' names) reads nothing of it.
func TestEncryptedLinkHidesEverythingFromTheProxy(t *testing.T) {
	key, pin := linkKey(t)
	s, err := New(Config{InstanceTokens: []string{"inst-secret-token"}, DeviceTokens: []string{"dev-tok"}, LinkKey: key, AnswerWait: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	upstream := strings.TrimPrefix(srv.URL, "http://")

	// plain first, to show the tap reads what an inspecting proxy reads
	plain := newTap(t, upstream)
	cl, states, cmds := startLink(t, link.Config{URL: plain.url(), Token: "inst-secret-token", Name: "office", Version: "test"})
	if st := waitState(t, states, link.Connected); st.Encrypted {
		t.Fatal("a link without a pin says it is encrypted")
	}
	waitOnline(t, srv, true)
	answerThrough(t, srv, cl, cmds, "plain-reply-text")
	if !plain.saw("inst-secret-token") || !plain.saw("plain-reply-text") {
		t.Fatal("the tap does not see the plain link: the test proves nothing")
	}
	cl.Close()
	waitOnline(t, srv, false)

	// encrypted: the same work, and none of it on the wire
	tp := newTap(t, upstream)
	cl, states, cmds = startLink(t, link.Config{URL: tp.url(), Token: "inst-secret-token", Pin: pin, Name: "office", Version: "test"})
	if st := waitState(t, states, link.Connected); !st.Encrypted {
		t.Fatal("the pinned link does not say it is encrypted")
	}
	waitOnline(t, srv, true) // offline in between, so this is the encrypted link's snapshot
	answerThrough(t, srv, cl, cmds, "encrypted-reply-text")
	for _, secret := range []string{"inst-secret-token", "encrypted-reply-text", "secret-agent", "secret-session", "permission:Bash", "office"} {
		if tp.saw(secret) {
			t.Errorf("the proxy read %q on the encrypted link", secret)
		}
	}
	if !tp.saw("GET /link/tls") {
		t.Error("the encrypted link did not dial /link/tls")
	}
}

func TestEncryptedLinkRefusals(t *testing.T) {
	key, pin := linkKey(t)
	_, otherPin := linkKey(t) // what a proxy that answers with its own key would present
	s, err := New(Config{InstanceTokens: []string{"inst-tok"}, DeviceTokens: []string{"dev-tok"}, LinkKey: key, TunnelOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	for _, c := range []struct {
		name   string
		cfg    link.Config
		detail string
	}{
		{"another key", link.Config{Token: "inst-tok", Pin: otherPin}, "does not match [link] pin (it is " + pin + ")"},
		{"a wrong token", link.Config{Token: "nope", Pin: pin}, "refused the token"},
		{"not a pin", link.Config{Token: "inst-tok", Pin: "abc"}, "is not a pin"},
		{"plain to an encrypted-only relay", link.Config{Token: "inst-tok"}, "403"},
	} {
		c.cfg.URL, c.cfg.Name, c.cfg.Version = srv.URL, "office", "test"
		_, states, _ := startLink(t, c.cfg)
		if st := waitState(t, states, link.Refused); !strings.Contains(st.Detail, c.detail) {
			t.Errorf("%s: refused with %q, want %q in it", c.name, st.Detail, c.detail)
		}
	}
	_, states, _ := startLink(t, link.Config{URL: srv.URL, Token: "inst-tok", Pin: pin, Name: "office", Version: "test"})
	waitState(t, states, link.Connected)

	// a relay without a key (no data dir) has no encrypted link
	bare, _ := New(Config{InstanceTokens: []string{"inst-tok"}, DeviceTokens: []string{"dev-tok"}})
	bsrv := httptest.NewServer(bare.Handler())
	defer bsrv.Close()
	_, states, _ = startLink(t, link.Config{URL: bsrv.URL, Token: "inst-tok", Pin: pin, Name: "office", Version: "test"})
	if st := waitState(t, states, link.Refused); !strings.Contains(st.Detail, "no encrypted link") {
		t.Errorf("relay without a key: %q", st.Detail)
	}
}

func stringOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
