# Remote hosts

[‹ README](../README.md)

Agents do not have to run where you sit. One flok shows and drives tmux servers on other
machines, over plain `ssh` with your `~/.ssh/config` (aliases, jump hosts, keys or an agent;
flok never answers a password prompt), and treats them like the local one: same panels, same
keys, same sounds, one work pane that shows whichever server is in front:

```sh
flok host add beta jaro@beta              # full: flok on the host streams its agents
flok host add gpu-1 --mode plain          # plain: tmux only, titles and screen rules
flok host install beta                    # no flok there yet? put this one there over ssh
flok host status                          # connect to each once and report
flok doctor                               # probes every host: tmux, flok, hooks, terminfo
```

## Putting flok on a host

A host with tmux but without flok reads `no flok`; `flok host install <name>` (or `I` on its row
in the servers panel, which shows the progress in a popup, or `flok host add … --install`) puts
flok there from your machine, over the same ssh, into `~/.local/bin/flok` on the host, which
flok already looks at, so nothing on the host needs configuring and no root is involved. A
host with your OS and CPU gets this very binary (it works offline and carries dev builds);
another gets the release tarball from GitHub, checked against its `sha256sums.txt`
(`--version` picks one; a local build that is no release takes the latest release). The new
flok must answer `flok version` on the host before it replaces an old one, so a build that
cannot run there changes nothing. `--from <file>` pushes a binary you built yourself.
`--hooks` also runs `flok install --claude` / `--copilot` there for the agent folders it finds
(`~/.claude`, `~/.copilot`). The same command upgrades an old flok; the sidebar reconnects by
itself. A Homebrew flok on the host is shadowed afterwards (`flok host set <name> --flok ""`
returns to it).

## Names and the host list

The first argument of `flok host add` is flok's **name** for the host, the second the ssh
**target**, passed to `ssh` exactly as typed. With the target alone the name is taken from it:
an ssh alias as it is spelled (`flok host add dockerAMS` names it `dockerAMS`), the first label of
a DNS name (`jaro@beta.example.org` → `beta`), an IPv4 address with dashes. Names are letters,
digits, `_` and `-` (at most 32); they keep their case but match in any case, so `flok host
connect dockerams` finds `dockerAMS`, and two names that differ only in case cannot both exist.

Hosts live in `hosts.json` in the state dir, not in `config.toml`: `flok host add|set|remove|
connect|disconnect` (or `c` on a row of the servers panel) changes the set while flok runs, and
`flok up` reconnects the enabled ones. `[hosts]` in `config.toml` holds the ssh defaults.

## Full and plain mode

In **full** mode the local flok runs `flok serve --stdio` on the host: the same hooks, Claude
registry and screen rules the sidebar would use there, merged on the host and streamed back as
JSON lines over the ssh session. The sounds play here; the host's own hooks stay silent while it
is served (a `served` record in its state dir says so). In **plain** mode the local flok drives the
host's tmux over ssh itself: titles and screen rules, no hook states, slower polls; for hosts
where flok cannot be installed.

## Session reports from a host

With `[notes]` on, a full-mode host's agents get session reports in your notes like the local
ones: the host's `flok hook` keeps the journals, `serve` streams a changed one, and the nt here
writes the report with the host's alias as its host (`beta--claude-code--api--1a2b3c4d.md`).
Nothing on the host needs nt. One machine imports a host; `flok host notes <name> off` leaves a
host out, `on --take` claims it from the machine that imports it now.
[Session reports](notes.md)

## The servers panel

The sidebar grows a **servers** panel above the sessions: local first, then the hosts in the
order they were added, each with its mode (`full` or `plain`) after the name, its rollup glyph
and agent count (an orange `· 2` for agents waiting there), or its connection state
(`connecting`, `retry in 8s`, `needs auth`, `no flok`,
`busy`, `incompatible`, `off`). `Enter` on a host brings its work pane next to the sidebar; the
sessions panel then shows that host's sessions (`sessions · beta`). The agents panel shows every
host, local first, remote rows tagged with their host (`beta · claude · fix login`).

