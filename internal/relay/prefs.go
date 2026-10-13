package relay

import (
	"strings"
	"time"

	"github.com/w4jnl/flok/internal/link/wire"
)

// Prefs is what one phone wants pushed: per kind on or off, its sound and whether it breaks
// through Focus, and the agents it muted. The app owns them (its notification settings) and
// sends them with its registration and on every change; the relay only reads them, before each
// push. iOS cannot drop a push once it arrives, so the filter has to live here.
type Prefs struct {
	Kinds      map[string]KindPrefs `json:"kinds,omitempty"` // by push kind (KindPermission …); a missing kind = its defaults
	Mutes      []Mute               `json:"mutes,omitempty"`
	NoActivity bool                 `json:"no_activity,omitempty"` // no flok overview in the Dynamic Island / on the Lock Screen
	// FinishedAsNotification keeps done and error as notifications while the overview shows;
	// false: they light up the overview instead (one thing on the Lock Screen, not two).
	FinishedAsNotification bool `json:"finished_as_notification,omitempty"`
}

// KindPrefs are the settings of one push kind; the zero value is the default.
type KindPrefs struct {
	Off           bool   `json:"off,omitempty"`
	Sound         string `json:"sound,omitempty"`          // "" or "flok": flok's sound; "default": the system's; "none": silent
	TimeSensitive *bool  `json:"time_sensitive,omitempty"` // nil: on for permission and question, off otherwise
}

// Mute silences one agent, matched by instance, host and row name (a pane id changes when the
// agent is restarted, its name does not).
type Mute struct {
	Instance string     `json:"instance"`
	Host     string     `json:"host,omitempty"`
	Agent    string     `json:"agent"`
	Until    *time.Time `json:"until,omitempty"` // nil: until unmuted
}

// Push kinds, what a phone filters on: an instance's blocked event is a permission prompt or a
// question (any other wait), then done and error.
const (
	KindPermission = "permission"
	KindQuestion   = "question"
	KindDone       = "done"
	KindError      = "error"
)

// Sounds the app bundles, named in the push (flok's sidebar sounds: request for anything that
// wants you, done for a finish).
const (
	SoundRequest = "flok-request.caf"
	SoundDone    = "flok-done.caf"
)

// finishedInIsland reports whether a push of this kind goes to the phone as an alert on its
// overview activity instead of a notification: done and error, while the phone shows the
// overview, unless it asked for them as notifications. Prompts and questions always stay
// notifications: they carry the actions and break through Focus.
func (p *Prefs) finishedInIsland(kind string, hasOverview bool) bool {
	if kind != KindDone && kind != KindError || !hasOverview {
		return false
	}
	return p == nil || !p.NoActivity && !p.FinishedAsNotification
}

// PushKind sorts an event into the kind a phone filters on.
func PushKind(ev wire.Event) string {
	switch ev.Kind {
	case "blocked":
		if strings.HasPrefix(ev.Reason, "permission") {
			return KindPermission
		}
		return KindQuestion
	case "error":
		return KindError
	}
	return KindDone
}

// Allows reports whether a push for this event goes to the phone: its kind is on and the agent
// is not muted (an expired mute no longer counts). Nil prefs allow everything.
func (p *Prefs) Allows(instance string, ev wire.Event, now time.Time) bool {
	return p.filtered(instance, ev, now) == ""
}

// filtered names why the phone does not want this push ("" = it does), for the log.
func (p *Prefs) filtered(instance string, ev wire.Event, now time.Time) string {
	if p == nil {
		return ""
	}
	if kind := PushKind(ev); p.Kinds[kind].Off {
		return kind + " is off"
	}
	for _, m := range p.Mutes {
		if m.Instance == instance && m.Host == ev.Host && m.Agent == ev.Agent && (m.Until == nil || now.Before(*m.Until)) {
			return "muted"
		}
	}
	return ""
}

// Sound is the payload's sound for a kind: flok's file, "default", or "" for none.
func (p *Prefs) Sound(kind string) string {
	var k KindPrefs
	if p != nil {
		k = p.Kinds[kind]
	}
	switch k.Sound {
	case "none":
		return ""
	case "default":
		return "default"
	}
	if kind == KindDone {
		return SoundDone
	}
	return SoundRequest
}

// TimeSensitive reports whether a kind's push breaks through Focus.
func (p *Prefs) TimeSensitive(kind string) bool {
	if p != nil {
		if v := p.Kinds[kind].TimeSensitive; v != nil {
			return *v
		}
	}
	return kind == KindPermission || kind == KindQuestion
}

// prune drops expired mutes, so a stored record does not grow forever.
func (p *Prefs) prune(now time.Time) {
	if p == nil {
		return
	}
	kept := p.Mutes[:0]
	for _, m := range p.Mutes {
		if m.Until == nil || now.Before(*m.Until) {
			kept = append(kept, m)
		}
	}
	p.Mutes = kept
}

// valid bounds what a phone may store: known kinds and sounds, a sane number of mutes.
func (p *Prefs) valid() bool {
	if p == nil {
		return true
	}
	for kind, k := range p.Kinds {
		switch kind {
		case KindPermission, KindQuestion, KindDone, KindError:
		default:
			return false
		}
		switch k.Sound {
		case "", "flok", "default", "none":
		default:
			return false
		}
	}
	if len(p.Mutes) > 500 {
		return false
	}
	for _, m := range p.Mutes {
		if m.Instance == "" || m.Agent == "" || len(m.Agent) > 256 || len(m.Instance) > 128 || len(m.Host) > 128 {
			return false
		}
	}
	return true
}
