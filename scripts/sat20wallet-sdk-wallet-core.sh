#!/bin/bash
set -uo pipefail

GO_BIN="/usr/local/go/bin/go"
WORKDIR="/Users/yingfeng/github/sat20wallet/sdk"
WORKSPACE="/Users/yingfeng/github"
LOG_ROOT="/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs/sat20wallet-sdk-wallet-core"
SUITE="sat20wallet-sdk-wallet-core"
TOTAL_TIMEOUT_MS="6000000"
WALLET_TIMEOUT_CAP_MS="3000000"
DIRECT_CORE_SKIP='^(TestSDKCoreModulesConnectedE2E|TestSDKCoreModulesValidation)$'
CORE_OUTER_RUN='^TestSDKCoreModulesE2E$'
# Fixed 16 native child groups from sat20wallet-sdk-native.sh. No PWA/browser groups.
CORE_NATIVE_RUN='^TestSDKCoreModulesConnectedE2E$/^(dkvs_real_paid_cross_instance_prefix_and_CAS|backup_multiple_wallets_and_subaccounts_control|RGB11_self_send_cold_restore_and_respend|RGB11_asset_name_and_transcend_registration_descriptor|RGB_issuance_in_multiple_subaccounts|invoice_transfer_every_paid_recovery_boundary|direct_transfer_every_paid_recovery_boundary|RGB11_same_root_three_device_cas_cancel_broadcast_and_respend|RGB11_recovery_identity_batch_and_hop_lifecycles|RGB11_fullsend_history_cold_recovery_reorg_and_competing_intent|RGB11_R11_stable_only_cold_restore_ignores_valid_and_corrupt_legacy_cache|RGB11_R23_temporary_TTL_expiry_cold_restore_and_respend|RGB11_R09_same_root_real_CAS_loser_retains_private_intent_and_tests_recovery|RGB11_recovery_capacity_boundaries|RGB11_account_recovery_failure_boundaries|guardian_two_of_three_and_wrong_knowledge)$'

if [[ "$#" -ne 0 ]]; then
  printf '%s\n' "This fixed SDK wallet + Core runner accepts no arguments." >&2
  exit 2
fi
if [[ ! -x /usr/bin/python3 ]]; then
  printf '%s\n' "Python 3 is required to verify the current Core child summary." >&2
  exit 127
fi
if [[ ! -x "$GO_BIN" ]]; then
  printf '%s\n' "Go executable not found: $GO_BIN" >&2
  exit 127
fi
for path in "$WORKDIR/wallet" "$WORKDIR/e2e"; do
  if [[ ! -d "$path" ]]; then
    printf '%s\n' "missing SDK package directory: $path" >&2
    exit 2
  fi
done
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

wallet_state="PENDING"
wallet_exit_code=""
wallet_started_at_utc=""
wallet_started_epoch=""
wallet_ended_at_utc=""
wallet_duration_seconds=""
wallet_timeout_ms="$WALLET_TIMEOUT_CAP_MS"
core_state="PENDING"
core_exit_code=""
core_go_exit_code=""
core_coverage_exit_code=""
core_started_at_utc=""
core_ended_at_utc=""
core_duration_seconds=""
core_timeout_ms=""
ended_at_utc=""
ended_at_local=""
ended_epoch=""
duration_seconds=""
final_exit_code=""
final_state="RUNNING"
status_write_failed=0

wallet_cmd=(/usr/bin/env -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS "$GO_BIN" test -mod=readonly -json -p=1 -parallel=1 -count=1 "-timeout=${wallet_timeout_ms}ms" -skip "$DIRECT_CORE_SKIP" ./wallet)
printf -v wallet_command_text '%q ' "${wallet_cmd[@]}"
wallet_command_text="+ ${wallet_command_text% }"
core_command_text="PENDING: timeout is the remaining shared 100-minute budget"

