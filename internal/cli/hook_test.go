package cli

import (
	"testing"

	"github.com/w4jnl/flok/internal/config"
)

func TestHookFocusWhenFocused(t *testing.T) {
	calls := 0
	watched := func() bool { calls++; return true }
	cfg := config.Default()
	if !hookFocus(cfg, watched)() || calls != 1 {
		t.Fatal("default: the real focus check decides")
	}
	cfg.Sounds.WhenFocused = true
	if hookFocus(cfg, watched)() || calls != 1 {
		t.Fatal("when_focused: a watched pane counts as unwatched, without asking tmux")
	}
	cfg.Sounds.Enabled = false
	if !hookFocus(cfg, watched)() {
		t.Fatal("sounds disabled: when_focused has nothing to do, focus behaves as usual")
	}
}
