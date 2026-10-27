// Package answer is what the phone may type into an agent pane: text, a few named keys and
// perhaps one file, checked against an allowlist before tmux sees them. The sidebar (local
// panes), `flok serve` (full-mode hosts) and the plain-mode connection all send the same thing;
// nothing else ever reaches a pane from outside. A short line is typed as keys (send-keys -l),
// anything longer or with newlines is pasted through a tmux buffer (paste-buffer -p: bracketed,
// so an agent takes it as one paste and not as lines sent one by one); a file is saved next to
// the instance's state and its path typed after the text, which is how Claude Code and Copilot
// take an image.
package answer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/w4jnl/flok/internal/agent"
)

// Answer is what is typed into an agent pane: Text first, literally, then Keys in order.
type Answer struct {
	Pane  string      `json:"pane"`            // pane ref: %12 here, beta:%12 on a remote host
	Text  string      `json:"text,omitempty"`  // typed as is: printable, tabs and newlines, at most MaxLongText runes (see Long)
	Keys  []string    `json:"keys,omitempty"`  // named keys after the text: Enter, Escape, Up, … (see Keys)
	Files []File      `json:"files,omitempty"` // saved on the agent's machine, their paths typed after the text, in order
	File  *File       `json:"file,omitempty"`  // one file, as the first app build sent it; Normalize folds it into Files
	Form  *FormAnswer `json:"form,omitempty"`  // fills the question form the agent shows (agent.Form), on its own: no text, keys or files with it
}

// FormAnswer answers a question form (agent.Form): one Choice per question, in the form's
// order. The instance drives the agent's own form with it (Fill), so the agent sees exactly
// what it would have seen typed at the keyboard.
type FormAnswer struct {
	Answers []Choice `json:"answers"`
}

// Choice answers one question: the options picked (indexes into the question's options, any
// number for a multi-select, one otherwise) and/or words of the user's own, typed into the
// form's "Type something" field (one line of at most MaxFormOther runes).
type Choice struct {
	Selected []int  `json:"selected,omitempty"`
	Other    string `json:"other,omitempty"`
}

// Empty reports a choice that picks nothing and says nothing.
func (c Choice) Empty() bool { return len(c.Selected) == 0 && c.Other == "" }

// File is an attachment: a screenshot, a photo, a PDF.
type File struct {
	Name string `json:"name"`           // screenshot.jpg: Normalize keeps letters, digits, . - _
	Mime string `json:"mime,omitempty"` // one of Mimes; "" is taken from the name's extension
	Data []byte `json:"data"`           // base64 on the wire, at most MaxFile bytes
}

const (
	// MaxText is the longest text typed as keys; a longer one, or one with a newline, is pasted
	// (Long). An instance before the long-text feature refuses anything over it.
	MaxText = 200
	// MaxLongText bounds a pasted text.
	MaxLongText = 8000
	MaxKeys     = 8       // named keys per answer
	MaxFile     = 4 << 20 // bytes of a file
	MaxFiles    = 6       // files per answer
	// MaxFormOther bounds the user's own words for one question of a form: typed into the
	// form's field as one line.
	MaxFormOther = 200
	// MaxBody bounds an answer on the wire (the files, base64, and the rest).
	MaxBody = 36 << 20
	// KeyGap separates the text from the keys that follow it. Written in one burst, an Enter
	// right behind the text reaches the agent inside what it takes for a paste, and Claude Code
	// then inserts a newline instead of sending the line; a pause makes it a keypress.
	KeyGap = 150 * time.Millisecond
	// PasteGap is the pause after a pasted text, which the agent takes a moment to swallow.
	PasteGap = 400 * time.Millisecond
	// Buffer is the tmux buffer a pasted text goes through (deleted by the paste).
	Buffer = "flok-answer"
	// KeepFiles is how long a saved file stays in the attach directory.
	KeepFiles = 24 * time.Hour
)

// Keys are the named keys an answer may send, as tmux spells them. Letters and digits travel
// as Text; C-c interrupts the agent, which is the one control key the phone needs.
var Keys = []string{"Enter", "Escape", "Tab", "BTab", "Up", "Down", "Left", "Right", "Space", "BSpace",
	"Home", "End", "PageUp", "PageDown", "C-c"}

// Mimes are the kinds of file an answer may carry, with the extension a saved one gets.
var Mimes = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/gif":       ".gif",
	"image/webp":      ".webp",
	"image/heic":      ".heic",
	"application/pdf": ".pdf",
}

var keyByLower = func() map[string]string {
	m := map[string]string{}
	for _, k := range Keys {
		m[strings.ToLower(k)] = k
	}
	m["esc"], m["return"], m["backspace"], m["ctrl-c"] = "Escape", "Enter", "BSpace", "C-c"
	return m
}()

// Empty reports an answer that types nothing.
func (a Answer) Empty() bool {
	return a.Text == "" && len(a.Keys) == 0 && len(a.Files) == 0 && a.File == nil && a.Form == nil
}

