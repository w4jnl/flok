package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// LinkJoinedFile is what `flok link join` leaves in the state dir: this flok's own key to the
// relay and what it joined as. It stands in for [link] url, token and pin in config.toml, and
// wins over them when both are there (a join is the later, deliberate act); `flok link leave`
// removes it. Mode 0600: the key is this user's alone.
const LinkJoinedFile = "link.json"

// LinkFile is the record of a join.
type LinkFile struct {
	Relay      string    `json:"relay"`                 // the relay as the app addresses it (https://flok.example.net)
	Instance   string    `json:"instance"`              // this flok's id on the relay
	Name       string    `json:"name"`                  // the name it joined under
	Tenant     string    `json:"tenant"`                // the tenant's name
	Token      string    `json:"token"`                 // this flok's key
	Pin        string    `json:"pin,omitempty"`         // the relay's pin: the link goes encrypted
	ApprovedBy string    `json:"approved_by,omitempty"` // who let it in
	Joined     time.Time `json:"joined"`
}

// LoadLink reads the join record; false without one (or one that does not parse).
func (s *Store) LoadLink() (LinkFile, bool) {
	data, err := os.ReadFile(filepath.Join(s.Dir, LinkJoinedFile))
	if err != nil {
		return LinkFile{}, false
	}
	var l LinkFile
	if json.Unmarshal(data, &l) != nil || l.Relay == "" || l.Token == "" {
		return LinkFile{}, false
	}
	return l, true
}

// SaveLink writes the join record (temp + rename, 0600).
func (s *Store) SaveLink(l LinkFile) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.Dir, LinkJoinedFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RemoveLink forgets the join record; no record is no error.
func (s *Store) RemoveLink() error {
	err := os.Remove(filepath.Join(s.Dir, LinkJoinedFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// LinkSettings is the relay link as flok uses it: a join record's relay, key and pin when there
// is one, else config.toml's [link] url, token and pin as given.
type LinkSettings struct {
	URL, Token, Pin string
	Joined          *LinkFile // the join record, when the settings come from one
	ConfigURL       string    // [link] url as the config has it, for a note when both are set
}

// LinkSettings resolves the link: the join record wins over the config's [link] values.
func (s *Store) LinkSettings(url, token, pin string) LinkSettings {
	if l, ok := s.LoadLink(); ok {
		return LinkSettings{URL: l.Relay, Token: l.Token, Pin: l.Pin, Joined: &l, ConfigURL: url}
	}
	return LinkSettings{URL: url, Token: token, Pin: pin, ConfigURL: url}
}
