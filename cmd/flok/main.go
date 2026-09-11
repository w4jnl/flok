// Command flok is a herdr-like agent sidebar for tmux.
package main

import (
	"os"

	"github.com/w4jnl/flok/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
