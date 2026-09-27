// Package tmuxtest is a tmux.Client stand-in for tests: it answers batched screen captures the
// way tmux does and records every call.
package tmuxtest

import (
	"errors"
	"strings"
)

// Fake answers a batched capture like tmux does: marker lines from display-message, then the
// pane text; when a pane is missing it stops the sequence there and reports an error. Every
// other command returns an empty answer.
type Fake struct {
	Screens map[string]string
	Calls   [][]string
}

func (f *Fake) Label() string { return "fake" }

func (f *Fake) Run(args ...string) (string, error) {
	f.Calls = append(f.Calls, args)
	var out strings.Builder
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "display-message": // -p -t <pane> <fmt>
			pane := args[i+3]
			out.WriteString(strings.ReplaceAll(args[i+4], "#{pane_id}", pane) + "\n")
			i += 4
		case "capture-pane": // -p -J -t <pane> [-S -n]
			pane := args[i+4]
			text, ok := f.Screens[pane]
			if !ok {
				return out.String(), errors.New("can't find pane: " + pane)
			}
			out.WriteString(text)
			i += 4
		}
	}
	return out.String(), nil
}
