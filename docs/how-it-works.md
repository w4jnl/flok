# How flok works

[‹ README](../README.md)

The picture at the top of the [README](../README.md) shows the parts; this page goes through the
Mac side: the two tmux servers, where agent state comes from, the states, and what each tmux
version can do. Remote hosts, the phone and the menu bar have pages of their own.

What the sidebar shows next to your tmux (the servers panel appears once a host is registered;
`beta` here is a machine reached over ssh, its agent `build` listed with the local ones):

```
┌──────────────────────────┬────────────────────────────────────────┐
│ [flok]                 ⇄ │                                        │
│ servers                  │                                        │
│  ◑ local            3 · 1│   the work pane: your tmux server, or  │
│  ○ beta full           1 │   a host's over ssh, both untouched    │
│                          │   (sessions, windows, plugins, keys)   │
│ sessions · local         │                                        │
│  ◑ Claude          main  │                                        │
│  ○ Hugo            main  │                                        │
│  ● flok            main  │                                        │
│                          │                                        │
│ agents · 1               │                                        │
│  ● flok        perm:Bash │                                        │
│    claude · feasibility  │                                        │
│  ◑ trading-jou… Bash 0:42│                                        │
│    claude · add-settle…  │                                        │
│  ◑ build        Bash 0:08│                                        │
│    beta · claude         │                                        │
│  ○ mwrelay               │                                        │
│    claude · store-forwa… │                                        │
│ j/k ⏎ ⇥ 1-9      ? help  │                                        │
└──────────────────────────┴────────────────────────────────────────┘
```

## Two tmux servers

```
Ghostty (or any terminal)
└── outer tmux server   socket "flok" · prefix None · status off · mouse on
    └── session "flok"
        ├── window 0
        │   ├── left pane   flok sidebar            (Bubble Tea program)
        │   └── right pane  tmux attach ───────────► inner tmux server   (your normal server)
        │                                            ├── session Claude   ├─ window 1 ─ pane: claude
        │                                            ├── session Hugo     │            ─ pane: zsh
        │                                            └── session ...      └─ ...
        └── window flok-host-beta   (one parked window per registered host)
            └── pane  ssh -t beta tmux attach ────► beta's tmux server; swapped in next to the
                                                     sidebar when beta comes to the front
```

`flok up` starts the outer server from a generated config and attaches your terminal to it. The
outer server has no prefix key and no status bar, so every keystroke and mouse event reaches the
inner tmux exactly as before; it merely frames your session with a sidebar. A remote host gets
the same treatment: its `ssh -t … tmux attach` lives in a parked window of the outer, and
bringing the host to the front is one `swap-pane`, so your keys reach that tmux just as
directly. The right pane runs an
attach loop: a killed session re-attaches elsewhere, a deliberate detach (`prefix d`) tears the
outer down and returns you to the shell.

The sidebar never modifies the inner server. It reads it (`list-sessions`, `list-panes`,
`list-clients`, `capture-pane`, `list-keys`) and drives your client with `switch-client`,
`select-window` and `select-pane`. The `prefix` bindings it adds for the session (or the
optional tmux.conf snippet) are plain `run-shell` calls to `flok jump|next|toggle|hide|focus|
menu …|host …`; the help is `flok keys --open`, a popup on tmux 3.2+.

