// Package keys reads the live key bindings of a tmux server and turns them into labelled,
// sectioned entries for the keybinds help.
package keys

import (
	"sort"
	"strings"

	"github.com/w4jnl/flok/internal/tmux"
)

type Binding struct {
	Table   string
	Key     string
	Command string // raw command text as tmux prints it
	Note    string // tmux's own note (stock bindings), "" for most user bindings
	Repeat  bool
	Section string
	Label   string
}

// Collect lists the bindings of the given key tables with their notes.
func Collect(c tmux.Client, tables []string) ([]Binding, string, error) {
	prefix, _ := c.Run("show-options", "-gv", "prefix")
	prefix = strings.TrimSpace(prefix)
	var all []Binding
	for _, table := range tables {
		out, err := c.Run("list-keys", "-T", table)
		if err != nil {
			continue
		}
		bindings := ParseListKeys(out)
		notesOut, _ := c.Run("list-keys", "-N", "-T", table)
		notes := ParseNotes(notesOut, prefix, table == "prefix")
		for i := range bindings {
			bindings[i].Note = notes[bindings[i].Key]
		}
		all = append(all, bindings...)
	}
	if len(all) == 0 {
		return nil, prefix, errorf("no key bindings found")
	}
	return all, prefix, nil
}

type errString string

func (e errString) Error() string { return string(e) }
func errorf(s string) error       { return errString(s) }

// ParseListKeys parses `tmux list-keys -T <table>` output:
//
//	bind-key [-r] -T <table> <key> <command...>
func ParseListKeys(out string) []Binding {
	var res []Binding
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "bind-key") {
			continue
		}
		pos := len("bind-key")
		var b Binding
		next := func() (string, bool) {
			for pos < len(line) && (line[pos] == ' ' || line[pos] == '\t') {
				pos++
			}
			if pos >= len(line) {
				return "", false
			}
			start := pos
			for pos < len(line) && line[pos] != ' ' && line[pos] != '\t' {
				pos++
			}
			return line[start:pos], true
		}
		ok := true
		for ok {
			var tok string
			tok, ok = next()
			if !ok {
				break
			}
			switch tok {
			case "-r":
				b.Repeat = true
			case "-T":
				b.Table, ok = next()
			case "-N":
				// notes are not printed by a plain list-keys, but be safe: skip one (possibly quoted) token
				var n string
				n, ok = next()
				if strings.HasPrefix(n, "\"") && !strings.HasSuffix(n, "\"") {
					for ok && !strings.HasSuffix(n, "\"") {
						n, ok = next()
					}
				}
			default:
				b.Key = unescapeKey(tok)
				b.Command = strings.TrimSpace(line[pos:])
				ok = false
			}
		}
		if b.Key != "" {
			res = append(res, b)
		}
	}
	return res
}

// ParseNotes parses `tmux list-keys -N -T <table>` output ("<key>  <note>", with the prefix
// key prepended for the prefix table) into key -> note.
func ParseNotes(out, prefix string, stripPrefix bool) map[string]string {
	notes := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if stripPrefix && prefix != "" && strings.HasPrefix(line, prefix+" ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, prefix+" "))
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		notes[f[0]] = strings.TrimSpace(strings.TrimPrefix(line, f[0]))
	}
	return notes
}

// unescapeKey undoes tmux's output quoting of special keys (\" \# \$ \% \' \; \{ \} \~).
func unescapeKey(k string) string {
	if len(k) == 2 && k[0] == '\\' {
		return k[1:]
	}
	return k
}

var mousePrefixes = []string{"Mouse", "Wheel", "DoubleClick", "TripleClick", "SecondClick"}

// IsMouse reports mouse keys, including modifier forms such as M-MouseDown3Pane.
func IsMouse(key string) bool {
	for len(key) > 2 && key[1] == '-' && strings.ContainsRune("MCS", rune(key[0])) {
		key = key[2:]
	}
	for _, p := range mousePrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// Section is an ordered group of bindings.
type Section struct {
	Name     string
	Bindings []Binding
}

// Organize labels and groups bindings: flok, prefix, no prefix, copy-mode-vi, plugins.
func Organize(bindings []Binding, prefix, bin string, overrides map[string]string, showMouse bool) []Section {
	order := []string{"flok", "prefix " + prefix, "no prefix", "copy-mode-vi"}
	groups := map[string][]Binding{}
	var plugins []string
	for _, b := range bindings {
		if !showMouse && IsMouse(b.Key) {
			continue
		}
		b.Label = Label(b, overrides)
		b.Section = SectionOf(b, prefix)
		if strings.HasPrefix(b.Section, "plugin:") {
			b.Section = strings.TrimPrefix(b.Section, "plugin:")
			if _, seen := groups[b.Section]; !seen {
				plugins = append(plugins, b.Section)
			}
		}
		groups[b.Section] = append(groups[b.Section], b)
	}
	sort.Strings(plugins)
	order = append(order, plugins...)
	var out []Section
	for _, name := range order {
		bs := groups[name]
		if len(bs) == 0 {
			continue
		}
		// user-defined bindings (no stock note) first, then stock ones; stable otherwise
		sort.SliceStable(bs, func(i, j int) bool {
			ui, uj := bs[i].Note == "", bs[j].Note == ""
			return ui && !uj
		})
		out = append(out, Section{Name: name, Bindings: bs})
	}
	return out
}
