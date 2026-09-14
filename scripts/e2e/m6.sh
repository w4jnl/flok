#!/usr/bin/env bash
# M6: the snapshot the sidebar publishes for flok-bar, and `flok goto`. The bar process itself
# is only exercised with FLOK_E2E_BAR=1 (it shows a menu bar item for a few seconds).
source "$(dirname "$0")/lib.sh"
SNAP=$T/state/snapshot.json
for _ in $(seq 1 30); do [ -f "$SNAP" ] && break; sleep 0.1; done
expect "sidebar publishes snapshot.json" 'fake-agent|flok' "$(cat "$SNAP" 2>/dev/null)"
py() { python3 -c "import json,sys; s=json.load(open('$SNAP')); $1"; }
expect "snapshot lists the fake agent pane" "^$AGENT\$" "$(py 'print(next(a["pane_id"] for a in s["agents"]))')"
expect "snapshot session list has Alpha and Beta" 'Alpha Beta' "$(py 'print(" ".join(sorted(x["name"] for x in s["sessions"])))')"

# hook-driven blocked state shows up in the snapshot with unseen count, and status agrees
hook claude '{"hook_event_name":"SessionStart","source":"startup","session_id":"abc","cwd":"/p"}'
hook claude '{"hook_event_name":"UserPromptSubmit","session_id":"abc"}'
hook claude '{"hook_event_name":"PermissionRequest","session_id":"abc","tool_name":"Bash","tool_input":{"command":"x"},"tool_use_id":"t1"}'
for _ in $(seq 1 30); do grep -q '"blocked"' "$SNAP" 2>/dev/null && break; sleep 0.1; done
expect "snapshot state blocked" '^blocked permission:Bash 1$' "$(py 'a=s["agents"][0]; print(a["state"], a["reason"], a["unseen"])')"
expect "status agrees with the snapshot" 'blocked' "$("$BIN" status | grep "$AGENT")"
expect "snapshot is fresh (written recently)" '^ok$' "$(python3 -c "import os,time; d=time.time()-os.path.getmtime('$SNAP'); print('ok' if d<10 else d)")"

# goto: switch the inner client to the agent pane (no window focus in tests) and mark it seen
IN select-window -t Alpha:0
"$BIN" goto "$AGENT" --no-focus
sleep 0.6
expect "goto selects the agent window" '^Alpha agent$' "$(IN display -p -t "$(IN list-clients -F '#{client_tty}' | head -1)" '#{session_name} #{window_name}')"
expect "goto marks the pane seen" 'seen_at' "$(cat "$T"/state/seen/*.json 2>/dev/null)"
expect "goto rejects garbage" 'unexpected argument' "$("$BIN" goto nonsense 2>&1 || true)"

# edit-config with [bar] editor: a new window in the client's session runs the editor on the file
printf '\n[bar]\neditor = "tail -f"\n' >> "$T/config.toml"
"$BIN" edit-config --no-focus; sleep 0.8
CSESS=$(IN list-clients -F '#{client_session}' | head -1)
expect "edit-config opened a flok-config window in the client's session" 'flok-config' "$(IN list-windows -t "$CSESS" -F '#{window_name}')"
expect "the editor runs on config.toml" 'tail' "$(IN display -p -t "$CSESS:flok-config" '#{pane_current_command}')"
IN kill-window -t "$CSESS:flok-config"
sed -i.bak -e '/^\[bar\]$/d' -e '/^editor = "tail -f"$/d' "$T/config.toml" && rm -f "$T/config.toml.bak"   # -i.bak: BSD and GNU sed

if [ -n "${FLOK_E2E_BAR:-}" ]; then
  python3 - "$T/config.toml" <<'PY'
import sys; p=sys.argv[1]; s=open(p).read(); s+='\n[bar]\nenabled = true\nfocus = "none"\n'; open(p,'w').write(s)
PY
  "$BIN" down; sleep 0.5
  FLOK_BAR_GONE_AFTER=3 "$BIN" up --detach; sleep 2
  pid=$(cat "$T/state/flok-bar.pid" 2>/dev/null || echo 0)
  if [ "$pid" -gt 0 ] && kill -0 "$pid" 2>/dev/null; then ok "flok up started flok-bar (pid $pid)"; else bad "flok-bar not started by up"; fi
  "$BIN" down; sleep 1
  if [ "$pid" -gt 0 ] && kill -0 "$pid" 2>/dev/null; then bad "flok down left flok-bar running"; kill "$pid"; else ok "flok down stopped flok-bar"; fi
fi
finish
