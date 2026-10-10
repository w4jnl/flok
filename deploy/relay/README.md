# flok-relay

The service between your flok instances and the phone. Instances dial out to it (nothing to
open at home or at the office), the app connects to it, pushes go through APNs. One static
binary, no database: the only state is `devices.json` (the phones registered for pushes).

## Run it behind Traefik with Portainer

1. DNS: point `flok.example.net` at the host Traefik runs on.
2. Tokens: `flok-relay token` (or `openssl rand -hex 24`) once per flok instance and once for
   the phone. Copy `stack.env.example` to `stack.env` and fill them in, with `FLOK_HOST` and the
   names of your Traefik network, HTTPS entrypoint and certificate resolver.
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

## See what the phone sees

```sh
flok-relay tail -url https://flok.example.net -token <device token>                # the live stream
flok-relay tail -url … -token … -subscribe home:%12                                # plus a pane's screen
flok-relay tail -url … -token … -answer home:%12 -text y -keys Enter               # type an answer, exit
curl -s -H "Authorization: Bearer <device token>" https://flok.example.net/api/instances | jq .
curl -s -H "Authorization: Bearer <device token>" -d '{"instance":"home","pane":"%12","keys":["Escape"]}' https://flok.example.net/api/answer
```

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
`FLOK_RELAY_APNS_TEAM_ID`, `FLOK_RELAY_APNS_TOPIC`, `FLOK_RELAY_APNS_SANDBOX`), which wins over
the file. Logs go to stdout: instances coming and
going, every answer, every push.
