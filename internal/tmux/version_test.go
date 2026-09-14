package tmux

import "testing"

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]struct {
		maj, min int
		known    bool
	}{
		"tmux 2.7\n":       {2, 7, true},
		"tmux 3.2a":        {3, 2, true},
		"tmux 3.3a":        {3, 3, true},
		"3.4":              {3, 4, true},
		"tmux next-3.6":    {3, 6, true},
		"tmux 3.7c":        {3, 7, true},
		"tmux openbsd-7.4": {7, 4, true},
		"garbage":          {99, 0, false},
	} {
		v := ParseVersion(in)
		if v.Major != want.maj || v.Minor != want.min || v.Known != want.known {
			t.Errorf("%q: got %+v want %+v", in, v, want)
		}
	}
	if !ParseVersion("tmux 3.2a").AtLeast(3, 2) || ParseVersion("tmux 3.2a").AtLeast(3, 3) || !ParseVersion("tmux 2.7").AtLeast(2, 7) {
		t.Fatal("AtLeast")
	}
}

func TestFeaturesByVersion(t *testing.T) {
	el8 := FeaturesFor(ParseVersion("tmux 2.7"))
	if el8.PaneOptions || el8.Popup || el8.ExtendedKeys || el8.Passthrough || el8.FocusHooks || el8.EscapedOutput || el8.WindowSizeLatest {
		t.Fatalf("2.7 must have none of the 3.x features: %+v", el8)
	}
	el9 := FeaturesFor(ParseVersion("tmux 3.2a"))
	if !el9.PaneOptions || !el9.Popup || !el9.ExtendedKeys || !el9.TerminalFeatures || !el9.BorderLines || !el9.WindowSizeLatest {
		t.Fatalf("3.2a must have the 3.0-3.2 features: %+v", el9)
	}
	if el9.Passthrough || el9.PopupBorder || el9.BorderIndicators || el9.FocusHooks || el9.ResizedHook || el9.EscapedOutput || el9.ExtendedKeysFormat {
		t.Fatalf("3.2a must lack the 3.3+ features: %+v", el9)
	}
	el10 := FeaturesFor(ParseVersion("tmux 3.3a"))
	if !el10.Passthrough || !el10.PopupBorder || !el10.FocusHooks || el10.EscapedOutput || el10.ExtendedKeysFormat {
		t.Fatalf("3.3a: %+v", el10)
	}
	u := FeaturesFor(ParseVersion("tmux 3.4"))
	if !u.EscapedOutput || u.ExtendedKeysFormat {
		t.Fatalf("3.4: %+v", u)
	}
	mac := FeaturesFor(ParseVersion("tmux 3.7c"))
	if len(mac.Degraded()) != 0 {
		t.Fatalf("3.7 must not be degraded: %v", mac.Degraded())
	}
	if n := len(el8.Degraded()); n < 5 {
		t.Fatalf("2.7 degraded list too short: %v", el8.Degraded())
	}
}

func TestDecode(t *testing.T) {
	esc := `P\037%2\037tab\011x\037bell\a`
	if got := Decode(esc, false); got != esc {
		t.Fatalf("no decoding below 3.4: %q", got)
	}
	want := "P\x1f%2\x1ftab\tx\x1fbell\a"
	if got := Decode(esc, true); got != want {
		t.Fatalf("decode: %q want %q", got, want)
	}
	if got := Decode(`end\`, true); got != `end\` {
		t.Fatalf("trailing backslash kept: %q", got)
	}
	if got := Decode(`a\zb`, true); got != `a\zb` { // unknown escape: left alone
		t.Fatalf("unknown escape: %q", got)
	}
}
