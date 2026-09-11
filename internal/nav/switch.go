// Package nav moves the driven inner tmux client to a session, window or pane.
package nav

import (
	"errors"

	"github.com/w4jnl/flok/internal/tmux"
)

// Go switches the client on tty to a session and optionally selects a window and pane, in one
// tmux invocation. IDs ($5, @12, %17) are used so names with spaces or colons never matter.
func Go(c tmux.Client, tty, sessionID, windowID, paneID string) error {
	if tty == "" {
		return errors.New("no inner tmux client to drive")
	}
	if sessionID == "" {
		return errors.New("no target session")
	}
	args := []string{"switch-client", "-c", tty, "-t", sessionID}
	if windowID != "" {
		args = append(args, ";", "select-window", "-t", windowID)
	}
	if paneID != "" {
		args = append(args, ";", "select-pane", "-t", paneID)
	}
	_, err := c.Run(args...)
	return err
}
