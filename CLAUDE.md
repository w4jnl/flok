# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What flok is

A herdr-like agent sidebar for tmux, written in Go (single binary, no cgo). It runs a tiny
**outer** tmux server (socket `flok`, prefix None, no status bar) with two panes: the Bubble Tea
sidebar on the left and a plain `tmux attach` to the user's normal **inner** server on the right.
The sidebar never modifies the inner server's config; it only drives the inner client with
`switch-client`. The README is accurate and detailed; read it for user-facing behaviour, keys
and config keys.

## Commands

```sh
make build                 # go build -o bin/flok ./cmd/flok
make install               # build + copy to ~/.local/bin (PREFIX overridable)
make test                  # go test ./...
make vet                   # go vet ./...
go test ./internal/merge -run TestHookAuthorityAndSeen -v   # one test
go test ./internal/rules -run TestClaudeFixtures -v         # screen-rule fixtures
make e2e                   # headless end-to-end suites m1..m11 in order (scripts/e2e/run-all.sh); SUITES="m4 m7" for a subset
make relay                 # go build -o bin/flok-relay ./cmd/flok-relay (the phone relay, cgo-free)
scripts/e2e/m1.sh          # one suite (see below); never run two at once, they share the isolated servers
scripts/spike/m0-outer.sh check   # nested-outer passthrough checks on isolated servers
```

`go.mod` says Go 1.27. There is no linter config beyond `go vet`. CI runs gofmt, vet, unit tests
and a build on macOS and ubuntu, and the e2e suites on both as well.

Releases: write the notes first — move the `## Unreleased` items in `CHANGELOG.md` under a new
`## <version> (YYYY-MM-DD)` heading (user-facing wording, newest first); the script refuses to
tag without that section and publishes it as the GitHub release notes; the menu bar's version
row opens `CHANGELOG.md` on GitHub. Then `scripts/release.sh <version>` tags `v<version>`, pushes, then bumps `url`/`sha256` in
`Formula/flok.rb` of the tap clone (`$FLOK_TAP_DIR`, default `../homebrew-tap`, repo
`w4jnl/homebrew-tap`) and creates the GitHub release. `internal/cli.Version` is a variable set via
`-ldflags -X`; never hardcode a version there.

### End-to-end scripts

`scripts/e2e/lib.sh` builds `bin/flok` and a fake agent binary named `claude`
(`scripts/e2e/fakeagent`), then starts isolated tmux servers `e2e-inner` / `e2e-outer` with a
private `FLOK_STATE` and `FLOK_CONFIG` in a temp dir. The real tmux server is never touched.
Each `mN.sh` sources lib.sh and asserts on `capture-pane` output of the sidebar with
`expect`/`wait_for`; `hook <agent> '<json>'` replays a hook payload against the fake agent's
pane. Suites: m1 title-driven states, m2 hook-driven states, m3 nav/toggle/keys, m4 screen
rules for hook-less agents, m5 launcher lifecycle (detach, killed session, reattach), m6
snapshot.json / goto / flok-bar plumbing, m7 the terminal bell (an outer `alert-bell` hook
observes the BEL), m8 `flok resurrect save`, m9 keep-awake (`pmset -g assertions` against the
sidebar pid on macOS, the macOS-only message elsewhere), m10 remote hosts: `fake_ssh_setup` /
`fake_host` in lib.sh put a fake `ssh` on PATH that runs the remote command locally against
isolated servers `e2e-<host>` with per-host state and config (`$T/down-<host>` / `$T/auth-<host>`
simulate failures) and `rhook` replays a hook on a host, m11 flok on the phone: it builds and runs
the real `flok-relay` on 127.0.0.1 with a stand-in APNs (`scripts/e2e/fakehttp`, the Go HTTP
stand-in m2 also uses for ntfy: it listens before it reports its port, because on macOS a connect to
a bound-but-unlistened port is dropped, not refused) and a generated P-256 key (openssl), links the sidebar to it through `E2E_EXTRA_CONFIG`, drives the relay's API with curl
and `flok-relay tail`, answers a local and a remote (beta, full mode) agent, and restarts the
relay. They need a real `tmux` on PATH and `python3` (to read `runtime.json`).
`lib.sh` exports `TMUX_VER`/`tmux_at_least MAJ MIN` for checks older servers cannot pass.
Timing rules, learned from a month of CI: assert a transition with `expect_soon NAME PATTERN
CMD…` (polls until it holds; `expect_soon … capture` for the sidebar), never `sleep; expect`;
a wait placed after an action must only be satisfiable after the transition (`wait_gone` the
old row first, `wait_json` on `last_connected`/`served`/`snapshot.json`), because
`wait_for` matches a stale render instantly; a fixed dwell is only right for a negative check
("nothing changed"); `paint` writes screen fixtures atomically; `kill_server`/`kill_serve`/
`serve_pid` instead of raw `kill-server`/`pkill -f`/`pgrep -f` (system-wide); the e2e config
samples screens every 500 ms. `set -Eeuo pipefail` with an ERR trap: a command failing outside
a check prints `ABORT file:line: cmd` and the summary says `aborted`; `scripts/e2e/run-all.sh`
(what CI runs) runs every suite even after a failure, kills one after `E2E_SUITE_TIMEOUT`, and
writes the per-suite table to the step summary; the debug artifact keeps only failed suites.

