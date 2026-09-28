package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// RequestsDir holds one-shot commands for the running sidebar, one JSON file each, written by
// `flok goto beta:%12`, `flok jump` and `flok host front` when the target is on a remote host:
// the sidebar owns the ssh channels, a one-shot process cannot open one per keypress. The
// sidebar's store watcher sees the file land and drains the directory.
const RequestsDir = "requests"

// Request is one command for the sidebar.
type Request struct {
	Cmd  string    `json:"cmd"`            // goto | front | reconnect | a flok key command
	Host string    `json:"host,omitempty"` // "" = local
	Pane string    `json:"pane,omitempty"` // goto: the pane on that host
	At   time.Time `json:"at"`
}

// WriteRequest files a request (temp + rename, so the sidebar never reads half a file).
func (s *Store) WriteRequest(r Request) error {
	dir := filepath.Join(s.Dir, RequestsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if r.At.IsZero() {
		r.At = time.Now()
	}
	return writeJSON(filepath.Join(dir, fmt.Sprintf("%d.json", r.At.UnixNano())), r)
}

// DrainRequests removes and returns the pending requests in the order they were filed; one
// older than maxAge is dropped (the sidebar was not running when it was filed, acting on it
// now would surprise).
func (s *Store) DrainRequests(now time.Time, maxAge time.Duration) []Request {
	files, _ := filepath.Glob(filepath.Join(s.Dir, RequestsDir, "*.json"))
	sort.Strings(files)
	var out []Request
	for _, f := range files {
		data, err := os.ReadFile(f)
		_ = os.Remove(f)
		if err != nil {
			continue
		}
		var r Request
		if json.Unmarshal(data, &r) != nil || r.Cmd == "" || now.Sub(r.At) > maxAge {
			continue
		}
		out = append(out, r)
	}
	return out
}
