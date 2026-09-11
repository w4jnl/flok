// Package launcher creates and attaches the outer tmux server that hosts the sidebar pane and
// the inner client pane, and keeps runtime.json for the nav/layout commands.
package launcher

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"golang.org/x/term"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/tmux"
)

//go:embed outer.tmux.conf.tmpl
var outerTmpl string

// Runtime describes the running outer/inner pair for other flok commands.
type Runtime struct {
	OuterSocket    string    `json:"outer_socket"`
	OuterSession   string    `json:"outer_session"`
	SidebarPane    string    `json:"sidebar_pane"`
	RightPane      string    `json:"right_pane"`
	SidebarPID     int       `json:"sidebar_pid"`
	InnerSocket    string    `json:"inner_socket"`
	InnerClientTTY string    `json:"inner_client_tty"`
	FullWidth      int       `json:"full_width"`
	RailWidth      int       `json:"rail_width"`
	StartedAt      time.Time `json:"started_at"`
}

func RuntimePath() string { return filepath.Join(config.StateDir(), "runtime.json") }
func quitMarker() string  { return filepath.Join(config.StateDir(), "quit") }

func WriteRuntime(r Runtime) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := RuntimePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, RuntimePath())
}

func ReadRuntime() (Runtime, error) {
	var r Runtime
	data, err := os.ReadFile(RuntimePath())
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(data, &r)
}

// UpdateRuntime applies fn to the stored runtime (if any) and writes it back.
func UpdateRuntime(fn func(*Runtime)) error {
	r, err := ReadRuntime()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fn(&r)
	return WriteRuntime(r)
}

// RenderOuterConf fills the embedded template.
func RenderOuterConf(cfg config.Config, bin string) (string, error) {
	tmpl, err := template.New("outer").Parse(outerTmpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = tmpl.Execute(&b, map[string]string{"Bin": bin, "Border": cfg.Theme.CurrentLine,
		"ExtraConf": config.ExpandHome(cfg.Outer.ExtraConf)})
	return b.String(), err
}

// WriteOuterConf renders the outer config into the state dir and returns its path.
func WriteOuterConf(cfg config.Config, bin string) (string, error) {
	text, err := RenderOuterConf(cfg, bin)
	if err != nil {
		return "", err
	}
	p := filepath.Join(config.StateDir(), "outer.conf")
	return p, os.WriteFile(p, []byte(text), 0o644)
}

func termSize() (int, int) {
	for _, f := range []*os.File{os.Stdout, os.Stdin, os.Stderr} {
		if w, h, err := term.GetSize(int(f.Fd())); err == nil && w > 0 && h > 0 {
			return w, h
		}
	}
	return 200, 50
}

// Up creates the outer session (or reuses it) and, unless detach is set, replaces this
// process with `tmux attach`.
func Up(cfg config.Config, bin string, detach bool) error {
	if os.Getenv("TMUX") != "" && !detach {
		return errors.New("flok up: run it from a plain terminal, not inside tmux")
	}
	inner := tmux.NewLocal(cfg.Inner.Socket)
	if _, err := inner.Run("list-sessions"); err != nil {
		if _, err := inner.Run("new-session", "-d", "-s", "main"); err != nil {
			return fmt.Errorf("start inner tmux server: %w", err)
		}
	}
	confPath, err := WriteOuterConf(cfg, bin)
	if err != nil {
		return err
	}
	outer := tmux.NewLocal(cfg.Outer.Socket)
	sess := cfg.Outer.Session
	if _, err := outer.Run("has-session", "-t", sess); err != nil {
		if err := createOuter(cfg, bin, confPath, outer, sess); err != nil {
			return err
		}
	} else if err := ensureSidebar(cfg, bin, outer); err != nil {
		return err
	}
	if detach {
		return nil
	}
	return runAttach(outer, sess)
}

func createOuter(cfg config.Config, bin, confPath string, outer *tmux.Local, sess string) error {
	cols, rows := termSize()
	home, _ := os.UserHomeDir()
	if _, err := outer.Run("-f", confPath, "new-session", "-d", "-s", sess, "-n", "main", "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows),
		"-c", home, "-e", "FLOK_OUTER=1", bin+" _attach-loop"); err != nil {
		return fmt.Errorf("create outer session: %w", err)
	}
	right, err := tmux.Display(outer, sess, "#{pane_id}")
	if err != nil {
		return err
	}
	out, err := outer.Run("split-window", "-hb", "-l", strconv.Itoa(cfg.Sidebar.Width), "-t", right, "-c", home,
		"-e", "FLOK_OUTER=1", "-e", "FLOK_RIGHT_PANE="+right, "-P", "-F", "#{pane_id}", bin+" sidebar")
	if err != nil {
		return fmt.Errorf("create sidebar pane: %w", err)
	}
	sidebar := strings.TrimSpace(out)
	_, _ = outer.Run("set-option", "-p", "-t", sidebar, "remain-on-exit", "on")
	_, _ = outer.Run("select-pane", "-t", right)
	return WriteRuntime(Runtime{OuterSocket: cfg.Outer.Socket, OuterSession: sess, SidebarPane: sidebar, RightPane: right,
		InnerSocket: cfg.Inner.Socket, FullWidth: cfg.Sidebar.Width, RailWidth: cfg.Sidebar.RailWidth, StartedAt: time.Now()})
}

// ensureSidebar respawns a dead sidebar pane in an existing outer session.
func ensureSidebar(cfg config.Config, bin string, outer *tmux.Local) error {
	rt, err := ReadRuntime()
	if err != nil || rt.SidebarPane == "" {
		return nil
	}
	dead, err := tmux.Display(outer, rt.SidebarPane, "#{pane_dead}")
	if err != nil {
		return nil // pane gone; the user can `down` and `up`
	}
	if dead == "1" {
		_, err = outer.Run("respawn-pane", "-k", "-t", rt.SidebarPane, "-e", "FLOK_OUTER=1",
			"-e", "FLOK_RIGHT_PANE="+rt.RightPane, bin+" sidebar")
	}
	return err
}

// runAttach attaches this terminal to the outer session and waits. A deliberate teardown
// (prefix d, `flok down`) kills the outer server, which makes the tmux client exit 1 with
// "[server exited]"; that is a normal end for `flok up`, so it returns nil then and launcher
// chains such as `flok up || tmux attach` do not fall through into plain tmux.
func runAttach(outer *tmux.Local, sess string) error {
	path, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	cmd := exec.Command(path, outer.Argv("attach-session", "-t", sess)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := cmd.Run()
	if _, err := outer.Run("has-session", "-t", sess); err != nil {
		return nil // the outer is gone: detach or down, not a failure
	}
	return runErr
}

// sessionIDs lists the inner server's session ids ("" -> server gone).
func sessionIDs(inner tmux.Client) map[string]bool {
	out, err := inner.Run("list-sessions", "-F", "#{session_id}")
	if err != nil {
		return nil
	}
	ids := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			ids[l] = true
		}
	}
	return ids
}

