#!/usr/bin/env bash
# M10: remote hosts, part 1 — the host registry, `flok serve --stdio` over a pipe, and `flok host
# status`, through a fake ssh that runs the "remote" command locally against the isolated servers
# e2e-beta (mode full) and e2e-gamma (mode plain). The sidebar's own use of hosts comes with part 2.
source "$(dirname "$0")/lib.sh"
fake_ssh_setup
fake_host beta
fake_host gamma
wait_file() { local i; for i in $(seq 1 $(( ${3:-5} * 10 ))); do grep -qE "$2" "$1" 2>/dev/null && return 0; sleep 0.1; done; return 1; }

# --- registry ------------------------------------------------------------------------------
expect "host list starts empty" 'no remote hosts' "$("$BIN" host list)"
"$BIN" host add beta beta >/dev/null
"$BIN" host add gamma gamma --mode plain --socket agents >/dev/null
rc=0; "$BIN" host add bad -x >/dev/null 2>&1 || rc=$?
expect "an option-like target is refused" '^2$' "$rc"
rc=0; out=$("$BIN" host add local x 2>&1) || rc=$?
expect "local is reserved" 'names the local server' "$out"
rc=0; "$BIN" host add beta other >/dev/null 2>&1 || rc=$?
expect "a duplicate name is refused" '^1$' "$rc"
expect "list --names in registry order" '^beta gamma$' "$("$BIN" host list --names | tr '\n' ' ' | sed 's/ $//')"
json=$("$BIN" host list --json)
expect "list --json carries the version" '"version": 1' "$json"
expect "gamma is plain with its socket" '"socket": "agents"' "$json"
expect "hosts.json lives in the state dir" '"name": "beta"' "$(cat "$T/state/hosts.json")"
expect "list shows enabled hosts never connected" '^beta +beta +full +yes +never' "$("$BIN" host list)"

# --- flok serve over a pipe, as the local flok runs it on beta -------------------------------
mkfifo "$T/serve-in"
SERVE_OUT=$T/serve-out
( "$FAKE/ssh" -o BatchMode=yes -T -- beta flok serve --stdio < "$T/serve-in" > "$SERVE_OUT" 2> "$T/serve-err" ) &
SERVE_PID=$!
exec 7> "$T/serve-in"
wait_file "$SERVE_OUT" '"type":"hello"' 5 || true
expect "serve greets with protocol 1 and the host's tmux" '"type":"hello","hello":."proto":1,.*"tmux_version":"[0-9]' "$(head -1 "$SERVE_OUT")"
wait_file "$SERVE_OUT" '"type":"snap"' 5 || true
expect "the first snap lists the remote agent" '"type":"snap".*remote-agent' "$(grep -m1 '"type":"snap"' "$SERVE_OUT")"
expect "served record names the ssh client" '"ssh_client": "127.0.0.1' "$(cat "$T/hosts/beta/state/served" 2>/dev/null)"
printf '{"type":"ping"}\n' >&7
wait_file "$SERVE_OUT" '"type":"pong"' 3 || true
expect "ping is answered" '"type":"pong"' "$(cat "$SERVE_OUT")"
printf '{"type":"visible","on":false}\n' >&7
sleep 0.5
expect "visible=false lands in the host's terminal-focus marker" '^0$' "$(cat "$T/hosts/beta/state/terminal-focus" 2>/dev/null)"
# a hook on the host while served: the state travels as a snap, the sound as an event, nothing plays there
rhook beta claude '{"hook_event_name":"SessionStart","source":"startup","session_id":"r1","cwd":"'"$R"'"}'
rhook beta claude '{"hook_event_name":"UserPromptSubmit","session_id":"r1"}'
rhook beta claude '{"hook_event_name":"PermissionRequest","session_id":"r1","tool_name":"Bash","tool_input":{"command":"ls"},"tool_use_id":"t1"}'
wait_file "$SERVE_OUT" '"state":"blocked"' 5 || true
expect "the hook state arrives as a snap update" '"type":"snap".*"state":"blocked"' "$(grep '"type":"snap"' "$SERVE_OUT" | tail -1)"
wait_file "$SERVE_OUT" '"type":"event"' 5 || true
expect "the unsounded notification arrives as an event" '"type":"event","event":."pane":"%[0-9]+","kind":"blocked"' "$(cat "$SERVE_OUT")"
expect "the host's hook played nothing while served" '^missing$' "$([ -e "$T/hosts/beta/played" ] && echo played || echo missing)"
printf '{"type":"goto","goto":{"session":"$1"}}\n' >&7
wait_file "$SERVE_OUT" '"type":"error"' 3 || true
expect "goto without a client on the host is an error frame" '"error":"goto: no inner tmux client' "$(cat "$SERVE_OUT")"
exec 7>&-   # the local side goes away
for _ in $(seq 1 30); do kill -0 "$SERVE_PID" 2>/dev/null || break; sleep 0.1; done
expect "serve exits on EOF" '^gone$' "$(kill -0 "$SERVE_PID" 2>/dev/null && echo alive || echo gone)"
expect "served record removed" '^missing$' "$([ -e "$T/hosts/beta/state/served" ] && echo present || echo missing)"
expect "serve wrote nothing to stderr" '^$' "$(cat "$T/serve-err")"
sleep 2.2   # past the per-pane sound rate limit
rhook beta claude '{"hook_event_name":"PostToolUse","session_id":"r1","tool_name":"Bash","tool_use_id":"t1"}'
rhook beta claude '{"hook_event_name":"PermissionRequest","session_id":"r1","tool_name":"Bash","tool_input":{"command":"pwd"},"tool_use_id":"t2"}'
sleep 0.5
expect "unserved, the host's hook plays again through [sounds] command" 'request\.wav' "$(cat "$T/hosts/beta/played" 2>/dev/null)"

