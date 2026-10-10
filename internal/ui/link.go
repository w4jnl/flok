package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/answer"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/poller"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/tmux"
)

// The phone link. link.Client keeps the WebSocket to the relay; the sidebar feeds it what it
// publishes anyway (publish, notePush) and acts on what comes back: an answer typed into an
// agent pane (checked against the federated view, local panes through the inner server, a
// host's through its connection), a seen mark, a subscription to a pane's screen (captured once
// a second while it lasts: local and plain-mode panes from here, full-mode ones by the host's
// serve). Every command is logged to events.log.

type (
	linkMsg           struct{ link.Msg }
	linkScreenTickMsg struct{}
	linkScreensMsg    struct{ screens map[string]string } // pane ref → text
	linkAnswerMsg     struct {
		id, ref string
		ans     answer.Answer
		err     error
	}
)

// linkWarnAfter is how long the relay may be out of reach before the footer mentions it.
const linkWarnAfter = 30 * time.Second

// startLink runs the link once the program is up.
func (m Model) startLink() tea.Cmd {
	if m.linker == nil {
		return nil
	}
	m.linker.Start()
	return m.waitLink()
}

// waitLink delivers the link's next message.
func (m Model) waitLink() tea.Cmd {
	if m.linker == nil {
		return nil
	}
	ch := m.linker.Messages()
	return func() tea.Msg { return linkMsg{<-ch} }
}

// onLink applies one link message.
func (m *Model) onLink(msg link.Msg) tea.Cmd {
	if msg.Status != nil {
		m.linkStatus = *msg.Status
		if msg.Status.State == link.Connected {
			m.linkWarned = false
		}
		m.refederate()
		m.publish()
		return nil
	}
	if msg.Command != nil {
		return m.linkCommand(*msg.Command)
	}
	return nil
}

// linkWarning is the footer line while the relay is refused or has been out of reach for a
// while; "" otherwise.
func (m Model) linkWarning() string {
	if m.linker == nil {
		return ""
	}
	st := m.linkStatus
	switch st.State {
	case link.Refused:
		return "phone link: " + st.Detail
	case link.Unreachable:
		if time.Since(st.Since) >= linkWarnAfter {
			d := st.Detail
			if d == "" {
				d = "relay out of reach"
			}
			return "phone link: " + d
		}
	}
	return ""
}

// linkWarnDue reports, once per outage, that the link has been down long enough for the footer.
func (m *Model) linkWarnDue() bool {
	if m.linker == nil || m.linkWarned || m.linkWarning() == "" {
		return false
	}
	m.linkWarned = true
	return true
}

// agentByRef finds an agent of the federated view by pane ref ("%12", "beta:%12"); the host is
// resolved to its registered spelling.
func (m Model) agentByRef(ref string) (agent.Agent, bool) {
	pr, err := agent.ParsePaneRef(ref)
	if err != nil {
		return agent.Agent{}, false
	}
	if h, ok := m.hostSet.Get(pr.Host); ok && pr.Host != "" {
		pr.Host = h.Name
	}
	for _, a := range m.fed.Agents {
		if a.Host == pr.Host && a.PaneID == pr.ID {
			return a, true
		}
	}
	return agent.Agent{}, false
}

// linkCommand performs what the phone asked.
func (m *Model) linkCommand(c link.Command) tea.Cmd {
	switch c.Kind {
	case "reset": // the connection ended: the relay subscribes again when it is back
		for ref := range m.linkSubs {
			m.setCapture(ref, false)
		}
		m.linkSubs = map[string]bool{}
		return nil
	case "subscribe":
		a, ok := m.agentByRef(c.Pane)
		ref := c.Pane
		if ok {
			ref = agent.PaneRef{Host: a.Host, ID: a.PaneID}.String()
		}
		if !c.On {
			delete(m.linkSubs, ref)
			m.setCapture(ref, false)
			return nil
		}
		if !ok {
			m.debugf("link: subscribe %s: no such agent", c.Pane)
			return nil
		}
		m.linkSubs[ref] = true
		m.linker.Forget(ref)
		m.setCapture(ref, true)
		if m.linkTicking {
			return nil
		}
		m.linkTicking = true
		return batch(m.captureSubs(), m.linkTick())
	case "seen":
		a, ok := m.agentByRef(c.Pane)
		if !ok {
			m.linker.Ack(c.ID, "no agent in pane "+c.Pane)
			return nil
		}
		if a.Host == "" {
			m.p.MarkSeen(a.PaneID)
		} else if m.remote != nil {
			m.remote.MarkSeen(a.Host, a.PaneID)
		}
		m.logLink("seen", agent.PaneRef{Host: a.Host, ID: a.PaneID}.String(), "", nil)
		m.linker.Ack(c.ID, "")
		return nil
	case "answer":
		a, ok := m.agentByRef(c.Pane)
		if !ok {
			m.linker.Ack(c.ID, "no agent in pane "+c.Pane)
			m.logLink("answer", c.Pane, answer.Describe(c.Answer), errNoAgent)
			return nil
		}
		ref := agent.PaneRef{Host: a.Host, ID: a.PaneID}.String()
		ans := c.Answer
		if n, err := answer.Normalize(ans); err == nil { // the log and the host see tmux's spelling
			ans = n
		}
		ans.Pane = a.PaneID // the host's own pane id
		id, inner, local, rem, host := c.ID, m.d.Inner, m.local, m.remote, a.Host
		return func() tea.Msg {
			var err error
			switch {
			case host == "":
				err = remote.TypeAnswer(inner, local, ans)
			case rem == nil:
				err = errNoRemote
			default:
				err = rem.Answer(host, ans)
			}
			return linkAnswerMsg{id: id, ref: ref, ans: ans, err: err}
		}
	}
	m.debugf("link: command %q ignored", c.Kind)
	return nil
}

