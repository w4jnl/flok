# Changelog

User-facing changes per release, newest first. `scripts/release.sh` refuses to tag a version
that has no section here and uses the section as the GitHub release notes, so every release
updates this file first. Dates are the tag dates.

## Unreleased

- **Pair the phone by scanning a code.** `flok phone pair` on a Mac that runs flok shows a code
  and its QR in the terminal (in a tmux popup with `--open`, or `p` in the sidebar's servers
  panel); scan it with the Camera app or in the app's new Pair sheet, or type the code there, and
  the relay gives that phone a key of its own. The phone's token leaves the relay's config:
  `device_tokens` is optional now (a static one still works, for curl and scripts), the relay
  keeps its phones in `phones.json`, and `flok phone list` / `flok phone forget <name>` show and
  revoke them: a forgotten phone's key stops working, its pushes stop, and its app asks to pair
  again. The request travels over the instance's relay link, so it works through the encrypted
  tunnel too; `flok-relay phones` lists the pairings at the container. Needs flok-relay updated;
  an older relay says so.

## 0.8.1 (2026-10-16)

- **A new app icon** on the phone: the mark in the brand's cyan on a dark tile, drawn larger.
  The master is `assets/brand/flok-appicon.svg`.
- **The screen holds still while you read.** An agent's screen on the phone went to its bottom
  at every refresh, so scrolling up to read something earlier lost the place a second later. It
  now follows the bottom only while you are there: scroll up and it stays put as the screen
  changes under it, scroll back down (or tap the arrow in the corner) and it follows again.
  On iOS 18 and later; an iOS 17 phone keeps to the bottom as before.
- **Wrapped screens keep their shape.** With long lines wrapped on the phone, prose wraps at
  words as before, but a divider as wide as the Mac's pane is cut to the screen instead of
  folding into three rows, and a table (box drawing, `|` columns) keeps its rows whole in a
  strip that scrolls sideways. The push's card cuts its dividers the same way, and a double tap
  fits the widest line of text, not the widest rule. Wrapping is now the default; a phone whose
  owner switched it off keeps scrolling sideways.
