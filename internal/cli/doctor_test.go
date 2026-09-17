package cli

import "testing"

func TestDoubleTmuxConfig(t *testing.T) {
	cases := map[string]bool{
		"/opt/homebrew/etc/tmux.conf,/Users/j/.tmux.conf,/Users/j/.config/tmux/tmux.conf": true,
		"/Users/j/.tmux.conf,/Users/j/.config/tmux/tmux.conf":                             true,
		"/Users/j/.config/tmux/tmux.conf":                                                 false,
		"/etc/tmux.conf,/home/j/.tmux.conf":                                               false,
		"":                                                                                false,
		"#{config_files}":                                                                 false, // tmux < 3.2 echoes the format
	}
	for in, want := range cases {
		if got := doubleTmuxConfig(in); got != want {
			t.Errorf("doubleTmuxConfig(%q) = %v, want %v", in, got, want)
		}
	}
}
