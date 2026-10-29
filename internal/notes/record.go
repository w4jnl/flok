// Package notes keeps a journal per agent session, what a session report is made of, and hands
// it to nt (`nt agent import`), which writes the report into the notes repository. The hook
// feeds the journal from every event; the record it sends is the contract in docs/notes.md.
package notes

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/w4jnl/flok/internal/agent"
)

// Version is the shape of Record; nt refuses a higher one.
const Version = 1

// Bounds on what a journal keeps: a session that runs for days stays a small file.
const (
	MaxTurns     = 200
	MaxEvents    = 50 // per turn
	MaxTools     = 30 // distinct tool names per turn
	MaxText      = 200
	MaxTextBytes = 2000 // of the message the text is taken from
)

// Record is one session as flok knows it, the whole of it on every import.
type Record struct {
	Version   int       `json:"version"`
	Agent     string    `json:"agent"`   // nt's slug: claude-code, copilot-cli
	Session   string    `json:"session"` // the agent's own id
	Host      string    `json:"host"`    // "" = the importing machine; else flok's alias for a remote host
	Cwd       string    `json:"cwd"`
	Branch    string    `json:"branch,omitempty"`
	Head      string    `json:"head,omitempty"`
	Model     string    `json:"model,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	Ended     time.Time `json:"ended,omitzero"`
	EndReason string    `json:"end_reason,omitempty"` // error | clear | logout | exit | other
	State     string    `json:"state"`                // working | blocked | waiting | idle | done
	Turns     int       `json:"turns"`
	Tools     int       `json:"tools"`
	BlockedS  int       `json:"blocked_s"`
	WaitedS   int       `json:"waited_s"`
	Log       []Turn    `json:"log"`
}

// Turn is one turn: from the prompt (or the first tool) to the Stop.
type Turn struct {
	Started time.Time      `json:"started"`
	Ended   time.Time      `json:"ended,omitzero"`
	Text    string         `json:"text"` // the first line of the agent's last message, ≤ MaxText
	Tools   map[string]int `json:"tools,omitempty"`
	Events  []Event        `json:"events,omitempty"`
}

// Event is something that happened in a turn worth a line in the report.
type Event struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"` // blocked | question | wait | error | compaction
	Text    string    `json:"text,omitempty"`
	Seconds int       `json:"seconds,omitempty"` // wait: how long, once known
}

// Journal is the file: the record and what the hook needs between events.
type Journal struct {
	Record     Record    `json:"record"`
	Pane       string    `json:"pane,omitempty"`
	BlockedAt  time.Time `json:"blocked_at,omitzero"` // an open block
	WaitAt     time.Time `json:"wait_at,omitzero"`    // an open wait (its event is the last of the last turn)
	LastError  bool      `json:"last_error,omitempty"`
	GitProbed  bool      `json:"git_probed,omitempty"` // branch and head were read (the worker does it, off the hook's path)
	HeadAtEnd  bool      `json:"head_at_end,omitempty"`
	ImportedAt time.Time `json:"imported_at,omitzero"` // the last import nt took
	Failed     bool      `json:"import_failed,omitempty"`
}

// Slug is nt's name for a flok agent id.
func Slug(id string) string {
	switch id {
	case "claude":
		return "claude-code"
	case "copilot":
		return "copilot-cli"
	}
	return id
}

