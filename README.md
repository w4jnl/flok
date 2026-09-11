# flok

A [herdr](https://herdr.dev)-like agent sidebar for tmux. One narrow pane on the left shows your
**sessions** (with git branch and a state dot) on top and your **agents** (Claude Code and Copilot
CLI panes, with state, current tool and unread count) at the bottom. It plays a sound
when an agent needs input or finishes while you are looking elsewhere, collapses to a compact rail,
hides completely, and opens a `prefix ?` keybinds help built from your live tmux bindings.
Nothing more.

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

## How it works

`flok up` starts a tiny **outer** tmux server (socket `flok`, `prefix None`, no status bar)
with two panes: the sidebar on the left and, on the right, a plain `tmux attach` to your normal
(**inner**) server. Every key and mouse event passes straight through to the inner tmux; your
prefix, plugins, layouts and resurrect state are untouched. The sidebar drives the inner client
with `switch-client`, so selecting a space or an agent just moves you there.

Agent state comes from four sources, most authoritative first:

1. **Hooks** – Claude Code and Copilot CLI call `flok hook` on every event (prompt, tool
   start/end, permission request, question, stop, session end). This gives precise
   working / blocked / done / idle transitions, the current tool, and the sounds.
2. **Claude's registry** – `claude agents --json`, matched to panes through the process tty.
3. **Pane title** – Claude Code shows a spinner while working and `✳` when idle.
4. **Screen rules** – herdr's Apache-2.0 detection manifests evaluated over `capture-pane`, the
   pane title and tmux's OSC 9;4 progress state, used
   for agents without hooks (e.g. Copilot before `install --copilot`, sessions started before the
   hooks were installed). Rules live in `internal/rules/manifests/` and can be overridden per
   agent in `~/.config/flok/agents/<id>.toml`.

States: **working** (cyan spinner), **blocked** (orange: `perm:Bash`, `question`, `elicit`,
`prompt`), **done** (green, finished while not being viewed, cleared when you look at it),
**idle**, **unknown**. Agents are sorted blocked > done > working > idle. An agent row shows the
project (base name of the agent's working directory, like herdr's workspace) with the state
detail on the right, and below it the agent kind plus the agent's own session title. A `~`
before the name means that agent has no hook data (restart it after `install --claude`).

## Install

```sh
git clone git@github.com:w4jnl/flok.git && cd flok
make install                       # builds bin/flok and copies it to ~/.local/bin
flok install                 # Claude Code hooks + Copilot hooks (if ~/.copilot exists) + prints the tmux snippet
```

Paste the printed snippet **below the tpm `run` line** of your tmux.conf and reload it. Restart
running Claude Code sessions so they pick up the hooks. Then, from a plain terminal (not inside
tmux):

```sh
flok up
```

`flok doctor` checks tmux version, hooks, sounds, manifests and the running outer session.

Shell completion (commands, flags, pane ids for `explain`, client ttys for `--client`):

```sh
echo 'eval "$(flok completion bash)"' >> ~/.bashrc
echo 'eval "$(flok completion zsh)"'  >> ~/.zshrc     # after compinit; or: flok completion zsh > ~/.zfunc/_flok
```

## Keys

Inside the inner tmux (your prefix, `C-a` by default in the snippet):

| key | action |
|---|---|
| `prefix b` | toggle sidebar between full width and the compact rail |
| `prefix B` | hide / show the sidebar (zooms the work pane) |
| `prefix g` | put the keyboard in the sidebar (j/k, Enter, Esc back) |
| `prefix o` | jump to the newest agent needing input, else the newest finished one |
| `prefix a` / `prefix A` | next / previous agent pane (sidebar order) |
| `prefix ?` | keybinds help popup: all live bindings, grouped, filter with `/` (`?` in the sidebar opens the same popup) |

In the sidebar pane (`prefix g`, a click, or `flok focus`): `j/k` move, `Enter` open and hand
the keyboard to the work pane, `Tab` switch panel, `1-9` open agent N, `!@#$%^&*(` open session
N, `?` help, `Esc`/`q` back to the work pane. Mouse: a click opens the row and keeps the keyboard
in the sidebar; wheel scrolls. The footer says where the keyboard is.

## Commands

```
flok up [--detach]   start or re-attach the outer session (your server keeps running)
flok down            stop the outer session
flok status [--json] one-shot dump of sessions and agents
flok jump|next|prev  navigation (used by the bindings; --client <tty> optional)
flok toggle|hide|focus
flok reload                restart the sidebar pane after editing config.toml
flok completion bash|zsh   print a shell completion script
flok keys [--print [--filter q]]
flok explain [pane]  which screen rules match (debug detection)
flok install [--claude] [--copilot] [--tmux]
flok doctor
```

## Configuration

`~/.config/flok/config.toml`, every key optional (defaults shown):

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
width = 28
rail_width = 6              # rail rows: sessions as ①②…, then "<n> <status>" per agent
rail_threshold = 12         # narrower than this renders the rail
sessions_max_ratio = 0.4    # at most this share of the height for the sessions list
session_order = "index"     # same order as tmux's chooser (prefix s): index | name | activity
agent_rows = 2              # 2: project + "kind · title" line per agent; 1: single line
show_branch = true
branch_source = "active_pane"   # or "session_path"
poll_ms = 1000
registry_poll_ms = 5000
screen_poll_ms = 2000
capture_lines = 0           # extra scrollback lines for screen rules (0 = visible screen only)

[agents]
enabled = ["claude", "copilot"]
manifest_dir = "~/.config/flok/agents"
use_herdr_cache = false     # also read herdr's own manifest cache when herdr is installed
screen_rules = "auto"       # auto (hook-less agents only) | always | never

[keys]
show_mouse = false
tables = ["prefix", "root", "copy-mode-vi"]
[keys.labels]               # command prefix -> label overrides for the help popup
# "select-pane -L" = "west"

[sounds]
enabled = true
player = "hook"             # hook | sidebar | none
volume = 0.6
min_interval_ms = 750
when_focused = false
done = ""                   # empty = herdr's bundled done.mp3; or e.g. "/System/Library/Sounds/Glass.aiff"
blocked = ""                # empty = herdr's bundled request.mp3 (also used for error)
error = ""

[theme]                     # Dracula by default; state tokens may name a colour or a hex value
working = "cyan"
blocked = "orange"
done = "green"
idle = "comment"
```

State lives in `~/.local/state/flok/`: `agents/` (hook records), `seen/`, `events.log`,
`hook.log`, `runtime.json`, the rendered `outer.conf`.

## Development

```sh
make test              # unit tests
scripts/e2e/m1.sh      # headless end-to-end suites on isolated tmux servers (m1..m4)
scripts/spike/m0-outer.sh check   # nested-outer passthrough checks
scripts/spike/m0-outer.sh up      # interactive checklist (CHECKLIST.md) against your real server
```

Layout: `internal/tmux` (exec client + one-call snapshot), `internal/agent` (model + adapters),
`internal/state` (hook state machine + file store), `internal/rules` (manifest engine),
`internal/merge` (authority merge), `internal/ui` (Bubble Tea sidebar + help), `internal/launcher`
(outer server), `internal/nav`, `internal/keys`, `internal/install`, `internal/cli`.

## Notes and limitations

- macOS first: sounds use `afplay`. Everything else is plain tmux and works on Linux; a remote
  mode (sidebar on a Linux box, sound on the Mac) is left for later.
- The outer server is disposable: killing it never touches your sessions.
- Detection manifests follow the agents' UIs; when Claude Code or Copilot change their screens,
  update `internal/rules/manifests/` (or drop herdr's newer file into the override dir).
- Screen rules evaluate the visible pane only; dismissed prompts in scrollback are ignored.

## License

MIT. The detection manifests are copied from herdr under the Apache License 2.0; see `NOTICE`.
