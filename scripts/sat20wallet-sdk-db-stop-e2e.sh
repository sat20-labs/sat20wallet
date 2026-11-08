#!/bin/bash
set -uo pipefail
umask 077

# One existing E2E root only. No MCP changes or shared-runtime cleanup.
if [[ "$#" -ne 0 ]]; then
  printf '%s\n' 'This fixed single-case runner accepts no arguments.' >&2
  exit 2
fi
ROOT="/Users/yingfeng/github/sat20wallet"
LOG_ROOT="$ROOT/.cache/script-logs"
RUN_ID="sdk-db-stop-$(/bin/date -u '+%Y%m%dT%H%M%SZ')-$$"
LOG="$LOG_ROOT/$RUN_ID.jsonl"
STATUS="$LOG_ROOT/$RUN_ID.status"
STARTED="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
/bin/mkdir -p "$LOG_ROOT" || exit 125
cd "$ROOT/sdk" || exit 125

write_status() {
  {
    printf 'state=%s\nrun_id=%s\npid=%s\n' "$1" "$RUN_ID" "$$"
    printf 'test=TestSDKAccountFailureRegressions\npackage=./e2e\n'
    printf 'started_at_utc=%s\nended_at_utc=%s\nexit_code=%s\n' "$STARTED" "$2" "$3"
    printf 'log_file=%s\nstatus_file=%s\n' "$LOG" "$STATUS"
  } > "$STATUS.tmp" && /bin/mv -f "$STATUS.tmp" "$STATUS"
}

write_status RUNNING '' '' || exit 125
printf 'run_id=%s\nlog_file=%s\nstatus_file=%s\n' "$RUN_ID" "$LOG" "$STATUS"
/usr/local/go/bin/go test -json -p=1 -parallel=1 ./e2e \
  -run '^TestSDKAccountFailureRegressions$' -count=1 -timeout=10m > "$LOG" 2>&1
RESULT=$?
STATE=FAILED
if [[ "$RESULT" -eq 0 ]]; then STATE=PASSED; fi
write_status "$STATE" "$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')" "$RESULT" || exit 125
printf 'state=%s\nexit_code=%s\nlog_file=%s\n' "$STATE" "$RESULT" "$LOG"
exit "$RESULT"
