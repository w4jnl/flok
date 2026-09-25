# Changelog

User-facing changes per release, newest first. `scripts/release.sh` refuses to tag a version
that has no section here and uses the section as the GitHub release notes, so every release
updates this file first. Dates are the tag dates.

## Unreleased

## 0.4.5 (2026-09-25)

- `flok keep-awake` (macOS): keeps the Mac awake with the display on while flok runs, for agents
  working unattended. Off by default; `flok keep-awake` toggles it, `on`/`off`/`status` also work,
  and the menu bar gets a "Keep awake" checkbox and shows ⚡ next to the state glyph while it is
  on. flok holds the power assertions itself (no `caffeinate`), inside the sidebar, so they end
  with the flok session and a new session starts with keep-awake off. `flok status` shows a
  `keep-awake: on` line while it is on, and `flok doctor` reports it. Closing the lid on battery
  still sleeps the Mac.
- `[keep_awake] presence = true`: while keep-awake is on, Teams, Slack and other apps that
  watch for input keep showing you as active. After 60 s without input flok posts an empty
  modifier-key event (no key press, no cursor movement), which resets the idle clock those apps
  read; the power assertions alone keep the display on but not your presence. It needs
  Accessibility permission for your terminal app; `flok doctor`, `flok keep-awake status`,
  `flok status` and the menu bar tooltip say when macOS blocks the events.
- `[sounds] when_focused = true` now works: an agent pane you are looking at sounds when it
  finishes or waits for you, like one in the background. The option was documented but ignored.
- README: a new top section (pitch, recording, menu bar, five-step setup, why flok) ahead
  of the architecture; the old Install section is folded into it. The banner is re-rendered
  from the brand lockup (`assets/brand/render-hero.sh`) with the plain tagline. The GIF comes from
  `assets/demo/record.sh`, a vhs tape played against the e2e fake agent on isolated tmux
  servers, so it can be re-recorded; `assets/demo/menubar.sh` captures the menu bar item and
  its open menu from the same scene (`FLOK_BAR_SHOT` makes the bar open its menu and report the
  frames).
- `SECURITY.md` (private vulnerability reporting), `CONTRIBUTING.md`, a bug report form and a
  Dependabot configuration for Go modules and GitHub Actions.

## 0.4.4 (2026-09-22)

- Menu bar: the version row opens `CHANGELOG.md` on GitHub (all releases newest first, so it
  also serves HEAD builds) instead of the running version's release page, and a new
  "GitHub repository" row below it opens the source. Both rows carry an icon, the flok mark
  and GitHub's, drawn into the row title because macOS 27 does not show menu item images.

## 0.4.3 (2026-09-18)

- A working Claude no longer drops to idle for a moment in the middle of a turn. Claude Code's
  screen spinner also draws `✳`, a frame the bundled rules did not know, so three unlucky
  captures in a row read as a bare prompt box and ended the state until the next hook event.
  The bundled Claude manifest is herdr's 2026.09.11.1, which knows the glyph, and a screen-idle
  override now needs twice the samples while Claude's own registry still reports the session
  busy.

## 0.4.2 (2026-09-17)

- `flok doctor`'s double-configuration warning now checks that `~/.tmux.conf` really sources
  `~/.config/tmux/tmux.conf` unconditionally; a shim guarded by `if-shell` or `%if` (the README
  shows one) no longer trips it. tmux's `config_files` lists both files either way.

## 0.4.1 (2026-09-17)

- `flok up` no longer pre-empts a tmux-continuum restore. It used to start the inner server
  with a session named `main`; when the save file also had a `main` session, tmux-resurrect
  found its first pane already taken and restored neither that pane's directory nor its agent.
  The bootstrap session is now called `~flok` (tmux resolves an unknown session target by
  prefix, so the name must not start like any saved session) and is dropped once the restore
  is over, or renamed to `[inner] session` (default `main`) when nothing was restored.
- `flok resurrect save` takes Claude Code session IDs from `claude agents --json` first and from
  hook records second, so a pane whose hooks never fired (installed after the session started,
  or calling a binary that no longer exists) is still restored exactly.
- `flok doctor` fails when the installed Claude Code or Copilot hooks call a flok binary that
  does not exist; such hooks fail silently on every event. It also warns when tmux loaded both
  `~/.tmux.conf` and `~/.config/tmux/tmux.conf`: plugins initialise twice, and tmux-continuum
  then runs two tmux-resurrect restores that type every restored command twice.
- Menu bar: the working spinner turns at the sidebar's pace (`[sidebar] spinner_ms`, 4 fps)
  unless `[bar] animate_ms` is set; it used to be fixed at 2 fps.

## 0.4.0 (2026-09-17)

- Optional tmux-resurrect integration restores hook-backed Claude Code and Copilot CLI panes to
  their exact conversations (`flok install --tmux-resurrect`); panes without an exact session ID
  restore as shells rather than guessing.
- Copilot CLI sounds now play only when a permission prompt is actually shown, not for
  auto-approved tool calls.
- Stale hook records are removed when an agent pane is reused, preventing duplicate sidebar rows
  and navigation to an unrelated or empty pane.

## 0.3.6 (2026-09-15)

