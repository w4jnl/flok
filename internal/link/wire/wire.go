// Package wire is the frame protocol of flok on the phone: JSON frames over a WebSocket between
// a flok instance and the relay (`/link`), and between the relay and the app (`/api/ws`). One
// Frame type carries every message; Type says which fields matter. Unknown types and fields are
// ignored on both sides, so additions stay compatible; a change an older side would misread
// bumps Proto and the relay refuses the hello.
package wire

import (
	"time"

	"github.com/w4jnl/flok/internal/answer"
	"github.com/w4jnl/flok/internal/snapshot"
)

// Proto is the protocol version an instance's hello carries.
const Proto = 1

// Frame types. Instance to relay: hello, snap, event, screen, ack. Relay to instance: answer,
// seen, subscribe. Relay to app: instances, instance, event, screen, ack. App to relay: answer,
// seen, subscribe. Ping/pong are WebSocket control frames, not these.
const (
	TypeHello     = "hello"
	TypeSnap      = "snap"
	TypeEvent     = "event"
	TypeScreen    = "screen"
	TypeAck       = "ack"
	TypeAnswer    = "answer"
	TypeSeen      = "seen"
	TypeSubscribe = "subscribe"
	TypeInstances = "instances" // relay to app on connect: every instance the relay knows
	TypeInstance  = "instance"  // relay to app: one instance changed (online, offline, a new snapshot)
	TypeError     = "error"     // either way: a message for the log
)

// Frame is one message.
type Frame struct {
	Type     string             `json:"type"`
	ID       string             `json:"id,omitempty"`       // answer / seen: request id, echoed by the ack
	Instance string             `json:"instance,omitempty"` // relay <-> app: which instance a frame is about
	Hello    *Hello             `json:"hello,omitempty"`
	Snap     *snapshot.Snapshot `json:"snap,omitempty"`
	Event    *Event             `json:"event,omitempty"`
	Screen   *Screen            `json:"screen,omitempty"`
	Answer   *answer.Answer     `json:"answer,omitempty"`
	Pane     string             `json:"pane,omitempty"` // seen / subscribe: a pane ref (%12, beta:%12)
	On       bool               `json:"on,omitempty"`   // subscribe: start (true) or stop streaming the pane's screen
	Error    string             `json:"error,omitempty"`
	// Instances is the relay's view of every instance (TypeInstances), Inst one of them
	// (TypeInstance).
	Instances []Instance `json:"instances,omitempty"`
	Inst      *Instance  `json:"inst,omitempty"`
}

// Hello opens an instance's link.
type Hello struct {
	Proto    int      `json:"proto"`
	Name     string   `json:"name"`     // [link] name, else the machine's short hostname
	Version  string   `json:"version"`  // flok version
	Hostname string   `json:"hostname"` // the machine
	Features []string `json:"features,omitempty"`
}

// Event is one user-facing transition: an agent needs the user, finished, or failed.
type Event struct {
	Pane   string    `json:"pane"`           // pane ref on the instance
	Host   string    `json:"host,omitempty"` // the instance's remote host the agent runs on; "" = the instance itself
	Agent  string    `json:"agent"`          // the row name
	Kind   string    `json:"kind"`           // blocked | done | error
	Reason string    `json:"reason,omitempty"`
	At     time.Time `json:"at"`
}

// Screen is a pane's visible text, streamed while someone subscribed to it.
type Screen struct {
	Pane string `json:"pane"`
	Text string `json:"text"`
}

// Instance is what the app sees of one flok: its name, whether its link is up, and its last
// snapshot (kept while offline, so the app can show what was there).
type Instance struct {
	Name     string             `json:"name"`
	Online   bool               `json:"online"`
	Since    time.Time          `json:"since"`              // when it came online, or went offline
	Version  string             `json:"version,omitempty"`  // flok version on the instance
	Hostname string             `json:"hostname,omitempty"` // the machine
	Snap     *snapshot.Snapshot `json:"snap,omitempty"`
}

// Features an instance advertises in its hello.
const (
	FeatureAnswer = "answer" // takes answer frames
	FeatureScreen = "screen" // streams screens on subscribe
)

// InstanceFeatures is what this flok's link advertises.
var InstanceFeatures = []string{FeatureAnswer, FeatureScreen}

// MaxFrame bounds one frame from an instance (a snapshot of a busy site is a few hundred KB).
const MaxFrame = 8 << 20

// MaxAppFrame bounds one frame from the app.
const MaxAppFrame = 64 << 10
