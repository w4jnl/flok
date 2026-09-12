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

# Claude's registry says idle right until a prompt is submitted (and for a moment after): that must
# not delay or hide the working state.
AGENT_PID=$(IN display -p -t "$AGENT" '#{pane_pid}')
registry "[{\"pid\":$AGENT_PID,\"status\":\"idle\",\"kind\":\"interactive\",\"name\":\"fake\",\"sessionId\":\"abc\"}]"
sleep 3.5   # several idle samples accumulate
IN select-pane -t "$AGENT" -T "◑ fake-agent"     # a working Claude shows the spinner in its title
hook claude '{"hook_event_name":"UserPromptSubmit","session_id":"abc"}'
wait_for "[◐◓◑◒] $PROJ" 2 || true
expect "working shows within 2 s of the prompt despite stale idle registry samples" "[◐◓◑◒] $PROJ" "$(capture)"
sleep 2.5
expect "still working while the registry catches up" "[◐◓◑◒] $PROJ" "$(capture)"
registry "[{\"pid\":$AGENT_PID,\"status\":\"busy\",\"kind\":\"interactive\",\"name\":\"fake\",\"sessionId\":\"abc\"}]"
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

# Background subagents (client is looking at this pane): the main turn ends (Stop) while tasks are
# in flight -> still active, "N agents"; idle_prompt does not end it; the Stop after the wake-up
# turn does. Focused, so it ends idle with nothing unseen, leaving the state as it was.
hook claude '{"hook_event_name":"UserPromptSubmit","session_id":"abc"}'
hook claude '{"hook_event_name":"PreToolUse","session_id":"abc","tool_name":"Agent","tool_input":{"description":"explore","run_in_background":true},"tool_use_id":"a1"}'
hook claude '{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"Agent","tool_use_id":"a1"}'
hook claude '{"hook_event_name":"Stop","session_id":"abc","background_tasks":[{"id":"t1","type":"subagent","status":"running","agent_type":"Explore"},{"id":"t2","type":"subagent","status":"running","agent_type":"Plan"}]}'
wait_for '2 agents' 3 || true
snap=$(capture); echo "--- waiting for subagents ---"; printf '%s\n' "$snap" | grep -v '^ *$' | sed -n '4,6p'
expect "waiting for subagents shows as working with the count" "[◐◓◑◒] $PROJ +2 agents" "$snap"
hook claude '{"hook_event_name":"Notification","session_id":"abc","notification_type":"idle_prompt"}'
sleep 0.6
expect "idle_prompt does not end the paused turn" "[◐◓◑◒] $PROJ +2 agents" "$(capture)"
hook claude '{"hook_event_name":"PreToolUse","session_id":"abc","tool_name":"Read","tool_input":{"file_path":"/x/y.go"},"tool_use_id":"r1"}'
hook claude '{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"Read","tool_use_id":"r1"}'
hook claude '{"hook_event_name":"Stop","session_id":"abc","background_tasks":[]}'
wait_for "○ $PROJ" 3 || true
expect "final Stop with nothing in flight ends the turn" "○ $PROJ" "$(capture)"
expect "nothing unseen after a watched wait" '^agents +priority' "$(capture)"

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
# Inside tmux Claude keeps the idle "✳" title while busy: that must NOT clear a hook working state.
IN select-pane -t "$AGENT" -T "✳ fake-agent"
sleep 2.5
expect "idle title does not clear hook working" '[◐◓◑◒] (flok|fake-agent)' "$(capture)"
# Interrupted turn (Esc emits no hook): Claude's registry turns idle -> idle after two samples, no sound, nothing new unseen.
AGENT_PID=$(IN display -p -t "$AGENT" '#{pane_pid}')
registry "[{\"pid\":$AGENT_PID,\"status\":\"idle\",\"kind\":\"interactive\",\"name\":\"fake\",\"sessionId\":\"abc\"}]"
wait_for '○ (flok|fake-agent)' 6 || true
snap=$(capture)
expect "registry idle x2 -> interrupted turn shows idle" '○ (flok|fake-agent)' "$snap"
expect "interrupted turn leaves only the earlier block unseen" '^agents · 1' "$snap"
expect "correction persisted into the hook record" '"state": "idle"' "$(cat "$T"/state/agents/*.json)"
expect "correction reason recorded" 'corrected:registry idle' "$(cat "$T"/state/agents/*.json)"
registry '[]'
sleep 6   # registry gone again: a persisted correction must not flap back to working
expect "corrected state does not flap back" '○ (flok|fake-agent)' "$(capture)"

hook claude '{"hook_event_name":"SessionEnd","session_id":"abc","reason":"prompt_input_exit"}'
wait_for "~$PROJ" 3 || true
expect "session end deletes the record (back to title-only ~)" "~$PROJ" "$(capture)"
expect "record file removed" '^0$' "$(ls "$T/state/agents" | grep -c '\.json$' || true)"
finish
