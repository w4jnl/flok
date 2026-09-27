package poller

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/w4jnl/flok/internal/state"
)

// WatchStore pushes a (coalesced) signal whenever a hook record or seen mark changes, or the
// sidebar-hidden, terminal-theme or keep-awake marker flips. The state dir root also sees the
// sidebar's own snapshot.json writes; those are ignored by name.
func WatchStore(dir string, ch chan struct{}) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	dir = filepath.Clean(dir)
	_ = w.Add(filepath.Join(dir, "agents"))
	_ = w.Add(filepath.Join(dir, "seen"))
	_ = w.Add(dir)
	for ev := range w.Events {
		if !StoreEventWanted(dir, ev.Name) {
			continue
		}
		select {
		case ch <- struct{}{}:
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// StoreEventWanted filters fsnotify events: hook records and seen marks under agents/ and seen/,
// plus the sidebar-hidden marker in the root (un-hide must resume the spinner at once), the
// terminal-theme record and the keep-awake marker (`flok keep-awake` waits for the snapshot to
// confirm); everything else in the root (the sidebar's own snapshot.json temp+rename writes,
// terminal-focus, which the next poll reads anyway, events.log, pid files) is noise.
func StoreEventWanted(root, name string) bool {
	base := filepath.Base(name)
	if filepath.Dir(name) == root {
		return base == "sidebar-hidden" || base == "terminal-theme" || base == state.KeepAwakeFile
	}
	return strings.HasSuffix(base, ".json")
}
