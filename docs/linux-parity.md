# Linux parity — branch handoff

Branch: `claude/flok-architecture-redesign-4sd212`. Scope agreed with the author: **keep the
outer-tmux architecture, make the pure terminal interface first-class on Linux, no menu bar
companion there.** This file is the state of that work so a Claude Code session (or a human)
can continue from it. Delete it when the branch is merged.

## Decision record (why no redesign)

A feasibility study on replacing the outer tmux with libghostty concluded: not now. As of
September 2026 the only public libghostty piece is `libghostty-vt` (parser + screen state, no
PTY/renderer/window, C API marked unstable); the full engine (`ghostty.h`) is documented as
internal to the macOS Swift app; all Go bindings are cgo and pre-release. Every libghostty path
makes Linux harder, the outer tmux is the one host that already runs on both platforms. The
in-repo consequence is this branch: fix what was actually broken on Linux and put Linux in CI.

## What was broken on Linux, and the fix

Verified on this box: tmux 3.4, Go 1.27, GNU sed, Debian userland. Before the branch, unit tests
passed but the e2e suites were 52 failures out of 132 checks; the sidebar listed **no sessions and
no agents** at all.

| # | Symptom | Root cause | Fix |
|---|---|---|---|
| 1 | Sidebar and `flok status` empty; "no client attached to inner tmux" | tmux 3.4 escapes non-printable bytes in `list-*` output with vis(3) octal, so the `\x1f` field separator arrives as the four characters `\037`. `ParseSnapshot` split nothing. macOS builds (3.5+) emit the raw byte, which is why CI never saw it. | `tmux.Unescape` + `tmux.Sep`; `ParseSnapshot` normalises before splitting (`internal/tmux/snapshot.go`); doctor's client check uses the same helpers. Test: `TestParseSnapshotEscapedSeparator`. |
| 2 | m3 "filter drops unrelated rows": `flok keys --print --filter split` still showed a Kill row | tmux 3.4 ships no notes for its `<` / `>` bindings, so the help showed their raw `display-menu …` commands, which mention Split and Kill. 3.5+ has notes ("Display window menu"). | `keys.Label` translates `display-menu` to "window menu" / "pane menu" / "session menu" / "menu" (`internal/keys/describe.go`). Test: `TestLabelDisplayMenu`. |
| 3 | m5/m6 `sed: can't read …` | BSD `sed -i ''` | `sed -i.bak … && rm -f …bak` (works on both). |
| 4 | m1 "both sessions listed" on a long branch name | Test expected the full branch name; the sidebar truncates with `…` at 28 columns. Only visible off `main`. | Pattern matches the first 6 characters of the branch. |
| 5 | Sounds hardcoded to `/usr/bin/afplay` | macOS-only | `notify.Player` replaces `notify.Afplay`: first of `afplay, pw-play, paplay, mpv, ffplay, play` on PATH, per-player volume args, or `[sounds] command` with `{file}`/`{volume}` placeholders (shell-quoted). New config key `Sounds.Command`. Tests in `internal/notify/notify_test.go`. Doctor reports the chosen player or warns. |
| 6 | `[bar] focus = "auto"` resolved to AppleScript everywhere | aerospace/osascript are macOS tools | `focus.Strategy` auto → `none` when `GOOS != darwin` (custom commands still run). `Options.GOOS` is injectable for tests. |
| 7 | `[bar] enabled = true` on Linux printed "flok-bar not found" | flok-bar is darwin-only by design | `flok up` and `flok doctor` say the companion is macOS only and continue. |
| 8 | CI e2e ran on macOS only | — | `e2e` job is now a `macos-latest` + `ubuntu-latest` matrix (`apt-get install tmux` on Linux, `tmux -V` printed). |

Result on Linux after the branch: `go test ./...` green, all six e2e suites green (m1 25, m2 31,
m3 38, m4 10, m5 17, m6 11 checks).

## Files touched

```
.github/workflows/ci.yml        e2e matrix
internal/tmux/snapshot.go       Sep, Unescape, ParseSnapshot normalisation (+ test)
internal/keys/describe.go       display-menu labels (+ test)
internal/notify/notify.go       Player, Detect, playerArgs (+ notify_test.go, bundled.go comment)
internal/config/config.go       Sounds.Command
internal/config/template.go     commented `command` line in [sounds]
internal/cli/hook.go            notify.Player
internal/cli/doctor.go          player report, escaped-separator client check, bar-on-linux note
internal/ui/model.go            notify.Player
internal/focus/focus.go         GOOS-aware auto strategy (+ test)
internal/launcher/launcher.go   bar skipped with a message off macOS
scripts/e2e/m1.sh m5.sh m6.sh   portable sed, branch pattern
README.md, CLAUDE.md            platform notes, config key, tmux escaping rule
docs/linux-parity.md            this file
```

## Not done / worth checking next

- **macOS CI must still pass.** Nothing here is Linux-only code, but the two behavioural
  changes visible on macOS are: `display-menu` bindings without a note now read "window menu"
  instead of the raw command (3.5+ has notes, so usually unaffected), and the doctor prints the
  player name. Run `scripts/e2e/m*.sh` on a Mac once before opening the PR.
- **Real audio on Linux is untested** (this container has no audio stack). `pw-play`/`paplay`
  decode mp3 through libsndfile ≥ 1.1 (2022+ distros). If a user's stack cannot, the documented
  escape hatch is `[sounds] command`, or pointing `done`/`blocked` at `.wav`/`.ogg` files. A
  `flok doctor --play` style smoke test would be a cheap addition.
- **tmux 3.3 (README's minimum)**: not exercised. 3.4 is the oldest verified here.
- **`flok goto` on Linux** now skips the focus step under `focus = "auto"`. Anyone wanting
  click-to-return from a script can set `[bar] focus` to a command such as
  `xdotool search --name TMUX windowactivate` or `hyprctl dispatch focuswindow title:TMUX`; no
  built-in strategy was added because nothing on Linux calls `goto` without the bar.
- **Snapshot publishing** (`snapshot.json`) is unchanged on Linux and can feed a waybar/polybar
  module through `flok status --json` if anyone wants a bar-like readout without flok-bar.

## How to continue

```sh
git fetch origin claude/flok-architecture-redesign-4sd212
git checkout claude/flok-architecture-redesign-4sd212
make test && go vet ./...
for s in scripts/e2e/m*.sh; do "$s"; done      # needs tmux + python3, uses isolated servers only
```

No PR has been opened yet; the author asked for the branch only.
