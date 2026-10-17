package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/qr"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
)

func TestPairURL(t *testing.T) {
	if got := pairURL("https://flok.example.net", "AB7Q-K2MN"); got != "flok://pair?code=AB7Q-K2MN&relay=https%3A%2F%2Fflok.example.net" {
		t.Fatalf("pairURL: %s", got)
	}
	if got := pairURL("http://127.0.0.1:8080", "AB7Q-K2MN"); got != "flok://pair?code=AB7Q-K2MN&relay=http%3A%2F%2F127.0.0.1%3A8080" {
		t.Fatalf("pairURL with a port: %s", got)
	}
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(newRequestID()) || !state.ValidRequestID(newRequestID()) {
		t.Fatal("request ids are 12 hex digits")
	}
}

// livePhoneSidebar publishes a fresh snapshot from a live sidebar with the link in a state.
func livePhoneSidebar(t *testing.T, dir string, l *snapshot.Link) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (&snapshot.Publisher{Dir: dir}).Publish(snapshot.Snapshot{SidebarPID: os.Getpid(), Link: l}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func newPhoneCmd(dir, url string) (phoneCmd, *bytes.Buffer, *bytes.Buffer) {
	out, errw := &bytes.Buffer{}, &bytes.Buffer{}
	var cfg config.Config
	cfg.Link.URL = url
	return phoneCmd{relayCmd{name: "phone", dir: dir, cfg: cfg, now: time.Now, wait: 2 * time.Second, out: out, errw: errw}}, out, errw
}

// Each thing that must be in place before a phone can pair is named when it is not.
func TestPhoneReady(t *testing.T) {
	dir := t.TempDir()
	c, _, errw := newPhoneCmd(dir, "")
	if rc := c.run([]string{"list"}); rc != 1 || !strings.Contains(errw.String(), "no relay link: set [link] url") {
		t.Fatalf("no url: %d %s", rc, errw)
	}
	c.cfg.Link.URL = "wss://flok.example.net/link"
	if _, err := c.ready(); err == nil || err.Error() != "the sidebar is not running (flok up)" {
		t.Fatalf("no sidebar: %v", err)
	}
	livePhoneSidebar(t, dir, nil)
	if _, err := c.ready(); err == nil || !strings.Contains(err.Error(), "has no relay link") {
		t.Fatalf("a sidebar without a link: %v", err)
	}
	livePhoneSidebar(t, dir, &snapshot.Link{State: "unreachable", Detail: "dial tcp: connection refused"})
	if _, err := c.ready(); err == nil || err.Error() != "the relay link is unreachable: dial tcp: connection refused (flok link status)" {
		t.Fatalf("a link down: %v", err)
	}
	livePhoneSidebar(t, dir, &snapshot.Link{State: "connected"})
	if origin, err := c.ready(); err != nil || origin != "https://flok.example.net" {
		t.Fatalf("ready: %q %v", origin, err)
	}
	c.cfg.Link.URL = "ftp://flok.example.net"
	if _, err := c.ready(); err == nil || !strings.Contains(err.Error(), "not a URL flok can turn") {
		t.Fatalf("a bad url: %v", err)
	}
}

// fakePhoneSidebar answers phone requests the way the sidebar does, from a function; for pair,
// pairedAfter > 0 adds the relay's later word that a phone took the code.
func fakePhoneSidebar(dir string, pairedAfter time.Duration, reply func(state.Request) wire.Frame) (stop func()) {
	st := state.New(dir)
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			case <-time.After(10 * time.Millisecond):
			}
			for _, r := range st.DrainRequests(time.Now(), time.Minute) {
				if r.Cmd != "phone" {
					continue
				}
				f := reply(r)
				f.Type, f.ID = wire.TypePhone, r.ID
				_ = st.WriteReply(r.ID, f)
				if r.Op == "pair" && pairedAfter > 0 {
					go func(id string) {
						time.Sleep(pairedAfter)
						_ = st.WriteReply(id, wire.Frame{Type: wire.TypePhone, ID: id, Phone: &wire.Phone{Op: "paired", Phone: &wire.PhoneInfo{ID: "p1", Name: "Jaro's iPhone"}}})
					}(r.ID)
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done); <-finished }) }
}

