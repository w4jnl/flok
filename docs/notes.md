# Session reports into nt

[‹ README](../README.md)

flok sees every hook event of every agent it watches: the prompts, the tools, the permission
blocks and question forms, the waits on background work, the errors, the end. With `[notes]`
on, it keeps that as a journal per session and hands it to [nt](https://github.com/w4jnl/nt),
the notes tool, which writes the session's report into the notes repository (one markdown
file per session, `agents/YYYY/YYYY-MM-DD/<host>--<agent>--<project>--<sid8>.md`), compiles
the day's digest and the week's. nt installs no agent hooks of its own for this; flok is the
one receiver, nt the one writer of the notes.

```toml
[notes]                     # session reports into nt (nt agent import); off by default
command = "auto"            # off | auto (the nt on PATH) | a path to nt
```

```sh
flok doctor                 # notes: /opt/homebrew/bin/nt writes the session reports, last import 2m0s ago
flok host notes beta off    # a remote host whose agents this machine does not report
```

## What a report gets

The hook on every machine keeps a journal under `~/.local/state/flok/sessions/<session id>.json`:

- the session: agent, directory, branch and head at start and end, model, start and end;
- one entry per turn: when it started and ended, the first line of the agent's last message
  (200 characters at most), the tools it ran as names and counts (never their arguments), and
  what happened in it: a permission block with its tool, a question with its summary, a wait on
  background work with its length, an error, a compaction;
- the totals: turns, tool runs, seconds blocked, seconds waited.

Nothing in it is a command line, a file path beyond the directory, or a transcript: the notes
repository is pushed to GitHub. The agent's own account of the work (Done, Decisions, Open)
stays what it is: the agent writes it with `nt agent write` through the nt skill, and the
import never touches it.

flok sends the journal to `nt agent import` when the report changes in a way worth showing:
at the start, on a new block, on a wait, at a Stop, on an error and at the end. Not on every
tool call. The hook only writes the journal and starts a small worker for that, detached, so
neither the agent nor the sidebar's state ever waits on git or nt; the worker reads the branch
and head, gives nt two seconds, and writes a failure to `hook.log` once per session. Every
import carries the whole journal, so a missed one is repaired by the next. Journals of ended
sessions are removed after 14 days.

