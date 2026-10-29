# Troubleshooting

[‹ README](../README.md)

- `flok doctor` first. It reports the binary path the hooks use, missing hooks, which keys flok
  has in your tmux, the manifests and whether the outer is running.
- The footer says `older tmux snippet: A → prev`: your tmux.conf still carries a flok snippet from
  before the menus. Replace it with the one `flok install --tmux` prints, or drop it and let the
  sidebar bind the keys itself.
- `flok status` shows what the sidebar sees, with the source of each state (`hook`, `registry`,
  `title`, `screen`). `flok explain <pane>` lists the screen rules matching a pane.
- `tail -f ~/.local/state/flok/events.log` while an agent works shows the hook events arriving.
  No events for a pane usually means the agent was started before `flok install`; restart it.
- `FLOK_DEBUG=1 flok up` logs every key the sidebar receives to `sidebar.log` in the state dir.
- If you moved the binary (for example from `make install` to Homebrew), run `flok install` again;
  it rewrites the hook commands to the new absolute path.
- A host reads `needs auth` or `host key`: run `ssh <target>` once by hand (flok uses
  `BatchMode=yes`, so it never types a password or accepts a key); `no flok`: the host has no
  flok on its non-interactive PATH: `flok host install <name>` (or `I` on its row) puts this one
  there, or register an existing one with `--flok /path/to/flok`, or use `--mode plain`;
  `old flok`: the flok there predates `serve` (the row names its version), `flok host install
  <name>` upgrades it; `no
  server`: tmux is there but not running for the user and socket you registered (the footer
  shows the socket path); the work pane starts one as soon as the host is connected in the
  sidebar (`[hosts] session`), so this only lingers when the sessions live under another user
  or socket, then `flok host set <name> --target user@host` or `--socket`; "no tmux
  on the host's PATH": tmux lives somewhere the non-interactive shell does not look, add that
  directory to `[hosts] remote_path` (Homebrew, /usr/local and ~/.local/bin are there by default);
  `incompatible`: the two floks speak different protocols, `flok host install <name>` puts this
  one's version there; `busy`: another flok
  (or a `flok host status` still running) serves that host. `flok doctor` runs the same probe on
  every host and says which of tmux, flok, the hooks or the terminfo entry is missing there.
- Agents on a full-mode host show only title and screen states: `flok install --claude` was not
  run on that host (`flok doctor` reports it). Sounds for remote agents play on the local side;
  `FLOK_DEBUG=1` writes the connection log to `remote.log` in the state dir.
- No session reports in nt: `flok doctor` says whether `[notes]` is on, which nt it found and
  when the last import was (`notes: off` means the key is not set; nt below 0.7.0 has no
  `agent import`). A session outside tmux is never seen by flok. A failed import is one line in
  `hook.log` in the state dir, per session; the next import carries the whole journal again.
  For a host: its row's footer line says when another machine imports it (`flok host notes
  <name> on --take`), `events.log` holds a record nt refused, and `hosts/<name>/notes-cursor`
  moves with every import.
- The phone shows nothing from this flok: `flok link status` says whether the link is on and
  connected (a `–` at the end of the `[flok]` line means switched off: `prefix U`), `flok doctor`
  why it is refused or out of reach. `flok-relay tail -url … -token <phone token>` shows what
  the relay would send the app; `curl https://<relay>/healthz` names the relay's version.
