#!/bin/bash
set -uo pipefail

GO_BIN="/usr/local/go/bin/go"
WORKDIR="/Users/yingfeng/github/sat20wallet/sdk"
WORKSPACE="/Users/yingfeng/github"
LOG_ROOT="/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs/sat20wallet-sdk-core-recovery-failure"
SUITE="sat20wallet-sdk-core-recovery-failure"
TIMEOUT_MS="6000000"
CORE_OUTER_RUN='^TestSDKCoreModulesE2E$'
# Fixed account-recovery failure group and its six cases; final acceptance still requires all 16 groups.
CORE_NATIVE_RUN='^TestSDKCoreModulesConnectedE2E$/^(RGB11_account_recovery_failure_boundaries)$'

if [[ "$#" -ne 0 ]]; then
  printf '%s\n' "This fixed SDK Core recovery-failure runner accepts no arguments." >&2
  exit 2
fi
if [[ ! -x "$GO_BIN" ]]; then
  printf '%s\n' "Go executable not found: $GO_BIN" >&2
  exit 127
fi
if [[ ! -x /usr/bin/python3 ]]; then
  printf '%s\n' "Python 3 is required to verify the current Core child report." >&2
  exit 127
fi
if [[ ! -d "$WORKDIR/e2e" ]]; then
  printf '%s\n' "missing SDK E2E package: $WORKDIR/e2e" >&2
  exit 2
fi
for dependency in satoshinet indexer rgb11 transcend; do
  if [[ ! -d "$WORKSPACE/$dependency" ]]; then
    printf '%s\n' "missing dependency required by SDK Core E2E: $WORKSPACE/$dependency" >&2
    exit 2
  fi
done
if ! /bin/mkdir -p "$LOG_ROOT"; then
  printf '%s\n' "cannot create run log directory: $LOG_ROOT" >&2
  exit 2
fi

started_epoch="$(/bin/date '+%s')"
started_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
started_at_local="$(/bin/date '+%Y-%m-%dT%H:%M:%S%z')"
stamp="$(/bin/date -u '+%Y%m%dT%H%M%SZ')"
run_id="${stamp}-$$"
log_file="$LOG_ROOT/$run_id.log"
status_file="$LOG_ROOT/$run_id.status"
status_tmp="$status_file.tmp"
report_file="$WORKDIR/review-evidence/core-modules-e2e-latest.json"

core_state="PENDING"
core_go_exit_code=""
core_coverage_exit_code=""
core_started_at_utc=""
core_ended_at_utc=""
core_duration_seconds=""
final_state="RUNNING"
final_exit_code=""

core_cmd=(/usr/bin/env -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS \
  "SAT20WALLET_CORE_E2E_CHILD_RUN=$CORE_NATIVE_RUN" \
  "$GO_BIN" test -mod=readonly -json -p=1 -parallel=1 -count=1 \
  -timeout=100m -run "$CORE_OUTER_RUN" ./e2e)
printf -v command_args '%q ' "${core_cmd[@]}"
command_text="+ ${command_args% }"

write_status() {
  local state="$1"
  local exit_code="$2"
  local ended_at_utc="$3"
  local ended_at_local="$4"
  local ended_epoch_value="$5"
  local duration="$6"

  {
    printf 'state=%s\n' "$state"
    printf 'suite=%s\n' "$SUITE"
    printf 'scope=fixed 1 native account-recovery failure group and 6 nested cases; other 15 native groups EXCLUDED; not full-16 acceptance\n'
    printf 'run_id=%s\n' "$run_id"
    printf 'pid=%s\n' "$$"
    printf 'working_directory=%s\n' "$WORKDIR"
    printf 'timeout_ms=%s\n' "$TIMEOUT_MS"
    printf 'started_at_utc=%s\n' "$started_at_utc"
    printf 'started_at_local=%s\n' "$started_at_local"
    printf 'started_epoch=%s\n' "$started_epoch"
    printf 'core_state=%s\n' "$core_state"
    printf 'core_go_exit_code=%s\n' "$core_go_exit_code"
    printf 'core_coverage_exit_code=%s\n' "$core_coverage_exit_code"
    printf 'core_started_at_utc=%s\n' "$core_started_at_utc"
    printf 'core_ended_at_utc=%s\n' "$core_ended_at_utc"
    printf 'core_duration_seconds=%s\n' "$core_duration_seconds"
    printf 'command=%s\n' "$command_text"
    printf 'ended_at_utc=%s\n' "$ended_at_utc"
    printf 'ended_at_local=%s\n' "$ended_at_local"
    printf 'ended_epoch=%s\n' "$ended_epoch_value"
    printf 'duration_seconds=%s\n' "$duration"
    printf 'exit_code=%s\n' "$exit_code"
    printf 'log_file=%s\n' "$log_file"
    printf 'status_file=%s\n' "$status_file"
  } > "$status_tmp" || return 1
  /bin/mv -f "$status_tmp" "$status_file"
}

