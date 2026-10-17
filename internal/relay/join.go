package relay

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/w4jnl/flok/internal/link/tunnel"
	"github.com/w4jnl/flok/internal/link/wire"
)

// A flok joins a tenant the way a phone pairs, with the roles the other way round: the flok
// shows the code (flok link join, a QR in its terminal) and a member of the tenant approves it,
// from the phone (the app scans it), from a linked flok (flok link approve) or from a flok with
// an admin token (flok tenant approve, which names the tenant). The flok asks with POST
// /api/enroll (no key yet; it gets a secret to poll with, since the code is on a screen), polls
// GET /api/enroll/{id} until approved, shows who approved it and for which tenant, and only then
// accepts (POST …/accept) for its key: nothing is kept until both sides said yes.

// joining is a flok waiting for its join to be approved.
type joining struct {
	id       string
	secret   [32]byte // sha256 of the poll secret
	code     [32]byte // sha256 of the code it shows
	info     wire.Joining
	ip       string
	tenant   *tenant // set when approved
	by       string  // who approved: "phone Jaro's iPhone", "flok home", "admin home"
	approved bool
	declined bool
	done     chan struct{} // closed on approval or decline, for the poll
}

// JoinPoll bounds how long GET /api/enroll/{id} holds the request for an approval.
var JoinPoll = 20 * time.Second

const (
	maxPending      = 64 // floks waiting to be approved, over every address
	maxPendingPerIP = 3
)

// sweepPendingLocked drops the joins whose time ran out. The caller holds s.mu.
func (s *Server) sweepPendingLocked(now time.Time) {
	for id, j := range s.pending {
		if !now.Before(j.info.ExpiresAt) {
			delete(s.pending, id)
		}
	}
}

type enrollReq struct {
	Name     string `json:"name"`
	Hostname string `json:"hostname,omitempty"`
	Version  string `json:"version,omitempty"`
}

// handleEnrollStart is a flok asking to join: it gets a code to show and a secret to poll with.
func (s *Server) handleEnrollStart(w http.ResponseWriter, r *http.Request) {
	ip, now := clientIP(r), time.Now()
	if ok, retry := s.pairLimit.allow(ip, now); !ok {
		w.Header().Set("Retry-After", fmt.Sprint(int(max(retry.Round(time.Second), time.Second).Seconds())))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many attempts: try again in " + retry.Round(time.Second).String()})
		return
	}
	var req enrollReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "the flok's name is needed (name; hostname and version help the approver)"})
		return
	}
	j := &joining{id: newRequestID(), ip: ip, done: make(chan struct{}),
		info: wire.Joining{Name: phoneName(req.Name), Hostname: phoneName(req.Hostname), Version: phoneName(req.Version), From: ip, Started: now, ExpiresAt: now.Add(s.cfg.PairTTL)}}
	if j.info.Hostname == "phone" { // phoneName's default for an empty one
		j.info.Hostname = ""
	}
	if j.info.Version == "phone" {
		j.info.Version = ""
	}
	secret, code := NewToken(), newCode()
	j.secret, j.code = sha256.Sum256([]byte(secret)), sha256.Sum256([]byte(code))
	s.mu.Lock()
	s.sweepPendingLocked(now)
	mine := 0
	for _, p := range s.pending {
		if p.ip == ip {
			mine++
		}
	}
	if len(s.pending) >= maxPending || mine >= maxPendingPerIP {
		s.mu.Unlock()
		s.pairLimit.failed(ip, now)
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many floks are waiting to join: try again in a few minutes"})
		return
	}
	s.pending[j.id] = j
	s.mu.Unlock()
	s.cfg.Logf("join: %s (%s, flok %s) from %s asks to join; it shows a code good for %s", j.info.Name, j.info.Hostname, j.info.Version, ip, s.cfg.PairTTL)
	writeJSON(w, http.StatusCreated, map[string]any{"id": j.id, "secret": secret, "code": FormatCode(code), "expires_at": j.info.ExpiresAt, "poll_s": int(JoinPoll.Seconds())})
}

// pendingBySecret is the join a poll is about: the id names it, the bearer secret proves it.
func (s *Server) pendingBySecret(r *http.Request) (*joining, bool) {
	h := sha256.Sum256([]byte(bearer(r)))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepPendingLocked(time.Now())
	j := s.pending[r.PathValue("id")]
	if j == nil || subtle.ConstantTimeCompare(j.secret[:], h[:]) != 1 {
		return nil, false
	}
	return j, true
}

