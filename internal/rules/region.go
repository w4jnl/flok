package rules

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Screen is what the engine looks at: the recent pane lines (oldest first), the title, and the
// OSC 9;4 progress payload ("4;<state>;<percent>", empty when unknown).
type Screen struct {
	Lines    []string
	Title    string
	Progress string
}

// OSCProgress renders tmux's pane_pb_state / pane_pb_progress as the OSC 9;4 payload the
// manifests match: hidden 0, normal 1, error 2, indeterminate 3, paused 4.
func OSCProgress(state, percent string) string {
	n, ok := map[string]int{"hidden": 0, "normal": 1, "error": 2, "indeterminate": 3, "paused": 4}[state]
	if !ok {
		return ""
	}
	if percent == "" {
		percent = "0"
	}
	return fmt.Sprintf("4;%d;%s", n, percent)
}

// WithProgress attaches the pane's progress-bar state (see OSCProgress).
func (s Screen) WithProgress(state, percent string) Screen {
	s.Progress = OSCProgress(state, percent)
	return s
}

var (
	bottomRe = regexp.MustCompile(`^bottom_non_empty_lines\((\d+)\)$`)
	topRe    = regexp.MustCompile(`^top_non_empty_lines\((\d+)\)$`)
	ruleRe   = regexp.MustCompile(`^[\s─━═╌┄┈╍┅┉]{3,}$`)
	asciiHR  = regexp.MustCompile(`^\s*-{10,}\s*$`)
	promptRe = regexp.MustCompile(`^\s*[›❯❭>]\s?`)
)

// SupportedRegion reports whether the engine can produce the region.
func SupportedRegion(r string) bool {
	switch {
	case r == "osc_title", r == "osc_progress", r == "whole_recent", r == "after_last_horizontal_rule", r == "prompt_box_body",
		r == "last_non_empty_above_prompt_box", r == "after_last_prompt_marker",
		r == "whole_recent_without_current_prompt_marker":
		return true
	case bottomRe.MatchString(r), topRe.MatchString(r):
		return true
	}
	return false
}

func isRule(line string) bool {
	t := strings.TrimSpace(line)
	return t != "" && (ruleRe.MatchString(t) || asciiHR.MatchString(t))
}

// NewScreen trims trailing blank lines from a capture.
func NewScreen(capture, title string) Screen {
	lines := strings.Split(strings.ReplaceAll(capture, "\r\n", "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return Screen{Lines: lines, Title: title}
}

// Region returns the lines of a named region (nil when the region is unsupported).
func (s Screen) Region(name string) []string {
	switch name {
	case "osc_title":
		return []string{s.Title}
	case "osc_progress":
		if s.Progress == "" {
			return nil
		}
		return []string{s.Progress}
	case "whole_recent":
		return s.Lines
	case "after_last_horizontal_rule":
		if i := s.lastRule(len(s.Lines)); i >= 0 {
			return s.Lines[i+1:]
		}
		return s.Lines
	case "prompt_box_body":
		bottom := s.lastRule(len(s.Lines))
		if bottom < 0 {
			return s.Lines
		}
		top := s.lastRule(bottom)
		if top < 0 {
			return s.Lines[bottom+1:]
		}
		return s.Lines[top+1 : bottom]
	case "last_non_empty_above_prompt_box":
		bottom := s.lastRule(len(s.Lines))
		top := -1
		if bottom >= 0 {
			top = s.lastRule(bottom)
		}
		if top < 0 {
			top = bottom
		}
		for i := top - 1; i >= 0; i-- {
			if strings.TrimSpace(s.Lines[i]) != "" {
				return []string{s.Lines[i]}
			}
		}
		return nil
	case "after_last_prompt_marker":
		for i := len(s.Lines) - 1; i >= 0; i-- {
			if promptRe.MatchString(s.Lines[i]) {
				return s.Lines[i+1:]
			}
		}
		return s.Lines
	case "whole_recent_without_current_prompt_marker":
		for i := len(s.Lines) - 1; i >= 0; i-- {
			if promptRe.MatchString(s.Lines[i]) {
				out := append([]string{}, s.Lines[:i]...)
				return append(out, s.Lines[i+1:]...)
			}
		}
		return s.Lines
	}
	if m := bottomRe.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		var out []string
		for i := len(s.Lines) - 1; i >= 0 && len(out) < n; i-- {
			if strings.TrimSpace(s.Lines[i]) != "" {
				out = append([]string{s.Lines[i]}, out...)
			}
		}
		return out
	}
	if m := topRe.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		var out []string
		for _, l := range s.Lines {
			if len(out) >= n {
				break
			}
			if strings.TrimSpace(l) != "" {
				out = append(out, l)
			}
		}
		return out
	}
	return nil
}

// lastRule returns the index of the last horizontal-rule line before limit, or -1.
func (s Screen) lastRule(limit int) int {
	for i := limit - 1; i >= 0; i-- {
		if isRule(s.Lines[i]) {
			return i
		}
	}
	return -1
}