- **The phone sees the rows the terminal shows.** Copilot's transcript on the phone had its
  gutter bars `│` glued to the end of the row before, after a run of spaces, and the frame of
  its input box folded into thick bars. The screen was captured with tmux's wrapped lines
  joined, and a row a TUI repaints in place keeps the wrapped flag it got when it once ran
  past the edge, so the next row came along with it. Screens for the phone are now captured
  row by row, from the sidebar for local and plain-mode panes and from a host's flok for
  full-mode ones (that part needs the host's flok updated, `flok host install <name>`). The
  phone's own classifier also knows the block characters (`▀▀▀`, `▁▁▁`) a box is drawn with,
  and with wrapping on it leaves out the right edge of a box wider than the phone, which
  would otherwise land on a row of its own after each wrapped row, or stand alone on an empty
  one; a gutter at the start of a row and a table's bars stay.
- **A host that refuses your key says so.** Now and then a full-mode host whose ssh refused the
  key showed as `unreachable` with `read |0: file already closed` for a detail instead of
  `needs auth` with its hint: the pipe closed under the reader as the process ended, and the
  read error spoke before the exit did. The exit speaks first now, before the hello and after it.

## 0.8.0 (2026-10-15)

- **Talk to an agent from the phone.** Hold the microphone next to the reply field and speak;
  let go and the words are in the field, or sent, if Settings says so. On iOS 26 the recogniser
  runs on the phone (Apple's `SpeechTranscriber`, nothing leaves it) with your agents' names
  and the identifiers on the agent's screen as context, and Apple Intelligence tidies the text:
  punctuation, filler words out, `relay.go` spelled as code. Before iOS 26 it is Apple's speech
  recognition. **Reply…** on every push opens the Lock Screen's text field, dictation included
  (an Apple Watch dictates it too), and types it into the agent, then Enter.
- **Pictures and long text to an agent.** A paperclip in the agent view attaches photos,
  screenshots or the clipboard's picture, up to six, shrunk on the phone, and *flok* is in the
  share sheet for screenshots, photos, a PDF, text or a link from any app. The instance saves
  the files under its state dir (`attach/`, swept after a day; an agent on a remote host: under
  that host's) and types their paths after your words, which Claude Code and Copilot (`@path`) read. A
  reply may be long and multi-line now, up to 8000 characters, pasted through a tmux buffer as
  one bracketed paste instead of typed as keys. Both need flok updated on the instance (and on
  a full-mode host for its agents); the relay names the missing update instead of failing, and
  the app shows what each instance takes. A reply that starts with `-` no longer fails
  (`send-keys` read it as a flag).
- **The relay link and the phone's stream are compressed.** Snapshots and screens travel
  deflated to the relay and on to the phone, with one window for the whole connection: a
  snapshot that changed by one agent's tool costs a few dozen bytes instead of a few kilobytes,
  and a screen you watch on the phone about a hundred bytes a second instead of ten kilobytes.
  The encrypted link compresses inside its TLS, after the token. It takes flok and the relay
  both updated (the app already asks for it); with either one older the frames go as before.
  `flok link status`, `flok doctor` and the menu bar's "Relay link" row say `compressed` when it
  is, and the relay's log says so per instance.

## 0.7.1 (2026-10-13)

- **flok's popups look like your terminal again.** tmux 3.8 gives popups a grey background of
  its own; the keybinds help (`prefix ?`) and the host install popup now ask for the terminal's
  colours, as before. tmux's own menus keep 3.8's look.

- **The card behind a push opens.** A long press on a flok notification showed iOS's plain view:
  the extension that draws flok's card (the agent's screen, keys to answer) did not link the
  framework iOS loads it through, so it quit at once. It now opens as intended.

- **Switch the relay link off and on with `prefix U`.** `flok link` (on, off, toggle, status) or
  the menu bar's new "Relay link" row does the same. Off, nothing leaves the Mac for the relay:
  the phone shows that flok as *off* rather than *offline*, its waiting agents leave the badge
  and the Lock Screen, answers are refused, and what happens meanwhile is not sent later. It
  stays off across restarts until you switch it on. The state is one quiet cell at the right
  end of the sidebar's `[flok]` line: `⇄` green when connected, blinking while it connects,
  orange when the relay is out of reach, red when it refuses, and `–` when switched off;
  `flok status` and `flok doctor` mention a link that is off.

- **A finished agent is one thing on the Lock Screen, not two.** While flok shows in the Dynamic
  Island and on the Lock Screen, a finished or failed agent lights it up, with flok's done sound,
  instead of arriving as a separate notification beside it; the app badge still counts it.
  Permission prompts and questions stay notifications (Approve / Deny, the card, Focus). Settings
  → Notifications → "Finished agents as notifications too" brings the notifications back.

- **An encrypted link for networks whose proxy reads HTTPS.** An office proxy that inspects TLS
  could read everything a flok sends its relay: agent names, screens, your answers, the
  instance token. Set `[link] pin` to the relay's key (`flok-relay pin` prints it) and the link
  runs TLS 1.3 inside the WebSocket, to that key and no other: the proxy sees an upgrade to
  `/link/tls` and data it cannot read, and a proxy that answers with a key of its own is
  refused, naming the key that answered. The token only travels inside. The relay creates its
  key in its data dir at the first start and logs the pin; `tunnel_only`
  (`FLOK_RELAY_TUNNEL_ONLY=1`) refuses links without one. `flok doctor` and `snapshot.json` say
  whether the link is encrypted.

- **A shorter README.** It is the front page now, with a picture of how the parts connect and
  the phone next to the menu bar; the detail (how it works, remote hosts, the phone, keys, every
  command and configuration key, troubleshooting) moved to [`docs/`](https://github.com/w4jnl/flok/tree/main/docs).

## 0.7.0 (2026-10-13)

- **Notifications on the phone, your way.** A new Settings → Notifications page in the app
  turns each kind (permission prompt, question, finished, failed) on or off, picks its sound
  (flok's, the iOS default, silent) and whether it breaks through Focus, and lists the muted
  agents. Mute an agent from its screen (1 hour, until tomorrow, always) or with **Mute 1 h** on
  the notification; a mute survives the agent's restart. The relay holds each phone's settings
  (`PUT /api/devices/{token}/prefs`) and decides before every push, logging what it held back.
- **Notifications look like flok.** flok's own sounds; the agent as the title, instance / host ·
  session under it, the sidebar's glyph and words (`● needs you: perm:Bash`, `✓ done`); a
  state tile in the brand colours as the thumbnail; and a long press opens a flok-styled card
  with the agent's screen as it is now (fetched from the relay, never sent through Apple) and
  keys (1 2 3 y n ↵ esc) that answer from the notification. Time-sensitive alerts now really
  break through Focus (the app has the entitlement).
- **Working agents spin** in the app's list and on the agent screen, like the sidebar's ◐◓◑◒.
- **flok in the Dynamic Island and on the Lock Screen.** A Live Activity, the phone's menu bar:
  what works (◐), what waits on you (●) and what finished unseen (✓) over every instance, the
  most urgent agents with their running time, in the brand's dress. The relay starts it when
  the first agent starts working or asking, keeps it current while the app sleeps, and ends it
  when all has been quiet for two minutes; a tap opens the agent. Follow one agent on its own
  from its screen (⋯ → Follow in Dynamic Island); it goes away a while after the agent finished.
  Switch the overview off in Settings → Notifications.
- **Seen on the phone is seen.** Opening an agent in the app, or answering it (from its screen,
  the lock screen or the notification card), marks it seen like focusing its pane in tmux. And
  when an agent stops waiting on you anywhere, its notification leaves the Lock Screen and the
  badge follows: the relay sends the phones a silent sync push.
- **The app's reply row no longer overlaps itself.** The quick keys have their own row; the
  reply row holds the fold arrow, the text field, a hide-keyboard button while typing, and send.
- **The relay's log tells a phone's registrations apart.** Every push, failed push and
  registration names the device, the start of its token and its Apple service
  (`iPhone 04344cb7… production`), and the app takes the token it registered before out of the
  relay when it registers a new one, so a TestFlight build that replaces an Xcode build leaves
  no stale entry behind.

## 0.6.1 (2026-10-12)

- **One relay for Xcode and TestFlight builds.** `flok-relay` sends each phone's pushes
  through the Apple service its token belongs to (the app says whether it is a development
  build), so `FLOK_RELAY_APNS_SANDBOX` is gone; a leftover one is ignored with a log line.
- **flok for iPhone** (`app/ios`, SwiftUI, iOS 17+): every instance in one list with its
  agents, states and host tags, a "needs you" filter, the live screen of an agent with a key
  bar and a reply field, Approve / Deny / Mark seen from the lock screen, the badge counting
  what waits. It talks to `flok-relay` only; the API it speaks is written down in `app/API.md`
  for the Android app to come. Built from `project.yml` with XcodeGen (`make ios-project`,
  `ios-test`; `ios-device` installs on a phone on the cable, `ios-testflight` uploads to App
  Store Connect); your team id goes in `Local.xcconfig`, the APNs key into the relay.
- **Replies from the phone are sent.** Text and Enter reached Claude Code in one burst, which it
  takes for a paste: the reply sat in the prompt unsent. The text and the keys now go in two
  writes 150 ms apart. Agents on a remote host need this release there too
  (`flok host install <name>`).
- **A remote host whose flok predates the phone** now says so: the phone shows "flok 0.5.2 on
  beta predates the phone … (flok host install beta)" in place of the screen and as the answer's
  error, instead of waiting for a screen that never comes.
- **Keys that outlive the binary they were bound with.** A sidebar started through a symlink
  (`~/.local/bin/flok`) bound every key to that path; when the link went away, each key
  answered `returned 127` and later sidebars left the bindings alone because they ran flok. The
  sidebar now resolves its own path (a Homebrew install keeps the stable `/opt/homebrew/bin`
  link) and rebinds a flok key that runs another binary than itself; the snippet's bare `flok`
  is still left to PATH.

## 0.6.0 (2026-10-10)

- **flok on your phone, the link and the relay.** `flok-relay` is a small service you run
  (one static binary; a container image `ghcr.io/w4jnl/flok-relay` and a Portainer stack behind
  Traefik in `deploy/relay`). With `[link] url` and `token` the sidebar keeps an outbound
  WebSocket to it (through `HTTPS_PROXY`, reconnecting with backoff) and streams its view,
  the blocked / done / error transitions and the screen of a pane while the phone looks at it;
  back come answers (allowlisted keys and a short line, typed into agent panes only, logged in
  `events.log`), seen marks and subscriptions, for agents on the instance's remote hosts too.
  The relay's API (`/api/instances`, `/api/answer`, `/api/seen`, `/api/devices`, `/api/ws`)
  is what the app will speak; `flok-relay tail` shows the stream from a shell; pushes go
  through APNs with your provider key, with categories for lock-screen actions. `flok doctor`
  reports the link, the footer says when the relay is out of reach, `snapshot.json` has a
  `link` block. New e2e suite m11 runs the real relay.
- **A message on your phone.** `[notify] url` posts to an ntfy topic (or any endpoint taking
  the JSON form, `format = "json"`) whenever an agent turns blocked, done or fails, on this
  machine or on any connected host, titled with the instance and the agent (`home · beta/docs`)
  and saying what happened (`needs you: perm:Bash`, `finished`); nothing for the pane in front
  of you, nothing at start, repeats within two seconds dropped. `[link] name` names the
  instance (default: the hostname). `flok doctor` shows the endpoint. The first step of flok on
  the phone; the relay and the app follow.
- **Paused, not working.** An agent that answered while background shells, subagents or
  workflows still run (Claude Code lists them on its Stop hook) was shown as working, spinner
  and all, until the work woke it. It is now `paused`: a still grey `◍`, the detail `bg shell`
  or `bg 2 agents`, no spinner in the menu bar, the session's dot grey too; sounds and the
  unread badge still wait for the real end of the work, and a tool start or the next Stop moves
  it on as before. `agent_order = "priority"` ranks it between working and idle.
- The idle prompt box is recognised again on Claude Code 2.1.29x, which writes the session title
  into the box's top rule: the rule that clears a stale working or blocked row after a few polls
  (an Esc-interrupted turn, a dismissed dialog) had gone blind there.

## 0.5.3 (2026-10-05)

- **Agents keep their place.** The agents panel lists agents in a stable order: the sessions as
  the panel above orders them, then window and pane. A row no longer moves when an agent
  finishes, asks, or you look at it, and `1`-`9` always mean the same agent; the glyph, colour and
  detail still say who needs you, and `prefix o`, the agents menu and the menu bar still pick by
  priority. `[sidebar] agent_order = "priority"` brings the old attention sort back (the header
  then reads `priority`). The pane you are on is now obvious: its agent row, its session row and
  its server row wear a `▶` and the agent's name is bold, in the sidebar and the rail. A
  session's rollup is the state that needs you most among its agents, as before.
- **Going back, on any server.** `flok last session`, `last agent` and `last server` return to
  the previously focused session, agent pane or server wherever it was: the sidebar keeps one
  focus history across the local server and every host, so the key that left a session on `beta`
  for one here brings you back to `beta` (the host comes to the front by itself). `flok last
  window` and `last pane` go back within the current session and window, as tmux's own do, so
  one key set behaves the same inside a remote session. They have no default key: `[keys.map]` in
  config.toml names yours (`Tab = "last session"`, `l = "last window"`, `";" = "last pane"`,
  `O = "last server"`, `"'" = "last agent"`; any flok key command works there). Mapped keys are
  bound whatever they held, locally and inside every connected host's tmux (a host's flok must be
  this version or newer), put back when flok ends, and listed in their own `flok · going back`
  section of the keybinds help. Without a running sidebar they do what tmux's own `last-*` would.
- `flok host next|prev` read the server in front from `runtime.json`, which the switch writes
  at once, instead of the published snapshot, which follows a moment later: two quick presses
  no longer skip a server. CI no longer runs the suites on tmux 2.7 (Rocky 8); flok still
  starts there.

## 0.5.2 (2026-10-02)

- Three tmux menus over the work pane, one key each: `prefix A` the agents (`1`-`9` open one,
  remote ones tagged with their host), `prefix S` the sessions of the server in front (the
  current one marked), `prefix @` the servers (as in `claude @beta`; H and R are resize and
  reload keys in many configs); each links to the other two (`flok menu
  agents|sessions|servers`; `flok host menu` stays as the servers one). The servers menu moves
  from `S` to `@`, and `prefix A` no longer steps to the previous agent (`a` cycles, `o` jumps to
  the one needing you, the menu picks). `flok goto` takes a session id (`$5`, `beta:$5`) as well.
  A sidebar start updates flok's own bindings from an older snippet (`A` = prev, `S` = the
  servers menu) in the local tmux, and the end unbinds them rather than putting them back; the
  footer and `flok doctor` say so, `flok install --tmux` prints the current snippet (a tmux.conf
  reload brings the old binding back until then).
- **A starter tmux.conf for people who have none.** `flok install` notices when neither
  `~/.config/tmux/tmux.conf` nor `~/.tmux.conf` exists and offers to write one: prefix `C-a`,
  mouse, vi keys, splits that keep the directory, the vim navigator keys, and tpm with sensible,
  pain-control, yank, open, resurrect, continuum and the Dracula status line, which install
  themselves at the first tmux start; the flok bits (exact Claude/Copilot conversations in
  resurrect saves, continuum's autosave under flok) are in place. Every line is annotated. `flok
  install --tmux-conf` writes it without asking, never over an existing file; tmux older than
  3.1 gets `~/.tmux.conf`. `flok doctor` warns when there is no tmux configuration.
- `flok install` no longer prints the tmux snippet: the sidebar binds flok's keys itself, and
  `flok install --tmux` prints the snippet for a tmux.conf that should carry them. The README's
  setup drops the tmux.conf step, and the demo recording and menu bar capture are current (the
  capture's scene has a remote host, so the servers block shows).
- Menu bar: the host rows sit under their own `servers` caption, set off from the agents, with
  local listed first (its agents and who is waiting), so the front can be switched back from
  there too.
- A hook-less agent's row no longer flips to done for a moment when a screen sample scheduled
  before its spinner appeared comes back late: the sample is judged against the title the
  sidebar knows now (seen as an m1 failure on a slow CI runner).

## 0.5.1 (2026-09-30)

- **Install flok on a host from here.** `flok host install <name>` (or `I` on the host's row in
  the servers panel, which shows the progress in a tmux popup, or `flok host add … --install`)
  puts flok on a host that has tmux but no flok, or upgrades an old one, over the same ssh:
  a host with your OS and CPU gets this very binary, another gets the release tarball from
  GitHub checked against its `sha256sums.txt` (`--version` picks one, `--from <file>` pushes
  your own build). It lands in `~/.local/bin/flok` there, which flok already looks at, so
  nothing on the host is configured, no root, no curl. The new flok must answer `flok
  version` on the host before it replaces the old one. `--hooks` then wires the Claude Code /
  Copilot hooks there for the agent folders it finds. A host without flok now says so in the
  sidebar footer, with the key; the `no flok` / `old flok` hints, `flok host status` and
  `flok doctor` name the command. `flok host reconnect <name>` asks the running sidebar to
  redial a host (what `r` does). Releases now ship macOS tarballs next to the Linux ones.
- An agent row keeps the name of the directory its session started in: Claude Code's Bash tool
  keeps a `cd` between calls and reported the sub-directory on every later hook, so a row could
  read `engine` for a while instead of its project. `n` on an agent row names it yourself
  (Enter saves, an empty name restores the directory name); the name sticks to that pane and
  is kept in `names.json` in the state dir.
- Sidebar footer: a warning or error wraps over up to three lines above the key hints, with a
  blank line between the two, instead of replacing the hints cut off ("+N" counts further
  warnings); key hints that do not fit the width wrap onto a second line instead of overrunning.
  The keys help (`?` in the sidebar, `prefix ?`) lists flok's bindings, then the sidebar's own
  keys (and the servers panel's), then the tmux tables; the sidebar sections used to sit at the
  very end. The old-flok warning reads `<host>: upgrade flok there, no key relay (<version>)`.
- Remote hosts: changing a host's mode, target or flok path, `r`, and `flok down` no longer wait
  for the old connection to tear down (a dead link could hold the sidebar for the ssh timeout);
  the replacement connects in the background and a replaced connection's last messages can no
  longer overwrite the new one's row. A hook-less pane whose screen shows the model picker
  keeps its `prompt` reason while the picker is open, instead of turning into `input`.

## 0.5.0 (2026-09-28)

flok goes multi-host: one local flok shows and drives tmux servers on other machines over
ssh. Without a host registered nothing changes.

- **Remote hosts.** `flok host add [<name>] <user@host|ssh alias> [--mode full|plain]`
  registers a host in `hosts.json` (`set` changes it in place, `remove`, `connect`,
  `disconnect`, `list`, `status`); `[hosts]` in config.toml holds the ssh defaults. The target
  goes to ssh as typed; the name defaults to the alias or the first DNS label, keeps its case
  (`dockerAMS`) and matches in any case. Mode `full` runs `flok serve --stdio` on the host (its
  hooks, Claude registry and screen rules, streamed back; hooks there stay silent while served
  and the sounds play here). Mode `plain` drives the host's tmux over ssh with titles and screen
  rules only, for hosts where flok cannot be installed.
- **Servers panel.** Above the sessions, local first: `Tab` cycles the three panels, `Enter`
  brings a host's work pane next to the sidebar, `Space` does the same with the keyboard
  staying in the sidebar, `c`/`d` connect or disconnect, `r` reconnects now, `m` flips the mode,
  `x` removes after a `y/n`, `i` shows the host's details. The sessions panel shows the front
  host, the agents panel every host, attention-sorted, remote rows tagged with their host; with
  the keyboard in the sidebar the selected row is a full-width bar with a `›` in front. Each
  host keeps a persistent `ssh -t … tmux attach` pane parked in the outer, so switching is
  instant and survives `flok hide`.
- **Connection states.** Hosts reconnect with backoff. `needs auth`, `no flok`, `old flok` (a
  flok without `serve` there, named with its version), `no tmux server` (the host answers but
  tmux is not running for that user or socket; the work pane then starts one with the session
  from `[hosts] session`, default `main`, or the host's `--session`), `busy` and `incompatible`
  say what to fix, and the sidebar footer names the reason behind a failing host.
  `[hosts] remote_path` (Homebrew, /usr/local and ~/.local/bin by default) is appended to the
  PATH of every command run on a host, since a non-interactive ssh shell sees only the system
  PATH.
- **Navigation across hosts.** `flok goto beta:%12` and `flok jump` reach agents on any host,
  bringing that host to the front first; `flok next`/`prev` walk the front host;
  `flok host front <name>|local` switches hosts from a key binding or script. Servers rotate
  from the keyboard: `prefix N`/`P` next and previous server, `prefix O` the previous one again,
  `prefix S` a tmux menu of them, `prefix F1`…`F9` a server by position (`flok host
  next|prev|last|menu`, `flok host front <N>`), inside remote sessions as well; the server that
  comes to the front flashes `flok: now on <server>` on its status line.
- **flok's keys inside remote sessions.** While a host is connected flok binds `prefix b B g o
  a A u` and the server keys in its running tmux and restores the host's own bindings on
  disconnect (`[hosts] keys`), relaying the key to the local sidebar through `flok relay` on
  full-mode hosts or a tmux user option on plain ones. The host also takes your local prefix
  while connected (`[hosts] prefix`, on by default): its own moves to `prefix2`, so the chords
  you type at home work there and its native ones still do; everything goes back on disconnect.
  A host whose flok predates the key relay is called out by `flok doctor`, `flok host status`,
  the host info screen and the sidebar footer instead of failing silently.
- **Local keys without the snippet.** The sidebar binds flok's keys in the local server at
  start (`[keys] bind`: `missing` leaves tmux's own `o` and `?` alone, `all` takes them over,
  `off` never); `flok down` removes them again, and `flok doctor` reports per key what is
  bound.
- **Menu bar.** Remote agents are tagged `@host`; a row per host shows its agents and waiting
  count, or why it is down, and clicking it brings its pane to the front.
- `prefix u` in the tmux snippet toggles keep-awake; `flok keep-awake --notify` shows the new
  state on tmux's status line instead of printing it, and the keybinds popup labels the key
  "keep the Mac awake (toggle)". Existing installs add the line by hand (`flok install --tmux`
  prints it).
- `flok doctor` probes every registered host over ssh: reachability, tmux and flok versions,
  protocol, Claude Code hooks and terminfo there.
- Dependencies: charmbracelet/x/ansi 0.11.8 with the matching cellbuf, colorprofile and x/term.

Upgrading: a full-mode host needs flok 0.5.0 too (`brew upgrade flok` there); the local
tmux snippet gains `prefix u`, `N`, `P`, `O`, `S` and `F1`…`F9` (`flok install --tmux` prints
it), or `[keys] bind` supplies whatever the snippet lacks.

## 0.4.6 (2026-09-25)

- `[keep_awake] presence = true`: while keep-awake is on, Teams, Slack and other apps that
  watch for input keep showing you as active. After 60 s without input flok posts an empty
  modifier-key event (no key press, no cursor movement), which resets the idle clock those apps
  read; the power assertions alone keep the display on but not your presence. It needs
  Accessibility permission for your terminal app; `flok doctor`, `flok keep-awake status`,
  `flok status` and the menu bar tooltip say when macOS blocks the events.

## 0.4.5 (2026-09-25)

- `flok keep-awake` (macOS): keeps the Mac awake with the display on while flok runs, for agents
  working unattended. Off by default; `flok keep-awake` toggles it, `on`/`off`/`status` also work,
  and the menu bar gets a "Keep awake" checkbox and shows ⚡ next to the state glyph while it is
  on. flok holds the power assertions itself (no `caffeinate`), inside the sidebar, so they end
  with the flok session and a new session starts with keep-awake off. `flok status` shows a
  `keep-awake: on` line while it is on, and `flok doctor` reports it. Closing the lid on battery
  still sleeps the Mac.
- `[sounds] when_focused = true` now works: an agent pane you are looking at sounds when it
  finishes or waits for you, like one in the background. The option was documented but ignored.
- README: a new top section (pitch, recording, menu bar, five-step setup, why flok) ahead
  of the architecture; the old Install section is folded into it. The banner is re-rendered
  from the brand lockup (`assets/brand/render-hero.sh`) with the plain tagline. The GIF comes from
  `assets/demo/record.sh`, a vhs tape played against the e2e fake agent on isolated tmux
  servers, so it can be re-recorded; `assets/demo/menubar.sh` captures the menu bar item and
  its open menu from the same scene (`FLOK_BAR_SHOT` makes the bar open its menu and report the
  frames).
- `SECURITY.md` (private vulnerability reporting), `CONTRIBUTING.md`, a bug report form and a
  Dependabot configuration for Go modules and GitHub Actions.

## 0.4.4 (2026-09-22)

- Menu bar: the version row opens `CHANGELOG.md` on GitHub (all releases newest first, so it
  also serves HEAD builds) instead of the running version's release page, and a new
  "GitHub repository" row below it opens the source. Both rows carry an icon, the flok mark
  and GitHub's, drawn into the row title because macOS 27 does not show menu item images.

## 0.4.3 (2026-09-18)

- A working Claude no longer drops to idle for a moment in the middle of a turn. Claude Code's
  screen spinner also draws `✳`, a frame the bundled rules did not know, so three unlucky
  captures in a row read as a bare prompt box and ended the state until the next hook event.
  The bundled Claude manifest is herdr's 2026.09.11.1, which knows the glyph, and a screen-idle
  override now needs twice the samples while Claude's own registry still reports the session
  busy.

## 0.4.2 (2026-09-17)

- `flok doctor`'s double-configuration warning now checks that `~/.tmux.conf` really sources
  `~/.config/tmux/tmux.conf` unconditionally; a shim guarded by `if-shell` or `%if` (the README
  shows one) no longer trips it. tmux's `config_files` lists both files either way.

## 0.4.1 (2026-09-17)

- `flok up` no longer pre-empts a tmux-continuum restore. It used to start the inner server
  with a session named `main`; when the save file also had a `main` session, tmux-resurrect
  found its first pane already taken and restored neither that pane's directory nor its agent.
  The bootstrap session is now called `~flok` (tmux resolves an unknown session target by
  prefix, so the name must not start like any saved session) and is dropped once the restore
  is over, or renamed to `[inner] session` (default `main`) when nothing was restored.
- `flok resurrect save` takes Claude Code session IDs from `claude agents --json` first and from
  hook records second, so a pane whose hooks never fired (installed after the session started,
  or calling a binary that no longer exists) is still restored exactly.
- `flok doctor` fails when the installed Claude Code or Copilot hooks call a flok binary that
  does not exist; such hooks fail silently on every event. It also warns when tmux loaded both
  `~/.tmux.conf` and `~/.config/tmux/tmux.conf`: plugins initialise twice, and tmux-continuum
  then runs two tmux-resurrect restores that type every restored command twice.
- Menu bar: the working spinner turns at the sidebar's pace (`[sidebar] spinner_ms`, 4 fps)
  unless `[bar] animate_ms` is set; it used to be fixed at 2 fps.

## 0.4.0 (2026-09-17)

- Optional tmux-resurrect integration restores hook-backed Claude Code and Copilot CLI panes to
  their exact conversations (`flok install --tmux-resurrect`); panes without an exact session ID
  restore as shells rather than guessing.
- Copilot CLI sounds now play only when a permission prompt is actually shown, not for
  auto-approved tool calls.
- Stale hook records are removed when an agent pane is reused, preventing duplicate sidebar rows
  and navigation to an unrelated or empty pane.

## 0.3.6 (2026-09-15)

- Light terminals get a light palette (Dracula's Alucard): `flok up` asks the terminal for its
  background and the sidebar, keybinds popup and pane border follow (`[theme] mode = auto | dark
  | light`, colours under `[theme.light]`). `flok theme` shows what was detected;
  `flok theme light|dark` switches a running sidebar at once.
- Sidebar: the `[flok]` wordmark on top of the wide layout, and `⌈⩓⌉` over a separator on the
  rail (`[sidebar] brand`, colour `[theme] brand`); a short pane drops it before losing a row.

## 0.3.5 (2026-09-14)

- Menu bar: a "flok <version>" row at the bottom of the menu opens these release notes on
  GitHub.
- Release notes live in CHANGELOG.md and are published as the GitHub release notes; all earlier
  releases were given theirs.

## 0.3.4 (2026-09-14)

- Menu bar icon is tinted by state: teal while an agent works, orange while one waits on you,
  the plain menu bar colour at rest, with a darker teal on a light menu bar.
- The rotating glyph stays in the menu bar colour; colour is reserved for the icon and the badge.

## 0.3.3 (2026-09-14)

- Waiting icon is a solid inversion of the mark (filled tile, chevron and cursor knocked out)
  and blinks about once a second while something waits and the terminal is unfocused
  (`[bar] blink`).
- Outline icon widened to the tile's footprint; the cursor reads as a bar in both states.

## 0.3.2 (2026-09-14)

- Menu bar: coloured spinner and badge (`[bar] color`); the icon is drawn at 18 pt
  (`[bar] icon_size`).
- Fixed: `flok up` could not restart a menu bar that had died while a session was attached.

## 0.3.1 (2026-09-14)

- New brand: the S3 mark (a terminal bracket holding a chevron and a cursor) as the menu bar
  icon, brand assets under `assets/brand`, README hero.
- `flok doctor` and `flok version` on a terminal print the ASCII lockup.

## 0.3.0 (2026-09-14)

- Linux is a first-class platform for the terminal sidebar: tmux 3.4's escaped list output is
  parsed, sound players are detected (mpv, ffplay, pw-play, paplay, play) or set with
  `[sounds] command`.
- tmux 2.7 (RHEL 8) and 3.2a (RHEL 9) are supported: the outer config is rendered per version,
  the keybinds help falls back to a bare popup or a window, `flok doctor` lists what a version
  lacks. CI runs the end-to-end suites on macOS, Ubuntu, Rocky 8 and Rocky 9.
- Terminal bell: `[sounds] bell = auto | always | never`; with no sound player the bell rings
  the local terminal, also over ssh.
- Bundled sounds are WAV (PulseAudio players cannot decode mp3 on RHEL 9).
- Prebuilt Linux tarballs (amd64, arm64, with checksums) are attached to every release; README
  gained a Linux install recipe and a tmux version table.
- The sidebar re-pins its width itself on tmux versions without the `window-resized` hook.

## 0.2.4 (2026-09-12)

- The spinner keeps running when the terminal window loses focus; idle mode applies only while
  the sidebar is hidden.
- A turn that ends without a Stop hook (Esc, usage limit, error) is cleared by three idle
  screen samples as well as by Claude's registry, so the sidebar catches up in seconds rather
  than tens of seconds.

## 0.2.3 (2026-09-12)

- Sidebar: only the 1 s tick spawns tmux; screen results, registry samples and hook changes
  re-merge cached data. Idle mode (`[sidebar] idle_poll_ms`) slows polling while hidden.

## 0.2.2 (2026-09-12)

- CPU: the menu bar redraws only on change and animates at 2 fps (`[bar] animate_ms`); the
  sidebar renders at 15 fps (`[sidebar] fps`), batches screen captures, takes focus from tmux
  focus events and polls Claude's registry only while it can use the answer.
- A Stop with background subagents still running shows as working ("waiting"), not done.

## 0.2.1 (2026-09-12)

- Menu bar: "Edit config…" and "Reload sidebar" items; `flok edit-config`.

## 0.2.0 (2026-09-12)

- flok-bar, the macOS menu bar companion (`[bar] enabled`): animated title, attention badge,
  agent dropdown, click to return to an agent (AeroSpace or AppleScript focus).
- Sidebar width is pinned across window resizes; a default config is written on install;
  fallback corrections are persisted, so states no longer flap; `claude` is found without PATH.
- CI for flok and the tap; README rewritten for a public audience.

## 0.1.2 (2026-09-12)

- Prefix chords typed while the sidebar has focus are replayed into the work pane; `prefix g`
  toggles focus; the rail shows a focus cue.
- `flok up` exits 0 after a deliberate detach or `flok down`.
- A pane title never clears a hook-reported working state.

## 0.1.1 (2026-09-11)

- The rail shows the keyboard cursor; hotkeys park it.

## 0.1.0 (2026-09-11)

- First release: a herdr-style sidebar for tmux with sessions on top and agents below, driven
  by Claude Code and Copilot CLI hooks with title, registry and screen-rule fallbacks; sounds
  when an agent needs input or finishes unseen; compact rail, hide, `prefix ?` keybinds help;
  Homebrew tap `w4jnl/tap`.
