package remote

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/tmux"
)

func TestArgvGolden(t *testing.T) {
	cfg := config.Default().Hosts
	cfg.SSHOptions = []string{"-o", "IdentitiesOnly=yes"}
	dir := t.TempDir()
	h := hosts.Host{Name: "beta", Target: "jaro@beta", Mode: hosts.ModeFull}
	got := Argv(cfg, dir, h, false, "flok serve --stdio")
	ctl, mux := ControlDir(dir)
	want := []string{"ssh", "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=2"}
	if mux {
		want = append(want, "-o", "ControlMaster=auto", "-o", "ControlPersist=60", "-o", "ControlPath="+filepath.Join(ctl, "%C"))
	}
	want = append(want, "-T", "--", "jaro@beta", "flok serve --stdio")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
	// a tty attach without a remote command ends in the target; a long state dir skips multiplexing
	long := filepath.Join(dir, strings.Repeat("x", 80))
	got = Argv(cfg, long, h, true, "")
	if got[len(got)-1] != "jaro@beta" || got[len(got)-3] != "-t" || strings.Contains(strings.Join(got, " "), "ControlMaster") {
		t.Fatalf("tty argv %q", got)
	}
	cfg.Multiplex = false
	if s := strings.Join(Argv(cfg, dir, h, false, ""), " "); strings.Contains(s, "Control") {
		t.Fatalf("multiplex off: %s", s)
	}
	if got := WithPath(config.Default().Hosts, "tmux -V"); got != `export PATH="$PATH:/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:/opt/local/bin"; tmux -V` {
		t.Fatalf("WithPath %q", got)
	}
	cfg.RemotePath = ""
	if WithPath(cfg, "tmux -V") != "tmux -V" || PathPrefix(config.Hosts{RemotePath: `/x"; rm -rf /`}) != "" {
		t.Fatal("empty or unsafe remote_path adds nothing")
	}
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		exit   int
		stderr string
		want   State
	}{
		{255, "jaro@beta: Permission denied (publickey,password).", Auth},
		{255, "Host key verification failed.", HostKey},
		{255, "@@@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @@@", HostKey},
		{255, "ssh: connect to host beta port 22: Connection refused", Unreachable},
		{255, "ssh: Could not resolve hostname beta: nodename nor servname provided", Unreachable},
		{127, "bash: flok: command not found", NoFlok},
		{127, "sh: 1: flok: not found", NoFlok},
		{2, "flok: unknown command \"serve\"", OldFlok},
		{1, "no server running on /tmp/tmux-0/default", NoServer},
		{1, "no sessions", NoServer},
		{1, "", Unreachable},
		{-1, "killed", Unreachable},
	} {
		if got := Classify(tc.exit, tc.stderr); got != tc.want {
			t.Errorf("Classify(%d, %q) = %s, want %s", tc.exit, tc.stderr, got, tc.want)
		}
	}
	if d := Detail("warning: x\nssh: connect to host beta port 22: Connection refused\n"); d != "Connection refused" {
		t.Fatalf("detail %q", d)
	}
	st, d := ClassifyErr(&tmux.ExitError{Code: 255, Stderr: "jaro@beta: Permission denied (publickey).", Cmd: "beta: tmux -V"})
	if st != Auth || d != "Permission denied (publickey)" {
		t.Fatalf("ClassifyErr: %s %q", st, d)
	}
	if st, _ := ClassifyErr(errors.New("dial: boom")); st != Unreachable {
		t.Fatal("plain errors are unreachable")
	}
	if !Auth.SlowRetry() || !OldFlok.SlowRetry() || Unreachable.SlowRetry() || Auth.Label() != "needs auth" || OldFlok.Label() != "old flok" || Disabled.Label() != "off" || Auth.Hint(hosts.Host{Target: "b"}) == "" || OldFlok.Hint(hosts.Host{}) == "" {
		t.Fatal("state helpers")
	}
	if Backoff(0, 30) != 1e9 || Backoff(3, 30) != 8e9 || Backoff(9, 30) != 30e9 || Backoff(20, 0) != 30e9 {
		t.Fatalf("backoff %v %v %v %v", Backoff(0, 30), Backoff(3, 30), Backoff(9, 30), Backoff(20, 0))
	}
}
