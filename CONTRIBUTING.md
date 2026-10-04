# Contributing

flok is a personal tool, published because the approach may be useful to others. Read
[Scope and status](README.md#scope-and-status) first: features are the ones I need, and requests
that do not fit that workflow will probably be declined, politely.

What is welcome:

- Bug reports with a reproduction, `flok doctor` output and `tmux -V` (the issue form asks for
  them).
- Fixes with a test. Unit tests construct the merge inputs and hook events directly; the
  end-to-end suites under `scripts/e2e` drive isolated tmux servers with a fake agent and never
  touch your own tmux. `make test`, `make vet` and `make e2e` (the suites `scripts/e2e/m1.sh` …
  `m10.sh`, run in order by `scripts/e2e/run-all.sh`; never two at once, they share the
  `e2e-inner`/`e2e-outer` servers) must pass; CI runs them on macOS and Linux, including tmux
  3.2a in a container (2.7 is flok's floor but no longer part of CI). In a suite, assert a transition with `expect_soon` (it polls until the
  pattern holds), never with `sleep` then `expect`; a wait after an action must only be
  satisfiable after the transition (`wait_gone`, `wait_json` on a value only the new state
  writes); a fixed dwell is for negative checks only.
- Small, focused pull requests. Describe what changed and what you verified, in plain words.

Before a larger change, open an issue and ask; it saves both of us a rewrite. The
[Development](README.md#development) section of the README explains the layout and the
conventions (tmux version gates, the verbatim herdr manifests, release notes in `CHANGELOG.md`).
