package cli

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// Lockup is the single-line ASCII brand lockup from assets/brand/README.md: the mark as
// "[⩓▁]" (bracket, chevron, cursor) followed by the name, version and tagline. Used where
// flok introduces itself to a person: `flok version` on a terminal and the doctor header.
func Lockup(version string) string {
	return "[⩓▁] flok " + strings.TrimPrefix(version, "v") + " · agent sidebar for tmux"
}

// stdoutIsTerminal reports whether a person, not a pipe, is reading stdout.
func stdoutIsTerminal() bool { return term.IsTerminal(int(os.Stdout.Fd())) }
