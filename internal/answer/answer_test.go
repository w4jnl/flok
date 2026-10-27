package answer

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
)

func TestNormalizeAndArgs(t *testing.T) {
	a, err := Normalize(Answer{Pane: "%3", Text: "yes", Keys: []string{"enter"}})
	if err != nil || !reflect.DeepEqual(a.Keys, []string{"Enter"}) {
		t.Fatalf("normalize: %+v %v", a, err)
	}
	want := []string{"send-keys", "-t", "%3", "-l", "--", "yes", ";", "send-keys", "-t", "%3", "Enter"}
	if got := Args("%3", a); !reflect.DeepEqual(got, want) {
		t.Fatalf("args: %q", got)
	}
	// a key alone, several keys, aliases
	a, err = Normalize(Answer{Keys: []string{"Esc", "ctrl-c", "Down"}})
	if err != nil || !reflect.DeepEqual(a.Keys, []string{"Escape", "C-c", "Down"}) {
		t.Fatalf("aliases: %+v %v", a, err)
	}
	if got := Args("%1", a); !reflect.DeepEqual(got, []string{"send-keys", "-t", "%1", "Escape", "C-c", "Down"}) {
		t.Fatalf("keys only: %q", got)
	}
	// text only; a trailing semicolon would end the tmux command
	a, _ = Normalize(Answer{Text: "echo hi;"})
	if got := Args("%1", a); !reflect.DeepEqual(got, []string{"send-keys", "-t", "%1", "-l", "--", `echo hi\;`}) {
		t.Fatalf("semicolon: %q", got)
	}
	// a text that starts with a dash is text, not a flag
	a, _ = Normalize(Answer{Text: "-v please"})
	if got := Args("%1", a); !reflect.DeepEqual(got, []string{"send-keys", "-t", "%1", "-l", "--", "-v please"}) {
		t.Fatalf("dash: %q", got)
	}
	if Describe(Answer{Text: "héllo", Keys: []string{"Enter"}}) != "5 chars + Enter" {
		t.Fatalf("describe: %q", Describe(Answer{Text: "héllo", Keys: []string{"Enter"}}))
	}
	// Type: text and keys in two writes with a pause between, one write otherwise
	var calls [][]string
	var times []time.Time
	run := func(args ...string) (string, error) {
		calls = append(calls, args)
		times = append(times, time.Now())
		return "", nil
	}
	if err := Type(run, "%3", Answer{Text: "yes", Keys: []string{"Enter"}}); err != nil || len(calls) != 2 ||
		!reflect.DeepEqual(calls[0], []string{"send-keys", "-t", "%3", "-l", "--", "yes"}) || !reflect.DeepEqual(calls[1], []string{"send-keys", "-t", "%3", "Enter"}) ||
		times[1].Sub(times[0]) < KeyGap {
		t.Fatalf("type text+keys: %q %v (gap %v)", calls, err, times[len(times)-1].Sub(times[0]))
	}
	calls = nil
	if err := Type(run, "%3", Answer{Keys: []string{"Escape"}}); err != nil || len(calls) != 1 {
		t.Fatalf("type keys only: %q %v", calls, err)
	}
}

