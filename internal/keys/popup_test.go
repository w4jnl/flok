package keys

import (
	"strings"
	"testing"

	"github.com/w4jnl/flok/internal/tmux"
)

func TestPopupArgsByVersion(t *testing.T) {
	if args := PopupArgs(tmux.FeaturesFor(tmux.ParseVersion("2.7")), "", "flok keys"); args != nil {
		t.Fatalf("no popups before 3.2: %v", args)
	}
	el9 := strings.Join(PopupArgs(tmux.FeaturesFor(tmux.ParseVersion("3.2a")), "/dev/pts/1", "flok keys"), " ")
	if !strings.HasPrefix(el9, "display-popup -E -w 80% -h 85% -c /dev/pts/1 flok keys") || strings.Contains(el9, "-b") {
		t.Fatalf("3.2: %q", el9)
	}
	full := strings.Join(PopupArgs(tmux.FeaturesFor(tmux.ParseVersion("3.7c")), "", "flok keys"), " ")
	if !strings.Contains(full, "-b rounded -T  keybinds  -s fg=default,bg=default -S fg=default,bg=default flok keys") {
		t.Fatalf("3.3+: %q (the terminal's colours, not tmux 3.8's grey popup)", full)
	}
}
