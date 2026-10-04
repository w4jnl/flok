// Package proto is the line protocol between a local flok and `flok serve --stdio` on a remote
// host: one JSON object per line, in both directions, over ssh's stdio. Unknown frame types
// and fields are ignored, so additions stay compatible; a change an older side would misread
// bumps Version and the local refuses the hello.
package proto

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/merge"
)

const (
	// Version of the protocol; the remote's hello carries it.
	Version = 1
	// MaxLine bounds one frame; a snapshot of a busy server is a few hundred KB.
	MaxLine = 4 << 20
)

// Frame types. Remote to local: hello, snap, event, pong, error. Local to remote: goto, seen,
// visible, ping.
const (
	TypeHello   = "hello"
	TypeSnap    = "snap"
	TypeEvent   = "event"
	TypePong    = "pong"
	TypeError   = "error"
	TypeGoto    = "goto"
	TypeSeen    = "seen"
	TypeVisible = "visible"
	TypePing    = "ping"
	TypeRequest = "request" // remote to local: a flok key pressed inside the host's tmux
	TypeNotify  = "notify"  // local to remote: show a short message on the host's status line
	TypePrefix  = "prefix"  // local to remote: the local tmux prefix, for the host's tmux to take while served
)

// Frame is one line; Type says which payload field is set.
type Frame struct {
	Type  string    `json:"type"`
	Hello *Hello    `json:"hello,omitempty"`
	Snap  *Snapshot `json:"snap,omitempty"`
	Event *Event    `json:"event,omitempty"`
	Error string    `json:"error,omitempty"`
	Goto  *Goto     `json:"goto,omitempty"`
	Pane  string    `json:"pane,omitempty"` // seen
	On    bool      `json:"on,omitempty"`   // visible
	Cmd   string    `json:"cmd,omitempty"`  // request: toggle | hide | focus | jump | next | prev | keep-awake | host …
	Text  string    `json:"text,omitempty"` // notify
	Key   string    `json:"key,omitempty"`  // prefix: a tmux key name such as C-a
	Keys  []KeyBind `json:"keys,omitempty"` // prefix: the mapped keys ([keys] map) the host's tmux binds as well
}

// KeyBind is one mapped key: a tmux key name and the flok key command it runs.
type KeyBind struct {
	Key string `json:"key"`
	Cmd string `json:"cmd"`
}

// Hello opens the stream.
type Hello struct {
	Proto       int      `json:"proto"`
	Version     string   `json:"version"` // flok version on the host
	Hostname    string   `json:"hostname"`
	PID         int      `json:"pid"`
	TmuxVersion string   `json:"tmux_version"`
	StateDir    string   `json:"state_dir"`
	Warnings    []string `json:"warnings,omitempty"`
	Features    []string `json:"features,omitempty"` // what this serve can do; an older flok sends none
}

// Features a serve advertises in its hello. The local side checks them before relying on a
// behaviour, and a flok too old to send any is taken as having none, which is what it has.
const (
	FeatureKeys   = "keys"   // binds flok's keys in the host's tmux and relays them (request frames)
	FeaturePrefix = "prefix" // takes the local prefix on a prefix frame
	FeatureNotify = "notify" // shows notify frames on the status line
	FeatureKeyMap = "keymap" // binds the mapped keys of a prefix frame and relays `last …`
)

// ServeFeatures is what this flok's serve advertises.
var ServeFeatures = []string{FeatureKeys, FeaturePrefix, FeatureNotify, FeatureKeyMap}

