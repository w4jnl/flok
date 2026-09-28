package remote

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/release"
)

// memProc is an in-memory remote command: it collects stdin and answers once stdin is closed
// (or at once, for a probe), with a reply computed from what it received.
type memProc struct {
	reply  func(sent []byte) (out string, exit int, stderr string)
	mu     sync.Mutex
	in     bytes.Buffer
	closed chan struct{}
	done   chan struct{}
	once   sync.Once
	outR   *io.PipeReader
	exit   int
	stderr string
}

func newMemProc(immediate bool, reply func([]byte) (string, int, string)) *memProc {
	p := &memProc{reply: reply, closed: make(chan struct{}), done: make(chan struct{})}
	var outW *io.PipeWriter
	p.outR, outW = io.Pipe()
	go func() {
		if !immediate {
			<-p.closed
		}
		p.mu.Lock()
		sent := append([]byte(nil), p.in.Bytes()...)
		p.mu.Unlock()
		out, exit, stderr := p.reply(sent)
		p.mu.Lock()
		p.exit, p.stderr = exit, stderr
		p.mu.Unlock()
		close(p.done)
		_, _ = outW.Write([]byte(out))
		_ = outW.Close()
	}()
	return p
}

func (p *memProc) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.in.Write(b)
}
func (p *memProc) Stdin() io.Writer  { return p }
func (p *memProc) CloseStdin() error { p.once.Do(func() { close(p.closed) }); return nil }
func (p *memProc) Stdout() io.Reader { return p.outR }
func (p *memProc) Kill()             { _ = p.CloseStdin() }
func (p *memProc) Wait() (int, string) {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exit, p.stderr
}

// localUname is what `uname -sm` prints on the machine running the tests.
func localUname() string {
	os := map[string]string{"darwin": "Darwin", "linux": "Linux"}[runtime.GOOS]
	arch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[runtime.GOARCH]
	return os + " " + arch
}

// otherUname is a supported target that is not this machine.
func otherUname() (string, release.Target) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return "Darwin arm64", release.Target{OS: "darwin", Arch: "arm64"}
	}
	return "Linux aarch64", release.Target{OS: "linux", Arch: "arm64"}
}

type installFixture struct {
	t       *testing.T
	calls   [][]string
	probe   string // stdout of the probe, after the marker
	pexit   int
	pstderr string
	reply   func(sent []byte) (string, int, string) // the install call
	hooks   func(sent []byte) (string, int, string) // the --hooks call
}

func (f *installFixture) dial(_ context.Context, argv []string) (Proc, error) {
	f.calls = append(f.calls, argv)
	switch len(f.calls) {
	case 1:
		out, exit, stderr := "banner line\n---\n"+f.probe, f.pexit, f.pstderr
		return newMemProc(true, func([]byte) (string, int, string) { return out, exit, stderr }), nil
	case 2:
		return newMemProc(false, f.reply), nil
	default:
		return newMemProc(true, f.hooks), nil
	}
}

func okReply(version string, path string) func([]byte) (string, int, string) {
	return func(sent []byte) (string, int, string) {
		return fmt.Sprintf("version=flok %s\nsize=%d\npath=%s\n", version, len(sent), path), 0, ""
	}
}

func installState(t *testing.T, h hosts.Host) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := hosts.Update(dir, func(s *hosts.Set) error { return s.Add(h) }); err != nil {
		t.Fatal(err)
	}
	return dir
}

func flokOf(t *testing.T, dir, name string) string {
	t.Helper()
	set, err := hosts.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := set.Get(name)
	return h.Flok
}

