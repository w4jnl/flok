#!/usr/bin/env bash
# M11: flok on the phone. A real flok-relay (built here) listens on 127.0.0.1; the sidebar links
# to it ([link] url/token), the "app" looks through the relay's API and WebSocket (flok-relay
# tail), a stand-in APNs logs the pushes, an answer typed on the phone lands in the agent pane
# (local, then on a remote host through the main instance), a subscription streams the screen,
# and the link comes back after the relay restarts.
R0=$(cd "$(dirname "$0")/../.." && pwd)
RELAY=$R0/bin/flok-relay
FH=$(mktemp -d)
go build -C "$R0" -o "$RELAY" ./cmd/flok-relay
go build -C "$R0" -o "$FH/fakehttp" ./scripts/e2e/fakehttp
free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'; }
RELAY_PORT=$(free_port)
RELAY_LOG=$(mktemp); RELAY_DATA=$(mktemp -d); APNS_KEY=$(mktemp)
# the stand-in APNs (scripts/e2e/fakehttp): one line per push,
# "<path>|<collapse id>|<title>|<body>|<category>|<badge>"; it reports its port once it listens
APNS_LOG=$FH/apns.log
"$FH/fakehttp" -mode apns -log "$APNS_LOG" -portfile "$FH/port" &
APNS_PID=$!
for _ in $(seq 1 100); do [ -s "$FH/port" ] && break; sleep 0.1; done
APNS_PORT=$(cat "$FH/port")
# a provider key for the relay's JWTs (what the .p8 from the developer account is)
openssl ecparam -name prime256v1 -genkey -noout 2>/dev/null | openssl pkcs8 -topk8 -nocrypt -out "$APNS_KEY" 2>/dev/null || true
INST_TOK=$("$RELAY" token); DEV_TOK=$("$RELAY" token)
start_relay() {
  FLOK_RELAY_INSTANCE_TOKENS=$INST_TOK FLOK_RELAY_DEVICE_TOKENS=$DEV_TOK FLOK_RELAY_APNS_KEY_FILE=$APNS_KEY \
  FLOK_RELAY_APNS_KEY_ID=KEY1234567 FLOK_RELAY_APNS_TEAM_ID=TEAM123456 FLOK_RELAY_APNS_TOPIC=nl.w4j.flok.e2e \
  FLOK_RELAY_APNS_URL=http://127.0.0.1:$APNS_PORT \
    "$RELAY" serve -listen "127.0.0.1:$RELAY_PORT" -data "$RELAY_DATA" >> "$RELAY_LOG" 2>&1 &
  RELAY_PID=$!
}
start_relay
E2E_EXTRA_CONFIG=$(printf '[link]\nname = "e2e"\nurl = "http://127.0.0.1:%s"\ntoken = "%s"\n' "$RELAY_PORT" "$INST_TOK")
export E2E_EXTRA_CONFIG
source "$(dirname "$0")/lib.sh"
m11_cleanup() { kill "${RELAY_PID:-}" "${APNS_PID:-}" "${TAIL_PID:-}" 2>/dev/null || true; rm -rf "$FH" "$RELAY_LOG" "$RELAY_DATA" "$APNS_KEY" "$T/tail.out" 2>/dev/null || true; }
trap 'm11_cleanup; cleanup' EXIT
api() { curl -s -m 5 -H "Authorization: Bearer $DEV_TOK" "$@" "http://127.0.0.1:$RELAY_PORT${API_PATH}"; }
GET() { API_PATH=$1 api; }
POST() { API_PATH=$1 api -X POST -d "$2"; }

# --- the relay is up, the instance links in --------------------------------------------------
expect_soon "the relay answers /healthz" '"ok":true' "$RELAY" health -url "http://127.0.0.1:$RELAY_PORT/healthz"
expect "the API wants a device token" 'device token is needed' "$(curl -s -m 5 "http://127.0.0.1:$RELAY_PORT/api/instances")"
expect_soon -t 15 "the instance is online at the relay with its agent" '"name":"e2e","online":true.*"pane_id":"'"$AGENT"'"' GET /api/instances
expect_soon "the link state is published in snapshot.json" '^connected$' json "$SNAP" "s['link']['state']"
expect "doctor reports the link as the sidebar sees it" 'link: connected to ws://127.0.0.1:'"$RELAY_PORT"'/link as "e2e"' "$("$BIN" doctor 2>&1 || true)"
expect "a phone registers for pushes" '"ok":true' "$(POST /api/devices '{"token":"e2edevicetoken0001","name":"e2e phone"}')"
expect "the device is listed, token shortened" '"name":"e2e phone"' "$(GET /api/devices)"
expect "the device list never shows the whole token" '^0$' "$(GET /api/devices | grep -c e2edevicetoken0001 || true)"

# --- a blocked agent: the app hears it, the phone gets a push ----------------------------------
"$RELAY" tail -url "http://127.0.0.1:$RELAY_PORT" -token "$DEV_TOK" -subscribe "e2e:$AGENT" > "$T/tail.out" 2>&1 &
TAIL_PID=$!
expect_soon "the app's WebSocket opens with the instances" '"type":"instances".*"name":"e2e"' cat "$T/tail.out"
hook claude '{"hook_event_name":"SessionStart","source":"startup","session_id":"ph1","cwd":"'"$R"'"}'
hook claude '{"hook_event_name":"UserPromptSubmit","session_id":"ph1"}'
hook claude '{"hook_event_name":"PermissionRequest","session_id":"ph1","tool_name":"Bash","tool_input":{"command":"npm test"},"tool_use_id":"t1"}'
expect_soon "the permission request reaches the app as an event" '"type":"event","instance":"e2e","event":."pane":"'"$AGENT"'".*"kind":"blocked"' cat "$T/tail.out"
expect_soon "… and the phone as a time-sensitive push with the permission category" '^/3/device/e2edevicetoken0001[|]e2e:'"$AGENT"'[|]e2e · '"$PROJ"'[|]needs you: perm:Bash[|]FLOK_PERMISSION[|]1$' cat "$APNS_LOG"
expect_soon "the subscription streams the agent's screen" '"type":"screen","instance":"e2e","screen":."pane":"'"$AGENT"'"' cat "$T/tail.out"

