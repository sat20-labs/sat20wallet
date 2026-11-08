#!/bin/bash
set -uo pipefail
umask 077

# Two fixed, serial Core scopes. Each gets its own existing E2E fixture;
# a failed Direct case must not suppress the independent eight-group stage.
if [[ "$#" -ne 0 ]]; then
  printf '%s\n' 'This fixed Core Direct/tail runner accepts no arguments.' >&2
  exit 2
fi
ROOT="/Users/yingfeng/github/sat20wallet"
GO_BIN="/usr/local/go/bin/go"
LOG_ROOT="$ROOT/.cache/script-logs"
RUN_ID="sdk-core-direct-tail-$(/bin/date -u '+%Y%m%dT%H%M%SZ')-$$"
REPORT="$ROOT/sdk/review-evidence/core-modules-e2e-latest.json"
DIRECT='direct_transfer_every_paid_recovery_boundary'
TAIL='RGB11_same_root_three_device_cas_cancel_broadcast_and_respend|RGB11_recovery_identity_batch_and_hop_lifecycles|RGB11_R11_stable_only_cold_restore_ignores_valid_and_corrupt_legacy_cache|RGB11_R23_temporary_TTL_expiry_cold_restore_and_respend|RGB11_R09_same_root_real_CAS_loser_retains_private_intent_and_tests_recovery|RGB11_recovery_capacity_boundaries|RGB11_account_recovery_failure_boundaries|guardian_two_of_three_and_wrong_knowledge'
unset GOFLAGS GOOS GOARCH SAT20WALLET_RUN_LIVE_NETWORK_TESTS \
  SAT20WALLET_LEGACY_INTEGRATION SAT20WALLET_CONTRACT_TESTNET \
  SAT20_LIVE_CHANNEL_HEARTBEAT SAT20WALLET_CORE_E2E_CONFIG \
  SAT20WALLET_CORE_E2E_CHILD_RUN
[[ -x "$GO_BIN" && -x /usr/bin/python3 ]] || exit 127
/bin/mkdir -p "$LOG_ROOT" || exit 125
cd "$ROOT/sdk" || exit 125

write_status() {
  local phase="$1" state="$2" start="$3" end="$4" go_exit="$5" coverage_exit="$6" duration="$7"
  local path="$LOG_ROOT/$RUN_ID.$phase.status"
  {
    printf 'run_id=%s\npid=%s\nphase=%s\nstate=%s\n' "$RUN_ID" "$$" "$phase" "$state"
    printf 'started_at_utc=%s\nended_at_utc=%s\ngo_exit_code=%s\ncoverage_exit_code=%s\nduration_seconds=%s\n' \
      "$start" "$end" "$go_exit" "$coverage_exit" "$duration"
    printf 'log_file=%s\nreport_file=%s\ncoverage_file=%s\n' \
      "$LOG_ROOT/$RUN_ID.$phase.jsonl" "$LOG_ROOT/$RUN_ID.$phase.report.json" "$LOG_ROOT/$RUN_ID.$phase.coverage.log"
  } > "$path.tmp" && /bin/mv -f "$path.tmp" "$path"
}
for phase in direct tail; do
  write_status "$phase" NOTRUN '' '' '' '' '' || exit 125
done
printf 'run_id=%s\nlog_root=%s\n' "$RUN_ID" "$LOG_ROOT"

