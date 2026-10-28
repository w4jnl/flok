#!/usr/bin/env bash
# M11: flok on the phone. A real flok-relay (built here) listens on 127.0.0.1; the sidebar links
# to it ([link] url/token, then the admin token from flok link admin), the "app" looks through
# the relay's API and WebSocket (flok-relay tail), a stand-in APNs logs the pushes, an answer
# typed on the phone lands in the agent pane (local, then on a remote host through the main
# instance), a subscription streams the screen, the link comes back after the relay restarts,
# flok link switches it off (the phone sees off, not offline) and prefix U on again, and the
# encrypted link ([link] pin) carries the same through a tap that reads every byte, which sees
# neither the token nor an answer. Both links are deflated (permessage-deflate on /link,
# flok-link/2 inside the tunnel).
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
INST_TOK=$("$RELAY" token); DEV_TOK=$("$RELAY" token); ADM_TOK=$("$RELAY" token)
start_relay() {
  FLOK_RELAY_INSTANCE_TOKENS=$INST_TOK FLOK_RELAY_ADMIN_TOKENS=$ADM_TOK FLOK_RELAY_DEVICE_TOKENS=$DEV_TOK FLOK_RELAY_APNS_KEY_FILE=$APNS_KEY \
  FLOK_RELAY_APNS_KEY_ID=KEY1234567 FLOK_RELAY_APNS_TEAM_ID=TEAM123456 FLOK_RELAY_APNS_TOPIC=nl.w4j.flok.e2e \
  FLOK_RELAY_APNS_URL=http://127.0.0.1:$APNS_PORT \
    "$RELAY" serve -listen "127.0.0.1:$RELAY_PORT" -data "$RELAY_DATA" >> "$RELAY_LOG" 2>&1 &
  RELAY_PID=$!
}
start_relay
E2E_EXTRA_CONFIG=$(printf '[link]\nname = "e2e"\nurl = "http://127.0.0.1:%s"\ntoken = "%s"\n' "$RELAY_PORT" "$INST_TOK")
export E2E_EXTRA_CONFIG
source "$(dirname "$0")/lib.sh"
m11_cleanup() { kill "${RELAY_PID:-}" "${APNS_PID:-}" "${TAIL_PID:-}" "${TAP_PID:-}" 2>/dev/null || true; rm -rf "$FH" "$RELAY_LOG" "$RELAY_DATA" "$APNS_KEY" "$T/tail.out" 2>/dev/null || true; }
trap 'm11_cleanup; cleanup' EXIT
api() { curl -s -m 12 -H "Authorization: Bearer $DEV_TOK" "$@" "http://127.0.0.1:$RELAY_PORT${API_PATH}"; }
GET() { API_PATH=$1 api; }
POST() { API_PATH=$1 api -X POST -d "$2"; }
PUT() { API_PATH=$1 api -X PUT -d "$2"; }

# --- the relay is up, the instance links in --------------------------------------------------
expect_soon "the relay answers /healthz" '"ok":true' "$RELAY" health -url "http://127.0.0.1:$RELAY_PORT/healthz"
expect "the API wants a device token" 'device token is needed' "$(curl -s -m 5 "http://127.0.0.1:$RELAY_PORT/api/instances")"
expect_soon -t 15 "the instance is online at the relay with its agent" '"name":"e2e","online":true.*"pane_id":"'"$AGENT"'"' GET /api/instances
expect_soon "the link state is published in snapshot.json" '^connected$' json "$SNAP" "s['link']['state']"
expect "doctor reports the link as the sidebar sees it" 'link: connected to ws://127.0.0.1:'"$RELAY_PORT"'/link as "e2e"' "$("$BIN" doctor 2>&1 || true)"
expect "a phone registers for pushes" '"ok":true' "$(POST /api/devices '{"token":"e2edevicetoken0001","name":"e2e phone"}')"
expect "the device is listed, token shortened" '"name":"e2e phone"' "$(GET /api/devices)"
expect "the device list never shows the whole token" '^0$' "$(GET /api/devices | grep -c e2edevicetoken0001 || true)"
expect "the phone reports a Live Activity" '"ok":true' "$(PUT /api/devices/e2edevicetoken0001/activity '{"activities":[{"id":"A1","token":"e2eoverviewtoken","kind":"overview"}]}')"
expect_soon "… which gets the overview as a liveactivity push" '^/3/device/e2eoverviewtoken[|].*[|]liveactivity$' cat "$APNS_LOG"

