#!/bin/bash
set -uo pipefail
umask 077

WORKDIR=/Users/yingfeng/github/sat20wallet/sdk
LOG_ROOT=/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs/sat20wallet-sdk-agent
mode="${1:-case}"
if [[ "$#" -gt 1 ]] || [[ "$mode" != case && "$mode" != group ]]; then
  printf '%s\n' 'usage: sat20wallet-sdk-agent.sh [case|group]' >&2
  exit 2
fi
if [[ "$mode" == case ]]; then
  selector='^TestRealSatoshiNetAgentPredictionPayoutByShare$'
else
  selector='^TestRealSatoshiNetAgentPrediction(TenBettors|PayoutByShare)$'
fi
mkdir -p "$LOG_ROOT" || exit 125
started_epoch="$(date +%s)"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
log_file="$LOG_ROOT/$run_id.log"
status_file="$LOG_ROOT/$run_id.status"
go_exit=""
gate_exit=""
write_status() {
  {
    printf 'state=%s\nrun_id=%s\npid=%s\nmode=%s\n' "$1" "$run_id" "$$" "$mode"
    printf 'selector=%s\nstarted_epoch=%s\n' "$selector" "$started_epoch"
    printf 'go_exit_code=%s\ngate_exit_code=%s\nexit_code=%s\n' "$go_exit" "$gate_exit" "${2:-}"
    printf 'ended_epoch=%s\nduration_seconds=%s\n' "${3:-}" "${4:-}"
    printf 'log_file=%s\nstatus_file=%s\n' "$log_file" "$status_file"
  } > "$status_file.tmp" && mv -f "$status_file.tmp" "$status_file"
}
write_status RUNNING || exit 125
printf 'run_id=%s\nlog_file=%s\nstatus_file=%s\n' "$run_id" "$log_file" "$status_file"
(
  cd "$WORKDIR" || exit 125
  exec /usr/bin/env -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS \
    -u SAT20WALLET_LEGACY_INTEGRATION -u SAT20WALLET_CONTRACT_TESTNET \
    -u SAT20WALLET_CORE_E2E_CONFIG -u SAT20WALLET_CORE_E2E_CHILD_RUN \
    /usr/local/go/bin/go test -mod=readonly -json -p=1 -parallel=1 -count=1 \
    -timeout=20m -run "$selector" ./e2e
) > "$log_file" 2>&1
go_exit=$?
/usr/bin/python3 - "$log_file" "$mode" <<'PY_GATE' >> "$log_file" 2>&1
import collections
import json
import sys

expected = {"TestRealSatoshiNetAgentPredictionPayoutByShare"}
if sys.argv[2] == "group":
    expected.add("TestRealSatoshiNetAgentPredictionTenBettors")
runs = collections.Counter()
verdicts = collections.defaultdict(list)
packages = []
with open(sys.argv[1]) as stream:
    for raw in stream:
        try:
            event = json.loads(raw)
        except ValueError:
            continue
        name, action = event.get("Test"), event.get("Action")
        if name and action == "run":
            runs[name] += 1
        if name and action in {"pass", "fail", "skip"}:
            verdicts[name].append(action)
        if not name and action in {"pass", "fail", "skip"}:
            packages.append((event.get("Package"), action))
valid = (set(runs) == expected and all(n == 1 for n in runs.values())
         and set(verdicts) == expected and all(v == ["pass"] for v in verdicts.values())
         and packages == [("github.com/sat20-labs/sat20wallet/sdk/e2e", "pass")])
print("[agent-coverage] " + ("PASSED" if valid else "FAILED")
      + ": exact selected roots once, all PASS, no SKIP or extra case")
raise SystemExit(0 if valid else 1)
PY_GATE
gate_exit=$?
ended_epoch="$(date +%s)"
duration=$((ended_epoch - started_epoch))
if (( go_exit == 0 && gate_exit == 0 )); then
  write_status PASSED 0 "$ended_epoch" "$duration" || exit 125
  exit 0
fi
write_status FAILED 1 "$ended_epoch" "$duration" || exit 125
exit 1
