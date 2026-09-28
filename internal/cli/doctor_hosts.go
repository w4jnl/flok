package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/remote/proto"
	"github.com/w4jnl/flok/internal/tmux"
)

// hostProbe is what one fixed command, run on a host through the same ssh the sidebar uses,
// reports back.
type hostProbe struct {
	TmuxVersion string       // "tmux 3.4" line, "" when tmux is missing
	Flok        string       // path of the flok the remote shell finds, "" when none
	Hello       *proto.Hello // `flok serve --hello`, nil when flok or serve is missing
	Terminfo    bool         // the host has the tmux-256color terminfo entry
	Hooks       int          // Claude Code hook entries in ~/.claude/settings.json; -1 = no file
	Version     string       // `flok version` there, "" when unknown (also set for a flok without serve)
	Sessions    int          // tmux sessions on the host's socket; -1 = no server running
}

const probeSep = "---"

// hostProbeScript is the remote command; sections are separated by "---" lines.
func hostProbeScript(h hosts.Host) string {
	flok, find := "flok", "command -v flok 2>/dev/null || echo none"
	if h.Flok != "" {
		flok = tmux.ShellQuote(h.Flok)
		find = "if [ -x " + flok + " ]; then echo " + flok + "; else echo none; fi"
	}
	return strings.Join([]string{
		"tmux -V 2>&1 || echo none",
		"echo " + probeSep,
		find,
		"echo " + probeSep,
		flok + " serve --hello 2>/dev/null || true",
		"echo " + probeSep,
		"if infocmp tmux-256color >/dev/null 2>&1; then echo terminfo=ok; else echo terminfo=missing; fi",
		"echo " + probeSep,
		`if [ -f "$HOME/.claude/settings.json" ]; then n=$(grep -c 'hook claude' "$HOME/.claude/settings.json" 2>/dev/null); echo "hooks=${n:-0}"; else echo hooks=nofile; fi`,
		"echo " + probeSep,
		flok + " version 2>/dev/null || true",
		"echo " + probeSep,
		tmuxCmd(h) + " list-sessions -F x 2>&1 || true",
	}, "; ")
}

func socketNote(h hosts.Host) string {
	if h.Socket != "" {
		return " on socket " + h.Socket
	}
	return ""
}

// tmuxCmd is the host's tmux on the registered socket.
func tmuxCmd(h hosts.Host) string {
	if h.Socket != "" {
		return "tmux -L " + tmux.ShellQuote(h.Socket)
	}
	return "tmux"
}

func parseHostProbe(out string) hostProbe {
	var p hostProbe
	var sections [][]string
	cur := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == probeSep {
			sections = append(sections, cur)
			cur = []string{}
			continue
		}
		if line != "" {
			cur = append(cur, line)
		}
	}
	sections = append(sections, cur)
	get := func(i int) []string {
		if i < len(sections) {
			return sections[i]
		}
		return nil
	}
	for _, line := range get(0) { // a login banner may precede it
		if strings.HasPrefix(line, "tmux ") {
			p.TmuxVersion = line
		}
	}
	for _, line := range get(1) {
		if strings.HasPrefix(line, "/") {
			p.Flok = line
		}
	}
	for _, line := range get(2) {
		if strings.HasPrefix(line, "{") {
			var f proto.Frame
			if r := proto.NewReader(strings.NewReader(line)); r != nil {
				if fr, err := r.Next(); err == nil && fr.Hello != nil {
					f = fr
				}
			}
			p.Hello = f.Hello
		}
	}
	for _, line := range get(3) {
		p.Terminfo = p.Terminfo || line == "terminfo=ok"
	}
	for _, line := range get(4) {
		switch v := strings.TrimPrefix(line, "hooks="); {
		case v == "nofile":
			p.Hooks = -1
		case strings.HasPrefix(line, "hooks="):
			p.Hooks, _ = strconv.Atoi(v)
		}
	}
	for _, line := range get(5) {
		if strings.HasPrefix(line, "flok ") {
			p.Version = strings.TrimPrefix(strings.TrimPrefix(line, "flok "), "v")
		}
	}
	for _, line := range get(6) {
		switch {
		case strings.Contains(line, "no server running"), strings.Contains(line, "no sessions"):
			p.Sessions = -1
		case line == "x":
			p.Sessions++
		}
	}
	return p
}

