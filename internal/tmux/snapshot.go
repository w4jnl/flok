package tmux

import (
	"strconv"
	"strings"
	"time"
)

// sep separates fields; tmux never emits it in names, paths or titles.
const sep = "\x1f"

type Session struct {
	ID, Name, Path    string
	Attached, Windows int
	Activity, Created int64
}

type Pane struct {
	ID, SessionID, SessionName, WindowID string
	WindowIndex, PaneIndex               int
	WindowActive, Active                 bool
	Command, Path, TTY                   string
	PID                                  int
	Dead                                 bool
	PBState, PBProgress                  string // OSC 9;4 progress bar: hidden|normal|error|indeterminate|paused, percent
	InMode                               bool   // copy/view mode: the visible text is scrollback, not the live screen
	Title                                string
}

type ClientInfo struct {
	TTY, SessionID, SessionName string
	Activity                    int64
	Width, Height               int
	TermName                    string
}

type Snapshot struct {
	Sessions []Session
	Panes    []Pane
	Clients  []ClientInfo
	TakenAt  time.Time
}

var (
	sessionFmt = "S" + sep + strings.Join([]string{"#{session_id}", "#{session_name}", "#{session_path}",
		"#{session_attached}", "#{session_windows}", "#{session_activity}", "#{session_created}"}, sep)
	paneFmt = "P" + sep + strings.Join([]string{"#{pane_id}", "#{session_id}", "#{session_name}", "#{window_id}",
		"#{window_index}", "#{pane_index}", "#{window_active}", "#{pane_active}", "#{pane_current_command}",
		"#{pane_current_path}", "#{pane_tty}", "#{pane_pid}", "#{pane_dead}", "#{pane_pb_state}", "#{pane_pb_progress}", "#{pane_in_mode}", "#{pane_title}"}, sep)
	clientFmt = "C" + sep + strings.Join([]string{"#{client_tty}", "#{session_id}", "#{client_session}",
		"#{client_activity}", "#{client_width}", "#{client_height}", "#{client_termname}"}, sep)
)

// TakeSnapshot lists sessions, panes and clients in a single tmux invocation.
func TakeSnapshot(c Client) (Snapshot, error) {
	s, _, err := TakeSnapshotRaw(c)
	return s, err
}

// TakeSnapshotRaw also returns tmux's raw output, a cheap fingerprint for "nothing changed".
// Output from a tmux that vis(3)-escapes its list-* output (3.4+) is decoded first; the
// client says so through Features (see Decode).
func TakeSnapshotRaw(c Client) (Snapshot, string, error) {
	out, err := c.Run("list-sessions", "-F", sessionFmt, ";", "list-panes", "-a", "-F", paneFmt, ";", "list-clients", "-F", clientFmt)
	if err != nil {
		return Snapshot{}, "", err
	}
	out = Decode(out, Escapes(c))
	snap := ParseSnapshot(out)
	snap.TakenAt = time.Now()
	return snap, out, nil
}

// Sep is the field separator flok puts in its -F formats: tmux never emits it in names,
// paths or titles.
const Sep = sep

// Escapes reports whether a client's tmux vis(3)-escapes list-* output (false for clients
// that do not know their version, such as test fakes).
func Escapes(c Client) bool {
	f, ok := c.(interface{ Features() Features })
	return ok && f.Features().EscapedOutput
}

// Decode undoes tmux's output escaping when escaped is true. From 3.4 on, server_client_print
// runs list-* output through strvis(VIS_OCTAL|VIS_CSTYLE|VIS_NOSLASH): non-printable bytes
// become `\ooo` or a C escape (`\t`, `\a`, …) and backslashes are NOT doubled, so only those
// two forms are decoded; 3.3 and earlier print the raw bytes and must not be touched (a title
// containing the text `\037` would otherwise be corrupted).
func Decode(out string, escaped bool) string {
	if !escaped || !strings.Contains(out, `\`) {
		return out
	}
	var b strings.Builder
	b.Grow(len(out))
	for i := 0; i < len(out); i++ {
		c := out[i]
		if c != '\\' || i+1 >= len(out) {
			b.WriteByte(c)
			continue
		}
		if i+3 < len(out) && isOctal(out[i+1]) && isOctal(out[i+2]) && isOctal(out[i+3]) {
			v, _ := strconv.ParseUint(out[i+1:i+4], 8, 8)
			b.WriteByte(byte(v))
			i += 3
			continue
		}
		if r, ok := cEscapes[out[i+1]]; ok {
			b.WriteByte(r)
			i++
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

var cEscapes = map[byte]byte{'a': 7, 'b': 8, 'f': 12, 'n': 10, 'r': 13, 't': 9, 'v': 11}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// ParseSnapshot parses the combined output of TakeSnapshot (already decoded, see Decode).
func ParseSnapshot(out string) Snapshot {
	var s Snapshot
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, sep)
		switch f[0] {
		case "S":
			if len(f) < 8 {
				continue
			}
			s.Sessions = append(s.Sessions, Session{ID: f[1], Name: f[2], Path: f[3], Attached: atoi(f[4]),
				Windows: atoi(f[5]), Activity: atoi64(f[6]), Created: atoi64(f[7])})
		case "P":
			if len(f) < 18 {
				continue
			}
			s.Panes = append(s.Panes, Pane{ID: f[1], SessionID: f[2], SessionName: f[3], WindowID: f[4],
				WindowIndex: atoi(f[5]), PaneIndex: atoi(f[6]), WindowActive: f[7] == "1", Active: f[8] == "1",
				Command: f[9], Path: f[10], TTY: f[11], PID: atoi(f[12]), Dead: f[13] == "1",
				PBState: f[14], PBProgress: f[15], InMode: f[16] == "1", Title: strings.Join(f[17:], sep)})
		case "C":
			if len(f) < 8 {
				continue
			}
			s.Clients = append(s.Clients, ClientInfo{TTY: f[1], SessionID: f[2], SessionName: f[3],
				Activity: atoi64(f[4]), Width: atoi(f[5]), Height: atoi(f[6]), TermName: f[7]})
		}
	}
	return s
}

// ActivePane returns the active pane of the active window of a session.
func (s Snapshot) ActivePane(sessionID string) (Pane, bool) {
	for _, p := range s.Panes {
		if p.SessionID == sessionID && p.WindowActive && p.Active {
			return p, true
		}
	}
	return Pane{}, false
}

func atoi(s string) int     { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }
func atoi64(s string) int64 { n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64); return n }
