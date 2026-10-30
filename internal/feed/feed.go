// Package feed turns an agent's transcript into a readable feed for the phone: the user's
// prompts, the assistant's text as it was written, and one short line per tool call, nothing
// of a tool's input or output. Claude Code keeps a session's transcript as JSON lines (the
// path is in every hook payload); a Tail follows one file and hands out the items that
// appeared since it last looked.
package feed

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Item kinds.
const (
	KindPrompt = "prompt" // what the user typed
	KindText   = "text"   // what the assistant wrote (markdown)
	KindTool   = "tool"   // a tool call, as one line ("Ran go test ./...", "Read notes.go")
	KindNote   = "note"   // something about the session itself ("context compacted")
)

// Bounds. MaxItems is how many items a feed keeps and a Reset frame carries; MaxText cuts a
// prompt or a text block (runes); MaxLine a tool line.
const (
	MaxItems = 80
	MaxText  = 6000
	MaxLine  = 120
)

// Item is one entry of the feed.
type Item struct {
	ID   string    `json:"id"`
	Kind string    `json:"kind"`
	At   time.Time `json:"at,omitzero"`
	Text string    `json:"text"`
	Tool string    `json:"tool,omitempty"` // KindTool: the tool's name, for an icon
}

// Feed is what travels: the items of one pane. Reset says the list replaces what the reader
// has (a new subscription, a new transcript); otherwise the items are appended. Note says why
// there is nothing to read when there is not (no transcript for this agent, a host too old).
type Feed struct {
	Pane  string `json:"pane"`
	Reset bool   `json:"reset,omitempty"`
	Note  string `json:"note,omitempty"`
	Items []Item `json:"items,omitempty"`
}

// Tail follows one transcript file. It is safe for one reader at a time per Tail; the methods
// lock, so a tick that overlaps the previous one is harmless.
type Tail struct {
	mu    sync.Mutex
	path  string
	off   int64
	rest  []byte // an incomplete last line, kept until it is finished
	items []Item // the last MaxItems seen
	sent  bool   // whether the reader got a Reset with the items so far
}

// NewTail starts following path from its beginning.
func NewTail(path string) *Tail { return &Tail{path: path} }

// Path is the file this Tail follows.
func (t *Tail) Path() string { return t.path }

// Next reads what was appended since the last call and returns the frame to send: a Reset with
// everything kept the first time (even when empty), then only new items; nil when there is
// nothing new. A file that shrank (a new session took the name) is read again from the start.
func (t *Tail) Next() (*Feed, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fresh, err := t.read()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if !t.sent {
		t.sent = true
		return &Feed{Reset: true, Items: append([]Item(nil), t.items...)}, nil
	}
	if len(fresh) == 0 {
		return nil, nil
	}
	return &Feed{Items: fresh}, nil
}

// read appends the complete new lines' items to t.items and returns them.
func (t *Tail) read() ([]Item, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() < t.off { // truncated: start over
		t.off, t.rest, t.items, t.sent = 0, nil, nil, false
	}
	if _, err := f.Seek(t.off, io.SeekStart); err != nil {
		return nil, err
	}
	var fresh []Item
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && err == nil {
			t.off += int64(len(line))
			if len(t.rest) > 0 {
				line = append(t.rest, line...)
				t.rest = nil
			}
			if it, ok := Parse(line); ok {
				fresh = append(fresh, it)
			}
		} else if len(line) > 0 { // no newline yet: keep it for the next read
			t.rest = append(t.rest, line...)
			t.off += int64(len(line))
		}
		if err != nil {
			break
		}
	}
	if len(fresh) > 0 {
		t.items = append(t.items, fresh...)
		if n := len(t.items) - MaxItems; n > 0 {
			t.items = append([]Item(nil), t.items[n:]...)
		}
	}
	return fresh, nil
}

