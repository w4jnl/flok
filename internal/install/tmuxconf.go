package install

import (
	"bytes"
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/w4jnl/flok/internal/tmux"
)

// tmuxConfTmpl is the starter tmux.conf `flok install` offers to people who have none.
//
//go:embed tmux.conf.tmpl
var tmuxConfTmpl string

// FindTmuxConf returns the configuration file the user's tmux reads: the XDG one
// ($XDG_CONFIG_HOME/tmux/tmux.conf, ~/.config by default), else ~/.tmux.conf; "" when neither
// exists.
func FindTmuxConf() string {
	for _, p := range []string{xdgTmuxConf(), legacyTmuxConf()} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func homeDir() string { h, _ := os.UserHomeDir(); return h }

func xdgTmuxDir() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "tmux")
	}
	return filepath.Join(homeDir(), ".config", "tmux")
}

func xdgTmuxConf() string    { return filepath.Join(xdgTmuxDir(), "tmux.conf") }
func legacyTmuxConf() string { return filepath.Join(homeDir(), ".tmux.conf") }

// TmuxConfPath is where the starter goes and where its plugins live: the XDG file from tmux
// 3.1 on (older servers read only ~/.tmux.conf), the plugins next to it.
func TmuxConfPath(ver tmux.Version) (conf, plugins string) {
	if ver.AtLeast(3, 1) {
		return xdgTmuxConf(), filepath.Join(xdgTmuxDir(), "plugins")
	}
	return legacyTmuxConf(), filepath.Join(homeDir(), ".tmux", "plugins")
}

// tilde writes a path under the home directory with ~, so the file reads the same on every
// machine it is copied to (tmux and the shell expand it).
func tilde(p string) string {
	if h := homeDir(); h != "" && (p == h || strings.HasPrefix(p, h+string(filepath.Separator))) {
		return "~" + p[len(h):]
	}
	return p
}

// TmuxConf renders the starter: conf is the file's own path (prefix r reloads it), plugins the
// tpm directory, bin this flok (the tmux-resurrect hook).
func TmuxConf(conf, plugins, bin string) string {
	var b bytes.Buffer
	_ = template.Must(template.New("tmux.conf").Parse(tmuxConfTmpl)).Execute(&b, map[string]string{
		"Conf": tilde(conf), "Plugins": tilde(plugins), "Resurrect": strings.TrimSuffix(TmuxResurrectSnippet(bin), "\n")})
	return b.String()
}

// WriteTmuxConf writes the starter unless a file is there already (then (false, nil)).
func WriteTmuxConf(conf, plugins, bin string) (written bool, err error) {
	if _, err := os.Stat(conf); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(conf, []byte(TmuxConf(conf, plugins, bin)), 0o644)
}
