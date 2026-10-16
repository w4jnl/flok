package state

import (
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// PhoneDir holds the sidebar's answers to `flok phone` (pair, list, forget): the command files
// a request with an id (Request.ID), the sidebar asks the relay over its link and writes what
// came back to <id>.json here, and the command reads it. A pairing's second word, that a phone
// took the code, lands in the same file. The command removes its file; a sweep takes the rest.
const PhoneDir = "phone"

var requestIDShape = regexp.MustCompile(`^[0-9a-f]{12}$`)

// ValidRequestID says whether an id is one flok phone makes (12 hex digits), so a request can
// never name a file outside PhoneDir.
func ValidRequestID(id string) bool { return requestIDShape.MatchString(id) }

// PhoneReplyPath is where the sidebar's answer to the request with this id goes.
func (s *Store) PhoneReplyPath(id string) string {
	return filepath.Join(s.Dir, PhoneDir, id+".json")
}

// WritePhoneReply writes the sidebar's answer to a phone request (temp + rename).
func (s *Store) WritePhoneReply(id string, v any) error {
	if !ValidRequestID(id) {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, PhoneDir), 0o755); err != nil {
		return err
	}
	return writeJSON(s.PhoneReplyPath(id), v)
}

// SweepPhoneReplies removes answers older than maxAge: a command that was interrupted leaves its
// file behind.
func (s *Store) SweepPhoneReplies(now time.Time, maxAge time.Duration) {
	files, _ := filepath.Glob(filepath.Join(s.Dir, PhoneDir, "*.json"))
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && now.Sub(st.ModTime()) > maxAge {
			_ = os.Remove(f)
		}
	}
}