write_status() {
  local state="$1"
  local exit_code="$2"
  {
    printf 'state=%s\n' "$state"
    printf 'suite=%s\n' "$SUITE"
    printf 'run_id=%s\n' "$run_id"
    printf 'pid=%s\n' "$$"
    printf 'working_directory=%s\n' "$WORKDIR"
    printf 'total_timeout_ms=%s\n' "$TOTAL_TIMEOUT_MS"
    printf 'wallet_timeout_cap_ms=%s\n' "$WALLET_TIMEOUT_CAP_MS"
    printf 'budget_policy=shared 100m total; wallet capped at 50m; Core receives remaining budget\n'
    printf 'started_at_utc=%s\n' "$started_at_utc"
    printf 'started_at_local=%s\n' "$started_at_local"
    printf 'started_epoch=%s\n' "$started_epoch"
    printf 'wallet_state=%s\n' "$wallet_state"
    printf 'wallet_timeout_ms=%s\n' "$wallet_timeout_ms"
    printf 'wallet_exit_code=%s\n' "$wallet_exit_code"
    printf 'wallet_started_at_utc=%s\n' "$wallet_started_at_utc"
    printf 'wallet_ended_at_utc=%s\n' "$wallet_ended_at_utc"
    printf 'wallet_duration_seconds=%s\n' "$wallet_duration_seconds"
    printf 'wallet_command=%s\n' "$wallet_command_text"
    printf 'core_state=%s\n' "$core_state"
    printf 'core_timeout_ms=%s\n' "$core_timeout_ms"
    printf 'core_go_exit_code=%s\n' "$core_go_exit_code"
    printf 'core_coverage_exit_code=%s\n' "$core_coverage_exit_code"
    printf 'core_exit_code=%s\n' "$core_exit_code"
    printf 'core_started_at_utc=%s\n' "$core_started_at_utc"
    printf 'core_ended_at_utc=%s\n' "$core_ended_at_utc"
    printf 'core_duration_seconds=%s\n' "$core_duration_seconds"
    printf 'core_command=%s\n' "$core_command_text"
    printf 'ended_at_utc=%s\n' "$ended_at_utc"
    printf 'ended_at_local=%s\n' "$ended_at_local"
    printf 'ended_epoch=%s\n' "$ended_epoch"
    printf 'duration_seconds=%s\n' "$duration_seconds"
    printf 'exit_code=%s\n' "$exit_code"
    printf 'log_file=%s\n' "$log_file"
    printf 'status_file=%s\n' "$status_file"
  } > "$status_tmp" || return 1
  /bin/mv -f "$status_tmp" "$status_file"
}

if ! write_status "$final_state" ""; then
  printf '%s\n' "cannot write run status: $status_file" >&2
  exit 125
fi
if ! : > "$log_file"; then
  printf '%s\n' "cannot create run log: $log_file" >&2
  exit 125
fi
{
  printf 'run_id=%s\n' "$run_id"
  printf 'status_file=%s\n' "$status_file"
  printf 'log_file=%s\n' "$log_file"
  printf 'started_at_utc=%s\n' "$started_at_utc"
  printf 'suite=%s\n' "$SUITE"
  printf 'scope=concentrated wallet + Core regression; not the final SDK native full suite\n'
  printf 'total_timeout_ms=%s wallet_timeout_cap_ms=%s\n' "$TOTAL_TIMEOUT_MS" "$WALLET_TIMEOUT_CAP_MS"
  printf 'wallet_command=%s\n' "$wallet_command_text"
} | /usr/bin/tee -a "$log_file"
if (( $? != 0 )); then
  final_state="FAILED"
  final_exit_code="125"
  ended_epoch="$(/bin/date '+%s')"
  ended_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
  ended_at_local="$(/bin/date '+%Y-%m-%dT%H:%M:%S%z')"
  duration_seconds=$(( ended_epoch - started_epoch ))
  write_status "$final_state" "$final_exit_code" || true
  exit 125
fi

wallet_state="RUNNING"
wallet_started_epoch="$(/bin/date '+%s')"
wallet_started_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
write_status "RUNNING" "" || status_write_failed=1
printf '[stage] name=wallet state=RUNNING started_at_utc=%s timeout_ms=%s\n%s\n' \
  "$wallet_started_at_utc" "$wallet_timeout_ms" "$wallet_command_text" | /usr/bin/tee -a "$log_file"
(
  cd "$WORKDIR" || exit 125
  "${wallet_cmd[@]}"
) 2>&1 | /usr/bin/tee -a "$log_file"
wallet_exit_code=$?
wallet_ended_epoch="$(/bin/date '+%s')"
wallet_ended_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
wallet_duration_seconds=$(( wallet_ended_epoch - wallet_started_epoch ))
if (( wallet_exit_code == 0 )); then
  wallet_state="PASSED"
else
  wallet_state="FAILED"
fi
write_status "RUNNING" "" || status_write_failed=1
printf '[stage] name=wallet state=%s exit_code=%s ended_at_utc=%s duration_seconds=%s\n' \
  "$wallet_state" "$wallet_exit_code" "$wallet_ended_at_utc" "$wallet_duration_seconds" | /usr/bin/tee -a "$log_file"

