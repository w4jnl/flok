package config

import (
	"os"
	"path/filepath"
	"strings"
)

const appName = "flok"

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// ExpandHome replaces a leading "~" with the user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Join(home(), strings.TrimPrefix(p, "~"))
	}
	return p
}

// ConfigDir is $XDG_CONFIG_HOME/flok or ~/.config/flok.
func ConfigDir() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, appName)
	}
	return filepath.Join(home(), ".config", appName)
}

// ConfigFile is the TOML file, overridable with $FLOK_CONFIG.
func ConfigFile() string {
	if v := os.Getenv("FLOK_CONFIG"); v != "" {
		return v
	}
	return filepath.Join(ConfigDir(), "config.toml")
}

// StateDir is $XDG_STATE_HOME/flok or ~/.local/state/flok; it is created on demand.
func StateDir() string {
	var d string
	if v := os.Getenv("FLOK_STATE"); v != "" {
		d = v
	} else if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		d = filepath.Join(v, appName)
	} else {
		d = filepath.Join(home(), ".local", "state", appName)
	}
	_ = os.MkdirAll(d, 0o755)
	return d
}
