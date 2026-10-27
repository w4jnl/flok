package answer

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/w4jnl/flok/internal/agent"
)

// Claude Code's question form (AskUserQuestion), as the instance fills it in for the phone.
// The agent draws the form in its pane; Fill presses the keys a user would and reads the pane
// back after each, so a key that did not do what it should stops the fill with a message
// instead of typing on blindly, and the user finishes on the screen. The form, as learned from
// Claude Code 2.1.310 and pinned by TestFill against a stand-in that behaves the same:
//
//	←  ☐ Fruit  ☐ Size  ✔ Submit  →      one tab per question, the review (Submit) last;
//	Which fruits do you like?             ←/→/Tab move between tabs (← on the first stays)
//	❯ 1. [ ] Apple                        multi-select: a digit, Space or Enter toggles a row
//	         red or green                 in place; single-select: a digit picks and moves on
//	  2. [✔] Banana
//	  4. [ ] Type something               the cursor here and typed words become the row's
//	     Next                             label, picked; Enter on Next moves on
//	────────────────────────
//	  5. Chat about this                  outside the form: never touched
//	Review your answers … ❯ 1. Submit answers    Enter submits; ⚠ names unanswered questions
//
// With the cursor on the free-text row every key is text: digits type, ← and → move inside the
// words. So the cursor is put on the first row before digits or ← are pressed, and the words
// are typed last, with ↓ to Next and Enter to leave the row. The last question's row under the
// words says Submit instead of Next and leads to the review. A form of one single-select
// question has no tabs and no review: its header stands alone (" ☐ Size") over the question,
// and a digit submits it there and then.

// Timing of the fill: how long a key gets to show its effect, how often the pane is read, and
// how long the submit gets to take the form away. Variables for the tests.
var (
	formStep  = 2 * time.Second
	formPoll  = 80 * time.Millisecond
	formFinal = 4 * time.Second
)

// formPlaceholder is the label of the free-text row while it holds no words.
const formPlaceholder = "Type something"

// Fill answers the form f the pane shows with fa through run (the tmux client's Run): the
// pane is read, each question's rows are toggled or picked with digits and the user's own
// words typed into the form's field, every key checked against the pane before the next, and
// the review submitted. An error says what the form did not do; the words are never in it.
func Fill(run func(args ...string) (string, error), pane string, f agent.Form, fa FormAnswer) error {
	if len(fa.Answers) != len(f.Questions) {
		return fmt.Errorf("the form has %d questions, the answer %d", len(f.Questions), len(fa.Answers))
	}
	for i, c := range fa.Answers {
		q := f.Questions[i]
		for _, j := range c.Selected {
			if j >= len(q.Options) {
				return fmt.Errorf("question %d has %d options, not %d", i+1, len(q.Options), j+1)
			}
		}
		switch {
		case c.Empty():
			return fmt.Errorf("question %d is not answered", i+1)
		case !q.Multi && len(c.Selected) > 1, !q.Multi && len(c.Selected) == 1 && c.Other != "":
			return fmt.Errorf("question %d takes one answer: an option or your own words", i+1)
		}
	}
	d := formDriver{run: run, pane: pane, form: f}
	s, err := d.look()
	if err != nil {
		return err
	}
	if !s.ok {
		return errors.New("no question form on the screen: it may have been answered already")
	}
	// back to the first question from wherever the form was left (← on the first tab stays),
	// with the cursor off the free-text row first, where ← would move inside the words
	for tries := 0; !d.at(s, 0); tries++ {
		if tries > len(f.Questions)+1 {
			return errors.New("the form did not come back to its first question: finish it on the screen")
		}
		if i := d.which(s); i >= 0 {
			if s, err = d.moveCursor(s, i, 0); err != nil {
				return err
			}
		}
		if err := d.key("Left"); err != nil {
			return err
		}
		time.Sleep(formPoll * 2)
		if s, err = d.look(); err != nil {
			return err
		}
	}
	for i, c := range fa.Answers {
		q := f.Questions[i]
		if s, err = d.await(fmt.Sprintf("show question %d", i+1), formStep, func(s formScreen) bool { return d.at(s, i) }); err != nil {
			return err
		}
		if q.Multi {
			s, err = d.fillMulti(s, i, q, c)
		} else {
			s, err = d.fillSingle(s, i, q, c)
		}
		if err != nil {
			return err
		}
	}
	// the end: the review, or no form at all when a single question's pick submitted it itself
	if s, err = d.await("reach the review", formStep, func(s formScreen) bool { return !s.ok || s.review }); err != nil {
		return err
	}
	if !s.ok {
		return nil
	}
	if s.unanswered {
		return errors.New("the form still names an unanswered question: finish it on the screen")
	}
	if s.cursor >= 0 && s.cursor < len(s.rows) && strings.HasPrefix(s.rows[s.cursor].label, "Submit") {
		err = d.key("Enter")
	} else {
		err = d.key("1") // the first row of the review is Submit answers
	}
	if err != nil {
		return err
	}
	_, err = d.await("close after the submit", formFinal, func(s formScreen) bool { return !s.ok })
	return err
}

