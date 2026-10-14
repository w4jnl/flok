package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/snapshot"
)

// The relay deflates every frame stream whose other end offers it: an instance's plain link
// (permessage-deflate), its encrypted one (flok-link/2 inside the TLS) and an app's /api/ws. A
// run of snapshots that each change one agent's tool then costs a small part of its JSON on the
// wire. An end that offers nothing, an older flok or app, gets the frames as they were.
func TestFramesTravelDeflated(t *testing.T) {
	key, pin := linkKey(t)
	var mu sync.Mutex
	var logged strings.Builder
	s, err := New(Config{InstanceTokens: []string{"inst-tok"}, DeviceTokens: []string{"dev-tok"}, LinkKey: key,
		Logf: func(format string, a ...any) {
			mu.Lock()
			fmt.Fprintf(&logged, format+"\n", a...)
			mu.Unlock()
		}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	log := func() string {
		mu.Lock()
		defer mu.Unlock()
		return logged.String()
	}

	deflating, resp := dialAppWith(t, srv, "dev-tok", websocket.CompressionContextTakeover)
	if !deflated(resp.Header) {
		t.Fatal("the relay did not deflate for an app that offers it")
	}
	plainApp, resp := dialAppWith(t, srv, "dev-tok", websocket.CompressionDisabled)
	if deflated(resp.Header) {
		t.Fatal("the relay deflates for an app that does not offer it")
	}
	apps := []*wsApp{deflating, plainApp}

	for _, tc := range []struct{ name, pin, how string }{
		{"plain", "", ", compressed)"},
		{"encrypted", pin, ", encrypted, compressed)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTap(t, strings.TrimPrefix(srv.URL, "http://"))
			cl, states, _ := startLink(t, link.Config{URL: tp.url(), Token: "inst-tok", Pin: tc.pin, Name: "office", Version: "test"})
			if st := waitState(t, states, link.Connected); !st.Compressed {
				t.Fatal("the link does not say it is compressed")
			}
			waitOnline(t, srv, true)
			before, raw := tp.n(), 0
			for i := 0; i < 30; i++ {
				snap := officeSnap(i)
				b, _ := json.Marshal(snap)
				raw += len(b)
				cl.Publish(snap)
				for _, a := range apps { // both get every snapshot whole, deflated or not
					a.next(t, func(f wire.Frame) bool {
						return f.Type == wire.TypeInstance && f.Inst != nil && f.Inst.Snap != nil && len(f.Inst.Snap.Agents) == 10 &&
							f.Inst.Snap.Agents[i%10].Tool == fmt.Sprintf("tool-%d", i)
					})
				}
			}
			n := tp.n() - before
			t.Logf("30 snapshots, %d bytes of JSON, crossed in %d", raw, n)
			if n > raw/4 {
				t.Errorf("30 snapshots of %d bytes of JSON crossed in %d", raw, n)
			}
			if l := log(); !strings.Contains(l, tc.how) {
				t.Errorf("the relay did not log the link as%s:\n%s", strings.TrimSuffix(tc.how, ")"), l)
			}
			cl.Close()
			waitOnline(t, srv, false)
		})
	}

	// an older flok offers nothing: its link works as it did
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/link",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer inst-tok"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if deflated(resp.Header) {
		t.Fatal("the relay deflates for a link that does not offer it")
	}
	for _, f := range []wire.Frame{
		{Type: wire.TypeHello, Hello: &wire.Hello{Proto: wire.Proto, Name: "old", Version: "0.7.1"}},
		{Type: wire.TypeSnap, Snap: &snapshot.Snapshot{Agents: []snapshot.Agent{{PaneID: "%1", Name: "old-agent", State: agent.Idle}}}},
	} {
		data, _ := json.Marshal(f)
		if err := c.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatal(err)
		}
	}
	deflating.next(t, func(f wire.Frame) bool {
		return f.Type == wire.TypeInstance && f.Inst != nil && f.Inst.Name == "old" && f.Inst.Snap != nil && f.Inst.Snap.Agents[0].Name == "old-agent"
	})
	if !regexp.MustCompile(`instance old online \(flok 0\.7\.1 on , [^,]*\)`).MatchString(log()) {
		t.Errorf("the older link is logged as compressed, or not at all:\n%s", log())
	}
}

// officeSnap is a site of ten agents in which agent i%10 just started tool-i.
func officeSnap(i int) snapshot.Snapshot {
	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	s := snapshot.Snapshot{Unseen: 2}
	for k := 0; k < 10; k++ {
		s.Agents = append(s.Agents, snapshot.Agent{PaneID: fmt.Sprintf("%%%d", 30+k), SessionID: fmt.Sprintf("$%d", k),
			SessionName: fmt.Sprintf("project-%d", k), WindowID: fmt.Sprintf("@%d", 40+k), Name: fmt.Sprintf("agent-%d", k),
			Kind: "claude", State: agent.Working, Tool: "Read", StateSince: at.Add(time.Duration(k) * time.Minute), HasHooks: true})
		s.Sessions = append(s.Sessions, snapshot.Session{ID: fmt.Sprintf("$%d", k), Name: fmt.Sprintf("project-%d", k), Rollup: agent.Working, Agents: 1})
	}
	s.Agents[i%10].Tool = fmt.Sprintf("tool-%d", i)
	return s
}

func (tp *tap) n() int {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.buf.Len()
}
