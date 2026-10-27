# The relay API

What a phone app speaks to `flok-relay`. Platform-neutral: the iOS app in `app/ios` uses it,
an Android app would use the same calls. JSON everywhere, snake_case keys, RFC 3339 times.

## Authentication

Every call carries `Authorization: Bearer <device token>`: the key this phone got from pairing
(`POST /api/pair`, below), or one of the relay's optional static `device_tokens`. (Instances use
their own key from a join, or `instance_tokens` / `admin_tokens`, on `/link`; an app never needs
those.) `GET /healthz`, `POST /api/pair` and the joining flok's `/api/enroll` calls are the
calls without a token.

A **tenant** is the set of floks and phones that see each other. A phone belongs to the tenant
of the flok that showed it the pairing code, and everything it reads or hears (`/api/instances`,
the stream, pushes, the badge, Live Activities) is that tenant's alone; devices registered under
another tenant's key are not visible to it (404). Static `device_tokens` sign in to the relay's
own tenant, `default`. The pairing answer names the tenant.

Pairing: `flok phone pair` on an instance asks the relay, over its link, for a code of eight
letters and digits (`AB7Q-K2MN`, alphabet `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`, five minutes, one
use, one live code per instance) and shows it with a QR holding
`flok://pair?code=AB7Q-K2MN&relay=https%3A%2F%2Fflok.example.net`. The app posts the code
(any case, dash optional) with the phone's name and gets a 48-hex key of its own, which it keeps
and uses as its device token from then on. A wrong, used or expired code is one 400; an address
that keeps guessing gets 429 with `Retry-After`. The relay ties the phone's push registrations
to that key: `flok phone forget` on an instance revokes it, removes its devices and closes its
`/api/ws` streams with code 1008 and the reason `forgotten: pair this phone again`, on which the
app asks to pair again instead of retrying; a 401 on a paired key means the same. A push token
registered under a new pairing moves to it and the old pairing is dropped when no device is
left to it (the same phone paired again).

The phone's own security is the device's: its key sits in the keychain, the Lock Screen's
answer actions need the device unlocked, and the iOS app adds an optional app lock (Face ID with
a grace period) in front of its own screens. None of that changes what the relay sees: a locked
phone's key is as valid as an unlocked one's, and revoking stays `flok phone forget`.

## Endpoints

| call | body | answer |
|---|---|---|
| `GET /healthz` | | `{"ok":true,"service":"flok-relay","version","instances","online","push","uptime_s"}` |
| `POST /api/pair` | `{"code","name"?}` | 201 `{"token","phone":{"id","name"},"tenant":{"id","name"},"relay":{"version"}}`; 400 `{"error"}` for a code unknown, used or expired; 429 + `Retry-After` after too many wrong codes from one address |
| `GET /api/enroll/code/{code}` | | `{"pending":{"name","hostname"?,"version"?,"from"?,"started","expires_at"},"tenant":{"id","name"}}`: the flok waiting with that code (`flok link join` on a Mac); 404 `{"error"}` for none, and after a few wrong codes from this key |
| `POST /api/enroll/approve` | `{"code"}` | the same object: that flok may join this phone's tenant (the Mac confirms and takes its key by itself); 409 `{"error"}` for a code nobody waits with, one approved already, or a flok of that name online in the tenant |
| `POST /api/enroll` | `{"name","hostname"?,"version"?}` | the joining flok's own calls, no token: 201 `{"id","secret","code","expires_at","poll_s"}`; then `GET /api/enroll/{id}` (bearer: the secret; held up to 20 s) → `{"status":"pending"\|"approved","tenant"?,"approved_by"?}`, 410 declined or expired; `POST /api/enroll/{id}/accept` → 201 `{"token","pin","tenant","instance":{"id","name"},"approved_by"}`; `DELETE /api/enroll/{id}` withdraws |
| `GET /api/instances` | | an `instances` frame (below) |
| `POST /api/answer` | `{"instance","pane","text"?,"keys"?,"files"?}` | `{"ok":true}`, or `{"error"}` with 400 (refused by the relay) / 502 (refused or unreachable instance) |
| `POST /api/seen` | `{"instance","pane"}` | `{"ok":true}` or `{"error"}` |
| `GET /api/devices` | | `{"devices":[{"token":"short…","name","sandbox","added","prefs"?,"phone"?}],"push":bool}`; `phone` is the id of the pairing that registered it |
| `POST /api/devices` | `{"token","name"?,"sandbox"?,"prefs"?}` | `{"ok":true,"push":bool}`; `token` is the platform push token (APNs hex, later an FCM token); a registration without `prefs` keeps the device's |
| `PUT /api/devices/{token}/prefs` | `Prefs` | `{"ok":true}`; 404 for a device the relay does not know (register again) |
| `PUT /api/devices/{token}/activity` | `{"start_token"?,"activities":[{"id","token","kind":"overview"\|"agent","instance"?,"pane"?}]}` | `{"ok":true}`: the device's Live Activity tokens, sent whole (below) |
| `DELETE /api/devices/{token}` | | `{"ok":true}` |
| `GET /api/screen?instance=…&pane=…` | | `{"pane","text"}`: one look at a pane (the relay subscribes, waits up to 4 s for the screen, lets go); 502 offline, 504 no screen in time |
| `GET /api/ws` | WebSocket | the live stream |

