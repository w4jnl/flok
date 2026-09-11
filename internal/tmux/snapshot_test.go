package tmux

import "testing"

func TestParseSnapshot(t *testing.T) {
	out := "S\x1f$5\x1fPRODUCTION Nomad cluster\x1f/Users/jaro\x1f1\x1f5\x1f1789127823\x1f1780000000\n" +
		"P\x1f%32\x1f$5\x1fPRODUCTION Nomad cluster\x1f@20\x1f1\x1f1\x1f1\x1f1\x1fzsh\x1f/ops/shared\x1f/dev/ttys012\x1f4242\x1f0\x1fhidden\x1f0\x1f✳ trading: journal\x1fx\n" +
		"C\x1f/dev/ttys000\x1f$5\x1fPRODUCTION Nomad cluster\x1f1789127823\x1f317\x1f75\x1fxterm-ghostty\n"
	s := ParseSnapshot(out)
	if len(s.Sessions) != 1 || s.Sessions[0].Name != "PRODUCTION Nomad cluster" || s.Sessions[0].Attached != 1 {
		t.Fatalf("sessions: %+v", s.Sessions)
	}
	p := s.Panes[0]
	if p.ID != "%32" || p.WindowIndex != 1 || !p.Active || p.Command != "zsh" || p.PID != 4242 || p.Title != "✳ trading: journal\x1fx" {
		t.Fatalf("pane: %+v", p)
	}
	c := s.Clients[0]
	if c.TTY != "/dev/ttys000" || c.SessionID != "$5" || c.Width != 317 || c.TermName != "xterm-ghostty" {
		t.Fatalf("client: %+v", c)
	}
	if ap, ok := s.ActivePane("$5"); !ok || ap.ID != "%32" {
		t.Fatalf("active pane: %+v %v", ap, ok)
	}
}
