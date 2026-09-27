package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/poller"
	"github.com/w4jnl/flok/internal/remote"
	"github.com/w4jnl/flok/internal/remote/proto"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

const serveUsage = `usage: flok serve --stdio | --hello

Runs flok headless on this host for a flok sidebar on another machine, which starts it over
ssh (flok host add <name> <this host>): the same hooks, Claude registry and screen rules as
the sidebar here would use, streamed as JSON lines on stdout; goto/seen/visible arrive on
stdin. It ends when stdin closes. While it runs, hooks on this host stay silent and the
remote side plays the sounds. --hello prints the greeting frame and exits (flok doctor).`

func runServe(cfg config.Config, args []string) int {
	hello := serveHello()
	switch {
	case len(args) == 1 && args[0] == "--hello":
		enc := json.NewEncoder(os.Stdout)
		return report(enc.Encode(proto.Frame{Type: proto.TypeHello, Hello: &hello}))
	case len(args) == 1 && args[0] == "--stdio":
	default:
		fmt.Fprintln(os.Stderr, serveUsage)
		return 2
	}
	st := state.New(config.StateDir())
	release, err := st.LockServe(state.Served{PID: os.Getpid(), Since: time.Now(), SSHClient: os.Getenv("SSH_CONNECTION")})
	if err != nil {
		_ = proto.Write(os.Stdout, proto.Frame{Type: proto.TypeError, Error: err.Error()})
		return 1
	}
	defer release()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	d := sidebarDeps(cfg)
	var debugf func(string, ...any)
	if os.Getenv("FLOK_DEBUG") != "" {
		debugf = serveLog
	}
	err = remote.Serve(ctx, remote.ServeDeps{Hello: hello, Inner: d.Inner, Store: d.Store, In: os.Stdin, Out: os.Stdout, Debugf: debugf,
		NewPoller: func(sound func(pane, kind string)) *poller.Poller {
			// ClientTTY stays empty: the focus follows the most recently active client, which
			// is the local side's attach pane; the local plays every sound, hook ones included
			return poller.New(poller.Deps{Cfg: cfg, Tmux: d.Inner, Store: d.Store, Registry: d.Registry, Rules: d.Rules,
				Adapters: d.Adapters, BranchOf: d.BranchOf, Sound: sound, SoundHookNotifications: true, Debugf: debugf})
		}})
	if err != nil {
		fmt.Fprintln(os.Stderr, "flok serve:", err)
		return 1
	}
	return 0
}

func serveHello() proto.Hello {
	host, _ := os.Hostname()
	return proto.Hello{Proto: proto.Version, Version: Version, Hostname: host, PID: os.Getpid(),
		TmuxVersion: tmux.DetectVersion("").String(), StateDir: config.StateDir()}
}

// serveLog appends to serve.log in the state dir (FLOK_DEBUG set).
func serveLog(format string, args ...any) {
	f, err := os.OpenFile(filepath.Join(config.StateDir(), "serve.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format(time.RFC3339)+" "+format+"\n", args...)
}