// formRow is one numbered row of the question shown: an option, the free-text row after them,
// or (next) the Next row under a multi-select (Submit under the last question).
type formRow struct {
	label  string
	marked bool // [✔] on a multi-select, a trailing ✔ on a single-select's picked option
	next   bool
}

// formScreen is what the pane shows of the form: nothing (ok false), a question (its words,
// its rows, where the cursor ❯ is) or the review.
type formScreen struct {
	ok         bool
	review     bool
	unanswered bool // the review warns of an unanswered question
	question   string
	rows       []formRow
	cursor     int // index into rows; -1 on none
}

var (
	formRowRe  = regexp.MustCompile(`^\s*(❯)?\s*(\d+)\.\s+(.*?)\s*$`)
	formNextRe = regexp.MustCompile(`^\s*(❯)?\s*(Next|Submit)\s*$`)
)

// parseForm reads the form out of a pane's text: from the last tab bar (the line that starts
// with ←), or the lone header of a one-question form (" ☐ Size"), to the rule under the rows.
// Lines before the first numbered row are the question, wrapped; a numbered row counts only in
// sequence, so a "2." inside a question is words. A header without the form's footer under it
// is a todo list's box, not a form.
func parseForm(text string) formScreen {
	lines := strings.Split(text, "\n")
	start, lone := -1, false
	for i, l := range lines {
		switch t := strings.TrimSpace(l); {
		case strings.HasPrefix(t, "←"):
			start, lone = i, false
		case strings.HasPrefix(t, "☐ "), strings.HasPrefix(t, "☒ "):
			start, lone = i, true
		}
	}
	if start < 0 {
		return formScreen{}
	}
	s := formScreen{ok: true, cursor: -1}
	var words []string
	numbered, footer := 0, false
	for _, l := range lines[start+1:] {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "Enter to select") {
			footer = true
			break
		}
		if strings.HasPrefix(t, "──") {
			for _, after := range lines[start+1:] { // the footer sits under the rule and the chat row
				if strings.HasPrefix(strings.TrimSpace(after), "Enter to select") {
					footer = true
				}
			}
			break
		}
		if t == "" {
			continue
		}
		if m := formNextRe.FindStringSubmatch(l); m != nil && numbered > 0 {
			if m[1] != "" {
				s.cursor = len(s.rows)
			}
			s.rows = append(s.rows, formRow{label: m[2], next: true})
			continue
		}
		if m := formRowRe.FindStringSubmatch(l); m != nil {
			if n, _ := strconv.Atoi(m[2]); n == numbered+1 {
				numbered++
				row := formRow{label: m[3]}
				if strings.HasPrefix(row.label, "[") {
					if end := strings.Index(row.label, "]"); end > 0 {
						row.marked = strings.TrimSpace(row.label[1:end]) != ""
						row.label = strings.TrimSpace(row.label[end+1:])
					}
				} else if strings.HasSuffix(row.label, "✔") {
					row.marked = true
					row.label = strings.TrimSpace(strings.TrimSuffix(row.label, "✔"))
				}
				if m[1] != "" {
					s.cursor = len(s.rows)
				}
				s.rows = append(s.rows, row)
				continue
			}
		}
		if numbered == 0 {
			// a long question is drawn as a quote, a bar down its left side: not its words
			words = append(words, strings.TrimLeft(t, "│┃ "))
		}
	}
	s.question = strings.Join(words, " ")
	s.review = strings.Contains(s.question, "Review your answers")
	s.unanswered = strings.Contains(s.question, "You have not answered")
	if lone && (!footer || numbered == 0) {
		return formScreen{}
	}
	return s
}

