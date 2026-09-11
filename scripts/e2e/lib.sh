# Shared setup for the headless end-to-end scripts. Everything runs on ISOLATED tmux servers
# (e2e-inner / e2e-outer) with a private state dir and config; the real tmux server is untouched.
set -euo pipefail
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
registry_poll_ms = 60000
[sounds]
enabled = false
${E2E_EXTRA_CONFIG:-}
CFG
FAKE=$T/fakebin; mkdir -p "$FAKE"; go build -o "$FAKE/claude" "$R/scripts/e2e/fakeagent"
IN() { tmux -L e2e-inner "$@"; }
OUT() { tmux -L e2e-outer "$@"; }
cleanup() { OUT kill-server 2>/dev/null || true; IN kill-server 2>/dev/null || true; rm -rf "$T"; }
trap cleanup EXIT
IN kill-server 2>/dev/null || true; OUT kill-server 2>/dev/null || true

pass=0; fail=0
ok()  { printf 'PASS  %s\n' "$1"; pass=$((pass+1)); }
bad() { printf 'FAIL  %s\n' "$1"; fail=$((fail+1)); }
expect() { # name, pattern (grep -E), text
  if printf '%s\n' "$3" | grep -qE "$2"; then ok "$1"; else bad "$1 (pattern: $2)"; printf '%s\n' "$3" | grep -v '^ *$' | sed 's/^/      | /'; fi; }
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

"$BIN" up --detach
sleep 1.5
SIDEBAR=$(python3 -c "import json;print(json.load(open('$T/state/runtime.json'))['sidebar_pane'])")
RIGHT=$(python3 -c "import json;print(json.load(open('$T/state/runtime.json'))['right_pane'])")
wait_for 'fake-agent' 5 || true

# hook <agent> <json>  — replays a hook payload as if the agent in $AGENT had emitted it.
hook() { printf '%s' "$2" | TMUX="$INNER_SOCK,0,0" TMUX_PANE="$AGENT" "$BIN" hook "$1"; }
