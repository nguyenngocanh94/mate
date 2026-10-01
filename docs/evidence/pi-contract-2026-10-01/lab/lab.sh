#!/bin/bash
# Lab helper for the pi contract measurement. Usage: lab.sh <cmd> [args]
set -u
ROOT=/Volumes/Work/Workspace/mate/.claude/worktrees/wf_81f3a9a6-12a-5/.scratch
S=$ROOT/tmp
LAB=$(cat "$S/labname" 2>/dev/null || true)
nap() { perl -e "select(undef,undef,undef,$1)"; }
clean_env() {
  env -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID -u CLAUDE_CODE_ENTRYPOINT \
    -u CLAUDE_CODE_CHILD_SESSION -u CLAUDE_CODE_SESSION_ATTENDED -u CLAUDE_PID \
    -u CLAUDE_EFFORT -u CLAUDE_CODE_MESSAGING_SOCKET -u CLAUDE_CODE_MESSAGING_TOKEN \
    -u CLAUDE_CODE_EXECPATH TMPDIR="$S" "$@"
}
cmd=$1; shift
case "$cmd" in
  up)
    LAB=fm-lab-pi-$$
    echo "$LAB" > "$S/labname"
    (clean_env herdr --session "$LAB" server >/dev/null 2>&1 &)
    for i in $(seq 1 20); do herdr --session "$LAB" workspace list >/dev/null 2>&1 && break; nap 0.5; done
    echo "$LAB"
    herdr --session "$LAB" workspace list
    ;;
  down)
    herdr session stop "$LAB"; herdr session delete "$LAB"
    ;;
  h)  # raw herdr against the lab
    herdr --session "$LAB" "$@"
    ;;
  nap) nap "$1" ;;
  reads)  # reads <agent> <pane> <capprefix> : every read source, text and ansi
    for src in visible recent recent-unwrapped; do
      herdr --session "$LAB" agent read "$1" --source "$src" --lines 12 --format text > "$3.$src.txt" 2>&1
      herdr --session "$LAB" agent read "$1" --source "$src" --lines 12 --format ansi > "$3.$src.ansi" 2>&1
      echo "=== $src"; tail -12 "$3.$src.txt" | cut -c1-140
    done
    ;;
  think)  # think <pane> <model> : start once per level, record effective level, exit
    pane=$1; model=$2
    for lvl in off minimal low medium high xhigh max; do
      tag="think-$(echo "$model" | tr '/:' '__')-$lvl"
      herdr --session "$LAB" pane run "$pane" "export PIPROBE_LOG=$S/probe-$tag.jsonl; clear"
      nap 0.5
      herdr --session "$LAB" agent start "t-$lvl" --kind pi --pane "$pane" --timeout 60000 -- \
        --model "$model" --thinking "$lvl" --no-session --no-extensions --no-skills --no-context-files \
        -e "$ROOT/ext/probe.ts" > /dev/null 2>&1
      nap 0.5
      foot=$(herdr --session "$LAB" agent read "t-$lvl" --source recent-unwrapped --lines 3 --format text 2>&1 | tail -1)
      eff=$(grep -o '"thinkingLevel":"[a-z]*"' "$S/probe-$tag.jsonl" | head -1)
      warn=$(herdr --session "$LAB" agent read "t-$lvl" --source recent-unwrapped --lines 40 --format text 2>&1 | grep -i -m1 'warn\|thinking' | head -c 160)
      printf '%s\t%s\t%s\tfooter=[%s]\twarn=[%s]\n' "$model" "$lvl" "$eff" "$(echo "$foot" | sed 's/  */ /g')" "$warn"
      herdr --session "$LAB" pane send-keys "$pane" ctrl+d
      nap 1.2
    done
    ;;
  start)  # start <pane> <agent> <runtag> <pi args...>; probe log tmp/probe-<runtag>.jsonl
    pane=$1; agent=$2; tag=$3; shift 3
    herdr --session "$LAB" pane run "$pane" "export PIPROBE_LOG=$S/probe-$tag.jsonl PIPROBE_MARK=7731; clear"
    nap 0.5
    herdr --session "$LAB" agent start "$agent" --kind pi --pane "$pane" --timeout 60000 -- "$@" > "$ROOT/cap/start-$tag.json" 2>&1
    echo "start exit $?"
    cat "$ROOT/cap/start-$tag.json" | head -c 600; echo
    herdr --session "$LAB" agent read "$agent" --source recent-unwrapped --lines 40 --format text > "$ROOT/cap/$tag-startup.txt" 2>&1
    herdr --session "$LAB" agent read "$agent" --source recent-unwrapped --lines 40 --format ansi > "$ROOT/cap/$tag-startup.ansi" 2>&1
    cat -n "$ROOT/cap/$tag-startup.txt"
    ;;
  rawstart)  # rawstart <pane> <tag> <shell command line> : run pi via pane run, no herdr agent start
    herdr --session "$LAB" pane run "$1" "export PIPROBE_LOG=$S/probe-$2.jsonl PIPROBE_MARK=7731; clear; $3"
    ;;
  pread)  # pread <pane> <file-prefix>
    herdr --session "$LAB" pane read "$1" --source visible --format text > "$2.txt" 2>&1
    herdr --session "$LAB" pane read "$1" --source visible --format ansi > "$2.ansi" 2>&1
    cat -n "$2.txt"
    ;;
  turn)  # turn <pane> <agent> <promptfile> <capprefix> <frames>
    herdr --session "$LAB" pane send-text "$1" "$(cat "$3")"
    herdr --session "$LAB" pane send-keys "$1" enter
    for i in $(seq 1 "$5"); do
      nap 1
      herdr --session "$LAB" agent read "$2" --source recent-unwrapped --lines 16 --format text > "$4-$i.txt" 2>&1
      herdr --session "$LAB" agent read "$2" --source recent-unwrapped --lines 16 --format ansi > "$4-$i.ansi" 2>&1
    done
    ;;
  snap)  # snap <agent> <capfile-prefix> [lines]
    herdr --session "$LAB" agent read "$1" --source recent-unwrapped --lines "${3:-60}" --format text > "$2.txt" 2>&1
    herdr --session "$LAB" agent read "$1" --source recent-unwrapped --lines "${3:-60}" --format ansi > "$2.ansi" 2>&1
    cat -n "$2.txt"
    ;;
  *) echo "unknown $cmd"; exit 2 ;;
esac
