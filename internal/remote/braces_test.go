package remote

import (
	"reflect"
	"strings"
	"testing"
)

// tmux 3.7 prints brace groups in list-keys ("{ join-pane }"); a saved line must go back as
// one argument per group, or tmux rejects the bind and stops the whole restore sequence.
func TestSplitTmuxWordsKeepsBraceGroups(t *testing.T) {
	line := `bind-key    -T prefix @       if-shell -F "#{pane_floating_flag}" { join-pane } { break-pane -W }`
	want := []string{"bind-key", "-T", "prefix", "@", "if-shell", "-F", "#{pane_floating_flag}", "{ join-pane }", "{ break-pane -W }"}
	if got := SplitTmuxWords(line); !reflect.DeepEqual(got, want) {
		t.Fatalf("words %q", got)
	}
	nested := `bind-key -T prefix x if-shell -F 1 { if-shell -F 2 { a } { b } } { c "d }" }`
	if got := SplitTmuxWords(nested); len(got) != 9 || got[7] != "{ if-shell -F 2 { a } { b } }" || got[8] != `{ c "d }" }` {
		t.Fatalf("nested %q", got)
	}
	if got := SplitTmuxWords(`bind-key -T prefix , command-prompt -I "#W" { rename-window "%%" }`); got[len(got)-1] != `{ rename-window "%%" }` {
		t.Fatalf("quoted inside %q", got)
	}
	args := bindArgs([]string{line, "bind-key -T prefix \\; last-pane"})
	joined := strings.Join(args, "|")
	if !strings.Contains(joined, "|{ join-pane }|{ break-pane -W }|;|bind-key|-T|prefix|\\;|last-pane") {
		t.Fatalf("bind args %q", joined)
	}
}
