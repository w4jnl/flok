#!/usr/bin/env bash
# M8: `flok resurrect save` rewrites an agent pane's process field in a tmux-resurrect state
# file: from Claude's registry (`claude agents --json`) first, else from the hook record's
# session ID; a pane with neither is saved as a shell and resurrect.log says why. Other panes
# and panes unknown to the inner server are left untouched.
. "$(dirname "$0")/lib.sh"

WIN=$(IN display -p -t "$AGENT" '#{window_index}')
PIDX=$(IN display -p -t "$AGENT" '#{pane_index}')
APID=$(IN display -p -t "$AGENT" '#{pane_pid}')     # the fake agent was exec'd: the pane pid is its pid
SAVE=$T/tmux_resurrect_test.txt
LOG=$T/state/resurrect.log
# 11 tab-separated fields per pane, as tmux-resurrect writes them:
# pane session window window_active :flags pane_index title :dir pane_active command :full_command
mk_save() { printf 'pane\tAlpha\t%s\t1\t:*\t%s\tagent\t:%s\t1\tclaude\t:claude\npane\tBeta\t0\t1\t:*\t0\tshell\t:%s\t1\tbash\t:\npane\tGone\t3\t1\t:*\t1\tstale\t:/nowhere\t1\tclaude\t:claude -c\nwindow\tAlpha\t%s\t:agent\t1\t:*\tlayout\t:\n' "$WIN" "$PIDX" "$R" "$T" "$WIN" > "$SAVE"; }
agent_line() { grep -E "^pane	Alpha	$WIN	" "$SAVE"; }

# nothing known about the pane: saved as a shell, with a diagnostic
registry '[]'
mk_save; "$BIN" resurrect save "$SAVE"; rc=$?
expect "resurrect save exits 0" '^0$' "$rc"
expect "unknown agent pane is saved as a shell" '	claude	:$' "$(agent_line)"
expect "resurrect.log names the pane and the reason" "^[0-9T:Z+-]+ $AGENT claude: no hook record and not listed by .claude agents." "$(cat "$LOG" 2>/dev/null)"   # RFC 3339 stamp: +02:00 here, Z on CI
expect "other panes untouched" '^pane	Beta	0	1	:\*	0	shell	:'"$T"'	1	bash	:$' "$(grep '^pane	Beta' "$SAVE")"
expect "panes the inner server does not know are untouched" ':claude -c$' "$(grep '^pane	Gone' "$SAVE")"
expect "window records untouched" '^window	Alpha	'"$WIN"'	:agent	1	:\*	layout	:$' "$(grep '^window' "$SAVE")"

# the registry alone is enough: hooks that never fired no longer lose the pane
registry '[{"pid":'"$APID"',"kind":"interactive","sessionId":"reg-1111","status":"idle","startedAt":1}]'
mk_save; "$BIN" resurrect save "$SAVE"
expect "registry session id becomes claude --resume" "	claude	:claude --resume 'reg-1111'$" "$(agent_line)"
expect "resurrect.log removed when nothing was skipped" '^0$' "$(ls "$T/state" | grep -cx resurrect.log || true)"

# a hook record alone is enough too
registry '[]'
hook claude '{"hook_event_name":"UserPromptSubmit","session_id":"hook-2222","cwd":"'"$R"'"}'
mk_save; "$BIN" resurrect save "$SAVE"
expect "hook session id becomes claude --resume" "	claude	:claude --resume 'hook-2222'$" "$(agent_line)"

# both: the registry describes the live process and wins over the (possibly stale) hook record
registry '[{"pid":'"$APID"',"kind":"interactive","sessionId":"reg-3333","status":"busy","startedAt":2}]'
mk_save; "$BIN" resurrect save "$SAVE"
expect "registry wins over the hook record" "	claude	:claude --resume 'reg-3333'$" "$(agent_line)"

# a malformed file is left alone and reported
printf 'pane\ttoo\tshort\n' > "$SAVE"
rc=0; out=$("$BIN" resurrect save "$SAVE" 2>&1) || rc=$?   # set -e: the failure must be caught on the same line
expect "malformed state file is reported" 'want at least 11' "$out"
expect "malformed state file exits non-zero" '^[1-9]' "$rc"
expect "malformed state file unchanged" '^pane	too	short$' "$(cat "$SAVE")"

finish
