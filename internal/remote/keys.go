package remote

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

// KeyCommand is one flok action bound in a host's prefix table while the host is connected:
// the same keys as the local tmux snippet, `?` excepted (tmux's own list-keys stays there).
type KeyCommand struct{ Key, Cmd string }

// KeyCommands in binding order: the agent keys, the three menus (A agents, S sessions, H
// servers), then the server keys (N/P next and previous server, O the last one, F1…F9 a
// server by position, local first).
var KeyCommands = append([]KeyCommand{
	{"b", "toggle"}, {"B", "hide"}, {"g", "focus"}, {"o", "jump"}, {"a", "next"}, {"u", "keep-awake"},
	{"A", "menu agents"}, {"S", "menu sessions"}, {"H", "menu servers"},
	{"N", "host next"}, {"P", "host prev"}, {"O", "host last"},
}, serverDigits()...)

// legacyKeyCommands are what bindings from before the menus may still relay.
var legacyKeyCommands = []string{"prev", "host menu"}

// LocalKeyCommands are the bindings the tmux snippet (`flok install --tmux`) makes in the local
// inner server, with their arguments; the sidebar binds the missing ones at start.
var LocalKeyCommands = append([]KeyCommand{
	{"a", "next --client '#{client_tty}'"}, {"o", "jump --client '#{client_tty}'"},
	{"b", "toggle"}, {"B", "hide"}, {"g", "focus"}, {"u", "keep-awake --notify"}, {"?", "keys --open --client '#{client_tty}'"},
	{"A", "menu agents"}, {"S", "menu sessions"}, {"H", "menu servers"},
	{"N", "host next"}, {"P", "host prev"}, {"O", "host last"},
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
	for _, c := range legacyKeyCommands {
		if c == cmd {
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

// recordOption names the tmux user option in which flok notes what a server had before flok
// changed one of its keys or its prefix: "<key>" alone (nothing was bound) or "<key> bind-key …"
// (the line list-keys printed) for a key, "prefix <key>" and "prefix2 <key>" for the prefix
// options, and under "keys" the list of keys a session holds. The notes live in the server
// next to the bindings, so whatever ends a session (a killed serve, a mode flip, a crash
// between unbind and rebind) the next flok on that server still knows what to give back, and
// never mistakes a leftover of its own for the host's binding.
func recordOption(name string) string { return "@flok-orig-" + name }

const heldRecord = "keys" // recordOption("keys") = "keys b B g … C-a C-b"

// serverKeys is what a round trip tells about flok's keys on a server: its prefix options,
// the current binding of each key and the records an earlier flok session left, if any.
type serverKeys struct {
	prefix, prefix2 string
	current         map[string]prefixBinding
	record          map[string]string // name -> the rest of the note; present = a session holds it
}

// readServerKeys reads the prefix options, the prefix table and the records of the given keys
// in one tmux invocation (a second one fetches the records of keys an earlier session held
// beyond those: its prefix chords).
func readServerKeys(c tmux.Client, keys []string) serverKeys {
	sk := serverKeys{current: map[string]prefixBinding{}, record: map[string]string{}}
	args := []string{"show-options", "-gv", "prefix", ";", "show-options", "-gv", "prefix2", ";", "list-keys", "-T", "prefix"}
	for _, n := range append([]string{heldRecord, "prefix", "prefix2"}, keys...) {
		args = append(args, ";", "show-options", "-gqv", recordOption(n))
	}
	out, err := c.Run(args...)
	if err != nil {
		return sk
	}
	lines := strings.Split(tmux.Decode(out, tmux.Escapes(c)), "\n")
	if len(lines) >= 2 {
		sk.prefix, sk.prefix2 = strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
		lines = lines[2:]
	}
	sk.parse(lines)
	var more [][]string
	for _, k := range strings.Fields(sk.record[heldRecord]) {
		if _, ok := sk.record[k]; !ok && !slices.Contains(keys, k) {
			more = append(more, []string{"show-options", "-gqv", recordOption(k)})
		}
	}
	if len(more) > 0 {
		if out, err := c.Run(joinCmds(more)...); err == nil {
			sk.parse(strings.Split(tmux.Decode(out, tmux.Escapes(c)), "\n"))
		}
	}
	return sk
}

// parse sorts output lines into bindings (list-keys lines) and records ("<name> <rest>").
func (sk *serverKeys) parse(lines []string) {
	var bindLines []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
		case strings.HasPrefix(t, "bind-key"):
			bindLines = append(bindLines, line)
		default:
			name, rest, _ := strings.Cut(t, " ")
			sk.record[name] = strings.TrimSpace(rest)
		}
	}
	for _, b := range parsePrefixBindings(strings.Join(bindLines, "\n")) {
		sk.current[b.key] = b
	}
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

// joinCmds flattens tmux commands into one invocation's arguments.
func joinCmds(cmds [][]string) []string {
	var args []string
	for i, c := range cmds {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, c...)
	}
	return args
}

// prefixBinding is one line of `list-keys -T prefix`.
type prefixBinding struct {
	key, cmd, line string
	ours           bool   // flok's remote binding (relay or request option), this or an earlier session
	flok           bool   // ours, or any other command running flok (the local snippet)
	sub            string // the flok command a local binding runs ("prev", "menu agents"), "" when not flok's
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
				sub := flokSub(cmd)
				list = append(list, prefixBinding{key: w[i+2], cmd: cmd, line: strings.TrimSpace(line), ours: ours, flok: ours || sub != "", sub: sub})
				break
			}
		}
	}
	return list
}

