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
		def  string
		want string
	}{
		{hosts.Host{Target: "beta"}, "main", "tmux attach-session || tmux new-session -s main"},
		{hosts.Host{Target: "beta"}, "", "tmux attach-session"},
		{hosts.Host{Target: "beta", Socket: "agents"}, "main", "tmux -L agents attach-session || tmux -L agents new-session -s main"},
		{hosts.Host{Target: "beta", Socket: "agents", Session: "work"}, "main", "tmux -L agents new-session -A -s work"},
		{hosts.Host{Target: "beta", Term: "screen-256color"}, "main", "export TERM=screen-256color; tmux attach-session || tmux new-session -s main"},
	} {
		if got := hostAttachCommand(tc.h, tc.def); got != tc.want {
			t.Errorf("%+v/%q: %q, want %q", tc.h, tc.def, got, tc.want)
		}
	}
	if strings.Contains(hostAttachLoopCommand("/x", "b")+hostAttachCommand(hosts.Host{}, "main"), " -e ") {
		t.Fatal("no -e flags: tmux 2.7 lacks them")
	}
}

func TestParseWorkPanes(t *testing.T) {
	// panes are known by what they run (tmux quotes the command), wherever a swap put them:
	// beta's pane sits in window 0, the local pane in beta's window, a stray twin of gpu-2 exists
	wp := parseWorkPanes("%1 @0 \"env FLOK_OUTER=1 FLOK_RIGHT_PANE=%0 '/x/flok' sidebar\"\n" +
		"%7 @0 \"env FLOK_OUTER=1 '/x/flok' _attach-loop --host beta\"\n" +
		"%0 @3 \"env FLOK_OUTER=1 '/x/flok' _attach-loop\"\n" +
		"%9 @4 \"env FLOK_OUTER=1 '/x/flok' _attach-loop --host gpu-2\"\n" +
		"%11 @5 \"env FLOK_OUTER=1 '/x/flok' _attach-loop --host gpu-2\"\n")
	if wp.Local != "%0" || wp.Hosts["beta"] != "%7" || wp.Hosts["gpu-2"] != "%9" || wp.Window["%7"] != "@0" || len(wp.Hosts) != 2 {
		t.Fatalf("%+v", wp)
	}
	if len(wp.Extra["gpu-2"]) != 1 || wp.Extra["gpu-2"][0] != "%11" || len(wp.Extra["beta"]) != 0 {
		t.Fatalf("extra %+v", wp.Extra)
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
		{127, "sh: tmux: not found", time.Second, 0, remote.Unreachable, "flok: beta unreachable (no tmux on the host's PATH: sh: tmux: not found), retry in 1s", time.Second},
	} {
		got := hostAttachOutcome(h, tc.exit, tc.stderr, tc.ran, tc.attempt, 30)
		if got.state != tc.state || got.line != tc.line || got.delay != tc.delay {
			t.Errorf("exit %d %q: got %+v", tc.exit, tc.stderr, got)
		}
	}
}
