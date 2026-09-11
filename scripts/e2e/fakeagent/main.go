// fakeagent stands in for a coding agent in end-to-end tests: it is built under the name of
// the agent ("claude", "copilot") so tmux reports it as the pane's current command. With a file
// argument it repaints the pane with that file's content whenever the file changes, which lets
// the tests drive the screen-rule engine.
package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	// `claude agents --json` shim for the sidebar's registry poller: prints the file named by
	// FLOK_E2E_REGISTRY (or an empty list).
	if len(os.Args) >= 3 && os.Args[1] == "agents" && os.Args[2] == "--json" {
		if data, err := os.ReadFile(os.Getenv("FLOK_E2E_REGISTRY")); err == nil {
			fmt.Print(string(data))
		} else {
			fmt.Print("[]")
		}
		return
	}
	if len(os.Args) < 2 {
		for {
			time.Sleep(time.Hour)
		}
	}
	path := os.Args[1]
	var last time.Time
	for {
		if fi, err := os.Stat(path); err == nil && !fi.ModTime().Equal(last) {
			last = fi.ModTime()
			data, _ := os.ReadFile(path)
			fmt.Print("\033[2J\033[H" + string(data))
		}
		time.Sleep(100 * time.Millisecond)
	}
}
