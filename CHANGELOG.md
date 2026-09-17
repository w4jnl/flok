# Changelog

User-facing changes per release, newest first. `scripts/release.sh` refuses to tag a version
that has no section here and uses the section as the GitHub release notes, so every release
updates this file first. Dates are the tag dates.

## Unreleased

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