if ! : > "$log_file"; then
  printf '%s\n' "cannot create run log: $log_file" >&2
  exit 125
fi
if ! write_status "$final_state" "" "" "" "" ""; then
  printf '%s\n' "cannot write run status: $status_file" >&2
  exit 125
fi
{
  printf 'run_id=%s\n' "$run_id"
  printf 'status_file=%s\n' "$status_file"
  printf 'log_file=%s\n' "$log_file"
  printf 'started_at_utc=%s\n' "$started_at_utc"
  printf 'suite=%s\n' "$SUITE"
  printf 'scope=single SDK Core integration entry; fixed 1 native account-recovery failure group and 6 nested cases; other 15 native groups EXCLUDED; not full-16 acceptance; no PWA/browser cases\n'
  printf 'timeout_ms=%s\n' "$TIMEOUT_MS"
  printf '%s\n' "$command_text"
} | /usr/bin/tee -a "$log_file"
if (( $? != 0 )); then
  ended_epoch="$(/bin/date '+%s')"
  ended_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
  ended_at_local="$(/bin/date '+%Y-%m-%dT%H:%M:%S%z')"
  duration_seconds=$(( ended_epoch - started_epoch ))
  core_state="NOTRUN_LOG_INITIALIZATION_FAILED"
  write_status "FAILED" "125" "$ended_at_utc" "$ended_at_local" "$ended_epoch" "$duration_seconds"
  exit 125
fi

core_state="RUNNING"
core_started_epoch="$(/bin/date '+%s')"
core_started_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
write_status "RUNNING" "" "" "" "" ""
printf '[stage] name=core state=RUNNING started_at_utc=%s timeout_ms=%s outer_run=%s child_run=%s\n%s\n' \
  "$core_started_at_utc" "$TIMEOUT_MS" "$CORE_OUTER_RUN" "$CORE_NATIVE_RUN" "$command_text" | /usr/bin/tee -a "$log_file"
(
  cd "$WORKDIR" || exit 125
  "${core_cmd[@]}"
) 2>&1 | /usr/bin/tee -a "$log_file"
core_go_exit_code=$?
core_ended_epoch="$(/bin/date '+%s')"
core_ended_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
core_duration_seconds=$(( core_ended_epoch - core_started_epoch ))

# A successful Go exit is insufficient: require this run's timestamp-matched
# report and an explicit PASS verdict for every selected native group.
/usr/bin/python3 - "$log_file" "$report_file" "$CORE_NATIVE_RUN" <<'PY_CORE' 2>&1 | /usr/bin/tee -a "$log_file"
import datetime
import decimal
import json
import re
import sys


def fail(message):
    print("[core-recovery-failure-coverage] FAILED: " + message)
    raise SystemExit(1)


def instant(value):
    stamp = re.fullmatch(
        r"([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})"
        r"(?:\.([0-9]{1,9}))?(Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])", value)
    if stamp is None:
        raise ValueError("invalid RFC3339 timestamp")
    whole = datetime.datetime.fromisoformat(stamp[1] + stamp[3].replace("Z", "+00:00"))
    elapsed = whole - datetime.datetime(1970, 1, 1, tzinfo=datetime.timezone.utc)
    seconds = elapsed.days * 86400 + elapsed.seconds
    return decimal.Decimal(seconds) + decimal.Decimal("0." + (stamp[2] or "0"))


log_path, report_path, selection = sys.argv[1:]
root = "TestSDKCoreModulesE2E"
outer_package = "github.com/sat20-labs/sat20wallet/sdk/e2e"
outer = {}
try:
    with open(log_path) as stream:
        for line in stream:
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if event.get("Package") == outer_package and event.get("Test") == root:
                outer[event.get("Action")] = event.get("Time")
except OSError as exc:
    fail("cannot read Core JSONL: " + str(exc))
terminal_action = "pass" if outer.get("pass") else "fail" if outer.get("fail") else None
terminal_time = outer.get(terminal_action) if terminal_action else None
if not outer.get("run") or not terminal_time:
    fail("Core parent did not produce both a run and terminal event")

match = re.fullmatch(r"\^TestSDKCoreModulesConnectedE2E\$/\^\(([^()]*)\)\$", selection)
if not match:
    fail("fixed child selector format changed")
expected = set(match.group(1).split("|"))
if len(expected) != 1:
    fail("fixed selector does not contain exactly 1 group")
try:
    with open(report_path) as stream:
        report = json.load(stream)
except (OSError, ValueError) as exc:
    fail("cannot read current Core report: " + str(exc))