# --- flok host status: one-shot connections through the fake ssh -----------------------------
rc=0; out=$("$BIN" host status 2>&1) || rc=$?
echo "--- host status ---"; printf '%s\n' "$out" | sed 's/^/      | /'
expect "beta answers in full mode with flok and its tmux" '^beta +full +connected +flok [^,]+, tmux [0-9][^ ]* +1( |$)' "$out"
expect "gamma answers in plain mode with its tmux and the title agent" '^gamma +plain +connected +tmux [0-9][^ ]* +1( |$)' "$out"
expect "status exits 0 when every host answers" '^0$' "$rc"
expect "last_connected recorded" '"last_connected"' "$(cat "$T/state/hosts.json")"
expect "gamma has a local store of its own" '^dir$' "$([ -d "$T/state/hosts/gamma/agents" ] && echo dir || echo none)"
json=$("$BIN" host status --json)
expect "status --json reports the hello" '"tmux_version": "[0-9]' "$json"
touch "$T/down-gamma" "$T/auth-beta"
rc=0; out=$("$BIN" host status 2>&1) || rc=$?
echo "--- host status, both failing ---"; printf '%s\n' "$out" | sed 's/^/      | /'
expect "a refused connection is unreachable with the reason" '^gamma +plain +unreachable +- +- +Connection refused' "$out"
expect "a rejected key needs auth with the hint" '^beta +full +needs auth +- +- +.*Permission denied.*run `ssh beta` once' "$out"
expect "status exits 1 when a host fails" '^1$' "$rc"
rm -f "$T/down-gamma" "$T/auth-beta"
"$BIN" host disconnect gamma >/dev/null
expect "a disabled host is skipped by status" '^0$' "$("$BIN" host status | grep -c '^gamma' || true)"
expect "list shows it disabled" '^gamma +gamma +plain +no ' "$("$BIN" host list)"
"$BIN" host remove gamma >/dev/null
expect "remove drops the host" '^beta$' "$("$BIN" host list --names)"
expect "remove prunes its local store" '^none$' "$([ -d "$T/state/hosts/gamma" ] && echo dir || echo none)"
finish
