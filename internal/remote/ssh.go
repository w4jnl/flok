// Package remote connects a local flok to remote tmux servers over ssh: it builds the ssh
// command lines, classifies failures, and runs one connection per enabled host (Manager) in
// either mode: full (flok serve on the host, JSON frames over stdio) or plain (the host's
// tmux driven through tmux.Remote). Serve is the other end of the full-mode stream.
package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/tmux"
)

// State of one host's connection.
type State string

const (
	Disabled     State = "disabled"
	Connecting   State = "connecting"
	Connected    State = "connected"
	Stale        State = "stale"        // connected, but no frame for a while
	Unreachable  State = "unreachable"  // network, ssh exit, remote gone; retried with backoff
	Auth         State = "auth"         // ssh needs a password or an unknown key
	HostKey      State = "hostkey"      // host key unknown or changed
	NoFlok       State = "noflok"       // full mode: no flok on the host
	OldFlok      State = "oldflok"      // full mode: a flok without `serve` on the host
	NoServer     State = "noserver"     // the host answers, but its tmux server is not running (for that user/socket)
	Incompatible State = "incompatible" // hello with another protocol version
	Busy         State = "busy"         // another flok already serves that host
)

// SlowRetry says the state is a configuration problem: retried once a minute (busy: every
// 10 s, the other serve is often short-lived), never spammed.
func (s State) SlowRetry() bool {
	switch s {
	case Auth, HostKey, NoFlok, OldFlok, Incompatible, Busy:
		return true
	}
	return false
}

// Label is the short form the sidebar and `flok host status` show.
func (s State) Label() string {
	switch s {
	case Disabled:
		return "off"
	case Auth:
		return "needs auth"
	case HostKey:
		return "host key"
	case NoFlok:
		return "no flok"
	case OldFlok:
		return "old flok"
	case NoServer:
		return "no tmux server"
	}
	return string(s)
}

// Hint is the one-line remedy for a configuration state, "" otherwise.
func (s State) Hint(h hosts.Host) string {
	switch s {
	case Auth:
		return "run `ssh " + h.Target + "` once (keys or an agent; flok never prompts)"
	case HostKey:
		return "run `ssh " + h.Target + "` once to accept the host key"
	case NoFlok:
		return "put flok there from here: `flok host install " + h.Name + "` (I in the servers panel); or `flok host set " + h.Name + " --flok <path>`, or --mode plain"
	case OldFlok:
		return "upgrade it from here: `flok host install " + h.Name + "` (I in the servers panel), or --mode plain"
	case NoServer:
		return "the sidebar's work pane starts one there once connected ([hosts] session, or --session); otherwise start tmux as " + h.Target + ", or point --target/--socket at the tmux that has the sessions"
	case Incompatible:
		return "`flok host install " + h.Name + "` puts this flok's version there, or use --mode plain"
	case Busy:
		return "another flok serves this host; stop it or disconnect here"
	}
	return ""
}

// Argv builds the ssh command line for h: BatchMode (never a prompt), the keepalive that ends
// a dead connection in about 45 s, flok's own ControlMaster socket when multiplexing, then
// the user's [hosts] ssh_options (theirs come first, and ssh keeps the first value of an
// option, so they win), -t or -T, "--" and the target, and the remote command when given.
func Argv(cfg config.Hosts, stateDir string, h hosts.Host, tty bool, remoteCmd string) []string {
	bin := cfg.SSH
	if bin == "" {
		bin = "ssh"
	}
	timeout := cfg.ConnectTimeoutS
	if timeout <= 0 {
		timeout = 10
	}
	argv := []string{bin}
	argv = append(argv, cfg.SSHOptions...)
	argv = append(argv, "-o", "BatchMode=yes", "-o", fmt.Sprintf("ConnectTimeout=%d", timeout),
		"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=2")
	if cfg.Multiplex {
		if dir, ok := ControlDir(stateDir); ok {
			argv = append(argv, "-o", "ControlMaster=auto", "-o", "ControlPersist=60", "-o", "ControlPath="+filepath.Join(dir, "%C"))
		}
	}
	if tty {
		argv = append(argv, "-t")
	} else {
		argv = append(argv, "-T")
	}
	argv = append(argv, "--", h.Target)
	if remoteCmd != "" {
		argv = append(argv, remoteCmd)
	}
	return argv
}

// PathPrefix is the shell text put before every command run on a host: it appends [hosts]
// remote_path to the remote's PATH, because a non-interactive ssh shell sees only the system
// PATH and Homebrew's tmux or flok would not be found. "" when remote_path is empty.
func PathPrefix(cfg config.Hosts) string {
	p := strings.TrimSpace(cfg.RemotePath)
	if p == "" || strings.ContainsAny(p, "\"';\n") {
		return ""
	}
	return `export PATH="$PATH:` + p + `"; `
}

// WithPath prefixes a remote command with PathPrefix.
func WithPath(cfg config.Hosts, cmd string) string { return PathPrefix(cfg) + cmd }

// ControlDir is where the ssh control sockets live ($FLOK_STATE/ssh, mode 0700). ok is false
// when a socket path there would exceed the Unix socket limit (about 104 bytes on macOS);
// multiplexing is then skipped rather than failing every connection.
func ControlDir(stateDir string) (string, bool) {
	dir := filepath.Join(stateDir, "ssh")
	if len(dir)+1+40 > 100 { // "%C" expands to a 40-character hash
		return dir, false
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return dir, false
	}
	return dir, true
}