// Long reports a text that is pasted rather than typed: over MaxText runes, or more than one
// line. An instance without the long-text feature refuses it.
func Long(text string) bool {
	return strings.ContainsRune(text, '\n') || len([]rune(text)) > MaxText
}

// Normalize checks an answer and spells its keys the way tmux does; it returns the answer that
// may be sent, or why it may not. Line ends become \n.
func Normalize(a Answer) (Answer, error) {
	if a.Empty() {
		return a, errors.New("nothing to type")
	}
	if a.Form != nil {
		if a.Text != "" || len(a.Keys) > 0 || len(a.Files) > 0 || a.File != nil {
			return a, errors.New("a form answer carries nothing else: no text, keys or files")
		}
		f, err := normalizeForm(*a.Form)
		if err != nil {
			return a, err
		}
		return Answer{Pane: a.Pane, Form: &f}, nil
	}
	text := strings.ReplaceAll(strings.ReplaceAll(a.Text, "\r\n", "\n"), "\r", "\n")
	if n := len([]rune(text)); n > MaxLongText {
		return a, fmt.Errorf("text is %d characters, at most %d", n, MaxLongText)
	}
	for _, r := range text {
		if !unicode.IsPrint(r) && r != '\t' && r != '\n' {
			return a, fmt.Errorf("text contains a control character (%U)", r)
		}
	}
	if len(a.Keys) > MaxKeys {
		return a, fmt.Errorf("%d keys, at most %d", len(a.Keys), MaxKeys)
	}
	out := Answer{Pane: a.Pane, Text: text}
	for _, k := range a.Keys {
		name, ok := keyByLower[strings.ToLower(strings.TrimSpace(k))]
		if !ok {
			return a, fmt.Errorf("key %q is not allowed (one of %s)", k, strings.Join(Keys, " "))
		}
		out.Keys = append(out.Keys, name)
	}
	files := a.Files
	if a.File != nil {
		files = append(append([]File{}, files...), *a.File)
	}
	if len(files) > MaxFiles {
		return a, fmt.Errorf("%d files, at most %d", len(files), MaxFiles)
	}
	for _, f := range files {
		n, err := normalizeFile(f)
		if err != nil {
			return a, err
		}
		out.Files = append(out.Files, n)
	}
	return out, nil
}

// normalizeForm checks a form answer: a bounded number of choices, each picking options by
// index without repeats, with the user's own words one printable line.
func normalizeForm(f FormAnswer) (FormAnswer, error) {
	switch n := len(f.Answers); {
	case n == 0:
		return f, errors.New("the form answer answers no question")
	case n > agent.MaxFormQuestions:
		return f, fmt.Errorf("%d answers, a form has at most %d questions", n, agent.MaxFormQuestions)
	}
	out := FormAnswer{Answers: make([]Choice, 0, len(f.Answers))}
	for i, c := range f.Answers {
		var picked Choice
		seen := map[int]bool{}
		for _, j := range c.Selected {
			if j < 0 || j >= agent.MaxFormOptions {
				return f, fmt.Errorf("answer %d picks option %d, options are 0 to %d", i+1, j, agent.MaxFormOptions-1)
			}
			if !seen[j] {
				seen[j] = true
				picked.Selected = append(picked.Selected, j)
			}
		}
		sort.Ints(picked.Selected)
		other := strings.TrimSpace(c.Other)
		if n := len([]rune(other)); n > MaxFormOther {
			return f, fmt.Errorf("answer %d's text is %d characters, at most %d", i+1, n, MaxFormOther)
		}
		for _, r := range other {
			if !unicode.IsPrint(r) {
				return f, fmt.Errorf("answer %d's text contains a control character (%U)", i+1, r)
			}
		}
		picked.Other = other
		out.Answers = append(out.Answers, picked)
	}
	return out, nil
}

// normalizeFile checks a file and gives it a name safe for a path and a shell.
func normalizeFile(f File) (File, error) {
	switch n := len(f.Data); {
	case n == 0:
		return f, errors.New("the file is empty")
	case n > MaxFile:
		return f, fmt.Errorf("the file is %.1f MB, at most %d MB", float64(n)/1e6, MaxFile>>20)
	}
	ext := strings.ToLower(filepath.Ext(f.Name))
	if f.Mime == "" {
		for m, e := range Mimes {
			if e == ext || (ext == ".jpeg" && m == "image/jpeg") {
				f.Mime = m
			}
		}
	}
	want, ok := Mimes[strings.ToLower(f.Mime)]
	if !ok {
		return f, fmt.Errorf("files of type %q are not taken (images and PDF are)", f.Mime)
	}
	f.Mime = strings.ToLower(f.Mime)
	base := strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, base)
	base = strings.Trim(base, "._")
	if base == "" {
		base = "file"
	}
	if r := []rune(base); len(r) > 48 {
		base = string(r[:48])
	}
	f.Name = base + want
	return f, nil
}

