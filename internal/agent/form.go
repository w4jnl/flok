package agent

import "strings"

// Form is a question form an agent put up and waits on: Claude Code's AskUserQuestion, as its
// PreToolUse hook hands flok the tool's input. It travels with the agent's record (Reason
// "question") to the sidebar's snapshot, the relay and the phone, which renders it natively
// and answers it as choices (answer.FormAnswer) that the instance types into the form for it.
// Trimmed to what that needs; the agent's own screen stays the reference.
type Form struct {
	Questions []Question `json:"questions"`
}

// Question is one tab of the form: its short header, the question, the options offered and
// whether several may be picked. Every question also takes a text of the user's own ("Type
// something"), which is why an answer may carry one.
type Question struct {
	Header  string   `json:"header,omitempty"`
	Text    string   `json:"text"`
	Options []Option `json:"options"`
	Multi   bool     `json:"multi,omitempty"`
}

// Option is one choice of a question.
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Bounds of a form as flok keeps it. Claude Code asks at most four questions of two to four
// options; the bounds leave room, and an option beyond the ninth could not be picked with a
// digit anyway.
const (
	MaxFormQuestions = 6
	MaxFormOptions   = 9
	maxFormHeader    = 40
	maxFormText      = 1000
	maxFormLabel     = 200
	maxFormDesc      = 600
)

// FormFromInput reads a form from a tool's input, {"questions":[{"question","header",
// "options":[{"label","description"}],"multiSelect"}]}; nil when there is nothing to show (no
// question with options).
func FormFromInput(input map[string]any) *Form {
	list, _ := input["questions"].([]any)
	var f Form
	for _, item := range list {
		m, _ := item.(map[string]any)
		q := Question{Header: squash(str(m, "header"), maxFormHeader), Text: squash(str(m, "question"), maxFormText)}
		q.Multi, _ = m["multiSelect"].(bool)
		opts, _ := m["options"].([]any)
		for _, o := range opts {
			om, _ := o.(map[string]any)
			label := squash(str(om, "label"), maxFormLabel)
			if label == "" {
				continue
			}
			q.Options = append(q.Options, Option{Label: label, Description: squash(str(om, "description"), maxFormDesc)})
			if len(q.Options) == MaxFormOptions {
				break
			}
		}
		if q.Text == "" || len(q.Options) == 0 {
			continue
		}
		f.Questions = append(f.Questions, q)
		if len(f.Questions) == MaxFormQuestions {
			break
		}
	}
	if len(f.Questions) == 0 {
		return nil
	}
	return &f
}

// Summary is the form in a line, for a push or a log: the first question with its header
// ("Work folders: Which folders in ~/notes hold work notes?"), cut to n runes; "" for no form.
func (f *Form) Summary(n int) string {
	if f == nil || len(f.Questions) == 0 {
		return ""
	}
	q := f.Questions[0]
	s := q.Text
	if q.Header != "" && !strings.HasPrefix(strings.ToLower(s), strings.ToLower(q.Header)) {
		s = q.Header + ": " + s
	}
	return squash(s, n)
}
