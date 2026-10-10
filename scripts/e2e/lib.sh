# Shared setup for the headless end-to-end scripts. Everything runs on ISOLATED tmux servers
# (e2e-inner / e2e-outer) with a private state dir and config; the real tmux server is untouched.
set -Eeuo pipefail   # -E: the ERR trap below reaches functions and command substitutions
unset TMUX TMUX_PANE FLOK_OUTER FLOK_RIGHT_PANE   # the suites drive their own isolated servers
R=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# a debug run also keeps the outer tmux server's own log (tmux -vv; E2E_TMUX_VERBOSE=0 opts out)
[ -z "${FLOK_DEBUG:-}" ] || [ "${E2E_TMUX_VERBOSE:-1}" = 0 ] || export FLOK_TMUX_VERBOSE=1
# An abort (a command failing under set -e, not a failed check) names its line and command, and
# the summary line says so, so a run that died is told apart from one that finished with failures.
aborted=''
on_err() { local rc=$?; [ "${BASH_SUBSHELL:-0}" -eq 0 ] || return 0; [ -z "$aborted" ] || return 0
  aborted="${BASH_SOURCE[1]:-$0}:${BASH_LINENO[0]}"; printf 'ABORT %s: %s (exit %d)\n' "$aborted" "$BASH_COMMAND" "$rc"; }
