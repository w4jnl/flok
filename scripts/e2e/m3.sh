#!/usr/bin/env bash
# M3: nav commands, toggle/hide, keybinds help.
source "$(dirname "$0")/lib.sh"
CLIENT=$(IN list-clients -F '#{client_tty}' | head -1)

# second agent in Beta so next/prev have somewhere to go
IN new-window -t Beta -n agent2 -c "$T"
AGENT2=$(IN display -p -t Beta:agent2 '#{pane_id}')
PROJ2=$(basename "$T")   # agent2 runs in $T, so that is its project label
IN select-pane -t "$AGENT2" -T "✳ second"
IN send-keys -t "$AGENT2" "exec $FAKE/claude" Enter
IN select-window -t Beta:0
sleep 0.5

# jump: nothing pending -> message only, client stays where it is
before=$(IN list-clients -F '#{client_session}')
"$BIN" jump --client "$CLIENT"
expect "jump with nothing pending keeps the client" "^$before\$" "$(IN list-clients -F '#{client_session}')"

# make agent 2 done (unfocused) via hooks, then jump lands there
printf '{"hook_event_name":"SessionStart","source":"startup","session_id":"z"}' | TMUX="$INNER_SOCK,0,0" TMUX_PANE="$AGENT2" "$BIN" hook claude
printf '{"hook_event_name":"UserPromptSubmit","session_id":"z"}' | TMUX="$INNER_SOCK,0,0" TMUX_PANE="$AGENT2" "$BIN" hook claude
printf '{"hook_event_name":"Stop","session_id":"z"}' | TMUX="$INNER_SOCK,0,0" TMUX_PANE="$AGENT2" "$BIN" hook claude
wait_for 'done · 1' 3 || true
"$BIN" jump --client "$CLIENT"
sleep 0.5
expect "jump switched to Beta"        '^Beta$'   "$(IN list-clients -F '#{client_session}')"
expect "jump selected agent2 window"  '^agent2$' "$(IN display -p -t Beta '#{window_name}')"
wait_for "○ $PROJ2 *$" 3 || true
expect "jump marks the agent seen"    "○ $PROJ2 *$" "$(capture)"
expect "agent2 second line: kind · title" 'claude · second' "$(capture)"

"$BIN" next --client "$CLIENT"; sleep 0.4
expect "next moves to the other agent (Alpha:agent)" '^Alpha agent$' "$(IN display -p -t "$CLIENT" '#{session_name} #{window_name}')"
"$BIN" next --client "$CLIENT"; sleep 0.4
expect "next wraps around to agent2" '^Beta agent2$' "$(IN display -p -t "$CLIENT" '#{session_name} #{window_name}')"
"$BIN" prev --client "$CLIENT"; sleep 0.4
expect "prev goes back" '^Alpha agent$' "$(IN display -p -t "$CLIENT" '#{session_name} #{window_name}')"

# toggle: full -> rail -> full; hide: zoom right pane and back
"$BIN" toggle; sleep 0.3
expect "toggle shrinks to rail width" '^6$' "$(OUT display -p -t "$SIDEBAR" '#{pane_width}')"
wait_for '①' 2 || true
expect "rail rendered after toggle" '①' "$(capture)"
expect "rail agent: index + status glyph" '^ *[0-9]+ [○✓●◐◓◑◒]' "$(capture)"
"$BIN" toggle; sleep 0.3
expect "toggle restores full width" '^28$' "$(OUT display -p -t "$SIDEBAR" '#{pane_width}')"
"$BIN" hide; sleep 0.3
expect "hide zooms the work pane" '^1$' "$(OUT display -p -t "$RIGHT" '#{window_zoomed_flag}')"
expect "hide writes the hidden marker" '^1$' "$(cat "$T/state/sidebar-hidden")"
"$BIN" toggle; sleep 0.3
expect "toggle while hidden unzooms" '^0$' "$(OUT display -p -t "$RIGHT" '#{window_zoomed_flag}')"
expect "un-hide clears the hidden marker" '^0$' "$(cat "$T/state/sidebar-hidden")"
"$BIN" hide; "$BIN" hide; sleep 0.3
expect "hide twice shows again" '^0$' "$(OUT display -p -t "$RIGHT" '#{window_zoomed_flag}')"

