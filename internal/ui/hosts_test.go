package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/hosts"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/remote/proto"
	"github.com/w4jnl/flok/internal/state"
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
	m.onRemote(remote.Msg{Host: "beta", State: remote.Connected, Hello: &proto.Hello{Proto: 1, Version: "0.5.0", TmuxVersion: "3.4", Features: proto.ServeFeatures}})
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
	want := []string{"[flok]", "servers", " ○ local", " ● beta full", " ○ gamma plain", "", "sessions · local", " ○ Alpha"}
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
	if !reflect.DeepEqual(m.snap.Warnings, []string{"beta: Connection refused"}) || !strings.HasPrefix(lines[len(lines)-3], "beta: Connection refused") ||
		strings.TrimSpace(lines[len(lines)-2]) != "" || !strings.HasPrefix(lines[len(lines)-1], "click or prefix g to focus") {
		t.Fatalf("the footer names the reason, a blank line, the status line: warnings=%v footer=%q", m.snap.Warnings, lines[len(lines)-3:])
	}
	m.onRemote(remote.Msg{Host: "beta", State: remote.NoServer, Detail: "no server running on /tmp/tmux-0/default"})
	if lines = render(m, 28, 40); strings.TrimRight(lines[3], " ") != " ○ beta full  no tmux server" {
		t.Fatalf("no server row: %q", lines[3])
	}
	// a long host name yields to the state, keeping eight cells of itself
	m.hostList[0].Name = "macmini-office"
	m.remotes["macmini-office"] = m.remotes["beta"]
	if lines = render(m, 28, 40); strings.TrimRight(lines[3], " ") != " ○ macmini-o… no tmux server" {
		t.Fatalf("long name row (the mode word yields to the state): %q", lines[3])
	}
	m.hostList[0].Name = "beta"
	delete(m.remotes, "macmini-office")
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
	if _, err := hosts.Update(m.d.Store.Dir, func(s *hosts.Set) error {
		return s.Add(hosts.Host{Name: "gamma", Target: "gamma", Mode: hosts.ModePlain})
	}); err != nil {
		t.Fatal(err)
	}
	press := func(r rune) tea.Cmd {
		t.Helper()
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
		return cmd
	}
	run := func(cmd tea.Cmd, what string) {
		t.Helper()
		if cmd == nil {
			t.Fatalf("%s: no command", what)
		}
		if msg, ok := cmd().(hostToggleMsg); !ok || msg.err != nil {
			t.Fatalf("%s: %+v", what, msg)
		}
	}
	gamma := func() hosts.Host { set, _ := hosts.Load(m.d.Store.Dir); h, _ := set.Get("gamma"); return h }
	m.cursor[panelHosts] = 2 // gamma, disabled
	run(press('c'), "c connects")
	if !gamma().Enabled {
		t.Fatal("c must enable gamma")
	}
	run(press('d'), "d disconnects")
	if gamma().Enabled {
		t.Fatal("d must disable gamma")
	}
	run(press('m'), "m flips the mode")
	if gamma().Mode != hosts.ModeFull {
		t.Fatal("m must flip plain to full")
	}
	if press('r') != nil {
		t.Fatal("r without a manager does nothing")
	}
	// x asks first; anything but y keeps the host
	if press('x') != nil || m.confirmRemove != "gamma" {
		t.Fatalf("x must ask, confirm=%q", m.confirmRemove)
	}
	if foot := render(m, 28, 40); !strings.HasPrefix(foot[len(foot)-1], "remove gamma? y/n") {
		t.Fatalf("footer must ask: %q", foot[len(foot)-1])
	}
	if press('n') != nil || m.confirmRemove != "" || gamma().Name != "gamma" {
		t.Fatal("n keeps the host")
	}
	press('x')
	_ = os.MkdirAll(filepath.Join(hosts.Dir(m.d.Store.Dir, "gamma"), "agents"), 0o755)
	run(press('y'), "y removes")
	if set, _ := hosts.Load(m.d.Store.Dir); len(set.Hosts) != 0 {
		t.Fatalf("y must remove gamma: %+v", set.Hosts)
	}
	if _, err := os.Stat(hosts.Dir(m.d.Store.Dir, "gamma")); !os.IsNotExist(err) {
		t.Fatal("removal prunes the host's cache")
	}
	// i opens the details overlay (sized like the sidebar); Esc closes it
	m.cursor[panelHosts] = 1
	m.width, m.height = 40, 30
	if press('i') != nil || m.help == nil {
		t.Fatal("i opens the info overlay")
	}
	if view := strings.Join(render(m, 40, 30), "\n"); !strings.Contains(view, "host beta") || !strings.Contains(view, "target") || !strings.Contains(view, "connected") {
		t.Fatalf("info overlay:\n%s", view)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m = next.(Model); m.help != nil {
		t.Fatal("Esc closes the overlay")
	}
	// Space peeks: no outer here, so no command, and the keyboard stays in the sidebar
	m.focused = true
	if next, cmd := m.Update(tea.KeyMsg{Type: tea.KeySpace}); cmd != nil || !next.(Model).focused {
		t.Fatal("space keeps the keyboard in the sidebar")
	}
	if foot := render(m, 28, 40); !strings.HasPrefix(foot[len(foot)-2], "⏎ front · c d r m x i I") || strings.TrimSpace(foot[len(foot)-1]) != "? help" { // two lines at 28
		t.Fatalf("servers footer hint: %q", foot[len(foot)-2:])
	}
	// a host whose flok predates the key relay is called out in the footer and the info overlay
	m.onRemote(remote.Msg{Host: "beta", State: remote.Connected, Hello: &proto.Hello{Proto: 1, Version: "0.4.6", TmuxVersion: "3.4"}})
	m.refederate()
	if view := strings.Join(render(m, 60, 30), "\n"); !strings.Contains(view, "beta: upgrade flok there, no key relay (0.4.6)") {
		t.Fatalf("old flok warning:\n%s", view)
	}
	if press('i') != nil || m.help == nil {
		t.Fatal("i opens the info overlay")
	}
	if view := strings.Join(render(m, 60, 30), "\n"); !strings.Contains(view, "none: flok 0.4.6 there pred") { // the overlay is as wide as the sidebar
		t.Fatalf("info overlay, old flok:\n%s", view)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	m.onRemote(remote.Msg{Host: "beta", State: remote.Connected, Hello: &proto.Hello{Proto: 1, Version: "0.5.0", TmuxVersion: "3.4", Features: proto.ServeFeatures}})
	m.refederate()
	if foot := render(m, 28, 40); !strings.HasPrefix(foot[len(foot)-2], "⏎ front · c d r m x i I") {
		t.Fatalf("footer hint back with a current flok: %q", foot[len(foot)-2:])
	}
	m.panel = panelAgents
	if foot := render(m, 28, 40); !strings.HasPrefix(foot[len(foot)-1], "j/k ⏎ ⇥ 1-9") {
		t.Fatalf("agents footer hint: %q", foot[len(foot)-1])
	}
	if secs := SidebarKeySections(true); len(secs) != 2 || secs[1].Name != "sidebar · servers" || len(SidebarKeySections(false)) != 1 {
		t.Fatal("help sections")
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

func TestSiblingPaneAndFrontResync(t *testing.T) {
	if siblingPane("%0\n%7\n", "%0") != "%7" || siblingPane("%0\n", "%0") != "" || siblingPane("", "%0") != "" {
		t.Fatal("siblingPane")
	}
	dir := t.TempDir()
	t.Setenv("FLOK_STATE", dir)
	if err := launcher.WriteRuntime(launcher.Runtime{RightPane: "%1", LocalPane: "%1", Hosts: map[string]launcher.HostPane{"beta": {Pane: "%5", Window: "@3"}}}); err != nil {
		t.Fatal(err)
	}
	m := multiHostModel(t)
	m.d.RightPane = "%1"
	if m.resyncFront("%1") != nil || m.front != "" {
		t.Fatal("the believed pane in front: nothing to do")
	}
	// tmux shows beta's pane next to the sidebar (a stray swap): follow it
	m.resyncFront("%5")
	rt, _ := launcher.ReadRuntime()
	if m.front != "beta" || m.d.RightPane != "%5" || rt.FrontHost != "beta" || rt.RightPane != "%5" || len(m.snap.Spaces) != 1 || m.snap.Spaces[0].SessionName != "web" {
		t.Fatalf("resync to beta: front=%q right=%q runtime=%+v spaces=%+v", m.front, m.d.RightPane, rt, m.snap.Spaces)
	}
	// a pane we do not know is left alone; back to the local pane is followed again
	m.resyncFront("%9")
	if m.front != "beta" {
		t.Fatal("unknown pane must not change the front")
	}
	m.resyncFront("%1")
	if m.front != "" || m.d.RightPane != "%1" {
		t.Fatalf("resync to local: front=%q right=%q", m.front, m.d.RightPane)
	}
	if m.resyncFront("") != nil { // no outer in tests: no recovery command, and no change
		t.Fatal("recovery needs the outer")
	}
	if m.front != "" || m.d.RightPane != "%1" {
		t.Fatal("an empty window changes nothing without an outer")
	}
}

func TestCursorBarWhenFocused(t *testing.T) {
	m := multiHostModel(t)
	m.panel, m.cursor[panelHosts] = panelHosts, 1
	if lines := render(m, 28, 40); !strings.HasPrefix(lines[3], " ● beta full") {
		t.Fatalf("unfocused: a blank first cell, %q", lines[3])
	}
	m.focused = true
	if lines := render(m, 28, 40); !strings.HasPrefix(lines[3], "›● beta full") || !strings.HasPrefix(lines[2], " ○ local") {
		t.Fatalf("focused: the selected row carries the cursor, %q / %q", lines[3], lines[2])
	}
	m.panel, m.cursor[panelAgents] = panelAgents, 0
	lines := render(m, 28, 40)
	agentsHdr := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "agents") {
			agentsHdr = i
		}
	}
	if !strings.HasPrefix(lines[agentsHdr+1], "›● api") || !strings.HasPrefix(lines[agentsHdr+2], "   beta · claude") {
		t.Fatalf("agent cursor: %q / %q", lines[agentsHdr+1], lines[agentsHdr+2])
	}
	m.panel = panelSpaces
	if lines := render(m, 28, 40); !strings.HasPrefix(lines[7], "›○ Alpha") {
		t.Fatalf("session cursor: %q", lines[7])
	}
}

// Mailbox requests may spell a host in any case (flok goto DOCKERams:%3 from a script); the
// sidebar resolves it to the registered name before its exact comparisons.
func TestRequestsMatchHostsInAnyCase(t *testing.T) {
	m := multiHostModel(t)
	m.front = "beta" // no outer here: only the front host's goto builds a command
	if cmd := m.runRequest(state.Request{Cmd: "goto", Host: "BETA", Pane: "%1"}); cmd == nil || m.errText != "" {
		t.Fatalf("goto BETA:%%1 must reach beta's agent: cmd=%v err=%q", cmd != nil, m.errText)
	}
	if cmd := m.runRequest(state.Request{Cmd: "goto", Host: "nope", Pane: "%1"}); cmd != nil || m.errText == "" {
		t.Fatalf("an unknown host still fails: cmd=%v err=%q", cmd != nil, m.errText)
	}
}

func TestInstallKeyAndNoFlokNotice(t *testing.T) {
	m := multiHostModel(t)
	m.d.Bin = "/x/flok"
	var started []string
	old := startDetached
	startDetached = func(bin string, args ...string) error {
		started = append(started, bin+" "+strings.Join(args, " "))
		return nil
	}
	defer func() { startDetached = old }()
	m.focused, m.panel = true, panelHosts
	m.cursor[panelHosts] = 1 // beta
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'I'}})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("I on a host row starts the install")
	}
	cmd()
	if len(started) != 1 || started[0] != "/x/flok host install beta --open" {
		t.Fatalf("started %v", started)
	}
	m.cursor[panelHosts] = 0
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'I'}}); cmd != nil {
		t.Fatal("I on the local row does nothing")
	}
	// a host without flok: the footer says how to install it, and the key hints stay
	m.onRemote(remote.Msg{Host: "beta", State: remote.NoFlok})
	lines := render(m, 40, 30)
	if joined := strings.Join(lines, "\n"); !strings.Contains(joined, "beta: no flok there · I installs it") || !strings.Contains(joined, "(flok host install beta)") { // wrapped at 40
		t.Fatalf("no-flok notice:\n%s", joined)
	}
	if foot := strings.TrimRight(lines[len(lines)-1], " "); !strings.HasPrefix(foot, "⏎ front · c d r m x i I") || !strings.HasSuffix(foot, "? help") {
		t.Fatalf("footer at 40: %q", foot)
	}
	lines = render(m, 28, 30)
	if keysLine, help := strings.TrimRight(lines[len(lines)-2], " "), strings.TrimRight(lines[len(lines)-1], " "); keysLine != "⏎ front · c d r m x i I" || help != "                      ? help" {
		t.Fatalf("footer at 28 wraps the hints onto two lines: %q %q", keysLine, help)
	}
	m.onRemote(remote.Msg{Host: "beta", State: remote.OldFlok, Detail: "flok 0.4.4 there is too old (no serve)"})
	if joined := strings.Join(render(m, 70, 30), "\n"); !strings.Contains(joined, "beta: flok 0.4.4 there is too old (no serve) · I installs this flok") {
		t.Fatalf("old-flok notice:\n%s", joined)
	}
	// the reconnect request: an unknown host lands in the footer, a known one needs the manager
	if cmd := m.runRequest(state.Request{Cmd: "reconnect", Host: "nope"}); cmd != nil || m.errText != "no host nope" {
		t.Fatalf("reconnect unknown: %v %q", cmd, m.errText)
	}
	m.errText = ""
	if cmd := m.runRequest(state.Request{Cmd: "reconnect", Host: "BETA"}); cmd != nil || m.errText != "" {
		t.Fatalf("reconnect without a manager: %v %q", cmd, m.errText)
	}
	found := false
	for _, b := range SidebarKeySections(true)[1].Bindings {
		found = found || b.Key == "I"
	}
	if !found {
		t.Fatal("the servers help lists I")
	}
}

