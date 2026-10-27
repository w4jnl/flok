package answer

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/w4jnl/flok/internal/agent"
)

// TestFillLive drives a real Claude Code question form, which is how the choreography in
// form.go was checked against Claude Code 2.1.310; skipped unless FLOK_LIVE_FORM names a pane.
//
//	FLOK_LIVE_FORM="<socket name>:<pane>"   a pane (tmux -L <socket name>) showing the probe form:
//	                                        Fruit (multi: Apple, Banana, Cherry), Size (Small, Large)
//	FLOK_LIVE_ANSWER='{"answers":[{"selected":[0,2],"other":"Kiwi"},{"other":"Medium"}]}'
//
// The form comes from asking Claude Code: Call the AskUserQuestion tool once, with exactly two
// questions. Question 1: header "Fruit", question "Which fruits do you like?", multiSelect
// true, options Apple, Banana, Cherry. Question 2: header "Size", question "Which size?",
// multiSelect false, options Small, Large.
func TestFillLive(t *testing.T) {
	spec := os.Getenv("FLOK_LIVE_FORM")
	if spec == "" {
		t.Skip("FLOK_LIVE_FORM not set")
	}
	sock, pane, _ := strings.Cut(spec, ":")
	run := func(args ...string) (string, error) {
		out, err := exec.Command("tmux", append([]string{"-L", sock}, args...)...).CombinedOutput()
		return string(out), err
	}
	f := agent.Form{Questions: []agent.Question{
		{Header: "Fruit", Text: "Which fruits do you like?", Multi: true, Options: []agent.Option{{Label: "Apple"}, {Label: "Banana"}, {Label: "Cherry"}}},
		{Header: "Size", Text: "Which size?", Options: []agent.Option{{Label: "Small"}, {Label: "Large"}}},
	}}
	if j := os.Getenv("FLOK_LIVE_FORMJSON"); j != "" { // another form on the screen, as agent.Form JSON
		f = agent.Form{} // decoded fresh: Unmarshal into the default would keep its fields
		if err := json.Unmarshal([]byte(j), &f); err != nil {
			t.Fatal(err)
		}
	}
	var fa FormAnswer
	if err := json.Unmarshal([]byte(os.Getenv("FLOK_LIVE_ANSWER")), &fa); err != nil {
		t.Fatal(err)
	}
	if err := Fill(run, pane, f, fa); err != nil {
		t.Fatal(err)
	}
}

// TestParseFormLive reads a pane and prints what parseForm makes of it, touching nothing:
// FLOK_LIVE_PARSE="<socket name>:<pane>" ("default:%54" for the user's own server). For a form
// that Fill said was not on the screen.
func TestParseFormLive(t *testing.T) {
	spec := os.Getenv("FLOK_LIVE_PARSE")
	if spec == "" {
		t.Skip("FLOK_LIVE_PARSE not set")
	}
	sock, pane, _ := strings.Cut(spec, ":")
	out, err := exec.Command("tmux", "-L", sock, "capture-pane", "-p", "-t", pane).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	lines := strings.Split(string(out), "\n")
	arrows := 0
	for i, l := range lines {
		if strings.Contains(l, "←") {
			arrows++
			t.Logf("row %d has ←: %q", i, strings.TrimRight(l, " "))
		}
	}
	s := parseForm(string(out))
	t.Logf("%d rows, %d with ←; ok=%v review=%v cursor=%d question=%q", len(lines), arrows, s.ok, s.review, s.cursor, s.question)
	for i, r := range s.rows {
		t.Logf("  row %d: marked=%v next=%v %q", i, r.marked, r.next, r.label)
	}
}
