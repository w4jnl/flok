# Copilot instructions

Read `CLAUDE.md` first. It is the repository's primary engineering guide and
contains the canonical build/test commands, e2e workflow, architecture,
state-pipeline details, tmux compatibility constraints, platform conventions,
and release process. Do not duplicate or contradict those instructions here.

## Quick start

Use the existing Makefile targets:

```sh
make build
make test
make vet
```

For a focused test, use Go's package and test selectors, for example:

```sh
go test ./internal/merge -run TestHookAuthorityAndSeen -v
go test ./internal/rules -run TestClaudeFixtures -v
```

CI also checks formatting with `gofmt`, runs `go vet ./...`, builds
`./cmd/flok`, and runs `./bin/flok version`. The end-to-end suites are the
individual `scripts/e2e/m*.sh` scripts; `scripts/e2e/m1.sh` is a single-suite
example and `CLAUDE.md` explains their isolated tmux setup and coverage.

## Changes that need cross-file care

- Treat `CLAUDE.md` and `README.md` as authoritative: `CLAUDE.md` is the
  developer/architecture guide, while `README.md` describes user-visible
  behavior, configuration, keys, and requirements.
- Preserve the outer/inner tmux boundary. The outer server hosts the sidebar;
  the inner server remains user-owned and must not be modified by the sidebar.
- Keep state semantics synchronized across `internal/state` and
  `internal/merge`; the relevant tests construct their inputs directly and
  should be updated with behavior changes.
- Keep tmux-version gates, ID-based targeting, escaped-output decoding, and
  cgo boundaries described in `CLAUDE.md`. These are compatibility
  requirements, not optional implementation details.
- Keep hook installation and event mapping synchronized when adding agent
  events, and keep Claude title detection synchronized between the adapter and
  its screen manifest.
- Use `FLOK_CONFIG` and `FLOK_STATE` for isolated tests/e2e. Do not rely on the
  user's tmux server or default state while testing.
