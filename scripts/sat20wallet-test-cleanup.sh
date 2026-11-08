#!/bin/zsh
set -euo pipefail

suite_dir="/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs/sat20wallet-sdk-test"
supervisor_script="/Users/yingfeng/mcp/local_access/scripts/commands/test-run-supervisor.sh"
expected_workdir="/Users/yingfeng/github/sat20wallet/sdk"

collect_tree() {
  local root="$1"
  local child
  for child in $(/usr/bin/pgrep -P "$root" 2>/dev/null || true); do
    collect_tree "$child"
  done
  print -r -- "$root"
}

stopped=0
skipped=0
for status_file in "$suite_dir"/*.status(N); do
  state="$(/usr/bin/awk -F= '$1=="state"{print substr($0,index($0,"=")+1);exit}' "$status_file")"
  suite="$(/usr/bin/awk -F= '$1=="suite"{print substr($0,index($0,"=")+1);exit}' "$status_file")"
  workdir="$(/usr/bin/awk -F= '$1=="working_directory"{print substr($0,index($0,"=")+1);exit}' "$status_file")"
  [[ "$suite" == "sat20wallet-sdk-test" && "$workdir" == "$expected_workdir" ]] || continue

  log="$(/usr/bin/awk -F= '$1=="log_file"{print substr($0,index($0,"=")+1);exit}' "$status_file")"
  launchd_label="$(/usr/bin/grep -Eo 'launchd_label=[A-Za-z0-9_.-]+' "$log" 2>/dev/null | /usr/bin/tail -n 1 | /usr/bin/cut -d= -f2 || true)"
  if [[ -n "$launchd_label" ]] && /bin/launchctl list "$launchd_label" >/dev/null 2>&1; then
    /bin/launchctl remove "$launchd_label" >/dev/null 2>&1 || true
    stopped=$((stopped + 1))
    print -r -- "removed launchd job $(basename "$status_file") label=$launchd_label"
    if [[ "$state" == "RUNNING" ]]; then
      now_epoch="$(/bin/date '+%s')"
      now_at="$(/bin/date '+%Y-%m-%dT%H:%M:%S%z')"
      tmp="${status_file}.cleanup.$"
      /usr/bin/awk -F= -v at="$now_at" -v epoch="$now_epoch" '
        $1=="state" {print "state=ABORTED"; next}
        $1=="ended_at" {print "ended_at=" at; next}
        $1=="ended_epoch" {print "ended_epoch=" epoch; next}
        $1=="exit_code" {print "exit_code=143"; next}
        {print}
      ' "$status_file" > "$tmp"
      /bin/mv -f "$tmp" "$status_file"
    fi
    continue
  fi

  [[ "$state" == "RUNNING" ]] || continue
  supervisor_pid="$(/usr/bin/grep -Eo 'supervisor_pid=[0-9]+' "$log" 2>/dev/null | /usr/bin/tail -n 1 | /usr/bin/cut -d= -f2 || true)"
  if [[ "$supervisor_pid" != <-> ]]; then
    skipped=$((skipped + 1))
    print -r -- "skip $(basename "$status_file"): no supervisor pid"
    continue
  fi
  command="$(/bin/ps -p "$supervisor_pid" -o command= 2>/dev/null || true)"
  if [[ "$command" != *"$supervisor_script"* || "$command" != *"$status_file"* ]]; then
    skipped=$((skipped + 1))
    print -r -- "skip $(basename "$status_file"): supervisor identity mismatch"
    continue
  fi

  pids=("${(@f)$(collect_tree "$supervisor_pid")}")
  for pid in "${pids[@]}"; do
    [[ "$pid" == <-> ]] || continue
    /bin/kill -TERM "$pid" 2>/dev/null || true
  done
  /bin/sleep 0.5
  for pid in "${pids[@]}"; do
    [[ "$pid" == <-> ]] || continue
    /bin/kill -KILL "$pid" 2>/dev/null || true
  done

  now_epoch="$(/bin/date '+%s')"
  now_at="$(/bin/date '+%Y-%m-%dT%H:%M:%S%z')"
  tmp="${status_file}.cleanup.$"
  /usr/bin/awk -F= -v at="$now_at" -v epoch="$now_epoch" '
    $1=="state" {print "state=ABORTED"; next}
    $1=="ended_at" {print "ended_at=" at; next}
    $1=="ended_epoch" {print "ended_epoch=" epoch; next}
    $1=="exit_code" {print "exit_code=143"; next}
    {print}
  ' "$status_file" > "$tmp"
  /bin/mv -f "$tmp" "$status_file"
  stopped=$((stopped + 1))
  print -r -- "stopped $(basename "$status_file") supervisor=$supervisor_pid"
done

print -r -- "stopped=$stopped skipped=$skipped"