// AttachLoop runs inside the outer's right pane: it attaches the inner client and decides what
// a client exit means. A destroyed session (kill-session while attached) re-attaches to another
// one; an explicit detach (prefix d) ends the outer like a normal detach would end `tmux attach`,
// unless inner.reattach_on_detach is set; `down` and a vanished inner server end it too.
func AttachLoop(cfg config.Config) error {
	outer := tmux.FromEnv()
	inner := tmux.NewLocal(cfg.Inner.Socket)
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		env = append(env, kv)
	}
	failures := 0
	for {
		before := sessionIDs(inner)
		args := inner.Argv("attach-session")
		if cfg.Inner.Session != "" {
			if _, err := inner.Run("has-session", "-t", cfg.Inner.Session); err == nil {
				args = append(args, "-t", cfg.Inner.Session)
			}
		}
		cmd := exec.Command("tmux", args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.Env = env
		started := time.Now()
		runErr := cmd.Run()
		if _, err := os.Stat(quitMarker()); err == nil {
			_ = os.Remove(quitMarker())
			break
		}
		after := sessionIDs(inner)
		if len(after) == 0 {
			break // inner server gone
		}
		destroyed := false
		for id := range before {
			if !after[id] {
				destroyed = true
			}
		}
		if runErr != nil && time.Since(started) < 2*time.Second {
			failures++
			if failures >= 3 {
				fmt.Fprintln(os.Stderr, "flok: inner attach keeps failing:", runErr)
				time.Sleep(3 * time.Second)
				break
			}
			time.Sleep(time.Second)
			continue
		}
		failures = 0
		if !destroyed && !cfg.Inner.ReattachOnDetach {
			break // the user detached on purpose
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = os.Remove(RuntimePath())
	if outer != nil {
		_, _ = outer.Run("kill-server")
	}
	return nil
}

// Down stops the outer server; the inner server is never touched.
func Down(cfg config.Config) error {
	_ = os.WriteFile(quitMarker(), []byte("1"), 0o644)
	outer := tmux.NewLocal(cfg.Outer.Socket)
	_, err := outer.Run("kill-server")
	_ = os.Remove(quitMarker())
	_ = os.Remove(RuntimePath())
	if err != nil && strings.Contains(err.Error(), "no server running") {
		return nil
	}
	return err
}

// SetTerminalFocus records whether the terminal window showing the outer client is focused.
func SetTerminalFocus(focused bool) error {
	v := "0"
	if focused {
		v = "1"
	}
	return os.WriteFile(filepath.Join(config.StateDir(), "terminal-focus"), []byte(v), 0o644)
}
