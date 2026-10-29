package notes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// One machine imports a host's reports. The host remembers which flok claimed it (by the
// importer's instance name) and refuses another while the first has been seen within
// ClaimTTL; `flok host notes <name> on --take` takes it over at once.

const (
	ClaimFile  = "notes-importer.json"
	CursorFile = "notes-cursor"
	ClaimTTL   = 7 * 24 * time.Hour
)

// Importer is the host's record of who imports its reports.
type Importer struct {
	Name string    `json:"name"`
	Seen time.Time `json:"seen"`
}

// Claim records name as the importer of the reports in stateDir, unless another importer was
// seen within ClaimTTL and take is false. It returns the importer that holds the claim after
// the call and whether that is name.
func Claim(stateDir, name string, take bool, now time.Time) (string, bool) {
	p := filepath.Join(stateDir, ClaimFile)
	var cur Importer
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &cur)
	}
	if cur.Name != "" && cur.Name != name && !take && now.Sub(cur.Seen) < ClaimTTL {
		return cur.Name, false
	}
	data, _ := json.Marshal(Importer{Name: name, Seen: now})
	_ = os.WriteFile(p, data, 0o644)
	return name, true
}

// Holder is who holds the claim in stateDir, "" when nobody does (or it lapsed).
func Holder(stateDir string, now time.Time) string {
	var cur Importer
	data, err := os.ReadFile(filepath.Join(stateDir, ClaimFile))
	if err != nil || json.Unmarshal(data, &cur) != nil || now.Sub(cur.Seen) >= ClaimTTL {
		return ""
	}
	return cur.Name
}

// Cursor is the newest `updated` of the records this machine imported from a host, kept in
// the host's local dir; the host sends what changed after it.
func Cursor(dir string) time.Time {
	data, err := os.ReadFile(filepath.Join(dir, CursorFile))
	if err != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	return t
}

// SetCursor advances the cursor; it never moves back.
func SetCursor(dir string, t time.Time) {
	if !t.After(Cursor(dir)) {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, CursorFile), []byte(t.Format(time.RFC3339Nano)+"\n"), 0o644)
}

// Release drops the claim in stateDir when name holds it (`flok host notes <name> off` on
// the importing machine): another machine may claim the host at once.
func Release(stateDir, name string) {
	p := filepath.Join(stateDir, ClaimFile)
	var cur Importer
	if data, err := os.ReadFile(p); err != nil || json.Unmarshal(data, &cur) != nil {
		return
	}
	if cur.Name == name {
		_ = os.Remove(p)
	}
}
