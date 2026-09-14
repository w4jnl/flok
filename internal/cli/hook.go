package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/launcher"
	"github.com/w4jnl/flok/internal/notify"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

func hookLog(format string, args ...any) {
	f, err := os.OpenFile(filepath.Join(config.StateDir(), "hook.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format(time.RFC3339)+" "+format+"\n", args...)
}

// runHook is the receiver behind `flok hook <agent> [--event name]`. It must be fast,
// silent and always exit 0: Copilot denies tools when a hook fails.
func runHook(cfg config.Config, args []string) (code int) {
	defer func() {
		if r := recover(); r != nil {
			hookLog("panic: %v", r)
			code = 0
		}
	}()
	if len(args) == 0 {
		return 0
	}
	id, event := args[0], ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--event" && i+1 < len(args) {
			event = args[i+1]
			i++
		}
	}
	pane := os.Getenv("TMUX_PANE")
	data, _ := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if pane == "" { // not inside tmux (herdr, plain terminal): nothing to track
		return 0
	}
	raw := map[string]any{}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			hookLog("%s %s: bad json: %v", id, pane, err)
			return 0
		}
	}
	if event != "" {
		if _, ok := raw["hook_event_name"]; !ok {
			raw["hook_event_name"] = event
		}
	}
	ev, ok := agent.MapHook(id, raw)
	if !ok {
		return 0
	}
	now := time.Now()
	ev.At = now
	st := state.New(config.StateDir())
	inner := tmux.FromEnv()
	focused := func() bool {
		if inner == nil || !st.TerminalFocused() {
			return false
		}
		v, err := tmux.Display(inner, pane, "#{?session_attached,1,0}#{window_active}#{pane_active}")
		return err == nil && v == "111"
	}
	var sounder notify.Sounder = notify.Noop{}
	plays := cfg.Sounds.Enabled && cfg.Sounds.Player == "hook"
	if plays {
		player := notify.Player{Files: notify.Resolve(st.Dir, map[string]string{"done": cfg.Sounds.Done, "blocked": cfg.Sounds.Blocked, "error": cfg.Sounds.Error}), Volume: cfg.Sounds.Volume, Command: cfg.Sounds.Command}
		// the bell goes into the outer's work pane (the inner client's pty from runtime.json),
		// which the outer server forwards to the terminal — also over ssh
		bell := notify.Bell{Resolve: func() string {
			if rt, err := launcher.ReadRuntime(); err == nil {
				return rt.InnerClientTTY
			}
			return ""
		}}
		sounder = notify.Compose(player, bell, cfg.Sounds.Bell)
	}
	a, fx, err := st.Update(pane, func(a *agent.Agent) state.Effects {
		fx := state.Apply(a, ev, focused, now)
		if fx.Sound != "" && plays &&
			notify.Allowed(st.Dir, pane+"-"+fx.Sound, 2*time.Second, time.Duration(cfg.Sounds.MinIntervalMs)*time.Millisecond, now) {
			if err := sounder.Play(fx.Sound); err == nil {
				if n := len(a.Notifications); n > 0 {
					a.Notifications[n-1].Sounded = true
				}
			} else {
				hookLog("sound %s: %v", fx.Sound, err)
			}
		}
		return fx
	})
	if err != nil {
		hookLog("%s %s: update: %v", id, pane, err)
	}
	st.AppendEvent(map[string]any{"at": now, "pane": pane, "agent": id, "event": ev.Name, "kind": ev.Kind,
		"state": a.State, "reason": a.Reason, "tool": a.CurrentTool, "detail": a.ToolDetail, "sound": fx.Sound})
	return 0
}
