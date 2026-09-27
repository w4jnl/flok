#!/usr/bin/env bash
# M10: remote hosts, part 1 — the host registry, `flok serve --stdio` over a pipe, and `flok host
# status`, through a fake ssh that runs the "remote" command locally against the isolated servers
# e2e-beta (mode full) and e2e-gamma (mode plain). The sidebar's own use of hosts comes with part 2.
source "$(dirname "$0")/lib.sh"
fake_ssh_setup
fake_host beta
fake_host gamma
wait_file() { local i; for i in $(seq 1 $(( ${3:-5} * 10 ))); do grep -qE "$2" "$1" 2>/dev/null && return 0; sleep 0.1; done; return 1; }
# Part 1 is the CLI without a sidebar: it uses a state dir of its own (as on a machine that only
# checks hosts), so the running sidebar, which connects to a host the moment it is registered,
# does not compete for the hosts' serve locks. Part 2 registers the hosts for the sidebar.
CLI_STATE=$T/state-cli
cli() { FLOK_STATE=$CLI_STATE "$BIN" "$@"; }

# --- registry ------------------------------------------------------------------------------
expect "host list starts empty" 'no remote hosts' "$(cli host list)"
cli host add beta beta >/dev/null
cli host add gamma gamma --mode plain --socket agents >/dev/null
rc=0; cli host add bad -x >/dev/null 2>&1 || rc=$?
expect "an option-like target is refused" '^2$' "$rc"
rc=0; out=$(cli host add local x 2>&1) || rc=$?
expect "local is reserved" 'names the local server' "$out"
rc=0; cli host add beta other >/dev/null 2>&1 || rc=$?
expect "a duplicate name is refused" '^1$' "$rc"
expect "list --names in registry order" '^beta gamma$' "$(cli host list --names | tr '\n' ' ' | sed 's/ $//')"
json=$(cli host list --json)
expect "list --json carries the version" '"version": 1' "$json"
expect "gamma is plain with its socket" '"socket": "agents"' "$json"
expect "hosts.json lives in the state dir" '"name": "beta"' "$(cat "$CLI_STATE/hosts.json")"
expect "list shows enabled hosts never connected" '^beta +beta +full +yes +never' "$(cli host list)"

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
printf '{"type":"goto","goto":{"session":"$999"}}\n' >&7   # the sidebar's parked pane is beta's client by now: aim at a session that is not there
wait_file "$SERVE_OUT" '"type":"error"' 3 || true
expect "a goto that fails on the host is an error frame, not a crash" '"error":"goto: ' "$(cat "$SERVE_OUT")"
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
rc=0; out=$(cli host status 2>&1) || rc=$?
echo "--- host status ---"; printf '%s\n' "$out" | sed 's/^/      | /'
expect "beta answers in full mode with flok and its tmux" '^beta +full +connected +flok [^,]+, tmux [0-9][^ ]* +1( |$)' "$out"
expect "gamma answers in plain mode with its tmux and the title agent" '^gamma +plain +connected +tmux [0-9][^ ]* +1( |$)' "$out"
expect "status exits 0 when every host answers" '^0$' "$rc"
expect "last_connected recorded" '"last_connected"' "$(cat "$CLI_STATE/hosts.json")"
expect "gamma has a local store of its own" '^dir$' "$([ -d "$CLI_STATE/hosts/gamma/agents" ] && echo dir || echo none)"
json=$(cli host status --json)
expect "status --json reports the hello" '"tmux_version": "[0-9]' "$json"
touch "$T/down-gamma" "$T/auth-beta"
rc=0; out=$(cli host status 2>&1) || rc=$?
echo "--- host status, both failing ---"; printf '%s\n' "$out" | sed 's/^/      | /'
expect "a refused connection is unreachable with the reason" '^gamma +plain +unreachable +- +- +Connection refused' "$out"
expect "a rejected key needs auth with the hint" '^beta +full +needs auth +- +- +.*Permission denied.*run `ssh beta` once' "$out"
expect "status exits 1 when a host fails" '^1$' "$rc"
rm -f "$T/down-gamma" "$T/auth-beta"
cli host disconnect gamma >/dev/null
expect "a disabled host is skipped by status" '^0$' "$(cli host status | grep -c '^gamma' || true)"
expect "list shows it disabled" '^gamma +gamma +plain +no ' "$(cli host list)"
cli host remove gamma >/dev/null
expect "remove drops the host" '^beta$' "$(cli host list --names)"
expect "remove prunes its local store" '^none$' "$([ -d "$CLI_STATE/hosts/gamma" ] && echo dir || echo none)"

