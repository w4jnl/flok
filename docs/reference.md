# Reference

[‹ README](../README.md)

Every command, every configuration key with its default, and the files flok keeps.

## Commands

```
flok up [--detach]          start or re-attach the outer session (your server keeps running)
flok down                   stop the outer session
flok keep-awake [on|off|toggle|status] [--notify]   keep the Mac awake, display on, while flok runs (macOS)
flok link [on|off|toggle|status] [--notify]   switch the relay link off and on again (stays off until on)
flok link join <relay> [--name <n>] [--yes] [--open] [--json]   join the relay by a code and its QR shown here; a member approves it, Enter accepts, the key lands in link.json
flok link approve <code> | list [--json] | forget <name|id> | leave   let a flok in, the floks of this tenant, revoke one, undo this flok's join
flok link admin [--clear]   keep an admin token in the state dir (link-admin, 0600), read without echo or from stdin; it wins over [link] admin
flok phone pair [--open] [--json]   show a code and its QR for the phone app to scan (5 min, one phone; --open: a tmux popup)
flok phone list [--json] | forget <name|id>   the phones paired with the relay; revoke one (its key, its pushes, its app)
flok tenant list | show <name> | approve <code> --tenant <name> | rename <name> <new> | remove <name>   the relay's tenants (with an admin token: flok link admin, or [link] admin)
flok status [--json]        one-shot dump of sessions and agents
flok jump | next | prev     navigation, used by the bindings           [--client <tty>]
flok menu agents|sessions|servers   a tmux menu over the work pane (prefix A / S / @), 1-9 picks
flok last session|window|pane|agent|server   back to the previous one ([keys.map] binds your keys)
flok toggle | hide | focus  sidebar layout and keyboard focus
flok goto [pane] [--no-focus]   switch to an agent pane (%12 here, beta:%12 on a host) and bring the
                            terminal window to the front
flok edit-config [--no-focus]      open config.toml (new tmux window with [bar] editor, else default app)
flok reload                 restart the sidebar pane after editing config.toml
flok keys [--print [--filter q]]   keybinds help; --print dumps it as text
flok explain [pane ...]     which screen-detection rules match agent panes
flok install [--config] [--claude] [--copilot] [--tmux-conf]   config.toml, the hooks, a starter tmux.conf
                            when there is none; [--tmux] prints the optional key snippet
flok doctor                 checks tmux, hooks, sounds, the outer session and every remote host
flok completion bash|zsh
flok host add [<name>] <user@host|ssh alias> [--mode full|plain] [--socket name] [--session name]
              [--flok /path/to/flok] [--term name] [--disabled] [--install [--hooks]]   register a remote host
flok host install <name> [--from file] [--version v] [--hooks] [--open] [--wait]   put or upgrade flok on the host from here
flok host reconnect <name>        the running sidebar redials the host now
flok host set <name> [--mode full|plain] [--target t] [--socket s] [--session s] [--flok p] [--term t]
flok host remove | connect | disconnect <name>
flok host list [--json|--names]  |  flok host status [--json]  |  flok host front <name>|local|<N> [--focus]
flok host next | prev | last | menu     rotate the server in front, or pick one from a tmux menu
flok serve --stdio | --hello      run headless on this host for a flok elsewhere (started over ssh by it)

flok-relay serve [-config relay.toml] [-listen :8080] [-data /data]   the relay for the phone (deploy/relay)
flok-relay tail -url <relay> -token <device token> [-subscribe inst:%12] [-answer inst:%12 -text y -keys Enter]
flok-relay token | tenants | instances | phones [-data /data] | pin | health [-url …] | version   (the listings read the relay's files)
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
agent_order = "stable"      # stable: rows keep their place (session order, then window and pane) |
                            # priority: blocked, done, working, idle, newest first (rows move)
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
bind = "missing"            # at start, bind flok's keys in your tmux for the session: missing = only
                            # keys nothing else uses | all = override tmux's own o and ? too | off
show_mouse = false
tables = ["prefix", "root", "copy-mode-vi"]
[keys.map]                  # your own keys for flok commands, bound here and in every connected host's
# Tab = "last session"      # tmux while flok runs (the old binding comes back after): the last … commands
# l = "last window"         # go back to the previous session, window, pane or server on any server;
# ";" = "last pane"         # any flok key command works (menu sessions, host next, jump, …)
# O = "last server"
# "'" = "last agent"        # the previous agent pane, on any server
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
when_focused = false        # true: also for the agent pane you are looking at (done, badge and sound
                            # as if you were elsewhere); default: a watched pane ends idle, silently
done = ""                   # empty = bundled done.wav; or e.g. "/System/Library/Sounds/Glass.aiff"
blocked = ""                # empty = bundled request.wav (also used for error)
error = ""

[bar]                       # macOS menu bar companion, started by `flok up` (ignored on Linux)
enabled = false
animate = true              # spin ◐◓◑◒ in the menu bar while an agent works
animate_ms = 0              # frame interval; 0 = the sidebar's spinner_ms (250, 4 fps); every frame
                            # redraws the status item, 500 halves that CPU
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

[keep_awake]                # macOS: `flok keep-awake` / the menu bar's "Keep awake" row
presence = false            # true: while keep-awake is on, also keep you "active" in Teams, Slack and
                            # other apps that watch input idle time (an empty modifier-key event after
                            # 60 s without input; needs Accessibility for your terminal app)

[hosts]                     # remote tmux servers shown next to the local one; the list itself is
                            # dynamic: flok host add [<name>] <user@host> (flok host --help)
ssh = "ssh"                 # ssh binary; aliases, jump hosts and keys come from ~/.ssh/config
ssh_options = []            # extra ssh arguments, e.g. ["-o", "IdentitiesOnly=yes"]; yours come first
connect_timeout_s = 10
backoff_max_s = 30          # reconnect backoff 1, 2, 4 … up to this many seconds
multiplex = true            # one ControlMaster connection per host, shared by the data channel,
                            # tmux calls and the work pane (sockets under the state dir)
session = "main"            # a host whose tmux is not running gets this session from its work pane
keys = true                 # bind flok's keys (prefix b B g o a u U, the menus A S @, N P O F1…F9) in a
                            # host's tmux while connected
remote_path = "/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:/opt/local/bin"
                            # appended to PATH for every command flok runs on a host: a non-interactive
                            # ssh shell sees only the system PATH (Homebrew's tmux would be invisible)
serve_command = "flok serve --stdio"   # what mode full runs on the host; flok host add --flok <path>
                            # names the binary per host when it is not on the non-interactive PATH

[notify]                    # a message on your phone when an agent needs you or finishes
url = ""                    # an ntfy topic (https://ntfy.sh/<your-topic> or your own ntfy), or any endpoint
                            # taking the JSON form; "" = off
token = ""                  # sent as Authorization: Bearer <token>
format = "ntfy"             # ntfy: Title/Priority/Tags headers and a text line | json
events = ["blocked", "done", "error"]

[link]                      # flok on your phone: this flok's name, and the relay it dials out to (a join, flok link join, stands in for url, token and pin)
name = ""                   # "" = the machine's short hostname
url = ""                    # your flok-relay: wss://flok.example.net/link (https://flok.example.net works too); "" = off
token = ""                  # one of the relay's instance_tokens (flok-relay token prints one); not needed after flok link join
admin = ""                  # one of the relay's admin_tokens: this flok may manage the relay's tenants (flok tenant), whichever way it links; alone, it links too; flok link admin keeps it out of this file
pin = ""                    # the relay's key (flok-relay pin): the link runs inside TLS to it, unreadable to a proxy that inspects HTTPS

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
| `~/.local/state/flok/names.json` | row names chosen with `n`, by pane (`%12`, `beta:%12`) |
| `~/.local/state/flok/events.log` | every hook event with the resulting state, and every command from the phone (JSON lines) |
| `~/.local/state/flok/runtime.json` | the running outer session: panes, sockets, client tty, terminal app |
| `~/.local/state/flok/snapshot.json` | the sidebar's merged view, read by flok-bar |
| `~/.local/state/flok/flok-bar.pid` | the menu bar process started by `flok up` |
| `~/.local/state/flok/keep-awake` | `1` while `flok keep-awake` asks the sidebar to keep the Mac awake |
| `~/.local/state/flok/link-off` | `1` while `flok link off` keeps the relay link switched off (kept across sessions) |
| `~/.local/state/flok/keys.json` | the keys the sidebar bound in your tmux for this session, and what `all` replaced |
| `~/.local/state/flok/outer.conf` | the generated outer tmux config |
| `~/.local/state/flok/hosts.json` | the remote hosts (`flok host …`), with `enabled`, a probed `flok` path and `last_connected` |
| `~/.local/state/flok/hosts/<name>/` | a plain-mode host's local store (records, seen marks) |
| `~/.local/state/flok/ssh/` | flok's ssh control sockets (0700) |
| `~/.local/state/flok/requests/` | one-shot commands for the running sidebar (`flok goto beta:%12`, `flok host front`, `flok phone …`, `flok link approve`, `flok tenant …`) |
| `~/.local/state/flok/replies/` | the sidebar's answers to `flok phone`, `flok link approve\|list\|forget` and `flok tenant`, one file per request, swept |
| `~/.local/state/flok/link.json` | this flok's own key to the relay and what it joined as (`flok link join`; 0600); wins over `[link]` in config.toml until `flok link leave` |
| `~/.local/state/flok/serve.lock`, `served` | on a served host: the lock `flok serve` holds and who holds it |
| `~/.claude/settings.json` | the hook entries `flok install --claude` adds (a backup is written) |
| `~/.copilot/hooks/flok.json` | the Copilot CLI hook file |

`FLOK_CONFIG` and `FLOK_STATE` override the two locations.

## Environment

| variable | where | meaning |
|---|---|---|
| `FLOK_CONFIG`, `FLOK_STATE` | yours | the config file and the state directory, instead of the XDG locations above |
| `FLOK_OUTER_SOCKET` | the inner server's global environment (`tmux show-environment -g FLOK_OUTER_SOCKET`) | the outer server's socket path while flok runs, for a program in your sessions that must ask the terminal behind flok (`tmux -S <path> display-message -p …`); set before every attach, removed when the outer goes |
