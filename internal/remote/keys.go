package remote

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

// KeyCommand is one flok action bound in a host's prefix table while the host is connected:
// the same keys as the local tmux snippet, `?` excepted (tmux's own list-keys stays there).
type KeyCommand struct{ Key, Cmd string }

// KeyCommands in binding order: the agent keys, then the server keys (N/P next and previous
// server, O the last one, S the servers menu, F1…F9 a server by position, local first).
var KeyCommands = append([]KeyCommand{
	{"b", "toggle"}, {"B", "hide"}, {"g", "focus"}, {"o", "jump"}, {"a", "next"}, {"A", "prev"}, {"u", "keep-awake"},
	{"N", "host next"}, {"P", "host prev"}, {"O", "host last"}, {"S", "host menu"},
}, serverDigits()...)

// LocalKeyCommands are the bindings the tmux snippet (`flok install --tmux`) makes in the local
// inner server, with their arguments; the sidebar binds the missing ones at start.
var LocalKeyCommands = append([]KeyCommand{
	{"a", "next --client '#{client_tty}'"}, {"A", "prev --client '#{client_tty}'"}, {"o", "jump --client '#{client_tty}'"},
	{"b", "toggle"}, {"B", "hide"}, {"g", "focus"}, {"u", "keep-awake --notify"}, {"?", "keys --open --client '#{client_tty}'"},
	{"N", "host next"}, {"P", "host prev"}, {"O", "host last"}, {"S", "host menu"},
}, serverDigits()...)

// serverDigits are F1 … F9: bring server N to the front (1 = local, then the hosts in registry
// order). Function keys are unbound in stock tmux (Alt-1 … Alt-5 select layouts there) and
// reach tmux from every terminal.
func serverDigits() []KeyCommand {
	var out []KeyCommand
	for i := 1; i <= 9; i++ {
		out = append(out, KeyCommand{"F" + string(rune('0'+i)), "host front " + string(rune('0'+i))})
	}
	return out
}

func keyNames(cmds []KeyCommand) []string {
	out := make([]string, 0, len(cmds))
	for _, k := range cmds {
		out = append(out, k.Key)
	}
	return out
}

// IsKeyCommand says whether cmd is one a remote key may ask for.
func IsKeyCommand(cmd string) bool {
	for _, k := range KeyCommands {
		if k.Cmd == cmd {
			return true
		}
	}
	return false
}

// RequestOption is the tmux user option a plain-mode binding sets; the local poller reads it
// with its next snapshot and clears it. Full-mode bindings go through `flok relay` instead.
const RequestOption = tmux.RequestOption

// BindRelayArgs binds every key to `<flok> relay <cmd>` (full mode: the host's serve forwards
// the request at once), as one tmux invocation.
func BindRelayArgs(flok string) []string {
	var args []string
	for i, k := range KeyCommands {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, "bind-key", "-T", "prefix", k.Key, "run-shell", "-b", tmux.ShellQuote(flok)+" relay "+k.Cmd)
	}
	return args
}

// BindOptionArgs binds every key to setting RequestOption (plain mode: no flok on the host).
func BindOptionArgs() []string {
	var args []string
	for i, k := range KeyCommands {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, "bind-key", "-T", "prefix", k.Key, "set-option", "-g", RequestOption, k.Cmd)
	}
	return args
}

// UnbindArgs removes the given keys from the prefix table, as one tmux invocation.
func UnbindArgs(keys []string) []string {
	var args []string
	for i, k := range keys {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, "unbind-key", "-T", "prefix", k)
	}
	return args
}

// recordOption names the tmux user option in which flok notes what a server had on one of
// flok's keys before flok took it: the key alone (nothing was bound) or the key followed by
// the bind-key line list-keys printed. The note lives in the server next to the binding, so
// whatever ends a session (a killed serve, a mode flip, a crash between unbind and rebind) the
// next flok on that server still knows what to give back, and never mistakes a leftover of its
// own for the host's binding.
func recordOption(key string) string { return "@flok-orig-" + key }

// serverKeys is what one round trip tells about flok's keys on a server: the current binding
// of each key and the record an earlier flok session left there, if any.
type serverKeys struct {
	current map[string]prefixBinding
	record  map[string]string // key -> "" (was unbound) or the bind-key line; present = flok holds the key
}

// readServerKeys lists the prefix table and flok's records in one tmux invocation.
func readServerKeys(c tmux.Client, keys []string) serverKeys {
	args := []string{"list-keys", "-T", "prefix"}
	for _, k := range keys {
		args = append(args, ";", "show-options", "-gqv", recordOption(k))
	}
	sk := serverKeys{current: map[string]prefixBinding{}, record: map[string]string{}}
	out, err := c.Run(args...)
	if err != nil {
		return sk
	}
	var bindLines []string
	for _, line := range strings.Split(tmux.Decode(out, tmux.Escapes(c)), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
		case strings.HasPrefix(t, "bind-key"):
			bindLines = append(bindLines, line)
		default: // a record: "<key>" or "<key> bind-key …"
			key, rest, _ := strings.Cut(t, " ")
			sk.record[key] = strings.TrimSpace(rest)
		}
	}
	for _, b := range parsePrefixBindings(strings.Join(bindLines, "\n")) {
		sk.current[b.key] = b
	}
	return sk
}

