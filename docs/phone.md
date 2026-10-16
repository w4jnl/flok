# flok on your phone

[‹ README](../README.md)

```
 home  flok ──┐                                   ┌── the app (iOS): every instance, answer, screen
              ├── wss (outbound, via HTTPS_PROXY) ──► flok-relay ◄── HTTPS / wss (device token)
office flok ──┘      [link] url, token            │  behind your reverse proxy
                                                  └── APNs ──► pushes with Approve / Deny
```

`flok-relay` is a small service you run (one static binary, a container behind Traefik, Caddy
or nginx; see [`deploy/relay`](../deploy/relay)). Every flok instance you want on the phone dials
out to it over a WebSocket with `[link] url` and `token`, so a Mac behind the office firewall
or a home NAT needs nothing opened: outbound HTTPS is enough, and `HTTPS_PROXY` is honoured.
The instance streams what the sidebar publishes anyway (the same view the menu bar reads,
remote hosts included), the user-facing transitions, and the screen of a pane while the phone
looks at it. Back come three things, nothing else: an **answer** (text, a few named keys and
perhaps some files for an agent pane: a short line is typed with `send-keys`, a long or
multi-line text pasted through a tmux buffer as one bracketed paste, files saved under the
state dir in `attach/` and typed as their paths, which the agent reads; the keys are an
allowlist, text is at most 8000 printable characters, at most six files of 4 MB, images or
PDFs, only agent panes qualify, every command lands in `events.log` by size and file name,
never the text), a **seen** mark, and a **subscription** to a pane's screen. Agents on the
instance's remote hosts are answered the same way, through the main instance (full mode: the
host's `flok serve` types it, with its own check, and keeps the files in its own state dir;
plain mode: `send-keys` over ssh, the files streamed into `~/.local/state/flok/attach` there).

## Setting it up

1. Run the relay: [`deploy/relay`](../deploy/relay) has the image (`ghcr.io/w4jnl/flok-relay`,
   amd64 and arm64), a compose stack for Portainer with Traefik labels and the example configs.
   `flok-relay token` makes the instance tokens, one per flok; a phone needs none, it pairs
   (step 4). Pushes need an APNs key from your Apple developer account (`FLOK_RELAY_APNS_*`);
   without one the relay runs and the app works, only nothing is pushed.
2. Link every flok you want on the phone:
   ```toml
   [link]
   name = "home"                          # how the phone names this flok
   url = "https://flok.example.net"
   token = "…"                            # one of the relay's instance tokens
   # pin = "sha256:…"                     # behind a proxy that reads HTTPS: flok-relay pin
   ```
   then `flok reload`; `flok doctor` and `flok link status` say whether it connected.
3. The app: build [`app/ios`](../app/ios) with your Apple team (Xcode, `make ios-project`; the
   simulator needs no signing) and install it from Xcode or TestFlight (`make ios-testflight`).
4. Pair the phone: `flok phone pair` on a Mac that runs flok shows a code and its QR (in a tmux
   popup with `--open`, or `p` in the sidebar's servers panel). Scan it with the Camera app,
   which opens flok, or in the app's Pair sheet, or type the code and the relay's address there;
   then *Enable notifications* in the app's Settings. The code lasts five minutes and pairs one
   phone; the relay keeps the pairing (`phones.json` in its data dir) with a key of that phone's
   own, and the request travels over this flok's relay link, so it works through the encrypted
   tunnel too. `flok phone list` shows the phones, `flok phone forget <name>` revokes one: its
   key stops working, its pushes stop, and its app asks to pair again. The same phone paired
   again replaces its old pairing as soon as its push token registers. A static token from the
   relay's optional `device_tokens` still signs a phone in, under *Enter a device token instead*.

## What the phone shows

- **The list**: every instance with its agents in the sidebar's order, remote ones tagged with
  their host, the ones that need you counted; *Needs you* filters the rest away.
- **An agent**: its live screen (the pane's text, refreshed while you look; a pinch zooms it,
  long lines wrap: prose at words, a divider cut to the width rather than folded, a table kept
  whole in a strip that scrolls sideways, the right edge of a box wider than the phone left
  out rather than dropped on a row of its own; the *Aa* menu or Settings switches to scrolling
  sideways instead, and the choice is kept). It follows the bottom while you are there; scroll
  up to read and it holds still as the screen changes under it, until you scroll back down or
  tap the arrow in the corner (iOS 18 and later; before it the screen keeps to the bottom).
  Below it a key bar (`1 2 3 y n ↵ esc ↑ ↓`, Ctrl-C behind a
  confirmation) and a reply field. Next to it a paperclip attaches photos, screenshots or the
  clipboard's picture, up to six (shrunk on the phone; the agent gets them as files and reads
  them) and a
  microphone: **hold it and talk**, let go and the words are in the field (or sent, if you
  say so in Settings). On iOS 26 the recogniser runs on the phone (Apple's
  `SpeechTranscriber`, nothing leaves it), with your agents' names and the identifiers on the
  agent's screen as context, and Apple Intelligence tidies the text: punctuation, filler words
  out, `relay.go` spelled as code. Before iOS 26 it is Apple's speech recognition.