# Stage 2 always follows Stage 1, including a wallet failure. Its timeout is
# bounded by the one shared 100-minute budget; Stage 1's 50-minute cap leaves
# the Core suite its own execution window.
now_epoch="$(/bin/date '+%s')"
elapsed_seconds=$(( now_epoch - started_epoch ))
core_timeout_ms=$(( TOTAL_TIMEOUT_MS - elapsed_seconds * 1000 - 5000 ))
if (( core_timeout_ms > TOTAL_TIMEOUT_MS )); then
  core_timeout_ms="$TOTAL_TIMEOUT_MS"
fi
if (( core_timeout_ms <= 0 )); then
  core_state="NOTRUN_BUDGET_EXHAUSTED"
  core_go_exit_code="124"
  core_coverage_exit_code="125"
  core_exit_code="1"
  core_command_text="NOTRUN: shared 100-minute budget exhausted before Core stage"
  printf '[stage] name=core state=%s reason=shared_budget_exhausted\n' "$core_state" | /usr/bin/tee -a "$log_file"
else
  core_cmd=(/usr/bin/env -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS "SAT20WALLET_CORE_E2E_CHILD_RUN=$CORE_NATIVE_RUN" \
    "$GO_BIN" test -mod=readonly -json -p=1 -parallel=1 -count=1 "-timeout=${core_timeout_ms}ms" -run "$CORE_OUTER_RUN" ./e2e)
  printf -v core_command_text '%q ' "${core_cmd[@]}"
  core_command_text="+ ${core_command_text% }"
  core_state="RUNNING"
  core_started_epoch="$(/bin/date '+%s')"
  core_started_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
  write_status "RUNNING" "" || status_write_failed=1
  printf '[stage] name=core state=RUNNING started_at_utc=%s timeout_ms=%s outer_run=%s child_run=%s\n%s\n' \
    "$core_started_at_utc" "$core_timeout_ms" "$CORE_OUTER_RUN" "$CORE_NATIVE_RUN" "$core_command_text" | /usr/bin/tee -a "$log_file"
  (
    cd "$WORKDIR" || exit 125
    "${core_cmd[@]}"
  ) 2>&1 | /usr/bin/tee -a "$log_file"
  core_go_exit_code=$?
  core_ended_epoch="$(/bin/date '+%s')"
  core_ended_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
  core_duration_seconds=$(( core_ended_epoch - core_started_epoch ))

  /usr/bin/python3 - "$log_file" "$WORKDIR/review-evidence/core-modules-e2e-latest.json" "$CORE_NATIVE_RUN" <<'PY_CORE' 2>&1 | /usr/bin/tee -a "$log_file"
import datetime
import json
import re
import sys


def fail(message):
    print("[core-native-coverage] FAILED: " + message)
    raise SystemExit(1)


def instant(value):
    try:
        return datetime.datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()
    except (AttributeError, TypeError, ValueError):
        fail("invalid report/test timestamp")


log_path, report_path, selection = sys.argv[1:]
root = "TestSDKCoreModulesE2E"
outer_package = "github.com/sat20-labs/sat20wallet/sdk/e2e"
outer = {}
try:
    with open(log_path) as stream:
        for line in stream:
            if not line.strip():
                continue
            try:
                event = json.loads(line)
            except ValueError:
                # Runner headers and stage summaries share the log with Go JSONL.
                continue
            if event.get("Package") == outer_package and event.get("Test") == root:
                outer[event.get("Action")] = event.get("Time")
except OSError as exc:
    fail("cannot read Core JSONL: " + str(exc))
if not outer.get("run") or not outer.get("pass"):
    fail("outer TestSDKCoreModulesE2E did not run and pass")
match = re.fullmatch(r"\^TestSDKCoreModulesConnectedE2E\$/\^\(([^()]*)\)\$", selection)
if not match:
    fail("fixed child selector format changed")
expected = set(match.group(1).split("|"))
if len(expected) != 16:
    fail("fixed selector does not contain exactly 16 distinct groups")
try:
    with open(report_path) as stream:
        report = json.load(stream)
except (OSError, ValueError) as exc:
    fail("cannot read current Core report: " + str(exc))
if report.get("started_by") != root or report.get("child_run") != selection:
    fail("Core report root or child selector differs")
if report.get("passed") is not True or report.get("child_exit_code") != 0:
    fail("Core child did not finish successfully")
if instant(outer["run"]) > instant(report.get("finished_at")) or instant(report.get("finished_at")) > instant(outer["pass"]):
    fail("Core report timestamp is not inside this outer test run")