// flokSub names the flok key command a binding runs (any path to flok, the snippet's bare
// `flok` included): "next", "menu agents", "host front 1", or one of the commands older
// snippets bound ("prev", "host menu"). "" when the binding is not flok's.
func flokSub(cmd string) string {
	if !strings.Contains(cmd, "flok") {
		return ""
	}
	subs := legacyKeyCommands
	for _, k := range LocalKeyCommands {
		subs = append(subs, cmdWords(k.Cmd))
	}
	best := ""
	for _, sub := range subs {
		if len(sub) <= len(best) {
			continue
		}
		for _, lead := range []string{"flok ", "flok' "} {
			if i := strings.Index(cmd, lead+sub); i >= 0 {
				if rest := cmd[i+len(lead)+len(sub):]; rest == "" || rest[0] == ' ' || rest[0] == '"' || rest[0] == ';' {
					best = sub
				}
			}
		}
	}
	return best
}

// cmdWords is a key command without its flags: "next --client '#{client_tty}'" → "next".
func cmdWords(cmd string) string {
	var words []string
	for _, w := range strings.Fields(cmd) {
		if strings.HasPrefix(w, "-") {
			break
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
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
// was there, "off" does nothing. Keys already running flok's command for that key (the snippet)
// are left as they are; a key running another flok command (an older snippet's `A` = prev or
// `S` = the servers menu, an earlier sidebar) is brought up to date in both modes, the old
// binding saved so the restore puts it back. What was done is recorded in <dir>/keys.json;
// conflicts lists the keys left alone in mode missing with their current command.
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
		case bound && b.flok && b.sub == cmdWords(k.Cmd):
			continue // the snippet, or an earlier sidebar
		case bound && b.flok: // flok's own, from before this key changed: update it, remember what it was
			if !have[k.Key] {
				st.Saved = append(st.Saved, b.line)
			}
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
// "flok <cmd>" for an older snippet's flok command on that key, "" (unbound) or the other
// command bound there.
func LocalKeyStatus(c tmux.Client) map[string]string {
	status := map[string]string{}
	current := map[string]prefixBinding{}
	for _, b := range prefixBindings(c) {
		current[b.key] = b
	}
	for _, k := range LocalKeyCommands {
		if b, ok := current[k.Key]; ok {
			if b.flok && b.sub == cmdWords(k.Cmd) {
				status[k.Key] = "flok"
			} else if b.flok {
				status[k.Key] = "flok " + b.sub
			} else {
				status[k.Key] = b.cmd
			}
		} else {
			status[k.Key] = ""
		}
	}
	return status
}

// Keys are flok's bindings installed on one tmux server, with the local prefix mirrored there
// when asked: the host's tmux takes it as `prefix`, keeps its own as `prefix2` (when that was
// None) and gets `<local> send-prefix` and `<own> send-prefix -2` bound, so the chords typed at
// home work inside its sessions and its native ones still do. The host's own bindings and
// prefix options are recorded in the server (recordOption) in the same invocation that changes
// them, and an install reads the records first: a key an earlier session still holds keeps its
// recorded original instead of adopting flok's leftover, so the restore puts the host's
// bindings back whatever happened in between.
type Keys struct {
	mu     sync.Mutex
	c      tmux.Client
	args   []string
	prefix string   // the local prefix to mirror; "" = leave the host's alone
	bound  []string // every key this session holds: flok's, then the prefix chords
	saved  []string // the host's own bindings of those, as list-keys printed them
	orig   prefixState
	closed bool
}

// prefixState is what the prefix mirror changed on the server and what it was.
type prefixState struct {
	mirrored   bool   // prefix set to the local one
	prefix     string // the host's own prefix
	setPrefix2 bool   // prefix2 set to the host's own prefix (it was None)
	prefix2    string // what prefix2 was
}

// InstallKeys records the host's bindings of flok's keys and binds flok's; prefix, when not
// empty, is the local prefix the host's tmux takes while the keys are installed.
func InstallKeys(c tmux.Client, args []string, prefix string) *Keys {
	k := &Keys{c: c, args: args, prefix: prefix}
	k.install()
	return k
}

func (k *Keys) install() {
	base := keyNames(KeyCommands)
	want := base
	if k.prefix != "" {
		want = append(slices.Clone(base), k.prefix)
	}
	sk := readServerKeys(k.c, want)
	own, own2 := sk.prefix, sk.prefix2
	if v, ok := sk.record["prefix"]; ok { // taken by a session that did not give it back
		own = v
	}
	if v, ok := sk.record["prefix2"]; ok {
		own2 = v
	}
	var ps prefixState
	var chords []KeyCommand
	if k.prefix != "" && k.prefix != "None" && own != "" && own != k.prefix {
		ps = prefixState{mirrored: true, prefix: own, prefix2: own2}
		chords = append(chords, KeyCommand{k.prefix, "send-prefix"})
		if own2 == "None" {
			ps.setPrefix2 = true
			chords = append(chords, KeyCommand{own, "send-prefix -2"})
		}
	}
	bound := slices.Clone(base)
	for _, ch := range chords {
		bound = append(bound, ch.Key)
	}
	var saved []string
	var cmds [][]string
	for _, key := range bound {
		val := key
		if orig := sk.original(key); orig != "" {
			saved = append(saved, orig)
			val += " " + orig
		}
		cmds = append(cmds, []string{"set-option", "-g", recordOption(key), val})
	}
	if ps.mirrored {
		cmds = append(cmds, []string{"set-option", "-g", recordOption("prefix"), "prefix " + ps.prefix}, []string{"set-option", "-g", "prefix", k.prefix})
		if ps.setPrefix2 {
			cmds = append(cmds, []string{"set-option", "-g", recordOption("prefix2"), "prefix2 " + ps.prefix2}, []string{"set-option", "-g", "prefix2", ps.prefix})
		}
	}
	cmds = append(cmds, []string{"set-option", "-g", recordOption(heldRecord), heldRecord + " " + strings.Join(bound, " ")}, k.args)
	for _, ch := range chords {
		cmds = append(cmds, append([]string{"bind-key", "-T", "prefix", ch.Key}, strings.Fields(ch.Cmd)...))
	}
	// what an earlier session held beyond this one's set goes back now: its prefix chords, or
	// a prefix it took when this one does not
	var stale, staleSaved []string
	for _, key := range strings.Fields(sk.record[heldRecord]) {
		if !slices.Contains(bound, key) {
			stale = append(stale, key)
			if orig := sk.original(key); orig != "" {
				staleSaved = append(staleSaved, orig)
			}
		}
	}
	_, heldPrefix := sk.record["prefix"]
	_, heldPrefix2 := sk.record["prefix2"]
	if left := (prefixState{mirrored: heldPrefix && !ps.mirrored, prefix: own, setPrefix2: heldPrefix2 && !ps.mirrored, prefix2: own2}); len(stale) > 0 || left.mirrored {
		cmds = append(cmds, restoreCmds(stale, staleSaved, left, false)...)
	}
	_, _ = k.c.Run(joinCmds(cmds)...)
	k.bound, k.saved, k.orig = bound, saved, ps
}

// restoreCmds undoes what a session did: its keys go, the host's own come back, the prefix
// options too, and the records are dropped (the held list with them when dropHeld).
func restoreCmds(keys, saved []string, ps prefixState, dropHeld bool) [][]string {
	var cmds [][]string
	for _, key := range keys {
		cmds = append(cmds, []string{"unbind-key", "-T", "prefix", key})
	}
	for _, line := range saved {
		if b := bindArgs([]string{line}); len(b) > 0 {
			cmds = append(cmds, b)
		}
	}
	if ps.mirrored {
		cmds = append(cmds, []string{"set-option", "-g", "prefix", ps.prefix})
	}
	if ps.setPrefix2 {
		cmds = append(cmds, []string{"set-option", "-g", "prefix2", ps.prefix2})
	}
	for _, key := range keys {
		cmds = append(cmds, []string{"set-option", "-gqu", recordOption(key)})
	}
	if ps.mirrored {
		cmds = append(cmds, []string{"set-option", "-gqu", recordOption("prefix")})
	}
	if ps.setPrefix2 {
		cmds = append(cmds, []string{"set-option", "-gqu", recordOption("prefix2")})
	}
	if dropHeld {
		cmds = append(cmds, []string{"set-option", "-gqu", recordOption(heldRecord)})
	}
	return cmds
}

// SetPrefix mirrors another local prefix (full mode learns it after the hello); "" gives the
// host its own back. A no-op when nothing changes or after Restore.
func (k *Keys) SetPrefix(prefix string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed || k.prefix == prefix {
		return
	}
	k.prefix = prefix
	k.install()
}

// Rebind installs flok's keys again on a restarted server (fresh bindings, fresh records).
func (k *Keys) Rebind() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.closed {
		k.install()
	}
}

// Restore removes flok's keys, puts the host's own bindings and prefix back and drops the
// records, in one tmux invocation; later Rebinds are ignored.
func (k *Keys) Restore() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.closed = true
	_, _ = k.c.Run(joinCmds(restoreCmds(k.bound, k.saved, k.orig, true))...)
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
