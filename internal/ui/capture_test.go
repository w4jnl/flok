package ui

import (
	"errors"
	"strings"
	"testing"
)

// fakeTmux answers a batched capture like tmux does: marker lines from display-message, then the
// pane text; when a pane is missing it stops the sequence there and reports an error.
type fakeTmux struct {
	screens map[string]string
	calls   [][]string
}

func (f *fakeTmux) Label() string { return "fake" }
func (f *fakeTmux) Run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	var out strings.Builder
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "display-message": // -p -t <pane> <fmt>
			pane := args[i+3]
			out.WriteString(strings.ReplaceAll(args[i+4], "#{pane_id}", pane) + "\n")
			i += 4
		case "capture-pane": // -p -J -t <pane> [-S -n]
			pane := args[i+4]
			text, ok := f.screens[pane]
			if !ok {
				return out.String(), errors.New("can't find pane: " + pane)
			}
			out.WriteString(text)
			i += 4
		}
	}
	return out.String(), nil
}

func TestCaptureAllSplitsOneInvocation(t *testing.T) {
	f := &fakeTmux{screens: map[string]string{"%1": "one a\none b\n", "%2": "\n╭──╮\n│ > │\n╰──╯\n"}}
	got := captureAll(f, []string{"%1", "%2"}, 0)
	if len(f.calls) != 1 {
		t.Fatalf("want one tmux call, got %d", len(f.calls))
	}
	for pane, want := range f.screens {
		if got[pane] != want {
			t.Errorf("%s: got %q want %q", pane, got[pane], want)
		}
	}
}

func TestCaptureAllFallsBackForMissingPane(t *testing.T) {
	f := &fakeTmux{screens: map[string]string{"%1": "first\n", "%3": "third\n"}}
	got := captureAll(f, []string{"%1", "%2", "%3"}, 0)
	if got["%1"] != "first\n" || got["%3"] != "third\n" {
		t.Errorf("got %q", got)
	}
	if _, ok := got["%2"]; ok {
		t.Errorf("vanished pane must be absent, got %q", got["%2"])
	}
	// the batch aborted at %2, so %3 (and %2 itself) were retried individually
	if len(f.calls) != 3 {
		t.Errorf("want batch + 2 single captures, got %d calls", len(f.calls))
	}
}