One thing flok does leave on the inner server while it runs: `FLOK_OUTER_SOCKET` in its global
environment, the outer server's socket path. A program in one of your sessions sees only the
inner tmux in its `$TMUX`; one that must ask the real terminal something (a terminal image
protocol riding on the outer's `allow-passthrough`, say) reads
`tmux show-environment -g FLOK_OUTER_SOCKET` and asks that server with
`tmux -S <path> display-message -p …`. The attach loop sets it before every attach and removes
it when the outer goes (a detach, `flok down`).

## Where agent state comes from

```
 Claude Code / Copilot CLI ──hooks──► flok hook ──► ~/.local/state/flok/agents/<pane>.json
                                                  (per-pane record, flock + atomic write, plays the sound)
                                              └──► ~/.local/state/flok/sessions/<session>.json ──► nt agent import
                                                  (the session's journal; with [notes] on, a report in your notes)
                                                              │ fsnotify
 tmux snapshot (1 s) ──────────────────────────────────────┐  │
 claude agents --json (10 s, pid → tty → pane) ────────────┤  ▼
 capture-pane + title + OSC 9;4 progress (2 s, rule engine)┴► merge.Build ──► sidebar view
                                                               │
                                                               └─ seen marks (done → idle once you look)
```

Four sources feed the merge, most authoritative first:

| source | what it gives | when it is used |
|---|---|---|
| **Hooks** (`flok hook claude`, `flok hook copilot`) | exact transitions: prompt submitted, tool start/end with the tool name, permission request, question, stop, session end | always, for agents started after `flok install` |
| **Claude's registry** (`claude agents --json`) | busy / idle per running session, matched to a pane through the process tty | the label, and clearing a turn that ended without a Stop hook (Esc, usage limit, error): two idle samples, or the screen rules showing a bare prompt box three times, whichever comes first |
| **Pane title** | Claude Code writes `✳ <name>` and a spinner glyph | the agent's own session name; a spinner counts as working. An idle glyph is *not* evidence: inside tmux Claude keeps `✳` while busy |
| **Screen rules** | herdr's detection manifests (TOML, Apache-2.0) evaluated over the visible pane text, the title and tmux's OSC 9;4 progress state | agents without hooks, and hook-driven agents while working or blocked, to notice a prompt dismissed with Esc |

A host in full mode runs this same pipeline on its side (`flok serve`, started over ssh) and
streams the merged result; the local sidebar federates every host's view with its own, so a
remote agent is sorted, coloured and sounded exactly like a local one, with the host's name on
its row. A plain-mode host is read through its tmux from here: titles and screen rules only.

The hook record is the source of truth for hook-driven agents. The other sources only refine it
in two narrow cases: two consecutive registry samples saying idle (or, without a registry, the
idle prompt box visible for three polls, six while Claude's registry still reports it busy) end
a *working* state that no hook closed; the idle
prompt box visible for two polls clears a *blocked* state whose dialog is gone. A `~` before an
agent's name means no hook data has arrived for that pane (restart the agent after `flok install`).

The hook keeps one more thing, beside the pane's record: a journal per session, the turns with
the first line of each answer, the tools as names and counts, the blocks, questions, waits and
errors. It is what the session's report in your notes is made of, when `[notes]` hands it to
[nt](https://github.com/w4jnl/nt); the sidebar itself never reads it.
[Session reports](notes.md)

## States

| state | glyph | colour | entered by | left by |
|---|---|---|---|---|
| working | `◐◓◑◒` | cyan | prompt submitted, tool start/end | stop, block, interrupted turn |
| paused | `◍` | grey | stop while background subagents, shells or workflows are still running (`bg 2 agents`): answered, but the work will wake the agent again; a monitor (a watch: the comments on an artifact the agent published, a PR, a log tail) does not count, it may never fire | the task wakes it (working), a stop with nothing in flight (done or idle) |
| blocked | `●` | orange | permission request, `AskUserQuestion`, elicitation dialog, a visible prompt the hooks missed | tool end, next prompt, stop, dialog gone |
| done | `✓` | green | stop while the pane is not the one you look at | looking at it (or jumping there) |
| idle | `○` | grey | session start, stop while you watch, done once seen | prompt |
| unknown | `◌` | grey | agent present, no signal yet | any signal |

A *done* agent keeps an unread counter (`done · 2` = one block plus one completion you did not
see). Looking at the pane, `Enter`, a click or `prefix o` marks it seen. Sounds: blocked and done
play their own sound, error a third; nothing plays for the pane that is currently in front of
you (set `[sounds] when_focused = true` to hear it there too), and repeats within 750 ms are
dropped.

## tmux versions

| tmux | shipped by | what flok does there |
|---|---|---|
| 3.3 and newer | RHEL 10 (3.3a), Debian/Ubuntu (3.4+), Homebrew (3.7) | everything |
| 3.2a | RHEL 9 | full sidebar; the keybinds popup has no border or title; no `allow-passthrough` for the inner server's apps; terminal focus is not tracked (done/idle assumes the terminal is focused) |
| 2.7 | RHEL 8 | degraded: keybinds help opens in a new window instead of a popup, no extended keys (shift+enter-style bindings inside agents) through the outer server, a crashed sidebar's pane closes instead of staying respawnable |

Below 2.7 `flok up` refuses to start. The gates live in `internal/tmux/version.go`; CI runs the
end-to-end suites on macOS (3.7), Ubuntu (3.4) and Rocky 9 (3.2a); 2.7 is no longer part of CI.
