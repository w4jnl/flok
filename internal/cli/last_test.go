package cli

import (
	"strings"
	"testing"
)

func TestLastFallbackIsTmuxOwn(t *testing.T) {
	for kind, want := range map[string]string{
		"session": "switch-client -l -c /dev/ttys003",
		"window":  "last-window -t $4",
		"pane":    "last-pane -t $4:",
	} {
		if got := strings.Join(lastFallback(kind, "/dev/ttys003", "$4"), " "); got != want {
			t.Errorf("%s: %q", kind, got)
		}
	}
}
