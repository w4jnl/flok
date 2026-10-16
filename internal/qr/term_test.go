package qr

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The terminal picture is one line per two module rows, every line the same width, the quiet
// zone light all round, and a known dark module (the finder's corner) dark.
func TestTerminal(t *testing.T) {
	m, err := Encode("flok://pair?code=AB7Q-K2MN&relay=https%3A%2F%2Fflok.example.net")
	if err != nil {
		t.Fatal(err)
	}
	cols, rows := m.TerminalSize(4)
	if cols != m.Size+8 || rows != (m.Size+8+1)/2 {
		t.Fatalf("size %dx%d for %d modules", cols, rows, m.Size)
	}
	out := m.Terminal(4)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != rows {
		t.Fatalf("%d lines, want %d", len(lines), rows)
	}
	for i, l := range lines {
		if !strings.HasPrefix(l, "\x1b[30;47m") || !strings.HasSuffix(l, "\x1b[0m") {
			t.Fatalf("line %d is not black on white: %q", i, l)
		}
		body := strings.TrimSuffix(strings.TrimPrefix(l, "\x1b[30;47m"), "\x1b[0m")
		if utf8.RuneCountInString(body) != cols {
			t.Fatalf("line %d is %d cells wide, want %d", i, utf8.RuneCountInString(body), cols)
		}
		if i < 2 || i >= rows-2 {
			if strings.Trim(body, " ") != "" {
				t.Fatalf("quiet line %d has ink: %q", i, body)
			}
		}
	}
	// the finder pattern's top-left module (0,0) is dark, and so is (0,1): a full block at
	// column 4 of line 2
	body := []rune(strings.TrimSuffix(strings.TrimPrefix(lines[2], "\x1b[30;47m"), "\x1b[0m"))
	if body[4] != '█' {
		t.Fatalf("the finder corner renders as %q", body[4])
	}
	if m.Size != 37 { // version 5 for this URL at level M
		t.Fatalf("version: %d modules", m.Size)
	}
}
