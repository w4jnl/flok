<p align="center"><img src="assets/brand/flok-hero.png" alt="flok — a herdr-style agent sidebar for tmux" width="800"></p>

[![ci](https://github.com/w4jnl/flok/actions/workflows/ci.yml/badge.svg)](https://github.com/w4jnl/flok/actions/workflows/ci.yml)

A [herdr](https://herdr.dev)-style agent sidebar for tmux.

flok adds one narrow pane to the left of your normal tmux: **sessions** on top (name, git branch,
state dot), **agents** below (every Claude Code or Copilot CLI pane, with its state, current tool
and unread count). It plays a sound when an agent needs your input or finishes while you are
looking elsewhere, collapses to a six-column rail, hides completely, and opens a `prefix ?`
keybinds help built from your live tmux bindings. Your tmux server, config, plugins and layouts
are not touched.

```
┌──────────────────────────┬────────────────────────────────────────┐
│ sessions                 │                                        │
│  ◑ Claude          main  │   your normal tmux server, untouched   │
│  ○ Hugo            main  │   (sessions, windows, plugins, keys)   │
│  ● flok            main  │                                        │
│                          │                                        │
│ agents · 1     priority  │                                        │
│  ● flok        perm:Bash │                                        │
│    claude · feasibility  │                                        │
│  ◑ trading-jou… Bash 0:42│                                        │
│    claude · add-settle…  │                                        │
│  ○ mwrelay               │                                        │
│    claude · store-forwa… │                                        │
│ j/k ⏎ ⇥ 1-9      ? help  │                                        │
└──────────────────────────┴────────────────────────────────────────┘
```

## Scope and status

flok is a personal tool. I built it for my own workflow: several Claude Code sessions in
parallel, tmux everywhere, Ghostty on macOS, herdr's sidebar as the model of what I wanted and
nothing beyond it. It is published because there is no reason not to, and because the approach
(a nested outer tmux server, hook-driven agent state) may be useful to others.

What that means in practice:

- Features are the ones I need. Requests that do not fit my workflow will probably be declined,
  politely.
- macOS is the first-class platform; on Linux flok is the terminal sidebar alone (no menu bar
  companion), sounds go through whichever player is installed, and the end-to-end suites run on
  both in CI.
- There is no compatibility promise between versions yet. Read the release notes before
  `brew upgrade`.
- Bug reports with a reproduction are welcome; support is best effort.

## Features

- **Sessions panel**: every tmux session with the git branch of its active pane, the current one
  highlighted, a state dot rolled up from the agents inside it.
- **Agents panel**: every agent pane across all sessions, sorted by attention (blocked, then done,
  then working, then idle), two lines per agent: project and state detail, agent kind and its own
  session title.
- **Live states** from hooks: working with the current tool and elapsed time (`Bash 0:42`),
  blocked (`perm:Bash`, `question`, `elicit`), done with an unread count, idle.
- **Sounds** when an agent gets blocked or finishes in a pane you are not looking at; never for
  the pane in front of you; debounced so ten agents finishing together beep once.
- **Navigation**: click or `Enter` on a row to jump there; `prefix o` jumps to whatever needs you.
- **Rail** mode at six columns, hide mode at zero, both a keystroke away. The `[flok]` wordmark
  sits on top of the wide sidebar, the mark on the rail.
- **Keybinds help**: `prefix ?` opens a popup listing all live bindings of your tmux server,
  grouped (flok, prefix, no prefix, copy-mode, plugins), with tmux's own notes as labels and `/`
  to filter.
- **Menu bar companion** (macOS, opt-in): a flok icon in the menu bar that spins while agents
  work, a badge for agents waiting for you, and a dropdown of agents; a click brings the terminal
  window to the front and puts you on that agent's pane.
- **Zero footprint** on your tmux: no plugin, no pane injected into your windows, nothing saved by
  resurrect. Kill the outer server and everything is as before.

## How it works

### Two tmux servers

```
Ghostty (or any terminal)
└── outer tmux server   socket "flok" · prefix None · status off · mouse on
    └── session "flok"
        ├── left pane   flok sidebar            (Bubble Tea program)
        └── right pane  tmux attach ───────────► inner tmux server   (your normal server)
                                                 ├── session Claude   ├─ window 1 ─ pane: claude
                                                 ├── session Hugo     │            ─ pane: zsh
                                                 └── session ...      └─ ...
```

`flok up` starts the outer server from a generated config and attaches your terminal to it. The
outer server has no prefix key and no status bar, so every keystroke and mouse event reaches the
inner tmux exactly as before; it merely frames your session with a sidebar. The right pane runs an
attach loop: a killed session re-attaches elsewhere, a deliberate detach (`prefix d`) tears the
outer down and returns you to the shell.

The sidebar never modifies the inner server. It reads it (`list-sessions`, `list-panes`,
`list-clients`, `capture-pane`, `list-keys`) and drives your client with `switch-client`,
`select-window` and `select-pane`. The `prefix` bindings you paste into your tmux.conf are plain
`run-shell` calls to `flok jump|next|prev|toggle|hide|focus` and a `display-popup` for the help.

### Where agent state comes from

```
 Claude Code / Copilot CLI ──hooks──► flok hook ──► ~/.local/state/flok/agents/<pane>.json
                                                  (per-pane record, flock + atomic write, plays the sound)
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

The hook record is the source of truth for hook-driven agents. The other sources only refine it
in two narrow cases: two consecutive registry samples saying idle (or, without a registry, the
idle prompt box visible for three polls) end a *working* state that no hook closed; the idle
prompt box visible for two polls clears a *blocked* state whose dialog is gone. A `~` before an
agent's name means no hook data has arrived for that pane (restart the agent after `flok install`).

### States

| state | glyph | colour | entered by | left by |
|---|---|---|---|---|
| working | `◐◓◑◒` | cyan | prompt submitted, tool start/end; a Stop while background subagents or shells are in flight keeps it working ("2 agents") | stop with nothing in flight, block, interrupted turn |
| blocked | `●` | orange | permission request, `AskUserQuestion`, elicitation dialog, a visible prompt the hooks missed | tool end, next prompt, stop, dialog gone |
| done | `●` | green | stop while the pane is not the one you look at | looking at it (or jumping there) |
| idle | `○` | grey | session start, stop while you watch, done once seen | prompt |
| unknown | `◌` | grey | agent present, no signal yet | any signal |

A *done* agent keeps an unread counter (`done · 2` = one block plus one completion you did not
see). Looking at the pane, `Enter`, a click or `prefix o` marks it seen. Sounds: blocked and done
play their own sound, error a third; nothing plays for the pane that is currently in front of
you, and repeats within 750 ms are dropped.

### The menu bar companion (macOS)

```
sidebar ──publishes──► ~/.local/state/flok/snapshot.json ──fsnotify──► flok-bar   [ ⩓ ◑ ● 2 ]
                                                                            │ click on an agent row
                                                                            ▼
                                                                   flok goto <pane-id>
                                                       (switch the inner client, mark seen,
                                                        focus the terminal window)
```

`flok-bar` is a second binary, the only one that needs cgo (`fyne.io/systray`), built on macOS
only. It never talks to tmux itself: the sidebar publishes its merged view as a JSON snapshot
(rewritten on change and every 5 s as a heartbeat), the bar renders it and forwards clicks to
`flok goto`. The title text is `○` when everything is idle, an animated `◐◓◑◒` while an agent
works, and `● N` when N agents are waiting for you (the icon gains a dot as well). The dropdown
lists agents in the sidebar's order with the same state detail. With `[bar] enabled = true`,
`flok up` starts the bar (single instance) and `flok down` or a detach ends it; the bar also quits
by itself 30 s after flok disappears.

Click-to-return uses AeroSpace when it is installed (`aerospace focus --window-id` on the window
titled `TMUX…`, which also switches workspace) and AppleScript otherwise: it activates the terminal
app that ran `flok up` (recorded from `TERM_PROGRAM`: Ghostty, iTerm2, Terminal, WezTerm, kitty) and,
best effort, raises the `TMUX…` window. Raising a specific window goes through System Events and
needs Accessibility permission for `osascript`; without it the app comes to the front with its
last-used window, which is usually the right one anyway.

### The help popup

`prefix ?` runs `flok keys` in a tmux popup. It reads `list-keys` for the prefix, root and
copy-mode tables of the inner server, keeps tmux's own notes as labels for stock bindings,
translates common commands for the rest (`select-pane -L` becomes "pane left"), groups plugin
bindings by the plugin directory in their `run-shell` path, and hides mouse bindings unless asked.
Nothing is hand-maintained: what the popup shows is what your server has bound right now.

## Requirements

- tmux 2.7 or newer, macOS or Linux. Everything works from 3.3; older servers lose a little
  (see the table below). `flok doctor` prints the version it found and what that version lacks.
- Claude Code and/or GitHub Copilot CLI for hook-driven state. Other agents get title and
  screen-rule detection only (manifests exist for Codex, Gemini and OpenCode).
- A sound player for the notification sounds: `afplay` on macOS; on Linux the first of `mpv`,
  `ffplay`, `pw-play`, `paplay`, `play` (sox) found on PATH, or any command via `[sounds] command`.
  Without one flok rings the terminal bell instead (`[sounds] bell = "auto"`), which reaches
  your local terminal even when the agents run on a remote host over ssh.
- Go 1.27 only if you build from source.

### tmux versions

| tmux | shipped by | what flok does there |
|---|---|---|
| 3.3 and newer | RHEL 10 (3.3a), Debian/Ubuntu (3.4+), Homebrew (3.7) | everything |
| 3.2a | RHEL 9 | full sidebar; the keybinds popup has no border or title; no `allow-passthrough` for the inner server's apps; terminal focus is not tracked (done/idle assumes the terminal is focused) |
| 2.7 | RHEL 8 | degraded: keybinds help opens in a new window instead of a popup, no extended keys (shift+enter-style bindings inside agents) through the outer server, a crashed sidebar's pane closes instead of staying respawnable |

Below 2.7 `flok up` refuses to start. The gates live in `internal/tmux/version.go`; CI runs the
end-to-end suites on macOS (3.7), Ubuntu (3.4), Rocky 9 (3.2a) and Rocky 8 (2.7).

## Install

Homebrew, from the `w4jnl/tap` tap:

```sh
brew install w4jnl/tap/flok
```

On Linux, a prebuilt static binary from the [releases page](https://github.com/w4jnl/flok/releases)
(no Go, no root; `x86_64`/`amd64` and `aarch64`/`arm64`):

```sh
ver=0.3.0; arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
curl -fsSLO "https://github.com/w4jnl/flok/releases/download/v$ver/flok_${ver}_linux_${arch}.tar.gz"
curl -fsSLO "https://github.com/w4jnl/flok/releases/download/v$ver/sha256sums.txt"
sha256sum -c --ignore-missing sha256sums.txt
mkdir -p ~/.local/bin && tar -xzf "flok_${ver}_linux_${arch}.tar.gz" --strip-components=1 -C ~/.local/bin "flok_${ver}_linux_${arch}/flok"
flok install && flok doctor            # hooks into Claude Code / Copilot, tmux snippet, version report
```

Or from source:

```sh
git clone git@github.com:w4jnl/flok.git && cd flok
make install                       # builds bin/flok and copies it to ~/.local/bin
```

Then wire it up once:

```sh
flok install                       # Claude Code hooks, Copilot hooks (if ~/.copilot exists), a commented
                                   # config.toml with the defaults, and it prints the tmux snippet
```

Paste the printed snippet **below the tpm `run` line** of your tmux.conf (bindings placed above
it get overwritten by plugins), reload tmux, and restart running agent sessions so they load the
hooks. Start flok from a plain terminal, not from inside tmux:

```sh
flok up
```

`flok doctor` checks tmux, hooks, sounds, manifests and the running outer session. Shell
completion (commands, flags, pane ids for `explain`, client ttys for `--client`):

```sh
echo 'eval "$(flok completion bash)"' >> ~/.bashrc
echo 'eval "$(flok completion zsh)"'  >> ~/.zshrc     # after compinit; or: flok completion zsh > ~/.zfunc/_flok
```

Launching from a window manager or a terminal binding: `flok up` exits 0 after a detach or
`flok down`, so a command like `flok up || tmux attach || tmux new-session` falls back to plain
tmux only when flok cannot start. `flok up` starts the inner tmux server itself when needed.

## Keys

In tmux (your prefix; the snippet assumes `C-a`):

| key | action |
|---|---|
| `prefix b` | toggle the sidebar between full width and the rail |
| `prefix B` | hide / show the sidebar (zooms the work pane) |
| `prefix g` | move the keyboard into the sidebar, or back to the work pane |
| `prefix o` | jump to the newest agent needing input, else the newest finished one |
| `prefix a` / `prefix A` | next / previous agent pane, in sidebar order |
| `prefix ?` | keybinds help popup (`?` inside the sidebar opens the same) |

Menu bar (when enabled): click an agent row to return to it, "Show flok" to bring the terminal
window to the front, "Edit config…" to open `config.toml` (in a new tmux window with `[bar] editor`
set, else with the default app), "Reload sidebar" to apply it, "Quit flok-bar" to remove the item
(flok keeps running).

Inside the sidebar (`prefix g`, a click, or `flok focus`):

| key | action |
|---|---|
| `j` `k` / arrows / wheel | move the cursor |
| `Tab` | switch between the sessions and agents panels |
| `Enter` | open the selected row and hand the keyboard to the work pane |
| `1`-`9` | open agent N; `!` `@` `#` … open session N |
| `g` / `G` | first / last row |
| `?` | keybinds help |
| `Esc` / `q` | keyboard back to the work pane |

A click opens the row but keeps the keyboard in the sidebar. The footer says where the keyboard
is; in rail mode the `›` cursor is pink while the sidebar has it. Any `prefix <key>` chord typed
while the sidebar has focus is replayed into the work pane, so all your bindings keep working;
only `prefix b` keeps the cursor in the sidebar so you can collapse it and continue.

## Commands

```
flok up [--detach]          start or re-attach the outer session (your server keeps running)
flok down                   stop the outer session
flok status [--json]        one-shot dump of sessions and agents
flok jump | next | prev     navigation, used by the bindings           [--client <tty>]
flok toggle | hide | focus  sidebar layout and keyboard focus
flok goto [pane-id] [--no-focus]   switch to an agent pane and bring the terminal window to the front
flok edit-config [--no-focus]      open config.toml (new tmux window with [bar] editor, else default app)
flok reload                 restart the sidebar pane after editing config.toml
flok keys [--print [--filter q]]   keybinds help; --print dumps it as text
flok explain [pane ...]     which screen-detection rules match agent panes
flok install [--claude] [--copilot] [--tmux]
flok doctor
flok completion bash|zsh
```

## Configuration

`~/.config/flok/config.toml`; every key is optional, defaults shown. `flok install` writes this file
with everything commented out if it does not exist; `flok reload` applies edits.

```toml
[inner]
socket = "default"          # your tmux server (-L name)
session = ""                # attach a specific session; empty = most recent
reattach_on_detach = false  # false: `prefix d` closes the flok window like a normal detach;
                            # true: it re-attaches at once. A killed session always re-attaches elsewhere.

[outer]
socket = "flok"
session = "flok"
extra_conf = "~/.config/flok/outer.extra.conf"   # sourced by the generated outer config

[sidebar]
width = 28                  # pinned: window resizes never scale the sidebar
rail_width = 6              # rail rows: sessions as ①②…, then "<n> <status>" per agent
rail_threshold = 12         # narrower than this renders the rail
sessions_max_ratio = 0.4    # at most this share of the height for the sessions list
session_order = "index"     # same order as tmux's chooser (prefix s): index | name | activity
agent_rows = 2              # 2: project + "kind · title" line per agent; 1: single line
show_branch = true
brand = true                # [flok] wordmark on top (the mark on the rail)
branch_source = "active_pane"   # or "session_path"
poll_ms = 1000
idle_poll_ms = 3000         # while the sidebar is hidden (prefix B); the spinner pauses too
spinner_ms = 250            # working-spinner frame interval in the sidebar
fps = 15                    # renderer frame-rate cap; 60 (Bubble Tea default) wakes up needlessly often
registry_poll_ms = 10000    # `claude agents --json` costs ~0.2 s CPU: polled at this rate only while a
                            # Claude turn/prompt is open or a pane lacks hooks, otherwise once a minute
screen_poll_ms = 2000
capture_lines = 0           # extra scrollback lines for screen rules (0 = visible screen only)

[agents]
enabled = ["claude", "copilot"]
manifest_dir = "~/.config/flok/agents"   # per-agent TOML overrides of the bundled manifests
use_herdr_cache = false     # also read herdr's own manifest cache when herdr is installed
screen_rules = "auto"       # auto | always | never

[keys]
show_mouse = false
tables = ["prefix", "root", "copy-mode-vi"]
[keys.labels]               # command prefix -> label overrides for the help popup
# "select-pane -L" = "west"

[sounds]
enabled = true
player = "hook"             # hook | sidebar | none (who plays: the hook process or the sidebar)
command = ""                # "" = first on PATH of afplay, mpv, ffplay, pw-play, paplay, play;
                            # or your own, e.g. "paplay --volume=40000 {file}" ({file}, {volume} expand)
bell = "auto"               # auto: ring the terminal bell instead when no player exists (headless or
                            # ssh hosts; the bell reaches your local terminal); always: bell and sound; never
volume = 0.6
min_interval_ms = 750
when_focused = false
done = ""                   # empty = bundled done.wav; or e.g. "/System/Library/Sounds/Glass.aiff"
blocked = ""                # empty = bundled request.wav (also used for error)
error = ""

[bar]                       # macOS menu bar companion, started by `flok up` (ignored on Linux)
enabled = false
animate = true              # spin ◐◓◑◒ in the menu bar while an agent works
animate_ms = 500            # frame interval; every frame redraws the status item, so 2 fps by default
color = true                # icon teal while an agent works, icon and badge orange while one waits on
                            # you; the spinner stays in the menu bar colour (state palette; no extra CPU)
icon_size = 18              # icon height in points (16 is the usual size)
blink = true                # while an agent waits on you the icon blinks (outline/solid, ~1.15 s) until
                            # the terminal window is focused
badge = true                # "● N" for agents waiting for you (icon gains a dot too)
focus = "auto"              # click-to-return: auto | aerospace | applescript | none | "shell command"
app = ""                    # terminal to focus; empty = the one `flok up` ran in (TERM_PROGRAM)
max_rows = 16
editor = ""                 # "Edit config…" in the bar: "nvim" opens it in a new tmux window; "" = `open`

[theme]                     # Dracula by default; state tokens may name a colour or a hex value
mode = "auto"               # auto: follow the terminal's background, asked at `flok up` | dark | light
working = "cyan"
blocked = "orange"
done = "green"
idle = "comment"
brand = "#3FD0D4"           # wordmark / rail mark accent

[theme.light]               # palette for light terminals (Dracula's Alucard); same keys as [theme]
brand = "#12999D"
```

## Files

| path | content |
|---|---|
| `~/.config/flok/config.toml` | configuration |
| `~/.config/flok/agents/*.toml` | your overrides of the detection manifests |
| `~/.local/state/flok/agents/` | one JSON record per agent pane, written by the hook |
| `~/.local/state/flok/seen/` | when you last looked at each agent pane |
| `~/.local/state/flok/events.log` | every hook event with the resulting state (JSON lines) |
| `~/.local/state/flok/runtime.json` | the running outer session: panes, sockets, client tty, terminal app |
| `~/.local/state/flok/snapshot.json` | the sidebar's merged view, read by flok-bar |
| `~/.local/state/flok/flok-bar.pid` | the menu bar process started by `flok up` |
| `~/.local/state/flok/outer.conf` | the generated outer tmux config |
| `~/.claude/settings.json` | the hook entries `flok install --claude` adds (a backup is written) |
| `~/.copilot/hooks/flok.json` | the Copilot CLI hook file |

`FLOK_CONFIG` and `FLOK_STATE` override the two locations.

## Troubleshooting

- `flok doctor` first. It reports the binary path the hooks use, missing hooks, the tmux snippet,
  the manifests and whether the outer is running.
- `flok status` shows what the sidebar sees, with the source of each state (`hook`, `registry`,
  `title`, `screen`). `flok explain <pane>` lists the screen rules matching a pane.
- `tail -f ~/.local/state/flok/events.log` while an agent works shows the hook events arriving.
  No events for a pane usually means the agent was started before `flok install`; restart it.
- `FLOK_DEBUG=1 flok up` logs every key the sidebar receives to `sidebar.log` in the state dir.
- If you moved the binary (for example from `make install` to Homebrew), run `flok install` again;
  it rewrites the hook commands to the new absolute path.

## Development

```sh
make build             # bin/flok with the version stamped from git describe
make test              # unit tests
scripts/e2e/m1.sh      # headless end-to-end suites on isolated tmux servers, m1..m6
make icons             # regenerate the menu bar template icons (assets/icons/gen)
scripts/spike/m0-outer.sh check   # nested-outer passthrough checks
scripts/spike/m0-outer.sh up      # interactive checklist against your real server
```

The end-to-end suites start their own tmux servers (`e2e-inner`, `e2e-outer`) with a private
state and config directory, run a fake agent binary named `claude` that paints scripted screens,
replay hook payloads against it, and assert on `capture-pane` output of the sidebar. Your real
tmux server is never touched.

```
cmd/flok               entry point
cmd/flok-bar           macOS menu bar companion (cgo, fyne.io/systray); everything else is cgo-free
internal/cli           subcommands (up, sidebar, hook, nav, keys, install, doctor, ...)
internal/launcher      outer server: config template, create/attach, attach loop, runtime.json
internal/ui            Bubble Tea sidebar, rail, help overlay
internal/snapshot      snapshot.json the sidebar publishes for flok-bar
internal/bar           menu bar title/rows logic (pure, tested)
internal/focus         bring the terminal window to the front (aerospace, applescript)
internal/merge         authority merge of all state sources into one snapshot
internal/state         hook state machine and the file store (flock, atomic writes)
internal/agent         model, events, adapters (claude, copilot)
internal/rules         herdr manifest engine (regions, matchers), bundled manifests
internal/claudereg     `claude agents --json` reader, pid → tty → pane
internal/tmux          exec-based tmux client, one-call snapshot
internal/keys          list-keys collector and labels for the help
internal/nav           switch-client / select-window / select-pane
internal/notify        sounds (afplay, pw-play, paplay, mpv, ffplay, play or a custom command), debounce
internal/install       settings.json / Copilot hook writers, tmux snippet
internal/config        config.toml, XDG paths
```

Releases: `scripts/release.sh <version>` runs the tests, tags, pushes, bumps the formula in
[w4jnl/homebrew-tap](https://github.com/w4jnl/homebrew-tap) and creates the GitHub release.

## Credits and license

flok is MIT licensed (see `LICENSE`).

The agent-detection manifests in `internal/rules/manifests/` and the two notification sounds in
`internal/notify/sounds/` are copied unchanged from [herdr](https://github.com/herdrdev/herdr),
Apache License 2.0; see `NOTICE` and `LICENSE-APACHE`. herdr also set the bar for what an agent
sidebar should feel like. The terminal UI uses
[Bubble Tea](https://github.com/charmbracelet/bubbletea) and
[Lip Gloss](https://github.com/charmbracelet/lipgloss); the menu bar companion uses
[fyne.io/systray](https://github.com/fyne-io/systray). The menu bar icon (a flock of monoline
chevrons) is generated by `assets/icons/gen` in the style of the W4J mark.
