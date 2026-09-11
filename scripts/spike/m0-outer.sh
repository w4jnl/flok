#!/usr/bin/env bash
# flok M0 spike: can a nested outer tmux server host a sidebar pane and pass keys,
# mouse, colours and clipboard through to an inner tmux client without getting in the way?
#
#   m0-outer.sh check   automated checks on ISOLATED servers (never touches your real tmux server)
#   m0-outer.sh up      interactive: outer + your real server (throwaway session), then follow CHECKLIST.md
#   m0-outer.sh down    tear everything down
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
OUTER_SOCK=flok-spike
OUTER_CONF=$HERE/outer.conf
ISO_SOCK=flok-spike-iso          # isolated inner server for `check`
ISO_CONF=$HERE/inner-spike.conf
INNER_SESSION=flok-spike-inner   # throwaway session on the REAL server for `up`
WIDTH=${SPIKE_WIDTH:-28}

outer() { tmux -L "$OUTER_SOCK" "$@"; }
iso()   { tmux -L "$ISO_SOCK" "$@"; }

pass=0; fail=0
ok()   { printf 'PASS  %s\n' "$1"; pass=$((pass+1)); }
bad()  { printf 'FAIL  %s\n' "$1"; fail=$((fail+1)); }
check() { local name=$1; shift; if "$@" >/dev/null 2>&1; then ok "$name"; else bad "$name"; fi; }

right_pane_of_outer() { outer display -p -t "flok:0" '#{pane_id}'; }

start_outer() { # $1 = command for the right pane, $2 = cols, $3 = rows
  outer -f "$OUTER_CONF" new-session -d -s flok -x "$2" -y "$3" "$1"
  RIGHT=$(right_pane_of_outer)
  outer split-window -hb -l "$WIDTH" -t "$RIGHT" "sh -c 'printf \"sidebar placeholder\\n\"; cat'"
  outer select-pane -t "$RIGHT"
}

