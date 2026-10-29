#!/usr/bin/env bash
# M2: hook-driven states. The inner client looks at Alpha:0, so the agent pane is unfocused.
# fakehttp (scripts/e2e/fakehttp) stands in for an ntfy topic: [notify] posts land in $NTFY_LOG
# as "<Title>|<Priority>|<Authorization>|<body>" lines; it reports its port once it listens.
R0=$(cd "$(dirname "$0")/../.." && pwd)
FH=$(mktemp -d)
go build -C "$R0" -o "$FH/fakehttp" ./scripts/e2e/fakehttp
NTFY_LOG=$FH/ntfy.log
"$FH/fakehttp" -mode ntfy -log "$NTFY_LOG" -portfile "$FH/port" &
NTFY_PID=$!
for _ in $(seq 1 100); do [ -s "$FH/port" ] && break; sleep 0.1; done
NTFY_PORT=$(cat "$FH/port")
E2E_EXTRA_CONFIG=$(printf '[notify]\nurl = "http://127.0.0.1:%s/flok"\ntoken = "t0k"\n[link]\nname = "e2e"\n[notes]\ncommand = "auto"\n' "$NTFY_PORT")
export E2E_EXTRA_CONFIG
source "$(dirname "$0")/lib.sh"
# a stand-in nt on PATH ([notes] command = auto): nt agent import appends each record to $NT_LOG
NT_LOG=$FH/nt-imports.log
cat > "$FAKE/nt" <<NT
#!/bin/sh
[ "\$1" = agent ] && [ "\$2" = import ] || exit 3
[ "\$3" = --probe ] && { echo 1; exit 0; }
cat >> "$NT_LOG"; echo >> "$NT_LOG"
NT
chmod +x "$FAKE/nt"

# Hooks outside tmux are ignored and write nothing.
printf '{"hook_event_name":"Stop"}' | TMUX_PANE= "$BIN" hook claude
expect "hook without TMUX_PANE writes no record" '^0$' "$(ls "$T/state/agents" | grep -c '\.json$' || true)"

t0=$(python3 -c 'import time;print(time.time())')
hook claude '{"hook_event_name":"SessionStart","source":"startup","session_id":"abc","cwd":"'"$R"'"}'
t1=$(python3 -c 'import time;print(time.time())')
ms=$(python3 -c "print(int(($t1-$t0)*1000))")
budget=80; [ -n "${CI:-}" ] && budget=250   # shared CI runners start processes slowly (two python3 launches sit inside the measurement)
expect "hook round trip < ${budget} ms (took ${ms} ms)" '^ok$' "$([ "$ms" -lt "$budget" ] && echo ok || echo slow)"
wait_for "○ $PROJ *$" 3 || true
expect "hooked agent shows without ~" "○ $PROJ *$" "$(capture)"
expect_soon "the session's start is imported into nt (version, agent slug, session, cwd, the git head)" '"version":1,"agent":"claude-code","session":"abc","host":"","cwd":"'"$R"'",("branch":"[^"]+",)?"head":"[0-9a-f]+".*"state":"idle"' cat "$NT_LOG"
expect "the journal is kept under sessions/" '^1$' "$(ls "$T/state/sessions" | grep -c '^abc\.json$' || true)"

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
expect_soon "the permission request is pushed to the phone (ntfy form)" "^e2e · $PROJ[|]high[|]Bearer t0k[|]needs you: perm:Bash\$" cat "$NTFY_LOG"
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
hook claude '{"hook_event_name":"Stop","session_id":"abc","stop_hook_active":false,"last_assistant_message":"## Done\n\n- **Tests green.** Nothing else changed."}'
expect_soon "the stop is imported: the turn's text (not its heading), its tools and the block it hit" '"state":"done","turns":1,"tools":1,"blocked_s":[0-9]+,.*"text":"Tests green. Nothing else changed.","tools":."Bash":1.,"events":..*"kind":"blocked","text":"permission: Bash"' cat "$NT_LOG"
expect "the start, the block and the stop imported; the tool calls alone did not (three imports)" '^3$' "$(grep -c '"version":1' "$NT_LOG")"
hook claude '{"hook_event_name":"Notification","session_id":"abc","notification_type":"agent_completed"}'
wait_for 'done · 2' 3 || true
snap=$(capture); echo "--- done ---"; printf '%s\n' "$snap" | grep -v '^ *$' | sed -n '4,6p'
expect "stop while unfocused -> done · 2 (blocked + done)" "✓ $PROJ +done · 2" "$snap"
expect_soon "... and the finish is pushed too" "^e2e · $PROJ[|]default[|]Bearer t0k[|]finished\$" cat "$NTFY_LOG"
expect "two pushes so far, nothing for the working turn" '^2$' "$(wc -l < "$NTFY_LOG" | tr -d ' ')"
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
expect "stop while focused -> idle, nothing unseen" '^agents *$' "$(capture)"

