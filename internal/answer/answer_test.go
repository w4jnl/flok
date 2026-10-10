package answer

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeAndArgs(t *testing.T) {
	a, err := Normalize(Answer{Pane: "%3", Text: "yes", Keys: []string{"enter"}})
	if err != nil || !reflect.DeepEqual(a.Keys, []string{"Enter"}) {
		t.Fatalf("normalize: %+v %v", a, err)
	}
	want := []string{"send-keys", "-t", "%3", "-l", "yes", ";", "send-keys", "-t", "%3", "Enter"}
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
	if got := Args("%1", a); !reflect.DeepEqual(got, []string{"send-keys", "-t", "%1", "-l", `echo hi\;`}) {
		t.Fatalf("semicolon: %q", got)
	}
	if Describe(Answer{Text: "héllo", Keys: []string{"Enter"}}) != "5 chars + Enter" {
		t.Fatalf("describe: %q", Describe(Answer{Text: "héllo", Keys: []string{"Enter"}}))
	}
}

func TestNormalizeRefuses(t *testing.T) {
	bad := []Answer{
		{},                               // nothing
		{Text: strings.Repeat("x", 201)}, // too long
		{Text: "a\x1bb"},                 // a control character
		{Keys: []string{"F1"}},           // not in the list
		{Keys: []string{"C-d"}},          // only C-c
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
}