## Architecture

### Process roles

One binary, several roles selected by subcommand (`internal/cli/root.go`):

- `flok up` (`internal/launcher`): renders `outer.tmux.conf.tmpl` into the state dir, creates
  the outer session whose right pane runs `flok _attach-loop` and whose left pane runs
  `flok sidebar`, writes `runtime.json`, then execs `tmux attach`. `_attach-loop` decides what a
  client exit means (destroyed session → re-attach elsewhere; deliberate detach → kill outer).
- `flok sidebar` (`internal/cli/sidebar.go` → `internal/ui`): the long-lived Bubble Tea
  program. It knows it is inside the outer via `FLOK_OUTER`, its own pane via `TMUX_PANE`, and
  the work pane via `FLOK_RIGHT_PANE` or `runtime.json`.
- `flok hook <agent>` (`internal/cli/hook.go`): the receiver Claude Code / Copilot call on
  every event. It must stay fast, silent and always exit 0 (Copilot denies tools when a hook
  fails). It identifies the pane from `TMUX_PANE`, maps the raw JSON through the agent adapter,
  applies the state machine under a per-pane flock, plays sounds, and appends to `events.log`.
- `flok serve --stdio` (`internal/cli/serve.go` → `remote.Serve`): the headless role a local flok
  starts over ssh on a remote host (`hosts.json`, mode `full`). It runs the same poller as the
  sidebar and streams JSON-lines frames (`internal/remote/proto`: hello/snap/event/pong/error
  out, goto/seen/visible/ping in); it holds `serve.lock` (flock) so `flok hook` on that host
  stays silent while served, and ends on stdin EOF. `remote.Manager` is the local end: one
  goroutine per enabled host, `full` over `serve`, `plain` driving `tmux.Remote` (ssh + quoted
  tmux command) with a per-host store under `$FLOK_STATE/hosts/<name>/`; states and backoff in
  `internal/remote/ssh.go`. ssh argv is always exec'd, never a shell; remote strings come from
  `hosts.Validate`-checked fields through `tmux.ShellQuote`.
