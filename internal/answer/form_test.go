package answer

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/agent"
)

// fakeForm stands in for Claude Code's question form in a pane: it takes the keys Fill sends
// the way the real form does (the rules at the top of form.go, learned from 2.1.310 and
// checked there with TestFillLive) and renders the screen the real one shows.
type fakeForm struct {
	form    agent.Form
	tab     int      // 0..len(questions); the last is the review
	cursor  []int    // per question: the highlighted row
	marked  [][]bool // per question: the options, then the free-text row (multi-select)
	picked  []int    // per question: a single-select's pick, -1 none, len(options) the words
	other   []string // per question: the words in the free-text row
	otherAt []int    // per question: the words' own cursor, in runes
	review  int      // the review's cursor: 0 Submit answers, 1 Cancel
	gone    bool     // submitted or cancelled: the form left the screen
	answers map[string]string
	deaf    bool // a broken form that ignores digits (the failure test)
	keys    []string
}

func newFakeForm(f agent.Form) *fakeForm {
	n := len(f.Questions)
	ff := &fakeForm{form: f, cursor: make([]int, n), marked: make([][]bool, n), picked: make([]int, n), other: make([]string, n), otherAt: make([]int, n)}
	for i, q := range f.Questions {
		ff.marked[i] = make([]bool, len(q.Options)+1)
		ff.picked[i] = -1
	}
	return ff
}

func (ff *fakeForm) run(args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("no command")
	}
	switch args[0] {
	case "capture-pane":
		return ff.render(), nil
	case "send-keys":
		rest := args[3:] // send-keys -t pane …
		if len(rest) >= 2 && rest[0] == "-l" {
			ff.typeText(rest[len(rest)-1])
			return "", nil
		}
		for _, k := range rest {
			ff.key(k)
		}
		return "", nil
	}
	return "", fmt.Errorf("unexpected tmux command %v", args)
}

// tabbed reports a form with tabs and a review: more than one question, or a multi-select. A
// lone single-select question has neither, and a pick submits it.
func (ff *fakeForm) tabbed() bool { return len(ff.form.Questions) > 1 || ff.form.Questions[0].Multi }

// pick is a single-select's choice made: on to the next tab, or submitted when there are none.
func (ff *fakeForm) pick(i, j int) {
	ff.picked[i] = j
	if ff.tabbed() {
		ff.tab++
	} else {
		ff.submit(true)
	}
}

func (ff *fakeForm) answered(i int) bool {
	if ff.form.Questions[i].Multi {
		for _, m := range ff.marked[i] {
			if m {
				return true
			}
		}
		return false
	}
	return ff.picked[i] >= 0
}

func (ff *fakeForm) answer(i int) string {
	q := ff.form.Questions[i]
	if q.Multi {
		var parts []string
		for j, m := range ff.marked[i] {
			if m && j < len(q.Options) {
				parts = append(parts, q.Options[j].Label)
			} else if m {
				parts = append(parts, ff.other[i])
			}
		}
		return strings.Join(parts, ", ")
	}
	switch p := ff.picked[i]; {
	case p < 0:
		return ""
	case p < len(q.Options):
		return q.Options[p].Label
	}
	return ff.other[i]
}

func (ff *fakeForm) typeText(text string) {
	ff.keys = append(ff.keys, "-l")
	i := ff.tab
	if ff.gone || i >= len(ff.form.Questions) || ff.cursor[i] != len(ff.form.Questions[i].Options) {
		return // words outside the free-text row go nowhere here (the real form has no field)
	}
	r := []rune(ff.other[i])
	at := ff.otherAt[i]
	r = append(r[:at], append([]rune(text), r[at:]...)...)
	ff.other[i] = string(r)
	ff.otherAt[i] = at + len([]rune(text))
	if ff.form.Questions[i].Multi {
		ff.marked[i][len(ff.form.Questions[i].Options)] = true
	}
}

