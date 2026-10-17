package relay

import (
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/relay/apns"
	"github.com/w4jnl/flok/internal/snapshot"
)

// A finished agent lights up the overview a phone shows, with its sound, instead of a second
// thing on the Lock Screen; a badge-only push keeps the badge. Prompts stay notifications, and
// so does a finish when the phone asked for that or shows no overview.
func TestFinishedLightsUpTheIsland(t *testing.T) {
	push := &fakePush{}
	s, err := New(Config{InstanceTokens: []string{"i"}, DeviceTokens: []string{"d"}, Push: push, LiveDelay: 10 * time.Millisecond,
		SyncDelay: time.Hour, QuietEnd: time.Hour, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	in := place(s, &instance{name: "home", subs: map[string]int{}, gen: 1, online: true})
	_ = s.devices.add(device{Token: "phone1", Name: "phone", Added: time.Now()})
	if _, err := s.devices.setActivities("phone1", "", []Activity{{ID: "A", Token: "ovtok", Kind: liveOverview}}); err != nil {
		t.Fatal(err)
	}
	done := snapshot.Agent{PaneID: "%1", SessionName: "Hugo", Name: "api", State: agent.Done, Unseen: 1, StateSince: time.Now()}
	s.setSnap(in, 1, &snapshot.Snapshot{Unseen: 1, Agents: []snapshot.Agent{done}})

	type sent struct {
		to string
		n  apns.Notification
	}
	settle := func() []sent {
		time.Sleep(120 * time.Millisecond)
		push.mu.Lock()
		defer push.mu.Unlock()
		var out []sent
		for i, n := range push.sent {
			out = append(out, sent{push.to[i].Token, n})
		}
		push.sent, push.to = nil, nil
		return out
	}
	settle() // the overview's first update, for the snapshot

	s.event(in, 1, wire.Event{Pane: "%1", Agent: "api", Kind: "done", At: time.Now()})
	got := settle()
	var island, badge, other []sent
	for _, p := range got {
		switch {
		case p.to == "ovtok" && p.n.Live != nil && p.n.Title != "":
			island = append(island, p)
		case p.to == "phone1" && p.n.Title == "" && p.n.Body == "" && p.n.Badge != nil:
			badge = append(badge, p)
		default:
			other = append(other, p)
		}
	}
	if len(island) != 1 || len(badge) != 1 || len(other) != 0 {
		t.Fatalf("done with the overview showing: island %d, badge %d, other %+v", len(island), len(badge), other)
	}
	if n := island[0].n; n.Title != "api" || n.Body != "✓ done · home · Hugo" || n.Sound != SoundDone || !n.Live.Urgent || n.Live.Event != "update" {
		t.Errorf("the island alert: %+v %+v", n, n.Live)
	}
	if *badge[0].n.Badge != 1 {
		t.Errorf("badge %d, want 1", *badge[0].n.Badge)
	}

	// a prompt: a notification as ever (with its actions), whatever the overview shows
	s.event(in, 1, wire.Event{Pane: "%1", Agent: "api", Kind: "blocked", Reason: "permission:Bash", At: time.Now()})
	if got := settle(); len(got) != 1 || got[0].to != "phone1" || got[0].n.Category != CategoryPermission || got[0].n.Live != nil {
		t.Fatalf("a prompt: %+v", got)
	}

	// the phone asked for finishes as notifications
	_, _ = s.devices.setPrefs("phone1", &Prefs{FinishedAsNotification: true})
	s.event(in, 1, wire.Event{Pane: "%1", Agent: "api", Kind: "error", At: time.Now()})
	if got := settle(); len(got) != 1 || got[0].to != "phone1" || got[0].n.Category != CategoryDone || got[0].n.Body != "✗ failed" {
		t.Fatalf("finished as a notification: %+v", got)
	}
	_, _ = s.devices.setPrefs("phone1", nil)

	// the overview's token is gone at Apple: the notification after all
	push.mu.Lock()
	push.failTo = map[string]error{"ovtok": apns.ErrUnregistered}
	push.mu.Unlock()
	s.event(in, 1, wire.Event{Pane: "%1", Agent: "api", Kind: "done", At: time.Now()})
	got = settle()
	push.mu.Lock()
	push.failTo = nil
	push.mu.Unlock()
	fell := false
	for _, p := range got {
		if p.to == "phone1" && p.n.Category == CategoryDone && p.n.Live == nil && strings.HasPrefix(p.n.Body, "✓") {
			fell = true
		}
	}
	if !fell {
		t.Fatalf("a failed island alert falls back to the notification: %+v", got)
	}

	// no overview on the phone (the token was dropped above): a notification
	s.event(in, 1, wire.Event{Pane: "%1", Agent: "api", Kind: "done", At: time.Now()})
	if got := settle(); len(got) != 1 || got[0].to != "phone1" || got[0].n.Category != CategoryDone {
		t.Fatalf("without an overview: %+v", got)
	}
}
