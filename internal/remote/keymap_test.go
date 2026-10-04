package remote

import (
	"strings"
	"testing"
)

func TestKeyMapValidatesAndSorts(t *testing.T) {
	cmds, bad := KeyMap(map[string]string{"l": "last window", "Tab": " last  session ", ";": "last pane", "x": "switch-client -l", "y": "prev", "": "jump"})
	if len(cmds) != 3 || cmds[0].Key != ";" || cmds[1].Key != "Tab" || cmds[1].Cmd != "last session" || cmds[2].Key != "l" {
		t.Fatalf("cmds %+v", cmds)
	}
	if len(bad) != 3 || !strings.Contains(strings.Join(bad, "|"), "x → switch-client -l") {
		t.Fatalf("bad %v", bad)
	}
	if !IsKeyCommand("last server") || IsKeyCommand("last") {
		t.Fatal("the last commands are key commands")
	}
	set := withExtra(KeyCommands, []KeyCommand{{"O", "last server"}, {"Tab", "last session"}})
	n := 0
	for _, k := range set {
		if k.Key == "O" {
			n++
			if k.Cmd != "last server" {
				t.Fatalf("a mapped key replaces the default: %+v", k)
			}
		}
	}
	if n != 1 || set[len(set)-1].Key != "Tab" || len(set) != len(KeyCommands)+1 {
		t.Fatalf("set %+v", set)
	}
}

// A mapped key is bound whatever it held, locally (saved and restored like mode all) and on a
// host (relayed or set as the option), and ; survives as a key name.
func TestMappedKeysLocalAndRemote(t *testing.T) {
	extra := []KeyCommand{{";", "last pane"}, {"Tab", "last session"}}
	dir := t.TempDir()
	c := &keysClient{keys: "bind-key    -T prefix       Tab               switch-client -l\n" +
		"bind-key    -T prefix       \\;                last-pane\n"}
	installed, conflicts, _ := InstallLocalKeys(c, dir, "/x/flok", "missing", extra)
	got := strings.Join(installed, " ")
	if !strings.Contains(got, " Tab") || !strings.Contains(got, " ;") || len(conflicts) != 0 {
		t.Fatalf("installed %v conflicts %v", installed, conflicts)
	}
	bind := strings.Join(c.calls[len(c.calls)-1], " ")
	if !strings.Contains(bind, `bind-key -T prefix Tab run-shell -b /x/flok last session`) || !strings.Contains(bind, `bind-key -T prefix \; run-shell -b /x/flok last pane`) {
		t.Fatalf("bind %q", bind)
	}
	if st := loadLocalKeys(dir); len(st.Saved) != 2 || !strings.Contains(strings.Join(st.Saved, "|"), "Tab               switch-client -l") {
		t.Fatalf("saved %+v", st)
	}
	c.calls = nil
	_ = RestoreLocalKeys(c, dir)
	joined := ""
	for _, call := range c.calls {
		joined += strings.Join(call, " ") + "\n"
	}
	if !strings.Contains(joined, `unbind-key -T prefix \;`) || !strings.Contains(joined, "bind-key -T prefix Tab switch-client -l") || !strings.Contains(joined, `bind-key -T prefix \; last-pane`) {
		t.Fatalf("restore %s", joined)
	}
	// a host: the option binder carries the mapped keys, the held record names them, SetKeys re-installs on a change
	h := &keysClient{keys: ""}
	k := InstallKeys(h, OptionBinder(), "", extra)
	all := ""
	for _, call := range h.calls {
		all += strings.Join(call, " ") + "\n"
	}
	if !strings.Contains(all, "bind-key -T prefix Tab set-option -g @flok-request last session") || !strings.Contains(all, `bind-key -T prefix \; set-option -g @flok-request last pane`) || !strings.Contains(all, "keys b B g o a u A S @ N P O F1 F2 F3 F4 F5 F6 F7 F8 F9 semicolon Tab") || !strings.Contains(all, "set-option -g @flok-orig-semicolon semicolon") {
		t.Fatalf("host install:\n%s", all)
	}
	n := len(h.calls)
	k.SetKeys("", extra) // nothing changed
	if len(h.calls) != n {
		t.Fatal("an unchanged set is not re-installed")
	}
	k.SetKeys("", []KeyCommand{{"Tab", "last session"}})
	if len(h.calls) == n {
		t.Fatal("a changed set is re-installed")
	}
}
