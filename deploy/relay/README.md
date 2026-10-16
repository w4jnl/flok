# flok-relay

The service between your flok instances and the phone. Instances dial out to it (nothing to
open at home or at the office), the app connects to it, pushes go through APNs. One static
binary, no database: the only state is `devices.json` (the phones registered for pushes).

## Run it behind Traefik with Portainer

1. DNS: point `flok.example.net` at the host Traefik runs on.
2. Tokens: `flok-relay token` (or `openssl rand -hex 24`) once per flok instance. Phones need
   none: once an instance is linked, `flok phone pair` there shows a code and its QR, and the
   relay keeps the phones it pairs in `phones.json` on the data volume (`flok-relay phones`
   lists them, `flok phone list | forget` on an instance manages them). A static device token
   is optional, for curl and scripts. Copy `stack.env.example` to `stack.env` and fill it in,
   with `FLOK_HOST` and the names of your Traefik network, HTTPS entrypoint and certificate
   resolver.
3. APNs (optional until the app exists): in the developer account, Keys → a key with the Apple
   Push Notifications service; download the `.p8`, note its key id and your team id. The key
   travels as one base64 line in `FLOK_RELAY_APNS_KEY_B64` (`base64 -i AuthKey_XXXX.p8 | tr -d
   '\n'`; Linux: `base64 -w0 …`), next to the key id, team id and the app's bundle id. Without
   it the relay runs without pushes (the app still works while open).
4. In Portainer: Stacks → Add stack → upload `compose.yml`, paste the variables from `stack.env`,
   deploy. The image is `ghcr.io/w4jnl/flok-relay:latest` (per release tag too, amd64 and
   arm64); `make relay-image` builds one locally when you would rather not pull.
5. Check: `curl https://flok.example.net/healthz` → `{"ok":true,…}`.

Without Portainer, `docker compose --env-file stack.env up -d` does the same; without Docker,
`flok-relay serve -config relay.toml` behind any reverse proxy that passes WebSockets through
(all of them do by default). Keep the relay on plain HTTP inside; TLS belongs to the proxy.

## Connect an instance

In that machine's `~/.config/flok/config.toml`:

```toml
[link]
name = "home"                          # how the phone names this flok (default: the hostname)
url = "https://flok.example.net"       # wss://…/link is implied
token = "<its instance token>"
```

`flok reload` (or the next `flok up`) connects. `flok doctor` says `link: connected to …`; the
footer says when the relay has been out of reach for half a minute. `HTTPS_PROXY` is honoured
for a machine that must go through a proxy.

### Through a proxy that inspects HTTPS

A proxy with its own root certificate (common in offices) reads the plain link, token included.
Pin the relay's key and the link runs TLS inside the WebSocket, to that key only:

```sh
docker logs flok-relay 2>&1 | grep 'link pin'      # logged at every start, or:
docker exec flok-relay /flok-relay pin             # sha256:…
```

```toml
[link]
url = "https://flok.example.net"
token = "<its instance token>"
pin = "sha256:…"                       # the relay's key: the link goes to /link/tls, encrypted
```

The key is created at the first start in the data dir (`link-key.pem`, keep it with the
volume: a new key means a new pin for every instance). `flok doctor` then says `…/link/tls …,
encrypted to the pinned key, compressed`, the relay logs `instance office online (…, encrypted,
compressed)`. A pin
that does not match is refused with the pin that answered: a proxy replacing the key, or a
relay whose key changed. Once every instance has its pin, `tunnel_only = true`
(`FLOK_RELAY_TUNNEL_ONLY=1`) refuses the plain `/link`.

## See what the phone sees

```sh
flok-relay tail -url https://flok.example.net -token <device token>                # the live stream
flok-relay tail -url … -token … -subscribe home:%12                                # plus a pane's screen
flok-relay tail -url … -token … -answer home:%12 -text y -keys Enter               # type an answer, exit
curl -s -H "Authorization: Bearer <device token>" https://flok.example.net/api/instances | jq .
curl -s -H "Authorization: Bearer <device token>" -d '{"instance":"home","pane":"%12","keys":["Escape"]}' https://flok.example.net/api/answer
docker exec flok-relay /flok-relay phones                                          # the phones paired, from phones.json
```

The device token here is a paired phone's key (the app got it from `POST /api/pair`; nothing
prints it again) or one of the optional static `device_tokens`.

## What it accepts, and what it never does

An answer is a line of at most 200 printable characters and up to 8 named keys from `Enter
Escape Tab BTab Up Down Left Right Space BSpace Home End PageUp PageDown C-c`, typed into a
pane that the instance's own view lists as an agent, in one `send-keys`. The relay never
originates a command, never runs anything, and never sees your shell: an instance that is
offline simply reports so. Every answer and seen mark is logged on the instance in
`~/.local/state/flok/events.log` (what and where, never the text).

## Configuration reference

`relay.example.toml` lists the file keys; every one has an environment variable
(`FLOK_RELAY_LISTEN`, `FLOK_RELAY_DATA_DIR`, `FLOK_RELAY_INSTANCE_TOKENS`,
`FLOK_RELAY_DEVICE_TOKENS`, `FLOK_RELAY_APNS_KEY_FILE`, or `FLOK_RELAY_APNS_KEY_B64` with the
`.p8` base64-encoded, or `FLOK_RELAY_APNS_KEY` with the PEM itself, `FLOK_RELAY_APNS_KEY_ID`,
`FLOK_RELAY_APNS_TEAM_ID`, `FLOK_RELAY_APNS_TOPIC`, `FLOK_RELAY_TUNNEL_ONLY`), which wins over
the file. There is no sandbox switch: the app registers its token with `sandbox: true` when it
is a build installed from Xcode, and the relay pushes each phone through the service its token
belongs to, so development and TestFlight builds share one relay (a leftover
`FLOK_RELAY_APNS_SANDBOX` is ignored with a log line). Logs go to stdout: instances coming and
going, every answer, every push.