# Background subagents (client is looking at this pane): the main turn ends (Stop) while tasks are
# in flight -> paused, a still ◍ with "bg N agents"; idle_prompt does not end it; the Stop after
# the wake-up turn does. Focused, so it ends idle with nothing unseen, leaving the state as it was.
hook claude '{"hook_event_name":"UserPromptSubmit","session_id":"abc"}'
hook claude '{"hook_event_name":"PreToolUse","session_id":"abc","tool_name":"Agent","tool_input":{"description":"explore","run_in_background":true},"tool_use_id":"a1"}'
hook claude '{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"Agent","tool_use_id":"a1"}'
hook claude '{"hook_event_name":"Stop","session_id":"abc","background_tasks":[{"id":"t1","type":"subagent","status":"running","agent_type":"Explore"},{"id":"t2","type":"subagent","status":"running","agent_type":"Plan"}]}'
wait_for '2 agents' 3 || true
snap=$(capture); echo "--- waiting for subagents ---"; printf '%s\n' "$snap" | grep -v '^ *$' | sed -n '4,6p'
expect "waiting for subagents shows as paused with the count" "◍ $PROJ +bg 2 agents" "$snap"
hook claude '{"hook_event_name":"Notification","session_id":"abc","notification_type":"idle_prompt"}'
sleep 0.6
expect "idle_prompt does not end the paused turn" "◍ $PROJ +bg 2 agents" "$(capture)"
hook claude '{"hook_event_name":"PreToolUse","session_id":"abc","tool_name":"Read","tool_input":{"file_path":"/x/y.go"},"tool_use_id":"r1"}'
hook claude '{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"Read","tool_use_id":"r1"}'
hook claude '{"hook_event_name":"Stop","session_id":"abc","background_tasks":[]}'
wait_for "○ $PROJ" 3 || true
expect "final Stop with nothing in flight ends the turn" "○ $PROJ" "$(capture)"
expect "nothing unseen after a watched wait" '^agents *$' "$(capture)"

# A monitor is a watch, not work that ends: the comments on an artifact the agent published, a PR
# watch, a log tail. Beside a shell the shell pauses the turn; alone it ends it (Claude's own
# registry calls the session idle), the watch only named in the events log.
hook claude '{"hook_event_name":"UserPromptSubmit","session_id":"abc"}'
hook claude '{"hook_event_name":"Stop","session_id":"abc","background_tasks":[{"id":"m1","type":"monitor","status":"running","description":"Comments on artifact Plan"},{"id":"s1","type":"shell","status":"running","description":"Wait for CI","command":"gh pr checks --watch"}]}'
wait_for 'bg shell' 3 || true
expect "a shell beside a monitor pauses the turn" "◍ $PROJ +bg shell" "$(capture)"
hook claude '{"hook_event_name":"Stop","session_id":"abc","background_tasks":[{"id":"m1","type":"monitor","status":"running","description":"Comments on artifact Plan"}]}'
wait_for "○ $PROJ" 3 || true
expect "a monitor alone does not pause the turn" "○ $PROJ" "$(capture)"
expect "nothing unseen after a stop behind a watch" '^agents *$' "$(capture)"
expect "the events log names the watch the turn ended behind" '"detail":"watch: Comments on artifact Plan".*"kind":"stop"' "$(tail -n 3 "$T/state/events.log")"