// A long or multi-line text is pasted through a buffer, bracketed, and the keys wait longer.
func TestLongTextIsPasted(t *testing.T) {
	multi := "first line\r\nsecond;"
	a, err := Normalize(Answer{Pane: "%3", Text: multi, Keys: []string{"Enter"}})
	if err != nil || a.Text != "first line\nsecond;" {
		t.Fatalf("normalize: %q %v", a.Text, err)
	}
	if !Long(a.Text) || Long("one line") || !Long(strings.Repeat("x", MaxText+1)) || Long(strings.Repeat("x", MaxText)) {
		t.Fatal("Long: a newline or more than MaxText runes")
	}
	want := []string{"set-buffer", "-b", Buffer, "--", "first line\nsecond\\;", ";", "paste-buffer", "-d", "-p", "-b", Buffer, "-t", "%3",
		";", "send-keys", "-t", "%3", "Enter"}
	if got := Args("%3", a); !reflect.DeepEqual(got, want) {
		t.Fatalf("args: %q", got)
	}
	long := strings.Repeat("y", MaxText+1)
	if got := Args("%3", Answer{Text: long}); got[0] != "set-buffer" || got[4] != long {
		t.Fatalf("long args: %q", got[:4])
	}
	if Gap(long) != PasteGap || Gap("y") != KeyGap {
		t.Fatal("gap")
	}
	var calls [][]string
	var times []time.Time
	run := func(args ...string) (string, error) {
		calls = append(calls, args)
		times = append(times, time.Now())
		return "", nil
	}
	if err := Type(run, "%3", a); err != nil || len(calls) != 2 || calls[0][0] != "set-buffer" || times[1].Sub(times[0]) < PasteGap {
		t.Fatalf("type: %q %v", calls, err)
	}
	if _, err := Normalize(Answer{Text: strings.Repeat("x", MaxLongText+1)}); err == nil {
		t.Fatal("over MaxLongText accepted")
	}
	// an error from tmux quotes the command line; the text never reaches a log through it
	fail := func(args ...string) (string, error) {
		return "", errorf("tmux send-keys -t %%3 -l -- " + strings.Join(args[5:], " ") + ": no such pane")
	}
	err = Type(fail, "%3", Answer{Text: "secret words"})
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "no such pane") {
		t.Fatalf("error: %v", err)
	}
}

type errorf string

func (e errorf) Error() string { return string(e) }

func TestFiles(t *testing.T) {
	data := []byte{0xff, 0xd8, 0xff, 1, 2, 3}
	a, err := Normalize(Answer{Pane: "%1", Text: "see", Keys: []string{"Enter"}, Files: []File{{Name: "Screenshot 2026-10-14 at 10.02.11.png", Data: data}}})
	if err != nil || len(a.Files) != 1 || a.Files[0].Name != "Screenshot_2026-10-14_at_10.02.11.png" || a.Files[0].Mime != "image/png" {
		t.Fatalf("normalize: %+v %v", a.Files, err)
	}
	// "file", one, is what the first app build sends: folded into the list, after the list's own
	a2, _ := Normalize(Answer{Files: []File{{Name: "first.png", Data: data}}, File: &File{Name: "../../etc/passwd", Mime: "image/jpeg", Data: data}})
	if len(a2.Files) != 2 || a2.Files[0].Name != "first.png" || a2.Files[1].Name != "etc_passwd.jpg" || a2.File != nil {
		t.Fatalf("name: %+v", a2.Files)
	}
	a3, _ := Normalize(Answer{File: &File{Name: "", Mime: "application/pdf", Data: data}})
	if a3.Files[0].Name != "file.pdf" {
		t.Fatalf("no name: %q", a3.Files[0].Name)
	}
	if _, err := Normalize(Answer{Files: make([]File, MaxFiles+1)}); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("too many files: %v", err)
	}
	for i, bad := range []File{
		{Name: "x.jpg"},                                       // empty
		{Name: "x.exe", Data: data},                           // not a kind we take
		{Name: "x", Mime: "text/plain", Data: data},           // nor that
		{Name: "x.jpg", Data: make([]byte, MaxFile+1)},        // too big
		{Name: "x.jpg", Mime: "application/x-sh", Data: data}, // the mime wins over the name
	} {
		if _, err := Normalize(Answer{File: &bad}); err == nil {
			t.Fatalf("file %d accepted: %+v", i, bad.Name)
		}
	}
	if got := Describe(a); got != "3 chars + Screenshot_2026-10-14_at_10.02.11.png (1 KB) + Enter" {
		t.Fatalf("describe: %q", got)
	}
	if got := Describe(a2); got != "first.png (1 KB), etc_passwd.jpg (1 KB)" {
		t.Fatalf("describe two: %q", got)
	}
	// typed after the text, as paths in order; @path for Copilot
	w := WithPaths(a, []string{"/tmp/attach/x.png", "/tmp/attach/y.pdf"}, "claude")
	if w.Text != "see /tmp/attach/x.png /tmp/attach/y.pdf" || w.Files != nil || w.Keys[0] != "Enter" {
		t.Fatalf("with paths: %+v", w)
	}
	if w := WithPaths(Answer{Text: "  \n"}, []string{"/tmp/x.png"}, "copilot"); w.Text != "@/tmp/x.png" {
		t.Fatalf("copilot: %q", w.Text)
	}
	// saved under a stamped name, old ones swept
	dir := filepath.Join(t.TempDir(), "attach")
	old := filepath.Join(dir, "old.png")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(old, data, 0o600)
	stale := time.Now().Add(-2 * KeepFiles)
	_ = os.Chtimes(old, stale, stale)
	p1, err := Save(dir, a.Files[0])
	if err != nil || !strings.HasPrefix(filepath.Base(p1), time.Now().Format("20060102")) || !strings.HasSuffix(p1, "-"+a.Files[0].Name) {
		t.Fatalf("save: %q %v", p1, err)
	}
	if b, _ := os.ReadFile(p1); !reflect.DeepEqual(b, data) {
		t.Fatal("content")
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("the old file was not swept")
	}
	p2, _ := Save(dir, a.Files[0])
	if p2 == p1 {
		t.Fatal("a second file in the same second got the same name")
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", info.Mode())
	}
}

