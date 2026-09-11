package rules

import (
	"strings"

	"github.com/w4jnl/flok/internal/agent"
)

// Result of evaluating a manifest against a screen.
type Result struct {
	Matched  bool
	Hold     bool // the winning rule has skip_state_update: keep the previous state
	State    agent.State
	RuleID   string
	Priority int
}

// Evaluate runs every rule and returns the highest-priority match.
func (m *Manifest) Evaluate(s Screen) Result {
	var best Result
	cache := map[string]struct {
		text, lower string
		lines       []string
	}{}
	for i := range m.Rules {
		r := &m.Rules[i]
		if r.broken || (best.Matched && r.Priority <= best.Priority) {
			continue
		}
		c, ok := cache[r.Region]
		if !ok {
			lines := s.Region(r.Region)
			c.lines = lines
			c.text = strings.Join(lines, "\n")
			c.lower = strings.ToLower(c.text)
			cache[r.Region] = c
		}
		if len(c.lines) == 0 && r.Region != "osc_title" {
			continue
		}
		if r.Matcher.match(c.text, c.lower, c.lines) {
			best = Result{Matched: true, Hold: r.SkipStateUpdate, State: agent.State(r.State), RuleID: r.ID, Priority: r.Priority}
		}
	}
	if best.Matched && best.State == "" {
		best.State = agent.Unknown
	}
	return best
}

// Explain lists every matching rule (highest priority first) for debugging.
func (m *Manifest) Explain(s Screen) []Result {
	var out []Result
	for i := range m.Rules {
		r := &m.Rules[i]
		if r.broken {
			continue
		}
		lines := s.Region(r.Region)
		if len(lines) == 0 && r.Region != "osc_title" {
			continue
		}
		text := strings.Join(lines, "\n")
		if r.Matcher.match(text, strings.ToLower(text), lines) {
			out = append(out, Result{Matched: true, Hold: r.SkipStateUpdate, State: agent.State(r.State), RuleID: r.ID, Priority: r.Priority})
		}
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Priority > out[i].Priority {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
