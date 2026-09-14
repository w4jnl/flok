#!/usr/bin/env bash
# M7: terminal bell — with no sound player a hook-driven transition rings BEL into the outer's
# work pane; the outer server turns that into an alert-bell hook (no client needed), which is
# also how it would reach a real terminal, locally or over ssh. Runs on every tmux version.
source "$(dirname "$0")/lib.sh"
python3 - "$FLOK_CONFIG" <<'PY'
import sys
p=sys.argv[1]; s=open(p).read()
assert '[sounds]\nenabled = false\n' in s
s=s.replace('[sounds]\nenabled = false\n', '[sounds]\nenabled = true\nbell = "always"\ncommand = ":"\n', 1)  # ":" = a silent "player"
open(p,'w').write(s)
PY
OUT set-hook -g alert-bell "run-shell -b 'touch $T/bell'"
rm -f "$T/bell"
expect "runtime.json knows the inner client tty (where the bell goes)" '^/dev/' "$(python3 -c "import json;print(json.load(open('$T/state/runtime.json')).get('inner_client_tty',''))")"
hook claude '{"hook_event_name":"SessionStart","source":"startup","session_id":"b"}'
hook claude '{"hook_event_name":"UserPromptSubmit","session_id":"b"}'
hook claude '{"hook_event_name":"Stop","session_id":"b"}'
for _ in $(seq 1 30); do [ -f "$T/bell" ] && break; sleep 0.1; done
expect "Stop (unfocused agent) rang the terminal bell through the outer server" '^yes$' "$([ -f "$T/bell" ] && echo yes || echo no)"
expect "doctor reports the bell mode" 'bell: always' "$("$BIN" doctor 2>&1 || true)"
finish
