package cli

import (
	"fmt"
	"os"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/state"
)

// runTheme shows or sets the sidebar's light/dark palette. Without arguments (or `auto`) it asks
// this terminal for its background, outside tmux only, records it like `flok up` does and reports
// what the sidebar uses; `dark` or `light` switch a running sidebar at once, until the next
// `flok up` detects the terminal again.
func runTheme(cfg config.Config, args []string) int {
	st := state.New(config.StateDir())
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	switch arg {
	case "dark", "light":
		if err := st.SetTerminalTheme(arg, ""); err != nil {
			return report(err)
		}
		fmt.Printf("sidebar theme: %s (until the next flok up detects the terminal again)\n", arg)
		if cfg.Theme.Mode == "dark" || cfg.Theme.Mode == "light" {
			fmt.Printf("note: [theme] mode = %q in config.toml takes precedence\n", cfg.Theme.Mode)
		}
		return 0
	case "", "auto":
	default:
		fmt.Fprintln(os.Stderr, "usage: flok theme [auto|dark|light]")
		return 2
	}
	if os.Getenv("TMUX") != "" {
		fmt.Println("inside tmux the answer would come from tmux, not your terminal: run `flok theme` in a plain terminal")
	} else if r, err := launcher.DetectTerminalTheme(); err != nil {
		fmt.Println("terminal background: not detected:", err)
	} else {
		fmt.Printf("terminal background: %s (%s)\n", r.BG, r.Theme)
	}
	detected, _ := st.TerminalTheme()
	fmt.Printf("sidebar theme: %s ([theme] mode = %q)\n", themeName(cfg.Theme.IsDark(detected)), cfg.Theme.Mode)
	return 0
}

func themeName(dark bool) string {
	if dark {
		return "dark"
	}
	return "light"
}
