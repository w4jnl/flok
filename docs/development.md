# Development

[‹ README](../README.md)

```sh
make build             # bin/flok with the version stamped from git describe
make test              # unit tests
make e2e               # headless end-to-end suites on isolated tmux servers, m1..m11, in order
scripts/e2e/m4.sh      # one suite (make e2e SUITES="m4 m7" for a few)
make relay             # bin/flok-relay, the phone's relay (cgo-free)
make ios-build         # the iOS app for the simulator, no signing (ios-test runs its unit tests)
make icons             # regenerate the menu bar template icons (assets/icons/gen)
assets/brand/flok-appicon.svg   # the phone app's icon; its 1024 px render, flattened (the App Store refuses an alpha channel), is the asset catalog's AppIcon.png
assets/demo/record.sh  # the README's GIF; menubar.sh the menu bar picture; phone.sh the phone screens
assets/diagram/render.sh   # the README's architecture picture
scripts/spike/m0-outer.sh check   # nested-outer passthrough checks
scripts/spike/m0-outer.sh up      # interactive checklist against your real server
```

The end-to-end suites start their own tmux servers (`e2e-inner`, `e2e-outer`) with a private
state and config directory, run a fake agent binary named `claude` that paints scripted screens,
replay hook payloads against it, and assert on `capture-pane` output of the sidebar. Your real
tmux server is never touched. m10 adds remote hosts behind a fake `ssh`; m11 runs the real
`flok-relay` with a stand-in APNs and answers agents through it. `scripts/e2e/run-all.sh` runs
them in order, every one even after a failure, each under a watchdog (`E2E_SUITE_TIMEOUT`, 300
s), and ends with a table; a suite that died prints `ABORT <file>:<line>: <command>` and its
summary line says `aborted`. `FLOK_DEBUG=1` keeps the logs of the suites that did not pass under
`/tmp/flok-e2e-logs` (`E2E_KEEP_LOGS=1` keeps them all; `E2E_TMUX_VERBOSE=0` leaves out the
outer tmux's own log).

```
cmd/flok               entry point
cmd/flok-bar           macOS menu bar companion (cgo, fyne.io/systray); everything else is cgo-free
cmd/flok-relay         the relay for the phone (serve, tail, token, pin, health)
internal/cli           subcommands (up, sidebar, hook, nav, keys, install, doctor, link, host, ...)
internal/launcher      outer server: config template, create/attach, attach loop, runtime.json
internal/ui            Bubble Tea sidebar, rail, help overlay
internal/poller        the state pipeline without the rendering (shared by the sidebar and flok serve)
internal/remote        remote hosts: ssh, flok serve and its frames, plain mode, the keys on hosts
internal/link          the sidebar's WebSocket to the relay; link/tunnel the TLS inside it, link/wire the frames
internal/relay         flok-relay: instances, the app's API, pushes and Live Activities (relay/apns)
internal/answer        the allowlist every answer from the phone goes through
internal/push          [notify]: ntfy or JSON posts
internal/snapshot      snapshot.json the sidebar publishes for flok-bar
internal/bar           menu bar title/rows logic (pure, tested)
internal/focus         bring the terminal window to the front (aerospace, applescript)
internal/awake         keep-awake power assertions (IOKit through purego, no cgo; macOS only)
internal/merge         authority merge of all state sources into one snapshot
internal/state         hook state machine and the file store (flock, atomic writes)
internal/agent         model, events, adapters (claude, copilot)
internal/rules         herdr manifest engine (regions, matchers), bundled manifests
internal/claudereg     `claude agents --json` reader, pid → tty → pane
internal/tmux          exec-based tmux client, one-call snapshot
internal/keys          list-keys collector and labels for the help
internal/nav           switch-client / select-window / select-pane
internal/notify        sounds (afplay, pw-play, paplay, mpv, ffplay, play or a custom command), debounce
internal/install       settings.json / Copilot hook writers, tmux snippet
internal/config        config.toml, XDG paths
app/ios                the iOS app (SwiftUI, XcodeGen); app/API.md its contract with the relay
deploy/relay           the relay's image, compose stack and example configs
```

Releases: `scripts/release.sh <version>` runs the tests, tags, pushes, bumps the formula in
[w4jnl/homebrew-tap](https://github.com/w4jnl/homebrew-tap) and creates the GitHub release.
