package poller

import (
	"fmt"
	"strings"

	"github.com/w4jnl/flok/internal/tmux"
)

// CaptureAll captures several panes in one tmux invocation (one fork instead of one per pane),
// with wrapped lines joined (-J) for the screen rules, whose patterns read a prompt across the
// wrap; extra > 0 adds that many scrollback lines. A marker line printed before each capture
// separates the sections. tmux stops the sequence at the first missing pane, so anything absent
// from the output is captured on its own.
func CaptureAll(c tmux.Client, panes []string, extra int) map[string]string {
	return captureAll(c, panes, func(p string) []string { return CaptureArgs(p, extra) })
}

// CaptureShown captures the screens of several panes as the terminal shows them: row by row,
// nothing joined, trailing blanks dropped. It is what goes to the phone. -J would be wrong
// there: tmux joins the rows it flagged as wrapped, and a TUI that repaints a row in place
// (text, then erase to the end of the line) never clears the flag a row got when it once ran
// past the right edge, so the next row's gutter would arrive glued to this row's text with the
// erased cells as a run of spaces (Copilot's transcript did exactly that).
func CaptureShown(c tmux.Client, panes []string) map[string]string {
	return captureAll(c, panes, func(p string) []string { return []string{"capture-pane", "-p", "-t", p} })
}

func captureAll(c tmux.Client, panes []string, argsFor func(pane string) []string) map[string]string {
	const marker = "-=flok-capture=- "
	var args []string
	for i, p := range panes {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, "display-message", "-p", "-t", p, marker+"#{pane_id}", ";")
		args = append(args, argsFor(p)...)
	}
	out, err := c.Run(args...)
	res := map[string]string{}
	cur := ""
	var buf strings.Builder
	flush := func() {
		if cur != "" {
			res[cur] = buf.String()
		}
		buf.Reset()
	}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.HasPrefix(line, marker) {
			flush()
			cur = strings.TrimSpace(strings.TrimPrefix(line, marker))
			continue
		}
		if cur != "" {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	if err == nil {
		flush()
	} // else the section in progress is the failed capture: its marker printed, its screen did not
	for _, p := range panes {
		if _, ok := res[p]; !ok {
			if o, err := c.Run(argsFor(p)...); err == nil {
				res[p] = o
			}
		}
	}
	return res
}

// CaptureArgs captures the visible screen of a pane with wrapped lines joined (agents redraw in
// place, so scrollback would resurrect dismissed prompts); extra > 0 adds that many scrollback
// lines.
func CaptureArgs(pane string, extra int) []string {
	args := []string{"capture-pane", "-p", "-J", "-t", pane}
	if extra > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", extra))
	}
	return args
}
