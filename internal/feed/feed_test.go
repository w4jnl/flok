package feed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if it, ok := Parse([]byte(line)); ok {
			got = append(got, it.ID+" "+it.Kind+" "+it.Tool+" "+strings.ReplaceAll(it.Text, "\n", "⏎"))
		}
	}
	want := []string{
		"u1 prompt  fix the failing test in notes please",
		"a2 text  Looking at the test first.⏎⏎- one⏎- two",
		"a3 tool Bash Ran Run the notes tests",
		"a4 tool Read Read record.go",
		"sys1 note  Context compacted",
		"u4 prompt  like this⏎⏎[1 image]",
		"a5 tool AskUserQuestion Asked: Which branch should it go to?",
		"a6 text  Fixed: the test expected the old reason.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("items:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLine(t *testing.T) {
	cases := []struct {
		tool  string
		input map[string]any
		want  string
	}{
		{"Bash", map[string]any{"command": "make test\nmake vet"}, "Ran make test"},
		{"Bash", map[string]any{"command": "ls", "description": "List the files"}, "Ran List the files"},
		{"Edit", map[string]any{"file_path": "/a/b/model.go"}, "Edited model.go"},
		{"Write", map[string]any{"file_path": "/a/b/new.go"}, "Wrote new.go"},
		{"Grep", map[string]any{"pattern": "func Parse"}, "Searched for func Parse"},
		{"Agent", map[string]any{"description": "Review the diff"}, "Started an agent: Review the diff"},
		{"WebFetch", map[string]any{"url": "https://pkg.go.dev/x?y=1"}, "Fetched pkg.go.dev"},
		{"Skill", map[string]any{"skill": "commit"}, "Used the skill commit"},
		{"AskUserQuestion", map[string]any{}, "Asked a question"},
		{"mcp__github__create_pr", map[string]any{}, "Used github: create_pr"},
		{"Bash", map[string]any{"description": strings.Repeat("x", 200)}, "Ran " + strings.Repeat("x", 115) + "…"},
	}
	for _, c := range cases {
		if got := Line(c.tool, c.input); got != c.want {
			t.Errorf("Line(%s) = %q, want %q", c.tool, got, c.want)
		}
	}
}

func TestTail(t *testing.T) {
	data, _ := os.ReadFile("testdata/transcript.jsonl")
	lines := strings.SplitAfter(strings.TrimSpace(string(data))+"\n", "\n")
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	write := func(s string) {
		f, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		f.WriteString(s)
		f.Close()
	}
	tail := NewTail(path)
	// no file yet: the reader still gets its (empty) Reset
	f, err := tail.Next()
	if err != nil || f == nil || !f.Reset || len(f.Items) != 0 {
		t.Fatalf("first Next without a file: %+v, %v", f, err)
	}
	write(strings.Join(lines[:6], ""))
	f, _ = tail.Next()
	if f == nil || f.Reset || len(f.Items) != 3 || f.Items[2].Text != "Ran Run the notes tests" {
		t.Fatalf("after 6 lines: %+v", f)
	}
	if f, _ = tail.Next(); f != nil {
		t.Fatalf("nothing new, got %+v", f)
	}
	// a line arriving in two pieces is read once it is whole
	half := len(lines[6]) / 2
	write(lines[6][:half])
	if f, _ = tail.Next(); f != nil {
		t.Fatalf("half a line is not an item: %+v", f)
	}
	write(lines[6][half:] + strings.Join(lines[7:], ""))
	f, _ = tail.Next()
	if f == nil || len(f.Items) != 5 || f.Items[0].Text != "Read record.go" || f.Items[4].ID != "a6" {
		t.Fatalf("the rest: %+v", f)
	}
	// the file shrank (another session took the name): a Reset with what it holds now
	os.WriteFile(path, []byte(lines[1]), 0o644)
	f, _ = tail.Next()
	if f == nil || !f.Reset || len(f.Items) != 1 || f.Items[0].Kind != KindPrompt {
		t.Fatalf("after truncation: %+v", f)
	}
}

func TestTailKeepsMaxItems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	var b strings.Builder
	for i := 0; i < MaxItems+10; i++ {
		b.WriteString(`{"type":"assistant","uuid":"a` + strings.Repeat("0", 3) + string(rune('a'+i%26)) + `","message":{"role":"assistant","content":[{"type":"text","text":"line"}]}}` + "\n")
	}
	os.WriteFile(path, []byte(b.String()), 0o644)
	f, _ := NewTail(path).Next()
	if len(f.Items) != MaxItems {
		t.Fatalf("kept %d items, want %d", len(f.Items), MaxItems)
	}
}