A session in a temporary directory (`/tmp`, `$TMPDIR`) gets no report, by nt's rule; a Claude
started with its hooks disabled (nt's own weekly summary does that) is never seen by flok.

## Remote hosts

A full-mode host journals the same way: its `flok hook` runs for every event there. It never
imports (a served host has no notes repository). Instead `flok serve` streams a changed journal
to the flok that drives the host, and that flok's nt writes the report, with the host's alias
as the report's host: `beta--claude-code--api--1a2b3c4d.md`. Nothing on the host needs nt.

With `[notes]` on, every enabled full-mode host is reported; `flok host notes <name> off`
leaves one out. One machine reports a host: the host remembers which flok imports it (the
first that claims it), and a second machine that turns notes on for the same host is told in
its footer that the host already reports through the first, and does not import; `flok host
notes <name> off` there lets a claim go. A machine that has not connected for seven days loses the claim; `flok host notes
<name> on --take` takes it over at once. After a disconnect, serve sends the journals that
changed since the importer last saw them, so a Mac that was off loses nothing; the reports
keep their real times. A plain-mode host has no hooks and no journal, so no report.

## The record (the contract with nt)

`nt agent import` reads one JSON object on stdin, the whole session as flok knows it, and
upserts the report for (`agent`, `session`). It exits 0 and prints nothing on success; on a
failure it exits non-zero with one line on stderr. `nt agent import --probe` prints the highest
`version` it accepts and exits 0 (`flok doctor` asks it; the hook never does). nt ignores
fields it does not know; a `version` above what it accepts is refused with exit 2.

```json
{
  "version": 1,
  "agent": "claude-code",
  "session": "f0ceca08-e88a-4f70-9b56-fe8fd372fd74",
  "host": "",
  "cwd": "/Volumes/data/work/claude/flok",
  "branch": "main",
  "head": "1553630",
  "model": "claude-fable-5-1",
  "created": "2026-10-29T13:47:44+01:00",
  "updated": "2026-10-29T18:28:13+01:00",
  "state": "working",
  "turns": 2,
  "tools": 15,
  "blocked_s": 95,
  "waited_s": 710,
  "log": [
    {
      "started": "2026-10-29T13:47:50+01:00",
      "ended": "2026-10-29T13:52:10+01:00",
      "text": "Yes, rework it. The nt side agrees, with one refinement.",
      "tools": {"Bash": 12, "Read": 3},
      "events": [
        {"at": "2026-10-29T13:48:02+01:00", "kind": "blocked", "text": "permission: Bash"},
        {"at": "2026-10-29T13:50:00+01:00", "kind": "question", "text": "Which branch?"},
        {"at": "2026-10-29T13:51:00+01:00", "kind": "wait", "text": "2 agents + shell", "seconds": 710},
        {"at": "2026-10-29T13:51:30+01:00", "kind": "compaction"},
        {"at": "2026-10-29T13:52:00+01:00", "kind": "error", "text": "stop failure"}
      ]
    }
  ]
}
```

| field | meaning |
|---|---|
| `version` | the record's shape; 1 is this table |
| `agent` | nt's slug: `claude-code`, `copilot-cli` |
| `session` | the agent's own session id, whole |
| `host` | `""`: this machine, nt uses its configured host; else flok's alias for the remote host the agent runs on, used as given |
| `cwd`, `branch`, `head` | the directory the session started in and its git state; `head` is taken again at the end |
| `model` | when the agent names it |
| `created`, `updated`, `ended` | RFC 3339 with offset; `ended` is absent while the session runs; `created`'s day is the report's folder |
| `end_reason` | with `ended`: `error` (the last turn failed), `clear`, `logout`, `exit`, `other` |
| `state` | live: `working`, `blocked`, `waiting`, `idle`, `done` |
| `turns`, `tools`, `blocked_s`, `waited_s` | totals over the session; `turns` counts every turn opened, the open one and those dropped from `log` included |
| `log[]` | the turns, oldest first, at most 200; `text` ≤ 200 characters, may be `""` |
| `log[].tools` | tool name → runs in that turn |
| `log[].events[]` | `blocked` (text `permission: <tool>`), `question` (the form's summary), `wait` (the kinds of background work, `seconds` once known), `error`, `compaction` |

What nt makes of it (nt's rules, written here so both sides read the same): the frontmatter
and `## Log` come from the record on every import; Done, Decisions, Open and a status or
summary the agent wrote with `nt agent write` are kept. The status while the session runs is
`blocked` when `state` is `blocked`, else `in-progress`; at the end, without the agent's own,
`failed` when `end_reason` is `error`, `abandoned` when no turn ended (`turns` is 0, or the log holds one open turn alone), else `done`. Optional
frontmatter keys (`model`, `turns`, `tools`, `blocked_minutes`, `waited_minutes`) are added,
`schema` stays 1. The Log is one line per turn, at the time the turn ended (an open turn
shows at its start, without text, when it has events, so a live block is visible before the
Stop; a turn without text is its time alone), with indented sub-bullets for the tools and the
events, every line at most 200 characters, no HTML. Log lines older than the record's first
turn are kept, so the record may drop old turns (flok keeps 200) while `turns`, `tools`,
`blocked_s` and `waited_s` stay whole-session totals. The report's folder is the date of
`created` in the offset it carries. nt's sessions map records the report
as flok's, so `nt agent write` finds it as before and nt's own `agent start|turn|end` yield
for it during the migration. A `cwd` in a temporary directory is skipped with exit 0.
