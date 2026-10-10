// Package answer is what the phone may type into an agent pane: a short line of text and a few
// named keys, checked against an allowlist before tmux sees them. The sidebar (local panes),
// `flok serve` (full-mode hosts) and the plain-mode connection all send the same thing, through
// one send-keys invocation; nothing else ever reaches a pane from outside.
package answer

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Answer is one line typed into an agent pane: Text first, literally, then Keys in order.
type Answer struct {
	Pane string   `json:"pane"`           // pane ref: %12 here, beta:%12 on a remote host
	Text string   `json:"text,omitempty"` // typed as is (send-keys -l): printable, at most MaxText runes
	Keys []string `json:"keys,omitempty"` // named keys after the text: Enter, Escape, Up, … (see Keys)
}

const (
	MaxText = 200 // runes of Text
	MaxKeys = 8   // named keys per answer
)

// Keys are the named keys an answer may send, as tmux spells them. Letters and digits travel
// as Text; C-c interrupts the agent, which is the one control key the phone needs.
var Keys = []string{"Enter", "Escape", "Tab", "BTab", "Up", "Down", "Left", "Right", "Space", "BSpace",
	"Home", "End", "PageUp", "PageDown", "C-c"}

var keyByLower = func() map[string]string {
	m := map[string]string{}
	for _, k := range Keys {
		m[strings.ToLower(k)] = k
	}
	m["esc"], m["return"], m["backspace"], m["ctrl-c"] = "Escape", "Enter", "BSpace", "C-c"
	return m
}()

// Empty reports an answer that types nothing.
func (a Answer) Empty() bool { return a.Text == "" && len(a.Keys) == 0 }

// Normalize checks an answer and spells its keys the way tmux does; it returns the answer that
// may be sent, or why it may not.
func Normalize(a Answer) (Answer, error) {
	if a.Empty() {
		return a, errors.New("nothing to type")
	}
	if n := len([]rune(a.Text)); n > MaxText {
		return a, fmt.Errorf("text is %d characters, at most %d", n, MaxText)
	}
	for _, r := range a.Text {
		if !unicode.IsPrint(r) && r != '\t' {
			return a, fmt.Errorf("text contains a control character (%U)", r)
		}
	}
	if len(a.Keys) > MaxKeys {
		return a, fmt.Errorf("%d keys, at most %d", len(a.Keys), MaxKeys)
	}
	out := Answer{Pane: a.Pane, Text: a.Text}
	for _, k := range a.Keys {
		name, ok := keyByLower[strings.ToLower(strings.TrimSpace(k))]
		if !ok {
			return a, fmt.Errorf("key %q is not allowed (one of %s)", k, strings.Join(Keys, " "))
		}
		out.Keys = append(out.Keys, name)
	}
	return out, nil
}

// Args is the tmux command for a normalized answer against a pane id: the text literally,
// then the keys, one invocation. A tmux argument that ends in ";" would end the command, so a
// trailing semicolon is escaped the way tmux reads it (\;).
func Args(pane string, a Answer) []string {
	var args []string
	if a.Text != "" {
		text := a.Text
		if strings.HasSuffix(text, ";") {
			text = text[:len(text)-1] + `\;`
		}
		args = append(args, "send-keys", "-t", pane, "-l", text)
	}
	if len(a.Keys) > 0 {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(append(args, "send-keys", "-t", pane), a.Keys...)
	}
	return args
}

// Describe is the answer for a log line: the keys and how much text, never the text itself.
func Describe(a Answer) string {
	parts := []string{}
	if a.Text != "" {
		parts = append(parts, fmt.Sprintf("%d chars", len([]rune(a.Text))))
	}
	if len(a.Keys) > 0 {
		parts = append(parts, strings.Join(a.Keys, " "))
	}
	return strings.Join(parts, " + ")
}
