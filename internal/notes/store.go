package notes

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Dir is where the journals live: one JSON file per session under the state dir.
func Dir(stateDir string) string { return filepath.Join(stateDir, "sessions") }

// Keep is how long an ended session's journal stays; a session that never ended goes after
// twice that without an event.
const Keep = 14 * 24 * time.Hour

func fileID(session string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '_'
		}
		return r
	}, session)
}

// Path is the journal file of a session.
func Path(stateDir, session string) string {
	return filepath.Join(Dir(stateDir), fileID(session)+".json")
}

// Load reads a session's journal; a missing one is empty, not an error.
func Load(stateDir, session string) (Journal, error) {
	var j Journal
	data, err := os.ReadFile(Path(stateDir, session))
	if errors.Is(err, os.ErrNotExist) {
		return j, nil
	}
	if err != nil {
		return j, err
	}
	return j, json.Unmarshal(data, &j)
}

// Update loads the session's journal, applies fn under an exclusive lock and saves it when fn
// says it changed. Hooks for one session can overlap (a notification and the request it
// announces), so the lock is per session.
func Update(stateDir, session string, fn func(j *Journal) bool) (Journal, error) {
	if err := os.MkdirAll(Dir(stateDir), 0o755); err != nil {
		return Journal{}, err
	}
	p := Path(stateDir, session)
	lock, err := os.OpenFile(p+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return Journal{}, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return Journal{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	j, err := Load(stateDir, session)
	if err != nil { // unreadable: start over rather than lose the session
		j = Journal{}
	}
	if !fn(&j) {
		return j, nil
	}
	return j, save(p, j)
}

func save(p string, j Journal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// All reads every journal in the state dir, for serve's resend and the sweep.
func All(stateDir string) []Journal {
	des, _ := os.ReadDir(Dir(stateDir))
	var out []Journal
	for _, d := range des {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(Dir(stateDir), d.Name()))
		if err != nil {
			continue
		}
		var j Journal
		if json.Unmarshal(data, &j) == nil && !j.Record.Created.IsZero() {
			out = append(out, j)
		}
	}
	return out
}

// Prune removes the journals of sessions that ended more than Keep ago, and of sessions
// without an event for twice that (a pane that died without a SessionEnd).
func Prune(stateDir string, now time.Time) {
	des, _ := os.ReadDir(Dir(stateDir))
	for _, d := range des {
		name := d.Name()
		if d.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		p := filepath.Join(Dir(stateDir), name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var j Journal
		if json.Unmarshal(data, &j) != nil {
			continue
		}
		r := j.Record
		gone := !r.Ended.IsZero() && now.Sub(r.Ended) > Keep || r.Ended.IsZero() && now.Sub(r.Updated) > 2*Keep
		if gone {
			_ = os.Remove(p)
			_ = os.Remove(p + ".lock")
		}
	}
}

// gitState is the branch and short head of the repository at dir; "" outside one. Two git
// calls, run at a session's start and end only.
func gitState(dir string) (branch, head string) {
	if dir == "" {
		return "", ""
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", ""
	}
	if branch = strings.TrimSpace(string(out)); branch == "HEAD" {
		branch = ""
	}
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output(); err == nil {
		head = strings.TrimSpace(string(out))
	}
	return branch, head
}

// LastImport is when nt last took a record from this machine, over every journal; zero when
// it never did.
func LastImport(stateDir string) time.Time {
	var last time.Time
	for _, j := range All(stateDir) {
		if j.ImportedAt.After(last) {
			last = j.ImportedAt
		}
	}
	return last
}
