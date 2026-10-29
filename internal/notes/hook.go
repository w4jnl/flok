package notes

import (
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/w4jnl/flok/internal/agent"
)

// Observe is the hook's step after its own work: the event goes into the session's journal,
// and when the report changed in a way worth showing, a detached worker (`flok _notes-import
// <session> <nt>`, Finish) reads the git state and, when this machine imports (nt is "" on a
// served host or with [notes] off), hands the whole record to nt, so the hook's own round trip
// never carries git or nt. An event without a session id is nothing to journal.
func Observe(stateDir, pane string, ev agent.Event, nt string, now time.Time, logf func(string, ...any)) {
	if ev.AgentSessionID == "" {
		return
	}
	var imp bool
	_, err := Update(stateDir, ev.AgentSessionID, func(j *Journal) bool {
		var changed bool
		changed, imp = Apply(j, ev, now)
		if changed && j.Pane == "" {
			j.Pane = pane
		}
		return changed
	})
	if err != nil {
		logf("notes: %s: %v", ev.AgentSessionID, err)
		return
	}
	if imp {
		if err := Spawn(ev.AgentSessionID, nt); err != nil {
			logf("notes: %s: %v", ev.AgentSessionID, err)
		}
	}
}

// Spawn starts the import worker for a session, detached from the hook; tests replace it.
var Spawn = func(session, nt string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	c := exec.Command(exe, "_notes-import", session, nt)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }()
	return nil
}

// Finish is the worker: it fills the git state the journal lacks (branch and head once, the
// head again at the end), imports the record with nt, and records the outcome; a failure is
// logged once per session, and the next import carries everything again. It also sweeps old
// journals.
func Finish(stateDir, session, nt string, now time.Time, logf func(string, ...any)) {
	Prune(stateDir, now)
	j, err := Update(stateDir, session, func(j *Journal) bool {
		r := &j.Record
		if r.Created.IsZero() {
			return false
		}
		changed := false
		if !j.GitProbed {
			r.Branch, r.Head = gitState(r.Cwd)
			j.GitProbed, changed = true, true
		}
		if !r.Ended.IsZero() && !j.HeadAtEnd {
			if _, head := gitState(r.Cwd); head != "" {
				r.Head = head
			}
			j.HeadAtEnd, changed = true, true
		}
		if changed && now.After(r.Updated) {
			r.Updated = now // a served host's serve sends the record again with its git state
		}
		return changed
	})
	if err != nil || j.Record.Created.IsZero() || nt == "" {
		return
	}
	if !j.ImportedAt.IsZero() && !j.Record.Updated.After(j.ImportedAt) {
		return // an earlier worker took this very record
	}
	err = Import(nt, j.Record)
	_, _ = Update(stateDir, session, func(j *Journal) bool {
		if err == nil {
			j.ImportedAt, j.Failed = now, false
			return true
		}
		if j.Failed {
			return false
		}
		j.Failed = true
		return true
	})
	if err != nil && !j.Failed {
		logf("notes: %s: %v", session, err)
	}
}
