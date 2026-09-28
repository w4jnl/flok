package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/remote"
)

type cannedProc struct {
	out    string
	exit   int
	stderr string
	r      io.Reader
}

func (p *cannedProc) Stdin() io.Writer  { return io.Discard }
func (p *cannedProc) CloseStdin() error { return nil }
func (p *cannedProc) Stdout() io.Reader { return p.r }
func (p *cannedProc) Wait() (int, string) {
	return p.exit, p.stderr
}
func (p *cannedProc) Kill() {}

func canned(answers map[string]*cannedProc) remote.Dialer {
	return func(_ context.Context, argv []string) (remote.Proc, error) {
		for i, a := range argv { // the target follows "--"
			if a == "--" && i+1 < len(argv) {
				p := answers[argv[i+1]]
				p.r = strings.NewReader(p.out)
				return p, nil
			}
		}
		return nil, nil
	}
}

const goodProbe = "tmux 3.4\n---\n/opt/homebrew/bin/flok\n---\n" +
	`{"type":"hello","hello":{"proto":1,"version":"v0.5.0","hostname":"beta","pid":7,"tmux_version":"3.4","state_dir":"/h/.local/state/flok","features":["keys","prefix","notify"]}}` +
	"\n---\nterminfo=ok\n---\nhooks=9\n---\nflok 0.5.0\n---\nx\nx\n"

func TestParseHostProbe(t *testing.T) {
	p := parseHostProbe(goodProbe)
	if p.TmuxVersion != "tmux 3.4" || p.Flok != "/opt/homebrew/bin/flok" || p.Hello == nil || p.Hello.Version != "v0.5.0" || !p.Terminfo || p.Hooks != 9 || p.Version != "0.5.0" || p.Sessions != 2 {
		t.Fatalf("%+v", p)
	}
	p = parseHostProbe("Welcome!\ntmux 2.7\n---\nnone\n---\n---\nterminfo=missing\n---\nhooks=nofile\n")
	if p.TmuxVersion != "tmux 2.7" || p.Flok != "" || p.Hello != nil || p.Terminfo || p.Hooks != -1 {
		t.Fatalf("%+v", p)
	}
	if !strings.Contains(hostProbeScript(hosts.Host{Flok: "/x/flok"}), "/x/flok serve --hello") || strings.Contains(hostProbeScript(hosts.Host{}), "/x/") {
		t.Fatal("the configured flok path is probed")
	}
}

func TestHostChecks(t *testing.T) {
	cfg := config.Default()
	set := hosts.Set{Hosts: []hosts.Host{
		{Name: "beta", Target: "beta", Mode: hosts.ModeFull, Enabled: true},
		{Name: "gamma", Target: "gamma", Mode: hosts.ModePlain, Enabled: true},
		{Name: "delta", Target: "delta", Mode: hosts.ModeFull, Enabled: true},
		{Name: "old", Target: "old", Mode: hosts.ModePlain, Enabled: true},
		{Name: "eps", Target: "eps", Mode: hosts.ModeFull, Enabled: true},
		{Name: "nokeys", Target: "nokeys", Mode: hosts.ModeFull, Enabled: true},
		{Name: "off", Target: "off", Mode: hosts.ModeFull},
	}}
	dial := canned(map[string]*cannedProc{
		"beta":  {out: goodProbe},
		"gamma": {out: "tmux 3.2a\n---\nnone\n---\n---\nterminfo=missing\n---\nhooks=nofile\n"},
		"delta": {exit: 255, stderr: "delta: Permission denied (publickey)."},
		"old":   {out: "tmux 2.6\n---\nnone\n---\n---\nterminfo=ok\n---\nhooks=nofile\n"},
		"eps":   {out: "tmux 3.4\n---\n/usr/local/bin/flok\n---\n---\nterminfo=ok\n---\nhooks=3\n---\nflok 0.4.4\n---\nno server running on /tmp/tmux-0/default\n"},
		"nokeys": {out: "tmux 3.4\n---\n/usr/local/bin/flok\n---\n" +
			`{"type":"hello","hello":{"proto":1,"version":"0.4.6","hostname":"nokeys","pid":7,"tmux_version":"3.4"}}` + "\n---\nterminfo=ok\n---\nhooks=3\n---\nflok 0.4.6\n---\nx\n"},
	})
	got := hostChecks(cfg, t.TempDir(), set, dial, time.Second)
	var lines []string
	for _, c := range got {
		lines = append(lines, c.level+" "+c.text)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"ok host beta (full): tmux 3.4, flok 0.5.0 (protocol 1)",
		"ok host gamma (plain): tmux 3.2a",
		"warn host gamma (plain): no tmux-256color terminfo",
		"fail host delta (full): needs auth (Permission denied (publickey)); run `ssh delta` once",
		"fail host old (plain): tmux 2.6 is too old, flok needs 2.7 or newer",
		"warn host eps (full): tmux 3.4, flok 0.4.4 at /usr/local/bin/flok is too old, it has no `serve`: `flok host install eps` upgrades it (or --mode plain)",
		"warn host eps (full): tmux is installed but not running for eps: the sidebar's work pane starts a session there",
		"warn host nokeys (full): tmux 3.4, flok 0.4.6 (protocol 1) predates the key relay: flok's keys do nothing inside its sessions (`flok host install nokeys` upgrades it)",
		"ok host off (full): disabled (flok host connect off)",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "host beta (full): no ~/.claude") || strings.Contains(joined, "host gamma (plain): Claude Code hooks") || strings.Contains(joined, "3.2a: ") {
		t.Fatalf("hooks are a full-mode concern, beta has them, and the outer's feature gates do not apply to hosts:\n%s", joined)
	}
	if hostChecks(cfg, t.TempDir(), hosts.Set{}, dial, time.Second) != nil {
		t.Fatal("no hosts, no lines")
	}
}
