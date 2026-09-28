package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/state"
)

// runRelay implements `flok relay <cmd>`: a flok key pressed inside a served host's tmux. The
// binding files the command in this machine's request mailbox; `flok serve` forwards it to the
// sidebar that drives this host, which runs the same `flok <cmd>` a local binding would.
func runRelay(_ config.Config, args []string) int {
	cmd := strings.Join(args, " ")
	if !remote.IsKeyCommand(cmd) {
		var cmds []string
		for _, k := range remote.KeyCommands {
			cmds = append(cmds, k.Cmd)
		}
		fmt.Fprintf(os.Stderr, "usage: flok relay <%s>\n", strings.Join(cmds, "|"))
		return 2
	}
	return report(state.New(config.StateDir()).WriteRequest(state.Request{Cmd: cmd}))
}
