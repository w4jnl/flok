package state

import (
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// RepliesDir holds the sidebar's answers to the commands that ask the relay over the link:
// `flok phone` (pair, list, forget), `flok link` (approve, list, forget) and `flok tenant`. The
// command files a request with an id (Request.ID), the sidebar asks the relay and writes what
// came back to <id>.json here, and the command reads it. A pairing's second word, that a phone
// took the code, lands in the same file. The command removes its file; a sweep takes the rest.
const RepliesDir = "replies"

var requestIDShape = regexp.MustCompile(`^[0-9a-f]{12}$`)

// ValidRequestID says whether an id is one flok makes for a reply (12 hex digits), so a request
// can never name a file outside RepliesDir.
func ValidRequestID(id string) bool { return requestIDShape.MatchString(id) }

// ReplyPath is where the sidebar's answer to the request with this id goes.
func (s *Store) ReplyPath(id string) string {
	return filepath.Join(s.Dir, RepliesDir, id+".json")
}

// WriteReply writes the sidebar's answer to a request (temp + rename).
func (s *Store) WriteReply(id string, v any) error {
	if !ValidRequestID(id) {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, RepliesDir), 0o755); err != nil {
		return err
	}
	return writeJSON(s.ReplyPath(id), v)
}

// SweepReplies removes answers older than maxAge: a command that was interrupted leaves its
// file behind.
func (s *Store) SweepReplies(now time.Time, maxAge time.Duration) {
	files, _ := filepath.Glob(filepath.Join(s.Dir, RepliesDir, "*.json"))
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && now.Sub(st.ModTime()) > maxAge {
			_ = os.Remove(f)
		}
	}
}
