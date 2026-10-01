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
	if !IsKeyCommand("jump") || IsKeyCommand("goto") || IsKeyCommand("") || !IsKeyCommand("host next") || !IsKeyCommand("host front 3") || IsKeyCommand("host front 10") || IsKeyCommand("host remove x") ||
		!IsKeyCommand("menu agents") || !IsKeyCommand("menu sessions") || !IsKeyCommand("menu servers") || !IsKeyCommand("host menu") || !IsKeyCommand("prev") || IsKeyCommand("menu x") {
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

// keysClient answers the reads the installers make (list-keys, the prefix options, flok's
// records) from canned state and records every call.
type keysClient struct {
	calls   [][]string
	keys    string            // the prefix table; "" = a stock o and c
	prefix  string            // "" = C-b
	prefix2 string            // "" = None
	records map[string]string // record name -> the note an earlier session left (without the option name)
}

func (k *keysClient) Label() string { return "fake" }
func (k *keysClient) Run(args ...string) (string, error) {
	k.calls = append(k.calls, args)
	joined := strings.Join(args, " ")
	var out strings.Builder
	if strings.HasPrefix(joined, "show-options -gv prefix ;") {
		p, p2 := k.prefix, k.prefix2
		if p == "" {
			p = "C-b"
		}
		if p2 == "" {
			p2 = "None"
		}
		out.WriteString(p + "\n" + p2 + "\n")
	}
	if strings.Contains(joined, "list-keys") {
		if k.keys != "" {
			out.WriteString(k.keys)
		} else {
			out.WriteString("bind-key    -T prefix       o                 select-pane -t :.+\nbind-key    -T prefix       c                 new-window\n")
		}
	}
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "show-options" && args[i+1] == "-gqv" {
			if v, ok := k.records[strings.TrimPrefix(args[i+2], "@flok-orig-")]; ok {
				out.WriteString(v + "\n")
			}
		}
	}
	return out.String(), nil
}

func joined(c *keysClient, i int) string { return strings.Join(c.calls[i], " ") }

