package ui

import (
	"reflect"
	"testing"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/merge"
)

type recSounder struct{ played []string }

func (r *recSounder) Play(kind string) error { r.played = append(r.played, kind); return nil }

// Hook-less agents (title/screen/registry): a watched pane is silent unless when_focused, and a
// watched turn that ends idle (never done) then sounds like a finished one.
func TestSoundTransitionsWhenFocused(t *testing.T) {
	for _, tc := range []struct {
		name        string
		whenFocused bool
		focus       string
		states      []agent.State
		want        []string
	}{
		{"watched, default", false, "%1", []agent.State{agent.Working, agent.Blocked, agent.Working, agent.Idle}, nil},
		{"watched, when_focused", true, "%1", []agent.State{agent.Working, agent.Blocked, agent.Working, agent.Idle}, []string{"blocked", "done"}},
		{"unwatched, default", false, "%2", []agent.State{agent.Working, agent.Blocked, agent.Working, agent.Done}, []string{"blocked", "done"}},
		{"looking at a done pane is silent", true, "%1", []agent.State{agent.Done, agent.Idle}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestModel(t)
			m.d.Cfg.Sounds.Enabled, m.d.Cfg.Sounds.WhenFocused, m.d.Cfg.Sounds.MinIntervalMs = true, tc.whenFocused, 0
			rec := &recSounder{}
			m.sounder = rec
			for i, st := range tc.states {
				if i == 0 {
					m.prevState["%1"] = st // the first sample is the starting point, not a transition
				}
				m.snap = merge.Snapshot{Focus: merge.Focus{PaneID: tc.focus}, Agents: []agent.Agent{{PaneID: "%1", State: st, Source: "title"}}}
				m.soundTransitions()
			}
			if !reflect.DeepEqual(rec.played, tc.want) {
				t.Fatalf("played %v, want %v", rec.played, tc.want)
			}
		})
	}
}
