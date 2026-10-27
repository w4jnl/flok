package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// The PreToolUse payload Claude Code 2.1.310 sends for AskUserQuestion, trimmed to tool_input.
const askUserQuestionInput = `{"questions":[
 {"question":"Which fruits do you like?","header":"Fruit","options":[{"label":"Apple","description":"red or green"},{"label":"Banana","description":"yellow"},{"label":"Cherry","description":"small and dark"}],"multiSelect":true},
 {"question":"Which size?","header":"Size","options":[{"label":"Small","description":"fits a hand"},{"label":"Large","description":"needs two"}],"multiSelect":false}]}`

func TestFormFromInput(t *testing.T) {
	var input map[string]any
	if err := json.Unmarshal([]byte(askUserQuestionInput), &input); err != nil {
		t.Fatal(err)
	}
	f := FormFromInput(input)
	if f == nil || len(f.Questions) != 2 {
		t.Fatalf("form: %+v", f)
	}
	q := f.Questions[0]
	if q.Header != "Fruit" || q.Text != "Which fruits do you like?" || !q.Multi || len(q.Options) != 3 ||
		q.Options[1].Label != "Banana" || q.Options[2].Description != "small and dark" {
		t.Errorf("question 1: %+v", q)
	}
	if q := f.Questions[1]; q.Multi || len(q.Options) != 2 || q.Options[0].Label != "Small" {
		t.Errorf("question 2: %+v", q)
	}
	if s := f.Summary(160); s != "Fruit: Which fruits do you like?" {
		t.Errorf("summary %q", s)
	}
	if s := f.Summary(14); s != "Fruit: Which …" {
		t.Errorf("summary cut %q", s)
	}
	// the hook's event carries it, a question without options is dropped, no questions is no form
	ev, ok := Claude{}.MapHook(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "AskUserQuestion", "tool_input": input, "tool_use_id": "q1"})
	if !ok || ev.Kind != EvBlocked || ev.Reason != "question" || ev.Form == nil || len(ev.Form.Questions) != 2 {
		t.Errorf("hook event: %+v", ev)
	}
	// Claude Code announces the form again as a permission request for the tool (same input, no
	// tool_use_id), and its permission notification names the tool: both are the question
	ev, ok = Claude{}.MapHook(map[string]any{"hook_event_name": "PermissionRequest", "tool_name": "AskUserQuestion", "tool_input": input})
	if !ok || ev.Kind != EvBlocked || ev.Reason != "question" || ev.Tool != "AskUserQuestion" || ev.Form == nil || len(ev.Form.Questions) != 2 {
		t.Errorf("permission request for the question: %+v", ev)
	}
	ev, ok = Claude{}.MapHook(map[string]any{"hook_event_name": "Notification", "notification_type": "permission_prompt", "message": "Claude wants to use AskUserQuestion"})
	if !ok || ev.Kind != EvBlocked || ev.Reason != "question" || ev.Form != nil {
		t.Errorf("permission notification for the question: %+v", ev)
	}
	ev, _ = Claude{}.MapHook(map[string]any{"hook_event_name": "PermissionRequest", "tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}})
	if ev.Reason != "permission" || ev.Tool != "Bash" || ev.Form != nil {
		t.Errorf("a permission request stays one: %+v", ev)
	}
	if f := FormFromInput(map[string]any{"questions": []any{map[string]any{"question": "Why?", "options": []any{}}}}); f != nil {
		t.Errorf("a question without options made a form: %+v", f)
	}
	if f := FormFromInput(map[string]any{}); f != nil || f.Summary(10) != "" {
		t.Errorf("no questions made a form: %+v", f)
	}
}

func TestFormBounds(t *testing.T) {
	var qs []any
	for i := 0; i < MaxFormQuestions+2; i++ {
		var opts []any
		for j := 0; j < MaxFormOptions+3; j++ {
			opts = append(opts, map[string]any{"label": strings.Repeat("o", maxFormLabel+10), "description": strings.Repeat("d", maxFormDesc+10)})
		}
		qs = append(qs, map[string]any{"question": "  spaced\n\nout  " + strings.Repeat("q", maxFormText), "header": strings.Repeat("h", maxFormHeader+5), "options": opts, "multiSelect": true})
	}
	f := FormFromInput(map[string]any{"questions": qs})
	if f == nil || len(f.Questions) != MaxFormQuestions {
		t.Fatalf("questions kept: %d", len(f.Questions))
	}
	q := f.Questions[0]
	if len(q.Options) != MaxFormOptions {
		t.Errorf("options kept: %d", len(q.Options))
	}
	if n := len([]rune(q.Header)); n != maxFormHeader {
		t.Errorf("header %d runes", n)
	}
	if n := len([]rune(q.Text)); n != maxFormText || !strings.HasPrefix(q.Text, "spaced out q") {
		t.Errorf("text %d runes, %q", n, q.Text[:20])
	}
	if n := len([]rune(q.Options[0].Label)); n != maxFormLabel {
		t.Errorf("label %d runes", n)
	}
	if n := len([]rune(q.Options[0].Description)); n != maxFormDesc {
		t.Errorf("description %d runes", n)
	}
}