// sameWords reports whether what the screen shows is the text wanted: the same words, compared
// up to the first 40 runes, since the screen wraps and may cut a long text.
func sameWords(screen, want string) bool {
	fold := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	ss, ws := fold(screen), []rune(fold(want))
	if len(ws) > 40 {
		return strings.HasPrefix(ss, string(ws[:40]))
	}
	return ss == string(ws)
}

type formDriver struct {
	run  func(args ...string) (string, error)
	pane string
	form agent.Form
}

func (d formDriver) look() (formScreen, error) {
	out, err := d.run("capture-pane", "-p", "-t", d.pane)
	if err != nil {
		return formScreen{}, err
	}
	return parseForm(out), nil
}

func (d formDriver) key(keys ...string) error {
	_, err := d.run(append([]string{"send-keys", "-t", d.pane}, keys...)...)
	return err
}

// typeText types words into the form's field, literally; a trailing ";" is escaped the way
// tmux reads it, as Args does.
func (d formDriver) typeText(text string) error {
	arg := text
	if strings.HasSuffix(arg, ";") {
		arg = arg[:len(arg)-1] + `\;`
	}
	_, err := d.run("send-keys", "-t", d.pane, "-l", "--", arg)
	return hide(err, text)
}

// await reads the pane until cond holds, or timeout: then the last screen and an error naming
// what the form did not do.
func (d formDriver) await(what string, timeout time.Duration, cond func(formScreen) bool) (formScreen, error) {
	deadline := time.Now().Add(timeout)
	for {
		s, err := d.look()
		if err != nil {
			return s, err
		}
		if cond(s) {
			return s, nil
		}
		if time.Now().After(deadline) {
			return s, fmt.Errorf("the form did not %s: finish it on the screen", what)
		}
		time.Sleep(formPoll)
	}
}

// at reports whether the screen shows question i.
func (d formDriver) at(s formScreen, i int) bool {
	if !s.ok || s.review || i >= len(d.form.Questions) {
		return false
	}
	return sameWords(s.question, d.form.Questions[i].Text)
}

// which is the question the screen shows, -1 for none (the review, no form).
func (d formDriver) which(s formScreen) int {
	for i := range d.form.Questions {
		if d.at(s, i) {
			return i
		}
	}
	return -1
}

// moveCursor puts the cursor on row target of question i with Up or Down.
func (d formDriver) moveCursor(s formScreen, i, target int) (formScreen, error) {
	if s.cursor == target {
		return s, nil
	}
	if s.cursor < 0 || target >= len(s.rows) {
		return s, fmt.Errorf("question %d shows no row %d: finish it on the screen", i+1, target+1)
	}
	key, n := "Down", target-s.cursor
	if n < 0 {
		key, n = "Up", -n
	}
	keys := make([]string, n)
	for k := range keys {
		keys[k] = key
	}
	if err := d.key(keys...); err != nil {
		return s, err
	}
	return d.await(fmt.Sprintf("move to row %d of question %d", target+1, i+1), formStep, func(s formScreen) bool { return d.at(s, i) && s.cursor == target })
}

// clearOther empties the free-text row (row n) when it holds other words than wanted: the
// cursor onto it and a Backspace per rune, so typing starts from nothing.
func (d formDriver) clearOther(s formScreen, i, n int, want string) (formScreen, error) {
	if n >= len(s.rows) {
		return s, nil
	}
	label := s.rows[n].label
	if label == formPlaceholder || label == formPlaceholder+"." || label == "" || sameWords(label, want) {
		return s, nil
	}
	s, err := d.moveCursor(s, i, n)
	if err != nil {
		return s, err
	}
	keys := []string{"End"} // the words' cursor may have been left in the middle
	for range []rune(label) {
		keys = append(keys, "BSpace")
	}
	if err := d.key(append(keys, "BSpace", "BSpace")...); err != nil {
		return s, err
	}
	return d.await(fmt.Sprintf("clear the text of question %d", i+1), formStep, func(s formScreen) bool {
		return d.at(s, i) && n < len(s.rows) && (s.rows[n].label == "" || strings.HasPrefix(s.rows[n].label, formPlaceholder))
	})
}