// handleEnrollPoll is the joining flok waiting: the request holds until the join is approved or
// declined, JoinPoll passes or the code expires; the flok polls again while it is pending.
func (s *Server) handleEnrollPoll(w http.ResponseWriter, r *http.Request) {
	j, ok := s.pendingBySecret(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such join: it expired, or was declined; run flok link join again"})
		return
	}
	select {
	case <-j.done:
	case <-time.After(min(JoinPoll, time.Until(j.info.ExpiresAt))):
	case <-r.Context().Done():
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case j.declined:
		writeJSON(w, http.StatusGone, map[string]any{"status": "declined", "error": "the join was declined"})
	case j.approved:
		writeJSON(w, http.StatusOK, map[string]any{"status": "approved", "tenant": j.tenant.info(), "approved_by": j.by, "expires_at": j.info.ExpiresAt})
	case !time.Now().Before(j.info.ExpiresAt):
		delete(s.pending, j.id)
		writeJSON(w, http.StatusGone, map[string]any{"status": "expired", "error": "the code expired: run flok link join again"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"status": "pending", "expires_at": j.info.ExpiresAt})
	}
}

// handleEnrollAccept is the joining flok saying yes to who approved it: now it gets its key, and
// a record of it is kept. A flok of that name that went offline in the tenant is replaced (the
// same machine set up again); one online was refused at the approval.
func (s *Server) handleEnrollAccept(w http.ResponseWriter, r *http.Request) {
	j, ok := s.pendingBySecret(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such join: it expired, or was declined; run flok link join again"})
		return
	}
	s.mu.Lock()
	if !j.approved || j.declined {
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{"error": "the join is not approved yet"})
		return
	}
	delete(s.pending, j.id)
	t := j.tenant
	var replaced *instance
	if in := t.instances[j.info.Name]; in != nil && in.rec != nil {
		replaced = in
	}
	s.mu.Unlock()
	if replaced != nil {
		s.forgetInstance(t, replaced.name, "replaced: this flok joined again") // closes its link, if any
	}
	key := NewToken()
	now := time.Now()
	rec := instanceRec{ID: newRequestID(), Tenant: t.id, Name: j.info.Name, KeySHA256: hashToken(key), Hostname: j.info.Hostname, Version: j.info.Version, Joined: now, ApprovedBy: j.by}
	if err := s.istore.add(rec); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	pin := ""
	if s.cfg.LinkKey != nil {
		pin, _ = tunnel.PinOf(*s.cfg.LinkKey)
	}
	s.cfg.Logf("join: %s (%s) joined %s, approved by %s", rec.Name, rec.ID, t.Name(), j.by)
	writeJSON(w, http.StatusCreated, map[string]any{"token": key, "pin": pin, "tenant": t.info(), "instance": map[string]any{"id": rec.ID, "name": rec.Name},
		"approved_by": j.by, "relay": map[string]any{"version": s.cfg.Version}})
}

