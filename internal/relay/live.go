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

// overviewLocked counts every online instance's agents and picks the rows: what asks first
// (oldest first), then what finished unseen (newest first), then what works (longest first).
// The caller holds s.mu.
func (s *Server) overviewLocked() LiveState {
	st := LiveState{Rows: []LiveRow{}}
	type ranked struct {
		r    LiveRow
		rank int
	}
	var all []ranked
	for _, in := range s.instances {
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
func (s *Server) agentStateLocked(instance, pane string) LiveState {
	st := LiveState{Rows: []LiveRow{{Instance: instance, Pane: pane, State: "gone", Label: "gone"}}}
	in := s.instances[instance]
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

// liveSoonLocked schedules one round of activity pushes (a burst of snapshots is one round).
// The caller holds s.mu.
func (s *Server) liveSoonLocked() {
	if s.cfg.Push == nil || s.liveDue != nil {
		return
	}
	s.liveDue = time.AfterFunc(s.cfg.LiveDelay, s.live)
}

type liveJob struct {
	dev    device
	act    *Activity // nil: a start to dev.ActivityStart
	n      apns.Notification
	key    string // what was sent, for liveSent
	ending bool
	alert  *pendingAlert // a finished agent's push this update carries
}

// live sends what changed to every phone's activities: starts, updates, ends.
func (s *Server) live() {
	now := time.Now()
	s.mu.Lock()
	s.liveDue = nil
	ov := s.overviewLocked()
	edge := ov.active() && !s.liveWasActive
	s.liveWasActive = ov.active()
	if !ov.quiet() {
		s.liveQuietSince = time.Time{}
	} else if s.liveQuietSince.IsZero() {
		s.liveQuietSince = now
		time.AfterFunc(s.cfg.QuietEnd+50*time.Millisecond, func() { // look again when the quiet is long enough to end
			s.mu.Lock()
			s.liveSoonLocked()
			s.mu.Unlock()
		})
	}
	endOverview := ov.quiet() && now.Sub(s.liveQuietSince) >= s.cfg.QuietEnd
	var jobs []liveJob
	for _, d := range s.devices.list() {
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
					jobs = append(jobs, liveJob{dev: d, act: &a, ending: true, n: apns.Notification{
						Live: &apns.Live{Event: "end", ContentState: ov, Dismissal: now}}})
					continue
				}
				if key := ov.json(); s.liveSent[a.Token] != key || alerting {
					urgent := ov.Waiting > s.liveWaiting[a.Token]
					j := liveJob{dev: d, act: &a, key: key, n: apns.Notification{Live: &apns.Live{Event: "update", ContentState: ov, Urgent: urgent}}}
					if alerting { // the finished agent lights the overview up, with its sound, at once
						j.n.Title, j.n.Body, j.n.Sound, j.n.Live.Urgent = alert.n.Title, liveAlertBody(alert.n), alert.n.Sound, true
						j.alert, alerting = &alert, false
					}
					jobs = append(jobs, j)
				}
			case liveAgent:
				st := s.agentStateLocked(a.Instance, a.Pane)
				switch st.Rows[0].State {
				case "gone":
					jobs = append(jobs, liveJob{dev: d, act: &a, ending: true, n: apns.Notification{
						Live: &apns.Live{Event: "end", ContentState: st, Dismissal: now}}})
				case "done", "idle": // finished: shown a little longer on the Lock Screen, then gone
					jobs = append(jobs, liveJob{dev: d, act: &a, ending: true, n: apns.Notification{
						Live: &apns.Live{Event: "end", ContentState: st, Dismissal: now.Add(s.cfg.AgentLinger)}}})
				default:
					if key := st.json(); s.liveSent[a.Token] != key {
						jobs = append(jobs, liveJob{dev: d, act: &a, key: key, n: apns.Notification{
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
			jobs = append(jobs, liveJob{dev: d, n: apns.Notification{Title: "flok", Body: overviewWords(ov), Live: &apns.Live{
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
	s.mu.Unlock()
	for _, j := range jobs {
		go s.sendLive(j)
	}
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
		if j.ending {
			s.devices.dropActivity(j.dev.Token, j.act.Token, "")
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
