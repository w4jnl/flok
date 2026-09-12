package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/install"
)

func claudeSettingsPath() string {
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return filepath.Join(v, "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

func copilotHooksPath() string {
	base := os.Getenv("COPILOT_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".copilot")
	}
	return filepath.Join(base, "hooks", "flok.json")
}

// runInstall handles `install [--claude] [--copilot] [--tmux]` (no flag = everything).
func runInstall(cfg config.Config, args []string) int {
	want := map[string]bool{}
	for _, a := range args {
		want[a] = true
	}
	all := len(args) == 0 || want["--all"]
	bin := binPath()
	rc := 0
	if all || want["--config"] {
		p := config.ConfigFile()
		switch written, err := config.WriteTemplate(p); {
		case err != nil:
			fmt.Fprintln(os.Stderr, "config:", err)
			rc = 1
		case written:
			fmt.Println("config: wrote defaults to", p, "(all keys commented; edit and `flok reload`)")
		default:
			fmt.Println("config: keeping existing", p)
		}
	}
	if all || want["--claude"] {
		p := claudeSettingsPath()
		changed, err := install.ClaudeSettings(p, bin)
		switch {
		case err != nil:
			fmt.Fprintln(os.Stderr, "claude hooks:", err)
			rc = 1
		case changed:
			fmt.Println("claude hooks: installed in", p, "(restart running Claude sessions to activate)")
		default:
			fmt.Println("claude hooks: already installed in", p)
		}
	}
	if all || want["--copilot"] {
		p := copilotHooksPath()
		if _, err := os.Stat(filepath.Dir(filepath.Dir(p))); err != nil && !want["--copilot"] {
			fmt.Println("copilot hooks: skipped (no ~/.copilot; run `install --copilot` to force)")
		} else {
			changed, err := install.CopilotHooks(p, bin)
			switch {
			case err != nil:
				fmt.Fprintln(os.Stderr, "copilot hooks:", err)
				rc = 1
			case changed:
				fmt.Println("copilot hooks: written to", p)
			default:
				fmt.Println("copilot hooks: already installed in", p)
			}
		}
	}
	if all || want["--tmux"] {
		fmt.Println("tmux: paste this BELOW the tpm `run` line of your tmux.conf, then `prefix r`:")
		fmt.Println()
		fmt.Print(install.TmuxSnippet(bin))
	}
	return rc
}