func TestInstallPushesThisFlok(t *testing.T) {
	zeta := hosts.Host{Name: "zeta", Target: "jaro@zeta", Mode: hosts.ModeFull, Enabled: true}
	dir := installState(t, zeta)
	var got []byte
	fx := &installFixture{t: t, probe: localUname() + "\n/home/jaro\nnone\nnone\nclaude\nagents-end\nhooks=0\n",
		reply: func(sent []byte) (string, int, string) {
			got = sent
			return okReply("0.5.0", "/home/jaro/.local/bin/flok")(sent)
		}}
	var log bytes.Buffer
	fetched := false
	res, err := Install(context.Background(), InstallOpts{Cfg: config.Default().Hosts, StateDir: dir, Host: zeta, Dial: fx.dial, LocalVersion: "v0.5.0",
		Local: func() ([]byte, error) { return []byte("THIS-FLOK"), nil },
		Fetch: func(context.Context, string, release.Target, io.Writer) ([]byte, string, error) {
			fetched = true
			return nil, "", nil
		}, Log: &log})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "THIS-FLOK" || fetched || res.Path != "/home/jaro/.local/bin/flok" || res.Version != "0.5.0" || !res.PathChanged || res.Replaced != "" ||
		!strings.HasPrefix(res.Source, "this flok 0.5.0 (") || res.Uname != localUname() || len(res.Agents) != 1 || res.Agents[0] != "claude" || res.HooksBefore != 0 || res.HooksDone != nil {
		t.Fatalf("result %+v, sent %q, fetched %v", res, got, fetched)
	}
	if flokOf(t, dir, "zeta") != "/home/jaro/.local/bin/flok" {
		t.Fatal("the path is recorded in hosts.json")
	}
	probe, install := strings.Join(fx.calls[0], " "), strings.Join(fx.calls[1], " ")
	if !strings.Contains(probe, " -T ") || !strings.Contains(probe, " -- jaro@zeta ") || !strings.Contains(probe, "export PATH=") || !strings.Contains(probe, "uname -sm") || !strings.Contains(probe, `[ -d "$HOME/.claude" ]`) {
		t.Fatalf("probe argv: %s", probe)
	}
	if !strings.HasSuffix(install, installScript) || !strings.Contains(install, " -- jaro@zeta ") || len(fx.calls) != 2 {
		t.Fatalf("install argv: %s (%d calls)", install, len(fx.calls))
	}
	if s := log.String(); !strings.Contains(s, "zeta (jaro@zeta): "+localUname()+", no flok, ~/.claude there") || !strings.Contains(s, "sending this flok 0.5.0") {
		t.Fatalf("log: %s", s)
	}
}

func TestInstallDownloadsForAnotherTarget(t *testing.T) {
	uname, target := otherUname()
	zeta := hosts.Host{Name: "zeta", Target: "zeta", Mode: hosts.ModeFull, Enabled: true}
	for _, tc := range []struct{ local, version, want string }{{"dev", "", ""}, {"0.5.0-1-gbfb2733", "", ""}, {"v0.5.0", "", "v0.5.0"}, {"v0.5.0", "0.4.6", "0.4.6"}} {
		var askedWant string
		var askedTarget release.Target
		fx := &installFixture{t: t, probe: uname + "\n/home/u\n/usr/local/bin/flok\nflok 0.4.4\nagents-end\nhooks=3\n", reply: okReply("0.5.0", "/home/u/.local/bin/flok")}
		var log bytes.Buffer
		res, err := Install(context.Background(), InstallOpts{Cfg: config.Default().Hosts, StateDir: installState(t, zeta), Host: zeta, Dial: fx.dial, LocalVersion: tc.local, Version: tc.version,
			Local: func() ([]byte, error) { t.Fatal("must not push"); return nil, nil },
			Fetch: func(_ context.Context, want string, tg release.Target, _ io.Writer) ([]byte, string, error) {
				askedWant, askedTarget = want, tg
				return []byte("RELEASE"), "v0.5.0", nil
			}, Log: &log})
		if err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		if askedWant != tc.want || askedTarget != target || res.Source != "release 0.5.0 ("+target.String()+")" || res.Replaced != "flok 0.4.4 at /usr/local/bin/flok" || res.HooksBefore != 3 {
			t.Fatalf("%+v: want=%q target=%v res=%+v", tc, askedWant, askedTarget, res)
		}
		if note := strings.Contains(log.String(), "not a release: the host gets the latest release"); note != (tc.want == "") {
			t.Fatalf("%+v: latest note shown=%v\n%s", tc, note, log.String())
		}
	}
	// this machine's target, but --version asked: the release is downloaded, not this binary
	fx := &installFixture{t: t, probe: localUname() + "\n/home/u\nnone\nnone\nagents-end\nhooks=0\n", reply: okReply("0.4.6", "/home/u/.local/bin/flok")}
	res, err := Install(context.Background(), InstallOpts{Cfg: config.Default().Hosts, Host: zeta, Dial: fx.dial, LocalVersion: "v0.5.0", Version: "0.4.6",
		Fetch: func(_ context.Context, want string, _ release.Target, _ io.Writer) ([]byte, string, error) {
			return []byte("OLD"), want, nil
		}})
	if err != nil || res.Version != "0.4.6" || res.PathChanged || res.Warning != "" {
		t.Fatalf("pinned: %+v %v", res, err)
	}
}