if report.get("started_by") != root or report.get("child_run") != selection:
    fail("Core report root or child selector differs")
if report.get("passed") is not True or report.get("child_exit_code") != 0:
    fail("Core child did not finish successfully")
finished_at = report.get("finished_at")
if not isinstance(finished_at, str):
    fail("Core report has no finished_at timestamp")
try:
    outer_start = instant(outer["run"])
    report_finish = instant(finished_at)
    outer_finish = instant(terminal_time)
except (TypeError, ValueError) as exc:
    fail("Core report or outer timestamps are invalid: " + str(exc))
if outer_start > report_finish or report_finish > outer_finish:
    fail("Core report timestamp is not inside this outer test run")
prefix = "TestSDKCoreModulesConnectedE2E/"
verdicts = report.get("verdicts", [])
if not isinstance(verdicts, list):
    fail("Core report verdicts are not a list")
groups = [row for row in verdicts if isinstance(row, dict) and row.get("Test", "").startswith(prefix)
          and "/" not in row["Test"][len(prefix):]]
actual = [row.get("Test")[len(prefix):] for row in groups]
if len(groups) != 1 or len(set(actual)) != 1 or set(actual) != expected:
    fail("Core report has missing, duplicate, or unexpected group verdicts")
if any(row.get("Action") != "pass" for row in groups):
    fail("the selected recovery-failure group did not PASS")
nested_prefix = prefix + "RGB11_account_recovery_failure_boundaries/"
expected_cases = {
    "RGB11ProviderFailureColdDatabaseRestartResumes",
    "MissingRGB11ModuleIsolatedAndRetryable",
    "CorruptRGB11ModuleIsolatedAndRetryable",
    "RGB11ProviderFailureMustAllowSameDatabaseRetry",
    "WalletRemoteApplyDoesNotReimportUnchangedRGB",
    "WalletRebaseDoesNotReimportUnchangedRGB",
}
cases = [row for row in verdicts if isinstance(row, dict) and row.get("Test", "").startswith(nested_prefix)
         and "/" not in row["Test"][len(nested_prefix):]]
actual_cases = [row["Test"][len(nested_prefix):] for row in cases]
if len(cases) != 6 or len(set(actual_cases)) != 6 or set(actual_cases) != expected_cases:
    fail("Core report has missing, duplicate, or unexpected recovery-failure cases")
if any(row.get("Action") != "pass" for row in cases):
    fail("one or more of the 6 recovery-failure cases did not PASS")
if any(isinstance(row, dict) and row.get("Action") == "skip" and row.get("Test", "").startswith(prefix)
       for row in verdicts):
    fail("a selected Core child was skipped; SKIP is not PASS")
if terminal_action != "pass":
    fail("outer TestSDKCoreModulesE2E did not PASS")
print("[core-recovery-failure-coverage] PASSED: current timestamp-matched report; the selected recovery-failure group and all 6 nested cases PASS; other 15 native groups EXCLUDED; not full-16 acceptance; no child SKIP")
PY_CORE
core_coverage_exit_code=$?

if (( core_go_exit_code == 0 && core_coverage_exit_code == 0 )); then
  core_state="PASSED"
  core_exit_code="0"
  final_state="PASSED"
  final_exit_code="0"
else
  core_state="FAILED"
  core_exit_code="1"
  final_state="FAILED"
  final_exit_code="1"
fi
write_status "RUNNING" "" "" "" "" ""
printf '[stage] name=core state=%s go_exit_code=%s coverage_exit_code=%s exit_code=%s ended_at_utc=%s duration_seconds=%s\n' \
  "$core_state" "$core_go_exit_code" "$core_coverage_exit_code" "$core_exit_code" "$core_ended_at_utc" "$core_duration_seconds" | /usr/bin/tee -a "$log_file"

ended_epoch="$(/bin/date '+%s')"
ended_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
ended_at_local="$(/bin/date '+%Y-%m-%dT%H:%M:%S%z')"
duration_seconds=$(( ended_epoch - started_epoch ))
if ! write_status "$final_state" "$final_exit_code" "$ended_at_utc" "$ended_at_local" "$ended_epoch" "$duration_seconds"; then
  printf '%s\n' "could not persist final status: $status_file" >&2
  final_exit_code="125"
fi
if ! printf '[test-run] state=%s exit_code=%s core_exit_code=%s ended_at_utc=%s run_id=%s status_file=%s log_file=%s\n' \
  "$final_state" "$final_exit_code" "$core_exit_code" "$ended_at_utc" "$run_id" "$status_file" "$log_file" | /usr/bin/tee -a "$log_file"; then
  if [[ "$final_exit_code" == "0" ]]; then
    final_exit_code="125"
  fi
fi
exit "$final_exit_code"
