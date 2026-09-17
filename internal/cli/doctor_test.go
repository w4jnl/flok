package cli

import "testing"

func TestDoubleTmuxConfig(t *testing.T) {
	both := "/opt/homebrew/etc/tmux.conf,/Users/j/.tmux.conf,/Users/j/.config/tmux/tmux.conf"
	shim := "source-file ~/.config/tmux/tmux.conf\n"
	guarded := "# tmux < 3.1 does not read the XDG file\nif-shell 'tmux -V | grep -qE \"^tmux (1\\.|2\\.|3\\.0)\"' 'source-file ~/.config/tmux/tmux.conf'\n"
	block := "%if #{<:#{version},3.1}\nsource-file ~/.config/tmux/tmux.conf\n%endif\n"
	cases := []struct {
		name, files, conf string
		want              bool
	}{
		{"unconditional shim", both, shim, true},
		{"source alias with flag", both, "source -q ~/.config/tmux/tmux.conf\n", true},
		{"if-shell guarded shim", both, guarded, false},
		{"%if guarded shim", both, block, false},
		{"commented out", both, "# source-file ~/.config/tmux/tmux.conf\n", false},
		{"two unrelated configs", both, "set -g mouse on\n", false},
		{"only the XDG file", "/Users/j/.config/tmux/tmux.conf", shim, false},
		{"only the legacy file", "/etc/tmux.conf,/home/j/.tmux.conf", shim, false},
		{"tmux < 3.2 echoes the format", "#{config_files}", shim, false},
		{"empty", "", "", false},
	}
	for _, c := range cases {
		if got := doubleTmuxConfig(c.files, c.conf); got != c.want {
			t.Errorf("%s: doubleTmuxConfig = %v, want %v", c.name, got, c.want)
		}
	}
}
