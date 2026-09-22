# Security policy

flok runs on your own machine, talks to your tmux servers and to the agent CLIs' hook
interfaces, and never sends anything over the network. The menu bar companion opens a browser
on two fixed GitHub URLs. The attack surface is local: the hook receiver (`flok hook`) is run by
Claude Code and Copilot CLI with their payloads, the sidebar reads pane contents through tmux,
and `flok resurrect save` rewrites tmux-resurrect state files.

## Supported versions

Only the latest release gets fixes. Update with `brew upgrade flok` or from the
[releases page](https://github.com/w4jnl/flok/releases).

## Reporting a vulnerability

Use GitHub's private vulnerability reporting for this repository:
<https://github.com/w4jnl/flok/security/advisories/new>. Describe what an attacker needs to
control (a hook payload, pane text, a config file, a tmux-resurrect save file) and what they
gain. You will get an answer within a week; fixes ship as a patch release with a note in
`CHANGELOG.md`. Please do not open a public issue for something exploitable before it is fixed.
