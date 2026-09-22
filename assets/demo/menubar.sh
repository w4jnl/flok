#!/usr/bin/env bash
# Captures assets/menubar-stack.png (the status item above its open menu, transparent around
# both) from the demo scene: a second flok-bar runs against the scene's state dir, opens its own menu on
# request (FLOK_BAR_SHOT) and reports the frames; screencapture takes them. Needs the Screen
# Recording permission for the terminal. The scene has api blocked, docs done, web working.
set -euo pipefail
R=$(cd "$(dirname "$0")/../.." && pwd)
cd "$R"
D=$("$R/assets/demo/scene.sh")
W=$(mktemp -d "${TMPDIR:-/tmp}/flok-shot.XXXX")
trap 'kill "${BAR:-}" 2>/dev/null; wait "${BAR:-}" 2>/dev/null || true; tmux -L demo-outer kill-server 2>/dev/null; tmux -L demo-inner kill-server 2>/dev/null; rm -rf "$D" "$W"' EXIT
cp "$D/screens/api-blocked.txt" "$D/screens/api-working.txt"
"$D/hook.sh" api '{"hook_event_name":"PermissionRequest","session_id":"api-1","tool_name":"Bash","tool_input":{"command":"npm run db:reset && npm test"},"tool_use_id":"t4"}'
# a bar built with the latest release's version string, so the version row reads like a release
ver=$(git -C "$R" describe --tags --abbrev=0 | sed 's/^v//')
CGO_ENABLED=1 go build -ldflags "-s -w -X github.com/w4jnl/flok/internal/cli.Version=$ver -X main.version=$ver" -o "$W/flok-bar" ./cmd/flok-bar
sleep 2.5   # the sidebar publishes snapshot.json
FLOK_STATE=$D/state FLOK_CONFIG=$D/config.toml FLOK_BAR_SHOT=$W "$W/flok-bar" >/dev/null 2>&1 &
BAR=$!
for _ in $(seq 1 40); do [ -f "$W/menu.json" ] && break; sleep 0.2; done
[ -f "$W/menu.json" ] || { echo "menubar.sh: the bar did not report its menu" >&2; exit 1; }
j() { python3 -c "import json,sys;print(int(round(json.load(open('$1'))['$2'])))"; }
screencapture -x -o -l "$(j "$W/menu.json" window)" "$W/menu.png"
x=$(j "$W/item.json" x); w=$(j "$W/item.json" w); h=$(j "$W/item.json" h)
screencapture -x -R "$((x - 10)),0,$((w + 20)),$h" "$W/item.png"
# normalise to 1x (a Retina main display captures at 2x) and trim the translucent bottom edge of
# the menu bar strip, which shows the wallpaper
norm() { # in, out, width in points, bottom trim in px
  local px; px=$(sips -g pixelWidth "$1" | awk '/pixelWidth/{print $2}')
  local scale=$(( px / $3 )); [ "$scale" -lt 1 ] && scale=1
  ffmpeg -y -loglevel error -i "$1" -vf "scale=iw/$scale:ih/$scale:flags=lanczos,crop=iw:ih-$4:0:0" "$2"
}
norm "$W/menu.png" "$W/menu1x.png" "$(j "$W/menu.json" w)" 0
norm "$W/item.png" "$W/item1x.png" "$((w + 20))" 6
# stack: the item centred above the menu on a transparent canvas, with 24 px of transparent
# margin on the right, so the README can float the pair left of a paragraph with a gap
mw=$(sips -g pixelWidth "$W/menu1x.png" | awk '/pixelWidth/{print $2}'); mh=$(sips -g pixelHeight "$W/menu1x.png" | awk '/pixelHeight/{print $2}')
iw=$(sips -g pixelWidth "$W/item1x.png" | awk '/pixelWidth/{print $2}'); ih=$(sips -g pixelHeight "$W/item1x.png" | awk '/pixelHeight/{print $2}')
gap=24; cw=$(( mw + 4 + gap )); ch=$(( ih + 8 + mh ))
ffmpeg -y -loglevel error -f lavfi -i "color=c=black@0.0:s=${cw}x${ch},format=rgba" -i "$W/item1x.png" -i "$W/menu1x.png" \
  -filter_complex "[0][1]overlay=($cw-$gap-$iw)/2:0[a];[a][2]overlay=2:$((ih + 8))" -frames:v 1 assets/menubar-stack.png
ls -la assets/menubar-stack.png
