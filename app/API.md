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
| `GET /api/devices` | | `{"devices":[{"token":"short…","name","sandbox","added","prefs"?}],"push":bool}` |
| `POST /api/devices` | `{"token","name"?,"sandbox"?,"prefs"?}` | `{"ok":true,"push":bool}`; `token` is the platform push token (APNs hex, later an FCM token); a registration without `prefs` keeps the device's |
| `PUT /api/devices/{token}/prefs` | `Prefs` | `{"ok":true}`; 404 for a device the relay does not know (register again) |
| `PUT /api/devices/{token}/activity` | `{"start_token"?,"activities":[{"id","token","kind":"overview"\|"agent","instance"?,"pane"?}]}` | `{"ok":true}`: the device's Live Activity tokens, sent whole (below) |
| `DELETE /api/devices/{token}` | | `{"ok":true}` |
| `GET /api/screen?instance=…&pane=…` | | `{"pane","text"}`: one look at a pane (the relay subscribes, waits up to 4 s for the screen, lets go); 502 offline, 504 no screen in time |
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

The relay sends one alert per event to every registered device whose `prefs` want it, worded
like the sidebar: title = the agent, subtitle `<instance>[ / <host>][ · <session>]`, body with
the sidebar's glyph, `● needs you: perm:Bash` / `● needs you: <reason>` / `✓ done` / `✗ failed`;
`thread-id` = instance, `collapse-id` = `<instance>:<pane>`, badge = unseen over all instances
(muted or not), `mutable-content` (the iOS service extension adds the state tile), and the
custom keys `instance`, `pane`, `kind`, `reason`, `agent`, `host`, `session`, `push_kind` for
the app to act on. Categories: `FLOK_PERMISSION` (a permission prompt; the iOS app offers
Approve = `Enter`, Deny = `Escape`, Mute 1 h), `FLOK_QUESTION` (Mute 1 h), `FLOK_DONE` (Mark
seen, Mute 1 h). Sounds are the app's bundled `flok-request.caf` (permission, question,
failed) and `flok-done.caf`, `default`, or none, as the device chose. APNs is the one provider
today; an Android build adds FCM or ntfy/UnifiedPush to the relay, with the same payload.

`push_kind` is what a device filters on: `permission` (a block whose reason starts with
`permission`), `question` (any other block), `done`, `error`. `Prefs`, owned by the app and sent
whole:

```
{"kinds":{"permission"|"question"|"done"|"error":
           {"off"?:bool, "sound"?:"flok"|"default"|"none", "time_sensitive"?:bool}},
 "mutes":[{"instance","host"?,"agent","until"?:time}],
 "no_activity"?:bool, "finished_as_notification"?:bool}
```

When an agent stops waiting on the user (it was unseen or blocked in the instance's last
snapshot and is neither now: seen on the Mac or through `/api/seen`, answered), the relay sends
every device one silent push a few seconds later (APNs `background`, `content-available`, a
burst of changes is one push): `{"flok_sync":1,"waiting":["<instance>:<pane>"…],"badge":int}`.
The app removes its delivered notifications for agents not in `waiting` and sets the badge.
Each sync carries the whole state, so a sync the platform delays or merges loses nothing. The
iOS app also marks an agent seen when its screen is opened and after it answers it, as
focusing the pane in tmux does.

A missing kind is on, with flok's sound, time-sensitive for `permission` and `question` only.
`"no_activity": true` in `Prefs` turns the overview Live Activity off for the device.

While a device runs the overview (as it last reported), `done` and `error` do not come as
notifications: the overview's next update carries them as its `alert` (`title` = agent, `body`
= `✓ done · <instance> / <host> · <session>`, the kind's `sound` inside the alert, priority 10),
which lights the activity up on the Lock Screen and expands the Dynamic Island, so a finish is
one thing on the screen, not two. A badge-only push (an `alert` push with `badge` alone, which
shows nothing) keeps the badge. Should that update fail, or the overview be gone by then, the
notification goes after all. `"finished_as_notification": true` keeps them notifications;
`permission` and `question` are always notifications (their actions, Focus).

## Live Activities (iOS: Dynamic Island, Lock Screen)

The app reports its push-to-start token and every running activity with its own update token
(`PUT …/activity`, whole, after each change). The relay pushes ActivityKit payloads (APNs
`liveactivity`, topic `<bundle id>.push-type.liveactivity`) with `content-state`:

```
{"working":int,"waiting":int,"done":int,
 "rows":[{"instance","host","agent","pane","state":"working|blocked|done|idle|gone","label","since":unix}]}
```

and attributes (type `FlokActivityAttributes`) `{"kind":"overview"|"agent","instance","pane","agent"}`;
every field is always present. The **overview** counts every online instance's working,
blocked and finished-unseen agents, the three most urgent as rows (blocked oldest first,
finished newest first, working longest first; a working row has no tool, so a tool change is
not a push). The relay starts it by push-to-start on an idle → busy edge (once until the device
reports it), updates it on every change (priority 10 when `waiting` grows, else 5), and ends it
after two quiet minutes (nothing working, waiting or unseen). An **agent** activity, started by
the app, follows that agent's row and ends when it is gone, or ten minutes after it finished. A
refused activity token is dropped alone.
A mute matches the event's instance, host and agent name (a pane id changes when the agent
restarts, its name does not); without `until` it lasts until removed, expired ones are
dropped. The relay logs a push it did not send and why.
