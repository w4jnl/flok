# Shared setup for the headless end-to-end scripts. Everything runs on ISOLATED tmux servers
# (e2e-inner / e2e-outer) with a private state dir and config; the real tmux server is untouched.
set -euo pipefail
unset TMUX TMUX_PANE FLOK_OUTER FLOK_RIGHT_PANE   # the suites drive their own isolated servers
R=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
PROJ=$(basename "$R")   # agent rows show the cwd base name
BIN=$R/bin/flok
go build -o "$BIN" "$R/cmd/flok"
T=$(mktemp -d)
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
[sounds]
enabled = false
${E2E_EXTRA_CONFIG:-}
CFG
FAKE=$T/fakebin; mkdir -p "$FAKE"; go build -o "$FAKE/claude" "$R/scripts/e2e/fakeagent"
export PATH="$FAKE:$PATH" FLOK_E2E_REGISTRY=$T/registry.json   # the sidebar's `claude agents --json` hits the shim
registry() { printf '%s' "$1" > "$FLOK_E2E_REGISTRY"; }     # registry '[{"pid":N,"status":"idle",...}]'
IN() { tmux -L e2e-inner "$@"; }
OUT() { tmux -L e2e-outer "$@"; }
cleanup() { OUT kill-server 2>/dev/null || true; IN kill-server 2>/dev/null || true
  for h in ${FAKE_HOSTS:-}; do tmux -L "e2e-$h" kill-server 2>/dev/null || true; done; rm -rf "$T"; }
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
finish() { echo "== $pass passed, $fail failed =="; test "$fail" -eq 0; }

# Two inner sessions; Alpha has a window "agent" running the fake claude with an idle title.
IN -f /dev/null new-session -d -s Alpha -x 200 -y 50 -c "$R"
IN new-session -d -s Beta -c "$T"
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
sleep 1.5
SIDEBAR=$(python3 -c "import json;print(json.load(open('$T/state/runtime.json'))['sidebar_pane'])")
RIGHT=$(python3 -c "import json;print(json.load(open('$T/state/runtime.json'))['right_pane'])")
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
[ "$in_mode" = "0" ] || OUT capture-pane -p -t "$RIGHT" | grep -v '^ *$' | head -12 | sed 's/^/      | /'
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
  tmux -L "e2e-$h" kill-server 2>/dev/null || true
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
