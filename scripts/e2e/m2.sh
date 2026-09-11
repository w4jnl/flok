#!/usr/bin/env bash
# M2: hook-driven states. The inner client looks at Alpha:0, so the agent pane is unfocused.
source "$(dirname "$0")/lib.sh"

# Hooks outside tmux are ignored and write nothing.
printf '{"hook_event_name":"Stop"}' | TMUX_PANE= "$BIN" hook claude
expect "hook without TMUX_PANE writes no record" '^0$' "$(ls "$T/state/agents" | grep -c '\.json$' || true)"

t0=$(python3 -c 'import time;print(time.time())')
hook claude '{"hook_event_name":"SessionStart","source":"startup","session_id":"abc","cwd":"'"$R"'"}'
t1=$(python3 -c 'import time;print(time.time())')
ms=$(python3 -c "print(int(($t1-$t0)*1000))")
expect "hook round trip < 80 ms (took ${ms} ms)" '^[0-7]?[0-9]$' "$ms"
wait_for "○ $PROJ *$" 3 || true
expect "hooked agent shows without ~" "○ $PROJ *$" "$(capture)"

IN select-pane -t "$AGENT" -T "◑ fake-agent"     # a working Claude shows the spinner in its title
hook claude '{"hook_event_name":"UserPromptSubmit","session_id":"abc"}'
hook claude '{"hook_event_name":"PreToolUse","session_id":"abc","tool_name":"Bash","tool_input":{"command":"npm test"},"tool_use_id":"t1"}'
wait_for 'Bash 0:0' 3 || true
snap=$(capture); echo "--- working with tool ---"; printf '%s\n' "$snap" | grep -v '^ *$' | sed -n '4,6p'
expect "working shows tool + elapsed" "[◐◓◑◒] $PROJ +Bash 0:0[0-9]" "$snap"

IN select-pane -t "$AGENT" -T "✳ fake-agent"     # permission prompt: title goes idle, block must stick
hook claude '{"hook_event_name":"PermissionRequest","session_id":"abc","tool_name":"Bash","tool_input":{"command":"npm test"},"tool_use_id":"t1"}'
hook claude '{"hook_event_name":"Notification","session_id":"abc","notification_type":"permission_prompt","message":"Claude wants to run: Bash"}'
wait_for 'perm:Bash' 3 || true
sleep 1.5   # long enough for any title-based fallback to have fired
snap=$(capture); echo "--- blocked ---"; printf '%s\n' "$snap" | grep -v '^ *$' | sed -n '1,6p'
expect "blocked shows perm:Bash"     "● $PROJ +perm:Bash" "$snap"
expect "blocked counted once (dedupe)" '^agents · 1' "$snap"
expect "space rollup blocked"        '● Alpha' "$snap"

IN select-pane -t "$AGENT" -T "◑ fake-agent"
hook claude '{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"Bash","tool_use_id":"t1"}'
wait_for "[◐◓◑◒] $PROJ" 3 || true
expect "tool end -> working again" "[◐◓◑◒] $PROJ +0:0[0-9]" "$(capture)"

IN select-pane -t "$AGENT" -T "✳ fake-agent"
hook claude '{"hook_event_name":"Stop","session_id":"abc","stop_hook_active":false}'
hook claude '{"hook_event_name":"Notification","session_id":"abc","notification_type":"agent_completed"}'
wait_for 'done · 2' 3 || true
snap=$(capture); echo "--- done ---"; printf '%s\n' "$snap" | grep -v '^ *$' | sed -n '4,6p'
expect "stop while unfocused -> done · 2 (blocked + done)" "✓ $PROJ +done · 2" "$snap"
expect "events logged" 'PermissionRequest' "$(cat "$T/state/events.log")"
expect "state file has hooks" '"has_hooks": true' "$(cat "$T"/state/agents/*.json)"

OUT send-keys -t "$SIDEBAR" Tab Enter
wait_for "○ $PROJ *$" 3 || true
expect "switching there marks it seen -> idle" "○ $PROJ *$" "$(capture)"
expect "seen mark persisted" 'seen_at' "$(cat "$T"/state/seen/*.json)"

# Stop while focused is idle, silent, no unseen.
hook claude '{"hook_event_name":"UserPromptSubmit","session_id":"abc"}'
hook claude '{"hook_event_name":"Stop","session_id":"abc"}'
sleep 0.6
expect "stop while focused -> idle, nothing unseen" '^agents +priority' "$(capture)"

# AskUserQuestion is a block with reason question.
OUT send-keys -t "$SIDEBAR" '!'   # look at Alpha window 0 again? '!' selects space 1 = Alpha (same session) -> still focused on agent pane
IN select-window -t Alpha:0        # move the inner client away from the agent pane
sleep 0.4
hook claude '{"hook_event_name":"UserPromptSubmit","session_id":"abc"}'
hook claude '{"hook_event_name":"PreToolUse","session_id":"abc","tool_name":"AskUserQuestion","tool_input":{},"tool_use_id":"q1"}'
wait_for 'question' 3 || true
expect "AskUserQuestion -> question" "● $PROJ +question" "$(capture)"

# The block sticks while the title is idle (that is what a real prompt looks like) ...
sleep 1.5
expect "question sticks despite idle title" "● $PROJ +question" "$(capture)"
# ... and clears through hook events only (answer -> PostToolUse -> working).
IN select-pane -t "$AGENT" -T "◑ fake-agent"
hook claude '{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"AskUserQuestion","tool_use_id":"q1"}'
wait_for "[◐◓◑◒] $PROJ" 3 || true
expect "answered question -> working" "[◐◓◑◒] $PROJ" "$(capture)"
# Interrupted turn: hooks say working, but the title goes idle with no Stop -> idle after 3 polls, no sound, nothing unseen.
IN select-pane -t "$AGENT" -T "✳ fake-agent"
wait_for "○ $PROJ *$" 4 || true
snap=$(capture)
expect "interrupted turn -> idle" "○ $PROJ *$" "$snap"
expect "interrupted turn leaves only the earlier block unseen" '^agents · 1' "$snap"

hook claude '{"hook_event_name":"SessionEnd","session_id":"abc","reason":"prompt_input_exit"}'
wait_for "~$PROJ" 3 || true
expect "session end deletes the record (back to title-only ~)" "~$PROJ" "$(capture)"
expect "record file removed" '^0$' "$(ls "$T/state/agents" | grep -c '\.json$' || true)"
finish
