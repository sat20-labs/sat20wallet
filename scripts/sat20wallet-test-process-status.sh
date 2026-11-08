#!/bin/zsh
set -euo pipefail
root="/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs/sat20wallet-sdk-test"
latest_status="$root/latest.status"
[[ -f "$latest_status" ]] || { print "No SDK tests started under the project script-logs directory yet."; exit 0; }
log_file="$(/usr/bin/awk -F= '$1=="log_file"{print substr($0,index($0,"=")+1);exit}' "$latest_status")"
run_id="$(/usr/bin/awk -F= '$1=="run_id"{print substr($0,index($0,"=")+1);exit}' "$latest_status")"
state="$(/usr/bin/awk -F= '$1=="state"{print substr($0,index($0,"=")+1);exit}' "$latest_status")"
supervisor_pid="$(/usr/bin/grep -Eo 'supervisor_pid=[0-9]+' "$log_file" 2>/dev/null | /usr/bin/tail -n 1 | /usr/bin/cut -d= -f2 || true)"
launchd_label="$(/usr/bin/grep -Eo 'launchd_label=[A-Za-z0-9_.-]+' "$log_file" 2>/dev/null | /usr/bin/tail -n 1 | /usr/bin/cut -d= -f2 || true)"
print -r -- "run_id=$run_id state=$state supervisor_pid=$supervisor_pid launchd_label=$launchd_label"
if [[ "$supervisor_pid" == <-> ]]; then
  /bin/ps -p "$supervisor_pid" -o pid=,ppid=,etime=,stat=,command= || true
  children="$(/usr/bin/pgrep -P "$supervisor_pid" 2>/dev/null || true)"
  for child in ${(f)children}; do
    /bin/ps -p "$child" -o pid=,ppid=,etime=,stat=,command= || true
    grandchildren="$(/usr/bin/pgrep -P "$child" 2>/dev/null || true)"
    for grandchild in ${(f)grandchildren}; do
      /bin/ps -p "$grandchild" -o pid=,ppid=,etime=,stat=,command= || true
    done
  done
elif [[ -n "$launchd_label" ]]; then
  /bin/launchctl list "$launchd_label" 2>/dev/null || print -r -- "launchd_job=missing"
fi
