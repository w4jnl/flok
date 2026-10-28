package cli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
)

// relayCmd is what `flok phone`, `flok link approve | list | forget` and `flok tenant` share:
// they ask the relay through the running sidebar's link (the request mailbox, a reply file),
// never over HTTP themselves, so the key stays on the link. The environment is injected for
// tests.
type relayCmd struct {
	name      string // the command, for messages: phone | link | tenant
	dir       string
	cfg       config.Config
	now       func() time.Time
	wait      time.Duration // how long to wait for the relay's answer through the sidebar
	tty       bool          // stdout is a terminal: draw the code screen
	out, errw io.Writer
}

func (c relayCmd) fail(err error) int {
	fmt.Fprintf(c.errw, "flok %s: %v\n", c.name, err)
	return 1
}

// settings is the relay link as this flok has it: a join record, else [link] in config.toml.
func (c relayCmd) settings() state.LinkSettings {
	return state.New(c.dir).LinkSettings(c.cfg.Link.URL, c.cfg.Link.Token, c.cfg.Link.Pin, c.cfg.Link.Admin)
}

// ready checks that this flok can reach its relay: a link in the config or a join record, a
// running sidebar that has it up. It returns the relay as the app addresses it (link.OriginURL).
func (c relayCmd) ready() (string, error) {
	settings := c.settings()
	if strings.TrimSpace(settings.URL) == "" {
		return "", errors.New("no relay link: set [link] url in config.toml, or flok link join <relay> (docs/phone.md has the setup)")
	}
	origin := link.OriginURL(settings.URL)
	if origin == "" {
		return "", fmt.Errorf("[link] url %q is not a URL flok can turn into the relay's address", settings.URL)
	}
	s, f := snapshot.Load(c.dir, c.now())
	if f != snapshot.Fresh || !s.SidebarAlive() {
		return "", errors.New("the sidebar is not running (flok up)")
	}
	if s.Link == nil {
		return "", errors.New("the running sidebar has no relay link (flok reload after setting [link] url)")
	}
	if st := link.State(s.Link.State); st != link.Connected {
		why := "the relay link is " + string(st)
		if s.Link.Detail != "" {
			why += ": " + s.Link.Detail
		}
		return "", errors.New(why + " (flok link status)")
	}
	return origin, nil
}

// ask files the request for the sidebar and waits for the relay's answer in the reply file,
// which stays for the caller (a pairing's second word lands there); the caller removes it.
func (c relayCmd) ask(r state.Request) (string, wire.Frame, error) {
	st := state.New(c.dir)
	st.SweepReplies(c.now(), 10*time.Minute)
	r.Cmd, r.ID = c.name, newRequestID()
	if err := st.WriteRequest(r); err != nil {
		return r.ID, wire.Frame{}, err
	}
	f, ok := pollReply(st.ReplyPath(r.ID), c.wait, func(wire.Frame) bool { return true })
	switch {
	case !ok:
		return r.ID, f, fmt.Errorf("no answer from the relay in %s: it predates this request (update flok-relay), or the sidebar is busy", c.wait)
	case f.Error != "":
		return r.ID, f, errors.New(f.Error)
	case f.Phone == nil && f.Enroll == nil && f.Tenant == nil:
		return r.ID, f, errors.New("the relay's answer carried nothing")
	}
	return r.ID, f, nil
}

// askOnce is ask for a request whose reply is read once: the file goes.
func (c relayCmd) askOnce(r state.Request) (wire.Frame, error) {
	id, f, err := c.ask(r)
	_ = os.Remove(state.New(c.dir).ReplyPath(id))
	return f, err
}

// pollReply reads the reply file every 50 ms until it holds a frame want accepts, or until
// wait has passed.
func pollReply(path string, wait time.Duration, want func(wire.Frame) bool) (wire.Frame, bool) {
	deadline := time.Now().Add(wait)
	for {
		if f, ok := readReply(path); ok && want(f) {
			return f, true
		}
		if time.Now().After(deadline) {
			return wire.Frame{}, false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func readReply(path string) (wire.Frame, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return wire.Frame{}, false
	}
	var f wire.Frame
	if json.Unmarshal(data, &f) != nil {
		return wire.Frame{}, false
	}
	return f, true
}

// jsonLine is v as one line of JSON for a script, with & and < as they are (the pairing URL
// holds a &; encoding/json would write & for a browser that never reads this).
func jsonLine(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return b.String()
}

func newRequestID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// seenAt is a last-seen time for a table; "never" for none.
func seenAt(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format("2006-01-02 15:04")
}
