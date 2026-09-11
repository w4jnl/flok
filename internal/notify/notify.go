// Package notify plays sounds. The hook process is short-lived, so playback is detached.
package notify

import (
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

// Afplay plays .aiff/.mp3 files with macOS afplay, detached from the caller.
type Afplay struct {
	Files  map[string]string // kind -> file
	Volume float64
}

func (a Afplay) Play(kind string) error {
	file := a.Files[kind]
	if file == "" {
		return fmt.Errorf("no sound configured for %q", kind)
	}
	vol := a.Volume
	if vol <= 0 || vol > 1 {
		vol = 0.6
	}
	cmd := exec.Command("/usr/bin/afplay", "-v", fmt.Sprintf("%.2f", vol), file)
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