# --- a phone pairs by the code the terminal shows ---------------------------------------------
# flok phone pair asks the relay over the sidebar's link (the request mailbox, a phone frame);
# the phone redeems the code at /api/pair for a key of its own, which the relay keeps in
# phones.json; forget revokes the key and takes the phone's device with it
PAIR=$("$BIN" phone pair --json)
expect "flok phone pair mints a code through the sidebar and the link" '"code":"[A-Z2-9]{4}-[A-Z2-9]{4}".*"url":"flok://pair\?code=[A-Z2-9-]{9}&relay=http%3A%2F%2F127.0.0.1%3A'"$RELAY_PORT"'"' "$PAIR"
CODE=$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["code"])' "$PAIR")
expect "a wrong code gets the one answer" 'not valid any more' "$(curl -s -m 5 -d '{"code":"AAAA-AAAA","name":"x"}' "http://127.0.0.1:$RELAY_PORT/api/pair")"
PAIRED=$(curl -s -m 5 -d '{"code":"'"$CODE"'","name":"e2e iPhone"}' "http://127.0.0.1:$RELAY_PORT/api/pair")
expect "the phone redeems it for a key of its own" '"phone":\{"id":"[0-9a-f]+","name":"e2e iPhone"\}.*"token":"[0-9a-f]{48}"' "$PAIRED"
PHONE_TOK=$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["token"])' "$PAIRED")
expect "the code is good once" 'not valid any more' "$(curl -s -m 5 -d '{"code":"'"$CODE"'"}' "http://127.0.0.1:$RELAY_PORT/api/pair")"
expect_soon "the sidebar hears that the phone paired" '"cmd":"phone","name":"e2e iPhone","op":"paired"' cat "$T/state/events.log"
expect "the key opens the API" '"name":"e2e"' "$(curl -s -m 5 -H "Authorization: Bearer $PHONE_TOK" "http://127.0.0.1:$RELAY_PORT/api/instances")"
expect "… and registers the phone's device as its own" '"ok":true' "$(curl -s -m 5 -H "Authorization: Bearer $PHONE_TOK" -d '{"token":"e2ephonedevice0001","name":"e2e iPhone"}' "http://127.0.0.1:$RELAY_PORT/api/devices")"
expect "the device carries its pairing" '"name":"e2e iPhone","phone":"[0-9a-f]{12}"' "$(GET /api/devices)"
expect "flok phone list shows the phone" 'e2e iPhone.*e2e' "$("$BIN" phone list)"
expect "flok-relay phones reads the relay's file" 'e2e iPhone' "$("$RELAY" phones -data "$RELAY_DATA")"
expect "flok phone forget revokes it" 'e2e iPhone forgotten' "$("$BIN" phone forget 'e2e iPhone')"
expect_soon "the key is refused from then on" '^401$' curl -s -m 5 -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $PHONE_TOK" "http://127.0.0.1:$RELAY_PORT/api/instances"
expect "its device went with it" '^0$' "$(GET /api/devices | grep -c e2ephonedevice || true)"
expect "the relay logged the pairing and the forget" 'phone e2e iPhone (.*) paired through e2e.*phone e2e iPhone (.*) forgotten by e2e' "$(tr '\n' ' ' < "$RELAY_LOG")"
# a relay needs instance tokens alone: phones come by pairing
P2=$(free_port)
FLOK_RELAY_INSTANCE_TOKENS=$INST_TOK "$RELAY" serve -listen "127.0.0.1:$P2" > "$T/relay2.log" 2>&1 &
R2_PID=$!
expect_soon "a relay starts with instance tokens alone" '"ok":true' "$RELAY" health -url "http://127.0.0.1:$P2/healthz"
expect "… and says its pairings live in memory without a data dir" 'phones: kept in memory' "$(cat "$T/relay2.log")"
kill "$R2_PID" 2>/dev/null || true; wait "$R2_PID" 2>/dev/null || true


