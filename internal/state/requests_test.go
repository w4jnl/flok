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
