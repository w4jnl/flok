package notify

import (
	"errors"
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
	if got := Detect(lookOnly("mpv", "pw-play")); got != "pw-play" {
		t.Fatalf("pw-play should win over mpv, got %q", got)
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
		"mpv":     "mpv --no-video --really-quiet --volume=60 /s/done.mp3",
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
