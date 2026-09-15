package termtheme

import "testing"

func TestParse(t *testing.T) {
	dark, ok := Parse([]byte("\x1b]11;rgb:2828/2a2a/3636\x1b\\\x1b[?62;22c"))
	if !ok || dark.Theme != "dark" || dark.BG != "#282a36" {
		t.Fatalf("Dracula: %+v %v", dark, ok)
	}
	light, ok := Parse([]byte("noise\x1b]11;rgb:ff/fb/eb\x07"))
	if !ok || light.Theme != "light" || light.BG != "#fffbeb" {
		t.Fatalf("Alucard, 2-digit, BEL: %+v %v", light, ok)
	}
	if white, ok := Parse([]byte("\x1b]11;rgba:ffff/ffff/ffff/ffff\x07")); !ok || white.Theme != "light" {
		t.Fatalf("rgba: %+v %v", white, ok)
	}
	if _, ok := Parse([]byte("\x1b[?1;2c")); ok {
		t.Fatal("a DA1 answer alone is no background")
	}
}
