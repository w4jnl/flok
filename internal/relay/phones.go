package relay

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/w4jnl/flok/internal/link/wire"
)

// phone is a paired phone: the app's own key to the API, kept as a hash, and what it was paired
// as. It is what flok phone pair makes and flok phone forget removes.
type phone struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	TokenSHA256 string    `json:"token_sha256"`
	Paired      time.Time `json:"paired"`
	LastSeen    time.Time `json:"last_seen,omitzero"`
	Instance    string    `json:"instance,omitempty"` // the instance that showed the code
}

func (p phone) info() wire.PhoneInfo {
	return wire.PhoneInfo{ID: p.ID, Name: p.Name, Paired: p.Paired, LastSeen: p.LastSeen, Instance: p.Instance}
}

// phoneStore keeps the phones in phones.json under the data dir (in memory without one).
type phoneStore struct {
	path   string
	mu     sync.Mutex
	byID   map[string]phone
	byHash map[string]string    // token sha256 → id
	seen   map[string]time.Time // id → LastSeen as the file has it, so a look does not rewrite it every time
}

func loadPhones(dir string) (*phoneStore, error) {
	ps := &phoneStore{byID: map[string]phone{}, byHash: map[string]string{}, seen: map[string]time.Time{}}
	if dir == "" {
		return ps, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	ps.path = filepath.Join(dir, "phones.json")
	data, err := os.ReadFile(ps.path)
	if errors.Is(err, os.ErrNotExist) {
		return ps, nil
	}
	if err != nil {
		return nil, err
	}
	var list []phone
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, errors.New(ps.path + ": " + err.Error())
	}
	for _, p := range list {
		ps.byID[p.ID] = p
		ps.byHash[p.TokenSHA256] = p.ID
		ps.seen[p.ID] = p.LastSeen
	}
	return ps, nil
}

// ListPhones reads the phones a relay's data dir holds, for flok-relay phones.
func ListPhones(dir string) ([]wire.PhoneInfo, error) {
	ps, err := loadPhones(dir)
	if err != nil {
		return nil, err
	}
	out := []wire.PhoneInfo{}
	for _, p := range ps.list() {
		out = append(out, p.info())
	}
	return out, nil
}

// list is the phones in the order they paired.
func (ps *phoneStore) list() []phone {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := make([]phone, 0, len(ps.byID))
	for _, p := range ps.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Paired.Before(out[j].Paired) })
	return out
}

func (ps *phoneStore) count() int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return len(ps.byID)
}

func (ps *phoneStore) add(p phone) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.byID[p.ID] = p
	ps.byHash[p.TokenSHA256] = p.ID
	ps.seen[p.ID] = p.LastSeen
	if err := ps.saveLocked(); err != nil { // a phone the file does not hold is not paired
		delete(ps.byID, p.ID)
		delete(ps.byHash, p.TokenSHA256)
		delete(ps.seen, p.ID)
		return err
	}
	return nil
}

