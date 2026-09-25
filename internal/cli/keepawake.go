package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/w4jnl/flok/internal/awake"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
)

const keepAwakeUsage = `usage: flok keep-awake [on|off|toggle|status]

Keeps the Mac awake with the display on while flok runs (off by default; no argument toggles).
The sidebar holds the power assertions, so they end with the flok session.`

// keepAwakeCmd is `flok keep-awake` with its environment injected for tests.
type keepAwakeCmd struct {
	dir       string
	supported bool
	wait      time.Duration // how long to wait for the sidebar to confirm a change
	out, errw io.Writer
}

func runKeepAwake(_ config.Config, args []string) int {
	return keepAwakeCmd{dir: config.StateDir(), supported: awake.Supported(), wait: 1500 * time.Millisecond,
		out: os.Stdout, errw: os.Stderr}.run(args)
}

// keepAwakeState is what the sidebar reports: on when a fresh snapshot from a live sidebar says it
// holds the assertions. Shared by `flok keep-awake status`, `flok status` and `flok doctor`.
type keepAwakeState struct {
	running   bool // flok is running: its sidebar is alive and published recently
	on        bool // the sidebar holds the assertions
	requested bool // the keep-awake marker asks for them
}

func readKeepAwake(dir string) keepAwakeState {
	s, f := snapshot.Load(dir, time.Now())
	running := f == snapshot.Fresh && s.SidebarAlive()
	return keepAwakeState{running: running, on: running && s.KeepAwake, requested: state.New(dir).KeepAwake()}
}

func (k keepAwakeState) String() string {
	switch {
	case k.on:
		return "keep-awake: on (display and idle sleep blocked until keep-awake off or flok down)"
	case !k.running:
		return "keep-awake: off (flok is not running)"
	case k.requested:
		return "keep-awake: off (requested, but the sidebar does not hold the power assertions; see flok doctor)"
	}
	return "keep-awake: off"
}

func (c keepAwakeCmd) run(args []string) int {
	action := "toggle"
	if len(args) > 0 {
		action = args[0]
	}
	switch {
	case len(args) > 1:
		fmt.Fprintln(c.errw, keepAwakeUsage)
		return 2
	case action == "-h" || action == "--help" || action == "help":
		fmt.Fprintln(c.out, keepAwakeUsage)
		return 0
	case action != "on" && action != "off" && action != "toggle" && action != "status":
		fmt.Fprintf(c.errw, "flok keep-awake: unknown argument %q\n%s\n", action, keepAwakeUsage)
		return 2
	case !c.supported:
		fmt.Fprintln(c.errw, "flok keep-awake: macOS only")
		return 1
	}
	cur := readKeepAwake(c.dir)
	switch action {
	case "status":
		fmt.Fprintln(c.out, cur)
		return 0
	case "toggle":
		action = "on"
		if cur.requested || cur.on {
			action = "off"
		}
	}
	want := action == "on"
	if want && !cur.running {
		fmt.Fprintln(c.errw, "flok keep-awake: flok is not running; start it with flok up")
		return 1
	}
	if err := state.New(c.dir).SetKeepAwake(want); err != nil {
		fmt.Fprintln(c.errw, "flok keep-awake:", err)
		return 1
	}
	if !cur.running { // off with no session: nothing holds anything, the marker is reset
		fmt.Fprintln(c.out, "keep-awake: off")
		return 0
	}
	deadline := time.Now().Add(c.wait)
	for {
		if now := readKeepAwake(c.dir); now.running && now.on == want {
			fmt.Fprintln(c.out, now)
			return 0
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Fprintf(c.errw, "flok keep-awake: the sidebar did not confirm keep-awake %s (see flok doctor; FLOK_DEBUG=1 logs the reason to sidebar.log)\n", action)
	return 1
}