// Apply feeds one hook event into the journal. It returns whether the journal changed and
// whether the report should be imported now: at the start, on a new block, on a wait, at a
// Stop, on an error and at the end, not on every tool call.
func Apply(j *Journal, ev agent.Event, now time.Time) (changed, imp bool) {
	r := &j.Record
	if r.Created.IsZero() {
		r.Version, r.Agent, r.Session, r.Cwd, r.Created, r.State = Version, Slug(ev.Agent), ev.AgentSessionID, ev.Cwd, now, "idle"
		changed = true
	}
	if ev.Model != "" && r.Model == "" {
		r.Model, changed = ev.Model, true
	}
	if r.Cwd == "" && ev.Cwd != "" {
		r.Cwd, changed = ev.Cwd, true
	}
	open := func() *Turn {
		if n := len(r.Log); n > 0 && r.Log[n-1].Ended.IsZero() {
			return &r.Log[n-1]
		}
		r.Log = append(r.Log, Turn{Started: now})
		r.Turns++ // every turn opened, the open one included: nt keeps the lines of the ones dropped off the front
		if len(r.Log) > MaxTurns {
			r.Log = r.Log[len(r.Log)-MaxTurns:]
		}
		return &r.Log[len(r.Log)-1]
	}
	closeBlock := func() {
		if !j.BlockedAt.IsZero() {
			r.BlockedS += int(now.Sub(j.BlockedAt) / time.Second)
			j.BlockedAt = time.Time{}
		}
	}
	closeWait := func() {
		if j.WaitAt.IsZero() {
			return
		}
		secs := int(now.Sub(j.WaitAt) / time.Second)
		r.WaitedS += secs
		j.WaitAt = time.Time{}
		if n := len(r.Log); n > 0 {
			if m := len(r.Log[n-1].Events); m > 0 && r.Log[n-1].Events[m-1].Kind == "wait" {
				r.Log[n-1].Events[m-1].Seconds = secs
			}
		}
	}
	closeTurn := func(text string) {
		t := open()
		t.Ended, t.Text = now, firstLine(text)
	}
	addEvent := func(e Event) {
		t := open()
		if len(t.Events) >= MaxEvents {
			return
		}
		t.Events = append(t.Events, e)
	}
	switch ev.Kind {
	case agent.EvSessionStart:
		r.State, changed, imp = "idle", true, true
	case agent.EvSessionRefresh:
		switch ev.Name {
		case "SessionStart": // a compaction, mid-turn
			addEvent(Event{At: now, Kind: "compaction"})
			changed = true
		case "SessionEnd": // /clear or a resume: this session id is over, a new one follows
			end(j, ev.Reason, now)
			changed, imp = true, true
		}
	case agent.EvPrompt:
		closeWait()
		closeBlock()
		if n := len(r.Log); n == 0 || !r.Log[n-1].Ended.IsZero() {
			open()
		}
		r.State, j.LastError, changed = "working", false, true
	case agent.EvToolStart:
		closeWait()
		closeBlock()
		t := open()
		if t.Tools == nil {
			t.Tools = map[string]int{}
		}
		if _, known := t.Tools[ev.Tool]; known || len(t.Tools) < MaxTools {
			t.Tools[ev.Tool]++
		}
		r.Tools++
		r.State, changed = "working", true
	case agent.EvToolEnd:
		closeBlock()
		r.State, changed = "working", true
	case agent.EvBlocked:
		kind, text := "blocked", "permission: "+ev.Tool
		if ev.Reason == "question" {
			kind, text = "question", ""
			if ev.Form != nil {
				text = ev.Form.Summary(MaxText)
			}
		} else if ev.Tool == "" {
			text = ev.Reason
		}
		if !j.BlockedAt.IsZero() { // the same block announced again (a notification, the form's second hook)
			if t := open(); len(t.Events) > 0 {
				if last := &t.Events[len(t.Events)-1]; last.Kind == kind && (last.Text == text || text == "" || last.Text == "") {
					if last.Text == "" {
						last.Text, changed = text, text != ""
					}
					if r.State != "blocked" {
						r.State, changed = "blocked", true
					}
					return changed, false
				}
			}
			closeBlock()
		}
		closeWait()
		addEvent(Event{At: now, Kind: kind, Text: text})
		j.BlockedAt = now
		r.State, changed, imp = "blocked", true, true
	case agent.EvNudge:
	case agent.EvWaiting:
		closeBlock()
		addEvent(Event{At: now, Kind: "wait", Text: ev.Tool}) // in the turn that ends behind it
		closeTurn(ev.Message)
		j.WaitAt = now
		r.State, changed, imp = "waiting", true, true
	case agent.EvStop:
		closeBlock()
		closeWait()
		if ev.Reason == "error" {
			addEvent(Event{At: now, Kind: "error", Text: firstLine(ev.Message)})
			j.LastError = true
		}
		closeTurn(ev.Message)
		r.State, changed, imp = "done", true, true
	case agent.EvSessionEnd:
		end(j, ev.Reason, now)
		changed, imp = true, true
	default:
		return false, false
	}
	if changed {
		r.Updated = now
	}
	return changed, imp
}

// end closes the session: an open turn ends without a text, the git head is read again.
func end(j *Journal, reason string, now time.Time) {
	r := &j.Record
	if !r.Ended.IsZero() {
		return
	}
	if !j.BlockedAt.IsZero() {
		r.BlockedS += int(now.Sub(j.BlockedAt) / time.Second)
		j.BlockedAt = time.Time{}
	}
	if !j.WaitAt.IsZero() {
		r.WaitedS += int(now.Sub(j.WaitAt) / time.Second)
		j.WaitAt = time.Time{}
	}
	if n := len(r.Log); n > 0 && r.Log[n-1].Ended.IsZero() {
		r.Log[n-1].Ended = now
	}
	switch reason {
	case "clear", "logout", "other":
	case "prompt_input_exit":
		reason = "exit"
	case "":
		reason = "other"
	default:
		reason = "other"
	}
	if j.LastError {
		reason = "error"
	}
	r.Ended, r.EndReason, r.State = now, reason, "done"
}

// firstLine is the first line of msg with text in it, markdown prefixes and extra spaces
// dropped, at most MaxText runes; a heading ("## Done") only when nothing else follows it.
// Long answers stay out of the report.
func firstLine(msg string) string {
	if len(msg) > MaxTextBytes {
		msg = msg[:MaxTextBytes]
	}
	heading, inCode := "", false
	for _, l := range strings.Split(msg, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		isHeading := strings.HasPrefix(strings.TrimSpace(l), "#")
		l = strings.Join(strings.Fields(strings.TrimLeft(l, "#>*- \t")), " ")
		l = strings.NewReplacer("**", "", "__", "").Replace(l)
		if l == "" {
			continue
		}
		if utf8.RuneCountInString(l) > MaxText {
			l = string([]rune(l)[:MaxText-1]) + "…"
		}
		if isHeading {
			if heading == "" {
				heading = l
			}
			continue
		}
		return l
	}
	return heading
}