// fillMulti toggles the rows of a multi-select to the choice, types the user's words into the
// free-text row if any, and goes on with Enter on Next.
func (d formDriver) fillMulti(s formScreen, i int, q agent.Question, c Choice) (formScreen, error) {
	n := len(q.Options)
	want := make([]bool, n+1) // the options, then the free-text row
	for _, j := range c.Selected {
		want[j] = true
	}
	var err error
	if c.Other != "" {
		if s, err = d.clearOther(s, i, n, c.Other); err != nil {
			return s, err
		}
	}
	if s, err = d.moveCursor(s, i, 0); err != nil { // off the free-text row: a digit there is text
		return s, err
	}
	for j := 0; j <= n; j++ {
		if j >= len(s.rows) {
			return s, fmt.Errorf("question %d shows %d rows, fewer than its options: finish it on the screen", i+1, len(s.rows))
		}
		if s.rows[j].marked == want[j] || (j == n && c.Other != "") { // the words mark their row themselves
			continue
		}
		if err := d.key(digit(j)); err != nil {
			return s, err
		}
		var err error
		if s, err = d.await(fmt.Sprintf("toggle option %d of question %d", j+1, i+1), formStep, func(s formScreen) bool {
			return d.at(s, i) && j < len(s.rows) && s.rows[j].marked == want[j]
		}); err != nil {
			return s, err
		}
	}
	if c.Other != "" && !(n < len(s.rows) && s.rows[n].marked && sameWords(s.rows[n].label, c.Other)) {
		var err error
		if s, err = d.moveCursor(s, i, n); err != nil {
			return s, err
		}
		if err := d.typeText(c.Other); err != nil {
			return s, err
		}
		if s, err = d.await(fmt.Sprintf("take the words for question %d", i+1), formStep, func(s formScreen) bool {
			return d.at(s, i) && n < len(s.rows) && s.rows[n].marked && sameWords(s.rows[n].label, c.Other)
		}); err != nil {
			return s, err
		}
	}
	next := -1
	for k, r := range s.rows {
		if r.next {
			next = k
		}
	}
	if next < 0 {
		return s, fmt.Errorf("question %d shows no Next or Submit row: finish it on the screen", i+1)
	}
	if s, err = d.moveCursor(s, i, next); err != nil {
		return s, err
	}
	return s, d.key("Enter")
}

// fillSingle picks the option with its digit, which moves the form on by itself, or types the
// user's words into the free-text row and picks them with Enter.
func (d formDriver) fillSingle(s formScreen, i int, q agent.Question, c Choice) (formScreen, error) {
	n := len(q.Options)
	if c.Other == "" {
		s, err := d.moveCursor(s, i, 0) // off the free-text row: a digit there is text
		if err != nil {
			return s, err
		}
		return s, d.key(digit(c.Selected[0]))
	}
	s, err := d.clearOther(s, i, n, c.Other)
	if err != nil {
		return s, err
	}
	if !(n < len(s.rows) && sameWords(s.rows[n].label, c.Other)) {
		if s, err = d.moveCursor(s, i, n); err != nil {
			return s, err
		}
		if err := d.typeText(c.Other); err != nil {
			return s, err
		}
		if s, err = d.await(fmt.Sprintf("take the words for question %d", i+1), formStep, func(s formScreen) bool {
			return d.at(s, i) && n < len(s.rows) && sameWords(s.rows[n].label, c.Other)
		}); err != nil {
			return s, err
		}
	} else if s, err = d.moveCursor(s, i, n); err != nil {
		return s, err
	}
	return s, d.key("Enter")
}

// digit is the key that toggles or picks row j (the rows are numbered from 1).
func digit(j int) string { return string(rune('1' + j)) }