func TestNormalizeRefuses(t *testing.T) {
	bad := []Answer{
		{}, // nothing
		{Text: strings.Repeat("x", MaxLongText+1)}, // too long
		{Text: "a\x1bb"},        // a control character
		{Keys: []string{"F1"}},  // not in the list
		{Keys: []string{"C-d"}}, // only C-c
		{Keys: strings.Fields("Up Up Up Up Up Up Up Up Up")}, // too many
	}
	for i, a := range bad {
		if _, err := Normalize(a); err == nil {
			t.Fatalf("case %d accepted: %+v", i, a)
		}
	}
	if _, err := Normalize(Answer{Text: "tab\there"}); err != nil {
		t.Fatalf("a tab is printable enough: %v", err)
	}
	if _, err := Normalize(Answer{Text: strings.Repeat("x", 201)}); err != nil {
		t.Fatalf("over MaxText is long, not refused: %v", err)
	}
}

func TestNormalizeForm(t *testing.T) {
	a, err := Normalize(Answer{Pane: "%3", Form: &FormAnswer{Answers: []Choice{{Selected: []int{2, 0, 2}, Other: "  Kiwi "}, {Selected: []int{1}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Form == nil || len(a.Form.Answers) != 2 || !a.Empty() == false || a.Text != "" {
		t.Fatalf("normalized: %+v", a)
	}
	if c := a.Form.Answers[0]; len(c.Selected) != 2 || c.Selected[0] != 0 || c.Selected[1] != 2 || c.Other != "Kiwi" {
		t.Errorf("choice 1 not sorted, deduplicated and trimmed: %+v", c)
	}
	if d := Describe(a); d != "form: 2 answers, 3 picked, 1 typed" {
		t.Errorf("describe %q", d)
	}
	bad := []Answer{
		{Form: &FormAnswer{}}, // answers nothing
		{Form: &FormAnswer{Answers: []Choice{{Selected: []int{9}}}}},              // beyond the digits
		{Form: &FormAnswer{Answers: []Choice{{Selected: []int{-1}}}}},             // below them
		{Form: &FormAnswer{Answers: []Choice{{Other: strings.Repeat("x", 201)}}}}, // too long
		{Form: &FormAnswer{Answers: []Choice{{Other: "a\nb"}}}},                   // not one line
		{Text: "yes", Form: &FormAnswer{Answers: []Choice{{Selected: []int{0}}}}}, // nothing else with a form
		{Keys: []string{"Enter"}, Form: &FormAnswer{Answers: []Choice{{Selected: []int{0}}}}},
		{Form: &FormAnswer{Answers: make([]Choice, agent.MaxFormQuestions+1)}}, // too many questions
	}
	for i, a := range bad {
		if _, err := Normalize(a); err == nil {
			t.Errorf("case %d accepted: %+v", i, a)
		}
	}
	// an empty choice passes Normalize (the relay cannot know the form); Fill refuses it against the form
	if _, err := Normalize(Answer{Form: &FormAnswer{Answers: []Choice{{}}}}); err != nil {
		t.Errorf("an empty choice is Fill's to refuse: %v", err)
	}
}
