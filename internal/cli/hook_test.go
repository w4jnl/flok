package cli

import (
	"os"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/state"
)

type recordingSounder struct {
	played []string
}

func (s *recordingSounder) Play(kind string) error {
	s.played = append(s.played, kind)
	return nil
}

func TestPermissionHookStartsGraceCheckWithoutWaiting(t *testing.T) {
	t.Setenv("FLOK_STATE", t.TempDir())
	t.Setenv("TMUX_PANE", "%7")
	t.Setenv("TMUX", "")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(`{"toolName":"bash","toolInput":{"command":"true"}}`); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = r.Close()
	})

	var startedPane string
	var startedAt time.Time
	start := time.Now()
	code := runHookWithBlockedSoundStarter(config.Default(), []string{"copilot", "--event", "permissionRequest"}, func(pane string, at time.Time) error {
		startedPane, startedAt = pane, at
		return nil
	})
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Fatalf("permission hook waited for grace period: %v", elapsed)
	}
	if code != 0 || startedPane != "%7" || startedAt.IsZero() {
		t.Fatalf("code=%d pane=%q at=%v", code, startedPane, startedAt)
	}
	if got := state.New(config.StateDir()).LoadAgents()["%7"]; got.State != agent.Blocked {
		t.Fatalf("agent state = %q, want blocked", got.State)
	}
}

func TestZeroGracePlaysSynchronously(t *testing.T) {
	t.Setenv("FLOK_STATE", t.TempDir())
	t.Setenv("TMUX_PANE", "%8")
	t.Setenv("TMUX", "")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(`{"toolName":"bash","toolInput":{"command":"true"}}`); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = r.Close()
	})

	cfg := config.Default()
	cfg.Sounds.BlockedGraceMs = 0
	cfg.Sounds.Command = "true"
	cfg.Sounds.Bell = "never"
	started := false
	code := runHookWithBlockedSoundStarter(cfg, []string{"copilot", "--event", "permissionRequest"}, func(string, time.Time) error {
		started = true
		return nil
	})
	if code != 0 || started {
		t.Fatalf("code=%d detached worker started=%v", code, started)
	}
	a := state.New(config.StateDir()).LoadAgents()["%8"]
	if len(a.Notifications) != 1 || !a.Notifications[0].Sounded {
		t.Fatalf("blocked notification was not played synchronously: %+v", a.Notifications)
	}
}

func TestBlockedGraceCheckTargetsOriginatingNotification(t *testing.T) {
	st := state.New(t.TempDir())
	oldAt := time.Now().Add(-time.Second)
	currentAt := oldAt.Add(500 * time.Millisecond)
	_, _, err := st.Update("%7", func(a *agent.Agent) state.Effects {
		a.State = agent.Blocked
		a.StateSince = currentAt
		a.Notifications = []agent.Notification{
			{Kind: "blocked", At: oldAt},
			{Kind: "blocked", At: currentAt},
		}
		return state.Effects{}
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	sounder := &recordingSounder{}
	playBlockedIfStillWaiting(st, sounder, cfg, "%7", oldAt)
	if len(sounder.played) != 0 {
		t.Fatalf("old notification played sounds: %v", sounder.played)
	}
	a := st.LoadAgents()["%7"]
	if !a.Notifications[0].Sounded || a.Notifications[1].Sounded {
		t.Fatalf("wrong notification marked after old check: %+v", a.Notifications)
	}

	playBlockedIfStillWaiting(st, sounder, cfg, "%7", currentAt)
	if len(sounder.played) != 1 || sounder.played[0] != "blocked" {
		t.Fatalf("current notification sounds: %v", sounder.played)
	}
	a = st.LoadAgents()["%7"]
	if !a.Notifications[1].Sounded {
		t.Fatalf("current notification not marked sounded: %+v", a.Notifications)
	}
}