func TestInstallFromFileAndErrors(t *testing.T) {
	zeta := hosts.Host{Name: "zeta", Target: "zeta", Mode: hosts.ModeFull, Enabled: true}
	file := filepath.Join(t.TempDir(), "flok-linux")
	if err := os.WriteFile(file, []byte("FROM-FILE"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx := &installFixture{t: t, probe: "Linux armv7l\n/home/u\nnone\nnone\nagents-end\nhooks=0\n", reply: func(sent []byte) (string, int, string) {
		if string(sent) != "FROM-FILE" {
			return "version=flok wrong\nsize=0\npath=/x\n", 0, ""
		}
		return okReply("anything", "/home/u/.local/bin/flok")(sent)
	}}
	if res, err := Install(context.Background(), InstallOpts{Cfg: config.Default().Hosts, Host: zeta, Dial: fx.dial, From: file, LocalVersion: "dev"}); err != nil || res.Source != "file "+file || res.Version != "anything" {
		t.Fatalf("--from: %+v %v", res, err)
	}
	// an unsupported CPU without --from
	fx = &installFixture{t: t, probe: "Linux armv7l\n/home/u\nnone\nnone\nagents-end\nhooks=0\n"}
	if _, err := Install(context.Background(), InstallOpts{Cfg: config.Default().Hosts, Host: zeta, Dial: fx.dial, LocalVersion: "dev"}); err == nil || !strings.Contains(err.Error(), "no flok release for Linux armv7l") {
		t.Fatalf("armv7l: %v", err)
	}
	cases := []struct {
		name    string
		reply   func([]byte) (string, int, string)
		pexit   int
		pstderr string
		want    string
	}{
		{"wrong version", okReply("0.4.4", "/home/u/.local/bin/flok"), 0, "", "reports 0.4.4, expected 0.5.0"},
		{"size", func(sent []byte) (string, int, string) { return "version=flok 0.5.0\nsize=99\npath=/p\n", 0, "" }, 0, "", "size mismatch"},
		{"does not run", func([]byte) (string, int, string) { return "", 4, "/home/u/.local/bin/.flok.new.7: Exec format error" }, 0, "", "does not run there (/home/u/.local/bin/.flok.new.7: Exec format error): a noexec home or the wrong build; the old one is untouched"},
		{"cannot write", func([]byte) (string, int, string) { return "", 3, "mkdir: cannot create directory: Permission denied" }, 0, "", "cannot write to ~/.local/bin there"},
		{"probe auth", nil, 255, "zeta: Permission denied (publickey).", "zeta: needs auth (Permission denied (publickey)); run `ssh zeta` once"},
		{"probe garbage", nil, 0, "", "unexpected answer"},
	}
	for _, tc := range cases {
		probe := localUname() + "\n/home/u\nnone\nnone\nagents-end\nhooks=0\n"
		if tc.name == "probe garbage" {
			probe = "just a banner\n"
		}
		fx := &installFixture{t: t, probe: probe, pexit: tc.pexit, pstderr: tc.pstderr, reply: tc.reply}
		_, err := Install(context.Background(), InstallOpts{Cfg: config.Default().Hosts, Host: zeta, Dial: fx.dial, LocalVersion: "v0.5.0", Local: func() ([]byte, error) { return []byte("B"), nil }})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v (want %q)", tc.name, err, tc.want)
		}
	}
}

func TestInstallInPlaceHooksAndWarnings(t *testing.T) {
	zeta := hosts.Host{Name: "zeta", Target: "zeta", Mode: hosts.ModeFull, Enabled: true, Flok: "/home/u/.local/bin/flok"}
	dir := installState(t, zeta)
	var hookArgv string
	fx := &installFixture{t: t, probe: localUname() + "\n/home/u\n/home/u/.local/bin/flok\nflok 0.4.6\nclaude\ncopilot\nagents-end\nhooks=9\n",
		reply: okReply("0.5.0", "/home/u/.local/bin/flok"),
		hooks: func([]byte) (string, int, string) {
			return "claude: hooks installed\ncopilot: hooks installed\n", 0, ""
		}}
	var log bytes.Buffer
	res, err := Install(context.Background(), InstallOpts{Cfg: config.Default().Hosts, StateDir: dir, Host: zeta, Dial: fx.dial, LocalVersion: "v0.5.0", Hooks: true,
		Local: func() ([]byte, error) { return []byte("B"), nil }, Log: &log})
	if err != nil {
		t.Fatal(err)
	}
	if len(fx.calls) == 3 {
		hookArgv = strings.Join(fx.calls[2], " ")
	}
	if res.PathChanged || res.Replaced != "flok 0.4.6 at /home/u/.local/bin/flok" || strings.Join(res.HooksDone, ",") != "claude,copilot" || res.HooksMissing ||
		!strings.Contains(hookArgv, "/home/u/.local/bin/flok install --claude 2>&1 && /home/u/.local/bin/flok install --copilot 2>&1") || !strings.Contains(log.String(), "  claude: hooks installed") {
		t.Fatalf("in place + hooks: %+v\nhooks argv: %s\nlog: %s", res, hookArgv, log.String())
	}
	// the probe registered the agents; the registered flok path is what the probe checks
	if probe := strings.Join(fx.calls[0], " "); !strings.Contains(probe, "f=/home/u/.local/bin/flok; [ -x \"$f\" ] || f=none") {
		t.Fatalf("probe uses the registered path: %s", probe)
	}
	// --hooks with no agent folder there: nothing to wire, said so
	fx = &installFixture{t: t, probe: localUname() + "\n/home/u\nnone\nnone\nagents-end\nhooks=0\n", reply: okReply("0.5.0", "/home/u/.local/bin/flok")}
	if res, err := Install(context.Background(), InstallOpts{Cfg: config.Default().Hosts, Host: zeta, Dial: fx.dial, LocalVersion: "v0.5.0", Hooks: true, Local: func() ([]byte, error) { return []byte("B"), nil }}); err != nil || !res.HooksMissing || len(fx.calls) != 2 {
		t.Fatalf("no agents: %+v %v calls=%d", res, err, len(fx.calls))
	}
	// a home with a space: the install works, the path cannot be recorded (hosts.json validates), a warning says so
	fx = &installFixture{t: t, probe: localUname() + "\n/Users/j o\nnone\nnone\nagents-end\nhooks=0\n", reply: okReply("0.5.0", "/Users/j o/.local/bin/flok")}
	res, err = Install(context.Background(), InstallOpts{Cfg: config.Default().Hosts, StateDir: dir, Host: zeta, Dial: fx.dial, LocalVersion: "v0.5.0", Local: func() ([]byte, error) { return []byte("B"), nil }})
	if err != nil || !strings.Contains(res.Warning, "not recorded") || flokOf(t, dir, "zeta") != "/home/u/.local/bin/flok" {
		t.Fatalf("space in home: %+v %v", res, err)
	}
}