// original is what the server should get back on key: the record when flok already holds the
// key, else what is bound now, unless that is a binding of flok's own remote kind.
func (sk serverKeys) original(key string) string {
	if rec, held := sk.record[key]; held {
		return rec
	}
	if b, ok := sk.current[key]; ok && !b.ours {
		return b.line
	}
	return ""
}

// prefixBinding is one line of `list-keys -T prefix`.
type prefixBinding struct {
	key, cmd, line string
	ours           bool // flok's remote binding (relay or request option), this or an earlier session
	flok           bool // ours, or any other command running flok (the local snippet)
}

func prefixBindings(c tmux.Client) []prefixBinding {
	out, err := c.Run("list-keys", "-T", "prefix")
	if err != nil {
		return nil
	}
	return parsePrefixBindings(tmux.Decode(out, tmux.Escapes(c)))
}

func parsePrefixBindings(out string) []prefixBinding {
	var list []prefixBinding
	for _, line := range strings.Split(out, "\n") {
		w := SplitTmuxWords(line)
		for i := 0; i+2 < len(w); i++ {
			if w[i] == "-T" && w[i+1] == "prefix" {
				cmd := strings.Join(w[i+3:], " ")
				ours := strings.Contains(line, "set-option -g "+RequestOption) || strings.Contains(cmd, "flok relay ") || strings.Contains(cmd, "flok' relay ")
				list = append(list, prefixBinding{key: w[i+2], cmd: cmd, line: strings.TrimSpace(line), ours: ours, flok: ours || flokCommand(cmd)})
				break
			}
		}
	}
	return list
}

// flokCommand says whether a binding's command runs flok (any path) with one of its key
// commands.
func flokCommand(cmd string) bool {
	if !strings.Contains(cmd, "flok") {
		return false
	}
	for _, k := range LocalKeyCommands {
		if strings.Contains(cmd, "flok "+strings.Fields(k.Cmd)[0]) || strings.Contains(cmd, "flok' "+strings.Fields(k.Cmd)[0]) {
			return true
		}
	}
	return false
}

// bindArgs turns saved bind-key lines back into one batch of commands (a `;` word inside a
// line is escaped so tmux keeps it in that binding).
func bindArgs(lines []string) []string {
	var args []string
	for _, line := range lines {
		w := SplitTmuxWords(line)
		if len(w) < 2 || w[0] != "bind-key" {
			continue
		}
		if len(args) > 0 {
			args = append(args, ";")
		}
		for _, word := range w {
			if strings.HasSuffix(word, ";") {
				word = word[:len(word)-1] + "\\;"
			}
			args = append(args, word)
		}
	}
	return args
}

// LocalKeysFile records what the sidebar bound in the local inner server, so `flok down` (or
// the next sidebar) can undo exactly that.
const LocalKeysFile = "keys.json"

// LocalKeys is the content of LocalKeysFile.
type LocalKeys struct {
	Installed []string `json:"installed"`       // keys flok bound
	Saved     []string `json:"saved,omitempty"` // the server's own bindings flok replaced (mode all)
}

// bindLocalArgs binds keys to `<flok> <cmd>` in the local server.
func bindLocalArgs(flok string, cmds []KeyCommand) []string {
	var args []string
	for i, k := range cmds {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, "bind-key", "-T", "prefix", k.Key, "run-shell", "-b", tmux.ShellQuote(flok)+" "+k.Cmd)
	}
	return args
}

// InstallLocalKeys binds flok's keys in the local inner server: mode "missing" binds only keys
// that are not bound at all (tmux's own `o` and `?` stay), "all" binds every key and saves what
// was there, "off" does nothing. Keys already running flok (the snippet) are left as they are.
// What was done is recorded in <dir>/keys.json; conflicts lists the keys left alone in mode
// missing with their current command.
func InstallLocalKeys(c tmux.Client, dir, flok, mode string) (installed []string, conflicts map[string]string) {
	conflicts = map[string]string{}
	if mode == "off" || flok == "" {
		return nil, conflicts
	}
	current := map[string]prefixBinding{}
	for _, b := range prefixBindings(c) {
		current[b.key] = b
	}
	st := loadLocalKeys(dir)
	have := map[string]bool{}
	for _, k := range st.Installed {
		have[k] = true
	}
	var todo []KeyCommand
	for _, k := range LocalKeyCommands {
		b, bound := current[k.Key]
		switch {
		case bound && b.flok:
			continue // the snippet, or an earlier sidebar
		case bound && mode != "all":
			conflicts[k.Key] = b.cmd
			continue
		case bound: // mode all: remember what it was, once
			if !have[k.Key] {
				st.Saved = append(st.Saved, b.line)
			}
		}
		todo = append(todo, k)
		if !have[k.Key] {
			st.Installed = append(st.Installed, k.Key)
			have[k.Key] = true
		}
		installed = append(installed, k.Key)
	}
	if len(todo) > 0 {
		_, _ = c.Run(bindLocalArgs(flok, todo)...)
	}
	if len(st.Installed) > 0 {
		_ = state.WriteJSONAtomic(filepath.Join(dir, LocalKeysFile), st)
	}
	return installed, conflicts
}