- `flok _attach-loop --host <name>` (`launcher.AttachLoopHost`) runs in a host's parked outer
  window `flok-host-<name>` and keeps `ssh -t … tmux attach` alive; `internal/ui/hosts.go` is the
  sidebar's side: `hosts.json` changes (store watcher) → `applyHosts` → `remote.Manager.Apply`
  plus `launcher.EnsureHostPane`/`KillHostPane`; manager messages → `onRemote`; `refederate`
  joins the local merge and the hosts' views (`m.fed` published, `m.snap` rendered with the
  front host's sessions); `swapCmd` swaps a host's parked pane with the one next to the sidebar
  (`swap-pane`, zoom preserved) and records `right_pane`/`front_host` in runtime.json; `gotoCmd`
  swaps first when the target host is not in front. Swaps move panes between the windows named
  after the hosts, so a host's pane soon sits in another host's window: work panes are always
  identified by their start command (`launcher.ScanWorkPanes`, `pane_start_command`, which tmux
  prints in double quotes), never by the window they sit in; a second loop for the same host is a
  stray and is killed; every 5th poll the sidebar re-reads which pane is next to it and resyncs
  `front`/runtime.json (`resyncFront`). flok's keys inside a remote session: `remote.InstallKeys`
  binds `prefix b B g o a u`, the menus `A S @` and the server keys in the host's running tmux (serve does it in full mode with
  `run-shell "<flok> relay <cmd>"`, the plain-mode conn with `set-option -g @flok-request <cmd>`)
  and restores the host's own bindings on disconnect, one tmux invocation each way; the originals
  are recorded in that server as `@flok-orig-<key>` user options in the same invocation that
  binds, and an install reads them first, so a session that died between unbind and rebind (a
  killed serve, a mode flip racing it) never costs the host a binding. `Keys.SetPrefix` mirrors
  the local prefix (`remote.Deps.LocalPrefix`, `[hosts] prefix`): `prefix` ← local, `prefix2` ←
  the host's own when it was None, `<local> send-prefix` / `<own> send-prefix -2`, recorded as
  `@flok-orig-prefix`/`-prefix2` with the held key list in `@flok-orig-keys`; full mode learns it from
  a `prefix` frame after the hello, gated on `hello.Features` (`proto.ServeFeatures`), which
  doctor, `host status`, the info screen and the footer also read to flag a remote flok that
  predates the relay; `flok relay` files a request the serve
  forwards as a `request` frame, `#{@flok-request}` rides in the clients snapshot format and
  `poller.Deps.OnRequest` clears it; the sidebar runs `flok <cmd>` against its own outer
  (`runKeyCommand`). Servers-panel keys: `hostKey` in `ui/hosts.go` (c d r m x i I, Space peeks).
  `flok host install <name>` (`remote.Install`, `internal/remote/install.go`; the release side in
  `internal/release`: target from `uname -sm`, version rules, `sha256sums.txt`, tar.gz in memory)
  puts flok on a host over the same ssh: a probe, then the binary streamed to a fixed sh script's
  stdin (`Proc.CloseStdin` ends it) that verifies `version` on a temp file before `mv` into
  `~/.local/bin` (on `remote_path`, probed by the manager, so no host config); a same-OS/CPU host
  gets this executable, another the release asset (latest resolved through the
  `releases/latest/download/sha256sums.txt` redirect, no API); the path is recorded in
  `hosts.json` (a change makes the manager reconnect) or a `reconnect` request is filed; `I` in
  the servers panel runs `flok host install <name> --open` detached (a popup via
  `keys.PopupArgsTitled`, a window before 3.2); the footer names the install for a `no flok`/
  `old flok` host. Only ever write `~/.local/bin/flok` there.
  `flok last session|window|pane|server` (`internal/cli/last.go` → a `last` request with `Kind`;
  `ui/history.go`: `noteFocus` at the end of `refederate` records the front's focus, newest
  first, `lastTarget` walks it scoped like tmux: pane within the window, window within the
  session, session, agent pane and server anywhere, skipping gone sessions; `lastCmd` → `gotoCmd`/`frontCmd`,
  with tmux's own last-window/last-pane as the local fallback; no sidebar → tmux's own last-*). `[keys] map` (`config.Keys.Map`,
  `remote.KeyMap` validates against `IsKeyCommand`) adds user keys to both key sets: locally
  `InstallLocalKeys(…, extra)` binds them whatever they held (saved like `all`), on hosts
  `remote.Deps.ExtraKeys` rides in the `prefix` frame (`Frame.Keys`, `FeatureKeyMap`) for
  serve's `Keys.SetKeys` and goes straight into plain mode's `InstallKeys`. A `;` key is `\;`
  as a tmux argument (`keyArg`) and `semicolon` in the records (`keyToken`): an argument that
  ends in `;` ends the tmux command. Locally, `runSidebar` calls `remote.InstallLocalKeys` (mode
  `[keys] bind`: missing/all/off) so the snippet is optional; a key running flok's command for it is left alone, one running another
  legacy flok command (`flokSub`/`isLegacy`: an older snippet's `A` = prev, `S` = the servers
  menu) is rebound in both modes and only unbound at the end (never saved: a restored line would
  raise the footer notice at every start); what it bound is in `$FLOK_STATE/keys.json` and
  `RestoreLocalKeys` (sidebar exit, `flok down`, the attach-loop teardown) undoes exactly that. Server keys:
  `flok host next|prev|last` and `front <N>`, the menus `flok menu agents|sessions|servers` on
  `prefix A/S/@` (`internal/cli/menu.go`: display-menu over the work pane, items run `flok goto
  <paneref|sessionref> --no-focus` / `host front N`; `flok host menu` = servers; `flok goto` takes
  `$5`/`beta:$5` session refs, remote ones through a `goto` request with `Session`) (rotation order = local + enabled hosts;
  `runtime.json` `previous_front`; the menu is an outer `display-menu` over the work pane,
  3.0+); `notifyFront` flashes the new front's name on that server's status line (`notify`
  frame in full mode, `display-message` over ssh in plain mode). One-shot commands aimed at a remote host
  (`flok goto beta:%12`, `flok jump`/`next`/`prev` with hosts, `flok host front`) cannot open ssh
  themselves: they file a JSON request in `$FLOK_STATE/requests/` (`state.WriteRequest`), which
  the store watcher delivers and `ui/requests.go` runs (older than 10 s are dropped). `flok doctor`
  probes every registered host with one fixed command (`internal/cli/doctor_hosts.go`). Without a
  host in `hosts.json` every one of these is a no-op and the single-host code paths, frames and
  JSON stay byte-identical.
