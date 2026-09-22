# Contributing

flok is a personal tool, published because the approach may be useful to others. Read
[Scope and status](README.md#scope-and-status) first: features are the ones I need, and requests
that do not fit that workflow will probably be declined, politely.

What is welcome:

- Bug reports with a reproduction, `flok doctor` output and `tmux -V` (the issue form asks for
  them).
- Fixes with a test. Unit tests construct the merge inputs and hook events directly; the
  end-to-end suites under `scripts/e2e` drive isolated tmux servers with a fake agent and never
  touch your own tmux. `make test`, `make vet` and `scripts/e2e/m1.sh` … `m8.sh` must pass; CI
  runs them on macOS and Linux, including tmux 2.7 and 3.2a in containers.
- Small, focused pull requests. Describe what changed and what you verified, in plain words.

Before a larger change, open an issue and ask; it saves both of us a rewrite. The
[Development](README.md#development) section of the README explains the layout and the
conventions (tmux version gates, the verbatim herdr manifests, release notes in `CHANGELOG.md`).