func loadLocalKeys(dir string) LocalKeys {
	var st LocalKeys
	if data, err := os.ReadFile(filepath.Join(dir, LocalKeysFile)); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

// RestoreLocalKeys undoes InstallLocalKeys: flok's keys go, the server's own come back, the
// record is removed. Safe to call twice.
func RestoreLocalKeys(c tmux.Client, dir string) error {
	st := loadLocalKeys(dir)
	if len(st.Installed) == 0 {
		return nil
	}
	sort.Strings(st.Installed)
	if _, err := c.Run(UnbindArgs(st.Installed)...); err != nil && !errors.Is(err, os.ErrNotExist) {
		if !strings.Contains(err.Error(), "no server running") {
			return err
		}
	}
	if args := bindArgs(st.Saved); len(args) > 0 {
		_, _ = c.Run(args...)
	}
	return os.Remove(filepath.Join(dir, LocalKeysFile))
}

// LocalKeyStatus reports, for every key of the snippet, what the local server has: "flok",
// "" (unbound) or the other command bound there.
func LocalKeyStatus(c tmux.Client) map[string]string {
	status := map[string]string{}
	current := map[string]prefixBinding{}
	for _, b := range prefixBindings(c) {
		current[b.key] = b
	}
	for _, k := range LocalKeyCommands {
		if b, ok := current[k.Key]; ok {
			if b.flok {
				status[k.Key] = "flok"
			} else {
				status[k.Key] = b.cmd
			}
		} else {
			status[k.Key] = ""
		}
	}
	return status
}

// Keys are flok's bindings installed on one tmux server. The host's own bindings of those
// keys are recorded in the server (recordOption) in the same invocation that binds flok's, and
// an install reads the records first: a key an earlier session still holds keeps its recorded
// original instead of adopting flok's leftover, so the restore puts the host's bindings back
// whatever happened in between.
type Keys struct {
	mu     sync.Mutex
	c      tmux.Client
	args   []string
	saved  []string
	closed bool
}

// InstallKeys records the host's bindings of flok's keys and binds flok's.
func InstallKeys(c tmux.Client, args []string) *Keys {
	k := &Keys{c: c, args: args}
	k.install()
	return k
}

func (k *Keys) install() {
	keys := keyNames(KeyCommands)
	sk := readServerKeys(k.c, keys)
	k.saved = nil
	var args []string
	for _, key := range keys {
		val := key
		if orig := sk.original(key); orig != "" {
			k.saved = append(k.saved, orig)
			val += " " + orig
		}
		args = append(args, "set-option", "-g", recordOption(key), val, ";")
	}
	_, _ = k.c.Run(append(args, k.args...)...)
}

// Rebind installs flok's keys again on a restarted server (fresh bindings, fresh records).
func (k *Keys) Rebind() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.closed {
		k.install()
	}
}

// Restore removes flok's keys, puts the host's own back and drops the records, in one tmux
// invocation; later Rebinds are ignored.
func (k *Keys) Restore() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.closed = true
	keys := keyNames(KeyCommands)
	args := UnbindArgs(keys)
	if b := bindArgs(k.saved); len(b) > 0 {
		args = append(append(args, ";"), b...)
	}
	for _, key := range keys {
		args = append(args, ";", "set-option", "-gqu", recordOption(key))
	}
	_, _ = k.c.Run(args...)
}

// SplitTmuxWords splits a tmux command line the way tmux's own parser does for the simple
// cases list-keys prints: whitespace separates words, double and single quotes group them, a
// backslash escapes the next character inside double quotes and outside quotes.
func SplitTmuxWords(line string) []string {
	var words []string
	var cur strings.Builder
	in := false
	quote := byte(0)
	flush := func() {
		if in {
			words = append(words, cur.String())
			cur.Reset()
			in = false
		}
	}
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case quote == '\'':
			if ch == '\'' {
				quote = 0
			} else {
				cur.WriteByte(ch)
			}
		case quote == '"':
			switch {
			case ch == '"':
				quote = 0
			case ch == '\\' && i+1 < len(line):
				i++
				cur.WriteByte(line[i])
			default:
				cur.WriteByte(ch)
			}
		case ch == '"' || ch == '\'':
			quote, in = ch, true
		case ch == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
			in = true
		case ch == ' ' || ch == '\t':
			flush()
		default:
			cur.WriteByte(ch)
			in = true
		}
	}
	flush()
	return words
}
