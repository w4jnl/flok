package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/w4jnl/flok/internal/config"
)

// install --tmux-conf writes the starter once; without a flag and without a terminal (as in
// this test) install only mentions it.
func TestInstallTmuxConf(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("FLOK_CONFIG", filepath.Join(home, "config.toml"))
	t.Setenv("FLOK_STATE", filepath.Join(home, "state"))
	if rc := runInstall(config.Default(), []string{"--tmux-conf"}); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	matches, _ := filepath.Glob(filepath.Join(home, "*tmux*"))
	xdg, _ := filepath.Glob(filepath.Join(home, ".config", "tmux", "tmux.conf"))
	conf := append(matches, xdg...)
	if len(conf) != 1 {
		t.Fatalf("one file written, got %v", conf)
	}
	data, _ := os.ReadFile(conf[0])
	if !strings.Contains(string(data), "set -g  prefix C-a") || !strings.Contains(string(data), "resurrect save") {
		t.Fatalf("starter content: %.200s", data)
	}
	_ = os.WriteFile(conf[0], []byte("# mine\n"), 0o644)
	if rc := runInstall(config.Default(), []string{"--tmux-conf"}); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	if data, _ := os.ReadFile(conf[0]); string(data) != "# mine\n" {
		t.Fatal("an existing file is kept")
	}
	_ = os.Remove(conf[0])
	if rc := runInstall(config.Default(), nil); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	if _, err := os.Stat(conf[0]); err == nil {
		t.Fatal("no terminal: nothing written")
	}
}
