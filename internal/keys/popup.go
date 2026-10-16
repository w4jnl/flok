package keys

import "github.com/w4jnl/flok/internal/tmux"

// PopupArgs builds the display-popup command that shows the keybinds help over the whole
// window, or nil when this tmux has no popups (before 3.2; the caller draws the help inline or
// in a window instead). The border and title flags need 3.3. client ("" = current) is the
// tty of the client that pressed the key, as tmux bindings see it in #{client_tty}.
func PopupArgs(f tmux.Features, client, cmd string) []string {
	return PopupArgsTitled(f, client, cmd, " keybinds ")
}

// popupStyle is the terminal's own colours, for the popup and its border alike: flok's help and
// install output are drawn for the terminal's background, and tmux 3.8 gives popups a grey one
// of its own (popup-style bg=themedarkgrey). Both flags get the same style, so which of -s and
// -S styles the border does not matter.
const popupStyle = "fg=default,bg=default"

// PopupArgsTitled is PopupArgs with the popup's title (shown from 3.3 on).
func PopupArgsTitled(f tmux.Features, client, cmd, title string) []string {
	return PopupArgsSized(f, client, cmd, title, "80%", "85%")
}

// PopupArgsSized is PopupArgsTitled with the popup's size (tmux's -w and -h: cells or a
// percentage), for a popup drawn to a fixed picture such as the pairing QR.
func PopupArgsSized(f tmux.Features, client, cmd, title, width, height string) []string {
	if !f.Popup {
		return nil
	}
	args := []string{"display-popup", "-E", "-w", width, "-h", height}
	if client != "" {
		args = append(args, "-c", client)
	}
	if f.PopupBorder {
		args = append(args, "-b", "rounded", "-T", title, "-s", popupStyle, "-S", popupStyle)
	}
	return append(args, cmd)
}