// The command's side of the mailbox: pair, list and forget travel as requests and come back
// through the reply file, the code becomes a URL for the QR, the outcomes read right.
func TestPhoneThroughTheSidebar(t *testing.T) {
	dir := t.TempDir()
	livePhoneSidebar(t, dir, &snapshot.Link{State: "connected"})
	exp := time.Now().Add(5 * time.Minute).Round(time.Second)
	var mu sync.Mutex
	var asked []state.Request
	stop := fakePhoneSidebar(dir, 150*time.Millisecond, func(r state.Request) wire.Frame {
		mu.Lock()
		asked = append(asked, r)
		mu.Unlock()
		switch r.Op {
		case "pair":
			return wire.Frame{Phone: &wire.Phone{Op: "pair", Code: "AB7Q-K2MN", ExpiresAt: exp}}
		case "list":
			return wire.Frame{Phone: &wire.Phone{Op: "list", Phones: []wire.PhoneInfo{{ID: "p1", Name: "Jaro's iPhone", Paired: exp, LastSeen: exp, Instance: "home"}}}}
		case "forget":
			if r.Name == "nobody" {
				return wire.Frame{Error: "no phone nobody (flok phone list)"}
			}
			return wire.Frame{Phone: &wire.Phone{Op: "forget", Phone: &wire.PhoneInfo{ID: "p1", Name: "Jaro's iPhone"}, Phones: []wire.PhoneInfo{{ID: "p2", Name: "iPad"}}}}
		}
		return wire.Frame{Error: "unknown op " + r.Op}
	})
	defer stop()

	c, out, errw := newPhoneCmd(dir, "wss://flok.example.net/link")
	if rc := c.run([]string{"pair", "--json"}); rc != 0 {
		t.Fatalf("pair --json: %d %s", rc, errw)
	}
	var got struct {
		ID, Code, URL string
		ExpiresAt     time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Code != "AB7Q-K2MN" || got.URL != pairURL("https://flok.example.net", "AB7Q-K2MN") || !got.ExpiresAt.Equal(exp) || !state.ValidRequestID(got.ID) {
		t.Fatalf("pair --json gave %s (%v)", out, err)
	}
	if !strings.Contains(out.String(), "&relay=") || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("pair --json is one plain line: %q", out)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, state.RepliesDir, "*.json")); len(files) != 0 {
		t.Fatalf("the reply file stays: %v", files)
	}

	out.Reset()
	if rc := c.run([]string{"pair"}); rc != 0 { // not a terminal: the lines, then the wait for the phone
		t.Fatalf("pair: %d %s", rc, errw)
	}
	if s := out.String(); !strings.Contains(s, "code   AB7Q-K2MN\n") || !strings.Contains(s, "relay  flok.example.net\n") || !strings.Contains(s, "url    flok://pair?code=AB7Q-K2MN") || !strings.HasSuffix(s, "paired: Jaro's iPhone\n") {
		t.Fatalf("pair printed:\n%s", s)
	}

	out.Reset()
	c.tty = true
	old := runCodeScreen
	runCodeScreen = func(s codeScreen) (codeScreen, error) {
		if s.code != "AB7Q-K2MN" || s.relay != "flok.example.net" || s.qr == nil || s.watch == nil || s.accept != nil || !s.expires.Equal(exp) {
			t.Errorf("screen: %+v", s)
		}
		s.step, s.outcome = stepDone, "paired: Jaro's iPhone"
		return s, nil
	}
	defer func() { runCodeScreen = old }()
	if rc := c.run([]string{"pair"}); rc != 0 || out.String() != "paired: Jaro's iPhone\n" {
		t.Fatalf("pair on a terminal: %d %q", rc, out)
	}
	c.tty = false

	out.Reset()
	if rc := c.run([]string{"list"}); rc != 0 || !strings.Contains(out.String(), "Jaro's iPhone") || !strings.Contains(out.String(), "home") || !strings.Contains(out.String(), "p1") {
		t.Fatalf("list: %d %s", rc, out)
	}
	out.Reset()
	if rc := c.run([]string{"list", "--json"}); rc != 0 || !strings.HasPrefix(out.String(), `[{"id":"p1","name":"Jaro's iPhone"`) {
		t.Fatalf("list --json: %d %s", rc, out)
	}
	errw.Reset()
	if rc := c.run([]string{"forget", "nobody"}); rc != 1 || errw.String() != "flok phone: no phone nobody (flok phone list)\n" {
		t.Fatalf("forget nobody: %d %q", rc, errw)
	}
	out.Reset()
	if rc := c.run([]string{"forget", "p1"}); rc != 0 || !strings.HasPrefix(out.String(), "Jaro's iPhone forgotten:") || !strings.Contains(out.String(), "still paired: iPad\n") {
		t.Fatalf("forget: %d %s", rc, out)
	}
	mu.Lock()
	if len(asked) != 7 || asked[0].Op != "pair" || asked[5].Op != "forget" || asked[5].Name != "nobody" || asked[6].Name != "p1" {
		t.Fatalf("asked %+v", asked)
	}
	mu.Unlock()

	stop() // no sidebar answering any more: the wait says what to do
	c.wait = 100 * time.Millisecond
	errw.Reset()
	if rc := c.run([]string{"list"}); rc != 1 || !strings.Contains(errw.String(), "no answer from the relay in 100ms") {
		t.Fatalf("timeout: %d %s", rc, errw)
	}
	if rc := c.run([]string{"pair", "--nope"}); rc != 2 {
		t.Fatal("an unknown flag")
	}
	if rc := c.run([]string{"forget"}); rc != 2 {
		t.Fatal("forget without a name")
	}
}

