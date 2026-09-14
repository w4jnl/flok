package launcher

import (
	"strings"
	"testing"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/tmux"
)

// The outer config must contain only what the given tmux understands: tmux does not abort on
// an unknown option, it shows the errors in a view-mode overlay the user has to dismiss.
func TestRenderOuterConfByVersion(t *testing.T) {
	render := func(v string) string {
		out, err := RenderOuterConf(config.Default(), "/opt/flok bin/flok", tmux.FeaturesFor(tmux.ParseVersion(v)))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	gated := []string{"allow-passthrough", "extended-keys on", "extended-keys-format", "terminal-features",
		"window-size latest", "pane-border-lines", "pane-border-indicators", "client-focus-in", "window-resized"}
	always := []string{"set -g prefix None", "set -g exit-empty on", "set -g bell-action any", "set -g visual-bell off",
		"set -g main-pane-width 28", "set-hook -g client-attached", `source-file -q "`}
	cases := map[string]struct{ has, hasNot []string }{
		"2.7": {hasNot: gated, has: []string{"set-hook -g client-resized"}},
		"3.2a": {has: []string{"extended-keys on", "terminal-features", "window-size latest", "pane-border-lines", "set-hook -g client-resized"},
			hasNot: []string{"allow-passthrough", "extended-keys-format", "pane-border-indicators", "client-focus-in", "window-resized"}},
		"3.3a": {has: []string{"allow-passthrough", "pane-border-indicators", "client-focus-in", "set-hook -g window-resized"},
			hasNot: []string{"extended-keys-format", "client-resized"}},
		"3.4":  {has: []string{"allow-passthrough"}, hasNot: []string{"extended-keys-format"}},
		"3.7c": {has: append([]string{"extended-keys-format csi-u"}, gated[:8]...), hasNot: []string{"client-resized"}},
	}
	for v, c := range cases {
		out := render(v)
		for _, s := range append(c.has, always...) {
			if !strings.Contains(out, s) {
				t.Errorf("tmux %s: missing %q", v, s)
			}
		}
		for _, s := range c.hasNot {
			if strings.Contains(out, s) {
				t.Errorf("tmux %s: must not contain %q", v, s)
			}
		}
		if !strings.HasSuffix(strings.TrimSpace(out), `outer.extra.conf"`) {
			t.Errorf("tmux %s: the user's extra conf must be sourced last", v)
		}
	}
}

func TestPaneCommandsUseEnvNotDashE(t *testing.T) {
	cmd := SidebarCommand("/opt/flok bin/flok", "%3")
	if cmd != "env FLOK_OUTER=1 FLOK_RIGHT_PANE=%3 '/opt/flok bin/flok' sidebar" {
		t.Fatalf("sidebar command: %q", cmd)
	}
	if got := attachLoopCommand("/usr/local/bin/flok"); got != "env FLOK_OUTER=1 '/usr/local/bin/flok' _attach-loop" {
		t.Fatalf("attach loop command: %q", got)
	}
}