- **The share sheet**: *flok* is among the apps when you share screenshots, photos, a PDF,
  text or a link; pick the agent, add a few words, send.
- **Pushes**, one per agent that needs you, worded like the sidebar: the agent as the title,
  `home · api` under it, `● needs you: perm:Bash`, flok's own sounds and a state tile.
  Permission prompts carry **Approve** (Enter on the highlighted choice) and **Deny** (Escape)
  for the Lock Screen, every push **Reply…** (the system's text field with the keyboard's
  dictation, an Apple Watch dictates it too; typed into the agent, then Enter) and **Mute
  1 h**. A long press opens a card with the agent's
  screen as it is now, fetched from the relay rather than sent through Apple, and keys to answer
  from there. The badge counts what waits over every instance; an agent you answer or look at,
  on the Mac or on the phone, comes off the Lock Screen by itself.
- **A Live Activity** while agents work: the overview in the Dynamic Island and on the Lock
  Screen (how many work, wait and finished unseen, the most urgent first), ended after a quiet
  spell; an agent's screen can follow that one agent there as well. A finished agent lights up
  the overview, with its sound, rather than adding a notification beside it.
- **Settings → Notifications**: per kind (permission, question, finished, failed) whether it
  pushes, its sound and whether it breaks through Focus, and the muted agents (an hour, until
  tomorrow morning, always). The relay holds these settings per phone and decides before every
  push.

## The relay and its API

The relay keeps the last snapshot of every instance for an instant first paint, tells the app
when an instance goes offline (or is switched off), and sends pushes through APNs with the
provider key of your developer account. It never originates a command. Its API (bearer device
token) is what the app speaks and what you can drive from a shell; [`app/API.md`](../app/API.md)
has all of it:

```
GET  /healthz                                       is it up (no token)
POST /api/pair     {"code","name"}                  a phone redeems the code from flok phone pair for its key (no token; once; 5 min)
GET  /api/instances                                 every instance with its last snapshot
POST /api/answer   {"instance","pane","text","keys"}   type into an agent pane; waits for the instance's ack
POST /api/seen     {"instance","pane"}
GET  /api/screen?instance=…&pane=…                  one look at a pane's screen
POST /api/devices  {"token","name","sandbox"}       register a phone for pushes (DELETE /api/devices/<token>)
PUT  /api/devices/<token>/prefs | /activity         its notification settings, its Live Activity tokens
GET  /api/ws                                        the live stream: instances, instance, event, screen; subscribe / answer / seen in
```

`flok-relay tail -url https://flok.example.net -token … [-subscribe home:%12]` prints that
stream as JSON lines, which is the quickest way to see what the phone would. Instance tokens
come from `flok-relay token` and go into `[link] token`; a phone's key comes from pairing
(`flok phone pair`, over the link: the relay mints the code, the phone redeems it at
`/api/pair`), the relay keeps it hashed in `phones.json`, and `flok phone list` / `forget` and
`flok-relay phones` manage the pairings. A static `device_tokens` entry still works for a shell.
`flok doctor` reports the link as the sidebar sees it (or probes the relay's `/healthz` when
the sidebar is not running), the footer says when the relay has been out of reach for half a
minute, and `snapshot.json` carries the link's state.

