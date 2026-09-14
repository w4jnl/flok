package keys

import "github.com/w4jnl/flok/internal/tmux"

// PopupArgs builds the display-popup command that shows the keybinds help over the whole
// window, or nil when this tmux has no popups (before 3.2; the caller draws the help inline or
// in a window instead). The border and title flags need 3.3. client ("" = current) is the
// tty of the client that pressed the key, as tmux bindings see it in #{client_tty}.
func PopupArgs(f tmux.Features, client, cmd string) []string {
	if !f.Popup {
		return nil
	}
	args := []string{"display-popup", "-E", "-w", "80%", "-h", "85%"}
	if client != "" {
		args = append(args, "-c", client)
	}
	if f.PopupBorder {
		args = append(args, "-b", "rounded", "-T", " keybinds ")
	}
	return append(args, cmd)
}