cmd_check() {
  echo "== automated checks (isolated servers) =="
  iso kill-server 2>/dev/null || true
  outer kill-server 2>/dev/null || true
  iso -f "$ISO_CONF" new-session -d -s t -x 200 -y 50
  start_outer "env -u TMUX -u TMUX_PANE tmux -L $ISO_SOCK attach -t t" 200 50
  sleep 0.7

  check "outer prefix is None"                 test "$(outer show -gv prefix)" = None
  check "outer prefix2 is None"                test "$(outer show -gv prefix2)" = None
  check "outer status is off"                  test "$(outer show -gv status)" = off

  rtty=$(outer display -p -t "$RIGHT" '#{pane_tty}')
  ctty=$(iso list-clients -F '#{client_tty}' | head -1)
  check "inner client tty == outer right pane tty ($rtty)" test -n "$ctty" -a "$ctty" = "$rtty"

  termname=$(iso list-clients -F '#{client_termname}' | head -1)
  check "inner client TERM is tmux-256color ($termname)" test "$termname" = tmux-256color

  feats=$(iso list-clients -F '#{client_termfeatures}' | head -1)
  echo "      inner client termfeatures before fix: ${feats:-<none>}"
  if printf '%s' "$feats" | grep -q RGB; then
    ok "RGB already advertised without the inner terminal-features line"
  else
    echo "      (expected: RGB missing - tmux-256color terminfo has no Tc/RGB here)"
    iso set -as terminal-features ',tmux-256color:RGB:extkeys'
    outer respawn-pane -k -t "$RIGHT" "env -u TMUX -u TMUX_PANE tmux -L $ISO_SOCK attach -t t"
    sleep 0.7
    feats=$(iso list-clients -F '#{client_termfeatures}' | head -1)
    echo "      inner client termfeatures after  fix: ${feats:-<none>}"
    has_feat() { printf '%s' "$feats" | grep -q "$1"; }
    check "RGB advertised after 'terminal-features ,tmux-256color:RGB:extkeys'" has_feat RGB
    check "extkeys advertised after fix" has_feat extkeys
  fi

  # Prefix passthrough: the inner (default prefix C-b) must receive C-b c and open a window.
  before=$(iso list-windows -t t | wc -l | tr -d ' ')
  outer send-keys -t "$RIGHT" C-b c
  sleep 0.5
  after=$(iso list-windows -t t | wc -l | tr -d ' ')
  check "inner prefix C-b c passed through outer (windows $before -> $after)" test "$after" -gt "$before"

  # Plain typing reaches the inner shell.
  outer send-keys -t "$RIGHT" "echo spike-marker-\$((6*7))" Enter
  for _ in $(seq 1 40); do   # a fresh zsh with dotfiles can take a moment to start
    iso capture-pane -p -t t 2>/dev/null | grep -q '^spike-marker-42' && break; sleep 0.25
  done
  marker_seen() { iso capture-pane -p -t t | grep -q '^spike-marker-42'; }
  check "typed text reaches inner shell (echo output seen)" marker_seen

  # Sidebar-style switching: drive the inner client by tty.
  iso new-session -d -s u
  iso switch-client -c "$ctty" -t u
  sess=$(iso list-clients -F '#{client_session}' | head -1)
  check "switch-client -c <tty> -t u moved the inner client (now: $sess)" test "$sess" = u

  # Title marker for AeroSpace.
  title=$(outer display -p -t "$RIGHT" '#{T:set-titles-string}')
  check "outer title starts with TMUX ('$title')" sh -c "printf '%s' '$title' | grep -q '^TMUX'"

  # Idle CPU of both servers.
  sleep 2
  opid=$(outer display -p '#{pid}'); ipid=$(iso display -p '#{pid}')
  ocpu=$(ps -o %cpu= -p "$opid" | tr -d ' '); icpu=$(ps -o %cpu= -p "$ipid" | tr -d ' ')
  check "idle CPU outer=$ocpu% inner=$icpu% (< 5%)" \
    sh -c "awk 'BEGIN{exit !($ocpu < 5 && $icpu < 5)}'"

  # Killing the outer must not harm the inner.
  outer kill-server
  sleep 0.3
  check "inner server survives outer kill-server" iso ls
  check "inner client gone after outer kill"  test "$(iso list-clients | wc -l | tr -d ' ')" = 0
  iso kill-server

  echo "== $pass passed, $fail failed =="
  test "$fail" -eq 0
}

cmd_up() {
  if [ -n "${TMUX:-}" ]; then echo "run this from a plain terminal, not inside tmux" >&2; exit 1; fi
  tmux has-session -t "$INNER_SESSION" 2>/dev/null || tmux new-session -d -s "$INNER_SESSION" -c "$HOME"
  if ! outer has-session -t flok 2>/dev/null; then
    cols=$(tput cols 2>/dev/null || echo 200); rows=$(tput lines 2>/dev/null || echo 50)
    outer -f "$OUTER_CONF" new-session -d -s flok -x "$cols" -y "$rows" \
      "env -u TMUX -u TMUX_PANE tmux attach -t $INNER_SESSION"
    RIGHT=$(right_pane_of_outer)
    outer split-window -hb -l "$WIDTH" -t "$RIGHT" "less -R '$HERE/CHECKLIST.md'"
    outer set -p -t "flok:0.0" remain-on-exit on
    outer select-pane -t "$RIGHT"
  fi
  exec tmux -L "$OUTER_SOCK" attach -t flok
}

cmd_down() {
  outer kill-server 2>/dev/null && echo "outer ($OUTER_SOCK) killed" || echo "outer not running"
  iso kill-server 2>/dev/null || true
  tmux kill-session -t "$INNER_SESSION" 2>/dev/null && echo "session $INNER_SESSION removed" || true
}

case "${1:-}" in
  check) cmd_check ;;
  up)    cmd_up ;;
  down)  cmd_down ;;
  *)     sed -n '2,8p' "$0"; exit 2 ;;
esac
