package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/release"
	"github.com/w4jnl/flok/internal/tmux"
)

// InstallOpts is what one `flok host install` needs. Dial nil = Exec; the function hooks are
// for tests (nil = the real thing).
type InstallOpts struct {
	Cfg      config.Hosts
	StateDir string
	Host     hosts.Host
	Dial     Dialer
	From     string // --from: this file is pushed as it is
	Version  string // --version: that release, whatever the host runs
	Hooks    bool   // --hooks: wire the agents' hooks there afterwards
	// LocalVersion is this flok's version (cli.Version): what a pushed binary must report, and
	// the release a downloaded one should match.
	LocalVersion string
	Local        func() ([]byte, error)                                                                          // nil = this executable
	Fetch        func(ctx context.Context, want string, t release.Target, log io.Writer) ([]byte, string, error) // nil = release.Resolve + Binary
	Log          io.Writer                                                                                       // progress lines; nil = none
}

// InstallResult is what happened on the host.
type InstallResult struct {
	Path, Version string   // the flok now at ~/.local/bin on the host, and what it reports
	Uname         string   // "Linux x86_64"
	Source        string   // "this flok 0.5.0 (linux/amd64)" | "release 0.5.0 (linux/arm64)" | "file ./flok"
	Replaced      string   // "flok 0.4.4 at /usr/local/bin/flok"; "" when the host had none
	PathChanged   bool     // hosts.json now names another path: a running sidebar reconnects by itself
	Agents        []string // "claude", "copilot": their folders exist in the host's home
	HooksBefore   int      // flok hooks in the host's ~/.claude/settings.json before the install
	HooksDone     []string // agents whose hooks --hooks wired
	HooksMissing  bool     // --hooks asked, but no agent folder on the host
	Warning       string   // something non-fatal, e.g. the path could not be recorded
}

// installTimeout bounds the transfer (a binary of some MB over a slow link), not the probe.
const installTimeout = 5 * time.Minute

// installScript receives the binary on stdin and puts it in place: only ~/.local/bin/flok is
// written, through a temp file that must run `version` before it replaces anything, so a build
// that cannot run there (a noexec home, the wrong CPU) leaves the old flok alone. Exit codes 3,
// 4 and 5 tell the three failures apart from ssh's 255 and a shell's 127. Nothing is
// interpolated into it.
const installScript = `d="$HOME/.local/bin"; t="$d/.flok.new.$$"; mkdir -p "$d" && cat > "$t" && chmod 755 "$t" || { rm -f "$t"; exit 3; }; v=$("$t" version 2>&1) || { printf '%s\n' "$v" >&2; rm -f "$t"; exit 4; }; mv -f "$t" "$d/flok" || { rm -f "$t"; exit 5; }; printf 'version=%s\nsize=%s\npath=%s\n' "$v" "$(wc -c < "$d/flok" | tr -d ' ')" "$d/flok"`

// installProbeScript asks what the host is and has: after a --- marker (a login banner may
// precede it) the OS and CPU, $HOME, the flok on the PATH (or the registered one) and its
// version, the agent folders present, and how many flok hooks the Claude Code settings hold.
func installProbeScript(h hosts.Host) string {
	find := `f=$(command -v flok 2>/dev/null) || f=none`
	if h.Flok != "" {
		find = "f=" + tmux.ShellQuote(h.Flok) + `; [ -x "$f" ] || f=none`
	}
	return `echo ---; uname -sm; printf '%s\n' "$HOME"; ` + find + `; printf '%s\n' "$f"; if [ "$f" != none ]; then "$f" version 2>/dev/null || echo none; else echo none; fi; ` +
		`[ -d "$HOME/.claude" ] && echo claude; [ -d "$HOME/.copilot" ] && echo copilot; echo agents-end; n=$(grep -c 'hook claude' "$HOME/.claude/settings.json" 2>/dev/null); echo "hooks=${n:-0}"`
}

type installProbe struct {
	uname, home, flok, version string // flok "" when the host has none
	agents                     []string
	hooks                      int
}

