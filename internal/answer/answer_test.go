package answer

import (
	"reflect"
	"strings"
	"testing"
	"time"
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
	// Type: text and keys in two writes with a pause between, one write otherwise
	var calls [][]string
	var times []time.Time
	run := func(args ...string) (string, error) {
		calls = append(calls, args)
		times = append(times, time.Now())
		return "", nil
	}
	if err := Type(run, "%3", Answer{Text: "yes", Keys: []string{"Enter"}}); err != nil || len(calls) != 2 ||
		!reflect.DeepEqual(calls[0], []string{"send-keys", "-t", "%3", "-l", "yes"}) || !reflect.DeepEqual(calls[1], []string{"send-keys", "-t", "%3", "Enter"}) ||
		times[1].Sub(times[0]) < KeyGap {
		t.Fatalf("type text+keys: %q %v (gap %v)", calls, err, times[len(times)-1].Sub(times[0]))
	}
	calls = nil
	if err := Type(run, "%3", Answer{Keys: []string{"Escape"}}); err != nil || len(calls) != 1 {
		t.Fatalf("type keys only: %q %v", calls, err)
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
