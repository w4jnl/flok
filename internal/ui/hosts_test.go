package ui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/remote/proto"
)

// multiHostModel: local with one idle agent, beta (full, connected: one blocked, one working,
// one unseen), gamma (plain, disabled).
func multiHostModel(t *testing.T) Model {
	t.Helper()
	m, _ := newTestModel(t)
	now := time.Now()
	m.local = merge.Snapshot{
		Spaces: []agent.Space{{SessionID: "$1", SessionName: "Alpha", Current: true, AgentCount: 1, Rollup: agent.Idle}},
		Agents: []agent.Agent{{PaneID: "%1", SessionID: "$1", SessionName: "Alpha", Kind: "claude", Name: "proj", State: agent.Idle, StateSince: now, HasHooks: true}},
		Focus:  merge.Focus{ClientTTY: "/dev/ttys1", SessionID: "$1", PaneID: "%1", Found: true},
	}
	set := hosts.Set{Version: 1, Hosts: []hosts.Host{
		{Name: "beta", Target: "beta", Mode: hosts.ModeFull, Enabled: true},
		{Name: "gamma", Target: "gamma", Mode: hosts.ModePlain, Enabled: false},
	}}
	if cmd := m.applyHosts(set); cmd != nil {
		t.Fatal("no outer, no manager: nothing to run")
	}
	m.onRemote(remote.Msg{Host: "beta", State: remote.Connecting})
	m.onRemote(remote.Msg{Host: "beta", State: remote.Connected, Hello: &proto.Hello{Proto: 1, TmuxVersion: "3.4"}})
	m.onRemote(remote.Msg{Host: "beta", State: remote.Connected, Snap: &proto.Snapshot{
		Spaces: []proto.Space{{SessionID: "$1", SessionName: "web", Current: true, AgentCount: 2, Rollup: agent.Blocked}},
		Agents: []agent.Agent{
			{PaneID: "%1", SessionID: "$1", SessionName: "web", Kind: "claude", Name: "api", Title: "fix login", State: agent.Blocked, Reason: "permission:Bash", StateSince: now, Unseen: 1, HasHooks: true},
			{PaneID: "%2", SessionID: "$1", SessionName: "web", Kind: "claude", Name: "docs", State: agent.Working, StateSince: now, HasHooks: true},
		},
		Focus:  proto.Focus{ClientTTY: "/dev/pts/3", SessionID: "$1", PaneID: "%2", Found: true},
		Unseen: 1,
	}})
	return m
}