- `flok jump|next|prev|toggle|hide|focus|reload` (`internal/cli/nav.go`): one-shot commands
  bound in the user's tmux.conf. They build a throwaway merge snapshot (no registry poll, too
  slow) and read `runtime.json` to find the outer panes and the inner client tty.
- `flok keep-awake [on|off|toggle|status]` (`internal/cli/keepawake.go`, macOS only): writes the
  `keep-awake` marker in the state dir and waits for the sidebar to confirm through
  `snapshot.json`. The **sidebar** holds the power assertions (`internal/awake`: IOKit's
  `IOPMAssertionCreateWithName` through `ebitengine/purego`, so no cgo; `ui/keepawake.go` follows
  the marker via the store's fsnotify watch and publishes `KeepAwake`), which is why they end with
  the sidebar process even on a crash. `[keep_awake] presence` adds a nudger goroutine to the
  held `awake.Assertion`: every 30 s, after 60 s of HID idle time, it posts an empty
  `flagsChanged` event through CoreGraphics (idle detectors like Teams read that clock, power
  assertions do not); it needs Accessibility (`AXIsProcessTrusted`, granted to the terminal app),
  and its state (`active`/`blocked`) is published as `keep_awake_presence`, with a republish
  when it changes between polls (`awakeHold.presenceMoved`). `createOuter`, `Down` and the
  attach-loop teardown reset the marker; `flok status`, `doctor` and flok-bar read the state
  with `Snapshot.SidebarAlive`/`KeepAwakeHeld` so a dead sidebar's lingering snapshot never
  shows it on.

### State pipeline (the part that spans files)

```
tmux.TakeSnapshot (one tmux call: sessions;panes;clients)
state.Store.LoadAgents/LoadSeen (hook records + seen marks, files in $FLOK_STATE)
claudereg (claude agents --json, matched by pane tty)
rules.Set.Evaluate (capture-pane + title + OSC 9;4, per hook-less pane)
        │
        ▼
merge.Tracker.Build(merge.Inputs) ──► merge.Snapshot{Spaces, Agents, Focus, NewlySeen}
        │                                   │
        ▼                                   ▼
ui.Model renders it            ui persists NewlySeen via Store.MarkSeen
```

- `internal/agent` is the shared model: `Agent`, `Space`, `State` (working/blocked/paused/done/
  idle/unknown; `paused` = a Stop with `background_tasks` in flight, reason `waiting`, detail
  the task list, cleared by the next tool start, prompt or Stop, or by the merge's stale rule
  after `StaleWorking`; older hosts send it as working+waiting and `proto.ToMerge` maps it), `Event`/`EventKind` (agent-neutral hook events), and the `Adapter` interface
  (process names, title parsing) plus optional `HookMapper`. Adapters self-register in `init()`;
  `claude.go` and `copilot.go` are the two implementations.
- `internal/state/machine.go` is the pure hook state machine: `Apply(agent, event, focused, now)`
  returns `Effects{Sound, Delete}`. It never touches files; `store.go` does (atomic temp+rename,
  flock per pane). `Agent.Cwd`, and with it the row name, is the directory the session started
  in (taken at SessionStart, the first event, or a new session id), never a later hook's cwd
  (Claude's Bash tool keeps a `cd`); the merge pins a hook-less pane's first path the same way
  (`track.cwd`). Names the user chose with `n` live in `names.json` (`Store.LoadNames`/`SetName`,
  keyed by pane ref) and are laid over the federated agents in `ui.applyNames`. `done` is only produced when the pane is not focused; `focused` is a lazy
  callback because it costs a tmux call.
- `internal/merge/merge.go` is the authority merge. For panes with `HasHooks` the hook record
  wins, with narrow escape hatches keyed on title/screen history kept in the per-pane `track`
  (e.g. two idle registry samples or three idle-screen polls, six while the registry says busy, clear a stale `working`, two idle-screen polls clear a stale
  `blocked`). Panes without hooks fall back to title → registry → screen rules. `done` becomes
  `idle` the moment the user looks at the pane; the seen mark is persisted by the caller from
  `Snapshot.NewlySeen`. Change state semantics here and in `machine.go` together, and cover them
  in `merge_test.go` / `machine_test.go`, which construct `Inputs` directly without tmux.
  Agents come out of `Build` in `Inputs.AgentOrder`: `stable` (the default; `SortAgentsStable`:
  the sessions' order, then window and pane, so rows never move and `1`-`9` stay put) or
  `priority` (`SortAgents`: blocked, done, working, idle, newest first); `Rollup` picks a
  session's state by priority either way, and `Federate` keeps a stable order as local + hosts in
  registry order (it re-sorts only for `priority`). `Build` is not idempotent (per-pane counters
  advance per call), so multi-host views are joined above it by `merge.Federate`, which stamps `Host` on agents, spaces and focus (`""` = local, so
  single-host keys, files and JSON stay byte-identical) and re-sorts; `agent.PaneRef` (`%12`,
  `beta:%12`) is the cross-host pane key. Host names keep their case (an ssh alias such as
  `dockerAMS`) but are unique and looked up regardless of case (`hosts.Set.Get`, the per-host
  dirs share a case-insensitive filesystem on macOS); the merge, snapshot and request mailbox
  compare hosts exactly, so anything taking a typed name resolves it to the registered spelling
  first (`flok host …`, `flok goto`, `runRequest`).
- `internal/rules` is a port of herdr's manifest engine. Manifests under `manifests/*.toml` are
  embedded and **copied verbatim from herdr under Apache-2.0** (see `NOTICE`); edit only the
  attribution header, otherwise drop a newer herdr file in. Load order is bundled → herdr cache
  (opt-in) → `~/.config/flok/agents/`, later replacing earlier by id. `testdata/*.txt` are
  captured screens; `TestClaudeFixtures` pins which rule id wins for each. `flok explain` runs
  `Manifest.Explain` on live panes for debugging.
- `internal/poller` is the state pipeline without the rendering: the tmux poll, the registry and
  screen samplers, the fsnotify watch on the store, the merge with its persistence (seen marks,
  corrections, stale hook records) and the sound transitions. `ui.Model` holds one `*poller.Poller`
  and drives it from Bubble Tea commands (`Poll`/`Rebuild`/`PollScreen`/`PollRegistryIfDue`
  return the work, `ApplySnapshot`/`ApplyRegistry`/`ApplyScreen` take the results);
  `poller.Run` drives the same pipeline headless for `flok serve` and plain-mode remote hosts.
  The sequencing rules below live there now; `internal/tmux/tmuxtest.Fake` is the tmux stand-in
  both packages' tests use.
- `internal/push` posts `[notify]` events (ntfy headers or JSON, bearer token, bounded queue on
  its own goroutine, `ProxyFromEnvironment`); `ui/push.go` `notePush` runs at the end of
  `refederate` and diffs the federated agents' states against the last view (primed, not
  announced, at start; blocked / done / error only; 2 s gap per pane and kind), so local and
  remote agents are covered alike, and hands the same transitions to the link;
  `config.InstanceName` is `[link] name` or the short hostname.
- flok on the phone: `internal/link` is the sidebar's outbound WebSocket to the relay
  (`[link] url`/`token`, `github.com/coder/websocket`, `ProxyFromEnvironment`, backoff, a
  `hello` then `snap`/`event`/`screen`/`ack` frames out and `answer`/`seen`/`subscribe` in;
  `internal/link/wire` is the frame set shared with the relay and the app; the snapshot is
  deduped and always precedes the events announced with it, so the relay's badge is right).
  `ui/link.go` feeds it from `publish` and `notePush`, acts on commands (`agentByRef` against
  `m.fed`, local answers through `remote.TypeAnswer` on the inner server, remote ones through
  `Manager.Answer`: an `answer` frame and its `ack` in full mode, `send-keys` over ssh in plain
  mode; screens: local and plain-mode panes captured every second from the sidebar, full-mode
  ones by serve on a `capture` frame → `screen` frames, resent after a host reconnects), logs
  every command to `events.log` and puts the link's state in the footer (after 30 s down) and
  `snapshot.json` (`link`). `internal/answer` is the allowlist (named keys, ≤ 200 printable
  chars, `Args` = one `send-keys` invocation, a trailing `;` escaped) every path goes through.
  `internal/relay` is `flok-relay` (`cmd/flok-relay`: serve, tail, token, health): `/link` for
  instances (bearer instance token; the newest connection under a name replaces the older),
  `/api/*` for the app (bearer device token; `/api/ws` live stream, `/api/answer` waits for the
  instance's ack), subscriptions refcounted per instance and pane, the last snapshot kept while
  offline, pushes through `internal/relay/apns` (HTTP/2, ES256 provider token cached 50 min,
  categories `FLOK_PERMISSION`/`FLOK_QUESTION`/`FLOK_DONE`, badge = unseen over all instances,
  410 forgets the device) to the devices in `devices.json`. `deploy/relay` has the Dockerfile
  (scratch + CA bundle), the Portainer compose with Traefik labels and the example configs; the
  release workflow publishes `ghcr.io/w4jnl/flok-relay` for amd64 and arm64 with plain docker
  (no third-party actions). The iOS app is not written yet; `flok-relay tail` stands in.
- `internal/ui/model.go` drives three independent poll cadences from config (`poll_ms` for the
  tmux snapshot, `registry_poll_ms`, `screen_poll_ms`) plus an fsnotify watch on the store so
  hook writes re-render immediately. CPU rules that are easy to undo by accident: an unchanged
  poll (fingerprint of the raw tmux output + hook records) skips the merge and keeps the cached
  frame (the fast path calls `Publisher.Heartbeat` so flok-bar still sees a live snapshot); only
  the 1 s tick spawns tmux — screen results, registry samples and hook/marker changes go through
  `rebuild()`, which re-merges the cached tmux snapshot (`screenSeq` still advances once per
  sample with results, identical or not, because the merge counts samples); all screen captures
  go in one tmux invocation (`captureAll`), and a sample is judged again at `ApplyScreen` when
  the pane's title or progress moved while the capture was out (`ScreenSample`; a late sample
  judged against the old title flipped a hook-less row working → idle → done on slow runners); the registry is only queried while a Claude
  turn/prompt is open or a pane lacks hooks (else once a minute); sidebar focus comes from
  `tea.FocusMsg`/`BlurMsg` with an outer `pane_active window_zoomed_flag` check every 5th poll as
  fallback; **idle mode** (`sidebar-hidden` = 1, written by `flok hide`/`toggle` and verified against
  `window_zoomed_flag`) pauses the spinner and stretches polls and captures to `idle_poll_ms`,
  with an fsnotify watch on that marker for prompt resume — an unfocused terminal window is
  deliberately not idle, it is usually still visible; the
  renderer runs at `fps` (15). `FLOK_CPUPROFILE=<file>` in the sidebar's environment writes a
  30 s CPU profile after start (`tmux -L flok set-environment -g …` then `flok reload`). Sounds are played by the hook by default
  (`sounds.player = "hook"`); the sidebar only plays them for title/screen-derived transitions or
  when `player = "sidebar"`.

### Menu bar companion (flok-bar)

`cmd/flok-bar` (build tag darwin) is the only cgo package: `fyne.io/systray`. Keep everything
under `internal/` cgo-free. The bar reads `snapshot.json`, which `internal/ui` publishes through
`internal/snapshot.Publisher` after every merge (changed content or a 5 s heartbeat), renders it
with the pure functions in `internal/bar`, and forwards clicks to `flok goto <pane>`
(`internal/cli/goto.go` → `nav.Go`, `Store.MarkSeen`, `internal/focus.Terminal`; the pane is a
`PaneRef` string, `beta:%12` for a remote agent, which goes through the sidebar's request mailbox)
and clicks on the pre-created host rows to `flok host front <name> --focus`. The bar's "Edit config…" runs `flok edit-config` (new tmux window with `[bar] editor`, else
`open`), "Reload sidebar" runs `flok reload` and the "Keep awake" checkbox runs
`flok keep-awake toggle` (checked and ⚡ in the title from the snapshot's `KeepAwake`). `flok up`
spawns it when `[bar] enabled` (`launcher.StartBar`, pid in `flok-bar.pid`), `flok down` and the
attach-loop teardown stop it, and it quits by itself 30 s after `runtime.json`/the snapshot
vanish (`FLOK_BAR_GONE_AFTER` shortens that for tests). systray gotchas: menus cannot grow (slots
are pre-created and hidden), an icon can be swapped but never removed, `systray.Run` owns the
main thread. Icons come from `assets/icons/gen` (`make icons`). e2e coverage: `scripts/e2e/m6.sh`
(set `FLOK_E2E_BAR=1` to also exercise the process; it shows a menu bar item briefly).

### Conventions worth knowing

- tmux is always driven by exec (`internal/tmux.Client`); there is no library binding. Prefer
  IDs (`$5`, `@12`, `%17`) over names in targets, and batch commands with `;` in one invocation
  as `nav.Go` and `TakeSnapshot` do.
- Paths and env: `FLOK_CONFIG`, `FLOK_STATE` override the XDG locations; `FLOK_OUTER`,
  `FLOK_RIGHT_PANE`, `TMUX_PANE` are set by the launcher for the sidebar pane. Tests and e2e
  scripts rely on the first two for isolation.
- `internal/install` edits Claude Code `settings.json` and `~/.copilot/hooks` idempotently and
  keys on the ` hook claude` marker in the command string to find its own entries. It also holds
  the starter `tmux.conf.tmpl` (go:embed, text/template: `{{.Conf}}`, `{{.Plugins}}` as `~`
  paths, `{{.Resurrect}}` = `TmuxResurrectSnippet`) that `flok install` offers when
  `FindTmuxConf` finds nothing (`--tmux-conf` writes it outright, never over an existing file;
  `TmuxConfPath` picks `~/.tmux.conf` below tmux 3.1). Keep it loadable by tmux 2.7: no
  `terminal-features` or `choose-tree -Z` without a version guard; m8 loads it on an isolated
  server with a stub tpm. Adding a new
  Claude hook event means updating `ClaudeEvents` there and the `MapHook` switch in
  `agent/claude.go`.
- Claude Code's title convention (spinner glyph ranges while working, `✳` idle) is duplicated in
  `agent/claude.go` and in the `osc_title_working` rule of `manifests/claude.toml`; keep them in
  step when Claude changes its spinner.
- Platforms: macOS first, Linux as a pure terminal interface (no flok-bar). Sounds go through
  `notify.Player`, which picks the first of afplay, mpv, ffplay, pw-play, paplay, play on PATH or
  runs `[sounds] command`; the bundled sounds are WAV (paplay/pw-play cannot decode mp3 on
  RHEL 9). With no player, `[sounds] bell = "auto"` makes `notify.Bell` write BEL into the outer's
  work pane (the inner client tty from runtime.json), which the outer forwards to the terminal,
  also over ssh; `notify.Compose` picks the sounder per mode. `focus.Strategy` "auto" is "none"
  off macOS; `[bar] enabled` is ignored off macOS with a message. A libghostty-based redesign was
  studied (Sep 2026) and rejected: only the VT parser is public, all bindings are cgo; the outer
  tmux is the one host that runs everywhere.
- Light/dark: `flok up` asks the terminal for its background (OSC 11 with a DA1 sentinel,
  `internal/termtheme`) before tmux owns it and records `terminal-theme` in the state dir; the
  sidebar reads it each poll (fsnotify for instant switches), `config.Theme` resolves `mode`
  against it, and `borderCmd` recolours the outer border. Do not rely on tmux's `client_theme`:
  it can report the OS appearance instead of the actual background (Dracula on a Light Mac).
- tmux versions: flok runs on 2.7 (RHEL 8) and newer, everything from 3.3. The gates are
  `tmux.Features` (`internal/tmux/version.go`, derived from `tmux -V`, recorded by `flok up` in
  `runtime.json` as `tmux_version` so other commands do not fork). The outer config template is
  rendered per version (`launcher.RenderOuterConf`, pinned by `render_test.go`); never add a
  tmux option, hook, flag or format modifier without a gate there — an unknown option does not
  stop tmux, it dumps the errors into a view-mode overlay on the work pane (lib.sh asserts
  `pane_in_mode` is 0 after `up`). `-e VAR=val` flags are banned (3.0–3.3 only): pane and popup
  commands go through `launcher.SidebarCommand` / an `env …` prefix. `keys.PopupArgs` picks the
  popup shape (none < 3.2, bare 3.2, bordered 3.3+); the tmux.conf snippet binds `?` to
  `flok keys --open`, which falls back to a window. From 3.4 tmux escapes non-printable bytes
  in `list-*` output as vis(3) octal (`\037` for the separator): `tmux.Decode` undoes it when
  the client's `Features.EscapedOutput` says so, so always take snapshots through
  `tmux.TakeSnapshotRaw`/`Decode`, never split raw output. CI runs the e2e suites on macOS
  (3.7), Ubuntu (3.4) and Rocky 9 (3.2a); the 2.7 job (Rocky 8) was dropped on 2026-10-04, the
  2.7 gates stay; keep scripts POSIX/GNU-safe (no BSD `sed -i ''`, use `sed -i.bak … && rm`).
