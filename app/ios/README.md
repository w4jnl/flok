# flok for iPhone

Every flok instance in one list, the agents that need you, their live screen, an answer typed
from the phone or straight from the lock screen. SwiftUI, iOS 17 and later, iPhone and iPad.
It talks to `flok-relay` only ([API](../API.md)); nothing here reaches your machines directly.

## Build

```sh
brew install xcodegen
make ios-project          # app/ios/Flok.xcodeproj from project.yml (not committed)
make ios-build            # a Debug build for the iPhone 15 simulator, no signing
make ios-test             # the unit tests on that simulator
open app/ios/Flok.xcodeproj
```

The simulator runs the app against a real relay: Settings → the relay's address and a device
token (`flok-relay token`, one of the relay's `device_tokens`). For a scripted start:

```sh
xcrun simctl launch booted nl.w4j.flok -relayURL https://flok.example.net -deviceToken <token> [-open Home:%12]
```

Pushes do not reach the simulator from APNs, but a payload file can be delivered to it:

```sh
xcrun simctl push booted nl.w4j.flok payload.apns
# payload.apns: {"aps":{"alert":{"title":"Home · api","body":"needs you: perm:Bash"},"category":"FLOK_PERMISSION","sound":"default"},
#                "instance":"Home","pane":"%12","kind":"blocked","reason":"permission:Bash"}
```

## On a phone

A free Apple ID installs the app on your own phone from Xcode (the build expires after a week;
run again); the Apple Developer Program adds pushes and TestFlight. With a free account set
`FLOK_ENTITLEMENTS =` (empty) in `Local.xcconfig`: its personal team cannot sign the push
entitlement, and nothing else changes.

1. Copy `Local.xcconfig.example` to `Local.xcconfig` and put your Apple team id in it (Xcode →
   Settings → Accounts after signing in, or Membership in the developer account).
2. In the developer account: an App ID `nl.w4j.flok` with Push Notifications enabled, and under
   Keys one key for the Apple Push Notifications service. The `.p8`, its key id, the team id and
   the bundle id go into the relay (`deploy/relay`, `FLOK_RELAY_APNS_*`).
3. Build and run on the device from Xcode (`make ios-device` with the phone on the cable). A
   build installed this way is a development build and registers a sandbox token; a TestFlight
   build (`make ios-testflight`, after creating the app record for `nl.w4j.flok` in App Store
   Connect; or Product → Archive → Distribute) a production one. The relay pushes each phone through
   the service its token belongs to, so both kinds work side by side.
4. In the app: Settings → relay and device token → Save and connect → Enable notifications.
   The APNs token is handed to the relay (`/api/devices`) and shows as registered.

## What the app does, and does not

Answers go through the relay's allowlist: a short line of text and the named keys the key bar
offers (Enter, Esc, arrows, Tab, Backspace; Ctrl-C behind a confirmation). Approve on a
permission push sends Enter (the highlighted choice), Deny sends Escape. Everything is logged on
the instance in `events.log`. The app keeps no data beyond the relay address (preferences),
the device token (keychain) and the font size.

## Layout

- `Model/Wire.swift`: the relay's JSON as Swift types, one decoder for the wire's dates.
- `Model/Relay.swift`: the client: the live WebSocket (reconnect with backoff, subscriptions
  resent), the HTTP calls, the badge.
- `Notifications.swift`: categories and actions, the APNs token to the relay, what a tap does.
- `Views/`: the instance list, the agent screen with its key bar, settings.
- `FlokTests/`: decoding, URL normalisation, the action mapping.
