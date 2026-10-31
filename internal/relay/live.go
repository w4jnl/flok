package relay

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/relay/apns"
	"github.com/w4jnl/flok/internal/snapshot"
)

// Live Activities: the phone's menu bar. The overview counts what works, what waits on the user
// and what finished unseen over every online instance, with the most urgent agents as rows; it
// starts by push when the first agent starts working or asking (an idle → busy edge, to phones
// that gave a push-to-start token), follows every change that matters, and ends once all has
// been quiet for a while. An agent's own activity, started from the app, follows that agent
// and ends a little after it finished. Only state changes are pushed (a tool change is not):
// the rows carry when their state began and the app's timers tick by themselves.

// LiveState is the activity's ContentState; the keys are the Swift property names, because
// ActivityKit decodes it with a plain JSONDecoder, and every field is always present.
type LiveState struct {
	Working int       `json:"working"`
	Waiting int       `json:"waiting"` // blocked: a prompt or a question
	Done    int       `json:"done"`    // finished, not seen yet
	Rows    []LiveRow `json:"rows"`
}

// LiveRow is one agent in an activity.
type LiveRow struct {
	Instance string  `json:"instance"`
	Host     string  `json:"host"`
	Agent    string  `json:"agent"`
	Pane     string  `json:"pane"`  // pane ref on the instance, for the app's link
	State    string  `json:"state"` // working | blocked | done | idle | gone
	Label    string  `json:"label"` // the sidebar's words, without the tool of a working agent
	Since    float64 `json:"since"` // Unix seconds the state began; 0 = unknown
}

// LiveAttributes are the activity's attributes (FlokActivityAttributes in the app).
type LiveAttributes struct {
	Kind     string `json:"kind"` // overview | agent
	Instance string `json:"instance"`
	Pane     string `json:"pane"`
	Agent    string `json:"agent"`
}

const (
	liveOverview  = "overview"
	liveAgent     = "agent"
	liveRows      = 3
	attributeType = "FlokActivityAttributes"
)

func (st LiveState) active() bool { return st.Working+st.Waiting > 0 }
func (st LiveState) quiet() bool  { return st.Working+st.Waiting+st.Done == 0 }

func (st LiveState) json() string {
	b, _ := json.Marshal(st)
	return string(b)
}

// liveRow is an agent of a snapshot as a row.
func liveRow(instance string, a snapshot.Agent) LiveRow {
	ref := a.PaneID
	if a.Host != "" {
		ref = a.Host + ":" + a.PaneID
	}
	r := LiveRow{Instance: instance, Host: a.Host, Agent: a.Name, Pane: ref}
	if !a.StateSince.IsZero() {
		r.Since = float64(a.StateSince.Unix())
	}
	switch a.State {
	case agent.Working:
		r.State, r.Label = "working", "working"
	case agent.Blocked:
		r.State, r.Label = "blocked", needsYou(a.Reason)
	case agent.Done:
		r.State, r.Label = "done", "done"
		if a.Reason == "error" {
			r.Label = "failed"
		}
	default:
		r.State, r.Label = "idle", string(a.State)
	}
	return r
}

// needsYou is the sidebar's words for a block.
func needsYou(reason string) string {
	switch {
	case strings.HasPrefix(reason, "permission"):
		if tool := strings.TrimPrefix(strings.TrimPrefix(reason, "permission"), ":"); tool != "" {
			return "needs you: perm:" + tool
		}
		return "needs you: perm"
	case reason != "":
		return "needs you: " + reason
	}
	return "needs you"
}

