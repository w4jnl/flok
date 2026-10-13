// Command phoneinst links two made-up flok instances, home and office, to a relay for the
// README's phone screenshots (assets/demo/phone.sh): agents in every state, a remote host, and
// the screens of assets/demo/screens for whoever looks at a pane, so nothing real is on screen.
// It answers an answer with an ack and runs until interrupted.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/snapshot"
)

type pane struct {
	a      snapshot.Agent
	screen string // file in the screens dir
}

func main() {
	url := flag.String("url", "", "the relay (http://127.0.0.1:8080)")
	token := flag.String("token", "", "an instance token of the relay")
	screens := flag.String("screens", "assets/demo/screens", "the screen fixtures")
	flag.Parse()
	if *url == "" || *token == "" {
		log.Fatal("usage: phoneinst -url <relay> -token <instance token> [-screens dir]")
	}
	now := time.Now()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	ag := func(id, host, sess, name string, st agent.State) snapshot.Agent {
		return snapshot.Agent{PaneID: id, Host: host, SessionID: "$" + id[1:], SessionName: sess, WindowID: "@" + id[1:], Name: name,
			Kind: "claude", State: st, HasHooks: true}
	}
	home := []pane{
		{with(ag("%3", "", "api", "api", agent.Blocked), "permission:Bash", "Bash", "npm run db:reset && npm test", 1, ago(2*time.Minute)), "api-blocked.txt"},
		{with(ag("%5", "", "web", "web", agent.Working), "", "Read", "src/pages/checkout.tsx", 0, ago(42*time.Second)), "web.txt"},
		{with(ag("%7", "", "docs", "docs", agent.Done), "", "", "", 1, ago(6*time.Minute)), "docs.txt"},
		{with(ag("%2", "beta", "build", "build", agent.Working), "", "Bash", "npm run build", 0, ago(3*time.Minute)), "api-working.txt"},
	}
	office := []pane{
		{with(ag("%4", "", "infra", "infra", agent.Working), "", "Bash", "terraform plan", 0, ago(95*time.Second)), "api-working.txt"},
		{with(ag("%1", "", "billing", "billing", agent.Idle), "", "", "", 0, ago(25*time.Minute)), "docs.txt"},
	}
	run(*url, *token, *screens, "home", "mac-studio", home, []snapshot.Host{{Name: "beta", Mode: "full", State: "connected", Agents: 1}})
	run(*url, *token, *screens, "office", "mbp-office", office, nil)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)
	<-sig
}

func with(a snapshot.Agent, reason, tool, detail string, unseen int, since time.Time) snapshot.Agent {
	a.Reason, a.Tool, a.Detail, a.Unseen, a.StateSince = reason, tool, detail, unseen, since
	return a
}

// run links one instance: its snapshot, then a screen for every subscription and an ack for
// every answer.
func run(url, token, dir, name, hostname string, panes []pane, hosts []snapshot.Host) {
	cl := link.New(link.Config{URL: url, Token: token, Name: name, Version: "0.7.0", Hostname: hostname})
	s := snapshot.Snapshot{Hosts: hosts}
	screens := map[string]string{}
	sessions := map[string]bool{}
	for _, p := range panes {
		s.Agents = append(s.Agents, p.a)
		s.Unseen += p.a.Unseen
		ref := p.a.PaneID
		if p.a.Host != "" {
			ref = p.a.Host + ":" + ref
		}
		if b, err := os.ReadFile(filepath.Join(dir, p.screen)); err == nil {
			screens[ref] = string(b)
		}
		if !sessions[p.a.Host+p.a.SessionName] {
			sessions[p.a.Host+p.a.SessionName] = true
			s.Sessions = append(s.Sessions, snapshot.Session{ID: p.a.SessionID, Host: p.a.Host, Name: p.a.SessionName, Rollup: p.a.State, Agents: 1})
		}
	}
	cl.Publish(s)
	cl.Start()
	go func() {
		for m := range cl.Messages() {
			switch {
			case m.Status != nil:
				log.Printf("%s: %s %s", name, m.Status.State, m.Status.Detail)
			case m.Command != nil && m.Command.Kind == "subscribe" && m.Command.On:
				cl.Forget(m.Command.Pane)
				cl.Screen(m.Command.Pane, screens[m.Command.Pane])
			case m.Command != nil && (m.Command.Kind == "answer" || m.Command.Kind == "seen"):
				cl.Ack(m.Command.ID, "")
			}
		}
	}()
}
