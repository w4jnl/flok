# The relay API

What a phone app speaks to `flok-relay`. Platform-neutral: the iOS app in `app/ios` uses it,
an Android app would use the same calls. JSON everywhere, snake_case keys, RFC 3339 times.

## Authentication

Every call carries `Authorization: Bearer <device token>`, one of the relay's
`device_tokens`. (Instances use `instance_tokens` on `/link`; an app never needs those.)
`GET /healthz` is the only call without a token.

## Endpoints

| call | body | answer |
|---|---|---|
| `GET /healthz` | | `{"ok":true,"service":"flok-relay","version","instances","online","push","uptime_s"}` |
| `GET /api/instances` | | an `instances` frame (below) |
| `POST /api/answer` | `{"instance","pane","text"?,"keys"?}` | `{"ok":true}`, or `{"error"}` with 400 (refused by the relay) / 502 (refused or unreachable instance) |
| `POST /api/seen` | `{"instance","pane"}` | `{"ok":true}` or `{"error"}` |
| `GET /api/devices` | | `{"devices":[{"token":"short…","name","sandbox","added"}],"push":bool}` |
| `POST /api/devices` | `{"token","name"?,"sandbox"?}` | `{"ok":true,"push":bool}`; `token` is the platform push token (APNs hex, later an FCM token) |
| `DELETE /api/devices/{token}` | | `{"ok":true}` |
| `GET /api/ws` | WebSocket | the live stream |

`/api/answer` waits for the instance to confirm (up to 8 s), so a 200 means the keys were
typed. An **answer** is `text` (typed literally first, at most 200 printable characters) and
`keys` (then, in order, at most 8 of `Enter Escape Tab BTab Up Down Left Right Space BSpace
Home End PageUp PageDown C-c`; aliases `esc return backspace ctrl-c`). The pane must be an
agent pane in the instance's view. `pane` is a pane ref: `%12` for the instance's own tmux,
`beta:%12` for a pane on its remote host `beta` (compose it from an agent's `host` and
`pane_id`).

## The live stream (`/api/ws`)

Text frames, one JSON object each.

Relay → app:

```
{"type":"instances","instances":[Instance…]}        on connect: every instance the relay knows
{"type":"instance","instance":"home","inst":Instance} one changed: online/offline, a new snapshot
{"type":"event","instance":"home","event":{"pane","host"?,"agent","kind","reason"?,"at"}}
{"type":"screen","instance":"home","screen":{"pane","text"}}   while subscribed, when it changed
{"type":"ack","id","instance","error"?}             the outcome of an answer / seen sent on the socket
```

App → relay:

```
{"type":"subscribe","instance":"home","pane":"%12","on":true|false}
{"type":"answer","id":"…","instance":"home","answer":{"pane","text"?,"keys"?}}   same as POST /api/answer, acked
{"type":"seen","id":"…","instance":"home","pane":"%12"}
```

Subscriptions end with the socket. Unknown frame types and fields must be ignored on both
sides; additions keep the protocol compatible.

`Instance`:

```
{"name","online":bool,"since":time,"version"?,"hostname"?,"snap":Snapshot?}
```

`Snapshot` is the sidebar's own published view (`snapshot.json`), kept while the instance is
offline:

```
{"updated_at","unseen":int,
 "focus":{"host"?,"session_name"?,"pane_id"?},
 "agents":[{"pane_id","host"?,"session_id","session_name","window_id","name","kind",
            "state":"working|blocked|paused|done|idle|unknown","reason"?,"tool"?,"detail"?,
            "unseen":int,"state_since":time,"has_hooks":bool}],
 "sessions":[{"id","host"?,"name","rollup"?,"agents":int}],
 "hosts":[{"name","mode","state","detail"?,"agents","pending","front"}],   absent without remote hosts
 "front_host"?, "keep_awake"?, "link":{"state","detail"?,"since","url"}}
```

`event.kind` is `blocked`, `done` or `error`; `reason` for a block is `permission:<tool>`,
`question`, or another wait. Agents are in the sidebar's order (sessions, then window and
pane); sort by `state` priority blocked < done < working < paused < idle for a "needs you" view.

## Pushes

The relay sends one alert per event to every registered device: title `<instance> · <agent>`
(`<instance> · <host>/<agent>` on a remote host), body `needs you: perm:Bash` / `needs you:
<reason>` / `finished` / `failed`, `thread-id` = instance, `collapse-id` = `<instance>:<pane>`,
badge = unseen over all instances, and the custom keys `instance`, `pane`, `kind`, `reason`,
`agent`, `host` for the app to act on. Categories: `FLOK_PERMISSION` (a permission prompt;
the iOS app offers Approve = `Enter`, Deny = `Escape`), `FLOK_QUESTION`, `FLOK_DONE`. APNs is
the one provider today; an Android build adds FCM or ntfy/UnifiedPush to the relay, with the
same payload.
