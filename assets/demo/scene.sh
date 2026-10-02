#!/usr/bin/env bash
# Builds the README demo scene on two isolated tmux servers (demo-inner, demo-outer) with the
# e2e fake agent standing in for Claude Code, so the states are deterministic and no real
# project is on screen. Leaves flok attached-ready: `tmux -L demo-outer attach` shows it.
# Exports nothing; prints the scene dir. `hook <session> '<json>'` in that dir replays a hook.
# DEMO_HOST=1 adds a remote host "beta": a third tmux server (demo-beta) behind a fake ssh on the
# scene's PATH, with one agent building, so the servers panel and the menu bar's servers block show.
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
if [ -n "${DEMO_HOST:-}" ]; then
  H=$D/host; mkdir -p "$H/state" "$H/home" "$H/bin" "$D/proj/build"
  REAL_TMUX=$(command -v tmux)
  cat > "$D/bin/ssh" <<SSH
#!/usr/bin/env bash
# the demo's ssh: "beta" is a tmux server on this machine (demo-beta) with its own flok state
host=; while [ \$# -gt 0 ]; do case "\$1" in -o) shift 2 ;; -*) shift ;; *) host=\$1; shift; break ;; esac; done
[ "\$host" = beta ] || { echo "ssh: Could not resolve hostname \$host" >&2; exit 255; }
unset TMUX TMUX_PANE FLOK_OUTER FLOK_RIGHT_PANE
export PATH="$H/bin:\$PATH" FLOK_STATE=$H/state FLOK_CONFIG=$H/config.toml HOME=$H/home SSH_CONNECTION="127.0.0.1 1 127.0.0.1 22"
[ \$# -gt 0 ] || exit 0
exec sh -c "\$*"
SSH
  printf '#!/usr/bin/env bash\nargs=(); while [ $# -gt 0 ]; do case "$1" in -L) shift 2 ;; *) args+=("$1"); shift ;; esac; done\nexec "%s" -L demo-beta "${args[@]}"\n' "$REAL_TMUX" > "$H/bin/tmux"
  printf '#!/usr/bin/env bash\nexec "%s" "$@"\n' "$R/bin/flok" > "$H/bin/flok"
  chmod +x "$D/bin/ssh" "$H/bin/tmux" "$H/bin/flok"
  printf '[inner]\nsocket = "demo-beta"\n[sounds]\nenabled = false\n[bar]\nenabled = false\n' > "$H/config.toml"
  tmux -L demo-beta kill-server 2>/dev/null || true
  tmux -L demo-beta -f /dev/null new-session -d -s build -n claude -c "$D/proj/build" -x 180 -y 45 "$D/bin/claude $D/screens/web.txt"
  tmux -L demo-beta new-window -d -t build -n shell -c "$D/proj/build"
  tmux -L demo-beta select-pane -t build:claude -T "✳ build"
  BSOCK=$(tmux -L demo-beta display -p '#{socket_path}'); BPANE=$(tmux -L demo-beta display -p -t build:claude '#{pane_id}')
  bhook() { printf '%s' "$1" | TMUX="$BSOCK,0,0" TMUX_PANE="$BPANE" FLOK_STATE=$H/state FLOK_CONFIG=$H/config.toml flok hook claude; }
  bhook '{"hook_event_name":"SessionStart","source":"startup","session_id":"build-1","cwd":"'"$D/proj/build"'"}'
  bhook '{"hook_event_name":"UserPromptSubmit","session_id":"build-1"}'
  bhook '{"hook_event_name":"PreToolUse","session_id":"build-1","tool_name":"Bash","tool_input":{"command":"npm run build"},"tool_use_id":"b1"}'
  flok host add beta >/dev/null
fi
flok up --detach
sleep 1
echo "$D"