// answerDone reports an answer's outcome to the relay and the log; a typed answer counts as
// having looked at the pane.
func (m *Model) answerDone(msg linkAnswerMsg) {
	errText := ""
	if msg.err != nil {
		errText = msg.err.Error()
	} else if pr, err := agent.ParsePaneRef(msg.ref); err == nil && pr.Host == "" {
		m.p.MarkSeen(pr.ID)
	}
	m.linker.Ack(msg.id, errText)
	m.logLink("answer", msg.ref, answer.Describe(msg.ans), msg.err)
}

// logLink appends a command from the phone to events.log (what, which pane, never the text).
func (m Model) logLink(cmd, ref, what string, err error) {
	if m.d.Store == nil {
		return
	}
	rec := map[string]any{"at": time.Now(), "source": "phone", "cmd": cmd, "pane": ref}
	if what != "" {
		rec["what"] = what
	}
	if err != nil {
		rec["error"] = err.Error()
	}
	m.d.Store.AppendEvent(rec)
	m.debugf("link: %s %s %s err=%v", cmd, ref, what, err)
}

// setCapture tells a full-mode host to start or stop streaming a pane's screen; local and
// plain-mode panes are captured from here (captureSubs).
func (m Model) setCapture(ref string, on bool) {
	pr, err := agent.ParsePaneRef(ref)
	if err != nil || pr.Host == "" || m.remote == nil {
		return
	}
	if h, ok := m.hostSet.Get(pr.Host); ok && h.Mode != hosts.ModePlain {
		m.remote.Capture(h.Name, pr.ID, on)
	}
}

// resubscribeHost asks a host that just (re)connected for the screens the phone is looking at
// there: its serve starts fresh.
func (m Model) resubscribeHost(host string) tea.Cmd {
	for ref := range m.linkSubs {
		if pr, err := agent.ParsePaneRef(ref); err == nil && pr.Host == host {
			m.linker.Forget(ref)
			m.setCapture(ref, true)
		}
	}
	return nil
}

func (m Model) linkTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return linkScreenTickMsg{} })
}

// captureSubs captures the subscribed panes of this machine and of plain-mode hosts (one tmux
// call per server) off the loop; full-mode hosts stream theirs.
func (m Model) captureSubs() tea.Cmd {
	type job struct {
		host  string
		c     tmux.Client
		panes []string
	}
	byHost := map[string]*job{}
	for ref := range m.linkSubs {
		pr, err := agent.ParsePaneRef(ref)
		if err != nil {
			continue
		}
		j := byHost[pr.Host]
		if j == nil {
			var c tmux.Client
			switch {
			case pr.Host == "":
				c = m.d.Inner
			case m.remote != nil:
				if h, ok := m.hostSet.Get(pr.Host); ok && h.Mode == hosts.ModePlain {
					c = m.remote.Client(h.Name)
				}
			}
			if c == nil {
				continue
			}
			j = &job{host: pr.Host, c: c}
			byHost[pr.Host] = j
		}
		j.panes = append(j.panes, pr.ID)
	}
	if len(byHost) == 0 {
		return nil
	}
	jobs := make([]*job, 0, len(byHost))
	for _, j := range byHost {
		jobs = append(jobs, j)
	}
	return func() tea.Msg {
		out := map[string]string{}
		for _, j := range jobs {
			for pane, text := range poller.CaptureAll(j.c, j.panes, 0) {
				out[agent.PaneRef{Host: j.host, ID: pane}.String()] = text
			}
		}
		return linkScreensMsg{out}
	}
}

var (
	errNoAgent  = errorString("no agent in that pane")
	errNoRemote = errorString("no remote hosts here")
)

type errorString string

func (e errorString) Error() string { return string(e) }