// handleEnrollDecline is the joining flok giving up (Esc at the approval, or on its way out).
func (s *Server) handleEnrollDecline(w http.ResponseWriter, r *http.Request) {
	j, ok := s.pendingBySecret(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such join"})
		return
	}
	s.mu.Lock()
	delete(s.pending, j.id)
	if !j.approved && !j.declined { // a poll still holding on learns at once
		j.declined = true
		close(j.done)
	}
	s.mu.Unlock()
	s.cfg.Logf("join: %s withdrew", j.info.Name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// lookupJoin finds the flok waiting with a code, for an approver; the approver's wrong guesses
// are slowed per approver (codeLimit), since it is signed in and the code is 40 bits. The caller
// holds s.mu.
func (s *Server) lookupJoinLocked(code, approver string, now time.Time) (*joining, string) {
	if ok, retry := s.codeLimit.allow(approver, now); !ok {
		return nil, "too many wrong codes: try again in " + retry.Round(time.Second).String()
	}
	s.sweepPendingLocked(now)
	nc := NormalizeCode(code)
	if len(nc) != PairCodeLen {
		return nil, "a join code is eight letters and digits, as flok link join shows it"
	}
	h := sha256.Sum256([]byte(nc))
	for _, j := range s.pending {
		if subtle.ConstantTimeCompare(j.code[:], h[:]) == 1 {
			s.codeLimit.succeeded(approver)
			return j, ""
		}
	}
	s.codeLimit.failed(approver, now)
	return nil, "no flok is waiting with that code: it expired, or was mistyped (run flok link join there again)"
}

// approveJoinLocked lets a waiting flok into a tenant, in the name of who approves it. A flok
// of that name online in the tenant stops it: the approver would kick it out without knowing.
// The caller holds s.mu.
func (s *Server) approveJoinLocked(j *joining, t *tenant, by string) string {
	if j.approved {
		return "that flok is approved already"
	}
	if in := t.instances[j.info.Name]; in != nil && in.online && in.rec != nil {
		return fmt.Sprintf("%s is online in %s already: set [link] name on the new one, or flok link forget %s first", j.info.Name, t.Name(), j.info.Name)
	}
	j.tenant, j.by, j.approved = t, by, true
	close(j.done)
	s.cfg.Logf("join: %s (%s) approved into %s by %s", j.info.Name, j.info.Hostname, t.Name(), by)
	return ""
}

type approveReq struct {
	Code string `json:"code"`
}

// handleEnrollLookup is the app asking what flok waits with a code, before it approves.
func (s *Server) handleEnrollLookup(w http.ResponseWriter, r *http.Request) {
	who := callerOf(r)
	s.mu.Lock()
	j, why := s.lookupJoinLocked(r.PathValue("code"), "phone:"+who.phone, time.Now())
	s.mu.Unlock()
	if why != "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": why})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pending": j.info, "tenant": who.t.info()})
}

// handleEnrollApprove is the app (a paired phone, or a static device token) letting a flok into
// its own tenant.
func (s *Server) handleEnrollApprove(w http.ResponseWriter, r *http.Request) {
	var req approveReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "the code the flok shows is needed (code)"})
		return
	}
	who := callerOf(r)
	by := "phone " + who.name
	if who.phone == "" {
		by = "a device token"
	}
	s.mu.Lock()
	j, why := s.lookupJoinLocked(req.Code, "phone:"+who.phone, time.Now())
	if why == "" {
		why = s.approveJoinLocked(j, who.t, by)
	}
	s.mu.Unlock()
	if why != "" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": why})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pending": j.info, "tenant": who.t.info()})
}

// enrollOp answers an instance's enroll frame: lookup and approve for a flok waiting with a
// code, list of its tenant's floks, forget to revoke one. The reply goes back under the frame's id.
func (s *Server) enrollOp(in *instance, gen uint64, id string, e wire.Enroll) {
	reply := wire.Frame{Type: wire.TypeEnroll, ID: id, Enroll: &wire.Enroll{Op: e.Op, Tenant: in.tenant.Name()}}
	now := time.Now()
	switch e.Op {
	case "lookup", "approve":
		s.mu.Lock()
		j, why := s.lookupJoinLocked(e.Code, "flok:"+in.tenant.id+"/"+in.name, now)
		if why == "" && e.Op == "approve" {
			why = s.approveJoinLocked(j, in.tenant, "flok "+in.name)
		}
		if why == "" {
			info := j.info
			reply.Enroll.Pending = &info
		}
		s.mu.Unlock()
		reply.Error = why
	case "list":
		s.mu.Lock()
		reply.Enroll.Instances = s.instanceInfosLocked(in.tenant)
		s.mu.Unlock()
	case "forget":
		name := strings.TrimSpace(e.Name)
		if name == "" {
			reply.Error = "a flok's name or id is needed (flok link list)"
			break
		}
		info, why := s.forgetInstance(in.tenant, name, wire.CloseInstanceForgotten)
		if why != "" {
			reply.Error = why
			break
		}
		reply.Enroll.Instance = &info
		s.mu.Lock()
		reply.Enroll.Instances = s.instanceInfosLocked(in.tenant)
		s.mu.Unlock()
		s.cfg.Logf("instance %s forgotten by %s", info.Name, in.label())
	default:
		reply.Error = "unknown enroll request " + e.Op
	}
	s.replyTo(in, gen, reply)
}