// byToken is the phone a bearer token belongs to.
func (ps *phoneStore) byToken(tok string) (phone, bool) {
	if tok == "" {
		return phone{}, false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	id, ok := ps.byHash[hashToken(tok)]
	if !ok {
		return phone{}, false
	}
	p, ok := ps.byID[id]
	return p, ok
}

// find is the phone with this id, else the one with this name (case does not matter; two
// phones of one name need the id).
func (ps *phoneStore) find(nameOrID string) (phone, bool) {
	nameOrID = strings.TrimSpace(nameOrID)
	if nameOrID == "" {
		return phone{}, false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if p, ok := ps.byID[nameOrID]; ok {
		return p, true
	}
	var found phone
	n := 0
	for _, p := range ps.byID {
		if strings.EqualFold(p.Name, nameOrID) {
			found, n = p, n+1
		}
	}
	return found, n == 1
}

func (ps *phoneStore) remove(id string) (phone, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p, ok := ps.byID[id]
	if !ok {
		return phone{}, false
	}
	delete(ps.byID, id)
	delete(ps.byHash, p.TokenSHA256)
	delete(ps.seen, id)
	_ = ps.saveLocked()
	return p, true
}

// touch notes that the phone's app connected; the file is rewritten at most every few minutes
// per phone, the memory always.
func (ps *phoneStore) touch(id string, now time.Time) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p, ok := ps.byID[id]
	if !ok {
		return
	}
	p.LastSeen = now
	ps.byID[id] = p
	if now.Sub(ps.seen[id]) < 5*time.Minute {
		return
	}
	if ps.saveLocked() == nil {
		ps.seen[id] = now
	}
}

func (ps *phoneStore) saveLocked() error {
	if ps.path == "" {
		return nil
	}
	list := make([]phone, 0, len(ps.byID))
	for _, p := range ps.byID {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Paired.Before(list[j].Paired) })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := ps.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, ps.path)
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func newPhoneID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// phoneName is the name a phone pairs under: what the app sent, trimmed to one line of at most
// 64 characters; "phone" when it sent none.
func phoneName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 64 {
		s = string(r[:64])
	}
	if s == "" {
		return "phone"
	}
	return s
}

// --- pairing codes ---------------------------------------------------------------------------

// pairAlphabet has 32 symbols, without 0/O and 1/I: eight of them are 40 bits, read aloud or
// typed without a doubt. 256 is a multiple of 32, so a random byte masked picks evenly.
const pairAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// PairCodeLen is the length of a pairing code without its dash.
const PairCodeLen = 8

func newCode() string {
	b := make([]byte, PairCodeLen)
	_, _ = rand.Read(b)
	out := make([]byte, PairCodeLen)
	for i, c := range b {
		out[i] = pairAlphabet[c&31]
	}
	return string(out)
}

// NormalizeCode is a code as typed or scanned, the way the relay compares it: upper case,
// without spaces and dashes.
func NormalizeCode(s string) string {
	s = strings.ToUpper(s)
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '‐' || r == '‑' || r == '–' {
			return -1
		}
		return r
	}, s)
}

// FormatCode is a normalised code as it is shown and scanned: XXXX-XXXX.
func FormatCode(code string) string {
	if len(code) != PairCodeLen {
		return code
	}
	return code[:4] + "-" + code[4:]
}

// pairCode is an instance's live pairing code: its hash, when it dies, and the request it was
// asked with, so the word that it was redeemed reaches the terminal that shows it.
type pairCode struct {
	hash    [32]byte
	expires time.Time
	reqID   string
}

// mintCodeLocked makes an instance's pairing code; a fresh one replaces the one it had, so one
// code per instance is live at a time. The caller holds s.mu.
func (s *Server) mintCodeLocked(instance, reqID string, now time.Time) (string, time.Time) {
	code := newCode()
	expires := now.Add(s.cfg.PairTTL)
	s.codes[instance] = pairCode{hash: sha256.Sum256([]byte(code)), expires: expires, reqID: reqID}
	return code, expires
}

// redeemCode finds the instance whose live code this is and spends it: a code is good once.
// False for one unknown, used or expired, which all look alike to the caller.
func (s *Server) redeemCode(code string, now time.Time) (instance, reqID string, ok bool) {
	h := sha256.Sum256([]byte(code))
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, pc := range s.codes {
		if !now.Before(pc.expires) {
			delete(s.codes, name)
			continue
		}
		if subtle.ConstantTimeCompare(pc.hash[:], h[:]) == 1 {
			delete(s.codes, name)
			return name, pc.reqID, true
		}
	}
	return "", "", false
}

// phoneInfos is every paired phone, for a reply.
func (s *Server) phoneInfos() []wire.PhoneInfo {
	var out []wire.PhoneInfo
	for _, p := range s.phones.list() {
		out = append(out, p.info())
	}
	return out
}

// PhoneCount is how many phones are paired, for the start-up log.
func (s *Server) PhoneCount() int { return s.phones.count() }
