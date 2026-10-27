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

// Frame types. Instance to relay: hello, snap, event, screen, ack, bye, phone, enroll, tenant.
// Relay to instance: answer, seen, subscribe, phone, enroll, tenant. Relay to app: instances, instance, event, screen, ack. App to
// relay: answer, seen, subscribe. Ping/pong are WebSocket control frames, not these. Inside the
// encrypted link (/link/tls) the instance's first frame is auth with its token, and the relay
// answers auth (go on) or error (refused) before the hello.
const (
	TypeAuth      = "auth"
	TypeHello     = "hello"
	TypeSnap      = "snap"
	TypeEvent     = "event"
	TypeScreen    = "screen"
	TypeAck       = "ack"
	TypeBye       = "bye" // instance to relay, before it closes on purpose: Reason says why ("off")
	TypeAnswer    = "answer"
	TypeSeen      = "seen"
	TypeSubscribe = "subscribe"
	TypePhone     = "phone"     // instance to relay: pair | list | forget (ID = the request); relay to instance: the reply under that ID, or paired, unsolicited, under the ID the code was asked with
	TypeEnroll    = "enroll"    // instance to relay: lookup | approve | list | forget, about floks joining its tenant (ID = the request); relay to instance: the reply
	TypeTenant    = "tenant"    // an instance linked with an admin token to relay: list | show | approve | remove (ID = the request); relay to instance: the reply
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
	Token    string             `json:"token,omitempty"`  // auth: the instance token, only ever inside the tunnel
	Admin    string             `json:"admin,omitempty"`  // auth: an admin token presented besides the instance's own ([link] admin), only ever inside the tunnel
	Reason   string             `json:"reason,omitempty"` // bye: ByeOff
	Phone    *Phone             `json:"phone,omitempty"`  // phone: a pairing request and its reply
	Enroll   *Enroll            `json:"enroll,omitempty"` // enroll: a request about a joining flok and its reply
	Tenant   *Tenant            `json:"tenant,omitempty"` // tenant: an admin's request and its reply
	// Instances is the relay's view of every instance (TypeInstances), Inst one of them
	// (TypeInstance); TenantInfo names the tenant they belong to (TypeInstances).
	Instances  []Instance  `json:"instances,omitempty"`
	Inst       *Instance   `json:"inst,omitempty"`
	TenantInfo *TenantInfo `json:"tenant_info,omitempty"`
}

// Enroll is what flok link asks the relay over the link about the floks of its tenant: the one
// waiting with a code (lookup), that it may join (approve), which are joined (list), and one to
// revoke (forget). A joining flok's key never crosses this link: the flok alone gets it, once,
// from the relay when it accepts the approval.
type Enroll struct {
	Op        string         `json:"op"`                  // lookup | approve | list | forget
	Code      string         `json:"code,omitempty"`      // lookup, approve: the code the joining flok shows
	Name      string         `json:"name,omitempty"`      // forget: the flok's name or id, as typed
	Pending   *Joining       `json:"pending,omitempty"`   // lookup, approve: the flok that asked
	Instances []InstanceInfo `json:"instances,omitempty"` // list, forget: the tenant's floks (after the forget)
	Instance  *InstanceInfo  `json:"instance,omitempty"`  // forget: the one it was
	Tenant    string         `json:"tenant,omitempty"`    // replies: the tenant acted in
}

// Joining is a flok waiting for its join to be approved: what the approver sees before saying
// yes, as the flok sent it with POST /api/enroll.
type Joining struct {
	Name      string    `json:"name"`
	Hostname  string    `json:"hostname,omitempty"`
	Version   string    `json:"version,omitempty"`
	From      string    `json:"from,omitempty"` // the address it asked from
	Started   time.Time `json:"started"`
	ExpiresAt time.Time `json:"expires_at"`
}

// InstanceInfo is one flok of a tenant as the relay keeps it, without its key.
type InstanceInfo struct {
	ID         string    `json:"id,omitempty"` // "" for one linking with a token from the relay's config
	Name       string    `json:"name"`
	Hostname   string    `json:"hostname,omitempty"`
	Version    string    `json:"version,omitempty"`
	Joined     time.Time `json:"joined,omitzero"`
	LastSeen   time.Time `json:"last_seen,omitzero"`
	Online     bool      `json:"online"`
	ApprovedBy string    `json:"approved_by,omitempty"`
	Static     bool      `json:"static,omitempty"` // links with a token from the relay's config
}

// Tenant is what flok tenant asks the relay (over the link of an instance that holds an admin
// token) and what it answers. A tenant is the set of floks and phones that see each other.
type Tenant struct {
	Op      string       `json:"op"`                 // list | show | approve | rename | remove
	Name    string       `json:"name,omitempty"`     // show, remove, approve, rename: the tenant's name
	NewName string       `json:"new_name,omitempty"` // rename: what it is called from now on
	Code    string       `json:"code,omitempty"`     // approve: the joining flok's code
	Tenants []TenantInfo `json:"tenants,omitempty"`  // list
	Tenant  *TenantInfo  `json:"tenant,omitempty"`   // show, approve, rename, remove: the one it is about
	Pending *Joining     `json:"pending,omitempty"`  // approve: the flok approved
}

