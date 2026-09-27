package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Remote drives a tmux server on another machine through a command prefix (an ssh argv ending
// in the target): each Run execs the prefix plus one argument, the shell-quoted tmux command
// line, and reads its output. It is a tmux.Client like Local, so TakeSnapshot, capture-pane and
// nav.Go work unchanged; only the floors are slower (a fork plus a round trip per call).
type Remote struct {
	Argv    []string      // the command prefix, up to and including the ssh target
	Prefix  string        // shell text placed before the tmux command line (a PATH export), may be ""
	Socket  string        // remote `tmux -L` socket; "" = tmux's default
	Bin     string        // remote tmux binary; "" = tmux
	Timeout time.Duration // per call; 0 = 15 s
	Name    string        // label in errors ("beta")
	feat    *Features
}

// ExitError is a failed remote command with the pieces remote.Classify needs.
type ExitError struct {
	Code   int // -1 when the process did not exit by itself (timeout, signal)
	Stderr string
	Cmd    string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = fmt.Sprintf("exit %d", e.Code)
	}
	return e.Cmd + ": " + msg
}

// Command is the remote command line for args: `tmux [-L socket] arg…`, each argument quoted
// for a POSIX shell (the remote login shell of ssh), the `;` separators included.
func (r *Remote) Command(args ...string) string {
	bin := r.Bin
	if bin == "" {
		bin = "tmux"
	}
	parts := []string{bin}
	if r.Socket != "" {
		parts = append(parts, "-L", r.Socket)
	}
	return r.Prefix + ShellJoin(append(parts, args...))
}

func (r *Remote) Run(args ...string) (string, error) {
	if len(r.Argv) == 0 {
		return "", errors.New("remote tmux: no command prefix")
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	remote := r.Command(args...)
	argv := append(append([]string{}, r.Argv...), remote)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	code := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		code = ee.ExitCode()
	}
	msg := strings.TrimSpace(stderr.String())
	switch {
	case ctx.Err() != nil:
		msg = fmt.Sprintf("timed out after %s", timeout)
	case msg == "":
		msg = err.Error()
	}
	return stdout.String(), &ExitError{Code: code, Stderr: msg, Cmd: r.Label() + ": " + remote}
}

func (r *Remote) Label() string {
	if r.Name != "" {
		return r.Name
	}
	return "remote"
}

// Features returns the remote tmux's capabilities, asking it for its version once when nobody
// recorded it with SetVersion (Escapes needs it: 3.4+ vis-escapes list-* output).
func (r *Remote) Features() Features {
	if r.feat == nil {
		v := Version{Major: 99, Raw: "unknown"}
		if out, err := r.Run("-V"); err == nil {
			v = ParseVersion(out)
		}
		f := FeaturesFor(v)
		r.feat = &f
	}
	return *r.feat
}

// SetVersion records the remote tmux's version.
func (r *Remote) SetVersion(v Version) {
	f := FeaturesFor(v)
	r.feat = &f
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// ShellQuote makes s one word for a POSIX shell: bare when it has only safe characters, else
// single-quoted with embedded quotes escaped.
func ShellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ShellJoin quotes every argument and joins them with spaces.
func ShellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = ShellQuote(a)
	}
	return strings.Join(q, " ")
}