// Install puts this flok (or the matching release, or a given file) on the host, over the same
// ssh the sidebar uses, and records where it landed. See the field comments for the rules.
func Install(ctx context.Context, o InstallOpts) (InstallResult, error) {
	dial := o.Dial
	if dial == nil {
		dial = Exec
	}
	logw := o.Log
	if logw == nil {
		logw = io.Discard
	}
	name := o.Host.Name
	var res InstallResult
	pr, err := probeForInstall(ctx, dial, o)
	if err != nil {
		return res, err
	}
	res.Uname, res.Agents, res.HooksBefore = pr.uname, pr.agents, pr.hooks
	if pr.flok != "" {
		res.Replaced = "flok " + pr.version + " at " + pr.flok
		if pr.version == "" {
			res.Replaced = "a flok at " + pr.flok + " that did not answer `version`"
		}
	}
	what := name + " (" + o.Host.Target + "): " + pr.uname
	if res.Replaced != "" {
		what += ", " + res.Replaced
	} else {
		what += ", no flok"
	}
	for _, a := range pr.agents {
		what += ", ~/." + a + " there"
	}
	fmt.Fprintln(logw, what)

	var bin []byte
	expected := "" // what `version` on the host must report; "" = only the prefix is checked
	switch {
	case o.From != "":
		if bin, err = os.ReadFile(o.From); err != nil {
			return res, err
		}
		res.Source = "file " + o.From
	default:
		t, terr := release.TargetFromUname(pr.uname)
		if terr != nil {
			return res, fmt.Errorf("%s: %w", name, terr)
		}
		if o.Version == "" && t.Local() {
			local := o.Local
			if local == nil {
				local = localBinary
			}
			if bin, err = local(); err != nil {
				return res, fmt.Errorf("reading this flok: %w", err)
			}
			expected = release.Clean(o.LocalVersion)
			res.Source = fmt.Sprintf("this flok %s (%s)", expected, t)
		} else {
			want := o.Version
			if want == "" && release.IsRelease(o.LocalVersion) {
				want = o.LocalVersion
			}
			if want == "" {
				fmt.Fprintf(logw, "this flok is %s, not a release: the host gets the latest release\n", o.LocalVersion)
			}
			fetch := o.Fetch
			if fetch == nil {
				fetch = fetchRelease
			}
			var ver string
			if bin, ver, err = fetch(ctx, want, t, logw); err != nil {
				return res, err
			}
			expected = release.Clean(ver)
			res.Source = fmt.Sprintf("release %s (%s)", expected, t)
		}
	}
	fmt.Fprintf(logw, "sending %s, %.1f MB…\n", res.Source, float64(len(bin))/1e6)

	ictx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	proc, err := dial(ictx, Argv(o.Cfg, o.StateDir, o.Host, false, WithPath(o.Cfg, installScript)))
	if err != nil {
		return res, fmt.Errorf("%s: %w", name, err)
	}
	outc := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(proc.Stdout(), 64<<10))
		outc <- b
	}()
	_, werr := io.Copy(proc.Stdin(), bytes.NewReader(bin)) // a short write shows in the exit code below
	_ = proc.CloseStdin()
	out := <-outc
	exit, stderr := proc.Wait()
	if ictx.Err() != nil && ctx.Err() == nil {
		return res, fmt.Errorf("%s: the transfer timed out after %s", name, installTimeout)
	}
	switch exit {
	case 0:
	case 3:
		return res, fmt.Errorf("%s: cannot write to ~/.local/bin there: %s", name, Detail(stderr))
	case 4:
		return res, fmt.Errorf("%s: the new flok does not run there (%s): a noexec home or the wrong build; the old one is untouched", name, Detail(stderr))
	case 5:
		return res, fmt.Errorf("%s: could not replace ~/.local/bin/flok there: %s", name, Detail(stderr))
	default:
		st := Classify(exit, stderr)
		msg := name + ": " + st.Label()
		if d := Detail(stderr); d != "" {
			msg += " (" + d + ")"
		} else if werr != nil {
			msg += " (" + werr.Error() + ")"
		}
		if hint := st.Hint(o.Host); hint != "" {
			msg += "; " + hint
		}
		return res, errors.New(msg)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			fields[k] = v
		}
	}
	reported := fields["version"]
	if !strings.HasPrefix(reported, "flok ") {
		return res, fmt.Errorf("%s: installed a flok that reports %q", name, reported)
	}
	got := release.Clean(strings.TrimPrefix(reported, "flok "))
	if expected != "" && got != expected {
		return res, fmt.Errorf("%s: the flok there reports %s, expected %s", name, got, expected)
	}
	if n, err := strconv.Atoi(fields["size"]); err != nil || n != len(bin) {
		return res, fmt.Errorf("%s: size mismatch: sent %d bytes, %s on the host", name, len(bin), fields["size"])
	}
	if fields["path"] == "" {
		return res, fmt.Errorf("%s: the host did not say where flok landed: %q", name, string(out))
	}
	res.Path, res.Version = fields["path"], got

	if o.StateDir != "" {
		old := ""
		_, err := hosts.Update(o.StateDir, func(s *hosts.Set) error {
			h, ok := s.Get(name)
			if !ok {
				return fmt.Errorf("host %q is not registered", name)
			}
			old = h.Flok
			return s.Set(name, func(h *hosts.Host) { h.Flok = res.Path })
		})
		if err != nil {
			res.Warning = "the path was not recorded in hosts.json (" + err.Error() + "); [hosts] remote_path still finds it"
		} else {
			res.PathChanged = old != res.Path
		}
	}
	if o.Hooks {
		if len(pr.agents) == 0 {
			res.HooksMissing = true
		} else if err := installHooks(ctx, dial, o, res.Path, pr.agents, logw); err != nil {
			return res, err
		} else {
			res.HooksDone = pr.agents
		}
	}
	return res, nil
}

