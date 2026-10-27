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

`assets/demo/phone.sh` makes the README's phone screenshots: a local relay with made-up
instances, then the `FlokScreens` UI test (`make ios-screens`, its own scheme, so `ios-test`
never runs it) walks the app on the iPhone 15 Pro simulator, asks the script for a push, and
writes the list, an agent's screen, the push's card and the Lock Screen as PNGs.

The simulator runs the app against a real relay. It pairs like a phone, without a camera: run
`flok phone pair --json` on an instance linked to that relay and open the URL it prints, or type
the code in the app's Pair sheet:

```sh
xcrun simctl openurl booted 'flok://pair?code=AB7Q-K2MN&relay=https%3A%2F%2Fflok.example.net'
```

A Mac joining the relay (`flok link join`) shows a code of the same shape; the app approves it
from Settings › Link a computer, or from its QR: `flok://link?code=…&relay=…` opened the same way.

A static device token (one of the relay's optional `device_tokens`) still works, typed under
*Enter a device token instead* or given at a scripted start:

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
3. Build and run on the device from Xcode (`make ios-device` with the phone or iPad on the
   cable; it takes the first reachable device, `IOS_DEVICE=<udid>` picks one, and a device new to
   your developer account is registered on the way). A
   build installed this way is a development build and registers a sandbox token; a TestFlight
   build (`make ios-testflight`, after creating the app record for `nl.w4j.flok` in App Store
   Connect; or Product → Archive → Distribute) a production one. The relay pushes each phone through
   the service its token belongs to, so both kinds work side by side. Both sign in with Xcode's
   account, whose App Store Connect session expires now and then ("Failed to Use Accounts"); an
   App Store Connect API key (Users and Access → Integrations, role Admin) in `Local.xcconfig`
   (`FLOK_ASC_KEY_ID`, `FLOK_ASC_ISSUER_ID`, the `.p8` in `~/.appstoreconnect/private_keys/`)
   takes its place.
4. Pair the phone: `flok phone pair` on a Mac that runs flok shows a code and its QR; scan it
   with the Camera app (it opens flok) or in the app's Pair sheet, then Settings → Enable
   notifications. The phone gets a key of its own from the relay (`POST /api/pair`), and the
   APNs token is handed to the relay (`/api/devices`) and shows as registered. `flok phone forget`
   on the Mac revokes a phone.

## Notifications

Settings → Notifications decides what this phone gets: each kind on or off, its sound (flok's
two, bundled from `Flok/Sounds`, the iOS default, or silent), whether it breaks through Focus,
and the muted agents. The relay keeps a copy per phone and filters before every push. Two
extensions dress the push: `FlokNotificationService` adds the state tile (`Tiles/`, rendered by
`make icons`), `FlokNotificationContent` is the expanded card (`Shared/NotificationCard.swift`,
JetBrains Mono from `Shared/Fonts`) that fetches the screen from the relay and answers with
keys. It reads the relay's address and the device token from the keychain group the app shares
(`keychain-access-groups` in both entitlements), so it works once the app ran after an update.

## Live Activity

`FlokWidgets` is the Live Activity (Dynamic Island, Lock Screen): `Shared/FlokActivity.swift`
holds the attributes and content the relay pushes, `Shared/LiveViews.swift` the faces.
`Flok/LiveActivities.swift` hands the relay the push-to-start token and each activity's update
token, starts the overview itself when the app is open and agents are busy, and follows one
agent from its screen. While the overview runs, a finished or failed agent comes as the
overview's alert instead of a notification (Settings → Notifications → "Finished agents as
notifications too" turns that off). A simulator build needs signing (`-allowProvisioningUpdates`, not
`CODE_SIGNING_ALLOWED=NO`) for a token-pushed activity: ActivityKit wants the APS environment.

## What the app does, and does not

