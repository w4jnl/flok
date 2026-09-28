package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/keys"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

// install is `flok host install <name> [--from <file>] [--version <v>] [--hooks] [--open] [--wait]`:
// put this flok (or the matching release, or a file) on the host over ssh, into ~/.local/bin
// there, which is on [hosts] remote_path, so nothing on the host needs configuring.
func (c hostCmd) install(args []string) int {
	var name, from, version string
	var hooks, open, wait bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(c.errw, "flok host install: %s needs a value\n", a)
				return "", false
			}
			i++
			return args[i], true
		}
		var ok bool
		switch a {
		case "--from":
			if from, ok = value(); !ok {
				return 2
			}
		case "--version":
			if version, ok = value(); !ok {
				return 2
			}
		case "--hooks":
			hooks = true
		case "--open":
			open = true
		case "--wait":
			wait = true
		default:
			if strings.HasPrefix(a, "-") || name != "" {
				fmt.Fprintf(c.errw, "flok host install: unexpected %q\n\n%s\n", a, hostUsage)
				return 2
			}
			name = a
		}
	}
	if name == "" {
		fmt.Fprintf(c.errw, "flok host install: need <name>\n\n%s\n", hostUsage)
		return 2
	}
	h, err := c.lookupHost(name)
	if err != nil {
		return c.fail(err)
	}
	if open {
		return c.openInstall(h.Name)
	}
	rc := c.runInstall(h, from, version, hooks)
	if wait {
		fmt.Fprintln(c.out, "press Enter to close")
		if c.in != nil {
			_, _ = bufio.NewReader(c.in).ReadString('\n')
		}
	}
	return rc
}

// lookupHost finds a registered host by name, in any case.
func (c hostCmd) lookupHost(name string) (hosts.Host, error) {
	if err := hosts.ValidateName(name); err != nil {
		return hosts.Host{}, err
	}
	set, err := hosts.Load(c.dir)
	if err != nil {
		return hosts.Host{}, err
	}
	h, ok := set.Get(name)
	if !ok {
		return hosts.Host{}, fmt.Errorf("no host %q (flok host list)", name)
	}
	return h, nil
}

// runInstall does the install and says what to expect next: the sidebar reconnects by itself
// when the recorded path changed, is asked to when it did not, or the host waits for `flok up`.
func (c hostCmd) runInstall(h hosts.Host, from, version string, hooks bool) int {
	installer := c.installer
	if installer == nil {
		installer = remote.Install
	}
	res, err := installer(context.Background(), remote.InstallOpts{Cfg: c.cfg.Hosts, StateDir: c.dir, Host: h,
		From: from, Version: version, Hooks: hooks, LocalVersion: Version, Log: c.out})
	if err != nil {
		return c.fail(err)
	}
	line := "installed flok " + res.Version + " at " + res.Path
	if res.Replaced != "" {
		line += " (replaced " + res.Replaced + ")"
	}
	fmt.Fprintln(c.out, line)
	if res.Warning != "" {
		fmt.Fprintln(c.errw, "flok host:", res.Warning)
	}
	switch _, f := snapshot.Load(c.dir, c.now()); {
	case f != snapshot.Fresh:
		fmt.Fprintf(c.out, "%s connects at the next flok up\n", h.Name)
	case res.PathChanged:
		fmt.Fprintf(c.out, "the sidebar reconnects %s now\n", h.Name)
	default:
		if err := state.New(c.dir).WriteRequest(state.Request{Cmd: "reconnect", Host: h.Name}); err == nil {
			fmt.Fprintf(c.out, "%s reconnects now\n", h.Name)
		}
	}
	switch {
	case hooks && res.HooksMissing:
		fmt.Fprintln(c.out, "no ~/.claude or ~/.copilot on the host: no hooks to wire")
	case hooks:
		fmt.Fprintf(c.out, "hooks wired there for %s\n", strings.Join(res.HooksDone, " and "))
	case len(res.Agents) > 0 && res.HooksBefore == 0:
		fmt.Fprintf(c.out, "~/.%s there without flok's hooks: flok host install %s --hooks wires them\n", strings.Join(res.Agents, " and ~/."), h.Name)
	}
	if h.Mode == hosts.ModePlain {
		fmt.Fprintf(c.out, "%s is in plain mode: flok host set %s --mode full uses the flok there\n", h.Name, h.Name)
	}
	return 0
}

// openInstall runs the install where the user can watch it: a tmux popup over the outer (a
// window on a tmux without popups), which is what I in the servers panel starts.
func (c hostCmd) openInstall(name string) int {
	rt, err := launcher.ReadRuntime()
	if err != nil || rt.OuterSocket == "" {
		return c.fail(errors.New("the sidebar is not running (flok up)"))
	}
	outer := tmux.NewLocal(rt.OuterSocket).SetVersion(rt.Version())
	clients, err := outer.Run("list-clients", "-F", "#{client_tty}")
	if err != nil || strings.TrimSpace(clients) == "" {
		return c.fail(errors.New("no terminal is attached to flok"))
	}
	client := strings.SplitN(strings.TrimSpace(clients), "\n", 2)[0]
	cmd := shellQuote(binPath()) + " host install " + shellQuote(name) + " --wait"
	if args := keys.PopupArgsTitled(outer.Features(), client, cmd, " flok install "+name+" "); args != nil {
		_, err := outer.Run(args...)
		return report(err)
	}
	_, err = outer.Run("new-window", "-t", rt.OuterSession+":", "-n", "flok-install-"+name, cmd)
	return report(err)
}

// reconnect is `flok host reconnect <name>`: the running sidebar redials the host now (r in
// the servers panel).
func (c hostCmd) reconnect(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(c.errw, "flok host reconnect: need <name>\n\n%s\n", hostUsage)
		return 2
	}
	h, err := c.lookupHost(args[0])
	if err != nil {
		return c.fail(err)
	}
	return c.requestReconnect(h.Name)
}

func (c hostCmd) requestReconnect(host string) int {
	if _, f := snapshot.Load(c.dir, c.now()); f != snapshot.Fresh {
		return c.fail(errors.New("the sidebar is not running (flok up)"))
	}
	if err := state.New(c.dir).WriteRequest(state.Request{Cmd: "reconnect", Host: host}); err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(c.out, "%s reconnects now\n", host)
	return 0
}
