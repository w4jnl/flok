# Keys

[‹ README](../README.md)

The keys flok binds in your tmux are listed in the [README](../README.md#keys). This page has
how they get there, the keys inside the sidebar, going back across servers, and the help popup.

## How the keys get there

The sidebar binds these keys in your tmux server when it starts and removes them when it ends
(`flok down`, a detach, a crash of the outer): nothing in tmux.conf. `[keys] bind` says how far
it goes: `missing` (the default) binds only the keys nothing else uses, so tmux's own `prefix o`
(next pane) and `prefix ?` (list-keys) keep working and flok's jump and help are reached from the
sidebar instead; `all` takes those over for the session too and puts them back at the end; `off`
binds nothing. If you would rather have the bindings in your config, `flok install --tmux` prints
the same keys as a snippet (paste it below the tpm `run` line, where plugins cannot overwrite
it); the sidebar then leaves them alone. A key still running an older snippet's flok command
(`prefix A` was "previous agent", `prefix S` the servers menu) is brought up to date for the
session, the footer says so, and the current snippet replaces the old one. `flok doctor` lists
which keys flok has, which are unbound, which tmux or you bound to something else and which an
older snippet still holds.

## Inside the sidebar

Inside the sidebar (`prefix g`, a click, or `flok focus`):

| key | action |
|---|---|
| `j` `k` / arrows / wheel | move the cursor |
| `Tab` / `Shift-Tab` | cycle the panels: servers (with remote hosts), sessions, agents |
| `Space` | servers panel: bring the host's work pane to the front, keyboard stays in the sidebar |
| `c` / `d` | servers panel: connect / disconnect the host (persisted: `off` until `c`) |
| `r` | servers panel: reconnect the host now (elsewhere: refresh) |
| `m` | servers panel: flip the host's mode, full ↔ plain |
| `x` | servers panel: remove the host (asks `y/n` in the footer) |
| `i` | servers panel: host details (target, mode, state and hint, flok and tmux versions, agents) |
| `p` | servers panel: pair a phone, a popup shows the code and its QR (`flok phone pair --open`) |
| `Enter` | open the selected row and hand the keyboard to the work pane (a servers row brings that host's work pane to the front) |
| `1`-`9` | open agent N; `!` `@` `#` … open session N |
| `g` / `G` | first / last row |
| `n` | name the agent row under the cursor (Enter saves; an empty name restores the directory name) |
| `?` | keybinds help |
| `Esc` / `q` | keyboard back to the work pane |

An agent row is named after the directory its session started in (a `cd` Claude keeps between
Bash calls does not rename it); `n` gives it a name of your own, which sticks to that pane across
the agent's sessions (`names.json` in the state dir). A click opens the row but keeps the
keyboard in the sidebar. The footer says where the keyboard
is; in rail mode the `›` cursor is pink while the sidebar has it. Any `prefix <key>` chord typed
while the sidebar has focus is replayed into the work pane, so all your bindings keep working;
only `prefix b` keeps the cursor in the sidebar so you can collapse it and continue.

## Going back, on any server

`flok last session` and `last server` return to the previously
focused session or server, wherever that was: the sidebar keeps one focus history across every
server, so the key that left a session on `beta` for one here brings you back to `beta`, host
swap included. `last window` and `last pane` go back within the current session and the current
window, as tmux's own `last-window` and `last-pane` do, so the same keys behave the same inside
a remote session. `last agent` returns to the previous agent pane, on any server. They have no
default key; name yours in `config.toml`:

```toml
[keys.map]
Tab = "last session"
l = "last window"
";" = "last pane"
O = "last server"
"'" = "last agent"
```

Any flok key command works there (`Tab = "menu sessions"`). A mapped key is bound whatever it
held, locally and inside every connected host's tmux (the host's flok must be as new as this
one; the footer says so otherwise), and put back when flok ends; the keybinds help lists them
in their own `flok · going back` section. Without a running sidebar they fall back to tmux's own
`last-*`.

## The help popup

`prefix ?` runs `flok keys` in a tmux popup. It reads `list-keys` for the prefix, root and
copy-mode tables of the inner server, keeps tmux's own notes as labels for stock bindings,
translates common commands for the rest (`select-pane -L` becomes "pane left"), groups plugin
bindings by the plugin directory in their `run-shell` path, and hides mouse bindings unless asked.
Nothing is hand-maintained: what the popup shows is what your server has bound right now.