Both connections are compressed when both ends can: the link offers permessage-deflate and the
relay agrees, one deflate window for the whole connection, so a snapshot that changed by one
agent's tool costs a few dozen bytes instead of a few kilobytes, and a screen you watch on the
phone about a hundred bytes a second instead of ten kilobytes. The encrypted link (below)
compresses inside its TLS, after the token. The app's WebSocket asks for it by itself. An older
flok or relay sends the frames as they are; `flok link status` says `compressed` when it is.

## Switching the link off

`prefix U` (or `flok link`, or the menu bar's "Relay link" row) switches
the link off and on again. Off, nothing leaves the Mac for the relay: the link says goodbye and
closes, the phone shows the instance as *off* (not *offline*, which means out of reach), its
waiting agents leave the badge and the Lock Screen, and an answer from the phone is refused.
What happens meanwhile is not sent later; switched on, the link starts over with the current
view. It stays off across `flok reload` and `flok down`/`up` until you switch it on. The state
is one cell at the right end of the sidebar's `[flok]` line (and of the rail's mark), no word:

```
[flok]                  ⇄      connected (green)
[flok]                  ⇄      connecting (blinks grey and green)
[flok]                  ⇄      out of reach (orange) / refused (red; the footer says why)
[flok]                  –      switched off (grey)
```

`flok link status` says it in words, and `flok status` and `flok doctor` mention a link that is
off.

## Behind a proxy that reads HTTPS

An office proxy that inspects TLS (it signs with the
company's own root certificate) reads the plain link: agent names, screens, your answers and
the instance token. `[link] pin` closes that: the link then runs TLS 1.3 *inside* the
WebSocket, to the relay's own key (`flok-relay pin` prints its pin, `sha256:…`), and flok trusts
that key and nothing else, so a proxy that answers with a key of its own is refused instead of
read. The token travels only inside; the proxy sees an upgrade to `/link/tls` and binary
messages. The relay still reads everything (it is yours), so pushes, the badge and the Live
Activity work as before. `tunnel_only` on the relay refuses links without a pin. The tunnel is
not disguised as anything else: a proxy that blocks or flags data it cannot read will do so,
and asking for the relay's name to be exempt from inspection is then the straight route.

## The app

The app is [`app/ios`](../app/ios): SwiftUI, iOS 17 and later, two notification extensions (the
state tile, the card) and the Live Activity, built from `project.yml` with XcodeGen
(`make ios-project`, `ios-build`, `ios-test`). [`app/API.md`](../app/API.md) is the contract it
speaks, written for an Android app to follow.

## Messages without the app

```
sidebar ──federated view, every merge──► blocked / done / error transitions ──► POST [notify] url
                                                                                 (ntfy headers, or JSON)
```

The simplest form: a message, no app and no relay, and no way to answer.

With `[notify] url` set, the sidebar posts a short message whenever an agent turns blocked,
done or fails, on this machine or on any connected host: the title names the instance and the
agent (`home · api`, `home · beta/docs`), the line says what happened (`needs you: perm:Bash`,
`finished`, `failed`). The default format is [ntfy](https://ntfy.sh)'s (`Title`, `Priority`,
`Tags` headers and a text body), so a topic on ntfy.sh or on your own ntfy server, with the
ntfy app on the phone, is all it takes; `format = "json"` posts `{instance, host, agent, pane,
kind, reason, at}` for anything else. A `token` goes out as a bearer token. The same rules as
for sounds apply: nothing for the pane in front of you (a watched turn ends idle, not done), a
start announces nothing, a repeat within two seconds is dropped. The instance name is
`[link] name`, else the machine's short hostname. The post runs on its own goroutine with a
ten-second timeout and a bounded queue, so a dead endpoint costs nothing but a line in
`sidebar.log` (`FLOK_DEBUG=1`). `flok doctor` shows the endpoint.
