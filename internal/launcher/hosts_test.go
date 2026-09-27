package launcher

import (
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/remote"
)

func TestHostCommands(t *testing.T) {
	if got := hostAttachLoopCommand("/opt/f lok/flok", "beta"); got != "env FLOK_OUTER=1 '/opt/f lok/flok' _attach-loop --host beta" {
		t.Fatalf("pane command %q", got)
	}
	for _, tc := range []struct {
		h    hosts.Host
		want string
	}{
		{hosts.Host{Target: "beta"}, "tmux attach-session"},
		{hosts.Host{Target: "beta", Socket: "agents", Session: "work"}, "tmux -L agents new-session -A -s work"},
		{hosts.Host{Target: "beta", Term: "screen-256color"}, "env TERM=screen-256color tmux attach-session"},
	} {
		if got := hostAttachCommand(tc.h); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.h, got, tc.want)
		}
	}
	if strings.Contains(hostAttachLoopCommand("/x", "b")+hostAttachCommand(hosts.Host{}), " -e ") {
		t.Fatal("no -e flags: tmux 2.7 lacks them")
	}
}

func TestParseHostWindows(t *testing.T) {
	got := parseHostWindows("@0\tmain\t%0\n@3\tflok-host-beta\t%7\n@4\tflok-host-gpu-2\t%9\n")
	if len(got) != 2 || got["beta"] != (HostPane{Window: "@3", Pane: "%7"}) || got["gpu-2"].Pane != "%9" {
		t.Fatalf("%+v", got)
	}
}

func TestHostAttachOutcome(t *testing.T) {
	h := hosts.Host{Name: "beta", Target: "jaro@beta"}
	for _, tc := range []struct {
		exit    int
		stderr  string
		ran     time.Duration
		attempt int
		state   remote.State
		line    string
		delay   time.Duration
	}{
		{0, "", time.Hour, 0, remote.Connected, "flok: beta: detached, re-attaching in 3s", 3 * time.Second},
		{1, "no sessions", time.Second, 0, remote.Connected, "flok: beta: no sessions yet, retry in 3s", 3 * time.Second},
		{255, "ssh: connect to host beta port 22: Connection refused", time.Second, 3, remote.Unreachable, "flok: beta unreachable (Connection refused), retry in 8s", 8 * time.Second},
		{255, "ssh: connect to host beta port 22: Connection refused", 2 * time.Minute, 3, remote.Unreachable, "flok: beta unreachable (Connection refused), retry in 1s", time.Second},
		{255, "jaro@beta: Permission denied (publickey).", time.Second, 0, remote.Auth, "flok: beta needs auth: run `ssh jaro@beta` once (keys or an agent; flok never prompts) (retry in 60s)", time.Minute},
		{127, "sh: tmux: not found", time.Second, 0, remote.Unreachable, "flok: beta unreachable (no tmux on the host: sh: tmux: not found), retry in 1s", time.Second},
	} {
		got := hostAttachOutcome(h, tc.exit, tc.stderr, tc.ran, tc.attempt, 30)
		if got.state != tc.state || got.line != tc.line || got.delay != tc.delay {
			t.Errorf("exit %d %q: got %+v", tc.exit, tc.stderr, got)
		}
	}
}