// hostChecks probes every registered host in parallel (each bounded by the connect timeout plus
// a margin) and turns the answers into doctor lines. No hosts, no lines.
func hostChecks(cfg config.Config, stateDir string, set hosts.Set, dial remote.Dialer, timeout time.Duration) []check {
	if len(set.Hosts) == 0 {
		return nil
	}
	var out []check
	if cfg.Hosts.Multiplex {
		if dir, ok := remote.ControlDir(stateDir); !ok {
			out = append(out, check{"warn", fmt.Sprintf("hosts: ssh multiplexing skipped, a control socket under %s would exceed the OS path limit; a shorter FLOK_STATE brings it back", dir)})
		}
	}
	results := make([][]check, len(set.Hosts))
	var wg sync.WaitGroup
	for i, h := range set.Hosts {
		wg.Add(1)
		go func(i int, h hosts.Host) {
			defer wg.Done()
			results[i] = probeHost(cfg, stateDir, h, dial, timeout)
		}(i, h)
	}
	wg.Wait()
	for _, r := range results {
		out = append(out, r...)
	}
	return out
}

func probeHost(cfg config.Config, stateDir string, h hosts.Host, dial remote.Dialer, timeout time.Duration) []check {
	name := fmt.Sprintf("host %s (%s)", h.Name, h.Mode)
	if !h.Enabled {
		return []check{{"ok", name + ": disabled (flok host connect " + h.Name + ")"}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	proc, err := dial(ctx, remote.Argv(cfg.Hosts, stateDir, h, false, remote.WithPath(cfg.Hosts, hostProbeScript(h))))
	if err != nil {
		return []check{{"fail", name + ": ssh could not start: " + err.Error()}}
	}
	data, _ := io.ReadAll(io.LimitReader(proc.Stdout(), 64<<10))
	exit, stderr := proc.Wait()
	p := parseHostProbe(string(data))
	if p.TmuxVersion == "" && p.Flok == "" && len(strings.TrimSpace(string(data))) == 0 {
		st := remote.Classify(exit, stderr)
		if ctx.Err() != nil {
			st = remote.Unreachable
		}
		text := name + ": " + st.Label()
		if d := remote.Detail(stderr); d != "" {
			text += " (" + d + ")"
		} else if ctx.Err() != nil {
			text += " (no answer within " + timeout.String() + ")"
		}
		if hint := st.Hint(h); hint != "" {
			text += "; " + hint
		}
		return []check{{"fail", text}}
	}
	var out []check
	add := func(level, format string, a ...any) { out = append(out, check{level, fmt.Sprintf(format, a...)}) }
	if p.TmuxVersion == "" {
		add("fail", "%s: no tmux on the host's PATH (a non-interactive ssh shell sees only the system PATH; [hosts] remote_path adds directories)", name)
		return out
	}
	ver := tmux.ParseVersion(p.TmuxVersion)
	summary := "tmux " + ver.String()
	level := "ok"
	if ver.Known && !ver.AtLeast(tmux.Floor.Major, tmux.Floor.Minor) {
		add("fail", "%s: tmux %s is too old, flok needs %s or newer", name, ver, tmux.Floor)
		return out
	}
	if h.Mode == hosts.ModeFull {
		switch {
		case p.Flok == "":
			level = "warn"
			summary += ", no flok on the remote's non-interactive PATH (the sidebar tries ~/.local/bin, /opt/homebrew/bin and /usr/local/bin; else `flok host add --flok <path>` or --mode plain)"
		case p.Hello == nil:
			level = "warn"
			which := p.Flok
			if p.Version != "" {
				which = "flok " + p.Version + " at " + p.Flok
			}
			summary += fmt.Sprintf(", %s is too old, it has no `serve` (upgrade flok there, or use --mode plain)", which)
		case p.Hello.Proto != proto.Version:
			level = "warn"
			summary += fmt.Sprintf(", flok %s speaks protocol %d, this one %d (upgrade one side, or use --mode plain)", p.Hello.Version, p.Hello.Proto, proto.Version)
		default:
			summary += fmt.Sprintf(", flok %s (protocol %d)", strings.TrimPrefix(p.Hello.Version, "v"), p.Hello.Proto)
			if !p.Hello.Has(proto.FeatureKeys) {
				level = "warn"
				summary += " predates the key relay: flok's keys do nothing inside its sessions (upgrade flok on the host)"
			}
		}
	}
	add(level, "%s: %s", name, summary) // the outer's feature gates do not apply to a remote inner server
	if h.Mode == hosts.ModeFull && p.Hello != nil {
		switch {
		case p.Hooks < 0:
			add("warn", "%s: no ~/.claude/settings.json there, so no Claude Code hooks: agents on it show title and screen states only (run `flok install --claude` on the host)", name)
		case p.Hooks == 0:
			add("warn", "%s: Claude Code hooks not installed there (run `flok install --claude` on the host)", name)
		}
	}
	if p.Sessions < 0 {
		add("warn", "%s: tmux is installed but not running for %s%s: the sidebar's work pane starts a session there when connected ([hosts] session, or --session); until then nothing can show. If the sessions live under another user or socket, set --target or --socket", name, h.Target, socketNote(h))
	}
	if !p.Terminfo && h.Term == "" {
		add("warn", "%s: no tmux-256color terminfo on the host, its tmux would refuse the attach; add the host with `--term screen-256color`", name)
	}
	return out
}