# --- a flok joins by the code it shows ---------------------------------------------------------
# flok link join talks to the relay itself (no key yet): a code, a poll; a member of the tenant
# approves it (a linked flok: flok link approve; the app: POST /api/enroll/approve with its key;
# an admin flok: flok tenant approve, naming the tenant); the flok accepts and keeps its key in
# link.json, which wins over [link] in config.toml. A separate state dir stands in for the
# other machine; --json implies --yes and prints the code, then the outcome.
JOIN_STATE=$T/join-state; mkdir -p "$JOIN_STATE"
jcode() { head -1 "$1" | python3 -c 'import json,sys; print(json.loads(sys.stdin.readline())["code"])'; }
FLOK_STATE=$JOIN_STATE "$BIN" link join "http://127.0.0.1:$RELAY_PORT" --name e2e-office --json > "$T/join.out" 2>&1 &
JOIN_PID=$!
expect_soon "flok link join asks the relay for a code" '"code":"[A-Z2-9]{4}-[A-Z2-9]{4}".*"url":"flok://link\?code=[A-Z2-9-]{9}&relay=http%3A%2F%2F127.0.0.1%3A'"$RELAY_PORT"'"' cat "$T/join.out"
JCODE=$(jcode "$T/join.out")
expect "the relay logged the request" 'join: e2e-office (.*, flok .*) from 127.0.0.1 asks to join' "$(cat "$RELAY_LOG")"
expect "a wrong code is nobody" 'no flok is waiting with that code' "$("$BIN" link approve BBBB-BBBB 2>&1 || true)"
expect "the app looks the code up before approving" '"pending":\{"name":"e2e-office"' "$(GET "/api/enroll/code/$JCODE")"
expect "flok link approve lets it into this tenant" 'approved e2e-office (.*, flok .*) into default: it links once it accepts there' "$("$BIN" link approve "$JCODE")"
expect_soon "the joining flok hears it and keeps its key" '"approved_by":"flok e2e","instance":"[0-9a-f]{12}","name":"e2e-office".*"status":"joined","tenant":"default"' cat "$T/join.out"
wait "$JOIN_PID" || true
expect "link.json holds the key, for this user alone" '^-rw-------' "$(ls -l "$JOIN_STATE/link.json")"
expect "flok link status there says it joined" 'joined default as e2e-office on .*, approved by flok e2e' "$(FLOK_STATE=$JOIN_STATE "$BIN" link status)"
expect "doctor there names the join" 'link: joined default as e2e-office' "$(FLOK_STATE=$JOIN_STATE "$BIN" doctor 2>&1 || true)"
expect "flok link list shows both floks" 'e2e .*online.*a token from the relay.s config.*e2e-office.*offline.*flok e2e' "$("$BIN" link list | tr '\n' ' ')"
expect "flok-relay instances reads the relay's file" 'e2e-office.*default.*flok e2e' "$("$RELAY" instances -data "$RELAY_DATA")"
FLOK_STATE=$JOIN_STATE "$BIN" link join "http://127.0.0.1:$RELAY_PORT" --name e2e-third --json > "$T/join3.out" 2>&1 &
JOIN3_PID=$!
expect_soon "another flok asks" '"code":"' cat "$T/join3.out"
expect "the app's own key approves into its tenant too" '"pending":\{"name":"e2e-third"' "$(POST /api/enroll/approve '{"code":"'"$(jcode "$T/join3.out")"'"}')"
expect_soon "… approved by a device token" '"approved_by":"a device token","instance":"[0-9a-f]{12}","name":"e2e-third".*"status":"joined","tenant":"default"' cat "$T/join3.out"
wait "$JOIN3_PID" || true
expect "flok link forget revokes a flok" 'e2e-third forgotten: its key no longer works' "$("$BIN" link forget e2e-third)"
expect "… and the other" 'e2e-office forgotten' "$("$BIN" link forget e2e-office)"
expect "the records are gone" 'no flok joined by code' "$("$RELAY" instances -data "$RELAY_DATA")"
# tenants take an admin token, which config.toml does not hold here: flok link admin keeps it in
# the state dir (from stdin, never the command line) and has the sidebar link again; the link
# carries it besides the instance token (the X-Flok-Admin header; inside the tunnel the auth
# frame), so this flok manages them
expect "without an admin token the relay keeps the tenants to itself" 'holds no admin token: flok link admin' "$("$BIN" tenant list 2>&1 || true)"
expect "flok link admin keeps the token from stdin" 'admin token kept in .*link-admin; the sidebar links again with it now' "$(printf '%s\n' "$ADM_TOK" | "$BIN" link admin)"
expect "… for this user alone" '^-rw-------' "$(ls -l "$FLOK_STATE/link-admin")"
expect_soon "flok tenant list shows the relay's own tenant" 'default.*the relay.s own' "$BIN" tenant list
expect "doctor says where the admin token comes from" 'link: the admin token in link-admin \(flok link admin\) is set' "$("$BIN" doctor 2>&1 || true)"
expect "flok tenant rename calls the relay's own tenant something else" 'renamed default to e2ehome' "$("$BIN" tenant rename default e2ehome)"
expect "… which the list shows" 'e2ehome.*the relay.s own' "$("$BIN" tenant list)"
expect "… and the relay's file" 'e2ehome' "$("$RELAY" tenants -data "$RELAY_DATA")"
expect "… and the phone, with its instances" '"tenant_info":\{"id":"default","name":"e2ehome"' "$(GET /api/instances)"
expect "the relay's own tenant answers to its id still" 'renamed default to default' "$("$BIN" tenant rename default default)"
FLOK_STATE=$JOIN_STATE "$BIN" link join "http://127.0.0.1:$RELAY_PORT" --name e2e-lab --json > "$T/join2.out" 2>&1 &
JOIN2_PID=$!
expect_soon "a flok asks to join a tenant of its own" '"code":"[A-Z2-9]{4}-[A-Z2-9]{4}"' cat "$T/join2.out"
expect "flok tenant approve makes the tenant and lets it in" 'approved e2e-lab (.*) into e2elab' "$("$BIN" tenant approve "$(jcode "$T/join2.out")" --tenant e2elab)"
expect_soon "… and it joins there" '"approved_by":"admin e2e","instance":"[0-9a-f]{12}","name":"e2e-lab".*"status":"joined","tenant":"e2elab"' cat "$T/join2.out"
wait "$JOIN2_PID" || true
expect "flok tenant show lists its flok" 'tenant e2elab.*e2e-lab.*admin e2e' "$("$BIN" tenant show e2elab | tr '\n' ' ')"
expect "flok-relay tenants reads the files" 'e2elab.*1.*0' "$("$RELAY" tenants -data "$RELAY_DATA")"
expect "the relay's own tenant stays" 'own tenant stays' "$("$BIN" tenant remove default 2>&1 || true)"
expect "flok tenant remove takes a tenant out with its floks" 'e2elab removed: 1 floks and 0 phones' "$("$BIN" tenant remove e2elab)"
expect "… from the files too" '^0$' "$("$RELAY" tenants -data "$RELAY_DATA" | grep -c e2elab || true)"
expect "a relay refuses to start when nothing could link in" 'nothing could link in' "$("$RELAY" serve -listen 127.0.0.1:1 -data "$T/empty-data" 2>&1 || true)"

