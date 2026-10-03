package keys

import "testing"

// The `flok last …` keys get their own subsection after the flok one, whatever keys they sit on.
func TestGoingBackSubsection(t *testing.T) {
	bs := []Binding{
		{Table: "prefix", Key: "Tab", Command: `run-shell -b "/x/flok last session"`},
		{Table: "prefix", Key: "o", Command: `run-shell -b "/x/flok jump --client '#{client_tty}'"`},
		{Table: "prefix", Key: "l", Command: `run-shell -b "/x/flok last window"`},
		{Table: "prefix", Key: "c", Command: "new-window", Note: "Create a new window"},
	}
	secs := Organize(bs, "C-a", "/x/flok", nil, false)
	if len(secs) != 3 || secs[0].Name != FlokSection || secs[1].Name != FlokBackSection || secs[2].Name != "prefix C-a" {
		t.Fatalf("sections %+v", secs)
	}
	if len(secs[0].Bindings) != 1 || secs[0].Bindings[0].Key != "o" || len(secs[1].Bindings) != 2 || secs[1].Bindings[0].Label != "back to the previous session (any server)" {
		t.Fatalf("flok sections %+v / %+v", secs[0].Bindings, secs[1].Bindings)
	}
	if SectionOf(Binding{Command: `run-shell -b '/x/flok' relay last pane`}, "C-a") != FlokBackSection {
		t.Fatal("a relayed last key on a host counts too")
	}
}
