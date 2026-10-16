package link

import (
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/link/wire"
)

func TestOriginURL(t *testing.T) {
	for in, want := range map[string]string{
		"wss://flok.example.net/link":     "https://flok.example.net",
		"https://flok.example.net":        "https://flok.example.net",
		"https://flok.example.net/":       "https://flok.example.net",
		"wss://flok.example.net/link/tls": "https://flok.example.net",
		"https://h.example/p/link":        "https://h.example/p",
		"http://127.0.0.1:8080":           "http://127.0.0.1:8080",
		"ws://127.0.0.1:8080/link":        "http://127.0.0.1:8080",
		" https://h.example/api/ws ":      "https://h.example",
		"https://h.example/link?x=1#f":    "https://h.example",
		"ftp://h.example":                 "",
		"not a url":                       "",
		"":                                "",
	} {
		if got := OriginURL(in); got != want {
			t.Errorf("OriginURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// A phone request goes out under its id and the relay's reply (and its later paired word) come
// back as commands of kind phone under that id.
func TestLinkPhoneFrames(t *testing.T) {
	r := newRelay(t, false)
	c := New(Config{URL: r.srv.URL, Token: "t0k", Name: "home", Version: "test", Hostname: "mac", BackoffMax: 300 * time.Millisecond})
	c.Start()
	defer c.Close()
	hello := r.next(t, wire.TypeHello)
	found := false
	for _, f := range hello.Hello.Features {
		found = found || f == wire.FeaturePhone
	}
	if !found {
		t.Fatalf("the hello does not advertise phone frames: %v", hello.Hello.Features)
	}
	c.Phone("abc123abc123", wire.Phone{Op: "pair"})
	f := r.next(t, wire.TypePhone)
	if f.ID != "abc123abc123" || f.Phone == nil || f.Phone.Op != "pair" {
		t.Fatalf("phone frame: %+v", f)
	}
	exp := time.Now().Add(5 * time.Minute).Round(time.Second)
	r.send(t, wire.Frame{Type: wire.TypePhone, ID: "abc123abc123", Phone: &wire.Phone{Op: "pair", Code: "AB7Q-K2MN", ExpiresAt: exp}})
	m := waitMsg(t, c, func(m Msg) bool { return m.Command != nil && m.Command.Kind == "phone" })
	if m.Command.ID != "abc123abc123" || m.Command.Phone == nil || m.Command.Phone.Code != "AB7Q-K2MN" || !m.Command.Phone.ExpiresAt.Equal(exp) {
		t.Fatalf("reply: %+v", m.Command)
	}
	r.send(t, wire.Frame{Type: wire.TypePhone, ID: "abc123abc123", Phone: &wire.Phone{Op: "paired", Phone: &wire.PhoneInfo{ID: "p1", Name: "Jaro's iPhone"}}})
	m = waitMsg(t, c, func(m Msg) bool {
		return m.Command != nil && m.Command.Kind == "phone" && m.Command.Phone != nil && m.Command.Phone.Op == "paired"
	})
	if m.Command.Phone.Phone == nil || m.Command.Phone.Phone.Name != "Jaro's iPhone" {
		t.Fatalf("paired: %+v", m.Command.Phone)
	}
	r.send(t, wire.Frame{Type: wire.TypePhone, ID: "abc123abc123", Error: "no phone x"})
	m = waitMsg(t, c, func(m Msg) bool { return m.Command != nil && m.Command.Kind == "phone" && m.Command.Error != "" })
	if m.Command.Error != "no phone x" {
		t.Fatalf("error reply: %+v", m.Command)
	}
	c.Phone("", wire.Phone{Op: "list"}) // no id: nothing to answer to, dropped
	select {
	case f := <-r.got:
		t.Fatalf("a frame without an id went out: %+v", f)
	case <-time.After(150 * time.Millisecond):
	}
}
