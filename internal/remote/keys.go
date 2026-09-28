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

// SavedBindings returns a server's own bindings of the given keys (as list-keys prints them),
// to be put back when flok lets go of them.
func SavedBindings(c tmux.Client, keys []string) []string {
	var lines []string
	for _, b := range prefixBindings(c) {
		if b.flok {
			continue
		}
		for _, k := range keys {
			if b.key == k {
				lines = append(lines, b.line)
			}
		}
	}
	return lines
}

// prefixBinding is one line of `list-keys -T prefix`.
type prefixBinding struct {
	key, cmd, line string
	flok           bool // one of flok's own (this or an earlier session)
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
				flok := strings.Contains(line, RequestOption) || strings.Contains(line, " relay ") || flokCommand(cmd)
				list = append(list, prefixBinding{key: w[i+2], cmd: cmd, line: strings.TrimSpace(line), flok: flok})
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

// ourBindings keeps the compatibility of the older helper: the lines of the remote key set.
func ourBindings(out string) []string {
	var lines []string
	for _, b := range parsePrefixBindings(out) {
		if b.flok {
			continue
		}
		for _, k := range KeyCommands {
			if b.key == k.Key {
				lines = append(lines, b.line)
			}
		}
	}
	return lines
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
	for _, line := range st.Saved {
		if w := SplitTmuxWords(line); len(w) > 1 && w[0] == "bind-key" {
			_, _ = c.Run(w...)
		}
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

// Keys are flok's bindings installed on one tmux server, with the host's own bindings of
// those keys saved once, at install time, so a restore puts them back whatever happened in
// between (a Rebind after the server restarted must not adopt flok's own bindings as saved).
type Keys struct {
	mu     sync.Mutex
	c      tmux.Client
	args   []string
	saved  []string
	closed bool
}

// InstallKeys saves the host's bindings of flok's keys and binds flok's.
func InstallKeys(c tmux.Client, args []string) *Keys {
	k := &Keys{c: c, args: args, saved: SavedBindings(c, keyNames(KeyCommands))}
	_, _ = c.Run(args...)
	return k
}

// Rebind binds flok's keys again (the server was restarted and lost them).
func (k *Keys) Rebind() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.closed {
		_, _ = k.c.Run(k.args...)
	}
}

// Restore removes flok's keys and puts the host's own back; later Rebinds are ignored.
func (k *Keys) Restore() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.closed = true
	_, _ = k.c.Run(UnbindArgs(keyNames(KeyCommands))...)
	for _, line := range k.saved {
		if w := SplitTmuxWords(line); len(w) > 1 && w[0] == "bind-key" {
			_, _ = k.c.Run(w...)
		}
	}
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