run_phase() {
  local phase="$1" groups="$2"
  local selector="^TestSDKCoreModulesConnectedE2E$/^($groups)$"
  local start epoch end duration go_exit coverage_exit state
  start="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
  epoch="$(/bin/date '+%s')"
  write_status "$phase" RUNNING "$start" '' '' '' '' || return 125
  # Existing Core runner budget and shared TestMain lock remain unchanged.
  /usr/bin/env "SAT20WALLET_CORE_E2E_CHILD_RUN=$selector" \
    "$GO_BIN" test -mod=readonly -json -p=1 -parallel=1 -count=1 \
    -timeout=100m -run '^TestSDKCoreModulesE2E$' ./e2e \
    > "$LOG_ROOT/$RUN_ID.$phase.jsonl" 2>&1
  go_exit=$?
  /usr/bin/python3 - "$LOG_ROOT/$RUN_ID.$phase.jsonl" "$REPORT" \
    "$LOG_ROOT/$RUN_ID.$phase.report.json" "$selector" "$groups" \
    > "$LOG_ROOT/$RUN_ID.$phase.coverage.log" 2>&1 <<'PY_COVERAGE'
import collections
import datetime
import json
import pathlib
import sys

log, source, saved, selection, groups = sys.argv[1:]
root = "TestSDKCoreModulesE2E"
child = "TestSDKCoreModulesConnectedE2E"
expected_groups = {child + "/" + group for group in groups.split("|")}
events = []
for line in pathlib.Path(log).read_text().splitlines():
    try:
        event = json.loads(line)
    except ValueError:
        continue
    if isinstance(event, dict):
        events.append(event)
starts = [e for e in events if e.get("Test") == root and e.get("Action") == "run"]
ends = [e for e in events if e.get("Test") == root and e.get("Action") in {"pass", "fail"}]

def instant(stamp):
    return datetime.datetime.fromisoformat(stamp.replace("Z", "+00:00"))

try:
    report = json.loads(pathlib.Path(source).read_text())
    current = (len(starts) == len(ends) == 1 and report.get("started_by") == root
               and report.get("child_run") == selection
               and instant(starts[0]["Time"]) <= instant(report["finished_at"]) <= instant(ends[0]["Time"]))
except (OSError, ValueError, KeyError, TypeError):
    current = False
if not current:
    print("FAILED: no timestamp-matched Core report; inspect this phase's JSONL")
    raise SystemExit(1)
# Preserve this stage's report before the independent next stage replaces it.
pathlib.Path(saved).write_text(json.dumps(report, indent=2) + "\n")
verdicts = report.get("verdicts", [])
failed = [e.get("Test", "") for e in verdicts if e.get("Action") == "fail"]
skipped = [e.get("Test", "") for e in verdicts if e.get("Action") == "skip"]
passed = {e.get("Test", "") for e in verdicts if e.get("Action") == "pass"}
missing = sorted(expected_groups - passed)
print("failed=" + json.dumps(failed))
print("skipped=" + json.dumps(skipped))
print("selected_groups_without_pass=" + json.dumps(missing))
counts = collections.Counter(e.get("Test", "") for e in verdicts)
outer_pass = (sum(e.get("Action") == "pass" and not e.get("Test") for e in events) == 1
              and ends[0].get("Action") == "pass"
              and not any(e.get("Action") in {"fail", "skip"} for e in events))
scope_ok = all(name == child or any(name == g or name.startswith(g + "/") for g in expected_groups)
               for name in counts)
ok = (outer_pass and report.get("passed") is True and report.get("child_exit_code") == 0
      and child in passed and not missing and not failed and not skipped and scope_ok
      and all(count == 1 for count in counts.values()))
print("coverage=" + ("PASSED" if ok else "FAILED"))
raise SystemExit(0 if ok else 1)
PY_COVERAGE
  coverage_exit=$?
  end="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
  duration=$(( $(/bin/date '+%s') - epoch ))
  state=FAILED
  if [[ "$go_exit" -eq 0 && "$coverage_exit" -eq 0 ]]; then state=PASSED; fi
  write_status "$phase" "$state" "$start" "$end" "$go_exit" "$coverage_exit" "$duration" || return 125
  printf 'phase=%s\nstate=%s\ngo_exit_code=%s\ncoverage_exit_code=%s\nduration_seconds=%s\nstatus_file=%s\n' \
    "$phase" "$state" "$go_exit" "$coverage_exit" "$duration" "$LOG_ROOT/$RUN_ID.$phase.status"
  [[ "$state" == PASSED ]]
}

run_phase direct "$DIRECT"
direct_exit=$?
# A separate existing Core fixture makes these eight groups independent.
run_phase tail "$TAIL"
tail_exit=$?
if [[ "$direct_exit" -eq 0 && "$tail_exit" -eq 0 ]]; then exit 0; fi
exit 1
