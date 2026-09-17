// Package resurrect integrates flok's hook records with tmux-resurrect save files.
package resurrect

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/tmux"
)

// Skipped describes an agent pane that could not be saved for exact resumption.
type Skipped struct {
	PaneID string
	Kind   string
	Reason string
}

// Report summarizes changes made to a tmux-resurrect state file.
type Report struct {
	Rewritten int
	Skipped   []Skipped
}

// Inputs is the live state a rewrite consults.
type Inputs struct {
	Snapshot tmux.Snapshot
	Hooks    map[string]agent.Agent // hook records by pane ID
	Adapters []agent.Adapter
	// Registry holds Claude session IDs by pane ID from Claude Code's own registry
	// (`claude agents --json`, matched by tty). It describes the live process, so it wins over a
	// hook record, and it covers panes whose hooks never fired: hooks installed after the
	// session started, or pointing at a binary that no longer exists.
	Registry map[string]string
}

// Rewrite updates agent pane commands in path and leaves every other field untouched.
func Rewrite(path string, in Inputs) (Report, error) {
	var report Report
	snap := in.Snapshot
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return report, fmt.Errorf("resolve state file %s: %w", path, err)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return report, fmt.Errorf("read state file %s: %w", resolved, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return report, fmt.Errorf("stat state file %s: %w", resolved, err)
	}

	panes := make(map[string]tmux.Pane, len(snap.Panes))
	for _, pane := range snap.Panes {
		panes[paneKey(pane.SessionName, pane.WindowIndex, pane.PaneIndex)] = pane
	}

	lines := bytes.Split(data, []byte{'\n'})
	changed := false
	for i, raw := range lines {
		if len(raw) == 0 || !bytes.HasPrefix(raw, []byte("pane\t")) {
			continue
		}
		fields := strings.Split(string(raw), "\t")
		if len(fields) < 11 {
			return report, fmt.Errorf("parse state file %s line %d: pane record has %d fields, want at least 11", resolved, i+1, len(fields))
		}
		windowIndex, err := strconv.Atoi(fields[2])
		if err != nil {
			return report, fmt.Errorf("parse state file %s line %d: window index %q: %w", resolved, i+1, fields[2], err)
		}
		paneIndex, err := strconv.Atoi(fields[5])
		if err != nil {
			return report, fmt.Errorf("parse state file %s line %d: pane index %q: %w", resolved, i+1, fields[5], err)
		}
		pane, ok := panes[paneKey(fields[1], windowIndex, paneIndex)]
		if !ok {
			continue
		}
		kind, recognized := agentKind(pane, in.Hooks[pane.ID], in.Registry[pane.ID], in.Adapters)
		if !recognized {
			continue
		}
		if id, reason := sessionFor(kind, in.Hooks[pane.ID], in.Registry[pane.ID]); id == "" {
			fields[10] = ":"
			report.Skipped = append(report.Skipped, Skipped{PaneID: pane.ID, Kind: kind, Reason: reason})
		} else {
			fields[10] = ":" + resumeCommand(kind, id)
			report.Rewritten++
		}
		lines[i] = []byte(strings.Join(fields, "\t"))
		changed = true
	}
	if !changed {
		return report, nil
	}
	if err := writeAtomic(resolved, bytes.Join(lines, []byte{'\n'}), info.Mode().Perm()); err != nil {
		return report, fmt.Errorf("write state file %s: %w", resolved, err)
	}
	return report, nil
}

func paneKey(session string, window, pane int) string {
	return session + "\x00" + strconv.Itoa(window) + "\x00" + strconv.Itoa(pane)
}

// agentKind recognizes an agent pane by its current command, by Claude's registry (which also
// covers a pane whose foreground process is a tool the agent runs) or by a hook record that
// says a turn is open.
func agentKind(pane tmux.Pane, rec agent.Agent, registryID string, adapters []agent.Adapter) (string, bool) {
	if ad := agent.Match(pane.Command, adapters); ad != nil && supported(ad.ID()) {
		return ad.ID(), true
	}
	if validSessionID(registryID) {
		return "claude", true
	}
	if rec.HasHooks && (rec.State == agent.Working || rec.State == agent.Blocked) && supported(rec.Kind) {
		return rec.Kind, true
	}
	return "", false
}

// sessionFor picks the session ID to resume, or explains why the pane is saved as a shell.
func sessionFor(kind string, rec agent.Agent, registryID string) (id, reason string) {
	if kind == "claude" && validSessionID(registryID) {
		return registryID, ""
	}
	switch {
	case !rec.HasHooks && kind == "claude":
		return "", "no hook record and not listed by `claude agents`"
	case !rec.HasHooks:
		return "", "no hook record"
	case rec.Kind != kind:
		return "", "hook record belongs to " + rec.Kind
	case !validSessionID(rec.AgentSessionID):
		return "", "no usable session ID"
	}
	return rec.AgentSessionID, ""
}

func supported(kind string) bool { return kind == "claude" || kind == "copilot" }

func validSessionID(id string) bool {
	return id != "" && !strings.ContainsAny(id, "\x00\r\n\t")
}

func resumeCommand(kind, sessionID string) string {
	switch kind {
	case "claude":
		return "claude --resume " + shellQuote(sessionID)
	case "copilot":
		return "copilot --resume=" + shellQuote(sessionID)
	default:
		return ""
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
