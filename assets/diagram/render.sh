#!/usr/bin/env bash
# Renders architecture.html to assets/architecture.png (1180x780 CSS px at 2x) with headless Chrome;
# the font comes from Google Fonts, so it needs network. Re-run after changing the page.
set -euo pipefail
cd "$(dirname "$0")"
CHROME=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
"$CHROME" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=2 \
  --window-size=1180,780 --virtual-time-budget=10000 --screenshot="$PWD/../architecture.png" "file://$PWD/architecture.html" 2>/dev/null
sips -g pixelWidth -g pixelHeight ../architecture.png | tail -2 | tr '\n' ' '; echo