func TestServersPanelAndFederatedAgents(t *testing.T) {
	m := multiHostModel(t)
	if !m.multiHost() || m.hostRowCount() != 3 {
		t.Fatalf("multi-host with 3 server rows, got %v %d", m.multiHost(), m.hostRowCount())
	}
	lines := render(m, 28, 40)
	want := []string{"[flok]", "servers", " ○ local", " ● beta", " ○ gamma", "", "sessions · local", " ○ Alpha"}
	for i, w := range want {
		if !strings.HasPrefix(strings.TrimRight(lines[i], " "), w) {
			t.Fatalf("line %d: %q, want prefix %q\n%s", i, lines[i], w, strings.Join(lines[:12], "\n"))
		}
	}
	if !strings.HasSuffix(strings.TrimRight(lines[2], " "), "1") || !strings.HasSuffix(strings.TrimRight(lines[3], " "), "2 · 1") || !strings.HasSuffix(strings.TrimRight(lines[4], " "), "off") {
		t.Fatalf("detail columns: %q %q %q", lines[2], lines[3], lines[4])
	}
	// agents: beta's blocked one first, then its working one, then the local idle; remote rows name the host
	var names []string
	for _, a := range m.snap.Agents {
		names = append(names, agent.PaneRef{Host: a.Host, ID: a.PaneID}.String())
	}
	if !reflect.DeepEqual(names, []string{"beta:%1", "beta:%2", "%1"}) {
		t.Fatalf("agent order %v", names)
	}
	if m.snap.Unseen != 1 || len(m.snap.Spaces) != 1 || m.snap.Spaces[0].SessionName != "Alpha" || len(m.fed.Spaces) != 2 {
		t.Fatalf("front=local: unseen=%d spaces=%d fed spaces=%d", m.snap.Unseen, len(m.snap.Spaces), len(m.fed.Spaces))
	}
	agentsHdr := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "agents") {
			agentsHdr = i
		}
	}
	if !strings.HasPrefix(lines[agentsHdr+1], " ● api") || !strings.Contains(lines[agentsHdr+1], "perm:Bash") || strings.TrimSpace(lines[agentsHdr+2]) != "beta · claude · fix login" {
		t.Fatalf("remote agent rows: %q %q", lines[agentsHdr+1], lines[agentsHdr+2])
	}
	if !strings.HasPrefix(lines[agentsHdr+5], " ○ proj") || strings.TrimSpace(lines[agentsHdr+6]) != "claude" {
		t.Fatalf("local agent rows unchanged: %q %q", lines[agentsHdr+5], lines[agentsHdr+6])
	}
	// one line per agent: the host prefixes the label
	m.d.Cfg.Sidebar.AgentRows = 1
	one := render(m, 28, 40)
	if !strings.HasPrefix(one[agentsHdr+1], " ● beta/api") || !strings.HasPrefix(one[agentsHdr+3], " ○ proj") {
		t.Fatalf("agent_rows = 1: %q %q", one[agentsHdr+1], one[agentsHdr+3])
	}
	// beta in front: its sessions, its focus, its current space
	m.front = "beta"
	m.refederate()
	lines = render(m, 28, 40)
	if !strings.HasPrefix(lines[6], "sessions · beta") || !strings.HasPrefix(lines[7], " ● web") || m.snap.Focus.Host != "beta" || m.snap.Focus.PaneID != "%2" {
		t.Fatalf("front=beta: %q %q focus=%+v", lines[6], lines[7], m.snap.Focus)
	}
	// a lost host takes its agents along and shows why
	m.onRemote(remote.Msg{Host: "beta", State: remote.Unreachable, Detail: "Connection refused", RetryAt: time.Now().Add(8 * time.Second)})
	lines = render(m, 28, 40)
	if len(m.snap.Agents) != 1 || !strings.HasPrefix(lines[3], " ✗ beta") || !strings.Contains(lines[3], "retry in") || !m.retryCountdown() {
		t.Fatalf("unreachable: agents=%d row=%q", len(m.snap.Agents), lines[3])
	}
	m.onRemote(remote.Msg{Host: "beta", State: remote.Auth, Detail: "Permission denied"})
	if lines = render(m, 28, 40); !strings.HasSuffix(strings.TrimRight(lines[3], " "), "needs auth") {
		t.Fatalf("auth: %q", lines[3])
	}
}

func TestRailHostRowsAndRowAt(t *testing.T) {
	m := multiHostModel(t)
	m.panel, m.cursor[panelHosts] = panelHosts, 1
	m.focused = true
	lines := render(m, 6, 30) // mark, its separator, the host rows, a separator, sessions
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	if len(lines) != 30 || !strings.Contains(lines[0], "⌈⩓⌉") || lines[2] != " lo ○" || lines[3] != "›be ●" || lines[4] != " ga ○" || lines[5] != strings.Repeat("─", 6) || !strings.Contains(lines[6], "①") {
		t.Fatalf("rail: %q", lines[:7])
	}
	m.width, m.height = 6, 30
	if p, i, ok := m.rowAt(3); !ok || p != panelHosts || i != 1 {
		t.Fatalf("rail row 3 is beta: %d %d %v", p, i, ok)
	}
	if p, i, ok := m.rowAt(6); !ok || p != panelSpaces || i != 0 {
		t.Fatalf("rail row 6 is the first session: %d %d %v", p, i, ok)
	}
	m.width, m.height = 28, 40
	if p, i, ok := m.rowAt(3); !ok || p != panelHosts || i != 1 {
		t.Fatalf("wide row 3 is beta: %d %d %v", p, i, ok)
	}
	if p, i, ok := m.rowAt(7); !ok || p != panelSpaces || i != 0 {
		t.Fatalf("wide row 7 is the first session: %d %d %v", p, i, ok)
	}
	if _, _, ok := m.rowAt(5); ok {
		t.Fatal("the blank line under the servers is not a row")
	}
}

