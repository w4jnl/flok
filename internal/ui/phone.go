package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/state"
)

// phoneRequest is `flok phone pair | list | forget` asking through the mailbox: the question
// goes to the relay over the link, and what comes back (linkPhone) is written where the command
// reads it (Store.WritePhoneReply). Without a working link the answer is the reason, at once.
func (m *Model) phoneRequest(r state.Request) tea.Cmd {
	if !state.ValidRequestID(r.ID) || m.d.Store == nil {
		return nil
	}
	why := ""
	switch {
	case m.linker == nil:
		why = "no relay link: set [link] url in config.toml (docs/phone.md has the setup)"
	case m.linkStatus.State != link.Connected:
		why = "the relay link is " + string(m.linkStatus.State)
		if m.linkStatus.Detail != "" {
			why += ": " + m.linkStatus.Detail
		}
		why += " (flok link status)"
	}
	if why != "" {
		m.phoneReply(r.ID, wire.Frame{Type: wire.TypePhone, ID: r.ID, Error: why})
		return nil
	}
	m.linker.Phone(r.ID, wire.Phone{Op: r.Op, Name: r.Name})
	return nil
}

// linkPhone is the relay's answer to a phone request, or its word that a phone took the code a
// request asked for: both go to the request's reply file; a pairing is logged too.
func (m *Model) linkPhone(c link.Command) tea.Cmd {
	m.phoneReply(c.ID, wire.Frame{Type: wire.TypePhone, ID: c.ID, Phone: c.Phone, Error: c.Error})
	if c.Phone != nil && c.Phone.Op == "paired" && c.Phone.Phone != nil && m.d.Store != nil {
		m.d.Store.AppendEvent(map[string]any{"at": time.Now(), "source": "phone", "cmd": "phone", "op": "paired",
			"name": c.Phone.Phone.Name, "phone": c.Phone.Phone.ID})
		m.debugf("link: phone %s paired", c.Phone.Phone.Name)
	}
	return nil
}

func (m *Model) phoneReply(id string, f wire.Frame) {
	if m.d.Store == nil {
		return
	}
	if err := m.d.Store.WritePhoneReply(id, f); err != nil {
		m.debugf("phone: reply %s: %v", id, err)
	}
}
