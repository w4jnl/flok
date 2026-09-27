package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
)

const hostUsage = `usage: flok host <command>

  add <name> <target> [--mode full|plain] [--socket name] [--session name]
                      [--flok /path/to/flok] [--term name] [--disabled]
              register a remote tmux server; <target> is what ssh accepts (alias, host,
              user@host). full runs flok serve on the host (hook states, needs flok there),
              plain drives its tmux over ssh (titles and screen rules only)
                      --term sets TERM for the attach when the host lacks the tmux-256color
                      terminfo (flok doctor tells; screen-256color usually works)
  set <name> [--mode full|plain] [--target t] [--socket s] [--session s] [--flok p] [--term t]
              change a host in place (an empty value clears the field); the sidebar reconnects
  remove <name>       forget the host (and its local cache)
  connect <name>      enable the host: the sidebar connects now and at every flok up
  disconnect <name>   disable the host: disconnect and stay disconnected across restarts
  list [--json|--names]
              show the registered hosts
  status [--json]
              connect to every enabled host once and report what answers (flok on the host,
              its tmux, agents); the sidebar keeps its own connections, this is a check
  front <name>|local [--focus]
              bring that host's work pane next to the running sidebar (--focus also brings
              the terminal window to the front, as the menu bar does)

Hosts live in the state dir (hosts.json); [hosts] in config.toml holds the ssh defaults.`

// hostCmd is `flok host` with its environment injected for tests.
type hostCmd struct {
	dir       string
	cfg       config.Config
	now       func() time.Time
	out, errw io.Writer
	manager   func(remote.Deps) *remote.Manager // nil = remote.New
}

func runHost(cfg config.Config, args []string) int {
	return hostCmd{dir: config.StateDir(), cfg: cfg, now: time.Now, out: os.Stdout, errw: os.Stderr}.run(args)
}

func (c hostCmd) run(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(c.errw, hostUsage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "add":
		return c.add(args[1:])
	case "set":
		return c.set(args[1:])
	case "remove", "rm":
		return c.remove(args[1:])
	case "connect", "enable":
		return c.setEnabled(args[1:], true)
	case "disconnect", "disable":
		return c.setEnabled(args[1:], false)
	case "list", "ls":
		return c.list(args[1:])
	case "status":
		return c.status(args[1:])
	case "front":
		return c.front(args[1:])
	}
	fmt.Fprintf(c.errw, "flok host: unknown command %q\n\n%s\n", args[0], hostUsage)
	return 2
}

func (c hostCmd) fail(err error) int {
	fmt.Fprintln(c.errw, "flok host:", err)
	return 1
}

func (c hostCmd) add(args []string) int {
	h := hosts.Host{Mode: hosts.ModeFull, Enabled: true, AddedAt: c.now()}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(c.errw, "flok host add: %s needs a value\n", a)
				return "", false
			}
			i++
			return args[i], true
		}
		var v string
		var ok bool
		switch a {
		case "--mode":
			if v, ok = value(); !ok {
				return 2
			}
			h.Mode = hosts.Mode(v)
		case "--socket":
			if v, ok = value(); !ok {
				return 2
			}
			h.Socket = v
		case "--session":
			if v, ok = value(); !ok {
				return 2
			}
			h.Session = v
		case "--flok":
			if v, ok = value(); !ok {
				return 2
			}
			h.Flok = v
		case "--term":
			if v, ok = value(); !ok {
				return 2
			}
			h.Term = v
		case "--disabled":
			h.Enabled = false
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(c.errw, "flok host add: unknown flag %s\n\n%s\n", a, hostUsage)
				return 2
			}
			pos = append(pos, a)
		}
	}
	if len(pos) != 2 {
		fmt.Fprintf(c.errw, "flok host add: need <name> and <target>\n\n%s\n", hostUsage)
		return 2
	}
	h.Name, h.Target = pos[0], pos[1]
	if _, err := hosts.Update(c.dir, func(s *hosts.Set) error { return s.Add(h) }); err != nil {
		return c.fail(err)
	}
	state := "connects at the next flok up (or now, while the sidebar runs)"
	if !h.Enabled {
		state = "disabled; flok host connect " + h.Name + " enables it"
	}
	fmt.Fprintf(c.out, "added %s (%s, %s): %s\n", h.Name, h.Target, h.Mode, state)
	return 0
}