func TestAbbrev(t *testing.T) {
	got := abbrev([]string{"local", "beta", "gpu-1", "gpu-2", "x"})
	if want := []string{"lo", "be", "g1", "g2", "x "}; !reflect.DeepEqual(got, want) {
		t.Fatalf("abbrev %v, want %v", got, want)
	}
	if got := abbrev([]string{"aa", "aa", "aa"}); got[0] == got[1] || got[1] == got[2] {
		t.Fatalf("identical names still get distinct labels: %v", got)
	}
}

func TestPanelCycleAndKeys(t *testing.T) {
	m, _ := newTestModel(t)
	if m.multiHost() || len(m.panels()) != 2 {
		t.Fatal("single host: two panels")
	}
	m.cyclePanel(1)
	if m.panel != panelAgents {
		t.Fatal("single host: tab toggles")
	}
	m = multiHostModel(t)
	m.focused = true
	var order []int
	for i := 0; i < 4; i++ {
		order = append(order, m.panel)
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = next.(Model)
	}
	if !reflect.DeepEqual(order, []int{panelSpaces, panelAgents, panelHosts, panelSpaces}) {
		t.Fatalf("tab order %v", order)
	}
	for i := 0; i < 2; i++ { // agents -> sessions -> servers
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		m = next.(Model)
	}
	if m.panel != panelHosts {
		t.Fatalf("shift+tab twice from agents lands on servers, got %d", m.panel)
	}
	m.cursor[panelHosts] = 2 // gamma, disabled: c enables it in the registry
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("c on a host row toggles it")
	}
	if _, err := hosts.Update(m.d.Store.Dir, func(s *hosts.Set) error {
		return s.Add(hosts.Host{Name: "gamma", Target: "gamma", Mode: hosts.ModePlain})
	}); err != nil {
		t.Fatal(err)
	}
	if msg, ok := cmd().(hostToggleMsg); !ok || msg.err != nil {
		t.Fatalf("toggle: %+v", msg)
	}
	set, _ := hosts.Load(m.d.Store.Dir)
	if h, _ := set.Get("gamma"); !h.Enabled {
		t.Fatal("gamma must be enabled now")
	}
	// enter on the front host's row only hands focus to the work pane (a no-op without an
	// outer); another host's row needs the outer to swap; a remote agent row needs the manager
	if cmd := m.activate(panelHosts, 0, false); cmd == nil {
		t.Fatal("the front row produces a focus command")
	} else if msg, ok := cmd().(switchedMsg); !ok || msg.err != nil {
		t.Fatalf("front row: %+v", msg)
	}
	if m.activate(panelHosts, 1, false) != nil {
		t.Fatal("no outer: no swap")
	}
	if m.activate(panelAgents, 0, false) != nil {
		t.Fatal("beta's agent while local is in front: the swap needs the outer")
	}
	m.front = "beta"
	m.refederate()
	if cmd := m.activate(panelAgents, 0, false); cmd == nil {
		t.Fatal("the front host's agent row produces a command")
	} else if msg, ok := cmd().(switchedMsg); !ok || msg.err == nil {
		t.Fatalf("a remote goto without a manager reports an error, got %+v", msg)
	}
}

func TestPublishedSnapshotCarriesHosts(t *testing.T) {
	m, _ := newTestModel(t)
	m.local = merge.Snapshot{Agents: []agent.Agent{{PaneID: "%1", State: agent.Idle}}}
	m.refederate()
	data, _ := json.Marshal(m.publishedSnapshot())
	if strings.Contains(string(data), `"host`) {
		t.Fatalf("single host: no host fields on the wire\n%s", data)
	}
	m = multiHostModel(t)
	s := m.publishedSnapshot()
	if len(s.Hosts) != 2 || s.Hosts[0].State != "connected" || s.Hosts[0].Agents != 2 || s.Hosts[0].Pending != 1 || s.Hosts[1].State != "disabled" || s.FrontHost != "" {
		t.Fatalf("hosts block %+v", s.Hosts)
	}
	if s.Agents[0].Host != "beta" || s.Agents[2].Host != "" || s.Sessions[1].Host != "beta" {
		t.Fatalf("tags: %+v", s.Agents)
	}
}
