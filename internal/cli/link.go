package cli

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
)

const linkUsage = `usage: flok link [on|off|toggle|status] [--notify]
       flok link join <relay> | approve <code> | list | forget <name> | leave   (flok link join --help)

Switches the relay link ([link] url, or the relay this flok joined) off and on again (no
argument toggles). Off, nothing leaves this machine for the relay: the phone shows this flok as
off, gets no notifications from it and cannot answer its agents. It stays off across flok down
and up until flok link on. --notify shows the result on tmux's status line instead of printing
it, for key bindings (prefix U).`

// linkCmd is `flok link` with its environment injected for tests.
type linkCmd struct {
	dir       string
	url       string          // the relay: [link] url, or the join record's; "" = no link to switch
	joined    *state.LinkFile // the join record, when the link comes from one
	wait      time.Duration   // how long to wait for the sidebar to confirm a change
	out, errw io.Writer
}

func runLink(cfg config.Config, args []string) int {
	if len(args) > 0 && isLinkOp(args[0]) {
		if len(args) > 1 && (args[1] == "-h" || args[1] == "--help") {
			fmt.Fprintln(os.Stdout, linkOpsUsage)
			return 0
		}
		return linkOps{relayCmd: relayCmd{name: "link", dir: config.StateDir(), cfg: cfg, now: time.Now, wait: 5 * time.Second,
			tty: term.IsTerminal(int(os.Stdout.Fd())), out: os.Stdout, errw: os.Stderr}, version: Version,
			joiner: func(origin string) link.Joiner { return link.Joiner{Origin: origin} }}.run(args)
	}
	settings := state.New(config.StateDir()).LinkSettings(cfg.Link.URL, cfg.Link.Token, cfg.Link.Pin)
	return withNotify(args, func(args []string, out, errw io.Writer) int {
		return linkCmd{dir: config.StateDir(), url: settings.URL, joined: settings.Joined, wait: 2 * time.Second, out: out, errw: errw}.run(args)
	})
}

// linkState is the switch and what the running sidebar reports of its link. Shared by
// `flok link status` and `flok doctor`.
type linkState struct {
	off     bool           // the switch says off (the link-off marker)
	running bool           // flok is running: its sidebar is alive and published recently
	link    *snapshot.Link // the running sidebar's link; nil without one (no [link] url when it started)
}

func readLink(dir string) linkState {
	s, f := snapshot.Load(dir, time.Now())
	l := linkState{off: state.New(dir).LinkOff(), running: f == snapshot.Fresh && s.SidebarAlive()}
	if l.running {
		l.link = s.Link
	}
	return l
}

func (l linkState) String() string {
	switch {
	case !l.running && l.off:
		return "relay link: off (it stays off when flok starts; flok link on)"
	case !l.running:
		return "relay link: on (flok is not running)"
	case l.link == nil:
		return "relay link: the running sidebar has none (flok reload after setting [link] url)"
	}
	k, relay := l.link, linkHost(l.link.URL)
	switch link.State(k.State) {
	case link.Off:
		return "relay link: off (flok link on)"
	case link.Connected:
		s := "relay link: on, connected to " + relay
		if k.Encrypted {
			s += ", encrypted"
		}
		if k.Compressed {
			s += ", compressed"
		}
		return s
	case link.Connecting:
		return "relay link: on, connecting to " + relay
	case link.Refused:
		return "relay link: on, refused by " + relay + ": " + k.Detail
	}
	if k.Detail != "" {
		return "relay link: on, " + relay + " out of reach: " + k.Detail
	}
	return "relay link: on, " + relay + " out of reach"
}

// linkHost is the relay's host in a link URL, the URL itself when it has none.
func linkHost(s string) string {
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Host
	}
	return s
}

func (c linkCmd) run(args []string) int {
	action := "toggle"
	if len(args) > 0 {
		action = args[0]
	}
	switch {
	case len(args) > 1:
		fmt.Fprintln(c.errw, linkUsage)
		return 2
	case action == "-h" || action == "--help" || action == "help":
		fmt.Fprintln(c.out, linkUsage)
		return 0
	case action != "on" && action != "off" && action != "toggle" && action != "status":
		fmt.Fprintf(c.errw, "flok link: unknown argument %q\n%s\n", action, linkUsage)
		return 2
	case c.url == "" && action == "status":
		fmt.Fprintln(c.out, "relay link: none ([link] url in config.toml, or flok link join <relay>, connects flok to your relay)")
		return 0
	case c.url == "":
		fmt.Fprintln(c.errw, "flok link: no relay link to switch; [link] url in config.toml, or flok link join <relay>, connects flok to your relay")
		return 1
	}
	cur := readLink(c.dir)
	switch action {
	case "status":
		fmt.Fprintln(c.out, cur)
		if j := c.joined; j != nil {
			fmt.Fprintf(c.out, "joined %s as %s on %s, approved by %s (flok link leave undoes it)\n", j.Tenant, j.Name, j.Joined.Local().Format("2006-01-02"), j.ApprovedBy)
		}
		return 0
	case "toggle":
		action = "off"
		if cur.off {
			action = "on"
		}
	}
	off := action == "off"
	if err := state.New(c.dir).SetLinkOff(off); err != nil {
		fmt.Fprintln(c.errw, "flok link:", err)
		return 1
	}
	if !cur.running || cur.link == nil { // the switch is all there is until a sidebar runs a link
		fmt.Fprintln(c.out, readLink(c.dir))
		return 0
	}
	// off is confirmed by the state; on as soon as the link leaves off, but a connected is worth
	// a moment's wait: that is what the user wants to read
	deadline := time.Now().Add(c.wait)
	for {
		now := readLink(c.dir)
		switched := now.running && now.link != nil && (link.State(now.link.State) == link.Off) == off
		late := time.Now().After(deadline)
		if switched && (off || link.State(now.link.State) != link.Connecting || late) {
			fmt.Fprintln(c.out, now)
			return 0
		}
		if late {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Fprintf(c.errw, "flok link: the sidebar did not confirm the link %s (see flok doctor; FLOK_DEBUG=1 logs the reason to sidebar.log)\n", action)
	return 1
}
