package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
)

// fakeSidebar publishes snapshots that follow the keep-awake marker, like the real sidebar does,
// until stop is closed. confirm=false simulates a sidebar that cannot take the assertions.
func fakeSidebar(t *testing.T, dir string, confirm bool) (stop func()) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, pub := state.New(dir), &snapshot.Publisher{Dir: dir}
	_, _ = pub.Publish(snapshot.Snapshot{SidebarPID: os.Getpid()}, time.Now())
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			case <-time.After(10 * time.Millisecond):
			}
			_, _ = pub.Publish(snapshot.Snapshot{SidebarPID: os.Getpid(), KeepAwake: confirm && st.KeepAwake()}, time.Now())
		}
	}()
	return func() { close(done); <-finished }
}

func keepAwakeTest(dir string, supported bool) (keepAwakeCmd, *bytes.Buffer, *bytes.Buffer) {
	var out, errw bytes.Buffer
	return keepAwakeCmd{dir: dir, supported: supported, wait: 500 * time.Millisecond, out: &out, errw: &errw}, &out, &errw
}

func TestKeepAwakeArguments(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"maybe"}, {"on", "off"}} {
		c, _, errw := keepAwakeTest(dir, true)
		if rc := c.run(args); rc != 2 || !strings.Contains(errw.String(), "usage: flok keep-awake") {
			t.Errorf("%v: rc=%d err=%q", args, rc, errw)
		}
	}
	c, out, _ := keepAwakeTest(dir, true)
	if rc := c.run([]string{"--help"}); rc != 0 || !strings.Contains(out.String(), "usage: flok keep-awake") {
		t.Errorf("--help: rc=%d", rc)
	}
	c, _, errw := keepAwakeTest(dir, false)
	if rc := c.run([]string{"on"}); rc != 1 || !strings.Contains(errw.String(), "macOS only") {
		t.Errorf("unsupported: rc=%d err=%q", rc, errw)
	}
}

func TestKeepAwakeNeedsARunningSession(t *testing.T) {
	dir := t.TempDir()
	c, _, errw := keepAwakeTest(dir, true)
	if rc := c.run([]string{"on"}); rc != 1 || !strings.Contains(errw.String(), "start it with flok up") {
		t.Fatalf("on: rc=%d err=%q", rc, errw)
	}
	if state.New(dir).KeepAwake() {
		t.Fatal("a refused on must not leave the marker set")
	}
	c, out, _ := keepAwakeTest(dir, true)
	if rc := c.run([]string{"status"}); rc != 0 || !strings.Contains(out.String(), "flok is not running") {
		t.Fatalf("status: rc=%d out=%q", rc, out)
	}
	c, out, _ = keepAwakeTest(dir, true)
	if rc := c.run([]string{"off"}); rc != 0 || strings.TrimSpace(out.String()) != "keep-awake: off" {
		t.Fatalf("off: rc=%d out=%q", rc, out)
	}
}

func TestKeepAwakeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	defer fakeSidebar(t, dir, true)()
	run := func(args ...string) string {
		t.Helper()
		c, out, errw := keepAwakeTest(dir, true)
		if rc := c.run(args); rc != 0 {
			t.Fatalf("%v: rc=%d err=%q", args, rc, errw)
		}
		return strings.TrimSpace(out.String())
	}
	if got := run("status"); got != "keep-awake: off" {
		t.Fatalf("initial status %q", got)
	}
	if got := run(); !strings.HasPrefix(got, "keep-awake: on") { // bare toggles
		t.Fatalf("toggle on: %q", got)
	}
	if got := run("on"); !strings.HasPrefix(got, "keep-awake: on") { // on when on: a no-op
		t.Fatalf("on again: %q", got)
	}
	if got := run("status"); !strings.HasPrefix(got, "keep-awake: on") {
		t.Fatalf("status on: %q", got)
	}
	if got := run("toggle"); got != "keep-awake: off" {
		t.Fatalf("toggle off: %q", got)
	}
	if got := run("off"); got != "keep-awake: off" {
		t.Fatalf("off again: %q", got)
	}
}

func TestKeepAwakeUnconfirmed(t *testing.T) {
	dir := t.TempDir()
	defer fakeSidebar(t, dir, false)()
	c, _, errw := keepAwakeTest(dir, true)
	if rc := c.run([]string{"on"}); rc != 1 || !strings.Contains(errw.String(), "did not confirm") {
		t.Fatalf("rc=%d err=%q", rc, errw)
	}
	if k := readKeepAwake(dir); k.on || !k.requested || !strings.Contains(k.String(), "requested") {
		t.Fatalf("state %+v %q", k, k)
	}
}

func TestStatusShowsKeepAwakeOnlyWhenOn(t *testing.T) {
	s := merge.Snapshot{}
	var off, on bytes.Buffer
	printStatus(&off, s, keepAwakeState{running: true})
	printStatus(&on, s, keepAwakeState{running: true, on: true})
	if strings.Contains(off.String(), "keep-awake") {
		t.Fatalf("off must not mention keep-awake:\n%s", off.String())
	}
	if !strings.Contains(on.String(), "keep-awake: on (display and idle sleep blocked)\n") {
		t.Fatalf("on must show the keep-awake line:\n%s", on.String())
	}
	for keep, want := range map[bool]bool{false: false, true: true} {
		data, err := json.Marshal(statusJSON{Snapshot: s, KeepAwake: keep})
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		if _, has := m["KeepAwake"]; has != want {
			t.Errorf("keep=%v: KeepAwake in JSON = %v: %s", keep, has, data)
		}
		if _, ok := m["Spaces"]; !ok {
			t.Errorf("the snapshot fields must stay at the top level: %s", data)
		}
	}
}