# --- a blocked agent: the app hears it, the phone gets a push ----------------------------------
"$RELAY" tail -url "http://127.0.0.1:$RELAY_PORT" -token "$DEV_TOK" -subscribe "e2e:$AGENT" > "$T/tail.out" 2>&1 &
TAIL_PID=$!
expect_soon "the app's WebSocket opens with the instances" '"type":"instances".*"name":"e2e"' cat "$T/tail.out"
hook claude '{"hook_event_name":"SessionStart","source":"startup","session_id":"ph1","cwd":"'"$R"'"}'
hook claude '{"hook_event_name":"UserPromptSubmit","session_id":"ph1"}'
hook claude '{"hook_event_name":"PermissionRequest","session_id":"ph1","tool_name":"Bash","tool_input":{"command":"npm test"},"tool_use_id":"t1"}'
expect_soon "the permission request reaches the app as an event" '"type":"event","instance":"e2e","event":."pane":"'"$AGENT"'".*"kind":"blocked"' cat "$T/tail.out"
expect_soon "… and the phone as a time-sensitive push with the permission category and flok's sound" '^/3/device/e2edevicetoken0001[|]e2e:'"$AGENT"'[|]'"$PROJ"'[|]● needs you: perm:Bash[|]FLOK_PERMISSION[|]1[|]e2e · [^|]+[|]flok-request.caf[|]time-sensitive[|]alert$' cat "$APNS_LOG"
expect_soon "the subscription streams the agent's screen" '"type":"screen","instance":"e2e","screen":."pane":"'"$AGENT"'"' cat "$T/tail.out"
expect "one look at the screen (the notification's expanded view)" '"pane":"'"$AGENT"'","text":"' "$(GET "/api/screen?instance=e2e&pane=$(printf %s "$AGENT" | sed 's/%/%25/')")"

