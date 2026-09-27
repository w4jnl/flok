package launcher

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/tmux"
)

// HostPane is a remote host's work pane: it lives in the outer window flok-host-<name>, parked
// out of sight, and is swapped next to the sidebar when the host comes to the front. Both ids
// are stable for the life of the outer; only which pane sits in window 0 changes.
type HostPane struct {
	Pane   string `json:"pane"`
	Window string `json:"window"`
}

// HostWindowName is the outer window that parks a host's pane.
func HostWindowName(name string) string { return "flok-host-" + name }

func hostAttachLoopCommand(bin, name string) string {
	return "env FLOK_OUTER=1 " + shellQuote(bin) + " _attach-loop --host " + name
}

// EnsureHostPane finds or creates the parked window of a host and records it in runtime.json.
func EnsureHostPane(outer tmux.Client, sess, bin, name string) (HostPane, error) {
	hp, ok := findHostWindow(outer, sess, name)
	if !ok {
		home, _ := os.UserHomeDir()
		out, err := outer.Run("new-window", "-d", "-t", sess+":", "-n", HostWindowName(name), "-c", home,
			"-P", "-F", "#{window_id}\t#{pane_id}", hostAttachLoopCommand(bin, name))
		if err != nil {
			return HostPane{}, fmt.Errorf("create work pane: %w", err)
		}
		f := strings.SplitN(strings.TrimSpace(out), "\t", 2)
		if len(f) != 2 {
			return HostPane{}, fmt.Errorf("create work pane: unexpected answer %q", out)
		}
		hp = HostPane{Window: f[0], Pane: f[1]}
	}
	return hp, UpdateRuntime(func(r *Runtime) {
		if r.Hosts == nil {
			r.Hosts = map[string]HostPane{}
		}
		r.Hosts[name] = hp
	})
}

// findHostWindow looks the parked window up by name (a `flok reload` or a second `flok up`
// must not create a twin).
func findHostWindow(outer tmux.Client, sess, name string) (HostPane, bool) {
	out, err := outer.Run("list-windows", "-t", sess, "-F", "#{window_id}\t#{window_name}\t#{pane_id}")
	if err != nil {
		return HostPane{}, false
	}
	return parseHostWindows(out)[name], parseHostWindows(out)[name] != HostPane{}
}

// parseHostWindows maps host names to their parked windows from list-windows output.
func parseHostWindows(out string) map[string]HostPane {
	m := map[string]HostPane{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) != 3 || !strings.HasPrefix(f[1], "flok-host-") {
			continue
		}
		m[strings.TrimPrefix(f[1], "flok-host-")] = HostPane{Window: f[0], Pane: f[2]}
	}
	return m
}

// KillHostPane removes a host's parked window and its record. The caller brings the local
// pane back to the front first when the host was there (the window then holds the host's pane).
func KillHostPane(outer tmux.Client, name string) error {
	rt, err := ReadRuntime()
	if err != nil {
		return err
	}
	hp, ok := rt.Hosts[name]
	if ok {
		if _, err := outer.Run("kill-window", "-t", hp.Window); err != nil && !strings.Contains(err.Error(), "can't find") {
			return err
		}
	}
	return UpdateRuntime(func(r *Runtime) { delete(r.Hosts, name) })
}

// hostAttachCommand is what the work pane runs on the host: attach to its tmux (a named session
// is created when missing), with TERM overridden when the host lacks the outer's terminfo.
func hostAttachCommand(h hosts.Host) string {
	parts := []string{"tmux"}
	if h.Socket != "" {
		parts = append(parts, "-L", h.Socket)
	}
	if h.Session != "" {
		parts = append(parts, "new-session", "-A", "-s", h.Session)
	} else {
		parts = append(parts, "attach-session")
	}
	cmd := tmux.ShellJoin(parts)
	if h.Term != "" {
		cmd = "env TERM=" + tmux.ShellQuote(h.Term) + " " + cmd
	}
	return cmd
}

