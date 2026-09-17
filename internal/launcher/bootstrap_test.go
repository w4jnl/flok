package launcher

import (
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/config"
)

func TestBootstrapTargetDefaultsToMain(t *testing.T) {
	if got := bootstrapTarget(config.Default()); got != "main" {
		t.Fatalf("default target = %q", got)
	}
	cfg := config.Default()
	cfg.Inner.Session = "work"
	if got := bootstrapTarget(cfg); got != "work" {
		t.Fatalf("configured target = %q", got)
	}
}

func TestIsResurrectRestore(t *testing.T) {
	yes := []string{
		"bash /Users/x/.tmux/plugins/tmux-resurrect/scripts/restore.sh",
		"/bin/bash /home/x/.config/tmux/plugins/tmux-continuum/scripts/continuum_restore.sh",
	}
	yes = append(yes, "/Users/x/.tmux/plugins/tmux-resurrect/scripts/restore.sh")
	no := []string{
		"bash /home/x/.tmux/plugins/tmux-resurrect/scripts/save.sh",
		"vim /home/x/.tmux/plugins/tmux-resurrect/scripts/restore.sh",
		"bash -c 'ls tmux-resurrect/scripts/restore.sh'", // a shell whose command line only mentions the script
		"tmux attach", "",
	}
	for _, l := range yes {
		if !isResurrectRestore(l) {
			t.Errorf("%q not recognized", l)
		}
	}
	for _, l := range no {
		if isResurrectRestore(l) {
			t.Errorf("%q recognized", l)
		}
	}
}

// The wait ends as soon as a restore that was seen running has finished, and it does not
// outlast the grace when no restore ever shows up.
func TestWaitForResurrectRestore(t *testing.T) {
	calls := 0
	start := time.Now()
	waitForResurrectRestore(10*time.Second, func() bool { calls++; return calls < 3 })
	if calls != 3 || time.Since(start) > 2*time.Second {
		t.Fatalf("seen restore: calls=%d took %s", calls, time.Since(start))
	}
	start = time.Now()
	waitForResurrectRestore(10*time.Second, func() bool { return false })
	if d := time.Since(start); d < 2*time.Second || d > 4*time.Second {
		t.Fatalf("no restore: waited %s, want about the 2 s grace", d)
	}
}

func TestPickHome(t *testing.T) {
	sessions := []sessionInfo{{ID: "$1", Name: "a", Activity: 10}, {ID: "$2", Name: "work", Activity: 5}, {ID: "$3", Name: "b", Activity: 20}}
	if got := pickHome(sessions, "work"); got != "$2" {
		t.Fatalf("preferred = %s", got)
	}
	if got := pickHome(sessions, "missing"); got != "$3" {
		t.Fatalf("most recent = %s", got)
	}
	if got := pickHome(sessions, ""); got != "$3" {
		t.Fatalf("no preference = %s", got)
	}
}

// tmux matches an unknown session target by prefix, so no ordinary session name may be a prefix
// of the bootstrap name: it starts with a character names do not start with.
func TestBootstrapNameCannotBePrefixMatched(t *testing.T) {
	if bootstrapSession == "" || strings.ContainsAny(bootstrapSession[:1], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") {
		t.Fatalf("bootstrap session %q starts like an ordinary session name", bootstrapSession)
	}
	if strings.ContainsAny(bootstrapSession, ".:") {
		t.Fatalf("bootstrap session %q contains characters tmux rewrites", bootstrapSession)
	}
}