// Messages carry the generation of the connection that sent them: an older connection's last
// words are dropped, and a host whose connection is rebuilt reads connecting until the new
// one speaks.
func TestRemoteGenerationsAndRebuiltConnections(t *testing.T) {
	m := multiHostModel(t)
	m.onRemote(remote.Msg{Host: "beta", State: remote.Connecting, Gen: 2})
	m.onRemote(remote.Msg{Host: "beta", State: remote.Connected, Gen: 1, Hello: &proto.Hello{Proto: 1}})
	if v := m.remotes["beta"]; v.state != remote.Connecting || v.gen != 2 {
		t.Fatalf("an older generation must not win: %+v", v)
	}
	m.onRemote(remote.Msg{Host: "beta", State: remote.Connected, Gen: 2, Hello: &proto.Hello{Proto: 1, Features: proto.ServeFeatures}})
	m.onRemote(remote.Msg{Host: "beta", State: remote.Connected, Gen: 2, Snap: &proto.Snapshot{}})
	if v := m.remotes["beta"]; v.state != remote.Connected || !v.hasSnap {
		t.Fatalf("the current generation applies: %+v", v)
	}
	// a mode change rebuilds the connection: the row reads connecting right away
	set := m.hostSet
	set.Hosts = append([]hosts.Host(nil), m.hostSet.Hosts...)
	set.Hosts[0].Mode = hosts.ModePlain
	m.applyHosts(set)
	if v := m.remotes["beta"]; v.state != remote.Connecting || v.hasSnap || v.gen != 2 {
		t.Fatalf("a rebuilt connection starts as connecting: %+v", v)
	}
	if lines := render(m, 40, 30); !strings.Contains(strings.Join(lines, "\n"), "beta plain") || !strings.Contains(strings.Join(lines, "\n"), "connecting") {
		t.Fatalf("row after the flip:\n%s", strings.Join(lines[:6], "\n"))
	}
	m.onRemote(remote.Msg{Host: "beta", State: remote.Connected, Gen: 3, Hello: &proto.Hello{Proto: 1, Version: "plain"}})
	// a change that keeps the connection (the attach session) leaves the view alone
	set2 := set
	set2.Hosts = append([]hosts.Host(nil), set.Hosts...)
	set2.Hosts[0].Session = "work"
	m.applyHosts(set2)
	if v := m.remotes["beta"]; v.state != remote.Connected || v.gen != 3 {
		t.Fatalf("an attach change keeps the view: %+v", v)
	}
}
