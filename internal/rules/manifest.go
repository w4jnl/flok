// Package rules evaluates herdr-style TOML detection manifests against a pane's screen text
// and title. It is the fallback for agents (or moments) without hook events.
package rules

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// Matcher is the recursive condition block used by rules and their any/all/not lists.
type Matcher struct {
	Contains  []string  `toml:"contains"`
	Regex     []string  `toml:"regex"`
	LineRegex []string  `toml:"line_regex"`
	Any       []Matcher `toml:"any"`
	All       []Matcher `toml:"all"`
	Not       []Matcher `toml:"not"`

	regex     []*regexp.Regexp
	lineRegex []*regexp.Regexp
	broken    bool // a pattern did not compile in Go's RE2 dialect; the matcher never matches
}

type Rule struct {
	Matcher
	ID              string `toml:"id"`
	State           string `toml:"state"`
	Priority        int    `toml:"priority"`
	Region          string `toml:"region"`
	SkipStateUpdate bool   `toml:"skip_state_update"`
	VisibleBlocker  bool   `toml:"visible_blocker"`
	VisibleWorking  bool   `toml:"visible_working"`
	VisibleIdle     bool   `toml:"visible_idle"`
}

type Manifest struct {
	ID      string   `toml:"id"`
	Version string   `toml:"version"`
	Aliases []string `toml:"aliases"`
	Rules   []Rule   `toml:"rules"`
	Skipped []string `toml:"-"` // rule ids that could not be compiled or use unsupported regions
}

// Parse decodes a manifest and compiles its patterns.
func Parse(text string) (*Manifest, error) {
	var m Manifest
	if _, err := toml.Decode(text, &m); err != nil {
		return nil, err
	}
	if m.ID == "" {
		return nil, fmt.Errorf("manifest without id")
	}
	for i := range m.Rules {
		r := &m.Rules[i]
		if !SupportedRegion(r.Region) {
			m.Skipped = append(m.Skipped, r.ID+" (region "+r.Region+")")
			r.broken = true
			continue
		}
		if err := r.Matcher.compile(); err != nil {
			m.Skipped = append(m.Skipped, r.ID+" ("+err.Error()+")")
		}
	}
	return &m, nil
}

func (m *Matcher) compile() error {
	var firstErr error
	note := func(err error) {
		m.broken = true
		if firstErr == nil {
			firstErr = err
		}
	}
	for _, p := range m.Regex {
		re, err := regexp.Compile(p)
		if err != nil {
			note(err)
			continue
		}
		m.regex = append(m.regex, re)
	}
	for _, p := range m.LineRegex {
		re, err := regexp.Compile(p)
		if err != nil {
			note(err)
			continue
		}
		m.lineRegex = append(m.lineRegex, re)
	}
	for i := range m.Any {
		if err := m.Any[i].compile(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for i := range m.All {
		if err := m.All[i].compile(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for i := range m.Not {
		if err := m.Not[i].compile(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// match evaluates the matcher against a region: its own conditions must all hold, at least
// one `any` entry must hold (when present), every `all` entry must hold, no `not` entry may hold.
func (m *Matcher) match(text, lower string, lines []string) bool {
	if m.broken {
		return false
	}
	for _, c := range m.Contains {
		if !strings.Contains(lower, strings.ToLower(c)) {
			return false
		}
	}
	for _, re := range m.regex {
		if !re.MatchString(text) {
			return false
		}
	}
	for _, re := range m.lineRegex {
		if !anyLine(re, lines) {
			return false
		}
	}
	if len(m.Any) > 0 {
		ok := false
		for i := range m.Any {
			if m.Any[i].match(text, lower, lines) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for i := range m.All {
		if !m.All[i].match(text, lower, lines) {
			return false
		}
	}
	for i := range m.Not {
		if m.Not[i].match(text, lower, lines) {
			return false
		}
	}
	return true
}

func anyLine(re *regexp.Regexp, lines []string) bool {
	for _, l := range lines {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}
