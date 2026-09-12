package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/w4jnl/flok/internal/agent"
)

// Store keeps hook-owned agent records (agents/), seen marks (seen/) and an event log
// under one directory. Writes are atomic (temp + rename) and serialized per pane with flock.
type Store struct{ Dir string }

func New(dir string) *Store {
	_ = os.MkdirAll(filepath.Join(dir, "agents"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "seen"), 0o755)
	return &Store{Dir: dir}
}

func fileID(pane string) string {
	return strings.NewReplacer("%", "", "/", "_").Replace(pane)
}

func (s *Store) agentPath(pane string) string {
	return filepath.Join(s.Dir, "agents", fileID(pane)+".json")
}
func (s *Store) seenPath(pane string) string {
	return filepath.Join(s.Dir, "seen", fileID(pane)+".json")
}

// Update loads the pane's record, applies fn under an exclusive lock and saves the result.
func (s *Store) Update(pane string, fn func(a *agent.Agent) Effects) (agent.Agent, Effects, error) {
	lock, err := os.OpenFile(s.agentPath(pane)+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return agent.Agent{}, Effects{}, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return agent.Agent{}, Effects{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	a := agent.Agent{PaneID: pane}
	if data, err := os.ReadFile(s.agentPath(pane)); err == nil {
		_ = json.Unmarshal(data, &a)
	}
	a.PaneID = pane
	fx := fn(&a)
	if fx.Delete {
		_ = os.Remove(s.agentPath(pane))
		_ = os.Remove(s.seenPath(pane))
		return a, fx, nil
	}
	return a, fx, writeJSON(s.agentPath(pane), a)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
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
	return os.Rename(tmp.Name(), path)
}

// Delete removes a pane's record and seen mark.
func (s *Store) Delete(pane string) {
	_ = os.Remove(s.agentPath(pane))
	_ = os.Remove(s.agentPath(pane) + ".lock")
	_ = os.Remove(s.seenPath(pane))
}

// LoadAgents returns all hook-owned records keyed by pane id.
func (s *Store) LoadAgents() map[string]agent.Agent {
	out := map[string]agent.Agent{}
	files, _ := filepath.Glob(filepath.Join(s.Dir, "agents", "*.json"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var a agent.Agent
		if json.Unmarshal(data, &a) == nil && a.PaneID != "" {
			out[a.PaneID] = a
		}
	}
	return out
}

type seenRecord struct {
	SeenAt time.Time `json:"seen_at"`
}

// MarkSeen records that the user looked at the pane at t.
func (s *Store) MarkSeen(pane string, t time.Time) error {
	return writeJSON(s.seenPath(pane), seenRecord{SeenAt: t})
}

// LoadSeen returns seen marks keyed by pane id (a bare "%N" id, reconstructed from the file name).
func (s *Store) LoadSeen() map[string]time.Time {
	out := map[string]time.Time{}
	files, _ := filepath.Glob(filepath.Join(s.Dir, "seen", "*.json"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var r seenRecord
		if json.Unmarshal(data, &r) == nil {
			out["%"+strings.TrimSuffix(filepath.Base(f), ".json")] = r.SeenAt
		}
	}
	return out
}

// TerminalFocused reports the last client-focus-in/out hook of the outer server (default true).
func (s *Store) TerminalFocused() bool {
	data, err := os.ReadFile(filepath.Join(s.Dir, "terminal-focus"))
	return err != nil || strings.TrimSpace(string(data)) != "0"
}

// AppendEvent appends one JSON line to events.log, rotating at 5 MB.
func (s *Store) AppendEvent(v any) {
	p := filepath.Join(s.Dir, "events.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > 5<<20 {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
}
