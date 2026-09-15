package config

import (
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestTemplateParsesToDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if w, err := WriteTemplate(p); err != nil || !w {
		t.Fatalf("write: %v %v", w, err)
	}
	if w, _ := WriteTemplate(p); w {
		t.Fatal("second write must keep the existing file")
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("template is not valid TOML: %v", err)
	}
	if cfg.Sidebar.Width != Default().Sidebar.Width || cfg.Outer.Socket != "flok" || len(cfg.Agents.Enabled) != 2 {
		t.Fatalf("template must not change defaults: %+v", cfg.Sidebar)
	}
}

func TestThemePalettes(t *testing.T) {
	cfg := Default()
	if _, err := toml.Decode("[theme]\nmode = \"light\"\ncyan = \"#000001\"\n[theme.light]\ncyan = \"#000002\"\n", &cfg); err != nil {
		t.Fatal(err)
	}
	def := Default().Theme
	if cfg.Theme.Mode != "light" || cfg.Theme.Cyan != "#000001" || cfg.Theme.Light.Cyan != "#000002" || cfg.Theme.Light.BG != def.Light.BG || cfg.Theme.BG != def.BG {
		t.Fatalf("[theme] keys set the dark palette, [theme.light] the light one, the rest keeps defaults: %+v", cfg.Theme)
	}
	if !def.IsDark("") || !def.IsDark("dark") || def.IsDark("light") {
		t.Fatal("auto follows the record, unknown counts as dark")
	}
	def.Mode = "light"
	if def.IsDark("dark") {
		t.Fatal("mode light wins")
	}
	if Default().Theme.Resolve(false).Brand != "#12999D" || Default().Theme.Resolve(true).Brand != "#3FD0D4" {
		t.Fatal("resolve picks the palette")
	}
}
