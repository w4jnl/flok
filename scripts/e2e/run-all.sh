#!/usr/bin/env bash
# Runs the end-to-end suites in order, every one of them even after a failure, each under a
# watchdog, and ends with a table of results (also published to the GitHub Actions step summary
# when there is one). Exit 1 when any suite did not pass. No retries: a flake is a bug.
#
#   scripts/e2e/run-all.sh            all suites, m1 … m10
#   scripts/e2e/run-all.sh m4 m7      a subset
#   E2E_SUITE_TIMEOUT=300             seconds a suite may take before it is killed (TERM, then KILL)
#   E2E_LOG_DIR=/tmp/flok-e2e-logs    where lib.sh keeps a debug run's logs (FLOK_DEBUG=1)
#   E2E_KEEP_LOGS=1                   keep the logs of passed suites too (default: only failed ones)
#
# bash 3.2 (macOS /bin/bash) is enough: no mapfile, no associative arrays, no coreutils timeout.
set -uo pipefail
cd "$(dirname "$0")/../.." || exit 1
SUITES=${*:-m1 m2 m3 m4 m5 m6 m7 m8 m9 m10}
TIMEOUT=${E2E_SUITE_TIMEOUT:-300}
LOGS=${E2E_LOG_DIR:-/tmp/flok-e2e-logs}
mkdir -p "$LOGS"

# sweep_servers kills the isolated servers a killed suite left behind (its cleanup never ran).
sweep_servers() {
  local d s
  for d in "${TMUX_TMPDIR:-/tmp}/tmux-$(id -u)" "/tmp/tmux-$(id -u)"; do
    [ -d "$d" ] || continue
    for s in "$d"/e2e-*; do [ -S "$s" ] && tmux -S "$s" kill-server 2>/dev/null; done
  done
  return 0
}

# run_one <suite>: runs it, tees its output to $LOGS/<suite>.out and sets RES P F SECS NOTE.
run_one() {
  local s=$1 out=$LOGS/$s.out t0 rc pid wd line
  t0=$(date +%s)
  "scripts/e2e/$s.sh" > >(tee "$out") 2>&1 &
  pid=$!
  # TERM reaches lib.sh's trap (exit 124 → its cleanup still runs); KILL is the backstop
  ( sleep "$TIMEOUT"; kill -TERM "$pid" 2>/dev/null; sleep 10; kill -KILL "$pid" 2>/dev/null ) 2>/dev/null &
  wd=$!
  wait "$pid"; rc=$?
  pkill -P "$wd" 2>/dev/null; kill "$wd" 2>/dev/null; wait "$wd" 2>/dev/null   # the sleep too: it holds our stdout (a pipe) open
  sleep 0.3 # tee drains
  sweep_servers
  SECS=$(( $(date +%s) - t0 ))
  line=$(grep -E '^== [0-9]+ passed, [0-9]+ failed' "$out" 2>/dev/null | tail -1)
  P=$(printf '%s' "$line" | sed -nE 's/^== ([0-9]+) passed.*/\1/p')
  F=$(printf '%s' "$line" | sed -nE 's/^== [0-9]+ passed, ([0-9]+) failed.*/\1/p')
  NOTE=""
  if [ "$rc" = 124 ] || [ "$rc" = 143 ] || [ "$rc" = 137 ]; then
    RES=timeout; NOTE="killed after ${TIMEOUT}s"
  elif [ -z "$line" ] || printf '%s' "$line" | grep -q aborted; then
    RES=abort; NOTE=$(grep -m1 '^ABORT ' "$out" 2>/dev/null || echo "no summary line (exit $rc)")
  elif [ "${F:-1}" != 0 ] || [ "$rc" != 0 ]; then
    RES=fail; NOTE="exit $rc"
  else
    RES=pass
  fi
}

results=()
overall=0
for s in $SUITES; do
  echo "== $s"
  run_one "$s"
  results+=("$s|$RES|${P:-}|${F:-}|$SECS|$NOTE")
  if [ "$RES" = pass ]; then
    [ "${E2E_KEEP_LOGS:-0}" = 1 ] || rm -rf "$LOGS/$s" "$LOGS/$s.out"
  else
    overall=1
  fi
done

header="tmux $(tmux -V 2>/dev/null | sed 's/^tmux //'), $(uname -sm), $(bash --version | head -1 | sed -E 's/^GNU bash, version ([^ ]*).*/bash \1/')"
echo
echo "e2e: $header"
printf '%-5s %-8s %6s %6s %5s  %s\n' suite result passed failed secs note
for r in "${results[@]}"; do
  IFS='|' read -r a b c d e f <<<"$r"
  printf '%-5s %-8s %6s %6s %5s  %s\n' "$a" "$b" "$c" "$d" "$e" "$f"
done
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    echo "### e2e: $header"
    echo
    echo "| suite | result | passed | failed | secs | note |"
    echo "|---|---|---|---|---|---|"
    for r in "${results[@]}"; do
      IFS='|' read -r a b c d e f <<<"$r"
      echo "| $a | $b | $c | $d | $e | $f |"
    done
  } >> "$GITHUB_STEP_SUMMARY"
fi
exit $overall