# --- the phone answers -------------------------------------------------------------------------
expect "an answer is typed into the agent pane" '"ok":true' "$(POST /api/answer '{"instance":"e2e","pane":"'"$AGENT"'","text":"y","keys":["enter"]}')"
expect_soon "the pane shows what was typed" '^y$' IN capture-pane -p -t "$AGENT"
expect "the answer is logged, the text is not" '"cmd":"answer","pane":"'"$AGENT"'","source":"phone","what":"1 chars \+ Enter"' "$(grep phone "$T/state/events.log")"
expect "the log holds no typed text" '^0$' "$(grep -c '"y"' "$T/state/events.log" || true)"
# a long reply with newlines is pasted through a tmux buffer; a file is saved under the state
# dir and typed as its path (what an image for Claude Code looks like); the relay tells the app
# what the instance takes, and refuses what it does not
expect "the relay lists what the instance takes" '"features":\["answer","screen","long-text","files","phone","enroll","form"\]' "$(GET /api/instances)"
expect "a long reply with newlines is taken" '"ok":true' "$(POST /api/answer '{"instance":"e2e","pane":"'"$AGENT"'","text":"first long line\nsecond long line","keys":["Enter"]}')"
expect_soon "the pane shows both lines" '^first long line *$' IN capture-pane -p -J -t "$AGENT"
expect "… the second as well" '^second long line *$' "$(IN capture-pane -p -J -t "$AGENT")"
PNG=$(printf '\211PNG\r\n\032\n' | base64 | tr -d '\n')
expect "two screenshots are taken" '"ok":true' "$(POST /api/answer '{"instance":"e2e","pane":"'"$AGENT"'","text":"look","keys":["Enter"],"files":[{"name":"Screen Shot.png","data":"'"$PNG"'"},{"name":"second.png","data":"'"$PNG"'"}]}')"
expect_soon "… saved under the state dir with safe names" '[0-9]{8}-[0-9]{6}-Screen_Shot\.png' ls "$T/state/attach"
expect "… the second as well" '[0-9]{8}-[0-9]{6}-second\.png' "$(ls "$T/state/attach")"
ATT=$(ls "$T/state/attach"/*-Screen_Shot.png | head -1); ATT2=$(ls "$T/state/attach"/*-second.png | head -1)
expect "… byte for byte" '^89504e470d0a1a0a$' "$(od -An -tx1 "$ATT" | tr -d ' \n')"
expect_soon "… and both paths typed after the text, in order" "^look $ATT $ATT2 *\$" IN capture-pane -p -J -t "$AGENT"
expect "the files are logged by name and size, never their content" '"what":"4 chars \+ Screen_Shot.png \(1 KB\), second.png \(1 KB\) \+ Enter"' "$(grep phone "$T/state/events.log")"
expect "a file of another kind is refused by the relay" 'files of type .*x-msdownload.* are not taken' "$(POST /api/answer '{"instance":"e2e","pane":"'"$AGENT"'","file":{"name":"x.exe","mime":"application/x-msdownload","data":"'"$PNG"'"}}')"
OTHER=$(IN display -p -t Alpha:0 '#{pane_id}')
expect "a pane without an agent is refused" '"error":"no agent in pane '"$OTHER"'"' "$(POST /api/answer '{"instance":"e2e","pane":"'"$OTHER"'","text":"rm -rf /"}')"
expect "the refusal never reached tmux" '^0$' "$(IN capture-pane -p -t "$OTHER" | grep -c 'rm -rf' || true)"
expect "a key outside the allowlist is refused by the relay" 'not allowed' "$(POST /api/answer '{"instance":"e2e","pane":"'"$AGENT"'","keys":["C-d"]}')"
# a form answer: the relay checks its shape, the instance checks the agent shows a form (the
# fake agent shows none: the message tells the phone to use the screen)
expect "a form answer with text is refused by the relay" 'carries nothing else' "$(POST /api/answer '{"instance":"e2e","pane":"'"$AGENT"'","text":"y","form":{"answers":[{"selected":[0]}]}}')"
expect "a form answer picking beyond the digits is refused by the relay" 'options are 0 to 8' "$(POST /api/answer '{"instance":"e2e","pane":"'"$AGENT"'","form":{"answers":[{"selected":[9]}]}}')"
expect "a form answer for an agent without a form is refused by the instance" 'no question form open in pane '"$AGENT" "$(POST /api/answer '{"instance":"e2e","pane":"'"$AGENT"'","form":{"answers":[{"selected":[0]}]}}')"
expect "an unknown instance is refused" 'no instance office' "$(POST /api/answer '{"instance":"office","pane":"%1","text":"y"}')"
expect "seen through the API is taken" '"ok":true' "$(POST /api/seen '{"instance":"e2e","pane":"'"$AGENT"'"}')"
hook claude '{"hook_event_name":"PostToolUse","session_id":"ph1","tool_name":"Bash","tool_use_id":"t1"}'
hook claude '{"hook_event_name":"Stop","session_id":"ph1"}'
expect_soon "the finish lights up the phone's overview instead of a notification, with the done sound" '^/3/device/e2eoverviewtoken[|][|]'"$PROJ"'[|]✓ done · e2e · [^|]+[|][|][|][|]flok-done.caf[|][|]liveactivity$' cat "$APNS_LOG"
expect_soon "… and a badge-only push, which shows nothing, keeps the badge" '^/3/device/e2edevicetoken0001[|][|][|][|][|][0-9]+[|][|][|][|]alert$' cat "$APNS_LOG"
expect "no done notification beside it" '^0$' "$(grep -c '✓ done[|]FLOK_DONE' "$APNS_LOG" || true)"
expect_soon "the relay logged it as an island alert" 'push: e2e · '"$PROJ"' → e2e phone e2edevic… production in the island \(done\)' cat "$RELAY_LOG"
expect "the phone marks the finished agent seen" '"ok":true' "$(POST /api/seen '{"instance":"e2e","pane":"'"$AGENT"'"}')"
expect_soon "… and every phone gets a silent sync to clear its alert and badge" '^/3/device/e2edevicetoken0001[|][|][|][|][|][|][|][|][|]background$' cat "$APNS_LOG"
expect "the phone mutes the agent" '"ok":true' "$(PUT /api/devices/e2edevicetoken0001/prefs '{"mutes":[{"instance":"e2e","agent":"'"$PROJ"'"}]}')"
expect "the settings are listed with the phone" '"mutes":\[\{"instance":"e2e","agent":"'"$PROJ"'"' "$(GET /api/devices)"
expect "and taken off again" '"ok":true' "$(PUT /api/devices/e2edevicetoken0001/prefs '{}')"

# --- an agent on a remote host, answered through the main instance -----------------------------
fake_ssh_setup
fake_host beta
"$BIN" host add beta beta >/dev/null
wait_hosts "beta=connected" 25 || true
BPANE=$(eval 'echo $HOST_beta_PANE')
expect_soon -t 15 "the relay sees beta's agent in the instance's snapshot" '"pane_id":"'"$BPANE"'","host":"beta"' GET /api/instances
expect "an answer for beta:pane goes through the host's serve" '"ok":true' "$(POST /api/answer '{"instance":"e2e","pane":"beta:'"$BPANE"'","text":"n","keys":["Enter"]}')"
expect_soon "beta's pane shows what was typed" '^n$' tmux -L e2e-beta capture-pane -p -t "$BPANE"
expect "a screenshot for beta's agent goes through its serve" '"ok":true' "$(POST /api/answer '{"instance":"e2e","pane":"beta:'"$BPANE"'","text":"on beta","file":{"name":"beta.png","data":"'"$PNG"'"}}')"
expect_soon "… saved in beta's own state dir" '[0-9]{8}-[0-9]{6}-beta\.png' ls "$T/hosts/beta/state/attach"
expect_soon "… and typed into beta's pane with its path there" "^on beta $T/hosts/beta/state/attach/[0-9-]+-beta\.png *\$" tmux -L e2e-beta capture-pane -p -J -t "$BPANE"
rhook beta claude '{"hook_event_name":"SessionStart","source":"startup","session_id":"rb1","cwd":"'"$R"'"}'
rhook beta claude '{"hook_event_name":"UserPromptSubmit","session_id":"rb1"}'
rhook beta claude '{"hook_event_name":"PermissionRequest","session_id":"rb1","tool_name":"Bash","tool_input":{"command":"ls"},"tool_use_id":"rt1"}'
expect_soon "a push for the remote agent names the host" '[|]'"$PROJ"'[|]● needs you: perm:Bash[|]FLOK_PERMISSION[|][0-9]*[|]e2e / beta( · [^|]*)?[|]' cat "$APNS_LOG"
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
expect "... with its frames deflated, as the admin flok" 'instance e2e online \(flok [^)]*, compressed, admin\)' "$(cat "$RELAY_LOG")"
expect "the relay logged the pushes" 'push: e2e · '"$PROJ"' → e2e phone e2edevic… production \(blocked\)' "$(cat "$RELAY_LOG")"

# --- flok link: the relay link switched off and on again (prefix U) ------------------------------
expect "flok link status says connected" "^relay link: on, connected to 127.0.0.1:$RELAY_PORT, compressed\$" "$("$BIN" link status)"
expect_soon "the sidebar shows the link at the end of its brand line" '\[flok\] +⇄' capture
expect "flok link off confirms" '^relay link: off \(flok link on\)$' "$("$BIN" link off)"
expect_soon -t 10 "the relay shows the instance off, not offline" '"name":"e2e","online":false,"off":true' GET /api/instances
expect_soon "... and logged why" 'instance e2e switched off \(flok link off there\)' cat "$RELAY_LOG"
expect_soon "the sidebar's mark turns to a dash" '\[flok\] +–' capture
expect "an answer from the phone is refused with the reason" 'e2e is switched off \(flok link on there\)' "$(POST /api/answer '{"instance":"e2e","pane":"'"$AGENT"'","text":"x"}')"
expect "flok status mentions it while off" '^relay link: off \(flok link on\)$' "$("$BIN" status | grep '^relay link' || true)"
expect "doctor reports it switched off" 'ok +link: switched off since' "$("$BIN" doctor 2>&1 || true)"
ONLINE=$(grep -c 'instance e2e online' "$RELAY_LOG" || true)
OLD_PID=$(json "$SNAP" "s['sidebar_pid']")
"$BIN" reload
wait_json "$SNAP" "s['sidebar_pid'] != $OLD_PID and s.get('link')" 15 || true
expect "a reloaded sidebar starts with the link off" '^off$' "$(json "$SNAP" "s['link']['state']")"
sleep 1   # a negative check: nothing dials the relay meanwhile
expect "... and never dials the relay" "^$ONLINE\$" "$(grep -c 'instance e2e online' "$RELAY_LOG" || true)"
OUT send-keys -t "$RIGHT" C-b; sleep 0.3; OUT send-keys -t "$RIGHT" U   # the key the sidebar bound in the inner
expect_soon -t 10 "prefix U switches it on again" '^connected$' json "$SNAP" "s['link']['state']"
expect_soon -t 10 "the instance is back online at the relay" '"name":"e2e","online":true' GET /api/instances
expect "... and no longer off" '^0$' "$(GET /api/instances | grep -c '"off":true' || true)"
expect "the switch is cleared" '^0$' "$(cat "$T/state/link-off")"

# --- the encrypted link: TLS inside the WebSocket, to the relay's own key -----------------------
# a tap between the sidebar and the relay keeps every byte, as a proxy that inspects HTTPS would
PIN=$("$RELAY" pin -data "$RELAY_DATA")
expect "the relay logged the pin of its key at start" "link pin $PIN" "$(cat "$RELAY_LOG")"
"$FH/fakehttp" -mode tap -upstream "127.0.0.1:$RELAY_PORT" -log "$FH/tap.log" -portfile "$FH/tapport" &
TAP_PID=$!
for _ in $(seq 1 100); do [ -s "$FH/tapport" ] && break; sleep 0.1; done
sed -i.bak "s#127.0.0.1:$RELAY_PORT#127.0.0.1:$(cat "$FH/tapport")#" "$FLOK_CONFIG" && rm -f "$FLOK_CONFIG.bak"
printf 'pin = "%s"\n' "$PIN" >> "$FLOK_CONFIG"   # [link] is the config's last table
"$BIN" reload
expect_soon -t 20 "the sidebar links in again, encrypted and deflated, through the tap" '^connected True True$' json "$SNAP" "s['link']['state'] + ' ' + str(s['link'].get('encrypted')) + ' ' + str(s['link'].get('compressed'))"
expect_soon -t 10 "the relay logged the encrypted link" 'instance e2e online \(flok [^)]*, encrypted, compressed, admin\)' cat "$RELAY_LOG"
expect_soon -t 10 "the snapshot reaches the relay inside the tunnel" '"name":"e2e","online":true.*"pane_id":"'"$AGENT"'"' GET /api/instances
expect "an answer goes through the tunnel" '"ok":true' "$(POST /api/answer '{"instance":"e2e","pane":"'"$AGENT"'","text":"tunnelreply"}')"
expect_soon "the pane shows it" 'tunnelreply' IN capture-pane -p -t "$AGENT"
expect "the link went through the tap, to the encrypted path" 'GET /link/tls' "$(cat "$FH/tap.log")"
expect "the tap never saw the instance token" '^0$' "$(grep -c -a -F "$INST_TOK" "$FH/tap.log" || true)"
expect "… nor the admin token" '^0$' "$(grep -c -a -F "$ADM_TOK" "$FH/tap.log" || true)"
expect "flok tenant works through the encrypted link too" 'default.*the relay.s own' "$("$BIN" tenant list)"
expect "the tap never saw the answer" '^0$' "$(grep -c -a -F tunnelreply "$FH/tap.log" || true)"
expect "flok doctor reports the encrypted link" 'link: connected to ws://127.0.0.1:[0-9]+/link/tls as "e2e", encrypted to the pinned key, compressed' "$("$BIN" doctor 2>&1)"
sed -i.bak "s#^pin = .*#pin = \"sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\"#" "$FLOK_CONFIG" && rm -f "$FLOK_CONFIG.bak"
"$BIN" reload
expect_soon -t 20 "a key other than the pinned one is refused" "^refused: the relay's key does not match \[link\] pin \(it is $PIN\)" json "$SNAP" "s['link']['state'] + ': ' + s['link'].get('detail', '')"
hook claude '{"hook_event_name":"SessionEnd","session_id":"ph1","reason":"exit"}'
m11_cleanup
finish
