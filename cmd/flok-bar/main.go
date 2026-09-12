//go:build darwin

// Command flok-bar is flok's macOS menu bar companion: an icon with an animated state glyph
// and a pending badge, plus a dropdown of the active agents. It is a pure reader of the
// snapshot the sidebar publishes and forwards clicks to `flok goto`. It is the only cgo
// package in the module (fyne.io/systray).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"fyne.io/systray"

	"github.com/w4jnl/flok/internal/config"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("flok-bar", strings.TrimPrefix(version, "v"))
		return
	}
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "flok-bar: config:", err)
	}
	dir := config.StateDir()
	// One bar per user: a duplicate (flok up racing a manual start) is a no-op by design.
	lock, err := os.OpenFile(filepath.Join(dir, "flok-bar.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flok-bar:", err)
		os.Exit(1)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		fmt.Fprintln(os.Stderr, "flok-bar: already running")
		os.Exit(0)
	}
	defer lock.Close()
	// Started by hand (not by `flok up`): leave the pid where `flok down` and doctor look.
	if _, err := os.Stat(filepath.Join(dir, "flok-bar.pid")); err != nil {
		_ = os.WriteFile(filepath.Join(dir, "flok-bar.pid"), []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
	b := newBar(cfg, dir)
	systray.Run(b.onReady, b.onExit)
}