# --- the phone answers -------------------------------------------------------------------------
expect "an answer is typed into the agent pane" '"ok":true' "$(POST /api/answer '{"instance":"e2e","pane":"'"$AGENT"'","text":"y","keys":["enter"]}')"
expect_soon "the pane shows what was typed" '^y$' IN capture-pane -p -t "$AGENT"
expect "the answer is logged, the text is not" '"cmd":"answer","pane":"'"$AGENT"'","source":"phone","what":"1 chars \+ Enter"' "$(grep phone "$T/state/events.log")"
expect "the log holds no typed text" '^0$' "$(grep -c '"y"' "$T/state/events.log" || true)"
OTHER=$(IN display -p -t Alpha:0 '#{pane_id}')
expect "a pane without an agent is refused" '"error":"no agent in pane '"$OTHER"'"' "$(POST /api/answer '{"instance":"e2e","pane":"'"$OTHER"'","text":"rm -rf /"}')"
expect "the refusal never reached tmux" '^0$' "$(IN capture-pane -p -t "$OTHER" | grep -c 'rm -rf' || true)"
expect "a key outside the allowlist is refused by the relay" 'not allowed' "$(POST /api/answer '{"instance":"e2e","pane":"'"$AGENT"'","keys":["C-d"]}')"
expect "an unknown instance is refused" 'no instance office' "$(POST /api/answer '{"instance":"office","pane":"%1","text":"y"}')"
expect "seen through the API is taken" '"ok":true' "$(POST /api/seen '{"instance":"e2e","pane":"'"$AGENT"'"}')"
hook claude '{"hook_event_name":"PostToolUse","session_id":"ph1","tool_name":"Bash","tool_use_id":"t1"}'
hook claude '{"hook_event_name":"Stop","session_id":"ph1"}'
expect_soon "the finish is pushed too" '[|]e2e · '"$PROJ"'[|]finished[|]FLOK_DONE[|]' cat "$APNS_LOG"

# --- an agent on a remote host, answered through the main instance -----------------------------
fake_ssh_setup
fake_host beta
"$BIN" host add beta beta >/dev/null
wait_hosts "beta=connected" 25 || true
BPANE=$(eval 'echo $HOST_beta_PANE')
expect_soon -t 15 "the relay sees beta's agent in the instance's snapshot" '"pane_id":"'"$BPANE"'","host":"beta"' GET /api/instances
expect "an answer for beta:pane goes through the host's serve" '"ok":true' "$(POST /api/answer '{"instance":"e2e","pane":"beta:'"$BPANE"'","text":"n","keys":["Enter"]}')"
expect_soon "beta's pane shows what was typed" '^n$' tmux -L e2e-beta capture-pane -p -t "$BPANE"
rhook beta claude '{"hook_event_name":"SessionStart","source":"startup","session_id":"rb1","cwd":"'"$R"'"}'
rhook beta claude '{"hook_event_name":"UserPromptSubmit","session_id":"rb1"}'
rhook beta claude '{"hook_event_name":"PermissionRequest","session_id":"rb1","tool_name":"Bash","tool_input":{"command":"ls"},"tool_use_id":"rt1"}'
expect_soon "a push for the remote agent names the host" '[|]e2e · beta/'"$PROJ"'[|]needs you: perm:Bash[|]FLOK_PERMISSION[|]' cat "$APNS_LOG"
kill "$TAIL_PID" 2>/dev/null || true
"$RELAY" tail -url "http://127.0.0.1:$RELAY_PORT" -token "$DEV_TOK" -subscribe "e2e:beta:$BPANE" > "$T/tail.out" 2>&1 &
TAIL_PID=$!
expect_soon -t 10 "beta's screen streams through serve, the sidebar and the relay" '"type":"screen","instance":"e2e","screen":."pane":"beta:'"$BPANE"'","text":".*\\nn *\\n' cat "$T/tail.out"   # 3.2a pads captured lines with blanks
kill "$TAIL_PID" 2>/dev/null || true

# --- the relay goes away and comes back --------------------------------------------------------
kill "$RELAY_PID"; wait "$RELAY_PID" 2>/dev/null || true
expect_soon -t 10 "the link reports the relay gone" '^unreachable$' json "$SNAP" "s['link']['state']"
start_relay
expect_soon -t 20 "the link is back after the relay restarts" '^connected$' json "$SNAP" "s['link']['state']"
expect_soon -t 10 "the instance is online at the new relay, snapshot included" '"name":"e2e","online":true.*"pane_id":"'"$AGENT"'"' GET /api/instances
expect "the relay logged the instance" 'instance e2e online' "$(cat "$RELAY_LOG")"
expect "the relay logged the pushes" 'push: e2e · '"$PROJ"' → e2e phone \(blocked\)' "$(cat "$RELAY_LOG")"
hook claude '{"hook_event_name":"SessionEnd","session_id":"ph1","reason":"exit"}'
m11_cleanup
finish