# --- part 2: the sidebar goes multi-host -----------------------------------------------------
# The running sidebar watches its hosts.json: registering the hosts connects both through the
# manager and parks a work pane per host in the outer.
"$BIN" host add beta beta >/dev/null
"$BIN" host add gamma gamma --mode plain >/dev/null
RT=$T/state/runtime.json
SNAP=$T/state/snapshot.json
rt() { python3 -c "import json,sys;r=json.load(open('$RT'));print(eval(sys.argv[1]))" "$1" 2>/dev/null || true; }
snap_hosts() { python3 -c "import json;print(' '.join(h['name']+'='+h['state'] for h in json.load(open('$SNAP')).get('hosts',[])))" 2>/dev/null || true; }
wait_hosts() { local i; for i in $(seq 1 $(( ${2:-10} * 10 ))); do [ "$(snap_hosts)" = "$1" ] && return 0; sleep 0.1; done; return 1; }
wait_for 'servers' 5 || true
snap=$(capture); echo "--- servers panel ---"; printf '%s\n' "$snap" | grep -v '^ *$' | sed -n '1,12p' | sed 's/^/      | /'
expect "the sidebar grows a servers panel" '^servers' "$snap"
expect "local is the first server row" '^ . local' "$snap"
expect "the sessions header names the front host" '^sessions · local' "$snap"
wait_hosts "beta=connected gamma=connected" 25 || true   # beta may sit out a busy retry after part 1's serves
expect "snapshot.json reports both hosts connected" '^beta=connected gamma=connected$' "$(snap_hosts)"
wait_for '. beta +[0-9]' 5 || true
wait_for '. gamma +[0-9]' 10 || true   # plain mode polls over the fake ssh with a 1 s floor: slower on a loaded runner
snap=$(capture)
expect "beta's row counts its agent" ' beta +1' "$snap"
expect "gamma's row counts its title-detected agent" ' gamma +1' "$snap"
expect "remote agents carry their host on the second line" 'beta · claude' "$snap"
expect "the plain host's agent shows too" 'gamma · claude' "$snap"
wins=$(OUT list-windows -t flok -F '#{window_name} #{window_panes}')
expect "a parked window per host, one pane each" '^flok-host-beta 1$' "$wins"
expect "gamma has its parked window too" '^flok-host-gamma 1$' "$wins"
BETA_PANE=$(rt "r['hosts']['beta']['pane']")
BETA_WIN=$(rt "r['hosts']['beta']['window']")
expect "runtime.json records beta's pane and window" '^%[0-9]+ @[0-9]+$' "$BETA_PANE $BETA_WIN"
# the parked pane attached to beta's tmux through the fake ssh
for _ in $(seq 1 50); do OUT capture-pane -p -t "$BETA_PANE" | grep -q 'remote-agent\|Remote' && break; sleep 0.1; done
expect "beta's parked pane shows beta's tmux" '' "$(OUT capture-pane -p -t "$BETA_PANE" | grep -c . )"
expect "beta's tmux has a client (the parked pane)" '^1$' "$(tmux -L e2e-beta list-clients | wc -l | tr -d ' ')"
# a hook on beta changes its row here (beta is not in front: unfocused there, so Stop ends done)
rhook beta claude '{"hook_event_name":"PostToolUse","session_id":"r1","tool_name":"Bash","tool_use_id":"t2"}'
rhook beta claude '{"hook_event_name":"Stop","session_id":"r1"}'
wait_for 'done' 5 || true
snap=$(capture)
expect "a Stop on beta shows done here" '✓ .*done' "$snap"
expect "beta's server row counts the pending agent" ' beta +1 · [0-9]' "$snap"

