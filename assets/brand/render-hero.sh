#!/usr/bin/env bash
# Renders hero.html to flok-hero.png (1280x320) with headless Chrome; the wordmark font comes
# from Google Fonts, so it needs network. Re-run after changing the tagline in the lockup SVG.
set -euo pipefail
cd "$(dirname "$0")"
CHROME=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
"$CHROME" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 \
  --window-size=1280,320 --virtual-time-budget=10000 --screenshot="$PWD/flok-hero.png" "file://$PWD/hero.html" 2>/dev/null
sips -g pixelWidth -g pixelHeight flok-hero.png | tail -2 | tr '\n' ' '; echo
