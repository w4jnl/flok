package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/w4jnl/flok/internal/claudereg"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/install"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/notify"
	"github.com/w4jnl/flok/internal/rules"
	"github.com/w4jnl/flok/internal/tmux"
)

type check struct {
	level string // ok | warn | fail
	text  string
}

// runDoctor prints a health report and exits 1 when something is broken.
func runDoctor(cfg config.Config) int {
	var out []check
	add := func(level, format string, a ...any) { out = append(out, check{level, fmt.Sprintf(format, a...)}) }

	// tmux: flok runs on 2.7 (RHEL 8) and newer; 3.3 has everything, older versions lose the
	// features listed by tmux.Features.Degraded (RHEL 9 ships 3.2a)
	ver := tmux.DetectVersion("")
	feat := tmux.FeaturesFor(ver)
	switch {
	case ver.Raw == "unknown":
		add("fail", "tmux not found in PATH")
	case !ver.Known:
		add("warn", "tmux version %q not recognised; assuming a current tmux", ver.Raw)
	case !ver.AtLeast(tmux.Floor.Major, tmux.Floor.Minor):
		add("fail", "tmux %s is too old: flok needs %s or newer", ver, tmux.Floor)
	default:
		add("ok", "tmux %s", ver)
		for _, d := range feat.Degraded() {
			add("warn", "tmux %s: %s", ver, d)
		}
	}
	inner := tmux.NewLocal(cfg.Inner.Socket).SetVersion(ver)
	if _, err := inner.Run("list-sessions"); err != nil {
		add("warn", "inner tmux server (%s) not running; `up` starts one", inner.Label())
	} else {
		add("ok", "inner tmux server %s reachable", inner.Label())
	}

	// binary + tmux.conf
	bin := binPath()
	add("ok", "binary %s", bin)
	if conf := findTmuxConf(); conf != "" {
		if data, err := os.ReadFile(conf); err == nil && strings.Contains(string(data), "flok") {
			add("ok", "tmux.conf snippet present in %s", conf)
		} else {
			add("warn", "no flok bindings in %s (run `flok install --tmux` and paste below the tpm line)", conf)
		}
	}

	// hooks
	if missing := install.ClaudeMissing(claudeSettingsPath()); len(missing) == 0 {
		add("ok", "Claude Code hooks installed (%s)", claudeSettingsPath())
	} else {
		add("warn", "Claude Code hooks missing for %s (run `flok install --claude`)", strings.Join(missing, ", "))
	}
	if data, err := os.ReadFile(claudeSettingsPath()); err == nil && strings.Contains(string(data), `"defaultMode": "auto"`) {
		add("ok", "Claude defaultMode is auto: few permission prompts; blocked comes mostly from questions")
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(copilotHooksPath()))); err == nil {
		if _, err := os.Stat(copilotHooksPath()); err == nil {
			add("ok", "Copilot CLI hooks installed (%s)", copilotHooksPath())
		} else {
			add("warn", "Copilot CLI hooks missing (run `flok install --copilot`)")
		}
	}
	if bin, err := claudereg.Binary(); err != nil {
		add("warn", "%v; registry fallback disabled", err)
	} else if entries, err := claudereg.List(3 * time.Second); err != nil {
		add("warn", "`%s agents --json` failed (%v); registry fallback disabled", bin, err)
	} else {
		add("ok", "`claude agents --json` works via %s (%d sessions)", bin, len(entries))
	}

	// sounds
	if cfg.Sounds.Enabled {
		player := notify.Player{Command: cfg.Sounds.Command}
		switch found := notify.Detect(nil); {
		case cfg.Sounds.Command != "":
			add("ok", "sound player: [sounds] command = %q", cfg.Sounds.Command)
		case found != "":
			add("ok", "sound player: %s", found)
		case cfg.Sounds.Bell == "never":
			add("warn", "no sound player found (afplay, mpv, ffplay, pw-play, paplay, play) and bell = never: sounds will not play")
		default:
			add("ok", "no sound player found: the terminal bell rings instead (bell = auto)")
		}
		add("ok", "bell: %s", notify.BellMode(player, cfg.Sounds.Bell))
		if cfg.Sounds.Bell != "never" {
			if rt, err := launcher.ReadRuntime(); err == nil && rt.InnerClientTTY == "" {
				add("warn", "bell: runtime.json has no inner client tty yet (the sidebar records it on start)")
			}
		}
		files := notify.Resolve(config.StateDir(), map[string]string{"done": cfg.Sounds.Done, "blocked": cfg.Sounds.Blocked, "error": cfg.Sounds.Error})
		for kind, f := range files {
			if _, err := os.Stat(f); err != nil {
				add("warn", "sound file for %s missing: %s", kind, f)
			}
		}
		src := "bundled herdr sounds"
		if cfg.Sounds.Done != "" || cfg.Sounds.Blocked != "" || cfg.Sounds.Error != "" {
			src = "custom sound files"
		}
		add("ok", "sounds enabled (player=%s, volume %.1f, %s)", cfg.Sounds.Player, cfg.Sounds.Volume, src)
	} else {
		add("ok", "sounds disabled")
	}

	// state dir + manifests
	dir := config.StateDir()
	if f, err := os.CreateTemp(dir, ".doctor-*"); err != nil {
		add("fail", "state dir %s not writable: %v", dir, err)
	} else {
		f.Close()
		os.Remove(f.Name())
		add("ok", "state dir %s", dir)
	}
	set := rules.Load(cfg.Agents.ManifestDir, cfg.Agents.UseHerdrCache)
	add("ok", "detection manifests: %s", strings.Join(set.IDs(), ", "))
	for _, p := range set.Problems {
		add("warn", "manifest: %s", p)
	}

	// outer
	rt, err := launcher.ReadRuntime()
	if err != nil {
		add("ok", "outer session not running (start with `flok up`)")
	} else {
		outer := tmux.NewLocal(rt.OuterSocket).SetVersion(ver)
		if dead, err := tmux.Display(outer, rt.SidebarPane, "#{pane_dead}"); err != nil {
			add("warn", "runtime.json refers to a missing outer pane; run `flok down` then `up`")
		} else if dead == "1" {
			add("fail", "sidebar pane is dead (see its output; `up` respawns it)")
		} else {
			add("ok", "outer %s running, sidebar pane %s", outer.Label(), rt.SidebarPane)
		}
		if tty, err := tmux.Display(outer, rt.RightPane, "#{pane_tty}"); err == nil {
			feats, _ := inner.Run("list-clients", "-F", "#{client_tty}"+tmux.Sep+"#{client_termfeatures}")
			found := false
			for _, line := range strings.Split(tmux.Decode(feats, feat.EscapedOutput), "\n") {
				f := strings.Split(line, tmux.Sep)
				if len(f) == 2 && f[0] == tty {
					found = true
					if strings.Contains(f[1], "RGB") {
						add("ok", "inner client %s advertises RGB (truecolor)", tty)
					} else {
						add("warn", "inner client %s lacks RGB; add `set -as terminal-features ',tmux-256color:RGB'` to the inner tmux.conf", tty)
					}
				}
			}
			if !found {
				add("warn", "no inner client on %s (right pane detached?)", tty)
			}
		}
	}

	// menu bar companion
	if cfg.Bar.Enabled && runtime.GOOS != "darwin" {
		add("warn", "[bar] enabled is ignored: the menu bar companion is macOS only (flok is the sidebar alone here)")
	} else if cfg.Bar.Enabled {
		if path, err := launcher.BarBinary(bin); err != nil {
			add("warn", "[bar] enabled but flok-bar not found next to %s or on PATH", bin)
		} else if pid, running := launcher.BarRunning(); running {
			add("ok", "menu bar: flok-bar running (pid %d, %s)", pid, path)
		} else {
			add("ok", "menu bar: flok-bar at %s (starts with `flok up`)", path)
		}
		if rt, err := launcher.ReadRuntime(); err == nil && rt.TerminalApp != "" {
			add("ok", "menu bar: click-to-return targets %s via %s", rt.TerminalApp, cfg.Bar.Focus)
		}
	} else {
		add("ok", "menu bar disabled ([bar] enabled = false)")
	}

	rc := 0
	for _, c := range out {
		mark := map[string]string{"ok": "ok  ", "warn": "warn", "fail": "FAIL"}[c.level]
		fmt.Printf("%s  %s\n", mark, c.text)
		if c.level == "fail" {
			rc = 1
		}
	}
	return rc
}

func findTmuxConf() string {
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".config", "tmux", "tmux.conf"), filepath.Join(home, ".tmux.conf")} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
