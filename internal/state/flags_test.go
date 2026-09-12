package state

import "testing"

func TestMarkerFlags(t *testing.T) {
	s := New(t.TempDir())
	if !s.TerminalFocused() || s.SidebarHidden() {
		t.Fatal("missing markers mean focused and visible")
	}
	if err := s.SetTerminalFocus(false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSidebarHidden(true); err != nil {
		t.Fatal(err)
	}
	if s.TerminalFocused() || !s.SidebarHidden() {
		t.Fatal("markers must round-trip")
	}
	_ = s.SetTerminalFocus(true)
	_ = s.SetSidebarHidden(false)
	if !s.TerminalFocused() || s.SidebarHidden() {
		t.Fatal("markers must flip back")
	}
}
