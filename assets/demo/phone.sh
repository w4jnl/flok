#!/usr/bin/env bash
# Captures assets/phone-screens.jpg, the README's phone screenshots: a local flok-relay with two
# made-up instances (assets/demo/phoneinst: home with a remote host, office), the app in the
# iPhone 15 Pro simulator pointed at it, and the FlokScreens UI test (make ios-screens) walking
# it: the list, an agent's screen, a push and its card, the Lock Screen with the Live Activity.
# Pushes reach the simulator only through `simctl push`, so the test asks for one with a file.
# phones.html frames the shots and headless Chrome renders them (fonts from Google Fonts: needs
# network). Needs Xcode, xcodegen and signing (Local.xcconfig): the Live Activity wants it.
set -euo pipefail
R=$(cd "$(dirname "$0")/../.." && pwd)
cd "$R"
W=$(mktemp -d "${TMPDIR:-/tmp}/flok-phone.XXXX")
mkdir -p "$W/shots" "$W/data"
PORT=${PHONE_PORT:-8787}
SIM=${PHONE_SIM:-iPhone 15 Pro}
U=$(xcrun simctl list devices available | sed -n "s/^ *$SIM (\([0-9A-F-]*\)).*/\1/p" | head -1)
[ -n "$U" ] || { echo "phone.sh: no '$SIM' simulator" >&2; exit 1; }
cleanup() {
  kill "${RELAY:-}" "${INST:-}" "${PUSHER:-}" 2>/dev/null || true
  xcrun simctl status_bar "$U" clear 2>/dev/null || true
  rm -rf "$W"
}
trap cleanup EXIT

make relay >/dev/null
go build -o "$W/phoneinst" ./assets/demo/phoneinst
IT=$(bin/flok-relay token); DT=$(bin/flok-relay token)
FLOK_RELAY_INSTANCE_TOKENS=$IT FLOK_RELAY_DEVICE_TOKENS=$DT bin/flok-relay serve -listen "127.0.0.1:$PORT" -data "$W/data" > "$W/relay.log" 2>&1 &
RELAY=$!
sleep 1
"$W/phoneinst" -url "http://127.0.0.1:$PORT" -token "$IT" -screens assets/demo/screens > "$W/inst.log" 2>&1 &
INST=$!

xcrun simctl boot "$U" 2>/dev/null || true
xcrun simctl bootstatus "$U" -b >/dev/null
xcrun simctl ui "$U" appearance dark
xcrun simctl status_bar "$U" override --time 9:41 --batteryState charged --batteryLevel 100 --cellularMode active --cellularBars 4 --wifiBars 3 --dataNetwork wifi --operatorName ''
xcrun simctl uninstall "$U" nl.w4j.flok 2>/dev/null || true   # a fresh install asks for notifications again,
xcrun simctl keychain "$U" reset                                 # and knows no token but the one it is given

# the push the test asks for: what the relay sends for api's permission prompt
cat > "$W/perm.apns" <<'APNS'
{"aps":{"alert":{"title":"api","subtitle":"home · api","body":"● needs you: perm:Bash"},"category":"FLOK_PERMISSION","sound":"flok-request.caf","badge":2,"mutable-content":1,"thread-id":"home","interruption-level":"time-sensitive"},
 "instance":"home","pane":"%3","kind":"blocked","reason":"permission:Bash","agent":"api","host":"","session":"api","push_kind":"permission"}
APNS
( while :; do
    if [ -f "$W/shots/push" ]; then xcrun simctl push "$U" nl.w4j.flok "$W/perm.apns" >/dev/null; rm -f "$W/shots/push"; fi
    sleep 0.5
  done ) &
PUSHER=$!

make ios-screens RELAY="http://127.0.0.1:$PORT" TOKEN="$DT" SHOTS="$W/shots" IOS_SCREENS_SIM="platform=iOS Simulator,id=$U" || true
for s in list agent card lock; do
  [ -s "$W/shots/$s.png" ] || { echo "phone.sh: no $s screenshot (see the FlokScreens test output above)" >&2; exit 1; }
done

cp assets/demo/phones.html "$W/phones.html"
CHROME=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
"$CHROME" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=2 \
  --window-size=1280,712 --virtual-time-budget=10000 --screenshot="$W/phones.png" "file://$W/phones.html" 2>/dev/null
sips -s format jpeg -s formatOptions 82 "$W/phones.png" --out assets/phone-screens.jpg >/dev/null
sips -g pixelWidth -g pixelHeight assets/phone-screens.jpg | tail -2 | tr '\n' ' '; ls -l assets/phone-screens.jpg | awk '{print $5" bytes"}'
