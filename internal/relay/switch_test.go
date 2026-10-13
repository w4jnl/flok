package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/wire"
)

// waitInstance polls /api/instances until the only instance satisfies ok, and returns it.
func waitInstance(t *testing.T, srv *httptest.Server, what string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, out := api(t, srv, "dev-tok", "GET", "/api/instances", nil)
		if insts, _ := out["instances"].([]any); len(insts) == 1 {
			if in, _ := insts[0].(map[string]any); in != nil && ok(in) {
				return in
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the relay never had the instance %s: %v", what, out)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Switched off (flok link off), the link says bye before it closes, plain or encrypted: the
// phone sees the instance as off rather than offline, its waiting agent leaves the badge and the
// Lock Screen, an answer is refused with the reason, and what the sidebar announces while off
// never arrives. Switched on again, it is back at once.
func TestLinkSwitchedOff(t *testing.T) {
	key, pin := linkKey(t)
	for _, encrypted := range []bool{false, true} {
		push := &fakePush{}
		s, err := New(Config{InstanceTokens: []string{"inst-tok"}, DeviceTokens: []string{"dev-tok"}, LinkKey: key, Push: push,
			SyncDelay: 20 * time.Millisecond, AnswerWait: time.Second, Logf: t.Logf})
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(s.Handler())
		_ = s.devices.add(device{Token: "phone1", Name: "phone", Added: time.Now()})
		cfg := link.Config{URL: srv.URL, Token: "inst-tok", Name: "office", Version: "test"}
		if encrypted {
			cfg.Pin = pin
		}
		cl, states, _ := startLink(t, cfg) // its snapshot: secret-agent blocked, unseen
		waitState(t, states, link.Connected)
		waitOnline(t, srv, true)

		cl.SetOff(true)
		waitState(t, states, link.Off)
		waitInstance(t, srv, "off", func(in map[string]any) bool { return in["online"] == false && in["off"] == true })
		var synced bool
		for deadline := time.Now().Add(2 * time.Second); !synced && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			push.mu.Lock()
			for _, n := range push.sent {
				if n.Background && n.Data["badge"] == 0 && len(n.Data["waiting"].([]string)) == 0 {
					synced = true
				}
			}
			push.mu.Unlock()
		}
		if !synced {
			t.Fatalf("encrypted=%v: no sync push clearing the switched-off instance's alerts: %+v", encrypted, push.sent)
		}
		cl.Event(wire.Event{Pane: "%1", Agent: "secret-agent", Kind: "done", At: time.Now()}) // dropped: it would arrive stale
		if code, out := api(t, srv, "dev-tok", "POST", "/api/answer", map[string]any{"instance": "office", "pane": "%1", "text": "y"}); code != http.StatusBadGateway ||
			!strings.Contains(stringOf(out["error"]), "switched off") {
			t.Errorf("encrypted=%v: answer to a switched-off instance: %d %v", encrypted, code, out)
		}

		cl.SetOff(false)
		waitState(t, states, link.Connected)
		waitInstance(t, srv, "back on", func(in map[string]any) bool { return in["online"] == true && in["off"] == nil })
		time.Sleep(300 * time.Millisecond) // past the link's hold: an event kept while off would be out by now
		push.mu.Lock()
		for _, n := range push.sent {
			if n.Title != "" {
				t.Errorf("encrypted=%v: a push for what happened while off: %+v", encrypted, n)
			}
		}
		push.mu.Unlock()
		cl.Close()
		srv.Close()
	}
}