# switch to beta with the keyboard: Tab Tab reaches the servers panel, j selects beta, Enter swaps
"$BIN" focus
OUT send-keys -t "$SIDEBAR" Tab Tab j Enter
for _ in $(seq 1 50); do [ "$(rt "r.get('front_host','')")" = beta ] && break; sleep 0.1; done
expect "front_host is beta" '^beta$' "$(rt "r.get('front_host','')")"
expect "right_pane is beta's pane" "^$BETA_PANE\$" "$(rt "r['right_pane']")"
expect "local_pane remembers the local loop" "^$RIGHT\$" "$(rt "r.get('local_pane','')")"
expect "beta's pane sits in window 0" "$BETA_PANE" "$(OUT list-panes -t flok:0 -F '#{pane_id}')"
expect "the local pane is parked in beta's window" "$RIGHT" "$(OUT list-panes -t "$BETA_WIN" -F '#{pane_id}')"
expect "the sidebar keeps its width" "^$(python3 -c "import json;print(json.load(open('$RT'))['full_width'])")\$" "$(OUT display -p -t "$SIDEBAR" '#{pane_width}')"
wait_for 'sessions · beta' 3 || true
snap=$(capture); echo "--- beta in front ---"; printf '%s\n' "$snap" | grep -v '^ *$' | sed -n '1,12p' | sed 's/^/      | /'
expect "the sessions panel shows beta's sessions" '^sessions · beta' "$snap"
expect "beta's session is listed" ' Remote' "$snap"
expect "snapshot.json marks beta as front" '"front_host": "beta"' "$(cat "$SNAP")"
expect "beta was told it is visible" '^1$' "$(cat "$T/hosts/beta/state/terminal-focus")"

# hide (zoom) with beta in front, switch back to local while hidden, un-hide: the zoom follows
"$BIN" hide
expect "hide zooms the front pane" '^1$' "$(OUT display -p -t "$BETA_PANE" '#{window_zoomed_flag}')"
OUT send-keys -t "$SIDEBAR" k Enter
for _ in $(seq 1 50); do [ "$(rt "r.get('front_host','')")" = "" ] && [ "$(rt "r['right_pane']")" = "$RIGHT" ] && break; sleep 0.1; done
expect "front_host is local again" '^$' "$(rt "r.get('front_host','')")"
expect "right_pane is the local pane again" "^$RIGHT\$" "$(rt "r['right_pane']")"
expect "the window stays zoomed on the new front pane" '^1$' "$(OUT display -p -t "$RIGHT" '#{window_zoomed_flag}')"
expect "beta's pane is parked again" "$BETA_PANE" "$(OUT list-panes -t "$BETA_WIN" -F '#{pane_id}')"
"$BIN" hide
expect "un-hide restores the layout" '^0$' "$(OUT display -p -t "$RIGHT" '#{window_zoomed_flag}')"
expect "beta was told it is out of sight" '^0$' "$(cat "$T/hosts/beta/state/terminal-focus")"

# beta goes down: the serve session dies, the attach pane loses its client, both report it
touch "$T/down-beta"
pkill -f "serve --stdio" || true
tmux -L e2e-beta detach-client 2>/dev/null || true
wait_for '✗ beta' 10 || true
snap=$(capture)
expect "a lost host shows ✗ with the reason or a countdown" '✗ beta +(retry in [0-9]+s|unreachable)' "$snap"
expect "its agents left the list" '^0$' "$(printf '%s\n' "$snap" | grep -c 'beta · claude' || true)"
for _ in $(seq 1 80); do OUT capture-pane -p -t "$BETA_PANE" | grep -q 'unreachable' && break; sleep 0.1; done
expect "the parked pane says why and when it retries" 'flok: beta unreachable \(Connection refused\), retry in [0-9]+s' "$(OUT capture-pane -p -t "$BETA_PANE")"
rm -f "$T/down-beta"
wait_hosts "beta=connected gamma=connected" 15 || true
expect "beta reconnects once reachable" '^beta=connected gamma=connected$' "$(snap_hosts)"
for _ in $(seq 1 80); do [ "$(tmux -L e2e-beta list-clients | wc -l | tr -d ' ')" = 1 ] && break; sleep 0.1; done
expect "the parked pane re-attaches" '^1$' "$(tmux -L e2e-beta list-clients | wc -l | tr -d ' ')"