trap on_err ERR
trap 'exit 124' TERM   # the runner's watchdog: leave through EXIT so cleanup below runs
PROJ=$(basename "$R")   # agent rows show the cwd base name
BIN=$R/bin/flok
go build -o "$BIN" "$R/cmd/flok"
T=$(mktemp -d)
cd "$T"   # tmux -vv (a debug run) writes its logs into the cwd: here, never next to the sources (go builds use -C "$R")
export FLOK_STATE=$T/state FLOK_CONFIG=$T/config.toml
cat > "$T/config.toml" <<CFG
[inner]
socket = "e2e-inner"
[outer]
socket = "e2e-outer"
session = "flok"
[sidebar]
width = 28
poll_ms = 250
registry_poll_ms = 1000
screen_poll_ms = 500
[sounds]
enabled = false
[keys.map]
Tab = "last session"
";" = "last pane"
BTab = "last server"
${E2E_EXTRA_CONFIG:-}
CFG
FAKE=$T/fakebin; mkdir -p "$FAKE"; go build -C "$R" -o "$FAKE/claude" ./scripts/e2e/fakeagent
export PATH="$FAKE:$PATH" FLOK_E2E_REGISTRY=$T/registry.json   # the sidebar's `claude agents --json` hits the shim
registry() { printf '%s' "$1" > "$FLOK_E2E_REGISTRY"; }     # registry '[{"pid":N,"status":"idle",...}]'
IN() { tmux -L e2e-inner "$@"; }
OUT() { tmux -L e2e-outer "$@"; }
cleanup() { [ -n "${summary_done:-}" ] || echo "== ${pass:-0} passed, ${fail:-0} failed, aborted at ${aborted:-unknown} =="
  OUT kill-server 2>/dev/null || true; IN kill-server 2>/dev/null || true
  for h in ${FAKE_HOSTS:-}; do tmux -L "e2e-$h" kill-server 2>/dev/null || true; done
  if [ -n "${FLOK_DEBUG:-}" ]; then # keep the sidebar/remote/serve logs of a debug run
    d=/tmp/flok-e2e-logs/$(basename "$0" .sh); rm -rf "$d"; mkdir -p "$d"
    cp "$T"/state/*.log "$d"/ 2>/dev/null || true
    for h in ${FAKE_HOSTS:-}; do cp "$T/hosts/$h/state/serve.log" "$d/serve-$h.log" 2>/dev/null || true; cp "$T/hosts/$h/ssh.log" "$d/ssh-$h.log" 2>/dev/null || true; done
    mv "$T"/tmux-*.log "$R"/tmux-*.log "$d"/ 2>/dev/null || true   # tmux -vv's server/client/out logs
  fi
  rm -rf "$T"; }
trap cleanup EXIT
IN kill-server 2>/dev/null || true; OUT kill-server 2>/dev/null || true

pass=0; fail=0
ok()  { printf 'PASS  %s\n' "$1"; pass=$((pass+1)); }
bad() { printf 'FAIL  %s\n' "$1"; fail=$((fail+1)); }
expect() { # name, pattern (grep -E), text — matched against a file: a pipe into grep -q would
  # end with SIGPIPE on large text (pipefail then reports a match as a failure)
  printf '%s\n' "$3" > "$T/expect.txt"
  if grep -qE "$2" "$T/expect.txt"; then ok "$1"; else bad "$1 (pattern: $2)"; sed '/^ *$/d; s/^/      | /' "$T/expect.txt"; fi; }
capture() { OUT capture-pane -p -t "$SIDEBAR"; }
wait_for() { # pattern, seconds
  local i; for i in $(seq 1 $(( ${2:-3} * 10 ))); do capture | grep -qE "$1" && return 0; sleep 0.1; done; return 1; }
finish() { summary_done=1; echo "== $pass passed, $fail failed =="; if [ "$fail" -eq 0 ]; then exit 0; else exit 1; fi; }
# expect_soon [-t SECS] NAME PATTERN CMD [ARGS…]: run CMD every 0.1 s until its output matches
# PATTERN (grep -E), then PASS; FAIL with the last output after SECS (default 5). The shape for
# every transition: a render that is late never fails it, a state that never arrives does.
expect_soon() { local secs=5; if [ "$1" = -t ]; then secs=$2; shift 2; fi
  local name=$1 pat=$2 i; shift 2
  for i in $(seq 1 $(( secs * 10 ))); do
    "$@" > "$T/expect.txt" 2>/dev/null || true
    if grep -qE "$pat" "$T/expect.txt"; then ok "$name"; return 0; fi
    sleep 0.1
  done
  bad "$name (pattern: $pat, after ${secs}s)"; sed '/^ *$/d; s/^/      | /' "$T/expect.txt"; return 0; }
# a wait after an action must only be satisfiable after the transition: wait_gone leaves the old
# state first, wait_json watches a value only the new state writes
wait_gone() { local i; for i in $(seq 1 $(( ${2:-3} * 10 ))); do capture | grep -qE "$1" || return 0; sleep 0.1; done; return 1; }
wait_file() { local i; for i in $(seq 1 $(( ${3:-5} * 10 ))); do grep -qE "$2" "$1" 2>/dev/null && return 0; sleep 0.1; done; return 1; }
json() { python3 -c "import json,sys;s=json.load(open(sys.argv[1]));print($2)" "$1" 2>/dev/null || true; }   # '' on any error, never aborts
wait_json() { local i; for i in $(seq 1 $(( ${3:-5} * 10 ))); do
  python3 -c "import json,sys;s=json.load(open(sys.argv[1]));sys.exit(0 if ($2) else 1)" "$1" 2>/dev/null && return 0; sleep 0.1; done; return 1; }
# kill_server SOCKET [SECS]: guarded kill-server that waits for the old pid, so a new-session
# on the socket never meets the dying server ("server exited unexpectedly")
kill_server() { local pid; pid=$(tmux -L "$1" display -p '#{pid}' 2>/dev/null || true); tmux -L "$1" kill-server 2>/dev/null || true
  [ -z "$pid" ] || for _ in $(seq 1 $(( ${2:-5} * 10 ))); do kill -0 "$pid" 2>/dev/null || break; sleep 0.1; done; return 0; }
server_state() { if tmux -L "$1" list-sessions >/dev/null 2>&1; then echo alive; else echo gone; fi; }
serve_pid() { json "$T/hosts/$1/state/served" "s['pid']"; }                      # '' when no serve holds the host
kill_serve() { local p; p=$(serve_pid "$1"); [ -z "$p" ] || kill "$p" 2>/dev/null || true; }   # this run's serve only, never pkill -f
paint() { if [ "$2" = - ]; then cat > "$1.tmp"; else cp "$2" "$1.tmp"; fi; mv -f "$1.tmp" "$1"; }   # atomic screen fixture
sound_guard_passed() { local d=${1:-$T/state}/sounds i; for i in $(seq 1 40); do   # the 2 s per-pane repeat guard (notify.Allowed)
  python3 -c "import glob,os,sys,time;sys.exit(0 if all(time.time()-os.path.getmtime(f)>2.2 for f in glob.glob(sys.argv[1]+'/*.stamp')) else 1)" "$d" && return 0; sleep 0.1; done; return 1; }
RT=$T/state/runtime.json
SNAP=$T/state/snapshot.json
rt() { python3 -c "import json,sys;r=json.load(open('$RT'));print(eval(sys.argv[1]))" "$1" 2>/dev/null || true; }
snap_hosts() { python3 -c "import json;print(' '.join(h['name']+'='+h['state'] for h in json.load(open('$SNAP')).get('hosts',[])))" 2>/dev/null || true; }
wait_hosts() { local i; for i in $(seq 1 $(( ${2:-10} * 10 ))); do [ "$(snap_hosts)" = "$1" ] && return 0; sleep 0.1; done; return 1; }
# ready_up [SECS]: after `flok up --detach`: waits for runtime.json to name both panes (not a
# fixed sleep), sets SIDEBAR/RIGHT, waits for the first frame; a sidebar that never comes up aborts
ready_up() { local secs=${1:-10}
  wait_json "$RT" "s.get('sidebar_pane') and s.get('right_pane')" "$secs" || { echo "ABORT runtime.json not ready after ${secs}s"; ls -la "$T/state" 2>/dev/null || true; return 1; }
  SIDEBAR=$(json "$RT" "s['sidebar_pane']"); RIGHT=$(json "$RT" "s['right_pane']")
  wait_for '\[flok\]|^sessions' 5 || true; }

# Two inner sessions; Alpha has a window "agent" running the fake claude with an idle title.
IN -f /dev/null new-session -d -s Alpha -x 200 -y 50 -c "$R"
IN new-session -d -s Beta -c "$T"
[ -z "${E2E_INNER_PREFIX:-}" ] || IN set-option -g prefix "$E2E_INNER_PREFIX"   # m10: the hosts take it while connected
IN new-window -t Alpha -n agent -c "$R"
AGENT=$(IN display -p -t Alpha:agent '#{pane_id}')
IN select-pane -t "$AGENT" -T "✳ fake-agent"
IN send-keys -t "$AGENT" "exec $FAKE/claude" Enter
IN select-window -t Alpha:0
sleep 0.5
INNER_SOCK=$(IN display -p '#{socket_path}')
# the branch the sidebar shows for a session whose active pane sits in this repo: the checked-out
# branch, or the short commit id when HEAD is detached (CI runs on tags)
REPO_BRANCH=$(git -C "$R" symbolic-ref --short -q HEAD || git -C "$R" rev-parse HEAD | cut -c1-7)

"$BIN" up --detach
ready_up
wait_for 'fake-agent' 5 || true

# tmux version of this host: suites skip what older servers cannot do (RHEL 8 ships 2.7, RHEL 9 3.2a)
TMUX_VER=$(tmux -V | sed -E 's/^tmux //')
TMUX_MAJOR=$(printf '%s' "$TMUX_VER" | sed -nE 's/^[^0-9]*([0-9]+)\.([0-9]+).*/\1/p')
TMUX_MINOR=$(printf '%s' "$TMUX_VER" | sed -nE 's/^[^0-9]*([0-9]+)\.([0-9]+).*/\2/p')
: "${TMUX_MAJOR:=99}" "${TMUX_MINOR:=0}"
tmux_at_least() { [ "$TMUX_MAJOR" -gt "$1" ] || { [ "$TMUX_MAJOR" -eq "$1" ] && [ "$TMUX_MINOR" -ge "$2" ]; }; }
# The outer config must load cleanly on this version: tmux does not abort on an unknown option,
# it shows the errors in a view-mode overlay on the work pane, which no client would ever see here.
in_mode=$(OUT display -p -t "$RIGHT" '#{pane_in_mode}')
expect "outer config loaded without errors on tmux $TMUX_VER" '^0$' "$in_mode"
[ "$in_mode" = "0" ] || OUT capture-pane -p -t "$RIGHT" | grep -v '^ *$' | head -12 | sed 's/^/      | /' || true
expect "runtime.json records the tmux version" "^$TMUX_MAJOR\\.$TMUX_MINOR" "$(python3 -c "import json;print(json.load(open('$T/state/runtime.json')).get('tmux_version',''))")"

# hook <agent> <json>  — replays a hook payload as if the agent in $AGENT had emitted it.
hook() { printf '%s' "$2" | TMUX="$INNER_SOCK,0,0" TMUX_PANE="$AGENT" "$BIN" hook "$1"; }

# ---- remote hosts (m10) -------------------------------------------------------------------
# fake_ssh_setup: $FAKE/ssh runs "ssh [options] [--] <host> <command…>" locally under the host's
# private environment (state dir, config, HOME, a PATH whose tmux and flok are shims); $T/down-<host>
# makes it fail like a refused connection, $T/auth-<host> like a rejected key. The real ssh is
# never used by the suites.
fake_ssh_setup() {
  REAL_TMUX=$(command -v tmux)
  cat > "$FAKE/ssh" <<SSH
#!/usr/bin/env bash
host=""
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) shift 2 ;;
    -t|-T|-q|-tt|-o*) shift ;;
    --) shift; host=\$1; shift; break ;;
    -*) shift ;;
    *) host=\$1; shift; break ;;
  esac