// attachOutcome is what one attach attempt ends with and what the pane says about it.
type attachOutcome struct {
	state remote.State
	line  string
	delay time.Duration
}

// hostAttachOutcome turns an attach's exit into the status line and the pause before the next
// try: a detach inside the remote tmux (or a server without sessions yet) re-attaches after a
// short pause, a lost connection backs off like the data channel, a configuration problem is
// retried once a minute with the fix spelled out.
func hostAttachOutcome(h hosts.Host, exit int, stderr string, ran time.Duration, attempt int, backoffMaxS int) attachOutcome {
	detail := remote.Detail(stderr)
	switch {
	case exit == 0:
		return attachOutcome{remote.Connected, fmt.Sprintf("flok: %s: detached, re-attaching in 3s", h.Name), 3 * time.Second}
	case exit == 1 && (strings.Contains(stderr, "no sessions") || strings.Contains(stderr, "no server running")):
		return attachOutcome{remote.Connected, fmt.Sprintf("flok: %s: no sessions yet, retry in 3s", h.Name), 3 * time.Second}
	}
	st := remote.Classify(exit, stderr)
	if st == remote.NoFlok { // the attach never runs flok; a missing command here is a missing tmux
		st, detail = remote.Unreachable, "no tmux on the host: "+detail
	}
	if st.SlowRetry() {
		return attachOutcome{st, fmt.Sprintf("flok: %s %s: %s (retry in 60s)", h.Name, st.Label(), st.Hint(h)), time.Minute}
	}
	if ran > time.Minute {
		attempt = 0
	}
	delay := remote.Backoff(attempt, backoffMaxS)
	if detail == "" {
		detail = fmt.Sprintf("exit %d", exit)
	}
	return attachOutcome{st, fmt.Sprintf("flok: %s unreachable (%s), retry in %ds", h.Name, detail, int(delay.Seconds())), delay}
}

// AttachLoopHost runs inside a host's parked pane: it keeps `ssh -t <target> tmux attach` alive
// for the host as long as the registry lists it enabled. It never touches runtime.json, the
// snapshot, the bar or the outer server: only the local attach loop ends flok.
func AttachLoopHost(cfg config.Config, name string) error {
	stateDir := config.StateDir()
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		env = append(env, kv)
	}
	attempt := 0
	for {
		set, err := hosts.Load(stateDir)
		if err != nil {
			return err
		}
		h, ok := set.Get(name)
		if !ok || !h.Enabled {
			fmt.Printf("\033[2J\033[Hflok: %s disconnected\n", name)
			return nil
		}
		argv := remote.Argv(cfg.Hosts, stateDir, h, true, hostAttachCommand(h))
		fmt.Print("\033[2J\033[H")
		var tail strings.Builder
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, io.MultiWriter(os.Stderr, &tail)
		cmd.Env = env
		started := time.Now()
		runErr := cmd.Run()
		exit := 0
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exit = ee.ExitCode()
		} else if runErr != nil { // ssh itself could not start
			exit = 255
			tail.WriteString(runErr.Error() + "\n")
		}
		if set, err := hosts.Load(stateDir); err == nil {
			if h, ok := set.Get(name); !ok || !h.Enabled {
				continue // the loop's next round prints the farewell and exits
			}
		}
		out := hostAttachOutcome(h, exit, tail.String(), time.Since(started), attempt, cfg.Hosts.BackoffMaxS)
		if out.state == remote.Connected || time.Since(started) > time.Minute {
			attempt = 0
		} else {
			attempt++
		}
		fmt.Println(out.line)
		// sleep in steps so a disconnect (the registry flips enabled) ends the pane promptly
		for waited := time.Duration(0); waited < out.delay; waited += time.Second {
			time.Sleep(time.Second)
			if set, err := hosts.Load(stateDir); err == nil {
				if h, ok := set.Get(name); !ok || !h.Enabled {
					break
				}
			}
		}
	}
}