// overviewLocked counts the agents of a tenant's online instances and picks the rows: what asks
// first (oldest first), then what finished unseen (newest first), then what works (longest
// first). The caller holds s.mu.
func (s *Server) overviewLocked(t *tenant) LiveState {
	st := LiveState{Rows: []LiveRow{}}
	type ranked struct {
		r    LiveRow
		rank int
	}
	var all []ranked
	for _, in := range t.instances {
		if !in.online || in.snap == nil {
			continue
		}
		for _, a := range in.snap.Agents {
			switch {
			case a.State == agent.Blocked:
				st.Waiting++
				all = append(all, ranked{liveRow(in.name, a), 0})
			case a.State == agent.Done && a.Unseen > 0:
				st.Done++
				all = append(all, ranked{liveRow(in.name, a), 1})
			case a.State == agent.Working:
				st.Working++
				all = append(all, ranked{liveRow(in.name, a), 2})
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.r.Since != b.r.Since {
			if a.rank == 1 {
				return a.r.Since > b.r.Since
			}
			return a.r.Since < b.r.Since
		}
		return a.r.Instance+a.r.Pane < b.r.Instance+b.r.Pane
	})
	for i := 0; i < len(all) && i < liveRows; i++ {
		st.Rows = append(st.Rows, all[i].r)
	}
	return st
}

// agentStateLocked is a followed agent's activity content: its one row, gone when the instance
// no longer lists it. The caller holds s.mu.
func (s *Server) agentStateLocked(t *tenant, instance, pane string) LiveState {
	st := LiveState{Rows: []LiveRow{{Instance: instance, Pane: pane, State: "gone", Label: "gone"}}}
	in := t.instances[instance]
	if in == nil || in.snap == nil {
		return st
	}
	for _, a := range in.snap.Agents {
		r := liveRow(instance, a)
		if r.Pane != pane {
			continue
		}
		switch r.State {
		case "working":
			st.Working = 1
		case "blocked":
			st.Waiting = 1
		case "done":
			st.Done = 1
		}
		st.Rows[0] = r
		return st
	}
	return st
}

// liveSoonLocked schedules one round of activity pushes to a tenant's phones (a burst of
// snapshots is one round). The caller holds s.mu.
func (s *Server) liveSoonLocked(t *tenant) {
	if s.cfg.Push == nil || t.liveDue != nil {
		return
	}
	t.liveDue = time.AfterFunc(s.cfg.LiveDelay, func() { s.live(t) })
}

// liveEnd is an activity's end on its way. One end push at priority 5, forgotten once the
// service took it, left the island showing a stale overview for hours when that push was
// lost: the end now goes at priority 10 and again every EndRetry while the phone still lists
// the activity, up to EndGiveUp. The phone's report without the activity (the app sees it end
// and reports), or the service's 410 for its token, closes it.
type liveEnd struct {
	dev         string    // the device's token: pruned by that phone's report
	first, last time.Time // the first end sent, the last
	dismissal   time.Time // the end's dismissal date, the same on every resend
}

type liveJob struct {
	t      *tenant
	dev    device
	act    *Activity // nil: a start to dev.ActivityStart
	n      apns.Notification
	key    string // what was sent, for liveSent
	ending bool
	alert  *pendingAlert // a finished agent's push this update carries
}

// live sends what changed to the activities of a tenant's phones: starts, updates, ends.
func (s *Server) live(t *tenant) {
	now := time.Now()
	s.mu.Lock()
	t.liveDue = nil
	ov := s.overviewLocked(t)
	edge := ov.active() && !t.liveWasActive
	t.liveWasActive = ov.active()
	if !ov.quiet() {
		t.liveQuietSince = time.Time{}
	} else if t.liveQuietSince.IsZero() {
		t.liveQuietSince = now
		time.AfterFunc(s.cfg.QuietEnd+50*time.Millisecond, func() { // look again when the quiet is long enough to end
			s.mu.Lock()
			s.liveSoonLocked(t)
			s.mu.Unlock()
		})
	}
	endOverview := ov.quiet() && now.Sub(t.liveQuietSince) >= s.cfg.QuietEnd
	devs := s.devices.listTenant(t.id)
	s.pruneEndingLocked(devs, now)
	var jobs []liveJob
	var drops [][2]string // device token, activity token: ends given up on
	retry := false
	// end is an activity's end job: the first time, or again once EndRetry passed since the last;
	// nil while the last is fresh, and nil for good (the activity dropped) after EndGiveUp
	end := func(d device, a Activity, st LiveState, dismissal time.Time) *liveJob {
		e, ok := s.liveEnding[a.Token]
		switch {
		case !ok:
			e = liveEnd{dev: d.Token, first: now, last: now, dismissal: dismissal}
		case now.Sub(e.first) >= s.cfg.EndGiveUp:
			s.cfg.Logf("live: end %s → %s: not reported gone in %s, forgotten", a.Kind, d.label(), s.cfg.EndGiveUp.Round(time.Second))
			s.endDoneLocked(a.Token)
			drops = append(drops, [2]string{d.Token, a.Token})
			return nil
		case now.Sub(e.last) < s.cfg.EndRetry:
			return nil
		default:
			e.last = now
		}
		s.liveEnding[a.Token] = e
		retry = true
		return &liveJob{t: t, dev: d, act: &a, ending: true, n: apns.Notification{Live: &apns.Live{Event: "end", ContentState: st, Dismissal: e.dismissal, Urgent: true}}}
	}
	for _, d := range devs {
		off := d.Prefs != nil && d.Prefs.NoActivity
		alert, alerting := s.liveAlert[d.Token]
		delete(s.liveAlert, d.Token)
		hasOverview := false
		for i := range d.Activities {
			a := d.Activities[i]
			switch a.Kind {
			case liveOverview:
				hasOverview = true
				if off || endOverview {
					if j := end(d, a, ov, now); j != nil {
						jobs = append(jobs, *j)
					}
					continue
				}
				delete(s.liveEnding, a.Token) // busy again before the phone reported the end: the activity goes on, if it is still there
				if key := ov.json(); s.liveSent[a.Token] != key || alerting {
					urgent := ov.Waiting > s.liveWaiting[a.Token]
					j := liveJob{t: t, dev: d, act: &a, key: key, n: apns.Notification{Live: &apns.Live{Event: "update", ContentState: ov, Urgent: urgent}}}
					if alerting { // the finished agent lights the overview up, with its sound, at once
						j.n.Title, j.n.Body, j.n.Sound, j.n.Live.Urgent = alert.n.Title, liveAlertBody(alert.n), alert.n.Sound, true
						j.alert, alerting = &alert, false
					}
					jobs = append(jobs, j)
				}
			case liveAgent:
				st := s.agentStateLocked(t, a.Instance, a.Pane)
				switch st.Rows[0].State {
				case "gone":
					if j := end(d, a, st, now); j != nil {
						jobs = append(jobs, *j)
					}
				case "done", "idle": // finished: shown a little longer on the Lock Screen, then gone
					if j := end(d, a, st, now.Add(s.cfg.AgentLinger)); j != nil {
						jobs = append(jobs, *j)
					}
				default:
					delete(s.liveEnding, a.Token)
					if key := st.json(); s.liveSent[a.Token] != key {
						jobs = append(jobs, liveJob{t: t, dev: d, act: &a, key: key, n: apns.Notification{
							Live: &apns.Live{Event: "update", ContentState: st, Urgent: st.Waiting > s.liveWaiting[a.Token]}}})
					}
				}
			}
		}
		if alerting { // the overview is gone (ended, or switched off) since: the notification after all
			go s.sendAlert(d, alert.n, alert.what, alert.kind)
		}
		pending := !s.liveStarted[d.Token].IsZero() && now.Sub(s.liveStarted[d.Token]) < 8*time.Hour
		if edge && !off && !hasOverview && !pending && d.ActivityStart != "" {
			jobs = append(jobs, liveJob{t: t, dev: d, n: apns.Notification{Title: "flok", Body: overviewWords(ov), Live: &apns.Live{
				Event: "start", ContentState: ov, AttributesType: attributeType, Attributes: LiveAttributes{Kind: liveOverview}}}})
			s.liveStarted[d.Token] = now // until the phone reports the activity (or 8 h, its longest life)
		}
	}
	for _, j := range jobs { // claimed before sending, so an overlapping round does not send it again
		if j.act != nil && !j.ending {
			s.liveSent[j.act.Token] = j.key
			if st, ok := j.n.Live.ContentState.(LiveState); ok {
				s.liveWaiting[j.act.Token] = st.Waiting
			}
		}
	}
	if retry { // look again when the next resend is due (nothing else may move while all is quiet)
		time.AfterFunc(s.cfg.EndRetry+50*time.Millisecond, func() {
			s.mu.Lock()
			s.liveSoonLocked(t)
			s.mu.Unlock()
		})
	}
	s.mu.Unlock()
	for _, dt := range drops {
		s.devices.dropActivity(dt[0], dt[1], "")
	}
	for _, j := range jobs {
		go s.sendLive(j)
	}
}

// pruneEndingLocked closes the ends of activities their phones no longer list (the app saw
// them end and reported), and of phones that are gone: an end older than EndGiveUp belongs to
// a device no round lists any more. The caller holds s.mu.
func (s *Server) pruneEndingLocked(devs []device, now time.Time) {
	mine, listed := map[string]bool{}, map[string]bool{}
	for _, d := range devs {
		mine[d.Token] = true
		for _, a := range d.Activities {
			listed[a.Token] = true
		}
	}
	for tok, e := range s.liveEnding {
		if mine[e.dev] && !listed[tok] || now.Sub(e.first) > s.cfg.EndGiveUp+time.Hour {
			s.endDoneLocked(tok)
		}
	}
}

// endDoneLocked forgets an activity whose end is through (or given up on). The caller holds s.mu.
func (s *Server) endDoneLocked(token string) {
	delete(s.liveEnding, token)
	delete(s.liveSent, token)
	delete(s.liveWaiting, token)
}

func (s *Server) sendLive(j liveJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tok, what := j.dev.ActivityStart, "start"
	if j.act != nil {
		tok, what = j.act.Token, j.n.Live.Event+" "+j.act.Kind
	}
	err := s.cfg.Push.Send(ctx, apns.Device{Token: tok, Sandbox: j.dev.Sandbox}, j.n)
	switch {
	case errors.Is(err, apns.ErrUnregistered): // an ended activity, a stale start token: only that token goes
		s.cfg.Logf("live: %s → %s: token gone, forgotten", what, j.dev.label())
		if j.act != nil {
			s.devices.dropActivity(j.dev.Token, j.act.Token, "")
		} else {
			s.devices.dropActivity(j.dev.Token, "", j.dev.ActivityStart)
		}
		s.forgetLive(j)
		if j.act != nil {
			s.mu.Lock()
			s.endDoneLocked(j.act.Token)
			if j.act.Kind == liveOverview && !j.ending && j.t != nil {
				j.t.liveWasActive = false // the overview had ended on the phone while agents work: the next round may start one
				s.liveSoonLocked(j.t)
			}
			s.mu.Unlock()
		}
		if j.alert != nil { // the overview is not there to light up: the notification after all
			s.sendAlert(j.dev, j.alert.n, j.alert.what, j.alert.kind)
		}
	case err != nil:
		s.cfg.Logf("live: %s → %s: %v", what, j.dev.label(), err)
		s.forgetLive(j)
		if j.alert != nil {
			s.sendAlert(j.dev, j.alert.n, j.alert.what, j.alert.kind)
		}
	default:
		s.cfg.Logf("live: %s → %s", what, j.dev.label())
		if j.alert != nil {
			s.cfg.Logf("push: %s in the island (%s)", j.alert.what, j.alert.kind)
		}
		if j.ending { // the activity stays listed until the phone reports it gone (live resends the end meanwhile)
			s.forgetLive(j)
		}
	}
}

// liveAlertBody is the body of a finished agent's alert on the overview: the notification's state
// and where it runs ("✓ done · home · Hugo"), since the alert has no subtitle.
func liveAlertBody(n apns.Notification) string {
	if n.Subtitle == "" {
		return n.Body
	}
	return n.Body + " · " + n.Subtitle
}

// forgetLive drops what was remembered as sent, so the next round sends it again (or a new
// start may go out).
func (s *Server) forgetLive(j liveJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j.act != nil {
		delete(s.liveSent, j.act.Token)
		delete(s.liveWaiting, j.act.Token)
	} else {
		delete(s.liveStarted, j.dev.Token)
	}
}

// overviewWords is the start alert's body: "2 working · 1 needs you".
func overviewWords(st LiveState) string {
	var parts []string
	if st.Working > 0 {
		parts = append(parts, strconv.Itoa(st.Working)+" working")
	}
	switch {
	case st.Waiting == 1:
		parts = append(parts, "1 needs you")
	case st.Waiting > 1:
		parts = append(parts, strconv.Itoa(st.Waiting)+" need you")
	}
	if st.Done > 0 {
		parts = append(parts, strconv.Itoa(st.Done)+" done")
	}
	return strings.Join(parts, " · ")
}
