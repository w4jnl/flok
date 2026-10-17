package cli

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/state"
)

const tenantUsage = `usage: flok tenant <command>

  list [--json]                    the tenants on the relay, with their counts
  show <name> [--json]             a tenant's floks and phones
  approve <code> --tenant <name>   let a flok that shows a code (flok link join) into a tenant;
                                   a name the relay does not know makes a new tenant
  remove <name>                    remove a tenant with all it holds: its floks' keys and links,
                                   its phones' keys and pushes

A tenant is the set of floks and phones that see each other on the relay. Managing them takes
an admin token: one of the relay's admin_tokens as this flok's [link] token. The relay's own
tenant, default, is where the tokens of its config link and sign in; it stays.`

// tenantCmd is `flok tenant`.
type tenantCmd struct{ relayCmd }

func runTenant(cfg config.Config, args []string) int {
	return tenantCmd{relayCmd{name: "tenant", dir: config.StateDir(), cfg: cfg, now: time.Now, wait: 5 * time.Second,
		tty: term.IsTerminal(int(os.Stdout.Fd())), out: os.Stdout, errw: os.Stderr}}.run(args)
}

func (c tenantCmd) run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.errw, tenantUsage)
		return 2
	}
	switch args[0] {
	case "list":
		return c.list(args[1:])
	case "show":
		return c.show(args[1:])
	case "approve":
		return c.approve(args[1:])
	case "remove":
		return c.remove(args[1:])
	case "help", "-h", "--help":
		fmt.Fprintln(c.out, tenantUsage)
		return 0
	}
	fmt.Fprintf(c.errw, "flok tenant: unknown command %q\n\n%s\n", args[0], tenantUsage)
	return 2
}

// jsonFlag takes --json out of the arguments.
func jsonFlag(args []string) (bool, []string) {
	rest := args[:0:0]
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		} else {
			rest = append(rest, a)
		}
	}
	return asJSON, rest
}

func (c tenantCmd) list(args []string) int {
	asJSON, rest := jsonFlag(args)
	if len(rest) != 0 {
		fmt.Fprintf(c.errw, "flok tenant list: unknown argument %q\n\n%s\n", rest[0], tenantUsage)
		return 2
	}
	if _, err := c.ready(); err != nil {
		return c.fail(err)
	}
	f, err := c.askOnce(state.Request{Op: "list"})
	if err != nil {
		return c.fail(err)
	}
	tenants := f.Tenant.Tenants
	if asJSON {
		if tenants == nil {
			tenants = []wire.TenantInfo{}
		}
		fmt.Fprint(c.out, jsonLine(tenants))
		return 0
	}
	tw := tabwriter.NewWriter(c.out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tFLOKS\tONLINE\tPHONES\tCREATED\tID")
	for _, t := range tenants {
		created := "the relay's own"
		if !t.Created.IsZero() {
			created = t.Created.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\t%s\n", t.Name, t.Instances, t.Online, t.Phones, created, t.ID)
	}
	_ = tw.Flush()
	return 0
}

func (c tenantCmd) show(args []string) int {
	asJSON, rest := jsonFlag(args)
	if len(rest) != 1 {
		fmt.Fprintf(c.errw, "flok tenant show: need <name>\n\n%s\n", tenantUsage)
		return 2
	}
	if _, err := c.ready(); err != nil {
		return c.fail(err)
	}
	f, err := c.askOnce(state.Request{Op: "show", Name: rest[0]})
	if err != nil {
		return c.fail(err)
	}
	t := f.Tenant.Tenant
	if t == nil {
		return c.fail(errors.New("the relay's answer carried nothing"))
	}
	if asJSON {
		fmt.Fprint(c.out, jsonLine(t))
		return 0
	}
	fmt.Fprintf(c.out, "tenant %s (%s)\n", t.Name, t.ID)
	printFloks(c.out, t.Floks)
	if len(t.PhoneList) == 0 {
		fmt.Fprintln(c.out, "no phones paired")
		return 0
	}
	tw := tabwriter.NewWriter(c.out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "PHONE\tPAIRED\tLAST SEEN\tVIA\tID")
	for _, p := range t.PhoneList {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, p.Paired.Local().Format("2006-01-02 15:04"), seenAt(p.LastSeen), p.Instance, p.ID)
	}
	_ = tw.Flush()
	return 0
}

func (c tenantCmd) approve(args []string) int {
	code, tenant := "", ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--tenant" && i+1 < len(args):
			tenant = args[i+1]
			i++
		case code == "" && args[i] != "--tenant":
			code = args[i]
		default:
			fmt.Fprintf(c.errw, "flok tenant approve: need <code> --tenant <name>\n\n%s\n", tenantUsage)
			return 2
		}
	}
	if code == "" || tenant == "" {
		fmt.Fprintf(c.errw, "flok tenant approve: need <code> --tenant <name>\n\n%s\n", tenantUsage)
		return 2
	}
	if _, err := c.ready(); err != nil {
		return c.fail(err)
	}
	f, err := c.askOnce(state.Request{Op: "approve", Code: code, Name: tenant})
	if err != nil {
		return c.fail(err)
	}
	p, t := f.Tenant.Pending, f.Tenant.Tenant
	if p == nil || t == nil {
		return c.fail(errors.New("the relay's answer carried nothing"))
	}
	fmt.Fprintf(c.out, "approved %s (%s, flok %s) into %s: it links once it accepts there\n", p.Name, p.Hostname, p.Version, t.Name)
	return 0
}

func (c tenantCmd) remove(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(c.errw, "flok tenant remove: need <name>\n\n%s\n", tenantUsage)
		return 2
	}
	if _, err := c.ready(); err != nil {
		return c.fail(err)
	}
	f, err := c.askOnce(state.Request{Op: "remove", Name: args[0]})
	if err != nil {
		return c.fail(err)
	}
	t := f.Tenant.Tenant
	if t == nil {
		return c.fail(errors.New("the relay's answer carried nothing"))
	}
	fmt.Fprintf(c.out, "%s removed: %d floks and %d phones lost their keys\n", t.Name, t.Instances, t.Phones)
	return 0
}
