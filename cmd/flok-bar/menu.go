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
	item  *systray.MenuItem
	mu    sync.Mutex
	pane  string
	last  string // last title pushed to AppKit
	shown bool
}

type menuBar struct {
	cfg  config.Config
	dir  string
	flok string // the flok CLI used for clicks

	header, show, edit, reload, quit *systray.MenuItem
	slots                            []*slot

	mu        sync.Mutex
	snap      snapshot.Snapshot
	fresh     snapshot.Freshness
	frame     int
	dotIcon   bool
	goneSince time.Time
	lastTitle string
	lastHead  string
	showOn    bool
	cfgMtime  time.Time
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
	setTemplateIcon(icons.Flok, b.cfg.Bar.IconSize)
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
	b.edit = systray.AddMenuItem("Edit config…", "open ~/.config/flok/config.toml; the bar re-reads it, the sidebar needs Reload")
	b.reload = systray.AddMenuItem("Reload sidebar", "restart the sidebar pane to apply config.toml changes")
	systray.AddSeparator()
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
	if fi, err := os.Stat(config.ConfigFile()); err == nil && !fi.ModTime().Equal(b.cfgMtime) { // hot edits of [bar]
		if cfg, err := config.Load(""); err == nil {
			b.cfg, b.cfgMtime = cfg, fi.ModTime()
		}
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
	b.setTitle(bar.TitleRuns(s, f, frame, b.options()))
	if head := bar.Header(s, f); head != b.lastHead { // every AppKit call below is diffed: most
		b.header.SetTitle(head) // refreshes change nothing and must cost nothing
		b.lastHead = head
	}
	wantDot := f == snapshot.Fresh && bar.Pending(s) > 0
	b.mu.Lock()
	swap := wantDot != b.dotIcon
	b.dotIcon = wantDot
	b.mu.Unlock()
	if swap { // an icon can be set but never removed, so only switch between the two variants
		if wantDot {
			setTemplateIcon(icons.FlokDot, b.cfg.Bar.IconSize)
		} else {
			setTemplateIcon(icons.Flok, b.cfg.Bar.IconSize)
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
			if sl.shown {
				sl.item.Hide()
				sl.shown, sl.last = false, ""
			}
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
		title := mark + r.Label + "    " + r.Detail
		if title != sl.last {
			sl.item.SetTitle(title)
			sl.last = title
		}
		if !sl.shown {
			sl.item.Show()
			sl.shown = true
		}
	}
	if on := f == snapshot.Fresh; on != b.showOn || b.lastHead == "" {
		if on {
			b.show.Enable()
		} else {
			b.show.Disable()
		}
		b.showOn = on
	}
}

// setTitle pushes the title once per change: as coloured runs ([bar] color, the state palette
// via an attributed title) or as systray's plain title. Both cost one AppKit redraw.
func (b *menuBar) setTitle(runs []bar.Run) {
	key := bar.Join(runs)
	for _, r := range runs {
		key += "\x00" + r.Color
	}
	b.mu.Lock()
	changed := key != b.lastTitle
	b.lastTitle = key
	b.mu.Unlock()
	if !changed {
		return
	}
	if b.cfg.Bar.Color {
		setColoredTitle(runs)
	} else {
		systray.SetTitle(bar.Join(runs))
	}
}

// animate advances the spinner while an agent is working. Every frame redraws the status item
// (AppKit lays the menu bar out again), so the default is 2 fps; [bar] animate_ms tunes it.
func (b *menuBar) animate() {
	ms := b.cfg.Bar.AnimateMs
	if ms < 100 {
		ms = 500
	}
	tick := time.NewTicker(time.Duration(ms) * time.Millisecond)
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
			b.setTitle(bar.TitleRuns(s, f, frame, b.options()))
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
		case <-b.edit.ClickedCh:
			b.run("edit-config")
		case <-b.reload.ClickedCh:
			b.run("reload")
		case <-b.quit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

// run executes a flok subcommand detached from the bar.
func (b *menuBar) run(args ...string) {
	cmd := exec.Command(b.flok, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}

// goto_ runs `flok goto [pane]` detached; the CLI switches the client and focuses the terminal.
func (b *menuBar) goto_(pane string) {
	if pane != "" {
		b.run("goto", pane)
		return
	}
	b.run("goto")
}
