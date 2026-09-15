package state

import (
	"encoding/json"
	"errors"
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
func (s *Store) TerminalFocused() bool { return readFlag(s.Dir, "terminal-focus") != "0" }

// SidebarHidden reports whether `flok hide` zoomed the work pane over the sidebar (default false).
func (s *Store) SidebarHidden() bool { return readFlag(s.Dir, "sidebar-hidden") == "1" }

// SetTerminalFocus and SetSidebarHidden write the 0/1 marker files the sidebar polls and watches;
// while either says "nobody can see the sidebar" it pauses the spinner and polls slowly.
func (s *Store) SetTerminalFocus(focused bool) error {
	return writeFlag(s.Dir, "terminal-focus", focused)
}
func (s *Store) SetSidebarHidden(hidden bool) error {
	return writeFlag(s.Dir, "sidebar-hidden", hidden)
}

// TerminalTheme is the terminal theme `flok up` detected ("dark", "light", or "" when unknown) and
// the background colour it came from ("" when set by hand with `flok theme`).
func (s *Store) TerminalTheme() (theme, bg string) {
	f := strings.Fields(readFlag(s.Dir, "terminal-theme"))
	if len(f) > 0 {
		theme = f[0]
	}
	if len(f) > 1 {
		bg = f[1]
	}
	return theme, bg
}

// SetTerminalTheme records the terminal theme; an empty theme removes the record.
func (s *Store) SetTerminalTheme(theme, bg string) error {
	p := filepath.Join(s.Dir, "terminal-theme")
	if theme == "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return os.WriteFile(p, []byte(strings.TrimSpace(theme+" "+bg)), 0o644)
}

func readFlag(dir, name string) string {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func writeFlag(dir, name string, v bool) error {
	b := []byte("0")
	if v {
		b = []byte("1")
	}
	return os.WriteFile(filepath.Join(dir, name), b, 0o644)
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
