#!/usr/bin/env bash
# M5: launcher lifecycle — detach closes the outer, a killed session re-attaches elsewhere,
# reattach_on_detach=true re-attaches on detach.
source "$(dirname "$0")/lib.sh"
CLIENT=$(IN list-clients -F '#{client_tty}' | head -1)
SESS=$(IN list-clients -F '#{client_session}' | head -1)

# the sidebar width is pinned: window resizes (window manager moving the terminal) must not scale it
OUT resize-window -t flok -x 120 -y 40; sleep 0.4
OUT resize-window -t flok -x 320 -y 80; sleep 0.4
expect "sidebar stays 28 columns after window resizes" '^28$' "$(OUT display -p -t "$SIDEBAR" '#{pane_width}')"
expect "sidebar is still the left pane" '^0$' "$(OUT display -p -t "$SIDEBAR" '#{pane_left}')"
"$BIN" toggle; sleep 0.4
OUT resize-window -t flok -x 200 -y 60; sleep 0.4
expect "rail width is pinned too" '^6$' "$(OUT display -p -t "$SIDEBAR" '#{pane_width}')"
"$BIN" toggle; sleep 0.4
expect "toggle back to full" '^28$' "$(OUT display -p -t "$SIDEBAR" '#{pane_width}')"
"$BIN" hide; sleep 0.4
OUT resize-window -t flok -x 260 -y 70; sleep 0.4
expect "resize while hidden keeps it hidden" '^1$' "$(OUT display -p -t "$RIGHT" '#{window_zoomed_flag}')"
"$BIN" hide; sleep 0.4
expect "unhide restores the pinned width" '^28$' "$(OUT display -p -t "$SIDEBAR" '#{pane_width}')"
OTHER=Alpha; [ "$SESS" = Alpha ] && OTHER=Beta

IN kill-session -t "$SESS"; sleep 1.2
if OUT has-session -t flok 2>/dev/null; then ok "outer survives killing the attached session"; else bad "outer died with the session"; fi
expect "client re-attached to the remaining session" "^$OTHER\$" "$(IN list-clients -F '#{client_session}' | head -1)"
expect "sidebar still alive" '^sessions' "$(capture)"

IN detach-client -t "$(IN list-clients -F '#{client_tty}' | head -1)"; sleep 1.2
if OUT has-session -t flok 2>/dev/null; then bad "outer still running after an explicit detach"; else ok "explicit detach closes the outer"; fi
if IN ls >/dev/null 2>&1; then ok "inner server untouched"; else bad "inner server died"; fi
expect "runtime.json removed" '^0$' "$(ls "$T/state" | grep -cx runtime.json || true)"

# reattach_on_detach = true: a detach comes straight back
python3 - "$T/config.toml" <<'PY'
import sys; p=sys.argv[1]; s=open(p).read()
s=s.replace('socket = "e2e-inner"', 'socket = "e2e-inner"\nreattach_on_detach = true', 1); open(p,'w').write(s)
PY
"$BIN" up --detach; sleep 1.5
SIDEBAR=$(python3 -c "import json;print(json.load(open('$T/state/runtime.json'))['sidebar_pane'])")
IN detach-client -t "$(IN list-clients -F '#{client_tty}' | head -1)"; sleep 1.2
if OUT has-session -t flok 2>/dev/null; then ok "outer kept with reattach_on_detach=true"; else bad "outer closed despite reattach_on_detach=true"; fi
expect "client re-attached after detach" '^1$' "$(IN list-clients | wc -l | tr -d ' ')"
"$BIN" down; sleep 0.5
if OUT has-session -t flok 2>/dev/null; then bad "down left the outer running"; else ok "down closes the outer"; fi

# `flok up` run interactively (in a pane of a third isolated server, which provides the tty)
# must exit 0 after a deliberate detach and after `flok down`, so launcher fallbacks stay quiet.
TTY() { tmux -L e2e-tty "$@"; }
TTY kill-server 2>/dev/null || true
sed -i '' '/reattach_on_detach = true/d' "$T/config.toml"   # back to the default: detach tears the outer down
TTY -f /dev/null new-session -d -s t -x 120 -y 40 "env -u TMUX -u TMUX_PANE FLOK_CONFIG=$FLOK_CONFIG FLOK_STATE=$FLOK_STATE $BIN up; echo UP_EXIT=\$?; sleep 20"
for _ in $(seq 1 30); do OUT has-session -t flok 2>/dev/null && break; sleep 0.2; done
sleep 0.8
IN detach-client -t "$(IN list-clients -F '#{client_tty}' | head -1)"; sleep 1.5
expect "flok up exits 0 after a deliberate detach" 'UP_EXIT=0' "$(TTY capture-pane -p -t t)"
TTY kill-server
TTY -f /dev/null new-session -d -s t -x 120 -y 40 "env -u TMUX -u TMUX_PANE FLOK_CONFIG=$FLOK_CONFIG FLOK_STATE=$FLOK_STATE $BIN up; echo UP_EXIT=\$?; sleep 20"
for _ in $(seq 1 30); do OUT has-session -t flok 2>/dev/null && break; sleep 0.2; done
sleep 0.8
"$BIN" down; sleep 1.5
expect "flok up exits 0 after flok down" 'UP_EXIT=0' "$(TTY capture-pane -p -t t)"
TTY kill-server 2>/dev/null || true
finish
