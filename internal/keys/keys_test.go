package keys

import "testing"

const sample = `bind-key    -T prefix Space   next-layout
bind-key    -T prefix ?       list-keys -N
bind-key    -T prefix r       source-file /Users/jaro/.tmux.conf \; display-message "Config Reloaded!"
bind-key    -T prefix y       set-window-option synchronize-panes \; display-message "sync-panes #{?pane_synchronized,ON,OFF}"
bind-key -r -T prefix Up      select-pane -U
bind-key -r -T prefix M-Up    resize-pane -U 5
bind-key    -T prefix Y       run-shell -b /Users/jaro/.config/tmux/plugins/tmux-yank/scripts/copy_line.sh
bind-key    -T prefix a       run-shell -b "/Users/jaro/.local/bin/flok next --client '#{client_tty}'"
bind-key    -T prefix |       split-window -h -c "#{pane_current_path}"
bind-key    -T prefix 3       select-window -t :=3
bind-key    -T prefix \"      split-window
`

const rootSample = `bind-key  -T root MouseDown1Pane            select-pane -t = \; send-keys -M
bind-key  -T root C-h                       if-shell "ps -o state= -o comm= -t '#{pane_tty}' | grep -iqE '^[^TXZ ]+ +(\\S+\\/)?g?(view|n?vim?x?)(diff)?$'" "send-keys C-h" "select-pane -L"
`

const notesSample = `C-a Space   Select next layout
C-a ?       List key bindings
C-a Up      Select the pane above the active pane
`

func TestParseAndLabel(t *testing.T) {
	bs := ParseListKeys(sample)
	if len(bs) != 11 {
		t.Fatalf("parsed %d bindings", len(bs))
	}
	if bs[2].Key != "r" || bs[2].Command != `source-file /Users/jaro/.tmux.conf \; display-message "Config Reloaded!"` {
		t.Fatalf("raw command lost: %+v", bs[2])
	}
	if !bs[4].Repeat || bs[4].Key != "Up" || bs[4].Table != "prefix" {
		t.Fatalf("repeat flag: %+v", bs[4])
	}
	notes := ParseNotes(notesSample, "C-a", true)
	if notes["Space"] != "Select next layout" || notes["Up"] == "" {
		t.Fatalf("notes: %v", notes)
	}
	for i := range bs {
		bs[i].Note = notes[bs[i].Key]
	}
	want := map[string]string{
		"Space": "Select next layout",
		"?":     "List key bindings",
		"r":     "reload config",
		"y":     "toggle sync panes",
		"Up":    "Select the pane above the active pane",
		"M-Up":  "resize up 5",
		"Y":     "tmux-yank: copy line",
		"a":     "next agent",
		"|":     "split right",
		"3":     "window 3",
		"\"":    "split below",
	}
	for _, b := range bs {
		if got := Label(b, nil); got != want[b.Key] {
			t.Errorf("key %s: label %q, want %q", b.Key, got, want[b.Key])
		}
	}
	root := ParseListKeys(rootSample)
	if l := Label(root[1], nil); l != "pane left (vim-aware)" {
		t.Errorf("vim-aware label %q", l)
	}
	secs := Organize(append(bs, root...), "C-a", "/x/flok", nil, false)
	names := []string{}
	for _, s := range secs {
		names = append(names, s.Name)
	}
	if len(secs) != 4 || names[0] != "flok" || names[1] != "prefix C-a" || names[2] != "no prefix" || names[3] != "tmux-yank" {
		t.Fatalf("sections %v", names)
	}
	if secs[2].Bindings[0].Key != "C-h" || len(secs[2].Bindings) != 1 {
		t.Fatalf("mouse rows must be hidden: %+v", secs[2].Bindings)
	}
	if secs[1].Bindings[0].Note != "" || secs[1].Bindings[len(secs[1].Bindings)-1].Note == "" {
		t.Fatalf("user bindings must sort before stock ones: %+v", secs[1].Bindings)
	}
	if l := Label(Binding{Command: "select-pane -L"}, map[string]string{"select-pane -L": "west"}); l != "west" {
		t.Errorf("override: %q", l)
	}
	for _, k := range []string{"MouseDown1Pane", "M-MouseDown3Status", "C-WheelUpPane", "DoubleClick1Pane"} {
		if !IsMouse(k) {
			t.Errorf("%s should be a mouse key", k)
		}
	}
	if IsMouse("M-Up") || IsMouse("C-h") {
		t.Error("modifier keys are not mouse keys")
	}
}

// tmux 3.4 has no notes for its < and > menu bindings; their display-menu commands must not be
// shown raw (they mention Split, Kill and more, which also confuses the help filter).
func TestLabelDisplayMenu(t *testing.T) {
	win := `display-menu -T "#[align=centre]#{window_index}:#{window_name}" -x W -y W "Swap Left" l { swap-window -t :-1 } '' Kill X { kill-window }`
	pane := `display-menu -T "#[align=centre]#{pane_index} (#{pane_id})" -x P -y P "Horizontal Split" h { split-window -h } '' Kill X { kill-pane }`
	if l := Label(Binding{Command: win}, nil); l != "window menu" {
		t.Fatalf("window menu label: %q", l)
	}
	if l := Label(Binding{Command: pane}, nil); l != "pane menu" {
		t.Fatalf("pane menu label: %q", l)
	}
	if l := Label(Binding{Command: `display-menu -T "x" "Item" i { list-keys }`}, nil); l != "menu" {
		t.Fatalf("generic menu label: %q", l)
	}
}
