// Package tmux is a thin exec-based client for a tmux server plus a one-call snapshot of
// its sessions, panes and clients.
package tmux

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Client runs tmux commands against one server.
type Client interface {
	Run(args ...string) (string, error)
	Label() string
}

// Local addresses a server by socket name (-L) or socket path (-S).
type Local struct {
	Name string
	Path string
	Bin  string
}

func NewLocal(name string) *Local     { return &Local{Name: name, Bin: "tmux"} }
func NewLocalPath(path string) *Local { return &Local{Path: path, Bin: "tmux"} }

// FromEnv returns the server this process runs inside ($TMUX is "socket-path,pid,index"), or nil.
func FromEnv() *Local {
	v := os.Getenv("TMUX")
	if v == "" {
		return nil
	}
	return NewLocalPath(strings.SplitN(v, ",", 2)[0])
}

// Argv prefixes args with the socket selection.
func (l *Local) Argv(args ...string) []string {
	var base []string
	switch {
	case l.Path != "":
		base = []string{"-S", l.Path}
	case l.Name != "":
		base = []string{"-L", l.Name}
	}
	return append(base, args...)
}

func (l *Local) Label() string {
	if l.Path != "" {
		return "-S " + l.Path
	}
	return "-L " + l.Name
}

func (l *Local) Run(args ...string) (string, error) {
	bin := l.Bin
	if bin == "" {
		bin = "tmux"
	}
	cmd := exec.Command(bin, l.Argv(args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("tmux %s %s: %s", l.Label(), strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// Display expands a format for a target with `display-message -p`.
func Display(c Client, target, format string) (string, error) {
	args := []string{"display-message", "-p"}
	if target != "" {
		args = append(args, "-t", target)
	}
	out, err := c.Run(append(args, format)...)
	return strings.TrimSpace(out), err
}
