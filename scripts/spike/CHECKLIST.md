flok M0 spike - manual checklist (run in the RIGHT pane unless stated)

You are inside: outer tmux (prefix None) -> right pane = client of your real tmux server,
session "flok-spike-inner". Nothing else on your server is touched.

 1. Truecolor   run:  awk 'BEGIN{for(i=0;i<256;i++){printf "\033[48;2;%d;%d;%dm ",i,255-i,i/2}print "\033[0m"}'
                expect a smooth gradient. If banded, run once on the inner:
                  tmux set -as terminal-features ',tmux-256color:RGB:extkeys'
                then detach/re-attach (C-a d, then run `m0-outer.sh up` again) and re-check.
 2. Mouse       click between inner panes (C-a | to split first), wheel-scroll should enter the
                INNER copy-mode, drag-select text -> `pbpaste` shows it (tmux-yank).
                Click into the LEFT pane: outer focus moves there (border unchanged colour).
 3. OSC 52      printf '\e]52;c;%s\a' "$(printf hi | base64)"; pbpaste   -> hi
                Same from an ssh pane to a Linux box.
 4. Keys        C-a c (new inner window), C-h/j/k/l between inner panes, Option+b / Option+f in zsh,
                Shift+Enter inside `claude`, C-a C-a inside an ssh'd remote tmux (prefix reaches it).
 5. Styles      in an inner pane run
                  printf '\e[3mitalic\e[23m  \e[4:3mundercurl\e[4:0m  \e[58:2::255:80:80m\e[4:3mred curl\e[59m\e[4:0m\n'
                then run the same line in a plain Ghostty tab (no tmux). Same look = pass.
                nvim variant: :set spell, type a mispelled word -> red undercurl; :hi Comment gui=italic.
 6. Focus       nvim :au FocusGained * echo "gained"  then switch AeroSpace workspace and back.
 7. Title       Ghostty tab title starts with TMUX (AeroSpace routes it).
 8. Resize      resize the Ghostty window: inner reflows, no artefacts. `top` shows both tmux
                servers idle (< 1 % CPU).
 9. Detach      C-a d detaches the inner client -> the flok window closes and you are back
                at the shell (with `flok up`); killing the attached session instead
                re-attaches to another one. `m0-outer.sh down` / `flok down` clean up.

Record: PASS/FAIL per item (+ notes). Any FAIL in 1-4 => per-window fallback.
