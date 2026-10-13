package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
)

// fakeLinkSidebar publishes snapshots whose link follows the link-off marker, like the real
// sidebar does (connected when on, encrypted), until stop is called.
func fakeLinkSidebar(t *testing.T, dir string) (stop func()) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, pub := state.New(dir), &snapshot.Publisher{Dir: dir}
	snap := func() snapshot.Snapshot {
		l := &snapshot.Link{State: "connected", URL: "wss://flok.example.net/link", Encrypted: true, Since: time.Now()}
		if st.LinkOff() {
			l.State = "off"
		}
		return snapshot.Snapshot{SidebarPID: os.Getpid(), Link: l}
	}
	_, _ = pub.Publish(snap(), time.Now())
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			case <-time.After(10 * time.Millisecond):
			}
			_, _ = pub.Publish(snap(), time.Now())
		}
	}()
	return func() { close(done); <-finished }
}

func linkTest(dir, url string) (linkCmd, *bytes.Buffer, *bytes.Buffer) {
	var out, errw bytes.Buffer
	return linkCmd{dir: dir, url: url, wait: 500 * time.Millisecond, out: &out, errw: &errw}, &out, &errw
}

const testLinkURL = "https://flok.example.net"

func TestLinkArguments(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"maybe"}, {"on", "off"}} {
		c, _, errw := linkTest(dir, testLinkURL)
		if rc := c.run(args); rc != 2 || !strings.Contains(errw.String(), "usage: flok link") {
			t.Errorf("%v: rc=%d err=%q", args, rc, errw)
		}
	}
	c, out, _ := linkTest(dir, testLinkURL)
	if rc := c.run([]string{"--help"}); rc != 0 || !strings.Contains(out.String(), "usage: flok link") {
		t.Errorf("--help: rc=%d", rc)
	}
	// no [link] url: nothing to switch, and status says so
	c, _, errw := linkTest(dir, "")
	if rc := c.run([]string{"off"}); rc != 1 || !strings.Contains(errw.String(), "[link] url") {
		t.Errorf("off without a link: rc=%d err=%q", rc, errw)
	}
	if state.New(dir).LinkOff() {
		t.Error("a refused off must not leave the marker set")
	}
	c, out, _ = linkTest(dir, "")
	if rc := c.run([]string{"status"}); rc != 0 || !strings.Contains(out.String(), "relay link: none") {
		t.Errorf("status without a link: rc=%d out=%q", rc, out)
	}
}

// Without a running flok the switch is still set, and it holds for the next start.
func TestLinkSwitchWithoutFlok(t *testing.T) {
	dir := t.TempDir()
	c, out, _ := linkTest(dir, testLinkURL)
	if rc := c.run(nil); rc != 0 || !strings.Contains(out.String(), "relay link: off (it stays off when flok starts") {
		t.Fatalf("toggle off: rc=%d out=%q", rc, out)
	}
	if !state.New(dir).LinkOff() {
		t.Fatal("the marker is not set")
	}
	c, out, _ = linkTest(dir, testLinkURL)
	if rc := c.run(nil); rc != 0 || strings.TrimSpace(out.String()) != "relay link: on (flok is not running)" {
		t.Fatalf("toggle on: rc=%d out=%q", rc, out)
	}
}

func TestLinkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	defer fakeLinkSidebar(t, dir)()
	run := func(args ...string) string {
		t.Helper()
		c, out, errw := linkTest(dir, testLinkURL)
		if rc := c.run(args); rc != 0 {
			t.Fatalf("%v: rc=%d err=%q", args, rc, errw)
		}
		return strings.TrimSpace(out.String())
	}
	on := "relay link: on, connected to flok.example.net, encrypted"
	if got := run("status"); got != on {
		t.Fatalf("initial status %q", got)
	}
	if got := run(); got != "relay link: off (flok link on)" { // bare toggles
		t.Fatalf("toggle off: %q", got)
	}
	if got := run("off"); got != "relay link: off (flok link on)" { // off when off: a no-op
		t.Fatalf("off again: %q", got)
	}
	if got := run("toggle"); got != on {
		t.Fatalf("toggle on: %q", got)
	}
}

func TestLinkUnconfirmed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// a sidebar that publishes a connected link and never follows the switch
	_, _ = (&snapshot.Publisher{Dir: dir}).Publish(snapshot.Snapshot{SidebarPID: os.Getpid(),
		Link: &snapshot.Link{State: "connected", URL: "wss://flok.example.net/link"}}, time.Now())
	c, _, errw := linkTest(dir, testLinkURL)
	if rc := c.run([]string{"off"}); rc != 1 || !strings.Contains(errw.String(), "did not confirm") {
		t.Fatalf("rc=%d err=%q", rc, errw)
	}
}

func TestLinkStateWording(t *testing.T) {
	l := func(st, detail string) linkState {
		return linkState{running: true, link: &snapshot.Link{State: st, Detail: detail, URL: "wss://relay.example/link"}}
	}
	for st, want := range map[linkState]string{
		l("connecting", ""):                                        "relay link: on, connecting to relay.example",
		l("unreachable", "no pong from relay"):                     "relay link: on, relay.example out of reach: no pong from relay",
		l("unreachable", ""):                                       "relay link: on, relay.example out of reach",
		l("refused", "the relay refused the token ([link] token)"): "relay link: on, refused by relay.example: the relay refused the token ([link] token)",
		{running: true}:                                            "relay link: the running sidebar has none (flok reload after setting [link] url)",
	} {
		if got := st.String(); got != want {
			t.Errorf("%+v: %q, want %q", st.link, got, want)
		}
	}
}