done
[ -n "\$host" ] || { echo "usage: ssh host command" >&2; exit 255; }
[ -e "$T/down-\$host" ] && { echo "ssh: connect to host \$host port 22: Connection refused" >&2; exit 255; }
[ -e "$T/auth-\$host" ] && { echo "\$host: Permission denied (publickey)." >&2; exit 255; }
[ -d "$FAKE/hosts/\$host" ] || { echo "ssh: Could not resolve hostname \$host: nodename nor servname provided" >&2; exit 255; }
unset TMUX TMUX_PANE FLOK_OUTER FLOK_RIGHT_PANE
export PATH="$FAKE/hosts/\$host/bin:\$PATH" FLOK_STATE="$T/hosts/\$host/state" FLOK_CONFIG="$T/hosts/\$host/config.toml" \\
  HOME="$T/hosts/\$host/home" FLOK_E2E_REGISTRY="$T/hosts/\$host/registry.json" SSH_CONNECTION="127.0.0.1 1 127.0.0.1 22"
[ \$# -gt 0 ] || exit 0
[ -z "\${FLOK_DEBUG:-}" ] || printf '%s\n' "\$*" >> "$T/hosts/\$host/ssh.log"   # what flok ran there (debug runs)
exec sh -c "\$*"
SSH
  chmod +x "$FAKE/ssh"
}
# fake_host <name>: an isolated tmux server e2e-<name> with a fake agent window (like Alpha:agent
# here), reachable through the fake ssh as <name>. Sounds on that host append the file name to
# $T/hosts/<name>/played instead of playing.
fake_host() {
  local h=$1 d=$T/hosts/$1 pane
  mkdir -p "$d/state" "$d/home" "$FAKE/hosts/$h/bin"
  cat > "$FAKE/hosts/$h/bin/tmux" <<TMUX
#!/usr/bin/env bash
# the host's tmux: whatever socket is asked for, it is the isolated server e2e-$h
args=(); while [ \$# -gt 0 ]; do case "\$1" in -L) shift 2 ;; *) args+=("\$1"); shift ;; esac; done
exec "$REAL_TMUX" -L "e2e-$h" "\${args[@]}"
TMUX
  printf '#!/usr/bin/env bash\nexec "%s" "$@"\n' "$BIN" > "$FAKE/hosts/$h/bin/flok"
  chmod +x "$FAKE/hosts/$h/bin/tmux" "$FAKE/hosts/$h/bin/flok"
  cat > "$d/config.toml" <<CFG
[inner]
socket = "e2e-$h"
[sidebar]
poll_ms = 250
registry_poll_ms = 1000
[sounds]
enabled = true
command = "echo {file} >> $d/played"
CFG
  kill_server "e2e-$h"   # a previous server (with the parked pane attached) takes a moment to go
  tmux -L "e2e-$h" -f /dev/null new-session -d -s Remote -x 200 -y 50 -c "$R"
  tmux -L "e2e-$h" new-window -t Remote -n agent -c "$R"
  pane=$(tmux -L "e2e-$h" display -p -t Remote:agent '#{pane_id}')
  tmux -L "e2e-$h" select-pane -t "$pane" -T "✳ remote-agent"
  tmux -L "e2e-$h" send-keys -t "$pane" "exec $FAKE/claude" Enter
  tmux -L "e2e-$h" select-window -t Remote:0
  FAKE_HOSTS="${FAKE_HOSTS:-} $h"
  eval "HOST_${h}_PANE=\$pane; HOST_${h}_SOCK=\$(tmux -L e2e-$h display -p '#{socket_path}')"
  sleep 0.3
}
# rhook <host> <agent> <json>: replays a hook payload on the host, as its agent pane would emit it.
rhook() { local sock pane; eval "sock=\$HOST_$1_SOCK; pane=\$HOST_$1_PANE"
  printf '%s' "$3" | TMUX="$sock,0,0" TMUX_PANE="$pane" FLOK_STATE="$T/hosts/$1/state" FLOK_CONFIG="$T/hosts/$1/config.toml" "$BIN" hook "$2"; }