func (ff *fakeForm) key(k string) {
	ff.keys = append(ff.keys, k)
	if ff.gone {
		return
	}
	last := len(ff.form.Questions)
	if ff.tab == last { // the review
		switch k {
		case "Left":
			ff.tab--
		case "Up", "Down":
			ff.review = 1 - ff.review
		case "Enter":
			ff.submit(ff.review == 0)
		case "1":
			ff.submit(true)
		case "2":
			ff.submit(false)
		}
		return
	}
	i := ff.tab
	q := ff.form.Questions[i]
	n := len(q.Options)
	rows := n + 1 // the options and the free-text row
	if q.Multi {
		rows++ // Next
	}
	c := ff.cursor[i]
	if c == n { // on the free-text row every key is text, except the ones that leave it
		switch k {
		case "Up":
			ff.cursor[i]--
		case "Down":
			if c < rows-1 {
				ff.cursor[i]++
			}
		case "Left":
			if ff.otherAt[i] > 0 {
				ff.otherAt[i]--
			}
		case "Right":
			if ff.otherAt[i] < len([]rune(ff.other[i])) {
				ff.otherAt[i]++
			}
		case "End":
			ff.otherAt[i] = len([]rune(ff.other[i]))
		case "BSpace":
			if at := ff.otherAt[i]; at > 0 {
				r := []rune(ff.other[i])
				ff.other[i] = string(append(r[:at-1], r[at:]...))
				ff.otherAt[i]--
				if ff.other[i] == "" && q.Multi {
					ff.marked[i][n] = false
				}
			}
		case "Enter":
			if q.Multi {
				ff.marked[i][n] = !ff.marked[i][n]
			} else if ff.other[i] != "" {
				ff.pick(i, n)
			}
		case "Space":
			ff.typeText(" ")
		default:
			if len(k) == 1 {
				ff.typeText(k)
			}
		}
		return
	}
	switch k {
	case "Up":
		if c > 0 {
			ff.cursor[i]--
		}
	case "Down":
		if c < rows-1 {
			ff.cursor[i]++
		}
	case "Left":
		if i > 0 {
			ff.tab--
		}
	case "Right", "Tab":
		ff.tab++
	case "Space":
		if q.Multi && c < n {
			ff.marked[i][c] = !ff.marked[i][c]
		}
	case "Enter":
		switch {
		case q.Multi && c == rows-1: // Next, or Submit under the last question
			ff.tab++
		case q.Multi:
			ff.marked[i][c] = !ff.marked[i][c]
		default:
			ff.pick(i, c)
		}
	default:
		if len(k) == 1 && k[0] >= '1' && k[0] <= '9' && !ff.deaf {
			j := int(k[0] - '1')
			switch {
			case j < n && q.Multi:
				ff.marked[i][j] = !ff.marked[i][j]
			case j < n:
				ff.pick(i, j)
			case j == n && q.Multi:
				ff.marked[i][n] = !ff.marked[i][n]
			}
		}
	}
}

func (ff *fakeForm) submit(yes bool) {
	ff.gone = true
	if !yes {
		return
	}
	ff.answers = map[string]string{}
	for i, q := range ff.form.Questions {
		ff.answers[q.Text] = ff.answer(i)
	}
}