- Light terminals get a light palette (Dracula's Alucard): `flok up` asks the terminal for its
  background and the sidebar, keybinds popup and pane border follow (`[theme] mode = auto | dark
  | light`, colours under `[theme.light]`). `flok theme` shows what was detected;
  `flok theme light|dark` switches a running sidebar at once.
- Sidebar: the `[flok]` wordmark on top of the wide layout, and `⌈⩓⌉` over a separator on the
  rail (`[sidebar] brand`, colour `[theme] brand`); a short pane drops it before losing a row.

## 0.3.5 (2026-09-14)

- Menu bar: a "flok <version>" row at the bottom of the menu opens these release notes on
  GitHub.
- Release notes live in CHANGELOG.md and are published as the GitHub release notes; all earlier
  releases were given theirs.

## 0.3.4 (2026-09-14)

- Menu bar icon is tinted by state: teal while an agent works, orange while one waits on you,
  the plain menu bar colour at rest, with a darker teal on a light menu bar.
- The rotating glyph stays in the menu bar colour; colour is reserved for the icon and the badge.

## 0.3.3 (2026-09-14)

- Waiting icon is a solid inversion of the mark (filled tile, chevron and cursor knocked out)
  and blinks about once a second while something waits and the terminal is unfocused
  (`[bar] blink`).
- Outline icon widened to the tile's footprint; the cursor reads as a bar in both states.

## 0.3.2 (2026-09-14)

- Menu bar: coloured spinner and badge (`[bar] color`); the icon is drawn at 18 pt
  (`[bar] icon_size`).
- Fixed: `flok up` could not restart a menu bar that had died while a session was attached.

## 0.3.1 (2026-09-14)

- New brand: the S3 mark (a terminal bracket holding a chevron and a cursor) as the menu bar
  icon, brand assets under `assets/brand`, README hero.
- `flok doctor` and `flok version` on a terminal print the ASCII lockup.

## 0.3.0 (2026-09-14)

- Linux is a first-class platform for the terminal sidebar: tmux 3.4's escaped list output is
  parsed, sound players are detected (mpv, ffplay, pw-play, paplay, play) or set with
  `[sounds] command`.
- tmux 2.7 (RHEL 8) and 3.2a (RHEL 9) are supported: the outer config is rendered per version,
  the keybinds help falls back to a bare popup or a window, `flok doctor` lists what a version
  lacks. CI runs the end-to-end suites on macOS, Ubuntu, Rocky 8 and Rocky 9.
- Terminal bell: `[sounds] bell = auto | always | never`; with no sound player the bell rings
  the local terminal, also over ssh.
- Bundled sounds are WAV (PulseAudio players cannot decode mp3 on RHEL 9).
- Prebuilt Linux tarballs (amd64, arm64, with checksums) are attached to every release; README
  gained a Linux install recipe and a tmux version table.
- The sidebar re-pins its width itself on tmux versions without the `window-resized` hook.

## 0.2.4 (2026-09-12)

- The spinner keeps running when the terminal window loses focus; idle mode applies only while
  the sidebar is hidden.
- A turn that ends without a Stop hook (Esc, usage limit, error) is cleared by three idle
  screen samples as well as by Claude's registry, so the sidebar catches up in seconds rather
  than tens of seconds.

## 0.2.3 (2026-09-12)

- Sidebar: only the 1 s tick spawns tmux; screen results, registry samples and hook changes
  re-merge cached data. Idle mode (`[sidebar] idle_poll_ms`) slows polling while hidden.

## 0.2.2 (2026-09-12)

- CPU: the menu bar redraws only on change and animates at 2 fps (`[bar] animate_ms`); the
  sidebar renders at 15 fps (`[sidebar] fps`), batches screen captures, takes focus from tmux
  focus events and polls Claude's registry only while it can use the answer.
- A Stop with background subagents still running shows as working ("waiting"), not done.

## 0.2.1 (2026-09-12)

- Menu bar: "Edit config…" and "Reload sidebar" items; `flok edit-config`.

## 0.2.0 (2026-09-12)

- flok-bar, the macOS menu bar companion (`[bar] enabled`): animated title, attention badge,
  agent dropdown, click to return to an agent (AeroSpace or AppleScript focus).
- Sidebar width is pinned across window resizes; a default config is written on install;
  fallback corrections are persisted, so states no longer flap; `claude` is found without PATH.
- CI for flok and the tap; README rewritten for a public audience.

## 0.1.2 (2026-09-12)

- Prefix chords typed while the sidebar has focus are replayed into the work pane; `prefix g`
  toggles focus; the rail shows a focus cue.
- `flok up` exits 0 after a deliberate detach or `flok down`.
- A pane title never clears a hook-reported working state.

## 0.1.1 (2026-09-11)

- The rail shows the keyboard cursor; hotkeys park it.

## 0.1.0 (2026-09-11)

- First release: a herdr-style sidebar for tmux with sessions on top and agents below, driven
  by Claude Code and Copilot CLI hooks with title, registry and screen-rule fallbacks; sounds
  when an agent needs input or finishes unseen; compact rail, hide, `prefix ?` keybinds help;
  Homebrew tap `w4jnl/tap`.