func (c hostCmd) remove(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(c.errw, "flok host remove: need <name>\n\n%s\n", hostUsage)
		return 2
	}
	name := args[0]
	if err := hosts.ValidateName(name); err != nil {
		return c.fail(err)
	}
	if _, err := hosts.Update(c.dir, func(s *hosts.Set) error {
		if !s.Remove(name) {
			return fmt.Errorf("no host %q (flok host list)", name)
		}
		return nil
	}); err != nil {
		return c.fail(err)
	}
	_ = os.RemoveAll(hosts.Dir(c.dir, name))
	fmt.Fprintf(c.out, "removed %s\n", name)
	return 0
}

func (c hostCmd) setEnabled(args []string, on bool) int {
	verb := "connect"
	if !on {
		verb = "disconnect"
	}
	if len(args) != 1 {
		fmt.Fprintf(c.errw, "flok host %s: need <name>\n\n%s\n", verb, hostUsage)
		return 2
	}
	name := args[0]
	if err := hosts.ValidateName(name); err != nil {
		return c.fail(err)
	}
	if _, err := hosts.Update(c.dir, func(s *hosts.Set) error {
		if !s.SetEnabled(name, on) {
			return fmt.Errorf("no host %q (flok host list)", name)
		}
		return nil
	}); err != nil {
		return c.fail(err)
	}
	if on {
		fmt.Fprintf(c.out, "%s enabled: the sidebar connects now (or at the next flok up)\n", name)
	} else {
		fmt.Fprintf(c.out, "%s disabled: disconnected, stays off until flok host connect %s\n", name, name)
	}
	return 0
}