# AskUserQuestion is a block with reason question.
OUT send-keys -t "$SIDEBAR" '!'   # look at Alpha window 0 again? '!' selects space 1 = Alpha (same session) -> still focused on agent pane
IN select-window -t Alpha:0        # move the inner client away from the agent pane
sleep 0.4
hook claude '{"hook_event_name":"UserPromptSubmit","session_id":"abc"}'
hook claude '{"hook_event_name":"PreToolUse","session_id":"abc","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which fruits do you like?","header":"Fruit","options":[{"label":"Apple","description":"red or green"},{"label":"Banana"},{"label":"Cherry"}],"multiSelect":true},{"question":"Which size?","header":"Size","options":[{"label":"Small"},{"label":"Large"}],"multiSelect":false}]},"tool_use_id":"q1"}'
wait_for 'question' 3 || true
expect "AskUserQuestion -> question" "● $PROJ +question" "$(capture)"
# the form the hook saw rides in the record and the published snapshot, for the phone to render
snap_form() { python3 -c "
import json
a=[a for a in json.load(open('$T/state/snapshot.json'))['agents'] if a['pane_id']=='$AGENT'][0]
f=a.get('form')
print('none' if not f else ' '.join(q['header']+'/'+str(len(q['options']))+('/multi' if q.get('multi') else '') for q in f['questions']))" 2>/dev/null || echo unread; }
expect_soon "the question's form is in the snapshot" '^Fruit/3/multi Size/2$' snap_form
# Claude Code announces the form a second time as a permission request for the tool (no
# tool_use_id) and as a permission notification: both stay the question, with its form
hook claude '{"hook_event_name":"PermissionRequest","session_id":"abc","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which fruits do you like?","header":"Fruit","options":[{"label":"Apple"},{"label":"Banana"},{"label":"Cherry"}],"multiSelect":true},{"question":"Which size?","header":"Size","options":[{"label":"Small"},{"label":"Large"}]}]}}'
hook claude '{"hook_event_name":"Notification","session_id":"abc","notification_type":"permission_prompt","message":"Claude needs your permission"}'
sleep 0.6
expect "the permission request for the form is still the question" "● $PROJ +question" "$(capture)"
expect "and the form is still there" '^Fruit/3/multi Size/2$' "$(snap_form)"

# The block sticks while the title is idle (that is what a real prompt looks like) ...
sleep 1.5
expect "question sticks despite idle title" "● $PROJ +question" "$(capture)"
# ... and clears through hook events only (answer -> PostToolUse -> working).
IN select-pane -t "$AGENT" -T "◑ fake-agent"
hook claude '{"hook_event_name":"PostToolUse","session_id":"abc","tool_name":"AskUserQuestion","tool_use_id":"q1"}'
wait_for "[◐◓◑◒] $PROJ" 3 || true
expect "answered question -> working" "[◐◓◑◒] $PROJ" "$(capture)"
expect_soon "the form goes with the question" '^none$' snap_form
# Inside tmux Claude keeps the idle "✳" title while busy: that must NOT clear a hook working state.
IN select-pane -t "$AGENT" -T "✳ fake-agent"
sleep 2.5
expect "idle title does not clear hook working" "[◐◓◑◒] ($PROJ|fake-agent)" "$(capture)"
# Interrupted turn (Esc emits no hook): Claude's registry turns idle -> idle after two samples, no sound, nothing new unseen.
AGENT_PID=$(IN display -p -t "$AGENT" '#{pane_pid}')
registry "[{\"pid\":$AGENT_PID,\"status\":\"idle\",\"kind\":\"interactive\",\"name\":\"fake\",\"sessionId\":\"abc\"}]"
wait_for "○ ($PROJ|fake-agent)" 6 || true
snap=$(capture)
expect "registry idle x2 -> interrupted turn shows idle" "○ ($PROJ|fake-agent)" "$snap"
expect "interrupted turn leaves only the earlier block unseen" '^agents · 1' "$snap"
expect "correction persisted into the hook record" '"state": "idle"' "$(cat "$T"/state/agents/*.json)"
expect "correction reason recorded" 'corrected:registry idle' "$(cat "$T"/state/agents/*.json)"
registry '[]'
sleep 6   # registry gone again: a persisted correction must not flap back to working
expect "corrected state does not flap back" "○ ($PROJ|fake-agent)" "$(capture)"

hook claude '{"hook_event_name":"SessionEnd","session_id":"abc","reason":"prompt_input_exit"}'
wait_for "~$PROJ" 3 || true
expect "session end deletes the record (back to title-only ~)" "~$PROJ" "$(capture)"
expect "record file removed" '^0$' "$(ls "$T/state/agents" | grep -c '\.json$' || true)"
kill "$NTFY_PID" 2>/dev/null || true; rm -rf "$FH"
finish