# --- part 3: navigation from the shell (key bindings, the menu bar) crosses hosts --------------
"$BIN" host front local >/dev/null
for _ in $(seq 1 50); do [ "$(rt "r.get('front_host','')")" = "" ] && break; sleep 0.1; done
expect "host front local brings the local pane back" '^$' "$(rt "r.get('front_host','')")"
BETA_AGENT=$(eval echo "\$HOST_beta_PANE")
"$BIN" goto "beta:$BETA_AGENT" --no-focus
for _ in $(seq 1 50); do [ "$(rt "r.get('front_host','')")" = beta ] && break; sleep 0.1; done
expect "goto beta:<pane> brings beta to the front" '^beta$' "$(rt "r.get('front_host','')")"
for _ in $(seq 1 30); do tmux -L e2e-beta display -p '#{window_name}' | grep -q agent && break; sleep 0.1; done
expect "... and selects the agent window on beta" '^agent$' "$(tmux -L e2e-beta display -p '#{window_name}')"
expect "... and marks the pane seen on beta" 'seen_at' "$(cat "$T"/hosts/beta/state/seen/*.json 2>/dev/null)"
expect "goto rejects a bad host ref" 'unexpected argument' "$("$BIN" goto 'Beta:%1' 2>&1 || true)"
"$BIN" host front local >/dev/null
for _ in $(seq 1 50); do [ "$(rt "r.get('front_host','')")" = "" ] && break; sleep 0.1; done
tmux -L e2e-beta select-window -t Remote:0
rhook beta claude '{"hook_event_name":"UserPromptSubmit","session_id":"r1"}'
rhook beta claude '{"hook_event_name":"PermissionRequest","session_id":"r1","tool_name":"Bash","tool_input":{"command":"ls"},"tool_use_id":"t3"}'
wait_for 'perm:Bash' 5 || true
"$BIN" jump
for _ in $(seq 1 50); do [ "$(rt "r.get('front_host','')")" = beta ] && break; sleep 0.1; done
expect "jump crosses hosts to the blocked agent" '^beta$' "$(rt "r.get('front_host','')")"
for _ in $(seq 1 30); do tmux -L e2e-beta display -p '#{window_name}' | grep -q agent && break; sleep 0.1; done
expect "... and lands on its window" '^agent$' "$(tmux -L e2e-beta display -p '#{window_name}')"
"$BIN" next
sleep 0.6
expect "next walks the front host only" '^beta$' "$(rt "r.get('front_host','')")"
expect "front_host is published for the menu bar" '"front_host": "beta"' "$(cat "$SNAP")"

# disconnect and remove from the CLI: the sidebar follows the registry
"$BIN" host disconnect gamma >/dev/null
for _ in $(seq 1 50); do OUT list-windows -t flok -F '#{window_name}' | grep -q flok-host-gamma || break; sleep 0.1; done
expect "disconnect kills gamma's parked window" '^0$' "$(OUT list-windows -t flok -F '#{window_name}' | grep -c flok-host-gamma || true)"
wait_for ' gamma +off' 5 || true
expect "gamma's row reads off" ' gamma +off' "$(capture)"
"$BIN" host remove gamma >/dev/null
for _ in $(seq 1 50); do capture | grep -q ' gamma' || break; sleep 0.1; done
expect "remove drops gamma's row" '^0$' "$(capture | grep -c ' gamma' || true)"

# down leaves the remote servers alone
"$BIN" down
sleep 0.5
expect "the outer is gone" '^0$' "$(OUT list-sessions 2>/dev/null | wc -l | tr -d ' ')"
expect "beta's tmux survives flok down" 'Remote' "$(tmux -L e2e-beta list-sessions -F '#{session_name}')"
expect "gamma's tmux survives flok down" 'Remote' "$(tmux -L e2e-gamma list-sessions -F '#{session_name}')"
finish
