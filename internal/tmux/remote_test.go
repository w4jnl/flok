package tmux

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestShellJoinGolden(t *testing.T) {
	got := ShellJoin([]string{"tmux", "-L", "agents", "list-panes", "-a", "-F", "#{pane_id}", ";", "rename-window", "it's a; test", ""})
	want := `tmux -L agents list-panes -a -F '#{pane_id}' ';' rename-window 'it'\''s a; test' ''`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// Run hands the prefix one extra argument: the quoted tmux command line.
func TestRemoteRunPassesOneCommandArgument(t *testing.T) {
	r := &Remote{Argv: []string{"sh", "-c", `printf '%s\n' "$#" "$1"`, "sh"}, Socket: "agents", Name: "beta"}
	out, err := r.Run("display-message", "-p", "#{pane_id};x")
	if err != nil {
		t.Fatal(err)
	}
	if out != "1\ntmux -L agents display-message -p '#{pane_id};x'\n" {
		t.Fatalf("got %q", out)
	}
	r = &Remote{Argv: []string{"sh", "-c", "echo boom >&2; exit 255", "sh"}, Name: "beta"}
	_, err = r.Run("list-sessions")
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 255 || ee.Stderr != "boom" || !strings.HasPrefix(err.Error(), "beta: tmux list-sessions: boom") {
		t.Fatalf("exit error: %v (%+v)", err, ee)
	}
	r = &Remote{Argv: []string{"sh", "-c", "sleep 5", "sh"}, Timeout: 100 * time.Millisecond}
	if _, err := r.Run("list-sessions"); !errors.As(err, &ee) || ee.Code != -1 || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout: %v", err)
	}
}

func TestRemoteFeaturesFollowVersion(t *testing.T) {
	r := &Remote{Argv: []string{"sh", "-c", "echo tmux 3.4", "sh"}}
	if !Escapes(r) {
		t.Fatal("a 3.4 remote escapes its output")
	}
	r = &Remote{Argv: []string{"false"}}
	r.SetVersion(ParseVersion("2.7"))
	if Escapes(r) || r.Features().Popup {
		t.Fatal("a recorded 2.7 has neither escaping nor popups")
	}
}