// instanceInfosLocked is a tenant's floks for a reply: those that joined by code (from their
// records) and those linking with a token from the config, online or remembered. The caller
// holds s.mu.
func (s *Server) instanceInfosLocked(t *tenant) []wire.InstanceInfo {
	var out []wire.InstanceInfo
	seen := map[string]bool{}
	for _, in := range t.instances {
		if in.hello.Name == "" && in.rec == nil { // a subscription's placeholder, never linked
			continue
		}
		out = append(out, in.info())
		if in.rec != nil {
			seen[in.rec.ID] = true
		}
	}
	for _, r := range s.istore.list(t.id) {
		if !seen[r.ID] {
			out = append(out, r.info())
		}
	}
	sortInfos(out)
	return out
}

func sortInfos(list []wire.InstanceInfo) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].Name < list[j-1].Name; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// forgetInstance revokes a flok of a tenant: its key stops working, its link is closed with the
// reason and the phones drop it. A flok linking with a token from the config cannot be revoked
// here (the token is in the relay's config); offline, it is dropped from the view. The reason
// it could not be done is returned as text.
func (s *Server) forgetInstance(t *tenant, nameOrID, reason string) (wire.InstanceInfo, string) {
	s.mu.Lock()
	in := t.instances[nameOrID]
	rec, hasRec := s.istore.find(t.id, nameOrID)
	if in == nil && hasRec {
		in = t.instances[rec.Name]
	}
	if in == nil && !hasRec {
		s.mu.Unlock()
		return wire.InstanceInfo{}, "no flok " + nameOrID + " in " + t.Name() + " (flok link list)"
	}
	if in != nil && in.rec == nil && !hasRec && in.online {
		s.mu.Unlock()
		return wire.InstanceInfo{}, in.name + " links with a token from the relay's config: take the token out there, or set another on that flok"
	}
	var info wire.InstanceInfo
	var conn frameConn
	liveRec := ""
	if in != nil {
		info = in.info()
		if in.online {
			conn = in.conn
		}
		if in.rec != nil {
			liveRec = in.rec.ID
		}
		for id, ch := range in.pending {
			ch <- "the instance was forgotten"
			delete(in.pending, id)
		}
		delete(t.instances, in.name)
		delete(t.codes, in.name)
		s.fanout(t, wire.Frame{Type: wire.TypeInstance, Instance: in.name, Inst: &wire.Instance{Name: in.name, Since: time.Now(), Gone: true}})
		s.liveSoonLocked(t)
		s.syncSoonLocked(t)
	} else {
		info = rec.info()
	}
	s.mu.Unlock()
	if hasRec {
		s.istore.remove(rec.ID)
	}
	if liveRec != "" && liveRec != rec.ID {
		s.istore.remove(liveRec)
	}
	if conn != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, reason)
	}
	return info, ""
}