// Has says whether the hello advertised a feature.
func (h *Hello) Has(feature string) bool {
	if h == nil {
		return false
	}
	for _, f := range h.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// Snapshot is the host's merged view, the tagged twin of merge.Snapshot without its
// write-back fields (the host's poller persists those itself).
type Snapshot struct {
	Spaces   []Space       `json:"spaces"`
	Agents   []agent.Agent `json:"agents"`
	Focus    Focus         `json:"focus"`
	Unseen   int           `json:"unseen"`
	Warnings []string      `json:"warnings,omitempty"`
	TakenAt  time.Time     `json:"taken_at"`
}

type Space struct {
	SessionID   string      `json:"session_id"`
	SessionName string      `json:"session_name"`
	Path        string      `json:"path,omitempty"`
	Branch      string      `json:"branch,omitempty"`
	Attached    bool        `json:"attached"`
	Current     bool        `json:"current"`
	Rollup      agent.State `json:"rollup,omitempty"`
	AgentCount  int         `json:"agent_count"`
}

type Focus struct {
	ClientTTY   string `json:"client_tty,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	SessionName string `json:"session_name,omitempty"`
	WindowID    string `json:"window_id,omitempty"`
	PaneID      string `json:"pane_id,omitempty"`
	Found       bool   `json:"found"`
}

// Event is a sound-worthy transition the host did not play (the local side does).
type Event struct {
	Pane string `json:"pane"`
	Kind string `json:"kind"` // done | blocked | error
}

// Goto asks the host to move its attached client (the work pane's ssh session).
type Goto struct {
	Session string `json:"session"`
	Window  string `json:"window,omitempty"`
	Pane    string `json:"pane,omitempty"`
}

// FromMerge converts the host's merge for the wire.
func FromMerge(s merge.Snapshot) Snapshot {
	out := Snapshot{Agents: s.Agents, Unseen: s.Unseen, Warnings: s.Warnings, TakenAt: s.TakenAt,
		Focus: Focus{ClientTTY: s.Focus.ClientTTY, SessionID: s.Focus.SessionID, SessionName: s.Focus.SessionName,
			WindowID: s.Focus.WindowID, PaneID: s.Focus.PaneID, Found: s.Focus.Found}}
	for _, sp := range s.Spaces {
		out.Spaces = append(out.Spaces, Space{SessionID: sp.SessionID, SessionName: sp.SessionName, Path: sp.Path, Branch: sp.Branch,
			Attached: sp.Attached, Current: sp.Current, Rollup: sp.Rollup, AgentCount: sp.AgentCount})
	}
	return out
}

// ToMerge is the local side's view of a host, ready for merge.Federate.
func (s Snapshot) ToMerge() merge.Snapshot {
	out := merge.Snapshot{Agents: s.Agents, Unseen: s.Unseen, Warnings: s.Warnings, TakenAt: s.TakenAt,
		Focus: merge.Focus{ClientTTY: s.Focus.ClientTTY, SessionID: s.Focus.SessionID, SessionName: s.Focus.SessionName,
			WindowID: s.Focus.WindowID, PaneID: s.Focus.PaneID, Found: s.Focus.Found}}
	for _, sp := range s.Spaces {
		out.Spaces = append(out.Spaces, agent.Space{SessionID: sp.SessionID, SessionName: sp.SessionName, Path: sp.Path, Branch: sp.Branch,
			Attached: sp.Attached, Current: sp.Current, Rollup: sp.Rollup, AgentCount: sp.AgentCount})
	}
	return out
}

// Write sends one frame.
func Write(w io.Writer, f Frame) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// ErrLineTooLong is returned for a frame over MaxLine; the stream is unusable after it.
var ErrLineTooLong = errors.New("frame exceeds " + fmt.Sprint(MaxLine) + " bytes")

// Reader parses frames. Lines that are not JSON objects are skipped (a chatty remote shell
// profile prints before flok starts) and handed to Noise when set.
type Reader struct {
	r     *bufio.Reader
	Noise func(line string)
}

func NewReader(r io.Reader) *Reader { return &Reader{r: bufio.NewReaderSize(r, 64<<10)} }

// Next returns the next frame; io.EOF when the stream ended cleanly.
func (r *Reader) Next() (Frame, error) {
	for {
		line, err := r.line()
		if err != nil {
			return Frame{}, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if line[0] != '{' {
			if r.Noise != nil {
				r.Noise(string(line))
			}
			continue
		}
		var f Frame
		if err := json.Unmarshal(line, &f); err != nil {
			if r.Noise != nil {
				r.Noise(string(line))
			}
			continue
		}
		return f, nil
	}
}

func (r *Reader) line() ([]byte, error) {
	var buf []byte
	for {
		part, isPrefix, err := r.r.ReadLine()
		if err != nil {
			if err == io.EOF && len(buf) > 0 {
				return buf, nil
			}
			return nil, err
		}
		buf = append(buf, part...)
		if len(buf) > MaxLine {
			return nil, ErrLineTooLong
		}
		if !isPrefix {
			return buf, nil
		}
	}
}
