package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPhoneReplies(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if ValidRequestID("abc123abc123") != true || ValidRequestID("ABC123ABC123") || ValidRequestID("../x") || ValidRequestID("abc") {
		t.Fatal("ValidRequestID")
	}
	if err := s.WritePhoneReply("../evil", map[string]any{}); err == nil {
		t.Fatal("a bad id must not name a file")
	}
	if err := s.WritePhoneReply("abc123abc123", map[string]any{"type": "phone", "id": "abc123abc123"}); err != nil {
		t.Fatal(err)
	}
	p := s.PhoneReplyPath("abc123abc123")
	if p != filepath.Join(dir, PhoneDir, "abc123abc123.json") {
		t.Fatalf("path %s", p)
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) == 0 {
		t.Fatalf("reply: %v %q", err, data)
	}
	if err := s.WriteRequest(Request{Cmd: "phone", ID: "abc123abc123", Op: "forget", Name: "iPhone"}); err != nil {
		t.Fatal(err)
	}
	reqs := s.DrainRequests(time.Now(), time.Minute)
	if len(reqs) != 1 || reqs[0].Cmd != "phone" || reqs[0].ID != "abc123abc123" || reqs[0].Op != "forget" || reqs[0].Name != "iPhone" {
		t.Fatalf("request round trip: %+v", reqs)
	}
	s.SweepPhoneReplies(time.Now(), time.Hour)
	if _, err := os.Stat(p); err != nil {
		t.Fatal("a young reply stays")
	}
	s.SweepPhoneReplies(time.Now().Add(2*time.Hour), time.Hour)
	if _, err := os.Stat(p); err == nil {
		t.Fatal("an old reply goes")
	}
}