func (c hostCmd) list(args []string) int {
	set, err := hosts.Load(c.dir)
	if err != nil {
		return c.fail(err)
	}
	switch {
	case len(args) == 1 && args[0] == "--json":
		enc := json.NewEncoder(c.out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(set)
		return 0
	case len(args) == 1 && args[0] == "--names":
		for _, n := range set.Names() {
			fmt.Fprintln(c.out, n)
		}
		return 0
	case len(args) != 0:
		fmt.Fprintf(c.errw, "flok host list: unknown argument %s\n\n%s\n", args[0], hostUsage)
		return 2
	}
	if len(set.Hosts) == 0 {
		fmt.Fprintln(c.out, "no remote hosts; add one with: flok host add <name> <user@host>")
		return 0
	}
	tw := tabwriter.NewWriter(c.out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTARGET\tMODE\tENABLED\tLAST CONNECTED")
	for _, h := range set.Hosts {
		enabled, last := "yes", "never"
		if !h.Enabled {
			enabled = "no"
		}
		if !h.LastConnected.IsZero() {
			last = h.LastConnected.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", h.Name, h.Target, h.Mode, enabled, last)
	}
	_ = tw.Flush()
	return 0
}

// status connects to every enabled host once, waits for each to answer (a hello and a first
// snapshot) or fail, and prints the result.
func (c hostCmd) status(args []string) int {
	asJSON := len(args) == 1 && args[0] == "--json"
	if len(args) > 0 && !asJSON {
		fmt.Fprintf(c.errw, "flok host status: unknown argument %s\n\n%s\n", args[0], hostUsage)
		return 2
	}
	set, err := hosts.Load(c.dir)
	if err != nil {
		return c.fail(err)
	}
	enabled := set.Enabled()
	if len(enabled) == 0 {
		if asJSON {
			fmt.Fprintln(c.out, "[]")
		} else {
			fmt.Fprintln(c.out, "no enabled hosts (flok host list)")
		}
		return 0
	}
	sink := make(chan remote.Msg, 256)
	deps := remote.Deps{Cfg: c.cfg, StateDir: c.dir, Sink: sink, Adapters: agent.Enabled(c.cfg.Agents.Enabled)}
	var m *remote.Manager
	if c.manager != nil {
		m = c.manager(deps)
	} else {
		m = remote.New(deps)
	}
	m.Apply(set)
	pending := map[string]bool{}
	for _, h := range enabled {
		pending[h.Name] = true
	}
	timeout := time.Duration(c.cfg.Hosts.ConnectTimeoutS+10) * time.Second
	deadline := time.After(timeout)
	for len(pending) > 0 {
		select {
		case msg := <-sink:
			if !pending[msg.Host] {
				continue
			}
			if msg.Snap != nil || (msg.State != remote.Connecting && msg.State != remote.Connected && msg.State != remote.Stale) {
				delete(pending, msg.Host)
			}
		case <-deadline:
			pending = nil
		}
	}
	status := m.Status()
	m.Close()
	rc := 0
	var rows []remote.Status
	for _, h := range enabled {
		st := status[h.Name]
		if st.State != remote.Connected {
			rc = 1
		}
		rows = append(rows, st)
	}
	if asJSON {
		enc := json.NewEncoder(c.out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rows)
		return rc
	}
	tw := tabwriter.NewWriter(c.out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tMODE\tSTATE\tREMOTE\tAGENTS\tDETAIL")
	for i, st := range rows {
		h := enabled[i]
		remoteCol, agents := "-", "-"
		if st.Hello != nil {
			remoteCol = "tmux " + st.Hello.TmuxVersion
			if st.Hello.Version != "" && st.Hello.Version != "plain" {
				remoteCol = "flok " + strings.TrimPrefix(st.Hello.Version, "v") + ", " + remoteCol
			}
		}
		if st.State == remote.Connected {
			agents = fmt.Sprint(st.Agents)
		}
		detail := st.Detail
		if st.State == remote.Connecting {
			detail = "no answer within " + timeout.String()
		}
		if hint := st.State.Hint(h); hint != "" {
			detail = strings.TrimSuffix(strings.TrimSpace(detail), ".")
			if detail != "" {
				detail += "; "
			}
			detail += hint
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", st.Host, h.Mode, st.State.Label(), remoteCol, agents, detail)
	}
	_ = tw.Flush()
	return rc
}

// front asks the running sidebar to bring a host's work pane next to it.
func (c hostCmd) front(args []string) int {
	name, doFocus := "", false
	for _, a := range args {
		switch {
		case a == "--focus":
			doFocus = true
		case strings.HasPrefix(a, "-") || name != "":
			fmt.Fprintf(c.errw, "flok host front: need <name> or local\n\n%s\n", hostUsage)
			return 2
		default:
			name = a
		}
	}
	if name == "" {
		fmt.Fprintf(c.errw, "flok host front: need <name> or local\n\n%s\n", hostUsage)
		return 2
	}
	host := name
	if name == agent.LocalHost {
		host = ""
	} else {
		if err := hosts.ValidateName(name); err != nil {
			return c.fail(err)
		}
		set, err := hosts.Load(c.dir)
		if err != nil {
			return c.fail(err)
		}
		if _, ok := set.Get(name); !ok {
			return c.fail(fmt.Errorf("no host %q (flok host list)", name))
		}
	}
	if _, f := snapshot.Load(c.dir, c.now()); f != snapshot.Fresh {
		return c.fail(errors.New("the sidebar is not running (flok up)"))
	}
	if err := state.New(c.dir).WriteRequest(state.Request{Cmd: "front", Host: host}); err != nil {
		return c.fail(err)
	}
	if doFocus {
		if rt, err := launcher.ReadRuntime(); err == nil {
			_ = focusTerminal(c.cfg, rt, "")
		}
	}
	fmt.Fprintf(c.out, "%s comes to the front\n", name)
	return 0
}

// set edits a registered host; the sidebar picks the change up from the file and reconnects.
func (c hostCmd) set(args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(c.errw, "flok host set: need <name>\n\n%s\n", hostUsage)
		return 2
	}
	name := args[0]
	if err := hosts.ValidateName(name); err != nil {
		return c.fail(err)
	}
	var edits []func(*hosts.Host)
	for i := 1; i < len(args); i++ {
		flag := args[i]
		if i+1 >= len(args) || !strings.HasPrefix(flag, "--") {
			fmt.Fprintf(c.errw, "flok host set: %s needs a value\n\n%s\n", flag, hostUsage)
			return 2
		}
		i++
		v := args[i]
		switch flag {
		case "--mode":
			edits = append(edits, func(h *hosts.Host) { h.Mode = hosts.Mode(v) })
		case "--target":
			edits = append(edits, func(h *hosts.Host) { h.Target = v })
		case "--socket":
			edits = append(edits, func(h *hosts.Host) { h.Socket = v })
		case "--session":
			edits = append(edits, func(h *hosts.Host) { h.Session = v })
		case "--flok":
			edits = append(edits, func(h *hosts.Host) { h.Flok = v })
		case "--term":
			edits = append(edits, func(h *hosts.Host) { h.Term = v })
		default:
			fmt.Fprintf(c.errw, "flok host set: unknown flag %s\n\n%s\n", flag, hostUsage)
			return 2
		}
	}
	if len(edits) == 0 {
		fmt.Fprintf(c.errw, "flok host set: nothing to change\n\n%s\n", hostUsage)
		return 2
	}
	set, err := hosts.Update(c.dir, func(s *hosts.Set) error {
		return s.Set(name, func(h *hosts.Host) {
			for _, e := range edits {
				e(h)
			}
		})
	})
	if err != nil {
		return c.fail(err)
	}
	h, _ := set.Get(name)
	fmt.Fprintf(c.out, "%s: %s, %s\n", h.Name, h.Target, h.Mode)
	return 0
}
