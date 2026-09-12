package config

import (
	"path/filepath"
	"testing"
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
