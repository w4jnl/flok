package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/qr"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
)

const linkOpsUsage = `usage: flok link join <relay> [--name <name>] [--yes] [--open] [--json]
       flok link approve <code>
       flok link list [--json]
       flok link forget <name|id>
       flok link leave
       flok link admin [--clear]

  join     this flok asks the relay to let it in: it shows a code and its QR, good for five
           minutes. A member of the tenant approves it: the phone app scans the code, or
           flok link approve <code> on a flok that is linked, or flok tenant approve on a
           flok with an admin token, which names the tenant. You see who approved it and for
           which tenant, and Enter accepts: the relay hands this flok a key of its own, kept
           in the state dir (link.json), which stands in for [link] url, token and pin in
           config.toml. --yes accepts without asking; --json prints the code as one line and
           the outcome as another, for a script; --open shows the screen in a tmux popup over
           flok; --name joins under a name other than [link] name or the hostname
  approve  let a flok that shows a code into this flok's tenant
  list     the floks of this flok's tenant
  forget   revoke a flok: its key stops working, its link closes, the phones drop it
  leave    forget this flok on the relay and remove its join record (back to [link] in config.toml)
  admin    keep one of the relay's admin_tokens in the state dir (link-admin), read from the
           terminal without echo or from stdin (op read … | flok link admin), never from the
           command line; it stands in for [link] admin in config.toml, so the config holds no
           secret, and lets this flok manage the relay's tenants (flok tenant) however it
           linked. --clear removes it; flok link leave keeps it

approve, list and forget go through this flok's relay link (the sidebar must be running); join
talks to the relay itself, over HTTPS, since there is no link yet. docs/phone.md has the setup.`

// linkOps is `flok link join | approve | list | forget | leave | admin`.
type linkOps struct {
	relayCmd
	version      string
	joiner       func(origin string) link.Joiner // the relay's API for a join; tests point it at a fake
	in           io.Reader                       // flok link admin reads the token here when stdin is no terminal
	readPassword func() ([]byte, error)          // reads it from the terminal without echo; nil when stdin is no terminal
}

func (c linkOps) run(args []string) int {
	switch args[0] {
	case "join":
		return c.join(args[1:])
	case "approve":
		return c.approve(args[1:])
	case "list":
		return c.list(args[1:])
	case "forget":
		return c.forget(args[1:])
	case "leave":
		return c.leave(args[1:])
	case "admin":
		return c.admin(args[1:])
	}
	fmt.Fprintf(c.errw, "flok link: unknown command %q\n\n%s\n", args[0], linkOpsUsage)
	return 2
}

// isLinkOp says whether a flok link argument is one of the join commands (the rest switch the link).
func isLinkOp(arg string) bool {
	switch arg {
	case "join", "approve", "list", "forget", "leave", "admin":
		return true
	}
	return false
}

// joinURL is what the join QR holds: the app opens flok://link, reads the relay and the code,
// looks the flok up and offers to approve it.
func joinURL(origin, code string) string {
	return "flok://link?" + url.Values{"code": {code}, "relay": {origin}}.Encode()
}

// joinWatch is what the poll goroutine learned, for the screen's watch.
type joinWatch struct {
	mu     sync.Mutex
	step   codeStep
	text   string
	tenant wire.TenantInfo
	by     string
}

func (w *joinWatch) set(step codeStep, text string) {
	w.mu.Lock()
	w.step, w.text = step, text
	w.mu.Unlock()
}

func (w *joinWatch) get() (codeStep, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.step, w.text
}