// render draws the pane as Claude Code does, with a prompt above and the footer below.
func (ff *fakeForm) render() string {
	var b strings.Builder
	b.WriteString("❯ Call the AskUserQuestion tool once, with exactly two questions.\n\n")
	if ff.gone {
		b.WriteString("⏺ User answered Claude's questions:\n⏺ done\n")
		return b.String()
	}
	tabBox := func(i int) string {
		if ff.answered(i) {
			return "☒"
		}
		return "☐"
	}
	if ff.tabbed() {
		b.WriteString("← ")
		for i, q := range ff.form.Questions {
			fmt.Fprintf(&b, " %s %s ", tabBox(i), q.Header)
		}
		b.WriteString(" ✔ Submit  →\n")
	} else { // a lone single-select question: its header stands alone, no tabs, no review
		b.WriteString(strings.Repeat("─", 60) + "\n")
		fmt.Fprintf(&b, " %s %s\n", tabBox(0), ff.form.Questions[0].Header)
	}
	last := len(ff.form.Questions)
	if ff.tab == last {
		b.WriteString("Review your answers\n")
		for i := range ff.form.Questions {
			if !ff.answered(i) {
				b.WriteString("⚠ You have not answered all questions\n")
				break
			}
		}
		for i, q := range ff.form.Questions {
			if ff.answered(i) {
				fmt.Fprintf(&b, " ● %s\n   → %s\n", q.Text, ff.answer(i))
			}
		}
		b.WriteString("Ready to submit your answers?\n")
		mark := func(r int) string {
			if ff.review == r {
				return "❯"
			}
			return " "
		}
		fmt.Fprintf(&b, "%s 1. Submit answers\n%s 2. Cancel\n", mark(0), mark(1))
		return b.String()
	}
	i := ff.tab
	q := ff.form.Questions[i]
	n := len(q.Options)
	b.WriteString(q.Text + "\n")
	cur := func(r int) string {
		if ff.cursor[i] == r {
			return "❯"
		}
		return " "
	}
	for j, o := range q.Options {
		if q.Multi {
			box := "[ ]"
			if ff.marked[i][j] {
				box = "[✔]"
			}
			fmt.Fprintf(&b, "%s %d. %s %s\n", cur(j), j+1, box, o.Label)
		} else {
			pick := ""
			if ff.picked[i] == j {
				pick = " ✔"
			}
			fmt.Fprintf(&b, "%s %d. %s%s\n", cur(j), j+1, o.Label, pick)
		}
		if o.Description != "" {
			fmt.Fprintf(&b, "         %s\n", o.Description)
		}
	}
	words := ff.other[i]
	if q.Multi {
		box := "[ ]"
		if ff.marked[i][n] {
			box = "[✔]"
		}
		if words == "" {
			words = formPlaceholder
		}
		fmt.Fprintf(&b, "%s %d. %s %s\n", cur(n), n+1, box, words)
		row := "Next"
		if i == last-1 {
			row = "Submit"
		}
		fmt.Fprintf(&b, "%s    %s\n", cur(n+1), row)
	} else {
		if words == "" {
			words = formPlaceholder + "."
		}
		fmt.Fprintf(&b, "%s %d. %s\n", cur(n), n+1, words)
	}
	b.WriteString(strings.Repeat("─", 60) + "\n")
	fmt.Fprintf(&b, "  %d. Chat about this\n", n+2)
	b.WriteString("Enter to select · Tab/Arrow keys to navigate · Esc to cancel\n\n")
	return b.String()
}

var probeForm = agent.Form{Questions: []agent.Question{
	{Header: "Fruit", Text: "Which fruits do you like?", Multi: true, Options: []agent.Option{{Label: "Apple", Description: "red or green"}, {Label: "Banana", Description: "yellow"}, {Label: "Cherry", Description: "small and dark"}}},
	{Header: "Size", Text: "Which size?", Options: []agent.Option{{Label: "Small", Description: "fits a hand"}, {Label: "Large", Description: "needs two"}}},
}}

func quickForm(t *testing.T) {
	t.Helper()
	step, poll, final := formStep, formPoll, formFinal
	formStep, formPoll, formFinal = 200*time.Millisecond, time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { formStep, formPoll, formFinal = step, poll, final })
}

