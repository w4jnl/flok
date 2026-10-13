# The menu bar and keep awake (macOS)

[‹ README](../README.md)

## The menu bar companion

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
works, and `● N` when N agents are waiting for you (the icon gains a dot as well); `⚡` follows
the glyph while keep-awake is on (`◑ ⚡ ● 2`). The dropdown
lists agents in the sidebar's order with the same state detail, remote ones tagged `@host`, and
once a host is registered a `servers` block: local first, then every host with its mode, agent
count or connection state, the front one marked; a click brings that server's work pane to the
front (`flok host front <name> --focus`). Below "Keep awake", a "Relay link" row (shown while
the sidebar runs one) is checked unless the link is switched off, names what is wrong in its
title, and switches it with a click (`flok link toggle`). With `[bar] enabled = true`,
`flok up` starts the bar (single instance) and `flok down` or a detach ends it; the bar also quits
by itself 30 s after flok disappears.

Click-to-return uses AeroSpace when it is installed (`aerospace focus --window-id` on the window
titled `TMUX…`, which also switches workspace) and AppleScript otherwise: it activates the terminal
app that ran `flok up` (recorded from `TERM_PROGRAM`: Ghostty, iTerm2, Terminal, WezTerm, kitty)
and,
best effort, raises the `TMUX…` window. Raising a specific window goes through System Events and
needs Accessibility permission for `osascript`; without it the app comes to the front with its
last-used window, which is usually the right one anyway.

Menu bar (when enabled): click an agent row to return to it (remote agents read `project ·
claude @beta`; under them a `servers` block lists local and every host, the front one marked, and a
click brings that server's pane to the front), "Show
flok" to bring the terminal window to the front, "Keep awake" to toggle it (checked while it is
on), "Edit config…" to
open `config.toml` (in a new tmux window with `[bar] editor`
set, else with the default app), "Reload sidebar" to apply it, "Quit flok-bar" to remove the item
(flok keeps running). The version row at the bottom opens the release notes (`CHANGELOG.md`,
all releases newest first) and "GitHub repository" the source.

## Keep awake

```
flok keep-awake / "Keep awake" ──writes──► ~/.local/state/flok/keep-awake (0|1)
                                                 │ fsnotify
                                                 ▼
                  sidebar: IOPMAssertionCreateWithName  ──► snapshot.json "keep_awake" ──► ⚡, ✓ row
```

`flok keep-awake` does what `caffeinate -d -i` does, without running another program: the sidebar
takes the two IOKit power assertions itself, `PreventUserIdleDisplaySleep` (the display stays on,
so neither the screen saver nor the idle lock kicks in) and `PreventUserIdleSystemSleep`. They
are called through [purego](https://github.com/ebitengine/purego), so the flok binary stays
cgo-free. Because the sidebar holds them, they cannot outlive the session: macOS drops them the
moment the sidebar exits, including a crash or a killed tmux server, and `flok down` or the
end of the session turns keep-awake off; the next `flok up` starts with it off. `flok reload`
keeps it (the new sidebar takes the assertions again).

`flok keep-awake` with no argument toggles; `on`, `off` and `status` do what they say, and
`--notify` puts the result on tmux's status line instead of printing it, which is what the
snippet's `prefix u` binding uses. It waits
until the sidebar confirms the change and prints the result. While it is on, `flok status`
adds a `keep-awake: on` line (`"KeepAwake": true` in `--json`), `flok doctor` reports it, and
`pmset -g assertions` lists both assertions for the sidebar's pid under the name
`flok keep-awake`. It needs flok to be running and is macOS only; on Linux the command says so.
Closing the lid of a MacBook on battery still puts it to sleep; no user-space program can
prevent that.

**Staying active in Teams, Slack & co.** The power assertions keep the Mac and its display
awake, but apps that show your presence watch the time since your last key press or mouse
move, so Teams still turns you Away after five minutes. With `[keep_awake] presence = true`
the sidebar also resets that clock while keep-awake is on: after 60 s without input it posts
an empty modifier-key event (a "flags changed" event that carries the modifiers already held,
so no app sees a key press and the cursor does not move; checked every 30 s). It skips the
nudge while the display sleeps, so a display you put to sleep yourself stays asleep. macOS
only accepts synthetic input from apps with Accessibility permission: grant it to the terminal
app you run `flok up` in (System Settings › Privacy & Security › Accessibility). `flok doctor`
says whether that is in place; `flok keep-awake status`, `flok status` (`"KeepAwakePresence"`
in `--json`) and the menu bar tooltip report the presence as active, or blocked when macOS
drops the events. The key is read when the sidebar starts; `flok reload` applies a change.