// probeForInstall runs installProbeScript and reads its answer.
func probeForInstall(ctx context.Context, dial Dialer, o InstallOpts) (installProbe, error) {
	var pr installProbe
	timeout := o.Cfg.ConnectTimeoutS
	if timeout <= 0 {
		timeout = 10
	}
	pctx, cancel := context.WithTimeout(ctx, time.Duration(timeout+10)*time.Second)
	defer cancel()
	proc, err := dial(pctx, Argv(o.Cfg, o.StateDir, o.Host, false, WithPath(o.Cfg, installProbeScript(o.Host))))
	if err != nil {
		return pr, fmt.Errorf("%s: %w", o.Host.Name, err)
	}
	out, _ := io.ReadAll(io.LimitReader(proc.Stdout(), 64<<10))
	exit, stderr := proc.Wait()
	if exit != 0 {
		st := Classify(exit, stderr)
		msg := o.Host.Name + ": " + st.Label()
		if d := Detail(stderr); d != "" {
			msg += " (" + d + ")"
		}
		if hint := st.Hint(o.Host); hint != "" {
			msg += "; " + hint
		}
		return pr, errors.New(msg)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "---" {
			lines = lines[i+1:]
			break
		}
	}
	if len(lines) < 4 {
		return pr, fmt.Errorf("%s: unexpected answer from the host: %q", o.Host.Name, string(out))
	}
	pr.uname, pr.home = strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
	if f := strings.TrimSpace(lines[2]); f != "none" {
		pr.flok = f
	}
	if v := strings.TrimSpace(lines[3]); v != "none" && strings.HasPrefix(v, "flok ") {
		pr.version = release.Clean(strings.TrimPrefix(v, "flok "))
	}
	rest := lines[4:]
	for i, l := range rest {
		if strings.TrimSpace(l) == "agents-end" {
			for _, a := range rest[:i] {
				if a = strings.TrimSpace(a); a != "" {
					pr.agents = append(pr.agents, a)
				}
			}
			rest = rest[i+1:]
			break
		}
	}
	for _, l := range rest {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "hooks="); ok {
			pr.hooks, _ = strconv.Atoi(v)
		}
	}
	return pr, nil
}

// installHooks runs `flok install --claude/--copilot` on the host with the flok just installed,
// for the agents whose folders are there.
func installHooks(ctx context.Context, dial Dialer, o InstallOpts, path string, agents []string, logw io.Writer) error {
	var cmds []string
	for _, a := range agents {
		cmds = append(cmds, tmux.ShellQuote(path)+" install --"+a+" 2>&1")
	}
	hctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	proc, err := dial(hctx, Argv(o.Cfg, o.StateDir, o.Host, false, WithPath(o.Cfg, strings.Join(cmds, " && "))))
	if err != nil {
		return fmt.Errorf("%s: hooks: %w", o.Host.Name, err)
	}
	out, _ := io.ReadAll(io.LimitReader(proc.Stdout(), 64<<10))
	exit, stderr := proc.Wait()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			fmt.Fprintln(logw, "  "+line)
		}
	}
	if exit != 0 {
		return fmt.Errorf("%s: hooks: flok install failed there (%s)", o.Host.Name, Detail(stderr))
	}
	return nil
}

// localBinary is this executable, symlinks resolved.
func localBinary() ([]byte, error) {
	p, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return os.ReadFile(p)
}

// fetchRelease is the default Fetch: the release's tarball for t, checked and unpacked.
func fetchRelease(ctx context.Context, want string, t release.Target, logw io.Writer) ([]byte, string, error) {
	src, err := release.Resolve(ctx, want)
	if err != nil {
		return nil, "", err
	}
	fmt.Fprintf(logw, "downloading flok %s for %s from GitHub…\n", src.Version, t)
	bin, err := src.Binary(ctx, t, nil)
	if err != nil {
		return nil, "", err
	}
	return bin, src.Version, nil
}