func TestFillAnswersTheForm(t *testing.T) {
	quickForm(t)
	ff := newFakeForm(probeForm)
	fa := FormAnswer{Answers: []Choice{{Selected: []int{0, 2}, Other: "Kiwi"}, {Other: "Medium"}}}
	if err := Fill(ff.run, "%0", probeForm, fa); err != nil {
		t.Fatal(err)
	}
	if ff.answers["Which fruits do you like?"] != "Apple, Cherry, Kiwi" || ff.answers["Which size?"] != "Medium" {
		t.Fatalf("answers %v", ff.answers)
	}
	for _, k := range ff.keys {
		if k == "5" || k == "4" && ff.tab == 1 { // never the row outside the form
			t.Fatalf("pressed %q: keys %v", k, ff.keys)
		}
	}
	// plain picks: digits only, no words
	ff = newFakeForm(probeForm)
	if err := Fill(ff.run, "%0", probeForm, FormAnswer{Answers: []Choice{{Selected: []int{1}}, {Selected: []int{1}}}}); err != nil {
		t.Fatal(err)
	}
	if ff.answers["Which fruits do you like?"] != "Banana" || ff.answers["Which size?"] != "Large" {
		t.Fatalf("answers %v", ff.answers)
	}
	if strings.Contains(strings.Join(ff.keys, " "), "-l") {
		t.Fatalf("typed words for a pick: %v", ff.keys)
	}
}

func TestFillFromAMessyForm(t *testing.T) {
	quickForm(t)
	// left on the review with Banana ticked, "Pear2" typed and its cursor inside, Large picked
	ff := newFakeForm(probeForm)
	ff.marked[0][1] = true
	ff.other[0], ff.otherAt[0], ff.marked[0][3] = "Pear2", 4, true
	ff.cursor[0] = 3
	ff.picked[1] = 1
	ff.tab = 2
	fa := FormAnswer{Answers: []Choice{{Selected: []int{0, 2}, Other: "Kiwi"}, {Selected: []int{0}}}}
	if err := Fill(ff.run, "%0", probeForm, fa); err != nil {
		t.Fatal(err)
	}
	if ff.answers["Which fruits do you like?"] != "Apple, Cherry, Kiwi" || ff.answers["Which size?"] != "Small" {
		t.Fatalf("answers %v", ff.answers)
	}
	// the words already there are kept, not typed twice
	ff = newFakeForm(probeForm)
	ff.other[0], ff.otherAt[0], ff.marked[0][3] = "Kiwi", 4, true
	ff.cursor[0] = 3
	if err := Fill(ff.run, "%0", probeForm, FormAnswer{Answers: []Choice{{Other: "Kiwi"}, {Selected: []int{0}}}}); err != nil {
		t.Fatal(err)
	}
	if ff.answers["Which fruits do you like?"] != "Kiwi" || strings.Contains(strings.Join(ff.keys, " "), "-l") {
		t.Fatalf("answers %v, keys %v", ff.answers, ff.keys)
	}
}

