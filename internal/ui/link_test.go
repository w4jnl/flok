package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/answer"
	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/merge"
)

// An answer from the phone lands in the agent's pane as one send-keys invocation, is logged,
// and counts as seen; a pane without an agent is refused; a subscription captures the screen.
func TestLinkCommands(t *testing.T) {
	m, ft := newTestModel(t)
	m.linker = link.New(link.Config{URL: "ws://127.0.0.1:1/link"}) // never started: queues only
	m.local = merge.Snapshot{Agents: []agent.Agent{{PaneID: "%1", Name: "api", State: agent.Blocked, Reason: "permission:Bash", Unseen: 1}}}
	m.refederate()
	ft.Screens["%1"] = "Allow Bash? [y/n]\n"
	run := func(c tea.Cmd) tea.Msg {
		if c == nil {
			return nil
		}
		return c()
	}
	msg := run(m.linkCommand(link.Command{ID: "a1", Kind: "answer", Pane: "%1", Answer: answer.Answer{Text: "y", Keys: []string{"enter"}}}))
	am, ok := msg.(linkAnswerMsg)
	if !ok || am.err != nil || am.id != "a1" || am.ref != "%1" {
		t.Fatalf("answer: %#v", msg)
	}
	m.answerDone(am)
	last := ft.Calls[len(ft.Calls)-1]
	if got := strings.Join(last, " "); got != "send-keys -t %1 -l y ; send-keys -t %1 Enter" {
		t.Fatalf("send-keys: %q", got)
	}
	log, _ := os.ReadFile(filepath.Join(m.d.Store.Dir, "events.log"))
	if !strings.Contains(string(log), `"source":"phone"`) || !strings.Contains(string(log), `"what":"1 chars + Enter"`) || strings.Contains(string(log), `"y"`) {
		t.Fatalf("events.log: %s", log)
	}
	// not an agent pane: refused before tmux hears of it, and logged with the error
	n := len(ft.Calls)
	if c := m.linkCommand(link.Command{ID: "a2", Kind: "answer", Pane: "%9", Answer: answer.Answer{Text: "y"}}); c != nil {
		t.Fatal("a pane without an agent must be refused at once")
	}
	if len(ft.Calls) != n {
		t.Fatalf("tmux was called for a refused answer: %v", ft.Calls[n:])
	}
	log, _ = os.ReadFile(filepath.Join(m.d.Store.Dir, "events.log"))
	if !strings.Contains(string(log), `"error":"no agent in that pane"`) {
		t.Fatalf("refusal not logged: %s", log)
	}
	// an unknown key is refused by the answer check, in the command's result
	msg = run(m.linkCommand(link.Command{ID: "a3", Kind: "answer", Pane: "%1", Answer: answer.Answer{Keys: []string{"F12"}}}))
	if am := msg.(linkAnswerMsg); am.err == nil || !strings.Contains(am.err.Error(), "not allowed") {
		t.Fatalf("bad key: %+v", am)
	}
	// subscribe: the pane is captured and its text goes to the link
	cmd := m.linkCommand(link.Command{Kind: "subscribe", Pane: "%1", On: true})
	if !m.linkSubs["%1"] || !m.linkTicking || cmd == nil {
		t.Fatalf("subscribe: subs=%v ticking=%v cmd=%v", m.linkSubs, m.linkTicking, cmd != nil)
	}
	sm, ok := run(m.captureSubs()).(linkScreensMsg)
	if !ok || sm.screens["%1"] != "Allow Bash? [y/n]\n" {
		t.Fatalf("captured: %#v", sm)
	}
	if m.linkCommand(link.Command{Kind: "subscribe", Pane: "%7", On: true}); m.linkSubs["%7"] {
		t.Fatal("a pane without an agent is not subscribed")
	}
	m.linkCommand(link.Command{Kind: "subscribe", Pane: "%1", On: false})
	if len(m.linkSubs) != 0 || m.captureSubs() != nil {
		t.Fatalf("unsubscribe: %v", m.linkSubs)
	}
	m.linkCommand(link.Command{Kind: "subscribe", Pane: "%1", On: true})
	m.linkCommand(link.Command{Kind: "reset"})
	if len(m.linkSubs) != 0 {
		t.Fatal("reset drops every subscription")
	}
	// the footer: refused at once, unreachable only after a while
	m.linkStatus = link.Status{State: link.Refused, Detail: "the relay refused the token"}
	if w := m.linkWarning(); !strings.HasPrefix(w, "phone link: the relay refused") {
		t.Fatalf("warning: %q", w)
	}
	m.linkStatus = link.Status{State: link.Unreachable, Detail: "connection refused", Since: time.Now()}
	if w := m.linkWarning(); w != "" {
		t.Fatalf("a fresh outage is not a warning yet: %q", w)
	}
	m.linkStatus.Since = time.Now().Add(-linkWarnAfter)
	if !m.linkWarnDue() || m.linkWarnDue() {
		t.Fatal("the warning is due once")
	}
	m.refederate()
	if len(m.snap.Warnings) == 0 || !strings.Contains(m.snap.Warnings[len(m.snap.Warnings)-1], "phone link: connection refused") {
		t.Fatalf("footer: %v", m.snap.Warnings)
	}
}
