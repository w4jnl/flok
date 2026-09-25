// Package cli dispatches flok subcommands.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/launcher"
)

// Version is stamped at build time: -ldflags "-X github.com/w4jnl/flok/internal/cli.Version=v0.1.0".
var Version = "dev"

const usageText = `flok — herdr-like agent sidebar for tmux

usage: flok <command>

  up          start (or re-attach) the sidebar + your tmux server in a nested outer session
              (--detach: create it without attaching)
  down        stop the outer session (your tmux server keeps running)
  keep-awake  [on|off|toggle|status]: keep the Mac awake with the display on while flok runs
              (off by default, no argument toggles; macOS only)
  sidebar     run the sidebar UI in the current pane (used by up)
  status      print sessions and agents once (--json for machine output)
  jump        switch the inner client to the newest agent needing input (else newest done)
  next, prev  cycle through agent panes in sidebar order        [--client <tty>]
  toggle      sidebar full width <-> rail;  hide: zoom the work area (sidebar takes no space)
  goto        [pane-id] [--no-focus]: switch to an agent pane and bring the terminal window
              to the front (used by the menu bar app flok-bar)
  edit-config open config.toml: in a new tmux window with [bar] editor, else the default app
  reload      restart the sidebar pane after editing config.toml (work pane untouched)
  focus       move the outer cursor into the sidebar pane
  keys        keybinds help (tmux popup); --print [--filter q] dumps it as text
  explain     show which screen-detection rules match agent panes (debugging)
  doctor      check tmux, hooks, sounds, manifests and the running outer session
  theme       [auto|dark|light]: show the terminal background flok detects, or switch the
              sidebar between its dark and light palette
  install     wire Claude Code / Copilot CLI hooks and print the tmux.conf snippet
              and write a commented default config.toml if none exists
              (--claude, --copilot, --tmux, --tmux-resurrect, --config; default excludes
              the opt-in tmux-resurrect integration)
  resurrect   tmux-resurrect integration (save <state-file>; called by its save hook)
  hook        hook receiver used by the agents (stdin JSON; never call by hand)
  completion  print a bash or zsh completion script (flok completion bash|zsh)
  version     print the version
`

func usage() { fmt.Fprint(os.Stderr, usageText) }

// Main runs the CLI and returns the exit code.
func Main(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "flok: config:", err)
		return 1
	}
	switch args[0] {
	case "up":
		detach := len(args) > 1 && (args[1] == "--detach" || args[1] == "-d")
		return report(launcher.Up(cfg, binPath(), detach))
	case "down":
		return report(launcher.Down(cfg))
	case "keep-awake":
		return runKeepAwake(cfg, args[1:])
	case "sidebar":
		return runSidebar(cfg)
	case "status":
		return runStatus(cfg, args[1:])
	case "hook":
		return runHook(cfg, args[1:])
	case "keys":
		return runKeys(cfg, args[1:])
	case "goto":
		return runGoto(cfg, args[1:])
	case "edit-config":
		return runEditConfig(cfg, args[1:])
	case "explain":
		return runExplain(cfg, args[1:])
	case "doctor":
		return runDoctor(cfg)
	case "theme":
		return runTheme(cfg, args[1:])
	case "jump", "next", "prev":
		return runNav(cfg, args[0], args[1:])
	case "toggle", "hide", "focus", "reload":
		return runLayout(cfg, args[0], args[1:])
	case "install":
		return runInstall(cfg, args[1:])
	case "resurrect":
		return runResurrect(cfg, args[1:])
	case "_attach-loop":
		return report(launcher.AttachLoop(cfg))
	case "_focus":
		return report(launcher.SetTerminalFocus(len(args) > 1 && args[1] == "1"))
	case "completion":
		return runCompletion(args[1:])
	case "version", "--version", "-V":
		if stdoutIsTerminal() { // the lockup for people; "flok X.Y.Z" for scripts and the brew test
			fmt.Println(Lockup(Version))
		} else {
			fmt.Println("flok", strings.TrimPrefix(Version, "v"))
		}
		return 0
	case "help", "-h", "--help":
		usage()
		return 0
	}
	fmt.Fprintf(os.Stderr, "flok: unknown command %q\n\n", args[0])
	usage()
	return 2
}

func report(err error) int {
	if err != nil {
		fmt.Fprintln(os.Stderr, "flok:", err)
		return 1
	}
	return 0
}

// binPath is the absolute path of this executable, used in hook configs and tmux bindings.
// Symlinks are kept on purpose: /opt/homebrew/bin/flok stays valid across `brew upgrade`,
// the Cellar path behind it does not. On Linux os.Executable is already resolved, so a Cellar
// path is mapped back to <prefix>/bin/flok when that link exists.
func binPath() string {
	p, err := os.Executable()
	if err != nil {
		return "flok"
	}
	if !filepath.IsAbs(p) {
		if a, err := filepath.Abs(p); err == nil {
			p = a
		}
	}
	if i := strings.Index(p, "/Cellar/"); i >= 0 {
		if link := filepath.Join(p[:i], "bin", filepath.Base(p)); fileExists(link) {
			return link
		}
	}
	return p
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
