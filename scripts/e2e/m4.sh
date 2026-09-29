#!/usr/bin/env bash
# M4: screen-rule detection for hook-less agents (Copilot, and Claude without hooks).
source "$(dirname "$0")/lib.sh"
FIX=$R/internal/rules/testdata
go build -o "$FAKE/copilot" "$R/scripts/e2e/fakeagent"

# Copilot pane in Beta painted from a file
SCR=$T/copilot.txt; paint "$SCR" "$FIX/copilot_selection.txt"
IN new-window -t Beta -n cop -c "$T"
COP=$(IN display -p -t Beta:cop '#{pane_id}')
IN send-keys -t "$COP" "exec $FAKE/copilot $SCR" Enter
IN select-window -t Beta:0
wait_for 'prompt' 6 || true
snap=$(capture); echo "--- copilot blocked ---"; printf '%s\n' "$snap" | grep -v '^ *$' | sed -n '4,7p'
expect "copilot blocked via screen rules" '● ~\S+ +prompt' "$snap"

paint "$SCR" "$FIX/copilot_working.txt"
wait_for '[◐◓◑◒] ~' 6 || true
expect "copilot working via screen rules" '[◐◓◑◒] ~\S+' "$(capture)"

printf '$ \n' | paint "$SCR" -
wait_for 'done · 1' 6 || true
expect "nothing visible -> idle fallback; unfocused working->idle = done" '✓ ~\S+ +done · 1' "$(capture)"

# Claude without hooks: prompt box = idle, permission dialog = blocked, model picker = hold
# its own cwd, so its row (~clproj) is told apart from lib.sh's always-idle agent in ~$PROJ
CL=$T/claude.txt; paint "$CL" "$FIX/claude_prompt_box.txt"
mkdir -p "$T/clproj"
IN new-window -t Alpha -n cl -c "$T/clproj"
CLP=$(IN display -p -t Alpha:cl '#{pane_id}')
IN select-pane -t "$CLP" -T "✳ cl"
IN send-keys -t "$CLP" "exec $FAKE/claude $CL" Enter
IN select-window -t Alpha:0
expect_soon -t 6 "claude prompt box -> idle" '○ ~clproj *$' capture

paint "$CL" "$FIX/claude_permission_bash.txt"
expect_soon -t 6 "claude permission dialog -> blocked (prompt)" '● ~clproj +prompt' capture
ex=$("$BIN" explain "$CLP")
expect "explain names the winning rule" '\* bash_permission_prompt' "$ex"

paint "$CL" "$FIX/claude_model_picker.txt"
expect_soon -t 6 "explain shows the hold rule" 'model_picker_menu.*\(hold\)' "$BIN" explain "$CLP"   # the fixture is on screen
sleep 1.2   # two screen samples at 500 ms: the hold rule had its say
expect "model picker holds the previous state" '● ~clproj +prompt' "$(capture)"

paint "$CL" "$FIX/claude_prompt_box.txt"
expect_soon -t 6 "dialog gone -> idle, no done (was blocked, not working)" '○ ~clproj *$' capture

expect "explain with no args lists agent panes" "$CLP" "$("$BIN" explain)"
finish