# focus toggles; prefix chords typed while the sidebar has focus are replayed into the work pane
"$BIN" focus; sleep 0.3
expect "focus selects the sidebar pane"        "^$SIDEBAR\$" "$(OUT display -p -t flok '#{pane_id}')"
"$BIN" focus; sleep 0.3
expect "focus again returns to the work pane"  "^$RIGHT\$"   "$(OUT display -p -t flok '#{pane_id}')"
"$BIN" focus; sleep 0.3
sess=$(IN list-clients -F '#{client_session}' | head -1)
before=$(IN list-windows -t "$sess" | wc -l | tr -d ' ')
OUT send-keys -t "$SIDEBAR" C-b; sleep 0.3; OUT send-keys -t "$SIDEBAR" c; sleep 0.8   # the isolated inner uses the default prefix C-b
expect "prefix chord from the sidebar reaches the inner (new window)" "^$((before+1))\$" "$(IN list-windows -t "$sess" | wc -l | tr -d ' ')"
expect "chord hands keyboard focus to the work pane" "^$RIGHT\$" "$(OUT display -p -t flok '#{pane_id}')"
IN bind-key b new-window -n viab
"$BIN" focus; sleep 0.3
OUT send-keys -t "$SIDEBAR" C-b; sleep 0.3; OUT send-keys -t "$SIDEBAR" b; sleep 0.8
expect "prefix b from the sidebar runs the inner binding" 'viab' "$(IN list-windows -t "$sess" -F '#{window_name}')"
expect "prefix b keeps the sidebar focused" "^$SIDEBAR\$" "$(OUT display -p -t flok '#{pane_id}')"
"$BIN" focus; sleep 0.3

# keys --print reads the inner server's bindings (stock notes present on the isolated server)
IN bind-key -T root C-Right next-window     # the -f /dev/null server has only mouse keys in root
dump=$("$BIN" keys --print)
expect "keys dump has prefix section"   '^prefix C-b'          "$dump"
expect "keys dump has no-prefix section" '^no prefix'          "$dump"
expect "keys dump has copy-mode-vi"      '^copy-mode-vi'       "$dump"
expect "keys dump uses tmux notes"       'Split window'        "$dump"
expect "keys dump hides mouse rows"      '^0$' "$(printf '%s\n' "$dump" | grep -c Mouse || true)"
dumpf=$("$BIN" keys --print --filter split)
expect "filter narrows to split"         '[Ss]plit'            "$dumpf"
expect "filter drops unrelated rows"     '^0$' "$(printf '%s\n' "$dumpf" | grep -c 'Kill' || true)"

# interactive keys UI rendered inside an inner pane (popups cannot be captured)
IN new-window -d -t Beta -n keysui "FLOK_CONFIG=$FLOK_CONFIG FLOK_STATE=$FLOK_STATE $BIN keys"
sleep 1
KP=$(IN display -p -t Beta:keysui '#{pane_id}')
ui=$(IN capture-pane -p -t "$KP")
expect "keys UI title + badge"     'keybinds.*esc close' "$ui"
expect "keys UI hint line"         'press / to filter'   "$ui"
expect "keys UI shows a binding"   'Split window'        "$ui"
IN send-keys -t "$KP" /kill   # one chunk on purpose: multi-rune input must still work
sleep 0.5
ui=$(IN capture-pane -p -t "$KP")
expect "keys UI filter mode"       'search: kill'        "$ui"
expect "keys UI filtered rows"     'Kill'                "$ui"
IN send-keys -t "$KP" Escape; sleep 0.3; IN send-keys -t "$KP" Escape
sleep 0.5
expect "esc closes the keys UI (pane gone)" '^0$' "$(IN list-panes -a -F '#{pane_id}' | grep -c "^$KP\$" || true)"

# sidebar '?' opens the same overlay in-pane
OUT send-keys -t "$SIDEBAR" '?'
sleep 0.8
snap=$(capture)
expect "sidebar ? shows keybinds overlay" 'keybinds' "$snap"
OUT send-keys -t "$SIDEBAR" Escape
sleep 0.5
expect "esc returns to the sidebar" '^sessions' "$(capture)"
finish