Answers go through the relay's allowlist: text (a short line typed as keys, a longer or
multi-line one pasted through a tmux buffer, with an instance that takes it), the named keys
the key bar offers (Enter, Esc, arrows, Tab, Backspace; Ctrl-C behind a confirmation) and one
file (an image shrunk here to about 1568 px as JPEG, or a PDF; the instance saves it next to
the agent and types its path after the words, which Claude Code and Copilot read). Approve on
a permission push sends Enter (the highlighted choice), Deny sends Escape, Reply… opens the
system's text field (dictation included, on the Watch too) and types it, then Enter. A
question Claude Code asks with its form comes as the form itself (`Views/QuestionFormView.swift`,
the choices in `FormDraft`): the phone sends the choices, the instance presses them into the
agent's own form key by key and reads the screen after each, so Claude gets what a keyboard
would have typed; the key bar under such a screen offers the form's keys for the rest.
Everything is logged on the instance in `events.log`, by size and file name, never the text.
The app keeps the relay address and the phone's key (the keychain group the extensions read),
what the pairing said (the phone's id and name, the tenant), its notification settings and
mutes, the list's filter, the font size, the voice settings and the lock settings; nothing of
the agents between starts.

Settings › Privacy has an optional app lock (`Model/LockGate.swift`, `LockPolicy.swift`): Face
ID or Touch ID with the passcode as fallback, after the app was away longer than the grace the
user picked (Immediately … 24 hours, or Until tomorrow: once a day; 1 minute by default), on a
cold start when "Lock on launch" is on (off, the last background the app remembered is the
start of the measure); a recent Face ID unlock of the phone itself counts for up to five minutes. The cover is a
second window above the alert level, so sheets hide under it too, and the relay keeps streaming
behind it. "Hide details until unlocked" redacts agent names and screens in the Live Activity
and the notification card while the phone is locked (`.privacySensitive`, a flag in the
keychain group the extensions already read, `Shared/Privacy.swift`). The lock is a UI gate: the phone's key
keeps working, and the notification card's keys and the share sheet stay behind the phone's own
lock, since iOS gives extensions no Face ID. See [docs/phone.md](../../docs/phone.md#locking-the-app).

Voice: hold the microphone next to the reply field and talk. On iOS 26 the recogniser is
`SpeechTranscriber` (on the Neural Engine; nothing leaves the phone; the first use downloads
the language's model), before that `SFSpeechRecognizer`. Both get the names the app holds and
the identifiers on the agent's screen (`Voice/Vocabulary.swift`) as context, so `relay.go`
comes out as written. With Apple Intelligence (iOS 26, iPhone 15 Pro or later) the text is
tidied on the phone (`Voice/Cleanup.swift`: punctuation, filler words out, names spelled as
code; on any doubt the raw text is kept). `SpeechTranscriber` does not run in the Simulator:
the voice path is tried on a phone (`make ios-device`).

## Layout

- `Shared/Wire.swift`: the relay's JSON as Swift types, one decoder for the wire's dates
  (shared with the extensions); `Shared/Attach.swift` the file an answer carries.
- `Model/Relay.swift`: the client: the live WebSocket (reconnect with backoff, subscriptions
  resent), the HTTP calls, the badge.
- `Notifications.swift`: categories and actions, the APNs token to the relay, what a tap does.
- `Model/LockGate.swift`, `Model/LockPolicy.swift`: the app lock (the gate and its cover
  window; the decisions, pure); `Shared/Privacy.swift`: the hide-details flag the Live
  Activity and the card read.
- `Views/`: the instance list, the agent screen with its key bar, the attachment and
  microphone buttons, settings.
- `Voice/`: the recogniser (`Voice.swift`, both engines and the microphone), the vocabulary
  and the clean-up.
- `FlokShare/`: the share extension ("flok" in the share sheet: a screenshot, a photo, a
  PDF, text or a link to an agent, with the relay and token from the shared keychain group).
- `FlokTests/`: decoding, URL normalisation, the action mapping, the image shrink, the
  vocabulary and the clean-up's guard rails.
