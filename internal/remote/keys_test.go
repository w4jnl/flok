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
	kinds := map[string]string{}
	var saved []string
	for _, b := range parsePrefixBindings("bind-key    -T prefix       o                 select-pane -t :.+\n" +
		"bind-key -r -T prefix       b                 run-shell \"tmux display 'hi there'\"\n" +
		"bind-key    -T prefix       c                 new-window\n" +
		"bind-key    -T prefix       g                 set-option -g @flok-request focus\n" +
		"bind-key    -T prefix       u                 run-shell -b \"/x/flok relay keep-awake\"\n" +
		"bind-key    -T prefix       a                 run-shell -b \"flok next\"\n" +
		"bind-key    -T copy-mode-vi b                 send -X cursor-left\n") {
		switch {
		case b.ours:
			kinds[b.key] = "ours"
		case b.flok:
			kinds[b.key] = "flok"
		default:
			kinds[b.key] = "host"
			saved = append(saved, b.line)
		}
	}
	if !reflect.DeepEqual(kinds, map[string]string{"o": "host", "b": "host", "c": "host", "g": "ours", "u": "ours", "a": "flok"}) {
		t.Fatalf("kinds %v", kinds)
	}
	if len(saved) != 3 || !strings.HasPrefix(saved[0], "bind-key    -T prefix       o") || !strings.Contains(saved[1], "hi there") {
		t.Fatalf("saved %q", saved)
	}
	if w := SplitTmuxWords(saved[1]); !reflect.DeepEqual(w, []string{"bind-key", "-r", "-T", "prefix", "b", "run-shell", "tmux display 'hi there'"}) {
		t.Fatalf("words %q", w)
	}
	if got := strings.Join(bindArgs([]string{saved[0], saved[1], `bind-key -T prefix x run-shell a \; display b`, "not a binding"}), " "); got !=
		`bind-key -T prefix o select-pane -t :.+ ; bind-key -r -T prefix b run-shell tmux display 'hi there' ; bind-key -T prefix x run-shell a \; display b` {
		t.Fatalf("bindArgs %q", got)
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
	read, write := strings.Join(c.calls[0], " "), strings.Join(c.calls[1], " ")
	if len(c.calls) != 2 || !strings.HasPrefix(read, "list-keys -T prefix ; show-options -gqv @flok-orig-b ;") || !strings.Contains(read, "; show-options -gqv @flok-orig-F9") {
		t.Fatalf("install reads the table and the records in one call: %v", c.calls)
	}
	if !strings.HasPrefix(write, "set-option -g @flok-orig-b b ; set-option -g @flok-orig-B B ;") ||
		!strings.Contains(write, "; set-option -g @flok-orig-o o bind-key    -T prefix       o                 select-pane -t :.+ ;") ||
		!strings.HasSuffix(write, " ; "+strings.Join(BindOptionArgs(), " ")) {
		t.Fatalf("install records the originals and binds in one call: %s", write)
	}
	if !reflect.DeepEqual(k.saved, []string{"bind-key    -T prefix       o                 select-pane -t :.+"}) {
		t.Fatalf("saved %q", k.saved)
	}
	k.Rebind()
	if len(c.calls) != 4 || c.calls[2][0] != "list-keys" || strings.Join(c.calls[3], " ") != write {
		t.Fatalf("rebind reads and installs again: %v", c.calls[2:])
	}
	k.Restore()
	restore := strings.Join(c.calls[4], " ")
	if len(c.calls) != 5 || !strings.HasPrefix(restore, "unbind-key -T prefix b ; unbind-key -T prefix B ;") ||
		!strings.Contains(restore, "unbind-key -T prefix F9 ; bind-key -T prefix o select-pane -t :.+ ; set-option -gqu @flok-orig-b ;") ||
		!strings.HasSuffix(restore, "set-option -gqu @flok-orig-F9") {
		t.Fatalf("restore unbinds, puts the host's o back and drops the records in one call: %s", restore)
	}
	k.Rebind()
	if len(c.calls) != 5 {
		t.Fatal("a rebind after restore is ignored")
	}
}

// A session that ended without restoring (a killed serve, a mode flip that raced it) leaves
// flok's bindings and its records behind: the next install keeps the recorded originals
// instead of adopting the leftovers, and even a key the interrupted restore had already
// unbound gets its original back.
func TestInstallRecoversRecordedOriginals(t *testing.T) {
	c := &keysClient{keys: "bind-key    -T prefix       o                 run-shell -b \"/x/flok relay jump\"\n" +
		"bind-key    -T prefix       c                 new-window\n" +
		"bind-key    -T prefix       g                 set-option -g @flok-request focus\n" +
		"o bind-key -T prefix o select-pane -t :.+\n" +
		"b\n" +
		"g bind-key -T prefix g new-window\n" +
		"a bind-key -T prefix a display hi\n"}
	k := InstallKeys(c, BindOptionArgs())
	if !reflect.DeepEqual(k.saved, []string{"bind-key -T prefix g new-window", "bind-key -T prefix o select-pane -t :.+", "bind-key -T prefix a display hi"}) { // binding order
		t.Fatalf("saved %q", k.saved)
	}
	write := strings.Join(c.calls[1], " ")
	if !strings.Contains(write, "set-option -g @flok-orig-o o bind-key -T prefix o select-pane -t :.+ ;") || !strings.Contains(write, "set-option -g @flok-orig-b b ;") {
		t.Fatalf("records rewritten: %s", write)
	}
	k.Restore()
	if restore := strings.Join(c.calls[2], " "); !strings.Contains(restore, "; bind-key -T prefix g new-window ; bind-key -T prefix o select-pane -t :.+ ; bind-key -T prefix a display hi ; set-option -gqu") {
		t.Fatalf("restore: %s", restore)
	}
}
