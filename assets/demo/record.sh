#!/usr/bin/env bash
# Records assets/demo.gif: scene.sh builds the scene, vhs (brew install vhs) plays demo.tape
# into a frames directory and ffmpeg assembles the GIF. A timeline in the background turns api
# blocked and, after the answer, working again, in step with the sleeps in the tape.
set -euo pipefail
R=$(cd "$(dirname "$0")/../.." && pwd)
cd "$R"
FPS=20
D=$("$R/assets/demo/scene.sh")
W=$(mktemp -d "${TMPDIR:-/tmp}/flok-vhs.XXXX")   # vhs work dir on the system volume
trap 'tmux -L demo-outer kill-server 2>/dev/null; tmux -L demo-inner kill-server 2>/dev/null; rm -rf "$D" "$W"' EXIT
cp assets/demo.tape "$W/demo.tape"
(
  sleep 8   # vhs needs 2-3 s to start, then hides the attach for 2 s: this lands a few seconds into the GIF
  cp "$D/screens/api-blocked.txt" "$D/screens/api-working.txt"
  "$D/hook.sh" api '{"hook_event_name":"PermissionRequest","session_id":"api-1","tool_name":"Bash","tool_input":{"command":"npm run db:reset && npm test"},"tool_use_id":"t4"}'
  # the tape jumps to api with prefix o and answers "1"; the fake agent echoes it, which is the cue
  for _ in $(seq 1 150); do tmux -L demo-inner capture-pane -p -t api:claude | grep -qx '1' && break; sleep 0.2; done
  sleep 0.6
  cp "$D/screens/api-resumed.txt" "$D/screens/api-working.txt"
  sleep 2.5   # let the 2 s screen poll see the dialog gone before the hook ends the block; the other order shows a stray "prompt" for a poll
  "$D/hook.sh" api '{"hook_event_name":"PostToolUse","session_id":"api-1","tool_name":"Bash","tool_use_id":"t4"}'
) &
(cd "$W" && vhs demo.tape)
wait
F=$W/frames
n=$(ls "$F"/frame-text-*.png | wc -l | tr -d ' ')
echo "frames: $n"
ffmpeg -y -loglevel error -framerate $FPS -i "$F/frame-text-%05d.png" -framerate $FPS -i "$F/frame-cursor-%05d.png" \
  -filter_complex "[0][1]overlay,split[a][b];[a]palettegen=max_colors=128:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle" \
  "$W/demo.gif"
cp "$W/demo.gif" assets/demo.gif
ls -la assets/demo.gif
