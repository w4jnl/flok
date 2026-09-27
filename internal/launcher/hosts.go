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

// WorkPanes is what the outer's panes run: the local attach loop and one loop per host,
// wherever the swaps have put them. Panes are known by their start command, never by the window
// they sit in: swaps move panes between the windows named after the hosts, so after a few
// switches a host's pane may sit in another host's window. Extra holds duplicates (a second
// loop for the same host, from a race or an older build), which are stray and get killed.
type WorkPanes struct {
	Local  string              // pane of `flok _attach-loop`
	Hosts  map[string]string   // host name -> pane of `flok _attach-loop --host <name>`
	Extra  map[string][]string // host name -> further panes running the same loop
	Window map[string]string   // pane id -> window id
}

// ScanWorkPanes lists the outer's panes once and sorts them by what they run. The format has
// no control characters (tmux 3.4 vis-escapes list-* output) and the answer is decoded like a
// snapshot: ids never contain spaces, so the command is everything after the second one.
func ScanWorkPanes(outer tmux.Client) (WorkPanes, error) {
	out, err := outer.Run("list-panes", "-a", "-F", "#{pane_id} #{window_id} #{pane_start_command}")
	if err != nil {
		return WorkPanes{}, err
	}
	return parseWorkPanes(tmux.Decode(out, tmux.Escapes(outer))), nil
}

func parseWorkPanes(out string) WorkPanes {
	w := WorkPanes{Hosts: map[string]string{}, Extra: map[string][]string{}, Window: map[string]string{}}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.SplitN(line, " ", 3)
		if len(f) != 3 {
			continue
		}
		id, win := f[0], f[1]
		cmd := strings.Trim(strings.TrimSpace(f[2]), `"`) // tmux prints the command in double quotes
		w.Window[id] = win
		switch {
		case strings.HasSuffix(cmd, " _attach-loop"):
			w.Local = id
		case strings.Contains(cmd, " _attach-loop --host "):
			name := strings.TrimSpace(cmd[strings.Index(cmd, " _attach-loop --host ")+len(" _attach-loop --host "):])
			if _, dup := w.Hosts[name]; dup {
				w.Extra[name] = append(w.Extra[name], id)
			} else {
				w.Hosts[name] = id
			}
		}
	}
	return w
}

// EnsureHostPane finds or creates the work pane of a host and records it in runtime.json. An
// existing pane is recognised by its start command; a second loop for the same host is a stray
// (a race, an older build) and is killed, so reloads never leave twins.
func EnsureHostPane(outer tmux.Client, sess, bin, name string) (HostPane, error) {
	wp, err := ScanWorkPanes(outer)
	if err != nil {
		return HostPane{}, err
	}
	for _, extra := range wp.Extra[name] {
		_, _ = outer.Run("kill-pane", "-t", extra)
	}
	hp := HostPane{Pane: wp.Hosts[name], Window: wp.Window[wp.Hosts[name]]}
	if hp.Pane == "" {
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
		if wp.Local != "" {
			r.LocalPane = wp.Local
		}
	})
}

// KillHostPane removes a host's work pane (and any stray twin) and its record; the window it
// sat in closes with it when it was alone there. A host still in front, its pane next to the
// sidebar, is refused: the caller swaps back to local first.
func KillHostPane(outer tmux.Client, name string) error {
	wp, err := ScanWorkPanes(outer)
	if err != nil {
		return err
	}
	rt, _ := ReadRuntime()
	pane := wp.Hosts[name]
	if pane != "" && rt.SidebarPane != "" {
		if out, err := outer.Run("list-panes", "-t", rt.SidebarPane, "-F", "#{pane_id}"); err == nil {
			for _, id := range strings.Fields(out) {
				if id == pane {
					return fmt.Errorf("host %s is in front; switch to local first", name)
				}
			}
		}
	}
	for _, id := range append(wp.Extra[name], pane) {
		if id != "" {
			if _, err := outer.Run("kill-pane", "-t", id); err != nil && !strings.Contains(err.Error(), "can't find") {
				return err
			}
		}
	}
	return UpdateRuntime(func(r *Runtime) { delete(r.Hosts, name) })
}

// hostAttachCommand is what the work pane runs on the host: attach to its tmux, and when no
// server runs there create a session (the host's --session, else [hosts] session), so a host
// that only has tmux installed still gets a server the moment it is connected. A host with its
// own session name always lands in that session. TERM is overridden when the host lacks the
// outer's terminfo.
func hostAttachCommand(h hosts.Host, defaultSession string) string {
	tm := []string{"tmux"}
	if h.Socket != "" {
		tm = append(tm, "-L", h.Socket)
	}
	var cmd string
	switch {
	case h.Session != "":
		cmd = tmux.ShellJoin(append(tm, "new-session", "-A", "-s", h.Session))
	case defaultSession != "":
		cmd = tmux.ShellJoin(append(tm, "attach-session")) + " || " + tmux.ShellJoin(append(tm, "new-session", "-s", defaultSession))
	default:
		cmd = tmux.ShellJoin(append(tm, "attach-session"))
	}
	if h.Term != "" {
		cmd = "export TERM=" + tmux.ShellQuote(h.Term) + "; " + cmd
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
		st, detail = remote.Unreachable, "no tmux on the host's PATH: "+detail
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
		argv := remote.Argv(cfg.Hosts, stateDir, h, true, remote.WithPath(cfg.Hosts, hostAttachCommand(h, cfg.Hosts.Session)))
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