func TestFillRefuses(t *testing.T) {
	quickForm(t)
	ff := newFakeForm(probeForm)
	bad := []FormAnswer{
		{Answers: []Choice{{Selected: []int{0}}}},                                   // one answer short
		{Answers: []Choice{{}, {Selected: []int{0}}}},                               // an empty choice
		{Answers: []Choice{{Selected: []int{0}}, {Selected: []int{0, 1}}}},          // two picks on a single-select
		{Answers: []Choice{{Selected: []int{0}}, {Selected: []int{0}, Other: "x"}}}, // a pick and words
		{Answers: []Choice{{Selected: []int{3}}, {Selected: []int{0}}}},             // no such option
	}
	for i, fa := range bad {
		if err := Fill(ff.run, "%0", probeForm, fa); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if len(ff.keys) != 0 {
		t.Fatalf("a refused answer pressed keys: %v", ff.keys)
	}
	ok := FormAnswer{Answers: []Choice{{Selected: []int{0}}, {Selected: []int{0}}}}
	ff.gone = true
	if err := Fill(ff.run, "%0", probeForm, ok); err == nil || !strings.Contains(err.Error(), "no question form") {
		t.Errorf("no form on the screen: %v", err)
	}
	ff = newFakeForm(probeForm)
	ff.deaf = true
	if err := Fill(ff.run, "%0", probeForm, ok); err == nil || !strings.Contains(err.Error(), "finish it on the screen") {
		t.Errorf("a form that ignores keys: %v", err)
	}
}

// The screens captured from Claude Code 2.1.310 during the probe.
const (
	realFruit = `❯ Call the AskUserQuestion tool once, with exactly two questions. Question 1: header "Fruit", question "Which fruits do you like?", multiSelect true,
  options Apple (description "red or green"), Banana (description "yellow"), Cherry (description "small and dark").
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
←  ☒ Fruit  ☐ Size  ✔ Submit  →
Which fruits do you like?
❯ 1. [ ] Apple
         red or green
  2. [✔] Banana
         yellow
  3. [ ] Cherry
         small and dark
  4. [ ] Type something
     Next
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
  5. Chat about this
Enter to select · Tab/Arrow keys to navigate · Esc to cancel
`
	realSize = `←  ☒ Fruit  ☒ Size  ✔ Submit  →
Which size?
❯ 1. Small
     fits a hand
  2. Large ✔
     needs two
  3. Type something.
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
  4. Chat about this
Enter to select · Tab/Arrow keys to navigate · Esc to cancel
`
	realReview = `←  ☐ Fruit  ☒ Size  ✔ Submit  →
Review your answers
⚠ You have not answered all questions
 ● Which size?
   → Medium
Ready to submit your answers?
❯ 1. Submit answers
  2. Cancel
`
	realTyped = `←  ☒ Fruit  ☐ Size  ✔ Submit  →
Which fruits do you like?
  1. [ ] Apple
         red or green
  2. [ ] Banana
         yellow
  3. [ ] Cherry
         small and dark
❯ 4. [✔] Kiwi
     Next
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
  5. Chat about this
Enter to select · Tab/Arrow keys to navigate · ctrl+g to edit in Nvim · Esc to cancel
`
)

func TestParseFormReal(t *testing.T) {
	s := parseForm(realFruit)
	if !s.ok || s.review || s.question != "Which fruits do you like?" || s.cursor != 0 || len(s.rows) != 5 {
		t.Fatalf("fruit: %+v", s)
	}
	if !s.rows[1].marked || s.rows[0].marked || s.rows[1].label != "Banana" || s.rows[3].label != "Type something" || !s.rows[4].next {
		t.Errorf("fruit rows: %+v", s.rows)
	}
	s = parseForm(realSize)
	if s.question != "Which size?" || len(s.rows) != 3 || !s.rows[1].marked || s.rows[1].label != "Large" || s.rows[2].label != "Type something." || s.cursor != 0 {
		t.Errorf("size: %+v", s)
	}
	s = parseForm(realReview)
	if !s.review || !s.unanswered || len(s.rows) != 2 || s.cursor != 0 || !strings.HasPrefix(s.rows[0].label, "Submit") {
		t.Errorf("review: %+v", s)
	}
	s = parseForm(realTyped)
	if s.cursor != 3 || !s.rows[3].marked || s.rows[3].label != "Kiwi" {
		t.Errorf("typed: %+v", s)
	}
	if s := parseForm("⏺ done\n❯ \n"); s.ok {
		t.Errorf("no form parsed as one: %+v", s)
	}
	if !sameWords("Which fruits do you  like?", "Which fruits do you like?") || sameWords("Which size?", "Which fruits do you like?") {
		t.Error("sameWords")
	}
	long := strings.Repeat("word ", 20)
	if !sameWords(long+"and more on the screen", long) {
		t.Error("a long question is matched on its first 40 runes")
	}
}

// A lone single-select question (no tabs, no review; captured from 2.1.311) and a lone
// multi-select one (tabs and a review after all, its row under the words says Submit).
const (
	realLone = `❯ Call the AskUserQuestion tool once, with exactly one question: header "Size", question "Which size?", multiSelect false, options Small (description
  "fits a hand"), Large (description "needs two"). After I answer, reply with exactly the word done and nothing else.
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
 ☐ Size
Which size?
❯ 1. Small
     fits a hand
  2. Large
     needs two
  3. Type something.
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
  4. Chat about this
Enter to select · ↑/↓ to navigate · Esc to cancel
`
	realLoneMulti = `←  ☐ Fruit  ✔ Submit  →
Which fruits do you like?
❯ 1. [ ] Apple
  2. [ ] Banana
  3. [ ] Cherry
  4. [ ] Type something
     Submit
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
  5. Chat about this
Enter to select · ↑/↓ to navigate · Esc to cancel
`
	// a long question is drawn as a quote, with a bar down its left side (2.1.311)
	realBarred = ` ☐ Confirm
│ Round 2, question 3 of 3: I'll call the trading journal's server_info tool (read-only).
│ Proceed?
❯ 1. Yes, run it
     Load and call server_info, then report
  2. Skip the call
     Report that I would have run it
  3. Type something.
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
  4. Chat about this
Enter to select · ↑/↓ to navigate · Esc to cancel
`
	// Claude's todo list, boxes at the line starts, after the form was submitted
	realTodo = `⏺ User answered Claude's questions:
  ⎿  · Which size? → Large
☐ Write the tests
☒ Run them
  1. first point
  2. second point
`
)

func TestParseFormLone(t *testing.T) {
	s := parseForm(realLone)
	if !s.ok || s.review || s.question != "Which size?" || len(s.rows) != 3 || s.cursor != 0 || s.rows[2].label != "Type something." {
		t.Fatalf("lone single-select: %+v", s)
	}
	s = parseForm(realLoneMulti)
	if !s.ok || len(s.rows) != 5 || !s.rows[4].next || s.rows[4].label != "Submit" {
		t.Fatalf("lone multi-select: %+v", s)
	}
	if s := parseForm(realTodo); s.ok {
		t.Fatalf("a todo list's boxes are not a form: %+v", s)
	}
	s = parseForm(realBarred)
	want := "Round 2, question 3 of 3: I'll call the trading journal's server_info tool (read-only). Proceed?"
	if !s.ok || s.question != want || len(s.rows) != 3 {
		t.Fatalf("barred question: %+v", s)
	}
	d := formDriver{form: agent.Form{Questions: []agent.Question{{Header: "Confirm", Text: want, Options: []agent.Option{{Label: "Yes, run it"}, {Label: "Skip the call"}}}}}}
	if !d.at(s, 0) {
		t.Fatal("the barred question is not recognised as the form's")
	}
}

func TestFillOneQuestion(t *testing.T) {
	quickForm(t)
	one := agent.Form{Questions: probeForm.Questions[1:]} // Size: single-select, no tabs
	ff := newFakeForm(one)
	if s := parseForm(ff.render()); !s.ok || len(s.rows) != 3 {
		t.Fatalf("the stand-in's lone question: %+v", s)
	}
	if err := Fill(ff.run, "%0", one, FormAnswer{Answers: []Choice{{Selected: []int{1}}}}); err != nil {
		t.Fatal(err)
	}
	if ff.answers["Which size?"] != "Large" || strings.Contains(strings.Join(ff.keys, " "), "Enter") {
		t.Fatalf("a digit submits a lone single-select by itself: %v keys %v", ff.answers, ff.keys)
	}
	ff = newFakeForm(one)
	if err := Fill(ff.run, "%0", one, FormAnswer{Answers: []Choice{{Other: "Medium"}}}); err != nil {
		t.Fatal(err)
	}
	if ff.answers["Which size?"] != "Medium" {
		t.Fatalf("words on a lone single-select: %v", ff.answers)
	}
	multi := agent.Form{Questions: probeForm.Questions[:1]} // Fruit: multi-select, tabs and a review
	ff = newFakeForm(multi)
	if err := Fill(ff.run, "%0", multi, FormAnswer{Answers: []Choice{{Selected: []int{0, 2}}}}); err != nil {
		t.Fatal(err)
	}
	if ff.answers["Which fruits do you like?"] != "Apple, Cherry" {
		t.Fatalf("a lone multi-select goes through Submit and the review: %v", ff.answers)
	}
}