prefix = "TestSDKCoreModulesConnectedE2E/"
segments = report.get("segments", [])
ordered = selection.split("/^(", 1)[1][:-2].split("|")
plan = [("ordinary", ordered[:9]), ("long-history", ordered[9:10]), ("recovery", ordered[10:])]
if (len(segments) != 3 or len(ordered) != 16
        or ordered[9] != "RGB11_fullsend_history_cold_recovery_reorg_and_competing_intent"
        or report.get("shared_budget_seconds") != 5100 or report.get("budget_exhausted") is not False):
    fail("Core did not complete three segments within the shared 85-minute budget")
endpoints = []
for segment, (name, names) in zip(segments, plan):
    run = "^TestSDKCoreModulesConnectedE2E$/^(" + "|".join(names) + ")$"
    if (segment.get("name") != name or segment.get("child_run") != run
            or segment.get("passed") is not True or segment.get("child_exit_code") != 0):
        fail("Core segment selector or terminal status differs: " + name)
    endpoint = segment.get("node_rpc")
    if not isinstance(endpoint, str) or not endpoint:
        fail("Core segment has no actual node RPC endpoint: " + name)
    endpoints.append(endpoint)
    roots = [row for row in segment.get("verdicts", [])
             if row.get("Test") == "TestSDKCoreModulesConnectedE2E"]
    if len(roots) != 1 or roots[0].get("Action") != "pass":
        fail("Core segment child parent did not PASS exactly once: " + name)
    actual = [row.get("Test", "")[len(prefix):] for row in segment.get("verdicts", [])
              if row.get("Test", "").startswith(prefix) and "/" not in row["Test"][len(prefix):]]
    if len(actual) != len(names) or set(actual) != set(names):
        fail("Core segment has missing, duplicate or unexpected groups: " + name)
if len(set(endpoints)) != 3:
    fail("Core segments reused the same node RPC endpoint")
for field in ("verdicts", "child_events"):
    combined = [row for segment in segments for row in segment.get(field, [])]
    if report.get(field) != combined:
        fail("Core aggregate differs from the actual segment " + field)

verdicts = report.get("verdicts", [])
groups = [row for row in verdicts if isinstance(row, dict) and row.get("Test", "").startswith(prefix)
          and "/" not in row["Test"][len(prefix):]]
actual = [row.get("Test")[len(prefix):] for row in groups]
if len(groups) != 16 or len(set(actual)) != 16 or set(actual) != expected:
    fail("Core report has missing, duplicate, or unexpected group verdicts")
if any(row.get("Action") != "pass" for row in groups):
    fail("one or more of the 16 Core groups did not PASS")
if any(isinstance(row, dict) and row.get("Action") == "skip" and row.get("Test", "").startswith(prefix)
       for row in verdicts):
    fail("a selected Core child was skipped; SKIP is not PASS")
print("[core-native-coverage] PASSED: current timestamp-matched report; all 16 native groups PASS; no child SKIP")
PY_CORE
  core_coverage_exit_code=$?
  if (( core_go_exit_code == 0 && core_coverage_exit_code == 0 )); then
    core_state="PASSED"
    core_exit_code="0"
  else
    core_state="FAILED"
    core_exit_code="1"
  fi
  write_status "RUNNING" "" || status_write_failed=1
  printf '[stage] name=core state=%s go_exit_code=%s coverage_exit_code=%s exit_code=%s ended_at_utc=%s duration_seconds=%s\n' \
    "$core_state" "$core_go_exit_code" "$core_coverage_exit_code" "$core_exit_code" "$core_ended_at_utc" "$core_duration_seconds" | /usr/bin/tee -a "$log_file"
fi

ended_epoch="$(/bin/date '+%s')"
ended_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
ended_at_local="$(/bin/date '+%Y-%m-%dT%H:%M:%S%z')"
duration_seconds=$(( ended_epoch - started_epoch ))
if (( wallet_exit_code == 0 )) && [[ "$core_state" == "PASSED" ]] && (( status_write_failed == 0 )); then
  final_state="PASSED"
  final_exit_code="0"
else
  final_state="FAILED"
  final_exit_code="1"
fi
write_status "$final_state" "$final_exit_code" || {
  printf '%s\n' "could not persist final status: $status_file" >&2
  final_exit_code="125"
}
if ! printf '[test-run] state=%s exit_code=%s wallet_exit_code=%s core_exit_code=%s ended_at_utc=%s run_id=%s status_file=%s log_file=%s\n' \
  "$final_state" "$final_exit_code" "$wallet_exit_code" "$core_exit_code" "$ended_at_utc" "$run_id" "$status_file" "$log_file" | /usr/bin/tee -a "$log_file"; then
  if [[ "$final_exit_code" == "0" ]]; then
    final_exit_code="125"
  fi
fi
exit "$final_exit_code"
