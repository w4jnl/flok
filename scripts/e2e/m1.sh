#!/usr/bin/env bash
# M1: read-only sidebar driven by pane titles.
source "$(dirname "$0")/lib.sh"

snap=$(capture)
echo "--- sidebar (idle) ---"; printf '%s\n' "$snap" | grep -v '^ *$' | head -8
expect "spaces header"            '^sessions'              "$snap"
expect "both sessions listed"     "Alpha.*$REPO_BRANCH"  "$snap"
expect "second session listed"    'Beta'                 "$snap"
expect "agents header"            '^agents'              "$snap"
expect "fake claude agent idle (~ = no hooks)" "○ ~$PROJ *$" "$snap"
expect "second line: kind · title"     'claude · fake-agent' "$snap"
expect "no shell pane listed as agent" '^0$' "$(printf '%s\n' "$snap" | grep -c 'zsh\|bash' || true)"

IN select-pane -t "$AGENT" -T "◑ fake-agent"
wait_for "[◐◓◑◒] ~$PROJ" 3 || true
snap=$(capture)
expect "working spinner + elapsed" "[◐◓◑◒] ~$PROJ +0:0[0-9]" "$snap"
expect "space rollup working"      '[◐◓◑◒] Alpha' "$snap"

IN select-pane -t "$AGENT" -T "✳ fake-agent"
wait_for 'done · 1' 3 || true
snap=$(capture)
expect "done with unseen count"    "✓ ~$PROJ +done · 1" "$snap"
expect "agents header unseen"      '^agents · 1' "$snap"

OUT send-keys -t "$SIDEBAR" Tab Enter
sleep 0.8
expect "inner client switched to Alpha" '^Alpha$' "$(IN list-clients -F '#{client_session}')"
expect "agent window selected"          '^agent$' "$(IN display -p -t Alpha '#{window_name}')"
expect "outer focus back on right pane" "^$RIGHT\$" "$(OUT display -p -t flok '#{pane_id}')"
wait_for "○ ~$PROJ *$" 3 || true
expect "seen -> idle again" "○ ~$PROJ *$" "$(capture)"

OUT send-keys -t "$SIDEBAR" '@'
sleep 0.8
expect "space hotkey switched to Beta" '^Beta$' "$(IN list-clients -F '#{client_session}')"

OUT resize-pane -t "$SIDEBAR" -x 6
sleep 0.8
snap=$(capture); echo "--- rail ---"; printf '%s\n' "$snap" | grep -v '^ *$' | head -5
expect "rail circled spaces" '①' "$snap"
expect "rail separator"      '─' "$snap"
OUT send-keys -t "$SIDEBAR" '!'; sleep 0.6      # a space hotkey parks the cursor in the spaces panel
expect "rail shows the cursor in the spaces panel" '^›\s*[①②]' "$(capture)"
OUT send-keys -t "$SIDEBAR" G; sleep 0.5
expect "rail cursor moves to the last space with G" '^›\s*②' "$(capture)"
OUT send-keys -t "$SIDEBAR" g; sleep 0.5
expect "rail cursor moves to the first space with g" '^›\s*①' "$(capture)"
OUT send-keys -t "$SIDEBAR" Tab; sleep 0.5
expect "rail cursor moves to agents with Tab" '^› *1 ' "$(capture)"
OUT send-keys -t "$SIDEBAR" Enter; sleep 0.8
expect "rail enter opens the agent pane" '^agent$' "$(IN display -p -t Alpha '#{window_name}')"

"$BIN" down
sleep 0.3
if OUT ls >/dev/null 2>&1; then bad "outer still running after down"; else ok "down killed the outer"; fi
if IN ls >/dev/null 2>&1; then ok "inner survives down"; else bad "inner died"; fi
finish
