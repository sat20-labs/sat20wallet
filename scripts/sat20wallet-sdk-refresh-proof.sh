#!/bin/bash
set -uo pipefail
umask 077

if [[ $# -ne 0 ]]; then
  printf '%s\n' 'This fixed refresh verification runner accepts no arguments.' >&2
  exit 2
fi
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
root=/Users/yingfeng/github/sat20wallet/.cache/script-logs/sdk-refresh-proof
mkdir -p "$root" || exit 2
run="$(date -u '+%Y%m%dT%H%M%SZ')-$$"
log="$root/$run.jsonl"
status="$root/$run.status"
started=$(date '+%s')
printf 'state=RUNNING\npid=%s\nstarted_epoch=%s\nlog=%s\n' "$$" "$started" "$log" > "$status"
printf 'log=%s\nstatus=%s\n' "$log" "$status"

/usr/bin/env -u GOFLAGS -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS \
  -u SAT20WALLET_LEGACY_INTEGRATION -u SAT20WALLET_CONTRACT_TESTNET \
  -u SAT20_LIVE_CHANNEL_HEARTBEAT -u SAT20WALLET_CORE_E2E_CONFIG \
  -u SAT20WALLET_CORE_E2E_CHILD_RUN \
  /usr/local/go/bin/go test -mod=readonly -json -v -p=1 -parallel=1 \
  -count=1 -timeout=10m -run '^TestRGB11UnchangedRefreshDoesNotRewriteSnapshot$' ./wallet > "$log" 2>&1
go_exit=$?
/usr/bin/python3 - "$log" <<'PY'
import json, sys
root = 'TestRGB11UnchangedRefreshDoesNotRewriteSnapshot'
passed = 0
failed = False
for line in open(sys.argv[1]):
    try:
        event = json.loads(line)
    except ValueError:
        continue
    if event.get('Action') == 'fail':
        failed = True
        print('FAIL', event.get('Test', event.get('Package', '')))
    if event.get('Test') == root and event.get('Action') == 'pass':
        passed += 1
    output = event.get('Output', '')
    if 'unchanged RGB refresh ' in output:
        print(output.strip())
print('exact_root_passes=%d failed=%s' % (passed, failed))
sys.exit(0 if passed == 1 and not failed else 1)
PY
coverage_exit=$?
exit_code=$go_exit
if [[ $exit_code -eq 0 && $coverage_exit -ne 0 ]]; then exit_code=$coverage_exit; fi
state=FAILED
if [[ $exit_code -eq 0 ]]; then state=PASSED; fi
ended=$(date '+%s')
printf 'state=%s\npid=%s\nstarted_epoch=%s\nended_epoch=%s\nduration_seconds=%s\ngo_exit=%s\ncoverage_exit=%s\nexit_code=%s\nlog=%s\n' \
  "$state" "$$" "$started" "$ended" "$((ended-started))" "$go_exit" "$coverage_exit" "$exit_code" "$log" > "$status"
exit "$exit_code"
