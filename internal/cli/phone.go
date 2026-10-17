package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/keys"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/qr"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

const phoneUsage = `usage: flok phone <command>

  pair [--open] [--json]   show a code and its QR for the phone app to scan (good for five
                           minutes, for one phone); --open shows it in a tmux popup over flok,
                           --json prints the code and exits
  list [--json]            the phones paired with the relay
  forget <name|id>         revoke a phone: its key stops working, its pushes stop, its app asks
                           to pair again

A phone pairs through this flok's relay link ([link] url in config.toml, or flok link join): the
relay keeps its phones, no device token in its config. docs/phone.md has the setup.`

// phoneCmd is `flok phone`.
type phoneCmd struct{ relayCmd }

func runPhone(cfg config.Config, args []string) int {
	return phoneCmd{relayCmd{name: "phone", dir: config.StateDir(), cfg: cfg, now: time.Now, wait: 5 * time.Second,
		tty: term.IsTerminal(int(os.Stdout.Fd())), out: os.Stdout, errw: os.Stderr}}.run(args)
}

func (c phoneCmd) run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.errw, phoneUsage)
		return 2
	}
	switch args[0] {
	case "pair":
		return c.pair(args[1:])
	case "list":
		return c.list(args[1:])
	case "forget":
		return c.forget(args[1:])
	case "help", "-h", "--help":
		fmt.Fprintln(c.out, phoneUsage)
		return 0
	}
	fmt.Fprintf(c.errw, "flok phone: unknown command %q\n\n%s\n", args[0], phoneUsage)
	return 2
}

// isPaired is the relay's word that a phone took the code (the reply file's second content).
func isPaired(f wire.Frame) bool {
	return f.Phone != nil && f.Phone.Op == "paired" && f.Phone.Phone != nil
}

// pairURL is what the QR holds: the app opens flok://pair, reads the relay and the code, and
// pairs. The relay is the origin the app would type (link.OriginURL), percent-encoded.
func pairURL(origin, code string) string {
	return "flok://pair?" + url.Values{"code": {code}, "relay": {origin}}.Encode()
}

// codePopupWidth and codePopupHeight hold a version-5 QR with its quiet zone and the lines under
// it; tmux clamps a popup to the window, and the screen drops the QR when it does not fit.
const codePopupWidth, codePopupHeight = "76", "33"

func (c phoneCmd) pair(args []string) int {
	open, asJSON, hold := false, false, false
	for _, a := range args {
		switch a {
		case "--open":
			open = true
		case "--json":
			asJSON = true
		case "--wait": // the popup's own: stay on the screen after the outcome until a key
			hold = true
		default:
			fmt.Fprintf(c.errw, "flok phone pair: unknown argument %q\n\n%s\n", a, phoneUsage)
			return 2
		}
	}
	if open {
		return openCodePopup(c.relayCmd, "phone pair --wait", " flok phone pair ", "flok-phone-pair")
	}
	origin, err := c.ready()
	if err != nil {
		return c.fail(err)
	}
	id, f, err := c.ask(state.Request{Op: "pair"})
	path := state.New(c.dir).ReplyPath(id)
	if err != nil {
		_ = os.Remove(path)
		return c.fail(err)
	}
	code, expires := f.Phone.Code, f.Phone.ExpiresAt
	u := pairURL(origin, code)
	if asJSON {
		_ = os.Remove(path)
		fmt.Fprint(c.out, jsonLine(map[string]any{"id": id, "code": code, "url": u, "expires_at": expires}))
		return 0
	}
	defer os.Remove(path)
	if !c.tty {
		fmt.Fprintf(c.out, "code   %s\nrelay  %s\nurl    %s\nscan it with the Camera app, or type the code in flok's Settings; the code lasts until %s\n",
			code, linkHost(origin), u, expires.Local().Format("15:04"))
		paired, ok := pollReply(path, time.Until(expires), isPaired)
		if !ok {
			fmt.Fprintln(c.out, "the code expired: run flok phone pair again")
			return 1
		}
		fmt.Fprintf(c.out, "paired: %s\n", paired.Phone.Phone.Name)
		return 0
	}
	m, err := qr.Encode(u)
	if err != nil {
		return c.fail(err)
	}
	// the screen watches the reply file for the relay's word that a phone took the code
	watch := func() (codeStep, string) {
		if f, ok := readReply(path); ok && isPaired(f) {
			return stepDone, "paired: " + f.Phone.Phone.Name
		}
		return stepWaiting, ""
	}
	final, err := runCodeScreen(codeScreen{qr: m, code: code, relay: linkHost(origin), expires: expires, hold: hold, watch: watch,
		hint: "scan it with the Camera app, or in flok › Settings › Pair this phone", expired: "the code expired: run flok phone pair again"})
	if err != nil {
		return c.fail(err)
	}
	switch {
	case final.step == stepDone:
		fmt.Fprintln(c.out, final.outcome)
		return 0
	case final.outcome != "":
		fmt.Fprintln(c.out, final.outcome)
	default:
		fmt.Fprintln(c.out, "no phone paired")
	}
	return 1
}