func TestLocalKeys(t *testing.T) {
	dir := t.TempDir()
	// tmux's own o and ?, the snippet's b already there
	table := "bind-key    -T prefix       o                 select-pane -t :.+\n" +
		"bind-key    -T prefix       ?                 list-keys -N\n" +
		"bind-key    -T prefix       b                 run-shell -b \"/opt/homebrew/bin/flok toggle\"\n" +
		"bind-key    -T prefix       c                 new-window\n"
	c := &keysClient{keys: table}
	installed, conflicts := InstallLocalKeys(c, dir, "/x/flok", "missing")
	if got := strings.Join(installed, ""); !strings.HasPrefix(got, "aBguASHNPOF1") || len(installed) != 19 {
		t.Fatalf("installed %v", installed)
	}
	if len(conflicts) != 2 || conflicts["o"] != "select-pane -t :.+" || conflicts["?"] != "list-keys -N" {
		t.Fatalf("conflicts %v", conflicts)
	}
	bind := strings.Join(c.calls[len(c.calls)-1], " ")
	if !strings.Contains(bind, "bind-key -T prefix a run-shell -b /x/flok next --client '#{client_tty}'") || strings.Contains(bind, "prefix o") || strings.Contains(bind, "prefix b ") {
		t.Fatalf("bind call %q", bind)
	}
	if st := loadLocalKeys(dir); len(st.Installed) != 19 || len(st.Saved) != 0 {
		t.Fatalf("keys.json %+v", st)
	}
	// a second sidebar (reload) with the keys now present: nothing new, the record stays
	c.keys = table + "bind-key    -T prefix       a                 run-shell -b \"/x/flok next --client '#{client_tty}'\"\n"
	if again, _ := InstallLocalKeys(c, dir, "/x/flok", "missing"); !strings.HasPrefix(strings.Join(again, ""), "BguASH") || len(again) != 18 {
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

const allKeys = "b B g o a u A S H N P O F1 F2 F3 F4 F5 F6 F7 F8 F9"

func TestInstallRebindRestore(t *testing.T) {
	c := &keysClient{}
	k := InstallKeys(c, BindOptionArgs(), "")
	read, write := joined(c, 0), joined(c, 1)
	if len(c.calls) != 2 || !strings.HasPrefix(read, "show-options -gv prefix ; show-options -gv prefix2 ; list-keys -T prefix ; show-options -gqv @flok-orig-keys ; show-options -gqv @flok-orig-prefix ; show-options -gqv @flok-orig-prefix2 ; show-options -gqv @flok-orig-b ;") ||
		!strings.HasSuffix(read, "; show-options -gqv @flok-orig-F9") {
		t.Fatalf("install reads the prefix options, the table and the records in one call: %v", c.calls)
	}
	if !strings.HasPrefix(write, "set-option -g @flok-orig-b b ; set-option -g @flok-orig-B B ;") ||
		!strings.Contains(write, "; set-option -g @flok-orig-o o bind-key    -T prefix       o                 select-pane -t :.+ ;") ||
		!strings.HasSuffix(write, " ; set-option -g @flok-orig-keys keys "+allKeys+" ; "+strings.Join(BindOptionArgs(), " ")) ||
		strings.Contains(write, "set-option -g prefix") {
		t.Fatalf("install records the originals and binds in one call, no prefix asked: %s", write)
	}
	if !reflect.DeepEqual(k.saved, []string{"bind-key    -T prefix       o                 select-pane -t :.+"}) {
		t.Fatalf("saved %q", k.saved)
	}
	k.Rebind()
	if len(c.calls) != 4 || c.calls[2][0] != "show-options" || joined(c, 3) != write {
		t.Fatalf("rebind reads and installs again: %v", c.calls[2:])
	}
	k.Restore()
	restore := joined(c, 4)
	if len(c.calls) != 5 || !strings.HasPrefix(restore, "unbind-key -T prefix b ; unbind-key -T prefix B ;") ||
		!strings.Contains(restore, "unbind-key -T prefix F9 ; bind-key -T prefix o select-pane -t :.+ ; set-option -gqu @flok-orig-b ;") ||
		!strings.HasSuffix(restore, "set-option -gqu @flok-orig-F9 ; set-option -gqu @flok-orig-keys") {
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
		"bind-key    -T prefix       g                 set-option -g @flok-request focus\n",
		records: map[string]string{"keys": "keys " + allKeys, "o": "o bind-key -T prefix o select-pane -t :.+", "b": "b", "g": "g bind-key -T prefix g new-window", "a": "a bind-key -T prefix a display hi"}}
	k := InstallKeys(c, BindOptionArgs(), "")
	if !reflect.DeepEqual(k.saved, []string{"bind-key -T prefix g new-window", "bind-key -T prefix o select-pane -t :.+", "bind-key -T prefix a display hi"}) { // binding order
		t.Fatalf("saved %q", k.saved)
	}
	if write := joined(c, 1); !strings.Contains(write, "set-option -g @flok-orig-o o bind-key -T prefix o select-pane -t :.+ ;") || !strings.Contains(write, "set-option -g @flok-orig-b b ;") {
		t.Fatalf("records rewritten: %s", write)
	}
	k.Restore()
	if restore := joined(c, 2); !strings.Contains(restore, "; bind-key -T prefix g new-window ; bind-key -T prefix o select-pane -t :.+ ; bind-key -T prefix a display hi ; set-option -gqu") {
		t.Fatalf("restore: %s", restore)
	}
}

func TestPrefixMirror(t *testing.T) {
	c := &keysClient{keys: "bind-key    -T prefix       o                 select-pane -t :.+\nbind-key    -T prefix       C-b               send-prefix\n"}
	k := InstallKeys(c, BindOptionArgs(), "C-a")
	write := joined(c, 1)
	for _, want := range []string{
		"set-option -g @flok-orig-C-a C-a ; set-option -g @flok-orig-C-b C-b bind-key    -T prefix       C-b               send-prefix ; " +
			"set-option -g @flok-orig-prefix prefix C-b ; set-option -g prefix C-a ; set-option -g @flok-orig-prefix2 prefix2 None ; set-option -g prefix2 C-b ; " +
			"set-option -g @flok-orig-keys keys " + allKeys + " C-a C-b ; bind-key -T prefix b set-option -g @flok-request toggle",
		"; bind-key -T prefix C-a send-prefix ; bind-key -T prefix C-b send-prefix -2",
	} {
		if !strings.Contains(write, want) {
			t.Fatalf("install with a prefix to mirror:\nwant %s\nin   %s", want, write)
		}
	}
	if !strings.HasSuffix(write, "send-prefix -2") || !reflect.DeepEqual(k.bound[len(k.bound)-2:], []string{"C-a", "C-b"}) {
		t.Fatalf("chords last: %s / %v", write, k.bound)
	}
	k.Restore()
	restore := joined(c, 2)
	if !strings.Contains(restore, "unbind-key -T prefix F9 ; unbind-key -T prefix C-a ; unbind-key -T prefix C-b ; bind-key -T prefix o select-pane -t :.+ ; bind-key -T prefix C-b send-prefix ; set-option -g prefix C-b ; set-option -g prefix2 None ; set-option -gqu @flok-orig-b ;") ||
		!strings.HasSuffix(restore, "set-option -gqu @flok-orig-C-b ; set-option -gqu @flok-orig-prefix ; set-option -gqu @flok-orig-prefix2 ; set-option -gqu @flok-orig-keys") {
		t.Fatalf("restore gives the prefix and the chords back: %s", restore)
	}

	// the host already uses the same prefix: nothing to mirror
	same := &keysClient{prefix: "C-a"}
	InstallKeys(same, BindOptionArgs(), "C-a")
	if w := joined(same, 1); strings.Contains(w, "set-option -g prefix") || strings.Contains(w, "send-prefix") || !strings.HasSuffix(w, " "+allKeys+" ; "+strings.Join(BindOptionArgs(), " ")) {
		t.Fatalf("same prefix: %s", w)
	}
	// the host has a prefix2 of its own: it is left alone, only the local chord is added
	two := &keysClient{prefix2: "C-Space"}
	InstallKeys(two, BindOptionArgs(), "C-a")
	if w := joined(two, 1); !strings.Contains(w, "set-option -g prefix C-a ;") || strings.Contains(w, "prefix2") || strings.Contains(w, "send-prefix -2") || !strings.HasSuffix(w, "bind-key -T prefix C-a send-prefix") {
		t.Fatalf("own prefix2: %s", w)
	}
	// full mode learns the prefix after the hello: a second install adds it; repeating it is free
	late := &keysClient{}
	kl := InstallKeys(late, BindOptionArgs(), "")
	kl.SetPrefix("C-a")
	if len(late.calls) != 4 || !strings.Contains(joined(late, 3), "set-option -g prefix C-a ;") {
		t.Fatalf("late prefix: %v", late.calls)
	}
	kl.SetPrefix("C-a")
	if len(late.calls) != 4 {
		t.Fatal("unchanged prefix is a no-op")
	}
	kl.Restore()
	if r := joined(late, 4); !strings.Contains(r, "set-option -g prefix C-b ; set-option -g prefix2 None ;") {
		t.Fatalf("late prefix restored: %s", r)
	}

	// a dead session left the prefix mirrored: the records name the host's own, the current
	// value (flok's) is not taken for it, and the restore gives the host its own back
	dead := &keysClient{prefix: "C-a", prefix2: "C-b",
		keys: "bind-key    -T prefix       o                 set-option -g @flok-request jump\nbind-key    -T prefix       C-a               send-prefix\nbind-key    -T prefix       C-b               send-prefix -2\n",
		records: map[string]string{"keys": "keys " + allKeys + " C-a C-b", "prefix": "prefix C-b", "prefix2": "prefix2 None",
			"o": "o bind-key -T prefix o select-pane -t :.+", "C-a": "C-a", "C-b": "C-b bind-key -T prefix C-b send-prefix"}}
	kd := InstallKeys(dead, BindOptionArgs(), "C-a")
	if kd.orig != (prefixState{mirrored: true, prefix: "C-b", setPrefix2: true, prefix2: "None"}) || !reflect.DeepEqual(kd.saved, []string{"bind-key -T prefix o select-pane -t :.+", "bind-key -T prefix C-b send-prefix"}) {
		t.Fatalf("recovered: %+v %q", kd.orig, kd.saved)
	}
	kd.Restore()
	if r := joined(dead, len(dead.calls)-1); !strings.Contains(r, "bind-key -T prefix C-b send-prefix ; set-option -g prefix C-b ; set-option -g prefix2 None ;") {
		t.Fatalf("recovered restore: %s", r)
	}
	// the local prefix changed since that session: its old chord goes back with this install
	moved := &keysClient{prefix: "C-a", prefix2: "C-b", keys: dead.keys, records: dead.records}
	InstallKeys(moved, BindOptionArgs(), "C-Space")
	if w := joined(moved, len(moved.calls)-1); !strings.Contains(w, "set-option -g prefix C-Space ;") || !strings.Contains(w, "; bind-key -T prefix C-Space send-prefix ;") ||
		!strings.Contains(w, "; unbind-key -T prefix C-a ; set-option -gqu @flok-orig-C-a") {
		t.Fatalf("moved prefix: %s", w)
	}
}
