# Restore agents with tmux-resurrect

[‹ README](../README.md)

tmux-resurrect restores panes and directories by default, but only restarts programs on its
allowlist. Adding `claude` and `copilot` to that list would restart the CLIs without reliably
selecting the same conversation when several sessions share a directory.

Flok can annotate each tmux-resurrect save with the exact session ID of the conversation in
each pane. Print the opt-in snippet and paste it below the tmux-resurrect/tpm configuration:

```sh
flok install --tmux-resurrect
```

The generated configuration is equivalent to:

```tmux
if-shell -F '#{m:*claude*,#{@resurrect-processes}}' '' "set -ag @resurrect-processes ' claude'"
if-shell -F '#{m:*copilot*,#{@resurrect-processes}}' '' "set -ag @resurrect-processes ' copilot'"
set -g @resurrect-hook-post-save-layout "'/path/to/flok' resurrect save"
```

On every save, `flok resurrect save` rewrites only Claude/Copilot process fields in the new
tmux-resurrect state file:

- Claude Code becomes `claude --resume <exact-session-id>`. The ID comes from Claude Code's own
  registry of running sessions (`claude agents --json`, matched to the pane by tty), which
  describes the live process, and otherwise from the hook record, so a pane whose hooks never
  fired is still saved exactly.
- Copilot CLI becomes `copilot --resume=<exact-session-id>`, from its hook record.
- A recognized agent without a usable session ID is deliberately saved with no process, so its
  restored pane remains a shell instead of opening the wrong conversation. `resurrect.log` in
  the state dir says which panes were skipped and why.

tmux-resurrect still owns pane creation, layout, working-directory restoration and process
launch. This means there is no post-restore race and no dependency on pane IDs being reused.
Conversation history resumes, but a tool that was in flight when tmux died is not rerun. With
tmux-continuum, the restored pane set is as current as its latest save (15 minutes by default).
Trigger a manual tmux-resurrect save after enabling the integration if you want an immediate
baseline.

tmux-resurrect supports one `@resurrect-hook-post-save-layout` command. If that option is already
used, chain both commands in the same shell value rather than replacing the existing hook. Run
`flok doctor` to confirm the hook and process allowlist are visible to the inner tmux server.

If a `~/.tmux.conf` from before tmux 3.1 still sources `~/.config/tmux/tmux.conf`, tmux 3.1+
loads that file twice (it reads both paths itself): every plugin initialises twice and
tmux-continuum starts two restores that type each restored command twice. `flok doctor` warns
about it. Keep the shim for older machines but guard it:

```tmux
# tmux < 3.1 does not read ~/.config/tmux/tmux.conf itself; 3.1+ does, and would load it twice
if-shell 'tmux -V | grep -qE "^tmux (1\.|2\.|3\.0)"' 'source-file ~/.config/tmux/tmux.conf'
```

Restart tmux through `flok up`: it starts the inner server with a throwaway `~flok` session
(a name no saved session can be mistaken for), tmux-continuum restores the saved layout in the
background, the client attaches at once (tmux-resurrect relaunches programs through that
client) and the throwaway session is dropped when the restore is over. Kill both servers (`flok
down`, then the inner
`tmux kill-server`) when you want a restore; tmux-continuum skips its automatic restore while
another tmux server, such as flok's outer one, is running.