```
local machine                                              host "beta" (mode full)
outer tmux -L flok                                         ┌───────────────────────────┐
 window 0        [ sidebar | work pane of the front host ] │ tmux server (yours)       │
 flok-host-beta  [ ssh -t beta tmux attach ]  ◄── swap ──► │  ▲ attached from the pane │
 flok-host-gpu-1 [ ssh -t gpu-1 tmux attach ]              │ flok serve --stdio ◄ hooks│
 sidebar ─ remote.Manager ── ssh -T beta flok serve ──────►│  agents, sessions, focus  │
           merge.Federate ◄── snap / event ── goto / seen ►└───────────────────────────┘
```

## The work pane of a host

Every enabled host keeps a persistent `ssh -t … tmux attach` pane parked in a hidden window of
the outer server, so switching hosts is one `swap-pane`: instant, no reconnect, `flok hide`
keeps working. A host with tmux installed but not running gets a server from that pane (`tmux
new-session -s main`, the name from `[hosts] session` or the host's `--session`), so a fresh box
needs nothing but tmux. The parked pane re-attaches by itself when you detach inside the remote
tmux, and shows one status line and a countdown while the host is unreachable. The data channel
reconnects with a backoff of 1, 2, 4 … 30 s; `needs auth`, `host key`, `no flok` and
`incompatible` are retried once a minute with the fix spelled out, `busy` (another flok already
serves that host) every 10 s. A laptop going to sleep ends the dead ssh within about 45 s; the
host's `flok serve` exits on the closed pipe and its hooks play there again.

## Moving between servers

Navigation crosses hosts: `prefix o` goes to the agent needing you on any host, `1`-`9`, a
click and the menu bar reach any row, `flok goto beta:%12` scripts it; `prefix a` walks the
agents of the host in front, so a step never swaps the work pane. `flok host front beta` (or
`local`) switches hosts from a key binding, `flok host next|prev|last` rotates, `flok menu
servers` (`prefix @`; `flok menu agents` and `flok menu sessions` are its siblings on `prefix A`
and `prefix S`, each menu linking to the other two) shows a tmux menu of them, the menu bar's
servers block does the same with a click, and the same keys work with a remote server in front.
Every switch
flashes `flok: now on <server>` on the status line of the server that came to the front, the
one the work pane shows.

## flok's keys inside a remote session

flok's keys work inside a remote session too, without touching the host's tmux config: while a
host is connected, flok binds `prefix b B g o a u U`, the menus `A S @` and the server keys
`N P O F1`…`F9` in that tmux server (`bind-key` lives in the running server, not in a file) and
puts the host's own bindings of those keys back when it disconnects. What a key had before is
noted in that server too (`@flok-orig-<key>` user
options), so a session that ends abruptly loses nothing: the next one restores from the note.
A connected host also takes your local prefix: its own moves to `prefix2`, so both `C-a b` and
its native chord work there, and `<prefix> <prefix>` still sends the key through; everything
goes back on disconnect (`[hosts] prefix = false` leaves hosts' prefixes alone). A host whose
flok predates the key relay is called out by `flok doctor`, `flok host status`, the host info
screen (`i` in the servers panel) and the sidebar footer instead of failing silently.
On a full-mode host the binding runs `flok relay <cmd>` and `flok serve` forwards it
at once; on a plain host it sets a tmux user option that the next poll picks up (within a
second). Either way the local sidebar runs the same `flok toggle|hide|focus|jump|next|menu …|
host …|keep-awake|link` the local binding would. `[hosts] keys = false` turns it off. `prefix ?`
stays the remote tmux's own list-keys.

## Security

Only ssh: no listening sockets, no daemons. The local side execs the ssh argv (never a
shell) with `BatchMode=yes`, its own ControlMaster socket under `~/.local/state/flok/ssh`
(0700) and no agent forwarding unless `[hosts] ssh_options` adds it; the remote commands are
fixed strings built from validated fields. Host keys are accepted by you, once, with a plain
`ssh <target>`. `flok serve` reads only its own state dir and drives only its own tmux server.
tmux-resurrect stays per host; "restoring" the local side is reconnecting at `flok up`.
