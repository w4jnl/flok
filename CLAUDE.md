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
scripts/e2e/m1.sh          # headless end-to-end suites, m1..m5 (see below)
scripts/spike/m0-outer.sh check   # nested-outer passthrough checks on isolated servers
```

`go.mod` says Go 1.27. There is no linter config beyond `go vet`.

Releases: `scripts/release.sh <version>` tags `v<version>`, pushes, then bumps `url`/`sha256` in
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
rules for hook-less agents, m5 launcher lifecycle (detach, killed session, reattach). They need
a real `tmux` on PATH and `python3` (to read `runtime.json`).

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
- `flok jump|next|prev|toggle|hide|focus|reload` (`internal/cli/nav.go`): one-shot commands
  bound in the user's tmux.conf. They build a throwaway merge snapshot (no registry poll, too
  slow) and read `runtime.json` to find the outer panes and the inner client tty.

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

- `internal/agent` is the shared model: `Agent`, `Space`, `State` (working/blocked/done/idle/
  unknown), `Event`/`EventKind` (agent-neutral hook events), and the `Adapter` interface
  (process names, title parsing) plus optional `HookMapper`. Adapters self-register in `init()`;
  `claude.go` and `copilot.go` are the two implementations.
- `internal/state/machine.go` is the pure hook state machine: `Apply(agent, event, focused, now)`
  returns `Effects{Sound, Delete}`. It never touches files; `store.go` does (atomic temp+rename,
  flock per pane). `done` is only produced when the pane is not focused; `focused` is a lazy
  callback because it costs a tmux call.
- `internal/merge/merge.go` is the authority merge. For panes with `HasHooks` the hook record
  wins, with narrow escape hatches keyed on title/screen history kept in the per-pane `track`
  (e.g. three idle-title polls clear a stale `working`, two idle-screen polls clear a stale
  `blocked`). Panes without hooks fall back to title → registry → screen rules. `done` becomes
  `idle` the moment the user looks at the pane; the seen mark is persisted by the caller from
  `Snapshot.NewlySeen`. Change state semantics here and in `machine.go` together, and cover them
  in `merge_test.go` / `machine_test.go`, which construct `Inputs` directly without tmux.
- `internal/rules` is a port of herdr's manifest engine. Manifests under `manifests/*.toml` are
  embedded and **copied verbatim from herdr under Apache-2.0** (see `NOTICE`); edit only the
  attribution header, otherwise drop a newer herdr file in. Load order is bundled → herdr cache
  (opt-in) → `~/.config/flok/agents/`, later replacing earlier by id. `testdata/*.txt` are
  captured screens; `TestClaudeFixtures` pins which rule id wins for each. `flok explain` runs
  `Manifest.Explain` on live panes for debugging.
- `internal/ui/model.go` drives three independent poll cadences from config (`poll_ms` for the
  tmux snapshot, `registry_poll_ms`, `screen_poll_ms`) plus an fsnotify watch on the store so
  hook writes re-render immediately. CPU rules that are easy to undo by accident: an unchanged
  poll (fingerprint of the raw tmux output + hook records) skips the merge and keeps the cached
  frame; all screen captures go in one tmux invocation (`captureAll`); the registry is only
  queried while a Claude turn/prompt is open or a pane lacks hooks (else once a minute); sidebar
  focus comes from `tea.FocusMsg`/`BlurMsg` with an outer `pane_active` check every 5th poll as
  fallback; the renderer runs at `fps` (15). `FLOK_CPUPROFILE=<file>` in the sidebar's
  environment writes a 30 s CPU profile after start (`tmux -L flok set-environment -g …` then
  `flok reload`). Sounds are played by the hook by default
  (`sounds.player = "hook"`); the sidebar only plays them for title/screen-derived transitions or
  when `player = "sidebar"`.

### Menu bar companion (flok-bar)

`cmd/flok-bar` (build tag darwin) is the only cgo package: `fyne.io/systray`. Keep everything
under `internal/` cgo-free. The bar reads `snapshot.json`, which `internal/ui` publishes through
`internal/snapshot.Publisher` after every merge (changed content or a 5 s heartbeat), renders it
with the pure functions in `internal/bar`, and forwards clicks to `flok goto <pane>`
(`internal/cli/goto.go` → `nav.Go`, `Store.MarkSeen`, `internal/focus.Terminal`). The bar's "Edit config…" runs `flok edit-config` (new tmux window with `[bar] editor`, else
`open`) and "Reload sidebar" runs `flok reload`. `flok up`
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
  keys on the ` hook claude` marker in the command string to find its own entries. Adding a new
  Claude hook event means updating `ClaudeEvents` there and the `MapHook` switch in
  `agent/claude.go`.
- Claude Code's title convention (spinner glyph ranges while working, `✳` idle) is duplicated in
  `agent/claude.go` and in the `osc_title_working` rule of `manifests/claude.toml`; keep them in
  step when Claude changes its spinner.
- macOS first: sounds use `afplay` (`internal/notify`). Everything else is plain tmux.
