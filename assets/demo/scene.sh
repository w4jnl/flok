#!/usr/bin/env bash
# Builds the README demo scene on two isolated tmux servers (demo-inner, demo-outer) with the
# e2e fake agent standing in for Claude Code, so the states are deterministic and no real
# project is on screen. Leaves flok attached-ready: `tmux -L demo-outer attach` shows it.
# Exports nothing; prints the scene dir. `hook <session> '<json>'` in that dir replays a hook.
set -euo pipefail
R=$(cd "$(dirname "$0")/../.." && pwd)
D=${DEMO_DIR:-$(mktemp -d /tmp/flok-demo.XXXX)}
mkdir -p "$D/bin" "$D/state" "$D/proj/api" "$D/proj/web" "$D/proj/docs" "$D/screens"
cp "$R/assets/demo/screens/"*.txt "$D/screens/"
make -C "$R" build >/dev/null 2>&1
go build -o "$D/bin/claude" "$R/scripts/e2e/fakeagent"
export PATH="$R/bin:$D/bin:$PATH" FLOK_CONFIG=$D/config.toml FLOK_STATE=$D/state FLOK_E2E_REGISTRY=$D/registry.json
cat > "$FLOK_CONFIG" <<CFG
[inner]
socket = "demo-inner"
session = "web"
[outer]
socket = "demo-outer"
[sounds]
enabled = false
[bar]
enabled = false
CFG
tmux -L demo-inner kill-server 2>/dev/null || true
tmux -L demo-outer kill-server 2>/dev/null || true
{
  printf 'set -g mouse on\nset -g status-style "bg=default,fg=colour244"\nset -g status-left " #S "\nset -g status-right ""\nset -g window-status-current-format "#[fg=colour252]#W"\nset -g window-status-format "#W"\n'
  flok install --tmux | sed -n '/>>> flok >>>/,/<<< flok <<</p'
} > "$D/inner.conf"
IN() { tmux -L demo-inner "$@"; }
IN -f "$D/inner.conf" new-session -d -s api -n claude -c "$D/proj/api" -x 180 -y 45 "$D/bin/claude $D/screens/api-working.txt"
IN new-session -d -s web -n claude -c "$D/proj/web" "$D/bin/claude $D/screens/web.txt"
IN new-session -d -s docs -n claude -c "$D/proj/docs" "$D/bin/claude $D/screens/docs.txt"
for s in api web docs; do
  IN new-window -d -t "$s" -n shell -c "$D/proj/$s"
  IN select-pane -t "$s:claude" -T "✳ $s"
done
sleep 0.5
SOCK=$(IN display -p '#{socket_path}')
hook() { # session, json: replay a Claude Code hook against that session's agent pane
  local pane; pane=$(IN display -p -t "$1:claude" '#{pane_id}')
  printf '%s' "$2" | TMUX="$SOCK,0,0" TMUX_PANE="$pane" flok hook claude
}
cat > "$D/hook.sh" <<HOOK
#!/usr/bin/env bash
export PATH="$R/bin:$D/bin:\$PATH" FLOK_CONFIG=$FLOK_CONFIG FLOK_STATE=$FLOK_STATE FLOK_E2E_REGISTRY=$FLOK_E2E_REGISTRY
pane=\$(tmux -L demo-inner display -p -t "\$1:claude" '#{pane_id}')
printf '%s' "\$2" | TMUX="$SOCK,0,0" TMUX_PANE="\$pane" flok hook claude
HOOK
chmod +x "$D/hook.sh"
# api: mid-turn, running the tests.  web: mid-turn, reading (the client starts here).
# docs: finished while nobody looked.
hook api  '{"hook_event_name":"SessionStart","source":"startup","session_id":"api-1","cwd":"'"$D/proj/api"'"}'
hook api  '{"hook_event_name":"UserPromptSubmit","session_id":"api-1"}'
hook api  '{"hook_event_name":"PreToolUse","session_id":"api-1","tool_name":"Bash","tool_input":{"command":"npm test"},"tool_use_id":"t1"}'
hook web  '{"hook_event_name":"SessionStart","source":"startup","session_id":"web-1","cwd":"'"$D/proj/web"'"}'
hook web  '{"hook_event_name":"UserPromptSubmit","session_id":"web-1"}'
hook web  '{"hook_event_name":"PreToolUse","session_id":"web-1","tool_name":"Read","tool_input":{"file_path":"src/pages/checkout.tsx"},"tool_use_id":"t2"}'
hook docs '{"hook_event_name":"SessionStart","source":"startup","session_id":"docs-1","cwd":"'"$D/proj/docs"'"}'
hook docs '{"hook_event_name":"UserPromptSubmit","session_id":"docs-1"}'
hook docs '{"hook_event_name":"PreToolUse","session_id":"docs-1","tool_name":"Write","tool_input":{"file_path":"docs/releases/2.3.md"},"tool_use_id":"t3"}'
hook docs '{"hook_event_name":"PostToolUse","session_id":"docs-1","tool_name":"Write","tool_use_id":"t3"}'
hook docs '{"hook_event_name":"Stop","session_id":"docs-1"}'
IN select-window -t web:claude
flok up --detach
sleep 1
echo "$D"
