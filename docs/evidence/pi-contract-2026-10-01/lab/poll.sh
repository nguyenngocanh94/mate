#!/bin/bash
# poll.sh <agent> <seconds> <outfile>: record herdr agent get every 0.25s, logging only changes.
ROOT=/Volumes/Work/Workspace/mate/.claude/worktrees/wf_81f3a9a6-12a-5/.scratch
LAB=$(cat "$ROOT/tmp/labname")
end=$(( $(date +%s) + $2 ))
last=""
while [ "$(date +%s)" -lt "$end" ]; do
  out=$(herdr --session "$LAB" agent get "$1" 2>&1 | python3 -c '
import json,sys
raw=sys.stdin.read()
try:
  d=json.loads(raw)["result"]["agent"]
  print(json.dumps({k:d.get(k) for k in ("agent","agent_status","agent_session","launch_pending","state_change_seq","terminal_title")}, sort_keys=True))
except Exception:
  print(raw.strip()[:300])
')
  if [ "$out" != "$last" ]; then
    echo "$(python3 -c 'import datetime;print(datetime.datetime.now().isoformat(timespec="milliseconds"))') $out" >> "$3"
    last=$out
  fi
  perl -e 'select(undef,undef,undef,0.25)'
done
