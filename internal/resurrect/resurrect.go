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

// Rewrite updates agent pane commands in path and leaves every other field untouched.
func Rewrite(path string, snap tmux.Snapshot, hooks map[string]agent.Agent, adapters []agent.Adapter) (Report, error) {
	var report Report
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
		kind, recognized := agentKind(pane, hooks[pane.ID], adapters)
		if !recognized {
			continue
		}
		rec, hasHook := hooks[pane.ID]
		switch {
		case !hasHook || !rec.HasHooks:
			fields[10] = ":"
			report.Skipped = append(report.Skipped, Skipped{PaneID: pane.ID, Kind: kind, Reason: "no hook record"})
		case rec.Kind != kind:
			fields[10] = ":"
			report.Skipped = append(report.Skipped, Skipped{PaneID: pane.ID, Kind: kind, Reason: "hook record belongs to " + rec.Kind})
		case !validSessionID(rec.AgentSessionID):
			fields[10] = ":"
			report.Skipped = append(report.Skipped, Skipped{PaneID: pane.ID, Kind: kind, Reason: "no usable session ID"})
		default:
			fields[10] = ":" + resumeCommand(kind, rec.AgentSessionID)
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

func agentKind(pane tmux.Pane, rec agent.Agent, adapters []agent.Adapter) (string, bool) {
	if ad := agent.Match(pane.Command, adapters); ad != nil && supported(ad.ID()) {
		return ad.ID(), true
	}
	if rec.HasHooks && (rec.State == agent.Working || rec.State == agent.Blocked) && supported(rec.Kind) {
		return rec.Kind, true
	}
	return "", false
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
