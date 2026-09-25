// Package snapshot is the file the sidebar publishes for out-of-process readers (flok-bar):
// the merged view of sessions and agents, refreshed on change and as a heartbeat.
package snapshot

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/merge"
)

const (
	FileName      = "snapshot.json"
	heartbeat     = 5 * time.Second  // rewrite even when unchanged, so readers can tell we are alive
	staleAfter    = 15 * time.Second // older than this: the sidebar is probably gone
	runtimeMarker = "runtime.json"   // written by `flok up`, removed by `flok down`
)

type Agent struct {
	PaneID      string      `json:"pane_id"`
	SessionID   string      `json:"session_id"`
	SessionName string      `json:"session_name"`
	WindowID    string      `json:"window_id"`
	Name        string      `json:"name"`
	Kind        string      `json:"kind"`
	State       agent.State `json:"state"`
	Reason      string      `json:"reason,omitempty"`
	Tool        string      `json:"tool,omitempty"`
	Detail      string      `json:"detail,omitempty"`
	Unseen      int         `json:"unseen"`
	StateSince  time.Time   `json:"state_since"`
	HasHooks    bool        `json:"has_hooks"`
}

type Session struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Rollup agent.State `json:"rollup,omitempty"`
	Agents int         `json:"agents"`
}

type Focus struct {
	SessionName string `json:"session_name,omitempty"`
	PaneID      string `json:"pane_id,omitempty"`
}

type Snapshot struct {
	UpdatedAt  time.Time `json:"updated_at"`
	SidebarPID int       `json:"sidebar_pid"`
	Focus      Focus     `json:"focus"`
	Agents     []Agent   `json:"agents"`
	Sessions   []Session `json:"sessions"`
	Unseen     int       `json:"unseen"`
	KeepAwake  bool      `json:"keep_awake,omitempty"` // the sidebar holds the power assertions (flok keep-awake)
	// KeepAwakePresence is "active" while keep-awake also keeps the user active ([keep_awake]
	// presence), "blocked" when macOS drops those events (no Accessibility permission).
	KeepAwakePresence string `json:"keep_awake_presence,omitempty"`
}

// Freshness tells a reader how much to trust a loaded snapshot.
type Freshness int

const (
	Fresh Freshness = iota
	Stale           // file exists but has not been refreshed recently
	Gone            // no snapshot, or flok is not running (no runtime.json)
)

// FromMerge converts the sidebar's merged snapshot.
func FromMerge(s merge.Snapshot) Snapshot {
	out := Snapshot{SidebarPID: os.Getpid(), Unseen: s.Unseen,
		Focus: Focus{SessionName: s.Focus.SessionName, PaneID: s.Focus.PaneID}}
	for _, a := range s.Agents {
		out.Agents = append(out.Agents, Agent{PaneID: a.PaneID, SessionID: a.SessionID, SessionName: a.SessionName,
			WindowID: a.WindowID, Name: a.Name, Kind: a.Kind, State: a.State, Reason: a.Reason, Tool: a.CurrentTool,
			Detail: a.ToolDetail, Unseen: a.Unseen, StateSince: a.StateSince, HasHooks: a.HasHooks})
	}
	for _, sp := range s.Spaces {
		out.Sessions = append(out.Sessions, Session{ID: sp.SessionID, Name: sp.SessionName, Rollup: sp.Rollup, Agents: sp.AgentCount})
	}
	return out
}

// Publisher writes the snapshot atomically, skipping unchanged content between heartbeats.
type Publisher struct {
	Dir      string
	last     []byte
	lastAt   time.Time
	lastSnap Snapshot
}

// Heartbeat rewrites the last published snapshot once the heartbeat interval has elapsed, so
// readers can tell the sidebar is alive while nothing changes (the merge only runs on changes).
func (p *Publisher) Heartbeat(now time.Time) (bool, error) {
	if p.last == nil || now.Sub(p.lastAt) < heartbeat {
		return false, nil
	}
	return p.Publish(p.lastSnap, now)
}

// Publish returns true when a file was written.
func (p *Publisher) Publish(s Snapshot, now time.Time) (bool, error) {
	s.UpdatedAt = time.Time{}
	body, err := json.Marshal(s)
	if err != nil {
		return false, err
	}
	if bytes.Equal(body, p.last) && now.Sub(p.lastAt) < heartbeat {
		return false, nil
	}
	s.UpdatedAt = now
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return false, err
	}
	path := filepath.Join(p.Dir, FileName)
	tmp, err := os.CreateTemp(p.Dir, FileName+".*.tmp")
	if err != nil {
		return false, err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return false, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return false, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return false, err
	}
	p.last, p.lastAt, p.lastSnap = body, now, s
	return true, nil
}

// Remove deletes the snapshot (called when the sidebar or the outer goes away).
func Remove(dir string) { _ = os.Remove(filepath.Join(dir, FileName)) }

// SidebarAlive reports whether the sidebar that wrote the snapshot still runs.
func (s Snapshot) SidebarAlive() bool {
	return s.SidebarPID > 0 && syscall.Kill(s.SidebarPID, 0) == nil
}

// KeepAwakeHeld reports whether the sidebar that wrote the snapshot holds the keep-awake
// assertions: it says so and it is still alive. macOS drops the assertions with the process,
// while its last snapshot lingers until it goes stale.
func (s Snapshot) KeepAwakeHeld() bool { return s.KeepAwake && s.SidebarAlive() }

// Load reads the snapshot and judges its freshness.
func Load(dir string, now time.Time) (Snapshot, Freshness) {
	var s Snapshot
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return s, Gone
	}
	if json.Unmarshal(data, &s) != nil {
		return s, Gone
	}
	if _, err := os.Stat(filepath.Join(dir, runtimeMarker)); err != nil {
		return s, Gone
	}
	if now.Sub(s.UpdatedAt) > staleAfter {
		return s, Stale
	}
	return s, Fresh
}