// Classify maps an ssh (or remote command) exit to a connection state.
func Classify(exit int, stderr string) State {
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "host key verification failed"), strings.Contains(low, "remote host identification has changed"),
		strings.Contains(low, "host key for") && strings.Contains(low, "has changed"):
		return HostKey
	case strings.Contains(low, "permission denied"), strings.Contains(low, "too many authentication failures"),
		strings.Contains(low, "authentication failed"):
		return Auth
	case strings.Contains(low, "no server running"), strings.Contains(low, "no sessions"): // tmux itself answered
		return NoServer
	case strings.Contains(low, "unknown command"): // a flok from before `serve`
		return OldFlok
	case exit == 127, strings.Contains(low, "command not found"), strings.Contains(low, ": not found"):
		return NoFlok
	}
	return Unreachable
}

// ClassifyErr classifies a tmux.Remote failure (or any error) and extracts its detail line.
func ClassifyErr(err error) (State, string) {
	var ee *tmux.ExitError
	if errors.As(err, &ee) {
		return Classify(ee.Code, ee.Stderr), Detail(ee.Stderr)
	}
	if err == nil {
		return Unreachable, ""
	}
	return Unreachable, Detail(err.Error())
}

// Detail is the last non-empty stderr line without ssh's prefixes, short enough for a row.
func Detail(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	line := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			line = l
			break
		}
	}
	line = strings.TrimPrefix(line, "ssh: ")
	if i := strings.Index(line, "port 22: "); i >= 0 && strings.HasPrefix(line, "connect to host") {
		line = line[i+len("port 22: "):]
	}
	if m := targetPrefixRe.FindStringSubmatch(line); m != nil { // "jaro@beta: Permission denied (publickey)."
		line = m[1]
	}
	line = strings.TrimSuffix(line, ".")
	if len(line) > 80 {
		line = line[:77] + "..."
	}
	return line
}

var targetPrefixRe = regexp.MustCompile(`^[A-Za-z0-9._@:-]+: ((?:Permission denied|Too many authentication failures|Authentication failed).*)$`)

// Backoff is the reconnect delay after attempt failures in a row: 1, 2, 4 … maxS seconds.
func Backoff(attempt int, maxS int) time.Duration {
	if maxS <= 0 {
		maxS = 30
	}
	if attempt > 10 {
		attempt = 10
	}
	d := time.Second << uint(attempt)
	if d > time.Duration(maxS)*time.Second {
		d = time.Duration(maxS) * time.Second
	}
	return d
}

// CloseMasters ends the ControlMaster connections flok opened (sockets under $FLOK_STATE/ssh),
// so `flok down` leaves no ssh behind. Failures are ignored: a master that is not running is
// the goal.
func CloseMasters(cfg config.Hosts, stateDir string) {
	dir, ok := ControlDir(stateDir)
	if !cfg.Multiplex || !ok {
		return
	}
	set, err := hosts.Load(stateDir)
	if err != nil {
		return
	}
	bin := cfg.SSH
	if bin == "" {
		bin = "ssh"
	}
	for _, h := range set.Hosts {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		args := append(append([]string{}, cfg.SSHOptions...), "-o", "ControlPath="+filepath.Join(dir, "%C"), "-O", "exit", "--", h.Target)
		_ = exec.CommandContext(ctx, bin, args...).Run()
		cancel()
	}
}

// Proc is a running remote command: what the manager needs from an ssh process.
type Proc interface {
	Stdin() io.Writer
	CloseStdin() error // EOF to the remote command (a streamed file ends, a serve leaves); Kill closes it too
	Stdout() io.Reader
	Wait() (exit int, stderr string) // blocks until the process ended; exit -1 = killed
	Kill()
}

// Dialer starts a command; tests substitute in-memory processes for ssh.
type Dialer func(ctx context.Context, argv []string) (Proc, error)

// Exec is the real Dialer: argv is executed directly, never through a local shell.
func Exec(ctx context.Context, argv []string) (Proc, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // Kill reaches the whole tree (a shell wrapper and its child)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &execProc{cmd: cmd, stdin: stdin, stdout: stdout, done: make(chan struct{})}
	cmd.Stderr = &p.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
			p.Kill()
		case <-p.done:
		}
	}()
	return p, nil
}

type execProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
	stderr tailBuffer
	once   sync.Once
	done   chan struct{}
	exit   int
}

func (p *execProc) Stdin() io.Writer  { return p.stdin }
func (p *execProc) CloseStdin() error { return p.stdin.Close() }
func (p *execProc) Stdout() io.Reader { return p.stdout }
func (p *execProc) Wait() (int, string) {
	p.once.Do(func() {
		err := p.cmd.Wait()
		p.exit = -1
		var ee *exec.ExitError
		if err == nil {
			p.exit = 0
		} else if errors.As(err, &ee) && ee.ExitCode() >= 0 {
			p.exit = ee.ExitCode()
		}
		close(p.done)
	})
	return p.exit, p.stderr.String()
}

// Kill ends the process: stdin closes first, which lets a remote `flok serve` see EOF and leave
// cleanly (restoring the keys it bound), then after a short grace the whole process group is
// killed, wrapper shells included.
func (p *execProc) Kill() {
	_ = p.stdin.Close()
	if p.cmd.Process == nil {
		return
	}
	select {
	case <-p.done:
		return
	case <-time.After(killGrace):
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	_ = p.cmd.Process.Kill()
}

// killGrace is how long a dialed process gets to exit on EOF before it is killed.
const killGrace = 700 * time.Millisecond

// tailBuffer keeps the last few KB written to it: enough stderr to classify, never unbounded.
type tailBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Write(p)
	if b.buf.Len() > 8<<10 {
		rest := b.buf.Bytes()[b.buf.Len()-4<<10:]
		b.buf = *bytes.NewBuffer(append([]byte{}, rest...))
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
