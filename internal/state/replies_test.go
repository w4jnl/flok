package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReplies(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if ValidRequestID("abc123abc123") != true || ValidRequestID("ABC123ABC123") || ValidRequestID("../x") || ValidRequestID("abc") {
		t.Fatal("ValidRequestID")
	}
	if err := s.WriteReply("../evil", map[string]any{}); err == nil {
		t.Fatal("a bad id must not name a file")
	}
	if err := s.WriteReply("abc123abc123", map[string]any{"type": "phone", "id": "abc123abc123"}); err != nil {
		t.Fatal(err)
	}
	p := s.ReplyPath("abc123abc123")
	if p != filepath.Join(dir, RepliesDir, "abc123abc123.json") {
		t.Fatalf("path %s", p)
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) == 0 {
		t.Fatalf("reply: %v %q", err, data)
	}
	if err := s.WriteRequest(Request{Cmd: "phone", ID: "abc123abc123", Op: "forget", Name: "iPhone"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteRequest(Request{Cmd: "tenant", ID: "abc123abc124", Op: "approve", Code: "AB7Q-K2MN", Tenant: "acme"}); err != nil {
		t.Fatal(err)
	}
	reqs := s.DrainRequests(time.Now(), time.Minute)
	if len(reqs) != 2 || reqs[0].Cmd != "phone" || reqs[0].ID != "abc123abc123" || reqs[0].Op != "forget" || reqs[0].Name != "iPhone" ||
		reqs[1].Cmd != "tenant" || reqs[1].Code != "AB7Q-K2MN" || reqs[1].Tenant != "acme" {
		t.Fatalf("request round trip: %+v", reqs)
	}
	s.SweepReplies(time.Now(), time.Hour)
	if _, err := os.Stat(p); err != nil {
		t.Fatal("a young reply stays")
	}
	s.SweepReplies(time.Now().Add(2*time.Hour), time.Hour)
	if _, err := os.Stat(p); err == nil {
		t.Fatal("an old reply goes")
	}
}

func TestLinkFile(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if _, ok := s.LoadLink(); ok {
		t.Fatal("no file yet")
	}
	l := LinkFile{Relay: "https://flok.example.net", Instance: "abc123abc123", Name: "office", Tenant: "jaro", Token: "k3y", Pin: "sha256:ab", ApprovedBy: "phone Jaro's iPhone", Joined: time.Now().Round(time.Second)}
	if err := s.SaveLink(l); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(dir, LinkJoinedFile)); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the key is private: %v %v", err, st.Mode())
	}
	got, ok := s.LoadLink()
	if !ok || got.Relay != l.Relay || got.Instance != l.Instance || got.Name != l.Name || got.Tenant != l.Tenant || got.Token != l.Token || got.Pin != l.Pin || got.ApprovedBy != l.ApprovedBy || !got.Joined.Equal(l.Joined) {
		t.Fatalf("round trip: %+v", got)
	}
	if err := s.RemoveLink(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LoadLink(); ok {
		t.Fatal("removed")
	}
	if err := s.RemoveLink(); err != nil {
		t.Fatal("removing twice is fine")
	}
}
