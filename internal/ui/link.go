package ui

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/answer"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/poller"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/remote/proto"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

// The relay link. link.Client keeps the WebSocket to the relay; the sidebar feeds it what it
// publishes anyway (publish, notePush) and acts on what comes back: an answer typed into an
// agent pane (checked against the federated view, local panes through the inner server, a
// host's through its connection), a seen mark, a subscription to a pane's screen (captured once
// a second while it lasts: local and plain-mode panes from here, full-mode ones by the host's
// serve). Every command is logged to events.log.

type (
	linkMsg struct {
		link.Msg
		from *link.Client // which link said it: one replaced by relink is ignored
	}
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

// newLinker builds the link from the settings: a join record in the state dir (flok link join)
// wins over [link] url, token and pin in config.toml, a kept admin token (flok link admin) over
// [link] admin. Nil without a relay.
func (m Model) newLinker() *link.Client {
	l := m.d.Cfg.Link
	settings := state.LinkSettings{URL: l.URL, Token: l.Token, Pin: l.Pin, Admin: l.Admin}
	off := false
	if m.d.Store != nil {
		settings = m.d.Store.LinkSettings(l.URL, l.Token, l.Pin, l.Admin)
		off = m.d.Store.LinkOff()
	}
	if settings.URL == "" {
		return nil
	}
	hostname, _ := os.Hostname()
	return link.New(link.Config{URL: settings.URL, Token: settings.Token, Admin: settings.Admin, Pin: settings.Pin, Name: m.d.Cfg.InstanceName(),
		Off: off, Version: m.d.Version, Hostname: hostname, Logf: m.debugf})
}

// relink replaces the link after the settings changed (flok link join wrote its record, flok
// link leave removed it): the old link closes, the new one starts from scratch.
func (m *Model) relink() tea.Cmd {
	if m.linker != nil {
		old := m.linker
		go old.Close()
	}
	for ref := range m.linkSubs {
		m.setCapture(ref, false)
	}
	m.linkSubs = map[string]bool{}
	m.linker = m.newLinker()
	m.linkStatus, m.linkWarned = link.Status{}, false
	if m.linker != nil {
		m.linkStatus = m.linker.Status()
	}
	m.refederate()
	m.publish()
	return m.startLink()
}

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
	c := m.linker
	ch := c.Messages()
	return func() tea.Msg { return linkMsg{<-ch, c} }
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
		return m.animCmd() // connecting blinks the mark
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
		return "relay link: " + st.Detail
	case link.Unreachable:
		if time.Since(st.Since) >= linkWarnAfter {
			d := st.Detail
			if d == "" {
				d = "relay out of reach"
			}
			return "relay link: " + d
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
	case "phone": // the relay's answer to flok phone, or a phone that paired
		return m.linkPhone(c)
	case "enroll", "tenant": // the relay's answer to flok link approve | list | forget, flok tenant
		m.writeReply(c.ID, wire.Frame{Type: c.Kind, ID: c.ID, Enroll: c.Enroll, Tenant: c.Tenant, Error: c.Error})
		return nil
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
		if notice := m.phoneUpgradeNotice(a.Host); notice != "" { // the host's serve would ignore the frame
			m.linker.Ack(c.ID, notice)
			m.logLink("answer", ref, answer.Describe(c.Answer), errorString(notice))
			return nil
		}
		if notice := m.hostFeatureNotice(a.Host, c.Answer); notice != "" { // the host's serve would refuse it
			m.linker.Ack(c.ID, notice)
			m.logLink("answer", ref, answer.Describe(c.Answer), errorString(notice))
			return nil
		}
		ans := c.Answer
		if n, err := answer.Normalize(ans); err == nil { // the log and the host see tmux's spelling
			ans = n
		}
		ans.Pane = a.PaneID // the host's own pane id
		var save answer.Saver
		if m.d.Store != nil {
			save = answer.DirSaver(remote.AttachDir(m.d.Store.Dir))
		}
		id, inner, local, rem, host := c.ID, m.d.Inner, m.local, m.remote, a.Host
		return func() tea.Msg {
			var err error
			switch {
			case host == "":
				err = remote.TypeAnswer(inner, local, ans, save)
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
// plain-mode panes are captured from here (captureSubs). A host whose flok predates the phone
// would never answer: the phone gets a line saying so instead of waiting.
func (m Model) setCapture(ref string, on bool) {
	pr, err := agent.ParsePaneRef(ref)
	if err != nil || pr.Host == "" || m.remote == nil {
		return
	}
	if h, ok := m.hostSet.Get(pr.Host); ok && h.Mode != hosts.ModePlain {
		if notice := m.phoneUpgradeNotice(h.Name); notice != "" {
			if on && m.linker != nil {
				m.linker.Screen(ref, notice+"\n")
			}
			return
		}
		m.remote.Capture(h.Name, pr.ID, on)
	}
}

// phoneUpgradeNotice names the upgrade a full-mode host needs before the phone can look at or
// answer its agents (its serve advertises no answer feature); "" when it can, or for local and
// plain-mode panes.
func (m Model) phoneUpgradeNotice(host string) string {
	if host == "" {
		return ""
	}
	h, ok := m.hostSet.Get(host)
	if !ok || h.Mode == hosts.ModePlain {
		return ""
	}
	v, ok := m.remotes[host]
	if !ok || v.hello == nil || v.hello.Has(proto.FeatureAnswer) {
		return ""
	}
	return "flok" + versionWord(v.hello.Version) + " on " + host + " predates the phone: screens and answers from it need an upgrade (flok host install " + host + ")"
}

// hostFeatureNotice names the upgrade a full-mode host needs for what an answer carries, a long
// text (pasted) or a file; "" when its serve takes it, or for local and plain-mode panes.
func (m Model) hostFeatureNotice(host string, a answer.Answer) string {
	if host == "" {
		return ""
	}
	h, ok := m.hostSet.Get(host)
	if !ok || h.Mode == hosts.ModePlain {
		return ""
	}
	v, ok := m.remotes[host]
	if !ok || v.hello == nil {
		return ""
	}
	who := "flok" + versionWord(v.hello.Version) + " on " + host
	switch {
	case len(a.Files) > 0 && !v.hello.Has(proto.FeatureFiles):
		return who + " cannot take files: it needs an upgrade (flok host install " + host + ")"
	case a.Form != nil && !v.hello.Has(proto.FeatureForm):
		return who + " cannot fill a question form: it needs an upgrade (flok host install " + host + ")"
	case answer.Long(a.Text) && !v.hello.Has(proto.FeatureLongText):
		return fmt.Sprintf("%s takes at most %d characters on one line: longer text needs an upgrade (flok host install %s)", who, answer.MaxText, host)
	}
	return ""
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
			for pane, text := range poller.CaptureShown(j.c, j.panes) {
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
