package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRequestsRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	now := time.Now()
	if err := s.WriteRequest(Request{Cmd: "goto", Host: "beta", Pane: "%3", At: now.Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteRequest(Request{Cmd: "front", At: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteRequest(Request{Cmd: "goto", Host: "beta", Pane: "%1", At: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(s.Dir, RequestsDir, "0.json"), []byte("{"), 0o644)
	got := s.DrainRequests(now, 10*time.Second)
	if len(got) != 2 || got[0].Cmd != "goto" || got[0].Pane != "%3" || got[1].Cmd != "front" || got[1].Host != "" {
		t.Fatalf("drain: %+v", got)
	}
	if files, _ := filepath.Glob(filepath.Join(s.Dir, RequestsDir, "*.json")); len(files) != 0 {
		t.Fatalf("drain must remove every file, left %v", files)
	}
	if got := s.DrainRequests(now, time.Second); len(got) != 0 {
		t.Fatal("empty after a drain")
	}
}

// Two drains at once (the store watcher's and the poll tick's) hand out every request once.
func TestDrainRequestsOnce(t *testing.T) {
	s := New(t.TempDir())
	now := time.Now()
	const n = 40
	for i := 0; i < n; i++ {
		if err := s.WriteRequest(Request{Cmd: "front", Host: "h", At: now.Add(time.Duration(i) * time.Microsecond)}); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan int, 8)
	for d := 0; d < 8; d++ {
		go func() { results <- len(s.DrainRequests(now, time.Minute)) }()
	}
	total := 0
	for d := 0; d < 8; d++ {
		total += <-results
	}
	if total != n {
		t.Fatalf("%d requests drained by 8 drains at once, want %d", total, n)
	}
	if files, _ := filepath.Glob(filepath.Join(s.Dir, RequestsDir, "*")); len(files) != 0 {
		t.Fatalf("left behind: %v", files)
	}
}