// openCodePopup shows a code screen where the user can see it: a tmux popup over the outer (a
// window on a tmux without popups), which is what p in the servers panel starts for a pairing.
func openCodePopup(c relayCmd, cmdArgs, title, window string) int {
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
	cmd := shellQuote(binPath()) + " " + cmdArgs
	if args := keys.PopupArgsSized(outer.Features(), client, cmd, title, codePopupWidth, codePopupHeight); args != nil {
		_, err := outer.Run(args...)
		return report(err)
	}
	_, err = outer.Run("new-window", "-t", rt.OuterSession+":", "-n", window, cmd)
	return report(err)
}

func (c phoneCmd) list(args []string) int {
	asJSON := false
	for _, a := range args {
		if a != "--json" {
			fmt.Fprintf(c.errw, "flok phone list: unknown argument %q\n\n%s\n", a, phoneUsage)
			return 2
		}
		asJSON = true
	}
	if _, err := c.ready(); err != nil {
		return c.fail(err)
	}
	f, err := c.askOnce(state.Request{Op: "list"})
	if err != nil {
		return c.fail(err)
	}
	phones := f.Phone.Phones
	if asJSON {
		if phones == nil {
			phones = []wire.PhoneInfo{}
		}
		fmt.Fprint(c.out, jsonLine(phones))
		return 0
	}
	if len(phones) == 0 {
		fmt.Fprintln(c.out, "no phones paired (flok phone pair shows the code)")
		return 0
	}
	tw := tabwriter.NewWriter(c.out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPAIRED\tLAST SEEN\tVIA\tID")
	for _, p := range phones {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, p.Paired.Local().Format("2006-01-02 15:04"), seenAt(p.LastSeen), p.Instance, p.ID)
	}
	_ = tw.Flush()
	return 0
}

func (c phoneCmd) forget(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(c.errw, "flok phone forget: need <name|id>\n\n%s\n", phoneUsage)
		return 2
	}
	if _, err := c.ready(); err != nil {
		return c.fail(err)
	}
	f, err := c.askOnce(state.Request{Op: "forget", Name: args[0]})
	if err != nil {
		return c.fail(err)
	}
	name := args[0]
	if f.Phone.Phone != nil {
		name = f.Phone.Phone.Name
	}
	fmt.Fprintf(c.out, "%s forgotten: its key no longer works, its pushes stop, and its app asks to pair again\n", name)
	if len(f.Phone.Phones) > 0 {
		names := make([]string, 0, len(f.Phone.Phones))
		for _, p := range f.Phone.Phones {
			names = append(names, p.Name)
		}
		fmt.Fprintf(c.out, "still paired: %s\n", strings.Join(names, ", "))
	}
	return 0
}
