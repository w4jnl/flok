#!/usr/bin/env bash
# M9: keep-awake. On macOS the sidebar holds the IOKit power assertions while the keep-awake marker
# is on (checked in `pmset -g assertions` against the sidebar's pid); they go with the session.
# Elsewhere the command says it is macOS-only.
source "$(dirname "$0")/lib.sh"
SNAP=$T/state/snapshot.json
for _ in $(seq 1 30); do [ -f "$SNAP" ] && break; sleep 0.1; done

if [ "$(uname -s)" != Darwin ]; then
  rc=0; out=$("$BIN" keep-awake on 2>&1) || rc=$?
  expect "keep-awake is macOS only here" 'macOS only' "$out"
  expect "keep-awake exits 1 off macOS" '^1$' "$rc"
  expect "status does not mention keep-awake" '^0$' "$("$BIN" status | grep -c "^keep-awake:" || true)"
  finish; exit
fi

sidebar_pid() { python3 -c "import json;print(json.load(open('$SNAP'))['sidebar_pid'])" 2>/dev/null || echo 0; }
# held <pid>: the assertion types flok's sidebar holds, sorted, on one line
held() { echo $(pmset -g assertions | grep "pid $1(" | grep 'named: "flok keep-awake"' | grep -oE 'PreventUserIdle(Display|System)Sleep' | sort -u || true); }
gone_after() { # pid, seconds: wait until the pid holds nothing
  local i; for i in $(seq 1 $(( $2 * 10 ))); do [ -z "$(held "$1")" ] && return 0; sleep 0.1; done; return 1; }
BOTH='PreventUserIdleDisplaySleep PreventUserIdleSystemSleep'

expect "keep-awake is off by default" '^keep-awake: off$' "$("$BIN" keep-awake status)"
PID=$(sidebar_pid)
expect "no assertion before keep-awake on" '^$' "$(held "$PID")"

expect "keep-awake on confirms" '^keep-awake: on' "$("$BIN" keep-awake on)"
expect "the sidebar holds both assertions" "^$BOTH\$" "$(held "$PID")"
expect "snapshot.json says keep_awake" '"keep_awake": true' "$(cat "$SNAP")"
expect "status shows keep-awake while on" '^keep-awake: on \(display and idle sleep blocked\)$' "$("$BIN" status)"
expect "status --json has KeepAwake" '"KeepAwake": true' "$("$BIN" status --json)"
expect "on again is a no-op" '^keep-awake: on' "$("$BIN" keep-awake on)"
expect "doctor reports keep-awake on" 'ok +keep-awake: on' "$("$BIN" doctor || true)"

expect "bare keep-awake toggles off" '^keep-awake: off$' "$("$BIN" keep-awake)"
if gone_after "$PID" 2; then ok "off releases the assertions"; else bad "assertions left after off: $(held "$PID")"; fi
expect "status drops the keep-awake line" '^0$' "$("$BIN" status | grep -c "^keep-awake:" || true)"
expect "status --json drops KeepAwake" '^0$' "$("$BIN" status --json | grep -c KeepAwake || true)"
expect "keep-awake toggle turns it on" '^keep-awake: on' "$("$BIN" keep-awake toggle)"

# reload restarts the sidebar: the new one takes the assertions again from the marker
"$BIN" reload
for _ in $(seq 1 50); do NEW=$(sidebar_pid); [ "$NEW" != "$PID" ] && [ "$NEW" != 0 ] && [ -n "$(held "$NEW")" ] && break; sleep 0.1; done
expect "a reloaded sidebar holds keep-awake again" "^$BOTH\$" "$(held "$NEW")"
if gone_after "$PID" 2; then ok "the old sidebar's assertions went with it"; else bad "old sidebar still holds: $(held "$PID")"; fi
PID=$NEW

# flok down ends keep-awake with the session, and the next session starts off
"$BIN" down
if gone_after "$PID" 3; then ok "flok down releases keep-awake"; else bad "assertions left after down: $(held "$PID")"; fi
expect "keep-awake needs a running flok" 'start it with flok up' "$("$BIN" keep-awake on 2>&1 || true)"
"$BIN" up --detach; sleep 1.5
for _ in $(seq 1 30); do [ -f "$SNAP" ] && break; sleep 0.1; done
expect "a new session starts with keep-awake off" '^keep-awake: off$' "$("$BIN" keep-awake status)"

# the outer server dying (no teardown) takes the assertions with the sidebar
expect "keep-awake on in the new session" '^keep-awake: on' "$("$BIN" keep-awake on)"
PID=$(sidebar_pid)
OUT kill-server
if gone_after "$PID" 3; then ok "killing the outer server releases keep-awake"; else bad "assertions survive the outer: $(held "$PID")"; fi
expect "keep-awake status says flok is not running once the sidebar died" '^keep-awake: off \(flok is not running\)$' "$("$BIN" keep-awake status)"
finish
