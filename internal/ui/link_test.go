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
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/remote/proto"
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
	k := len(ft.Calls)
	text, keys := strings.Join(ft.Calls[k-2], " "), strings.Join(ft.Calls[k-1], " ")
	if text != "send-keys -t %1 -l y" || keys != "send-keys -t %1 Enter" { // two writes: the Enter must not ride in the text's paste
		t.Fatalf("send-keys: %q then %q", text, keys)
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
	if w := m.linkWarning(); !strings.HasPrefix(w, "relay link: the relay refused") {
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
	if len(m.snap.Warnings) == 0 || !strings.Contains(m.snap.Warnings[len(m.snap.Warnings)-1], "relay link: connection refused") {
		t.Fatalf("footer: %v", m.snap.Warnings)
	}
}

// A full-mode host whose flok predates the phone cannot stream screens or take answers: the
// phone is told which upgrade it needs instead of waiting, and an answer is refused at once.
func TestLinkHostPredatingThePhone(t *testing.T) {
	m, _ := newTestModel(t)
	m.linker = link.New(link.Config{URL: "ws://127.0.0.1:1/link"})
	m.hostSet = hosts.Set{Hosts: []hosts.Host{{Name: "beta", Target: "beta", Mode: hosts.ModeFull, Enabled: true}, {Name: "gamma", Target: "gamma", Mode: hosts.ModePlain, Enabled: true}}}
	m.hostList = m.hostSet.Hosts
	old := &proto.Hello{Proto: 1, Version: "0.5.2", Features: []string{proto.FeatureKeys, proto.FeaturePrefix}}
	m.remotes = map[string]hostView{
		"beta":  {state: remote.Connected, hello: old, hasSnap: true, snap: merge.Snapshot{Agents: []agent.Agent{{PaneID: "%1", Name: "docs", State: agent.Blocked}}}},
		"gamma": {state: remote.Connected, hello: &proto.Hello{Version: "plain"}, hasSnap: true, snap: merge.Snapshot{Agents: []agent.Agent{{PaneID: "%2", Name: "web", State: agent.Idle}}}},
	}
	m.refederate()
	if n := m.phoneUpgradeNotice("beta"); !strings.Contains(n, "flok 0.5.2 on beta predates the phone") || !strings.Contains(n, "flok host install beta") {
		t.Fatalf("notice: %q", n)
	}
	if m.phoneUpgradeNotice("gamma") != "" || m.phoneUpgradeNotice("") != "" {
		t.Fatal("plain-mode and local panes need no upgrade")
	}
	if c := m.linkCommand(link.Command{ID: "a1", Kind: "answer", Pane: "beta:%1", Answer: answer.Answer{Keys: []string{"Enter"}}}); c != nil {
		t.Fatal("an answer for an old host is refused without a round trip")
	}
	log, _ := os.ReadFile(filepath.Join(m.d.Store.Dir, "events.log"))
	if !strings.Contains(string(log), "predates the phone") {
		t.Fatalf("refusal not logged: %s", log)
	}
	m.remotes["beta"] = hostView{state: remote.Connected, hello: &proto.Hello{Version: "0.6.0", Features: proto.ServeFeatures}, hasSnap: true, snap: m.remotes["beta"].snap}
	if m.phoneUpgradeNotice("beta") != "" {
		t.Fatal("a current flok needs none")
	}
}