// The screen: the QR with its code and relay, text alone when the window is too small, the
// outcome from the reply file or the clock, and the keys.
func TestCodeScreen(t *testing.T) {
	m, err := qr.Encode(pairURL("https://flok.example.net", "AB7Q-K2MN"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "r.json")
	watch := func() (codeStep, string) {
		if f, ok := readReply(path); ok && isPaired(f) {
			return stepDone, "paired: " + f.Phone.Phone.Name
		}
		return stepWaiting, ""
	}
	s := codeScreen{qr: m, code: "AB7Q-K2MN", relay: "flok.example.net", expires: time.Now().Add(90 * time.Second), watch: watch,
		hint: "scan it with the Camera app", expired: "the code expired: run flok phone pair again"}
	if v := s.View(); !strings.Contains(v, "code    \x1b[1mA B 7 Q - K 2 M N\x1b[0m") || !strings.Contains(v, "relay   flok.example.net") || !strings.Contains(v, "\x1b[30;47m") || !strings.Contains(v, "the code lasts 1:") || !strings.Contains(v, "q closes") || !strings.Contains(v, "scan it with the Camera app") {
		t.Fatalf("view:\n%s", v)
	}
	small := s
	small.width, small.height = 40, 10
	if v := small.View(); !strings.Contains(v, "too small") || strings.Contains(v, "\x1b[30;47m") {
		t.Fatalf("small view:\n%s", v)
	}
	quits := func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		_, ok := cmd().(tea.QuitMsg)
		return ok
	}
	mdl, cmd := s.Update(codeTickMsg(time.Now()))
	if ps := mdl.(codeScreen); ps.outcome != "" || cmd == nil || quits(cmd) {
		t.Fatalf("a tick with nothing new keeps ticking: %+v", ps)
	}
	data, _ := json.Marshal(wire.Frame{Type: wire.TypePhone, Phone: &wire.Phone{Op: "paired", Phone: &wire.PhoneInfo{Name: "Jaro's iPhone"}}})
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	mdl, cmd = s.Update(codeTickMsg(time.Now()))
	if ps := mdl.(codeScreen); ps.step != stepDone || ps.outcome != "paired: Jaro's iPhone" || !quits(cmd) {
		t.Fatalf("paired ends the screen: %+v", ps)
	}
	h := s
	h.hold = true
	mdl, cmd = h.Update(codeTickMsg(time.Now()))
	ps := mdl.(codeScreen)
	if cmd != nil || !strings.Contains(ps.View(), "paired: Jaro's iPhone") || !strings.Contains(ps.View(), "Enter closes") {
		t.Fatalf("hold shows the outcome and waits: %+v", ps)
	}
	if _, cmd = ps.Update(tea.KeyMsg{Type: tea.KeyEnter}); !quits(cmd) {
		t.Fatal("Enter closes a held outcome")
	}
	if _, cmd = s.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatal("Enter before the outcome does nothing")
	}
	_ = os.Remove(path)
	e := s
	e.expires = time.Now().Add(-time.Second)
	mdl, cmd = e.Update(codeTickMsg(time.Now()))
	if ps := mdl.(codeScreen); ps.step != stepFailed || !strings.Contains(ps.outcome, "expired") || !quits(cmd) {
		t.Fatalf("expiry: %+v", ps)
	}
	if _, cmd = s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); !quits(cmd) {
		t.Fatal("q quits")
	}
	if spaced("AB7Q-K2MN") != "A B 7 Q - K 2 M N" {
		t.Fatal("spaced")
	}

	// a join: approved asks for Enter (accept) or Esc (decline); --yes accepts at once
	accepted, declined := 0, 0
	j := s
	j.watch = func() (codeStep, string) { return stepApproved, "approved by phone Jaro's iPhone for the tenant jaro" }
	j.accept = func() (string, error) { accepted++; return "joined jaro as office", nil }
	j.decline = func() { declined++ }
	mdl, cmd = j.Update(codeTickMsg(time.Now()))
	js := mdl.(codeScreen)
	if cmd != nil || js.step != stepApproved || !strings.Contains(js.View(), "approved by phone Jaro's iPhone for the tenant jaro") || !strings.Contains(js.View(), "Enter links this flok, Esc declines") {
		t.Fatalf("approved waits for the user: %+v\n%s", js, js.View())
	}
	mdl, cmd = js.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if out := mdl.(codeScreen); out.step != stepDone || out.outcome != "joined jaro as office" || accepted != 1 || !quits(cmd) {
		t.Fatalf("Enter accepts: %+v", out)
	}
	mdl, cmd = js.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if out := mdl.(codeScreen); out.step != stepFailed || declined != 1 || !quits(cmd) {
		t.Fatalf("Esc declines: %+v", out)
	}
	y := j
	y.yes = true
	mdl, cmd = y.Update(codeTickMsg(time.Now()))
	if out := mdl.(codeScreen); out.step != stepDone || accepted != 2 || !quits(cmd) {
		t.Fatalf("--yes accepts at once: %+v", out)
	}
	j.accept = func() (string, error) { return "", errors.New("the relay said no") }
	mdl, _ = js.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mdl, cmd = j.Update(codeTickMsg(time.Now()))
	mdl, cmd = mdl.(codeScreen).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if out := mdl.(codeScreen); out.step != stepFailed || out.outcome != "the relay said no" || !quits(cmd) {
		t.Fatalf("a refused accept: %+v", out)
	}
}
