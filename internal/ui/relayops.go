package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/state"
)

// relayRequest is `flok phone`, `flok link approve | list | forget` or `flok tenant` asking
// through the mailbox: the question goes to the relay over the link, and what comes back
// (linkCommand) is written where the command reads it (Store.WriteReply). Without a working link
// the answer is the reason, at once.
func (m *Model) relayRequest(r state.Request) tea.Cmd {
	if !state.ValidRequestID(r.ID) || m.d.Store == nil {
		return nil
	}
	why := ""
	switch {
	case m.linker == nil:
		why = "no relay link: set [link] url in config.toml, or flok link join <relay> (docs/phone.md has the setup)"
	case m.linkStatus.State != link.Connected:
		why = "the relay link is " + string(m.linkStatus.State)
		if m.linkStatus.Detail != "" {
			why += ": " + m.linkStatus.Detail
		}
		why += " (flok link status)"
	}
	if why != "" {
		m.writeReply(r.ID, wire.Frame{Type: r.Cmd, ID: r.ID, Error: why})
		return nil
	}
	switch r.Cmd {
	case "phone":
		m.linker.Phone(r.ID, wire.Phone{Op: r.Op, Name: r.Name})
	case "link":
		m.linker.Enroll(r.ID, wire.Enroll{Op: r.Op, Code: r.Code, Name: r.Name})
	case "tenant":
		m.linker.Tenant(r.ID, wire.Tenant{Op: r.Op, Code: r.Code, Name: r.Name})
	}
	return nil
}

// linkPhone is the relay's answer to a phone request, or its word that a phone took the code a
// request asked for: both go to the request's reply file; a pairing is logged too.
func (m *Model) linkPhone(c link.Command) tea.Cmd {
	m.writeReply(c.ID, wire.Frame{Type: wire.TypePhone, ID: c.ID, Phone: c.Phone, Error: c.Error})
	if c.Phone != nil && c.Phone.Op == "paired" && c.Phone.Phone != nil && m.d.Store != nil {
		m.d.Store.AppendEvent(map[string]any{"at": time.Now(), "source": "phone", "cmd": "phone", "op": "paired",
			"name": c.Phone.Phone.Name, "phone": c.Phone.Phone.ID})
		m.debugf("link: phone %s paired", c.Phone.Phone.Name)
	}
	return nil
}

func (m *Model) writeReply(id string, f wire.Frame) {
	if m.d.Store == nil {
		return
	}
	if err := m.d.Store.WriteReply(id, f); err != nil {
		m.debugf("reply %s: %v", id, err)
	}
}