`/api/answer` waits for the instance to confirm (up to 8 s), so a 200 means the keys were
typed. An **answer** is `text` (typed first: printable characters, tabs and newlines, at most
8000; one line of up to 200 characters is typed as keys, anything longer or with a newline is
pasted through a tmux buffer as one bracketed paste, so an agent takes it as a paste and not
as lines sent one by one), `keys` (then, in order, at most 8 of `Enter Escape Tab BTab Up Down
Left Right Space BSpace Home End PageUp PageDown C-c`; aliases `esc return backspace ctrl-c`)
and `files` (a list of at most 6 `{"name","mime"?,"data"}`: `data` base64, at most 4 MB each;
`mime` one of `image/jpeg image/png image/gif image/webp image/heic application/pdf`, or
taken from the name's extension; the name is reduced to letters, digits, `. - _`; `file`, one
of them, is taken as well). The instance saves each file under its state dir as
`attach/<stamp>-<name>` (an agent on a remote host: under that host's, swept after a day) and
types the paths after the text, in order, `@path` for a Copilot agent, which is how Claude
Code and Copilot read images and documents. The body may be 36 MB. An instance whose flok
predates these takes neither a long text nor files: its `features` (below) lack `long-text` /
`files`, and the relay answers 400 with a message naming the update before anything is sent.
The pane must be an agent pane in the instance's view. `pane` is a pane ref: `%12` for the
instance's own tmux, `beta:%12` for a pane on its remote host `beta` (compose it from an
agent's `host` and `pane_id`). An answer on the WebSocket is the same without `files` (its
frames are small; files go over HTTP).

A **form answer** answers the question form an agent shows (an agent with `reason`
`question` and a `form`, below): `{"instance","pane","form":{"answers":[{"selected":[0,2],
"other":"Kiwi"},{"selected":[1]}]}}`, one entry per question in the form's order, `selected`
the indexes into that question's `options` (any number for a `multi` question, one
otherwise), `other` words of the user's own (one line, at most 200 characters; on a
single-select instead of a pick). It carries no `text`, `keys` or `files`. The instance
presses the choices into the agent's own form key by key and reads the pane after each, so
the agent receives what a keyboard would have typed; a 200 means the form was submitted. When
the form on the screen does not follow (it was answered or moved meanwhile), the error says
what did not happen and that the user should finish it on the screen, and the screen shows
the form as far as it got. Every question needs an answer. An instance whose `features` lack
`form` is refused with a message naming the update; such an agent is answered with keys.

