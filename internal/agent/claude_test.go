package agent

import "testing"

func TestClaudeTitle(t *testing.T) {
	c := Claude{}
	cases := []struct {
		title string
		state State
		ok    bool
		name  string
	}{
		{"✳ trading-journal", Idle, true, "trading-journal"},
		{"◑ Tmux herdr-like addon feasibility", Working, true, "Tmux herdr-like addon feasibility"},
		{"⠹ mw-studies (2)", Working, true, "mw-studies (2)"},
		{"jaroMac.w4j.nl", Unknown, false, ""},
		{"✳", Unknown, false, ""},
	}
	for _, tc := range cases {
		st, ok := c.TitleState(tc.title)
		if st != tc.state || ok != tc.ok {
			t.Errorf("%q: state %v ok %v, want %v %v", tc.title, st, ok, tc.state, tc.ok)
		}
		if n := c.TitleName(tc.title); n != tc.name {
			t.Errorf("%q: name %q, want %q", tc.title, n, tc.name)
		}
	}
	ads := Enabled([]string{"claude", "copilot", "nope"})
	if len(ads) != 2 || Match("claude", ads).ID() != "claude" || Match("2.1.268", ads).ID() != "claude" ||
		Match("copilot", ads).ID() != "copilot" || Match("zsh", ads) != nil {
		t.Fatalf("adapter matching broken: %v", ads)
	}
}
