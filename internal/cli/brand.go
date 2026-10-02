package cli

import (
	"bufio"
	"fmt"
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

// yesNo asks a person on the terminal and reads one line; without a terminal on both ends
// (a script, a pipe, the e2e) it asks nothing and answers no.
func yesNo(prompt string) bool {
	if !stdoutIsTerminal() || !term.IsTerminal(int(os.Stdin.Fd())) {
		return false
	}
	fmt.Print(prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}
