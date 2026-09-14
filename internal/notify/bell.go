package notify

import (
	"errors"
	"os"
	"strings"
	"syscall"
)

// Bell rings the terminal bell by writing BEL into a pty that tmux reads as pane output: the
// outer server forwards it to its client (Ghostty, iTerm, an ssh session …), so it works on
// hosts without any audio stack. Resolve returns the pty path — normally the inner client's
// tty recorded in runtime.json, which is the outer's work pane — and is called lazily because
// the hook process must stay fast when nothing rings.
type Bell struct {
	Resolve func() string
}

func (b Bell) Play(string) error {
	tty := ""
	if b.Resolve != nil {
		tty = b.Resolve()
	}
	if tty == "" {
		return errors.New("bell: no tty to ring (is flok up?)")
	}
	f, err := os.OpenFile(tty, os.O_WRONLY|syscall.O_NOCTTY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write([]byte{7})
	return err
}

// Multi plays through every sounder; it fails only when all of them fail.
type Multi []Sounder

func (m Multi) Play(kind string) error {
	var last error
	ok := false
	for _, s := range m {
		if err := s.Play(kind); err != nil {
			last = err
		} else {
			ok = true
		}
	}
	if ok || last == nil {
		return nil
	}
	return last
}

// Available reports whether a custom command or a known player is there to play files.
func (p Player) Available() bool { return strings.TrimSpace(p.Command) != "" || Detect(p.look) != "" }

// Compose picks the sounder for a [sounds] bell mode: "never" plays files only, "always" plays
// files and rings, "auto" (the default) rings only when no player is available — which is what
// makes a headless host over ssh alert the local terminal with no configuration.
func Compose(p Player, bell Bell, mode string) Sounder {
	switch strings.TrimSpace(mode) {
	case "never":
		return p
	case "always":
		return Multi{p, bell}
	}
	if p.Available() {
		return p
	}
	return bell
}

// BellMode describes the effective setup for doctor.
func BellMode(p Player, mode string) string {
	switch strings.TrimSpace(mode) {
	case "never":
		return "never"
	case "always":
		return "always (terminal bell in addition to the sound)"
	}
	if p.Available() {
		return "auto (a sound player is available, the bell is not used)"
	}
	return "auto: terminal bell (no sound player found)"
}
