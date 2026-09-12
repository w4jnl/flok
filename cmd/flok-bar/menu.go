//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"fyne.io/systray"
	"github.com/fsnotify/fsnotify"

	"github.com/w4jnl/flok/assets/icons"
	"github.com/w4jnl/flok/internal/bar"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/snapshot"
)

// goneAfter is how long the bar stays after flok disappeared before quitting itself
// (FLOK_BAR_GONE_AFTER=<seconds> overrides it for tests).
var goneAfter = 30 * time.Second

type slot struct {
	item *systray.MenuItem
	mu   sync.Mutex
	pane string
}

type menuBar struct {
	cfg  config.Config
	dir  string
	flok string // the flok CLI used for clicks

	header, show, quit *systray.MenuItem
	slots              []*slot

	mu        sync.Mutex
	snap      snapshot.Snapshot
	fresh     snapshot.Freshness
	frame     int
	dotIcon   bool
	goneSince time.Time
	lastTitle string
}

func newBar(cfg config.Config, dir string) *menuBar {
	if v, err := strconv.Atoi(os.Getenv("FLOK_BAR_GONE_AFTER")); err == nil && v > 0 {
		goneAfter = time.Duration(v) * time.Second
	}
	return &menuBar{cfg: cfg, dir: dir, flok: flokPath(), fresh: snapshot.Gone}
}

// flokPath finds the CLI next to our own executable, else on PATH.
func flokPath() string {
	if exe, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(exe), "flok"); fileExists(p) {
			return p
		}
	}
	return "flok"
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func (b *menuBar) maxRows() int {
	if n := b.cfg.Bar.MaxRows; n > 0 {
		return n
	}
	return 16
}

func (b *menuBar) onReady() {
	systray.SetTemplateIcon(icons.Flok, icons.Flok)
	systray.SetTitle("–")
	systray.SetTooltip("flok")
	b.header = systray.AddMenuItem("flok", "")
	b.header.Disable()
	for i := 0; i < b.maxRows(); i++ {
		s := &slot{item: systray.AddMenuItem("", "")}
		s.item.Hide()
		b.slots = append(b.slots, s)
		go b.slotClicks(s)
	}
	systray.AddSeparator()
	b.show = systray.AddMenuItem("Show flok", "bring the flok terminal window to the front")
	b.quit = systray.AddMenuItem("Quit flok-bar", "flok itself keeps running")
	go b.staticClicks()
	go b.watch()
	go b.animate()
	b.refresh()
}

func (b *menuBar) onExit() {}

// watch re-reads the snapshot when the sidebar writes it, and every 2 s as a safety net.
func (b *menuBar) watch() {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	events := make(chan struct{}, 1)
	if w, err := fsnotify.NewWatcher(); err == nil && w.Add(b.dir) == nil {
		go func() {
			for ev := range w.Events {
				if filepath.Base(ev.Name) == snapshot.FileName || filepath.Base(ev.Name) == "runtime.json" {
					select {
					case events <- struct{}{}:
					default:
					}
				}
			}
		}()
	}
	for {
		select {
		case <-events:
			time.Sleep(50 * time.Millisecond) // let the rename settle
		case <-tick.C:
		}
		b.refresh()
	}
}

// refresh loads the snapshot and updates title, icon, header and rows.
func (b *menuBar) refresh() {
	now := time.Now()
	s, f := snapshot.Load(b.dir, now)
	if cfg, err := config.Load(""); err == nil { // hot edits of [bar]
		b.cfg = cfg
	}
	b.mu.Lock()
	b.snap, b.fresh = s, f
	if f == snapshot.Gone {
		if b.goneSince.IsZero() {
			b.goneSince = now
		} else if now.Sub(b.goneSince) > goneAfter {
			b.mu.Unlock()
			systray.Quit()
			return
		}
	} else {
		b.goneSince = time.Time{}
	}
	b.mu.Unlock()
	b.render(now)
}

func (b *menuBar) options() bar.Options {
	return bar.Options{Animate: b.cfg.Bar.Animate, Badge: b.cfg.Bar.Badge, MaxRows: b.maxRows()}
}

func (b *menuBar) render(now time.Time) {
	b.mu.Lock()
	s, f, frame := b.snap, b.fresh, b.frame
	b.mu.Unlock()
	b.setTitle(bar.Title(s, f, frame, b.options()))
	b.header.SetTitle(bar.Header(s, f))
	wantDot := f == snapshot.Fresh && bar.Pending(s) > 0
	b.mu.Lock()
	swap := wantDot != b.dotIcon
	b.dotIcon = wantDot
	b.mu.Unlock()
	if swap { // an icon can be set but never removed, so only switch between the two variants
		if wantDot {
			systray.SetTemplateIcon(icons.FlokDot, icons.FlokDot)
		} else {
			systray.SetTemplateIcon(icons.Flok, icons.Flok)
		}
	}
	rows := []bar.Row{}
	if f == snapshot.Fresh {
		rows = bar.Rows(s, now, len(b.slots))
	}
	for i, sl := range b.slots {
		if i >= len(rows) {
			sl.mu.Lock()
			sl.pane = ""
			sl.mu.Unlock()
			sl.item.Hide()
			continue
		}
		r := rows[i]
		mark := "  "
		if r.Attention {
			mark = "● "
		}
		sl.mu.Lock()
		sl.pane = r.PaneID
		sl.mu.Unlock()
		sl.item.SetTitle(mark + r.Label + "    " + r.Detail)
		sl.item.Show()
	}
	if f == snapshot.Fresh {
		b.show.Enable()
	} else {
		b.show.Disable()
	}
}

func (b *menuBar) setTitle(t string) {
	b.mu.Lock()
	changed := t != b.lastTitle
	b.lastTitle = t
	b.mu.Unlock()
	if changed {
		systray.SetTitle(t)
	}
}

// animate advances the spinner while an agent is working.
func (b *menuBar) animate() {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for range tick.C {
		b.mu.Lock()
		working := b.fresh == snapshot.Fresh && bar.Working(b.snap) && b.cfg.Bar.Animate
		if working {
			b.frame++
		}
		s, f, frame := b.snap, b.fresh, b.frame
		b.mu.Unlock()
		if working {
			b.setTitle(bar.Title(s, f, frame, b.options()))
		}
	}
}

func (b *menuBar) slotClicks(s *slot) {
	for range s.item.ClickedCh {
		s.mu.Lock()
		pane := s.pane
		s.mu.Unlock()
		if pane != "" {
			b.goto_(pane)
		}
	}
}

func (b *menuBar) staticClicks() {
	for {
		select {
		case <-b.show.ClickedCh:
			b.goto_("")
		case <-b.quit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

// goto_ runs `flok goto [pane]` detached; the CLI switches the client and focuses the terminal.
func (b *menuBar) goto_(pane string) {
	args := []string{"goto"}
	if pane != "" {
		args = append(args, pane)
	}
	cmd := exec.Command(b.flok, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}