// tenantOp answers an admin instance's tenant frame: the tenants and their counts, one tenant's
// members, a waiting flok approved into a tenant (made when new), a tenant renamed, a tenant
// removed with all it holds. An instance whose link holds no admin token is told so.
func (s *Server) tenantOp(in *instance, gen uint64, id string, q wire.Tenant, admin bool) {
	reply := wire.Frame{Type: wire.TypeTenant, ID: id, Tenant: &wire.Tenant{Op: q.Op}}
	if !admin {
		reply.Error = "this flok's link holds no admin token: put one of the relay's admin_tokens in [link] admin (flok reload) to manage tenants"
		s.replyTo(in, gen, reply)
		return
	}
	now := time.Now()
	switch q.Op {
	case "list":
		s.mu.Lock()
		for _, rec := range s.tstore.list() {
			reply.Tenant.Tenants = append(reply.Tenant.Tenants, s.tenantInfoLocked(rec, false))
		}
		s.mu.Unlock()
	case "show":
		rec, ok := s.tstore.find(q.Name)
		if !ok {
			reply.Error = "no tenant " + strings.TrimSpace(q.Name) + " (flok tenant list)"
			break
		}
		s.mu.Lock()
		info := s.tenantInfoLocked(rec, true)
		s.mu.Unlock()
		reply.Tenant.Tenant = &info
	case "approve":
		name := strings.TrimSpace(q.Name)
		if name == "" {
			reply.Error = "the tenant's name is needed (--tenant; a new name makes a new tenant)"
			break
		}
		rec, ok := s.tstore.find(name)
		if !ok && !ValidTenantName(name) {
			reply.Error = "a tenant's name is letters, digits, dots, dashes and underscores, at most 64"
			break
		}
		s.mu.Lock()
		j, why := s.lookupJoinLocked(q.Code, "flok:"+in.tenant.id+"/"+in.name, now)
		if why == "" && !ok {
			rec = tenantRec{ID: newTenantID(), Name: name, Created: now, By: in.name}
			if err := s.tstore.add(rec); err != nil {
				why = err.Error()
			} else {
				s.cfg.Logf("tenant %s (%s) made by %s", rec.Name, rec.ID, in.label())
			}
		}
		var t *tenant
		if why == "" {
			t = s.tenantLocked(rec.ID)
			why = s.approveJoinLocked(j, t, "admin "+in.name)
		}
		if why == "" {
			info, p := s.tenantInfoLocked(rec, false), j.info
			reply.Tenant.Tenant, reply.Tenant.Pending = &info, &p
		}
		s.mu.Unlock()
		reply.Error = why
	case "rename":
		rec, ok := s.tstore.find(q.Name)
		if !ok {
			reply.Error = "no tenant " + strings.TrimSpace(q.Name) + " (flok tenant list)"
			break
		}
		old := rec.Name
		rec, why := s.tstore.rename(rec.ID, q.NewName)
		if why != "" {
			reply.Error = why
			break
		}
		s.mu.Lock()
		if t := s.tenantLocked(rec.ID); t != nil {
			t.setName(rec.Name)
			s.fanout(t, s.snapshotFrameLocked(t)) // the phones learn the name
		}
		info := s.tenantInfoLocked(rec, false)
		s.mu.Unlock()
		reply.Tenant.Tenant = &info
		s.cfg.Logf("tenant %s renamed to %s by %s", old, rec.Name, in.label())
	case "remove":
		rec, ok := s.tstore.find(q.Name)
		switch {
		case !ok:
			reply.Error = "no tenant " + strings.TrimSpace(q.Name) + " (flok tenant list)"
		case rec.ID == DefaultTenant:
			reply.Error = "the relay's own tenant stays: it is where the config's tokens link in"
		default:
			info := s.removeTenant(rec, in)
			reply.Tenant.Tenant = &info
		}
	default:
		reply.Error = "unknown tenant request " + q.Op
	}
	s.replyTo(in, gen, reply)
}

// tenantInfoLocked is a tenant with its counts, and with its members when asked. The caller
// holds s.mu.
func (s *Server) tenantInfoLocked(rec tenantRec, members bool) wire.TenantInfo {
	info := wire.TenantInfo{ID: rec.ID, Name: rec.Name, Created: rec.Created, Static: rec.ID == DefaultTenant}
	t := s.tenantLocked(rec.ID)
	floks := s.instanceInfosLocked(t)
	info.Instances = len(floks)
	for _, f := range floks {
		if f.Online {
			info.Online++
		}
	}
	phones := s.phoneInfos(rec.ID)
	info.Phones = len(phones)
	if members {
		info.Floks, info.PhoneList = floks, phones
	}
	return info
}

// removeTenant takes a tenant out with everything it holds: its floks' keys and links, its
// phones' keys, devices and streams.
func (s *Server) removeTenant(rec tenantRec, by *instance) wire.TenantInfo {
	s.mu.Lock()
	info := s.tenantInfoLocked(rec, true)
	t := s.tenants[rec.ID]
	s.mu.Unlock()
	for _, f := range info.Floks {
		if t != nil {
			_, _ = s.forgetInstance(t, f.Name, "forgotten: the tenant was removed from the relay")
		}
	}
	for _, r := range s.istore.list(rec.ID) { // records no live instance carried
		s.istore.remove(r.ID)
	}
	for _, p := range info.PhoneList {
		_, _ = s.forgetPhone(p.ID)
	}
	s.mu.Lock()
	delete(s.tenants, rec.ID)
	s.mu.Unlock()
	s.tstore.remove(rec.ID)
	s.cfg.Logf("tenant %s (%s) removed by %s: %d floks, %d phones", rec.Name, rec.ID, by.label(), info.Instances, info.Phones)
	return info
}

// newRequestID is an id for a join or a joined flok: 12 hex digits, like a request id in flok.
func newRequestID() string { return newPhoneID() }