func (c linkOps) join(args []string) int {
	relay, name := "", ""
	yes, open, asJSON, hold := false, false, false, false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--yes":
			yes = true
		case a == "--open":
			open = true
		case a == "--json":
			asJSON, yes = true, true
		case a == "--wait": // the popup's own: stay on the screen after the outcome until a key
			hold = true
		case a == "--name" && i+1 < len(args):
			name = args[i+1]
			i++
		case strings.HasPrefix(a, "--"):
			fmt.Fprintf(c.errw, "flok link join: unknown argument %q\n\n%s\n", a, linkOpsUsage)
			return 2
		case relay == "":
			relay = a
		default:
			fmt.Fprintf(c.errw, "flok link join: one relay, not %q and %q\n\n%s\n", relay, a, linkOpsUsage)
			return 2
		}
	}
	if relay == "" {
		if s := c.settings(); s.URL != "" { // the relay this flok has already: a join under a key of its own
			relay = s.URL
		} else {
			fmt.Fprintf(c.errw, "flok link join: the relay's address is needed (https://flok.example.net)\n\n%s\n", linkOpsUsage)
			return 2
		}
	}
	origin := link.OriginURL(relay)
	if origin == "" {
		return c.fail(fmt.Errorf("%q is not a relay address flok can use (https://flok.example.net)", relay))
	}
	if open {
		return openCodePopup(c.relayCmd, "link join "+shellQuote(relay)+" --wait", " flok link join ", "flok-link-join")
	}
	if name == "" {
		name = c.cfg.InstanceName()
	}
	hostname, _ := os.Hostname()
	ctx := context.Background()
	j := c.joiner(origin)
	start, err := j.Start(ctx, name, hostname, c.version)
	if err != nil {
		return c.fail(fmt.Errorf("%s: %v", linkHost(origin), err))
	}
	u := joinURL(origin, start.Code)
	w := &joinWatch{}
	go c.pollJoin(ctx, j, start, w)
	accept := func() (string, error) {
		joined, err := j.Accept(ctx, start)
		if err != nil {
			return "", err
		}
		rec := state.LinkFile{Relay: origin, Instance: joined.Instance.ID, Name: joined.Instance.Name, Tenant: joined.Tenant.Name, Token: joined.Token, Pin: joined.Pin,
			ApprovedBy: joined.ApprovedBy, Joined: c.now()}
		if err := state.New(c.dir).SaveLink(rec); err != nil {
			return "", fmt.Errorf("joined, but the key could not be kept: %v", err)
		}
		return fmt.Sprintf("joined %s as %s%s", rec.Tenant, rec.Name, c.relinkNote()), nil
	}
	decline := func() { _ = j.Decline(ctx, start) }
	if asJSON {
		fmt.Fprint(c.out, jsonLine(map[string]any{"id": start.ID, "code": start.Code, "url": u, "expires_at": start.ExpiresAt}))
		step, text := c.waitJoin(w, start.ExpiresAt)
		if step != stepApproved {
			fmt.Fprint(c.out, jsonLine(map[string]any{"status": "failed", "error": text}))
			return 1
		}
		out, err := accept()
		if err != nil {
			fmt.Fprint(c.out, jsonLine(map[string]any{"status": "failed", "error": err.Error()}))
			return 1
		}
		l, _ := state.New(c.dir).LoadLink()
		fmt.Fprint(c.out, jsonLine(map[string]any{"status": "joined", "tenant": l.Tenant, "name": l.Name, "instance": l.Instance, "approved_by": l.ApprovedBy, "outcome": out}))
		return 0
	}
	if !c.tty {
		fmt.Fprintf(c.out, "code   %s\nrelay  %s\nurl    %s\nscan it with the phone app, or approve it with flok link approve %s on a linked flok; the code lasts until %s\n",
			start.Code, linkHost(origin), u, start.Code, start.ExpiresAt.Local().Format("15:04"))
		step, text := c.waitJoin(w, start.ExpiresAt)
		if step != stepApproved {
			fmt.Fprintln(c.out, text)
			return 1
		}
		fmt.Fprintln(c.out, text)
		if !yes {
			decline()
			fmt.Fprintln(c.out, "not linked: without a terminal to confirm in, run flok link join with --yes to accept an approval")
			return 1
		}
		out, err := accept()
		if err != nil {
			return c.fail(err)
		}
		fmt.Fprintln(c.out, out)
		return 0
	}
	m, err := qr.Encode(u)
	if err != nil {
		return c.fail(err)
	}
	final, err := runCodeScreen(codeScreen{qr: m, code: start.Code, relay: linkHost(origin), expires: start.ExpiresAt, hold: hold, yes: yes,
		watch: w.get, accept: accept, decline: decline,
		hint:    "scan it with the phone app (Settings › Link a computer), or flok link approve " + start.Code + " on a linked flok",
		expired: "the code expired: run flok link join again"})
	if err != nil {
		return c.fail(err)
	}
	if final.step == stepWaiting { // q before anything happened: the relay forgets the join
		decline()
		fmt.Fprintln(c.out, "not joined")
		return 1
	}
	fmt.Fprintln(c.out, final.outcome)
	if final.step == stepDone {
		return 0
	}
	return 1
}