// TenantInfo is one tenant as the relay keeps it: its counts, and for show its members.
type TenantInfo struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Created   time.Time      `json:"created,omitzero"`
	Static    bool           `json:"static,omitempty"` // the relay's own: the tokens in its config link and sign in here
	Instances int            `json:"instances"`
	Online    int            `json:"online"`
	Phones    int            `json:"phones"`
	Floks     []InstanceInfo `json:"floks,omitempty"`      // show
	PhoneList []PhoneInfo    `json:"phone_list,omitempty"` // show
}

// CloseInstanceForgotten is the reason the relay closes an instance's link with when it was
// forgotten (flok link forget, or its tenant removed): its key is gone, it joins again or not.
const CloseInstanceForgotten = "forgotten: this flok was removed from the relay (flok link join joins it again)"

// Phone is what flok phone asks the relay over the link (pair, list, forget) and what the relay
// answers: a pairing code for the QR, the phones paired, the one a request was about. The relay
// never sends a phone's key over the link; the phone alone gets it, once, from POST /api/pair.
type Phone struct {
	Op        string      `json:"op"`                  // pair | list | forget; paired: the relay's word that a code was redeemed
	Name      string      `json:"name,omitempty"`      // forget: the phone's name or id, as typed
	Code      string      `json:"code,omitempty"`      // pair: the code, AB7Q-K2MN
	ExpiresAt time.Time   `json:"expires_at,omitzero"` // pair: when the code dies
	Phones    []PhoneInfo `json:"phones,omitempty"`    // list, forget: the phones paired (after the forget)
	Phone     *PhoneInfo  `json:"phone,omitempty"`     // paired, forget: the one it is about
}

// PhoneInfo is one paired phone as the relay keeps it, without its key.
type PhoneInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Paired   time.Time `json:"paired"`
	LastSeen time.Time `json:"last_seen,omitzero"`
	Instance string    `json:"instance,omitempty"` // the instance that showed the code
	Tenant   string    `json:"tenant,omitempty"`   // the tenant it belongs to (flok-relay phones, flok tenant show)
}

// CloseForgotten is the reason the relay closes an app's stream with when its phone was
// forgotten (flok phone forget): the app asks to pair again instead of retrying.
const CloseForgotten = "forgotten: pair this phone again"

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
	Pane     string    `json:"pane"`           // pane ref on the instance
	Host     string    `json:"host,omitempty"` // the instance's remote host the agent runs on; "" = the instance itself
	Agent    string    `json:"agent"`          // the row name
	Kind     string    `json:"kind"`           // blocked | done | error
	Reason   string    `json:"reason,omitempty"`
	Question string    `json:"question,omitempty"` // blocked by a question: its first question in a line, for the push's body
	At       time.Time `json:"at"`
}

// Screen is a pane's visible text, streamed while someone subscribed to it.
type Screen struct {
	Pane string `json:"pane"`
	Text string `json:"text"`
}

// ByeOff: the link was switched off on the instance (flok link off); it stays away until it is
// switched on again, which is not an outage.
const ByeOff = "off"

// Instance is what the app sees of one flok: its name, whether its link is up, and its last
// snapshot (kept while offline, so the app can show what was there).
type Instance struct {
	Name     string             `json:"name"`
	Online   bool               `json:"online"`
	Off      bool               `json:"off,omitempty"`      // offline because its link was switched off there (flok link off)
	Since    time.Time          `json:"since"`              // when it came online, or went offline
	Version  string             `json:"version,omitempty"`  // flok version on the instance
	Hostname string             `json:"hostname,omitempty"` // the machine
	Features []string           `json:"features,omitempty"` // what its link can do (FeatureLongText, FeatureFiles): the app shows what the instance takes
	Snap     *snapshot.Snapshot `json:"snap,omitempty"`
	Gone     bool               `json:"gone,omitempty"` // forgotten on the relay: the app drops it
}

// Features an instance advertises in its hello.
const (
	FeatureAnswer   = "answer"    // takes answer frames
	FeatureScreen   = "screen"    // streams screens on subscribe
	FeatureLongText = "long-text" // an answer's text may be long and multi-line (pasted); without it at most answer.MaxText on one line
	FeatureFiles    = "files"     // an answer may carry a file, saved next to the agent and typed as its path
	FeaturePhone    = "phone"     // sends phone frames (flok phone pair | list | forget) and takes the replies
	FeatureEnroll   = "enroll"    // sends enroll and tenant frames (flok link approve | list | forget, flok tenant) and takes the replies
	FeatureForm     = "form"      // an answer may fill a question form (answer.Answer.Form): the instance types the choices into the agent's form
)

// InstanceFeatures is what this flok's link advertises.
var InstanceFeatures = []string{FeatureAnswer, FeatureScreen, FeatureLongText, FeatureFiles, FeaturePhone, FeatureEnroll, FeatureForm}

// MaxFrame bounds one frame from an instance (a snapshot of a busy site is a few hundred KB).
const MaxFrame = 8 << 20

// MaxRelayFrame bounds one frame from the relay to an instance: an answer with files (answer.MaxBody).
const MaxRelayFrame = 36 << 20

// MaxAppFrame bounds one frame from the app on its stream; an answer with a file goes over HTTP
// (POST /api/answer, answer.MaxBody).
const MaxAppFrame = 64 << 10
