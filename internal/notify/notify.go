// Package notify plays sounds. The hook process is short-lived, so playback is detached.
package notify

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Sounder interface {
	Play(kind string) error
}

// players lists the command-line players flok knows, in detection order: afplay is macOS's
// own; on Linux pw-play (PipeWire) and paplay (PulseAudio) come with the desktop, mpv, ffplay
// and play (sox) are common installs. All of them decode mp3 on a current desktop
// (pw-play/paplay through libsndfile 1.1+); anything else goes through [sounds] command.
var players = []string{"afplay", "pw-play", "paplay", "mpv", "ffplay", "play"}

// playerArgs builds the argv for a known player: volume in [0,1] mapped to the player's scale.
func playerArgs(name, file string, vol float64) []string {
	switch name {
	case "afplay":
		return []string{name, "-v", fmt.Sprintf("%.2f", vol), file}
	case "pw-play":
		return []string{name, fmt.Sprintf("--volume=%.2f", vol), file}
	case "paplay":
		return []string{name, fmt.Sprintf("--volume=%d", int(vol*65536)), file}
	case "mpv":
		return []string{name, "--no-video", "--really-quiet", fmt.Sprintf("--volume=%d", int(vol*100)), file}
	case "ffplay":
		return []string{name, "-nodisp", "-autoexit", "-loglevel", "quiet", "-volume", fmt.Sprintf("%d", int(vol*100)), file}
	case "play":
		return []string{name, "-q", "-v", fmt.Sprintf("%.2f", vol), file}
	}
	return nil
}

// Detect returns the first known player found on PATH ("" when none). look is exec.LookPath
// unless a test injects one.
func Detect(look func(string) (string, error)) string {
	if look == nil {
		look = exec.LookPath
	}
	for _, name := range players {
		if _, err := look(name); err == nil {
			return name
		}
	}
	return ""
}

// Player plays sound files with an external command, detached from the caller (the hook
// process is short-lived). Command is an optional shell command with {file} and {volume}
// placeholders; empty means the first player Detect finds.
type Player struct {
	Files   map[string]string // kind -> file
	Volume  float64
	Command string
	look    func(string) (string, error)
}

// argv resolves the command line for one file (nil when no player is available).
func (p Player) argv(file string) []string {
	vol := p.Volume
	if vol <= 0 || vol > 1 {
		vol = 0.6
	}
	if cmd := strings.TrimSpace(p.Command); cmd != "" {
		cmd = strings.NewReplacer("{file}", shellQuote(file), "{volume}", fmt.Sprintf("%.2f", vol)).Replace(cmd)
		return []string{"/bin/sh", "-c", cmd}
	}
	if name := Detect(p.look); name != "" {
		return playerArgs(name, file, vol)
	}
	return nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (p Player) Play(kind string) error {
	file := p.Files[kind]
	if file == "" {
		return fmt.Errorf("no sound configured for %q", kind)
	}
	argv := p.argv(file)
	if argv == nil {
		return errors.New("no sound player found (afplay, pw-play, paplay, mpv, ffplay, play) and [sounds] command is empty")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	null, _ := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if null != nil {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// Noop is used with sounds disabled or when a sidebar elsewhere plays them.
type Noop struct{}

func (Noop) Play(string) error { return nil }

// Allowed enforces a per-key and a global minimum interval using stamp files, so that six
// agents finishing at once produce one sound and one agent cannot drum.
func Allowed(stateDir, key string, perKey, global time.Duration, now time.Time) bool {
	dir := filepath.Join(stateDir, "sounds")
	_ = os.MkdirAll(dir, 0o755)
	key = strings.NewReplacer("%", "", "/", "_", ":", "_").Replace(key)
	g, k := filepath.Join(dir, "global.stamp"), filepath.Join(dir, key+".stamp")
	if recent(g, global, now) || recent(k, perKey, now) {
		return false
	}
	touch(g, now)
	touch(k, now)
	return true
}

func recent(path string, within time.Duration, now time.Time) bool {
	fi, err := os.Stat(path)
	return err == nil && now.Sub(fi.ModTime()) < within
}

func touch(path string, now time.Time) {
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Close()
	}
	_ = os.Chtimes(path, now, now)
}
