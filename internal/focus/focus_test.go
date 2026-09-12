package focus

import (
	"strings"
	"testing"
)

func TestStrategiesAndAerospaceSelection(t *testing.T) {
	var calls []string
	run := func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "aerospace" && args[0] == "list-windows" {
			return "12|com.apple.finder|Finder\n34|com.mitchellh.ghostty|zsh\n56|com.mitchellh.ghostty|TMUX - flok\n", nil
		}
		return "", nil
	}
	if s := Strategy(Options{Strategy: "auto", Look: func(string) bool { return true }}); s != "aerospace" {
		t.Fatalf("auto with aerospace -> %s", s)
	}
	if s := Strategy(Options{Strategy: "auto", Look: func(string) bool { return false }}); s != "applescript" {
		t.Fatalf("auto without aerospace -> %s", s)
	}
	if err := Terminal(Options{App: "ghostty", Strategy: "aerospace", Run: run}); err != nil {
		t.Fatal(err)
	}
	if calls[len(calls)-1] != "aerospace focus --window-id 56" {
		t.Fatalf("expected the TMUX-titled ghostty window, got %v", calls)
	}
	calls = nil
	if err := Terminal(Options{App: "iTerm.app", Strategy: "applescript", Run: run}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !strings.Contains(calls[0], `tell application "iTerm" to activate`) || !strings.Contains(calls[1], "AXRaise") {
		t.Fatalf("applescript calls %v", calls)
	}
	calls = nil
	if err := Terminal(Options{Strategy: "none", Run: run}); err != nil || len(calls) != 0 {
		t.Fatal("none must run nothing")
	}
	if err := Terminal(Options{Strategy: "echo {pane} {app}", App: "ghostty", Pane: "%3", Run: run}); err != nil || !strings.Contains(calls[0], "echo %3 ghostty") {
		t.Fatalf("custom command %v", calls)
	}
}
