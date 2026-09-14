package tmux

import "testing"

func TestParseSnapshot(t *testing.T) {
	out := "S\x1f$5\x1fPRODUCTION Nomad cluster\x1f/Users/jaro\x1f1\x1f5\x1f1789127823\x1f1780000000\n" +
		"P\x1f%32\x1f$5\x1fPRODUCTION Nomad cluster\x1f@20\x1f1\x1f1\x1f1\x1f1\x1fzsh\x1f/ops/shared\x1f/dev/ttys012\x1f4242\x1f0\x1fhidden\x1f0\x1f1\x1f✳ trading: journal\x1fx\n" +
		"C\x1f/dev/ttys000\x1f$5\x1fPRODUCTION Nomad cluster\x1f1789127823\x1f317\x1f75\x1fxterm-ghostty\n"
	s := ParseSnapshot(out)
	if len(s.Sessions) != 1 || s.Sessions[0].Name != "PRODUCTION Nomad cluster" || s.Sessions[0].Attached != 1 {
		t.Fatalf("sessions: %+v", s.Sessions)
	}
	p := s.Panes[0]
	if p.ID != "%32" || p.WindowIndex != 1 || !p.Active || p.Command != "zsh" || p.PID != 4242 || !p.InMode || p.Title != "✳ trading: journal\x1fx" {
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

// tmux 3.4+ prints the 0x1f separator as the vis(3) escape `\037`; TakeSnapshotRaw decodes it
// when the client reports that version.
func TestParseSnapshotEscapedSeparator(t *testing.T) {
	out := `S\037$0\037Alpha\037/home/user/flok\0370\0372\0371789335588\0371789335588` + "\n" +
		`P\037%2\037$0\037Alpha\037@1\0371\0370\0370\0371\037claude\037/home/user/flok\037/dev/pts/2\03715300\0370\037\0370\0370\037✳ fake-agent` + "\n" +
		`C\037/dev/pts/3\037$1\037Beta\0371789335548\037200\03750\037tmux-256color` + "\n"
	if raw := ParseSnapshot(out); len(raw.Panes) != 0 {
		t.Fatalf("undecoded output must not parse into panes: %+v", raw.Panes)
	}
	s := ParseSnapshot(Decode(out, true))
	if len(s.Sessions) != 1 || s.Sessions[0].Name != "Alpha" || s.Sessions[0].Windows != 2 {
		t.Fatalf("sessions: %+v", s.Sessions)
	}
	if len(s.Panes) != 1 || s.Panes[0].ID != "%2" || s.Panes[0].Command != "claude" || s.Panes[0].PBState != "" || s.Panes[0].PBProgress != "0" || s.Panes[0].InMode || s.Panes[0].Title != "✳ fake-agent" {
		t.Fatalf("panes: %+v", s.Panes)
	}
	if len(s.Clients) != 1 || s.Clients[0].TTY != "/dev/pts/3" || s.Clients[0].SessionName != "Beta" {
		t.Fatalf("clients: %+v", s.Clients)
	}
}
