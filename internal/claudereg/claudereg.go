// Package claudereg reads Claude Code's own registry of running sessions (`claude agents
// --json`) and maps entries to tmux panes through the controlling tty of their pid.
package claudereg

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/w4jnl/flok/internal/agent"
)

type Entry struct {
	PID       int    `json:"pid"`
	ID        string `json:"id"`
	Cwd       string `json:"cwd"`
	Kind      string `json:"kind"`
	StartedAt int64  `json:"startedAt"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	Status    string `json:"status"` // idle | busy
	State     string `json:"state"`  // blocked | done (background kinds)
	TTY       string `json:"-"`      // filled by ByTTY
}

// State maps registry fields to an agent state.
func (e Entry) AgentState() agent.State {
	switch {
	case e.State == "blocked":
		return agent.Blocked
	case e.Status == "busy":
		return agent.Working
	case e.Status == "idle":
		return agent.Idle
	}
	return agent.Unknown
}

// Binary locates the claude executable: PATH first, then the usual install locations. The
// sidebar often runs with a minimal PATH (launched from a window manager), where PATH alone fails.
func Binary() (string, error) {
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, c := range []string{
		filepath.Join(home, ".local", "bin", "claude"),
		"/opt/homebrew/bin/claude", "/usr/local/bin/claude",
		filepath.Join(home, ".claude", "local", "claude"),
	} {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	return "", errors.New("claude not found in PATH or the usual install locations")
}

// List runs `claude agents --json`.
func List(timeout time.Duration) ([]Entry, error) {
	bin, err := Binary()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "agents", "--json").Output()
	if err != nil {
		return nil, err
	}
	var entries []Entry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

type proc struct {
	ppid int
	tty  string
}

// procTable returns pid -> (ppid, tty) for all processes.
func procTable() (map[int]proc, error) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,tty=").Output()
	if err != nil {
		return nil, err
	}
	table := map[int]proc{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		table[pid] = proc{ppid: ppid, tty: f[2]}
	}
	return table, nil
}

// ByTTY maps live entries to their controlling tty ("/dev/ttys012"), walking up the parent
// chain for processes without one.
func ByTTY(entries []Entry) map[string]Entry {
	table, err := procTable()
	if err != nil {
		return nil
	}
	out := map[string]Entry{}
	for _, e := range entries {
		if e.PID == 0 {
			continue
		}
		pid := e.PID
		for hops := 0; hops < 8; hops++ {
			p, ok := table[pid]
			if !ok {
				break
			}
			if p.tty != "" && p.tty != "??" && p.tty != "-" {
				e.TTY = "/dev/" + p.tty
				if prev, exists := out[e.TTY]; !exists || e.StartedAt > prev.StartedAt {
					out[e.TTY] = e
				}
				break
			}
			pid = p.ppid
		}
	}
	return out
}
