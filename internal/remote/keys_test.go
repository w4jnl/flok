package remote

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestKeyBindings(t *testing.T) {
	relay := strings.Join(BindRelayArgs("/opt/homebrew/bin/flok"), " ")
	if !strings.HasPrefix(relay, "bind-key -T prefix b run-shell -b /opt/homebrew/bin/flok relay toggle ; bind-key -T prefix B run-shell -b /opt/homebrew/bin/flok relay hide") ||
		!strings.Contains(relay, "prefix u run-shell -b /opt/homebrew/bin/flok relay keep-awake") || strings.Contains(relay, "?") {
		t.Fatalf("relay bindings %q", relay)
	}
	opt := strings.Join(BindOptionArgs(), " ")
	if !strings.Contains(opt, "bind-key -T prefix o set-option -g @flok-request jump") {
		t.Fatalf("option bindings %q", opt)
	}
	if u := strings.Join(UnbindArgs(keyNames(KeyCommands)), " "); !strings.HasPrefix(u, "unbind-key -T prefix b ; unbind-key -T prefix B") {
		t.Fatalf("unbind %q", u)
	}
	if !IsKeyCommand("jump") || IsKeyCommand("goto") || IsKeyCommand("") || !IsKeyCommand("host next") || !IsKeyCommand("host front 3") || IsKeyCommand("host front 10") || IsKeyCommand("host remove x") {
		t.Fatal("IsKeyCommand")
	}
	if got := strings.Join(BindRelayArgs("/x/flok"), " "); !strings.Contains(got, "prefix N run-shell -b /x/flok relay host next") || !strings.Contains(got, "prefix F9 run-shell -b /x/flok relay host front 9") {
		t.Fatalf("server keys relay %q", got)
	}
	saved := ourBindings("bind-key    -T prefix       o                 select-pane -t :.+\n" +
		"bind-key -r -T prefix       b                 run-shell \"tmux display 'hi there'\"\n" +
		"bind-key    -T prefix       c                 new-window\n" +
		"bind-key    -T prefix       g                 set-option -g @flok-request focus\n" +
		"bind-key    -T prefix       u                 run-shell -b \"/x/flok relay keep-awake\"\n" +
		"bind-key    -T copy-mode-vi b                 send -X cursor-left\n")
	if len(saved) != 2 || !strings.HasPrefix(saved[0], "bind-key    -T prefix       o") || !strings.Contains(saved[1], "hi there") {
		t.Fatalf("saved %q", saved)
	}
	if w := SplitTmuxWords(saved[1]); !reflect.DeepEqual(w, []string{"bind-key", "-r", "-T", "prefix", "b", "run-shell", "tmux display 'hi there'"}) {
		t.Fatalf("words %q", w)
	}
	if w := SplitTmuxWords(`a "b \"c\" d" 'e f' g\ h`); !reflect.DeepEqual(w, []string{"a", `b "c" d`, "e f", "g h"}) {
		t.Fatalf("words %q", w)
	}
}

// keysClient answers list-keys with a canned table and records every call.
type keysClient struct {
	calls [][]string
	keys  string
}

func (k *keysClient) Label() string { return "fake" }
func (k *keysClient) Run(args ...string) (string, error) {
	k.calls = append(k.calls, args)
	if len(args) > 0 && args[0] == "list-keys" {
		if k.keys != "" {
			return k.keys, nil
		}
		return "bind-key    -T prefix       o                 select-pane -t :.+\nbind-key    -T prefix       c                 new-window\n", nil
	}
	return "", nil
}