// Args is the tmux command for a normalized answer against a pane id: the text, then the keys,
// one invocation. A short one-line text goes as keys (send-keys -l), a long or multi-line one
// through a buffer and a bracketed paste, which an agent takes as one paste. A tmux argument
// that ends in ";" would end the command, so a trailing semicolon is escaped the way tmux reads
// it (\;); "--" keeps a text that starts with "-" from being read as a flag.
func Args(pane string, a Answer) []string {
	var args []string
	if a.Text != "" {
		text := a.Text
		if strings.HasSuffix(text, ";") {
			text = text[:len(text)-1] + `\;`
		}
		if Long(a.Text) {
			args = append(args, "set-buffer", "-b", Buffer, "--", text, ";", "paste-buffer", "-d", "-p", "-b", Buffer, "-t", pane)
		} else {
			args = append(args, "send-keys", "-t", pane, "-l", "--", text)
		}
	}
	if len(a.Keys) > 0 {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(append(args, "send-keys", "-t", pane), a.Keys...)
	}
	return args
}

// Type sends a normalized answer to a pane: the text, then after a pause the keys, each in its
// own tmux invocation when both are present. run is the tmux client's Run. An error never
// carries the text (tmux's own errors quote the command line).
func Type(run func(args ...string) (string, error), pane string, a Answer) error {
	if a.Text != "" && len(a.Keys) > 0 {
		if _, err := run(Args(pane, Answer{Text: a.Text})...); err != nil {
			return hide(err, a.Text)
		}
		time.Sleep(Gap(a.Text))
		_, err := run(Args(pane, Answer{Keys: a.Keys})...)
		return hide(err, a.Text)
	}
	_, err := run(Args(pane, a)...)
	return hide(err, a.Text)
}

// Gap is the pause between a text and the keys after it.
func Gap(text string) time.Duration {
	if Long(text) {
		return PasteGap
	}
	return KeyGap
}

// hide takes the typed text out of an error's message.
func hide(err error, text string) error {
	if err == nil || text == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), text, "…")
	if strings.HasSuffix(text, ";") {
		msg = strings.ReplaceAll(msg, text[:len(text)-1]+`\;`, "…")
	}
	return errors.New(msg)
}

// WithPaths is the answer as it is typed once its files are saved at paths: the text, then the
// paths in order, which Claude Code reads as images or documents (Copilot wants them as @path).
func WithPaths(a Answer, paths []string, agentKind string) Answer {
	text := strings.TrimRight(a.Text, " \t\n")
	for _, p := range paths {
		if agentKind == "copilot" {
			p = "@" + p
		}
		if text == "" {
			text = p
		} else {
			text += " " + p
		}
	}
	return Answer{Pane: a.Pane, Text: text, Keys: a.Keys}
}

// A Saver puts a file where the agent can read it and returns the path to type; nil where
// files cannot be saved.
type Saver func(File) (string, error)

// DirSaver saves into dir (Save).
func DirSaver(dir string) Saver {
	return func(f File) (string, error) { return Save(dir, f) }
}

// Save writes a normalized file into dir (created if need be, mode 0700) under a stamped name,
// drops what has been there longer than KeepFiles, and returns the file's path.
func Save(dir string, f File) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	Sweep(dir, time.Now())
	stamp := time.Now().Format("20060102-150405")
	path := filepath.Join(dir, stamp+"-"+f.Name)
	for i := 2; ; i++ {
		if _, err := os.Lstat(path); err != nil {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%d-%s", stamp, i, f.Name))
	}
	tmp := path + ".part"
	if err := os.WriteFile(tmp, f.Data, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// Sweep removes the files in dir older than KeepFiles as of now.
func Sweep(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() && now.Sub(info.ModTime()) > KeepFiles {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Describe is the answer for a log line: the keys, how much text and the files' names and
// sizes, never the text itself.
func Describe(a Answer) string {
	parts := []string{}
	if a.Text != "" {
		parts = append(parts, fmt.Sprintf("%d chars", len([]rune(a.Text))))
	}
	var files []string
	for _, f := range a.Files {
		files = append(files, fmt.Sprintf("%s (%d KB)", f.Name, (len(f.Data)+1023)/1024))
	}
	if len(files) > 0 {
		parts = append(parts, strings.Join(files, ", "))
	}
	if len(a.Keys) > 0 {
		parts = append(parts, strings.Join(a.Keys, " "))
	}
	if a.Form != nil {
		picked, other := 0, 0
		for _, c := range a.Form.Answers {
			picked += len(c.Selected)
			if c.Other != "" {
				other++
			}
		}
		s := fmt.Sprintf("form: %d answers, %d picked", len(a.Form.Answers), picked)
		if other > 0 {
			s += fmt.Sprintf(", %d typed", other)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " + ")
}
