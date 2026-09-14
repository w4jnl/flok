package notify

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lookOnly(names ...string) func(string) (string, error) {
	return func(n string) (string, error) {
		for _, x := range names {
			if x == n {
				return "/usr/bin/" + n, nil
			}
		}
		return "", errors.New("not found")
	}
}

func TestDetectOrderAndArgs(t *testing.T) {
	if got := Detect(lookOnly("mpv", "pw-play")); got != "mpv" {
		t.Fatalf("mpv (decodes anything) should win over pw-play, got %q", got)
	}
	if got := Detect(lookOnly("afplay", "pw-play")); got != "afplay" {
		t.Fatalf("afplay first on macOS, got %q", got)
	}
	if got := Detect(lookOnly()); got != "" {
		t.Fatalf("nothing on PATH -> %q", got)
	}
	cases := map[string]string{
		"afplay":  "afplay -v 0.60 /s/done.mp3",
		"pw-play": "pw-play --volume=0.60 /s/done.mp3",
		"paplay":  "paplay --volume=39321 /s/done.mp3",
		"mpv":     "mpv --no-config --no-video --really-quiet --volume=60 /s/done.mp3",
		"ffplay":  "ffplay -nodisp -autoexit -loglevel quiet -volume 60 /s/done.mp3",
		"play":    "play -q -v 0.60 /s/done.mp3",
	}
	for name, want := range cases {
		if got := strings.Join(playerArgs(name, "/s/done.mp3", 0.6), " "); got != want {
			t.Errorf("%s: %q", name, got)
		}
	}
	if playerArgs("unknown", "/s/x", 0.6) != nil {
		t.Fatal("unknown player must yield nil")
	}
}

func TestPlayerArgvCommandAndFallback(t *testing.T) {
	p := Player{Volume: 0.6, look: lookOnly("mpv")}
	if got := strings.Join(p.argv("/s/done.mp3"), " "); !strings.HasPrefix(got, "mpv ") {
		t.Fatalf("detected player: %q", got)
	}
	p = Player{Volume: 2, Command: "myplay --vol {volume} {file}", look: lookOnly()}
	argv := p.argv("/s/it's done.mp3")
	if len(argv) != 3 || argv[0] != "/bin/sh" || argv[2] != `myplay --vol 0.60 '/s/it'\''s done.mp3'` {
		t.Fatalf("custom command: %q (out-of-range volume falls back to 0.6, file is shell-quoted)", argv)
	}
	p = Player{Files: map[string]string{"done": "/s/done.mp3"}, look: lookOnly()}
	if err := p.Play("done"); err == nil || !strings.Contains(err.Error(), "no sound player") {
		t.Fatalf("no player must be reported, got %v", err)
	}
	if err := p.Play("nope"); err == nil {
		t.Fatal("unknown kind must error")
	}
}

func TestBellAndCompose(t *testing.T) {
	tty := filepath.Join(t.TempDir(), "pty")
	if err := os.WriteFile(tty, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := Bell{Resolve: func() string { return tty }}
	if err := b.Play("done"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(tty); string(data) != "x\a" && string(data) != "\a" {
		t.Fatalf("bell must write BEL to the tty, got %q", data)
	}
	if err := (Bell{}).Play("done"); err == nil {
		t.Fatal("no tty must be an error")
	}
	none := func(string) (string, error) { return "", errors.New("missing") }
	some := func(string) (string, error) { return "/bin/x", nil }
	if _, ok := Compose(Player{look: none}, b, "auto").(Bell); !ok {
		t.Fatal("auto without a player rings the bell")
	}
	if _, ok := Compose(Player{look: some}, b, "auto").(Player); !ok {
		t.Fatal("auto with a player plays the file")
	}
	if _, ok := Compose(Player{look: none, Command: "true {file}"}, b, "auto").(Player); !ok {
		t.Fatal("a custom command counts as a player")
	}
	if m, ok := Compose(Player{look: some}, b, "always").(Multi); !ok || len(m) != 2 {
		t.Fatal("always plays both")
	}
	if _, ok := Compose(Player{look: none}, b, "never").(Player); !ok {
		t.Fatal("never keeps files only")
	}
	if !strings.Contains(BellMode(Player{look: none}, ""), "terminal bell") {
		t.Fatal("doctor wording")
	}
}