func TestLocalKeys(t *testing.T) {
	dir := t.TempDir()
	// tmux's own o and ?, the snippet's b already there
	table := "bind-key    -T prefix       o                 select-pane -t :.+\n" +
		"bind-key    -T prefix       ?                 list-keys -N\n" +
		"bind-key    -T prefix       b                 run-shell -b \"/opt/homebrew/bin/flok toggle\"\n" +
		"bind-key    -T prefix       c                 new-window\n"
	c := &keysClient{keys: table}
	installed, conflicts := InstallLocalKeys(c, dir, "/x/flok", "missing")
	if got := strings.Join(installed, ""); !strings.HasPrefix(got, "aABguNPOSF1") || len(installed) != 18 {
		t.Fatalf("installed %v", installed)
	}
	if len(conflicts) != 2 || conflicts["o"] != "select-pane -t :.+" || conflicts["?"] != "list-keys -N" {
		t.Fatalf("conflicts %v", conflicts)
	}
	bind := strings.Join(c.calls[len(c.calls)-1], " ")
	if !strings.Contains(bind, "bind-key -T prefix a run-shell -b /x/flok next --client '#{client_tty}'") || strings.Contains(bind, "prefix o") || strings.Contains(bind, "prefix b ") {
		t.Fatalf("bind call %q", bind)
	}
	if st := loadLocalKeys(dir); len(st.Installed) != 18 || len(st.Saved) != 0 {
		t.Fatalf("keys.json %+v", st)
	}
	// a second sidebar (reload) with the keys now present: nothing new, the record stays
	c.keys = table + "bind-key    -T prefix       a                 run-shell -b \"/x/flok next --client '#{client_tty}'\"\n"
	if again, _ := InstallLocalKeys(c, dir, "/x/flok", "missing"); !strings.HasPrefix(strings.Join(again, ""), "ABgu") || len(again) != 17 {
		t.Fatalf("second install %v", again)
	}
	status := LocalKeyStatus(c)
	if status["a"] != "flok" || status["o"] != "select-pane -t :.+" || status["g"] != "" {
		t.Fatalf("status %v", status)
	}
	c.calls = nil
	if err := RestoreLocalKeys(c, dir); err != nil {
		t.Fatal(err)
	}
	if u := strings.Join(c.calls[0], " "); !strings.HasPrefix(u, "unbind-key -T prefix A ; unbind-key -T prefix B") || len(c.calls) != 1 {
		t.Fatalf("restore %v", c.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, LocalKeysFile)); !os.IsNotExist(err) {
		t.Fatal("restore removes the record")
	}
	if RestoreLocalKeys(c, dir) != nil {
		t.Fatal("a second restore is a no-op")
	}
	// mode all replaces tmux's own and puts them back
	c = &keysClient{keys: table}
	installed, conflicts = InstallLocalKeys(c, dir, "/x/flok", "all")
	if len(conflicts) != 0 || !strings.Contains(strings.Join(installed, ""), "o") {
		t.Fatalf("all: %v %v", installed, conflicts)
	}
	if st := loadLocalKeys(dir); len(st.Saved) != 2 {
		t.Fatalf("all must save o and ?: %+v", st)
	}
	c.calls = nil
	_ = RestoreLocalKeys(c, dir)
	joined := ""
	for _, call := range c.calls {
		joined += strings.Join(call, " ") + "\n"
	}
	if !strings.Contains(joined, "bind-key -T prefix o select-pane -t :.+") || !strings.Contains(joined, "bind-key -T prefix ? list-keys -N") {
		t.Fatalf("restore after all: %s", joined)
	}
	if got, _ := InstallLocalKeys(c, dir, "/x/flok", "off"); got != nil {
		t.Fatal("off binds nothing")
	}
}

func TestInstallRebindRestore(t *testing.T) {
	c := &keysClient{}
	k := InstallKeys(c, BindOptionArgs())
	if len(c.calls) != 2 || c.calls[0][0] != "list-keys" || c.calls[1][0] != "bind-key" {
		t.Fatalf("install: %v", c.calls)
	}
	k.Rebind()
	if len(c.calls) != 3 || strings.Join(c.calls[2], " ") != strings.Join(BindOptionArgs(), " ") {
		t.Fatalf("rebind: %v", c.calls[2:])
	}
	k.Restore()
	if len(c.calls) != 5 || c.calls[3][0] != "unbind-key" || strings.Join(c.calls[4], " ") != "bind-key -T prefix o select-pane -t :.+" {
		t.Fatalf("restore must put the host's o back with a full bind-key command: %v", c.calls[3:])
	}
	k.Rebind()
	if len(c.calls) != 5 {
		t.Fatal("a rebind after restore is ignored")
	}
}
