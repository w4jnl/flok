package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeysMapDecodes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	_ = os.WriteFile(p, []byte("[keys]\nbind = \"all\"\n[keys.map]\nTab = \"last session\"\n\";\" = \"last pane\"\n"), 0o644)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Keys.Bind != "all" || cfg.Keys.Map["Tab"] != "last session" || cfg.Keys.Map[";"] != "last pane" || len(cfg.Keys.Map) != 2 {
		t.Fatalf("keys %+v", cfg.Keys)
	}
}
