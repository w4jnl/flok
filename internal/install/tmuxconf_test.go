package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/w4jnl/flok/internal/tmux"
)

func TestTmuxConfRendersPathsAndNothingPersonal(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	s := TmuxConf("/home/u/.config/tmux/tmux.conf", "/home/u/.config/tmux/plugins", "/opt/x/flok")
	for _, want := range []string{
		"bind    r source-file ~/.config/tmux/tmux.conf",
		`set-environment -g TMUX_PLUGIN_MANAGER_PATH "~/.config/tmux/plugins"`,
		"run '~/.config/tmux/plugins/tpm/tpm'",
		"git clone -q https://github.com/tmux-plugins/tpm ~/.config/tmux/plugins/tpm",
		`set -g @resurrect-hook-post-save-layout "'/opt/x/flok' resurrect save"`,
		"set -g @plugin 'dracula/tmux'",
		"set -g  prefix C-a",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, never := range []string{"rgsender", "jaro", "/opt/homebrew", "TMUX -", "bind N ", "bind-key a ", "{{", "reattach-to-user-namespace"} {
		if strings.Contains(s, never) {
			t.Errorf("contains %q", never)
		}
	}
	if strings.Count(s, "mode-keys vi") != 1 {
		t.Errorf("mode-keys set %d times", strings.Count(s, "mode-keys vi"))
	}
	// a path outside home stays absolute
	if s := TmuxConf("/etc/tmux/tmux.conf", "/srv/plugins", "/opt/x/flok"); !strings.Contains(s, "source-file /etc/tmux/tmux.conf") || !strings.Contains(s, "run '/srv/plugins/tpm/tpm'") {
		t.Error("absolute paths outside home")
	}
}

func TestTmuxConfPathsAndFind(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if c, p := TmuxConfPath(tmux.ParseVersion("3.4")); c != filepath.Join(home, ".config", "tmux", "tmux.conf") || p != filepath.Join(home, ".config", "tmux", "plugins") {
		t.Fatalf("3.4: %s %s", c, p)
	}
	if c, p := TmuxConfPath(tmux.ParseVersion("2.7")); c != filepath.Join(home, ".tmux.conf") || p != filepath.Join(home, ".tmux", "plugins") {
		t.Fatalf("2.7: %s %s", c, p)
	}
	if FindTmuxConf() != "" {
		t.Fatal("nothing there yet")
	}
	xdg := filepath.Join(home, "cfg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if c, _ := TmuxConfPath(tmux.ParseVersion("3.4")); c != filepath.Join(xdg, "tmux", "tmux.conf") {
		t.Fatalf("XDG_CONFIG_HOME: %s", c)
	}
	written, err := WriteTmuxConf(filepath.Join(xdg, "tmux", "tmux.conf"), filepath.Join(xdg, "tmux", "plugins"), "/x/flok")
	if err != nil || !written {
		t.Fatalf("write: %v %v", written, err)
	}
	if FindTmuxConf() != filepath.Join(xdg, "tmux", "tmux.conf") {
		t.Fatalf("find: %q", FindTmuxConf())
	}
	if again, err := WriteTmuxConf(filepath.Join(xdg, "tmux", "tmux.conf"), "", "/y/flok"); err != nil || again {
		t.Fatal("an existing file is kept")
	}
	if data, _ := os.ReadFile(filepath.Join(xdg, "tmux", "tmux.conf")); !strings.Contains(string(data), "'/x/flok' resurrect save") {
		t.Fatal("the first write stands")
	}
	// the legacy file is found when the XDG one is absent
	t.Setenv("XDG_CONFIG_HOME", "")
	_ = os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte("set -g mouse on\n"), 0o644)
	if FindTmuxConf() != filepath.Join(home, ".tmux.conf") {
		t.Fatalf("legacy: %q", FindTmuxConf())
	}
}