## The live stream (`/api/ws`)

Text frames, one JSON object each. The relay agrees to permessage-deflate (RFC 7692) when the
client offers it, with one window per connection and direction; URLSession offers it on its own.
A client that does not offer it gets the frames uncompressed.

Relay → app:

```
{"type":"instances","instances":[Instance…],"tenant_info":{"id","name"}}   on connect: every instance of this phone's tenant, and the tenant (sent again when it is renamed)
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
{"name","online":bool,"off"?:true,"since":time,"version"?,"hostname"?,"features"?:[string],"snap":Snapshot?,"gone"?:true}
```

`gone` (in an `instance` frame) means the flok was forgotten on the relay (`flok link forget`,
or its tenant removed): drop it from the list, its events with it.

`features` is what the instance's link can do: `answer`, `screen`, `long-text` (an answer's
text may be long and multi-line), `files` (an answer may carry files); absent for an older
flok, which takes a one-line text of at most 200 characters and no files.

`off` (with `online` false) means the instance's link was switched off there (`flok link off`),
on purpose: show it as *off*, quietly, not as *offline* (out of reach), and expect it back only
when it is switched on. Its agents are out of the badge and a sync push clears their
notifications; an answer is refused with `<name> is switched off (flok link on there)`.

`Snapshot` is the sidebar's own published view (`snapshot.json`), kept while the instance is
offline:

```
{"updated_at","unseen":int,
 "focus":{"host"?,"session_name"?,"pane_id"?},
 "agents":[{"pane_id","host"?,"session_id","session_name","window_id","name","kind",
            "state":"working|blocked|paused|done|idle|unknown","reason"?,"form"?,"tool"?,"detail"?,
            "unseen":int,"state_since":time,"has_hooks":bool}],
 "sessions":[{"id","host"?,"name","rollup"?,"agents":int}],
 "hosts":[{"name","mode","state","detail"?,"agents","pending","front"}],   absent without remote hosts
 "front_host"?, "keep_awake"?, "link":{"state","detail"?,"since","url"}}
```

`event.kind` is `blocked`, `done` or `error`; `reason` for a block is `permission:<tool>`,
`question`, or another wait; an event for a question carries `question`, the first question in
a line, for a notification's body. Agents are in the sidebar's order (sessions, then window and
pane); sort by `state` priority blocked < done < working < paused < idle for a "needs you" view.

An agent blocked by a question whose form the instance's hook saw (Claude Code's
AskUserQuestion) carries it as `form`: `{"questions":[{"header"?,"text","options":[{"label",
"description"?}],"multi"?:bool}]}`, at most 6 questions of at most 9 options. Render it
natively and answer it with a form answer (above); the agent's screen stays the reference, and
an agent without `form` (an older flok, a question the hooks did not see) is answered with
keys. The form goes with the block: it is absent once the agent moves on.

## Pushes

The relay sends one alert per event to every registered device whose `prefs` want it, worded
like the sidebar: title = the agent, subtitle `<instance>[ / <host>][ · <session>]`, body with
the sidebar's glyph, `● needs you: perm:Bash` / `● needs you: <reason>` / `✓ done` / `✗ failed`;
`thread-id` = instance, `collapse-id` = `<instance>:<pane>`, badge = unseen over all instances
not switched off
(muted or not), `mutable-content` (the iOS service extension adds the state tile), and the
custom keys `instance`, `pane`, `kind`, `reason`, `agent`, `host`, `session`, `push_kind` for
the app to act on. Categories: `FLOK_PERMISSION` (a permission prompt; the iOS app offers
Approve = `Enter`, Deny = `Escape`, Reply… = the system's text field, dictation included, its
text then `Enter`, Mute 1 h), `FLOK_QUESTION` (Reply…, Mute 1 h), `FLOK_DONE` (Reply…, Mark
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