// pollJoin asks the relay where the join stands until it moves: approved (with who and for
// which tenant), or gone. A relay out of reach is tried again until the code expires.
func (c linkOps) pollJoin(ctx context.Context, j link.Joiner, start link.JoinStart, w *joinWatch) {
	for {
		st, err := j.Wait(ctx, start)
		switch {
		case errors.Is(err, link.ErrJoinGone):
			w.set(stepFailed, strings.TrimPrefix(err.Error(), link.ErrJoinGone.Error()+": "))
			return
		case err != nil:
			if !c.now().Before(start.ExpiresAt) {
				w.set(stepFailed, "the code expired: run flok link join again")
				return
			}
			time.Sleep(2 * time.Second)
			continue
		}
		switch st.Status {
		case "approved":
			w.mu.Lock()
			w.step, w.tenant, w.by = stepApproved, st.Tenant, st.ApprovedBy
			w.text = fmt.Sprintf("approved by %s for the tenant %s", st.ApprovedBy, st.Tenant.Name)
			w.mu.Unlock()
			return
		case "pending":
			continue
		default:
			w.set(stepFailed, st.Error)
			return
		}
	}
}

// waitJoin waits without a screen until the join moves or the code expires.
func (c linkOps) waitJoin(w *joinWatch, expires time.Time) (codeStep, string) {
	for {
		if step, text := w.get(); step != stepWaiting {
			return step, text
		}
		if !c.now().Before(expires.Add(2 * time.Second)) {
			return stepFailed, "the code expired: run flok link join again"
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// relinkNote tells the running sidebar to take the new settings, and says what happens next.
func (c linkOps) relinkNote() string {
	if c.relinkSidebar() {
		return "; the sidebar connects with its new key now"
	}
	return "; flok up connects it"
}

// relinkSidebar files a relink request when a sidebar runs; false when none does (or the
// request could not be written), so the settings are read at the next flok up.
func (c linkOps) relinkSidebar() bool {
	s, f := snapshot.Load(c.dir, c.now())
	return f == snapshot.Fresh && s.SidebarAlive() && state.New(c.dir).WriteRequest(state.Request{Cmd: "relink"}) == nil
}

func (c linkOps) approve(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(c.errw, "flok link approve: need <code>\n\n%s\n", linkOpsUsage)
		return 2
	}
	if _, err := c.ready(); err != nil {
		return c.fail(err)
	}
	f, err := c.askOnce(state.Request{Op: "approve", Code: args[0]})
	if err != nil {
		return c.fail(err)
	}
	p := f.Enroll.Pending
	if p == nil {
		return c.fail(errors.New("the relay's answer carried nothing"))
	}
	fmt.Fprintf(c.out, "approved %s (%s, flok %s) into %s: it links once it accepts there\n", p.Name, p.Hostname, p.Version, f.Enroll.Tenant)
	return 0
}

func (c linkOps) list(args []string) int {
	asJSON := false
	for _, a := range args {
		if a != "--json" {
			fmt.Fprintf(c.errw, "flok link list: unknown argument %q\n\n%s\n", a, linkOpsUsage)
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
	floks := f.Enroll.Instances
	if asJSON {
		if floks == nil {
			floks = []wire.InstanceInfo{}
		}
		fmt.Fprint(c.out, jsonLine(map[string]any{"tenant": f.Enroll.Tenant, "instances": floks}))
		return 0
	}
	fmt.Fprintf(c.out, "tenant %s\n", f.Enroll.Tenant)
	printFloks(c.out, floks)
	return 0
}

// printFloks is a tenant's floks as a table.
func printFloks(out interface{ Write([]byte) (int, error) }, floks []wire.InstanceInfo) {
	if len(floks) == 0 {
		fmt.Fprintln(out, "no floks (flok link join on a flok shows the code)")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tHOST\tFLOK\tSTATE\tJOINED\tLAST SEEN\tAPPROVED BY\tID")
	for _, i := range floks {
		st := "offline"
		if i.Online {
			st = "online"
		}
		joined, by := "", i.ApprovedBy
		if !i.Joined.IsZero() {
			joined = i.Joined.Local().Format("2006-01-02 15:04")
		}
		if i.Static {
			by = "a token from the relay's config"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", i.Name, i.Hostname, i.Version, st, joined, seenAt(i.LastSeen), by, i.ID)
	}
	_ = tw.Flush()
}

func (c linkOps) forget(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(c.errw, "flok link forget: need <name|id>\n\n%s\n", linkOpsUsage)
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
	if f.Enroll.Instance != nil {
		name = f.Enroll.Instance.Name
	}
	fmt.Fprintf(c.out, "%s forgotten: its key no longer works, its link is closed, and the phones drop it\n", name)
	if len(f.Enroll.Instances) > 0 {
		names := make([]string, 0, len(f.Enroll.Instances))
		for _, i := range f.Enroll.Instances {
			names = append(names, i.Name)
		}
		fmt.Fprintf(c.out, "still in %s: %s\n", f.Enroll.Tenant, strings.Join(names, ", "))
	}
	return 0
}

// leave forgets this flok on the relay (best effort: the relay may be out of reach) and removes
// the join record, so [link] in config.toml is the link again, or none.
func (c linkOps) leave(args []string) int {
	if len(args) != 0 {
		fmt.Fprintf(c.errw, "flok link leave takes no argument\n\n%s\n", linkOpsUsage)
		return 2
	}
	s := c.settings()
	if s.Joined == nil {
		return c.fail(errors.New("this flok has not joined by code; [link] in config.toml links it"))
	}
	if _, err := c.ready(); err == nil {
		if _, err := c.askOnce(state.Request{Op: "forget", Name: s.Joined.Instance}); err != nil {
			fmt.Fprintf(c.errw, "flok link leave: the relay did not forget this flok (%v): run flok link forget %s from another flok of %s\n", err, s.Joined.Name, s.Joined.Tenant)
		}
	} else {
		fmt.Fprintf(c.errw, "flok link leave: %v: the relay keeps this flok's key until flok link forget %s runs on another flok of %s\n", err, s.Joined.Name, s.Joined.Tenant)
	}
	if err := state.New(c.dir).RemoveLink(); err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(c.out, "left %s: the join record is gone%s\n", s.Joined.Tenant, c.relinkNote())
	return 0
}

// maxAdminToken bounds what flok link admin keeps: flok-relay token makes 43 characters.
const maxAdminToken = 512

// admin keeps an admin token in the state dir, where it stands in for [link] admin, or removes
// it (--clear). The token comes from the terminal without echo, or from stdin, never from the
// arguments, where ps and the shell's history would show it.
func (c linkOps) admin(args []string) int {
	st := state.New(c.dir)
	path := filepath.Join(c.dir, state.LinkAdminFile)
	switch {
	case len(args) == 1 && args[0] == "--clear":
		had := st.LoadLinkAdmin() != ""
		if err := st.RemoveLinkAdmin(); err != nil {
			return c.fail(err)
		}
		if !had {
			fmt.Fprintf(c.out, "no admin token kept in %s\n", path)
			return 0
		}
		back := ""
		if c.cfg.Link.Admin != "" {
			back = "; [link] admin in config.toml counts again"
		}
		fmt.Fprintf(c.out, "the admin token is gone from %s%s%s\n", path, back, c.adminNote("without it"))
		return 0
	case len(args) != 0:
		fmt.Fprintf(c.errw, "flok link admin: the token is read from the terminal or stdin, never from the command line (op read … | flok link admin)\n\n%s\n", linkOpsUsage)
		return 2
	}
	var raw []byte
	var err error
	if c.readPassword != nil {
		fmt.Fprint(c.errw, "admin token (one of the relay's admin_tokens): ")
		raw, err = c.readPassword()
		fmt.Fprintln(c.errw)
	} else {
		raw, err = io.ReadAll(io.LimitReader(c.in, maxAdminToken+2))
	}
	if err != nil {
		return c.fail(fmt.Errorf("reading the token: %v", err))
	}
	token := strings.TrimSpace(string(raw))
	switch {
	case token == "":
		return c.fail(errors.New("no token given: flok link admin --clear removes the one kept"))
	case len(token) > maxAdminToken || strings.ContainsFunc(token, func(r rune) bool { return r <= ' ' || r == 0x7f }):
		return c.fail(errors.New("that is not a token: one line, no spaces (flok-relay token makes one)"))
	}
	if err := st.SaveLinkAdmin(token); err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(c.out, "admin token kept in %s%s\n", path, c.adminNote("with it"))
	return 0
}

// adminNote has the running sidebar link again with the admin token as it now is (how: "with
// it" or "without it"), and says what happens next.
func (c linkOps) adminNote(how string) string {
	switch {
	case c.settings().URL == "":
		return "; this flok has no relay link yet ([link] url, or flok link join <relay>)"
	case c.relinkSidebar():
		return "; the sidebar links again " + how + " now"
	}
	return "; flok up links " + how
}
