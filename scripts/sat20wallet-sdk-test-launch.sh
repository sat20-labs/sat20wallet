#!/bin/zsh
set -euo pipefail


variant="${1:-}"
case "$variant" in
  all|e2e) ;;
  *) print -u2 "usage: sat20wallet-sdk-test-launch.sh <all|e2e>"; exit 2 ;;
esac

suite="sat20wallet-sdk-test"
workdir="/Users/yingfeng/github/sat20wallet/sdk"
root="/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs/$suite"
supervisor="/Users/yingfeng/mcp/local_access/scripts/commands/test-run-supervisor.sh"
timeout_ms="6000000"
started_epoch="$(/bin/date '+%s')"
started_at="$(/bin/date '+%Y-%m-%dT%H:%M:%S%z')"
stamp="$(/bin/date '+%Y%m%dT%H%M%S')"
run_id="${stamp}-$$-${variant}"
/bin/mkdir -p "$root"
log_file="$root/$run_id.log"
status_file="$root/$run_id.status"
tail_file="$root/$run_id.tail"
latest_status="$root/latest.status"
latest_tail="$root/latest.tail"
latest_run="$root/latest.run"
latest_lock="$root/.latest.lock"
: > "$log_file"

{
  print -r -- "state=RUNNING"
  print -r -- "suite=$suite"
  print -r -- "variant=$variant"
  print -r -- "run_id=$run_id"
  print -r -- "pid=$$"
  print -r -- "working_directory=$workdir"
  print -r -- "timeout_ms=$timeout_ms"
  print -r -- "started_at=$started_at"
  print -r -- "started_epoch=$started_epoch"
  print -r -- "ended_at="
  print -r -- "ended_epoch="
  print -r -- "duration_seconds="
  print -r -- "exit_code="
  print -r -- "log_file=$log_file"
  print -r -- "tail_file=$tail_file"
  print -r -- "status_file=$status_file"
} > "$status_file"

# Publish this run as latest before starting the supervisor.
print -r -- "$run_id" > "$latest_run"
cp "$status_file" "$latest_status"

if [[ "$variant" == "all" ]]; then
  cmd=(/bin/bash /Users/yingfeng/github/sat20wallet/scripts/sat20wallet-sdk-native.sh)
else
  for dependency in satoshinet indexer rgb11 transcend; do
    [[ -d "/Users/yingfeng/github/$dependency" ]] || { print -u2 "missing dependency: $dependency"; exit 2; }
  done
  cmd=(/usr/bin/env -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS /usr/local/go/bin/go test -p=1 -parallel=1 ./e2e "-timeout=${timeout_ms}ms" -count=1)
fi

print -r -- "+ ${(q-)cmd}" >> "$log_file"
/usr/bin/nohup /bin/zsh "$supervisor" "$status_file" "$tail_file" "$latest_status" "$latest_tail" "$latest_run" "$latest_lock" "$run_id" "$suite" "$variant" "$workdir" "$timeout_ms" "$started_at" "$started_epoch" "$log_file" -- "${cmd[@]}" </dev/null >/dev/null 2>&1 &
supervisor_pid=$!
print -r -- "[test-run] supervisor_pid=$supervisor_pid" >> "$log_file"
print -r -- "run_id=$run_id"
print -r -- "supervisor_pid=$supervisor_pid"
print -r -- "status_file=$status_file"
print -r -- "tail_file=$tail_file"
print -r -- "log_file=$log_file"
