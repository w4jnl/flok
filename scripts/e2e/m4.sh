#!/usr/bin/env bash
# M4: screen-rule detection for hook-less agents (Copilot, and Claude without hooks).
source "$(dirname "$0")/lib.sh"
FIX=$R/internal/rules/testdata
go build -o "$FAKE/copilot" "$R/scripts/e2e/fakeagent"

# Copilot pane in Beta painted from a file
SCR=$T/copilot.txt; cp "$FIX/copilot_selection.txt" "$SCR"
IN new-window -t Beta -n cop -c "$T"
COP=$(IN display -p -t Beta:cop '#{pane_id}')
IN send-keys -t "$COP" "exec $FAKE/copilot $SCR" Enter
IN select-window -t Beta:0
wait_for 'prompt' 6 || true
snap=$(capture); echo "--- copilot blocked ---"; printf '%s\n' "$snap" | grep -v '^ *$' | sed -n '4,7p'
expect "copilot blocked via screen rules" '● ~\S+ +prompt' "$snap"

cp "$FIX/copilot_working.txt" "$SCR"
wait_for '[◐◓◑◒] ~' 6 || true
expect "copilot working via screen rules" '[◐◓◑◒] ~\S+' "$(capture)"

printf '$ \n' > "$SCR"
wait_for 'done · 1' 6 || true
expect "nothing visible -> idle fallback; unfocused working->idle = done" '✓ ~\S+ +done · 1' "$(capture)"

# Claude without hooks: prompt box = idle, permission dialog = blocked, model picker = hold
CL=$T/claude.txt; cp "$FIX/claude_prompt_box.txt" "$CL"
IN new-window -t Alpha -n cl -c "$R"
CLP=$(IN display -p -t Alpha:cl '#{pane_id}')
IN select-pane -t "$CLP" -T "✳ cl"
IN send-keys -t "$CLP" "exec $FAKE/claude $CL" Enter
IN select-window -t Alpha:0
wait_for "○ ~$PROJ *$" 6 || true
expect "claude prompt box -> idle" "○ ~$PROJ *$" "$(capture)"

cp "$FIX/claude_permission_bash.txt" "$CL"
wait_for "● ~$PROJ +prompt" 6 || true
expect "claude permission dialog -> blocked (prompt)" "● ~$PROJ +prompt" "$(capture)"
ex=$("$BIN" explain "$CLP")
expect "explain names the winning rule" '\* bash_permission_prompt' "$ex"

cp "$FIX/claude_model_picker.txt" "$CL"
sleep 1.5
expect "model picker holds the previous state" "● ~$PROJ +prompt" "$(capture)"
expect "explain shows the hold rule" 'model_picker_menu.*\(hold\)' "$("$BIN" explain "$CLP")"

cp "$FIX/claude_prompt_box.txt" "$CL"
wait_for "○ ~$PROJ *$" 6 || true
expect "dialog gone -> idle, no done (was blocked, not working)" "○ ~$PROJ *$" "$(capture)"

expect "explain with no args lists agent panes" "$CLP" "$("$BIN" explain)"
finish
