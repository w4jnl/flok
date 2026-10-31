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
	Cmd     string    `json:"cmd"`            // goto | front | reconnect | relink | phone | link | tenant | a flok key command
	Host    string    `json:"host,omitempty"` // "" = local
	Pane    string    `json:"pane,omitempty"` // goto: the pane on that host
	Session string    `json:"session,omitempty"`
	Kind    string    `json:"kind,omitempty"`   // last: session | window | pane | server // goto without a pane: the session on that host
	ID      string    `json:"id,omitempty"`     // phone, link, tenant: the reply file's name (ReplyPath)
	Op      string    `json:"op,omitempty"`     // phone: pair | list | forget; link: lookup | approve | list | forget; tenant: list | show | approve | remove
	Name    string    `json:"name,omitempty"`   // forget: the phone's or flok's name or id; tenant show | remove: the tenant's
	Code    string    `json:"code,omitempty"`   // link approve, tenant approve: the joining flok's code
	Tenant  string    `json:"tenant,omitempty"` // tenant rename: the new name
	At      time.Time `json:"at"`
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
// now would surprise). A request is claimed by a rename before it is read, so two drains at
// once (the store watcher's and the poll tick's) never both run it: a `flok link approve` run
// twice answered "approved already" to the one that asked.
func (s *Store) DrainRequests(now time.Time, maxAge time.Duration) []Request {
	files, _ := filepath.Glob(filepath.Join(s.Dir, RequestsDir, "*.json"))
	sort.Strings(files)
	var out []Request
	for _, f := range files {
		claimed := f + ".taken"
		if os.Rename(f, claimed) != nil {
			continue // another drain has it
		}
		data, err := os.ReadFile(claimed)
		_ = os.Remove(claimed)
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