// entry is the part of a transcript line the feed reads.
type entry struct {
	Type        string    `json:"type"`
	Subtype     string    `json:"subtype"`
	UUID        string    `json:"uuid"`
	Timestamp   time.Time `json:"timestamp"`
	IsSidechain bool      `json:"isSidechain"`
	IsMeta      bool      `json:"isMeta"`
	Message     *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// Parse reads one transcript line. Besides the user's prompts, the assistant's text and its
// tool calls, everything is skipped: thinking, tool results, a subagent's side chain, the
// session's bookkeeping, and the text Claude Code itself puts into a user turn (system
// reminders, task notifications, slash-command output), which starts with a tag.
func Parse(line []byte) (Item, bool) {
	var e entry
	if json.Unmarshal(bytes.TrimSpace(line), &e) != nil || e.IsSidechain {
		return Item{}, false
	}
	switch e.Type {
	case "system":
		if e.Subtype == "compact_boundary" {
			return Item{ID: id(e), Kind: KindNote, At: e.Timestamp, Text: "Context compacted"}, true
		}
		return Item{}, false
	case "user", "assistant":
	default:
		return Item{}, false
	}
	if e.Message == nil || e.IsMeta {
		return Item{}, false
	}
	var blocks []block
	var s string
	if json.Unmarshal(e.Message.Content, &s) == nil {
		blocks = []block{{Type: "text", Text: s}}
	} else if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return Item{}, false
	}
	if e.Type == "user" {
		var texts []string
		images := 0
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if t := strings.TrimSpace(b.Text); t != "" && !strings.HasPrefix(t, "<") {
					texts = append(texts, t)
				}
			case "image":
				images++
			}
		}
		text := strings.Join(texts, "\n\n")
		if images > 0 {
			if text != "" {
				text += "\n\n"
			}
			text += fmt.Sprintf("[%s]", plural(images, "image"))
		}
		if text == "" {
			return Item{}, false
		}
		return Item{ID: id(e), Kind: KindPrompt, At: e.Timestamp, Text: cut(text, MaxText)}, true
	}
	for _, b := range blocks { // one block per line in practice; the first that reads is it
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				return Item{ID: id(e), Kind: KindText, At: e.Timestamp, Text: cut(t, MaxText)}, true
			}
		case "tool_use":
			return Item{ID: id(e), Kind: KindTool, At: e.Timestamp, Text: Line(b.Name, b.Input), Tool: b.Name}, true
		}
	}
	return Item{}, false
}

func id(e entry) string {
	if e.UUID != "" {
		return e.UUID
	}
	return e.Timestamp.Format(time.RFC3339Nano)
}

// Line is a tool call in a few words, the way a reader would say it: "Ran go test ./..."
// (Bash's own description when it has one), "Read notes.go", "Edited model.go", "Searched for
// func Parse", "Asked: Which branch?", "Started an agent: review the diff". Nothing of the
// input beyond that one phrase.
func Line(tool string, input map[string]any) string {
	s := func(k string) string {
		v, _ := input[k].(string)
		return strings.TrimSpace(v)
	}
	first := func(v string) string {
		if i := strings.IndexByte(v, '\n'); i >= 0 {
			v = v[:i]
		}
		return v
	}
	var line string
	switch tool {
	case "Bash":
		if d := s("description"); d != "" {
			line = "Ran " + d
		} else {
			line = "Ran " + first(s("command"))
		}
	case "Read":
		line = "Read " + filepath.Base(s("file_path"))
	case "Edit", "MultiEdit", "NotebookEdit":
		line = "Edited " + filepath.Base(s("file_path"))
	case "Write":
		line = "Wrote " + filepath.Base(s("file_path"))
	case "Grep":
		line = "Searched for " + first(s("pattern"))
	case "Glob":
		line = "Looked for " + first(s("pattern"))
	case "Agent", "Task":
		line = "Started an agent: " + s("description")
	case "WebFetch":
		host := s("url")
		if u, err := url.Parse(host); err == nil && u.Host != "" {
			host = u.Host
		}
		line = "Fetched " + host
	case "WebSearch":
		line = "Searched the web for " + s("query")
	case "Skill":
		line = "Used the skill " + s("skill")
	case "TodoWrite":
		line = "Updated the todo list"
	case "AskUserQuestion":
		line = "Asked a question"
		if qs, ok := input["questions"].([]any); ok && len(qs) > 0 {
			if q, ok := qs[0].(map[string]any); ok {
				if t, _ := q["question"].(string); strings.TrimSpace(t) != "" {
					line = "Asked: " + first(strings.TrimSpace(t))
				}
			}
		}
	case "EnterPlanMode":
		line = "Started planning"
	case "ExitPlanMode":
		line = "Presented the plan"
	default:
		name := tool
		if strings.HasPrefix(tool, "mcp__") {
			if parts := strings.SplitN(tool, "__", 3); len(parts) == 3 {
				name = parts[1] + ": " + parts[2]
			}
		}
		line = "Used " + name
	}
	return cut(strings.TrimRight(line, " :"), MaxLine)
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
