#!/bin/bash
set -uo pipefail
umask 077

GO_BIN="/usr/local/go/bin/go"
WORKDIR="/Users/yingfeng/github/sat20wallet/sdk"
WORKSPACE="/Users/yingfeng/github"
LOG_ROOT="/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs/sat20wallet-sdk-core-invoice"
SUITE="sat20wallet-sdk-core-invoice"
TIMEOUT_MS="6000000"
CORE_OUTER_RUN='^TestSDKCoreModulesE2E$'
# Fixed complete invoice recovery group; no passed native5/Agent2 stages or other Core groups.
CORE_NATIVE_RUN='^TestSDKCoreModulesConnectedE2E$/^(invoice_transfer_every_paid_recovery_boundary)$'

if [[ "$#" -ne 0 ]]; then
  printf '%s\n' "This fixed SDK Core invoice-group runner accepts no arguments." >&2
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
  -u SAT20WALLET_LEGACY_INTEGRATION -u SAT20WALLET_CONTRACT_TESTNET \
  -u SAT20_LIVE_CHANNEL_HEARTBEAT -u SAT20WALLET_CORE_E2E_CONFIG \
  -u SAT20WALLET_CORE_E2E_CHILD_RUN \
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
    printf 'scope=complete invoice recovery group only; 17 checkpoints; native5/Agent2 and other 15 groups EXCLUDED; not full-16 acceptance\n'
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
  printf 'scope=single SDK Core integration entry; complete invoice recovery group only; 17 checkpoints; native5/Agent2 and other 15 groups EXCLUDED; not full-16 acceptance; no PWA/browser cases\n'
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
import collections
import datetime
import decimal
import json
import re
import sys


def check(condition, message):
    if not condition:
        print("[core-invoice-coverage] FAILED: " + message)
        raise SystemExit(1)


def instant(value):
    stamp = re.fullmatch(
        r"([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})"
        r"(?:\.([0-9]{1,9}))?(Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])", value)
    if stamp is None:
        raise ValueError("invalid RFC3339 timestamp")
    whole = datetime.datetime.fromisoformat(stamp[1] + stamp[3].replace("Z", "+00:00"))
    elapsed = whole - datetime.datetime(1970, 1, 1, tzinfo=datetime.timezone.utc)
    return decimal.Decimal(elapsed.days * 86400 + elapsed.seconds) + decimal.Decimal("0." + (stamp[2] or "0"))


log_path, report_path, selection = sys.argv[1:]
expected = {"TestSDKCoreModulesE2E"}
package = "github.com/sat20-labs/sat20wallet/sdk/e2e"
events = collections.defaultdict(lambda: collections.defaultdict(list))
with open(log_path) as stream:
    for line in stream:
        try:
            event = json.loads(line)
        except ValueError:
            continue
        check(event.get("Package") == package, "unexpected package in Core invoice stage")
        name, action = event.get("Test", ""), event.get("Action")
        check(not name or name.split("/", 1)[0] in expected, "unexpected test: " + name)
        check(action not in {"fail", "skip"}, action + ": " + (name or package) if action else "invalid action")
        if action in {"start", "run", "pass"}:
            events[name][action].append(instant(event["Time"]))
check(len(events[""]["start"]) == len(events[""]["pass"]) == 1, "package must start and PASS once")
start, end = events[""]["start"][0], events[""]["pass"][0]
for name in expected:
    check(len(events[name]["run"]) == len(events[name]["pass"]) == 1, "missing or duplicate root run/PASS: " + name)
for name, actions in events.items():
    if name:
        check(len(actions["run"]) == len(actions["pass"]) == 1, "nested case did not run and PASS once: " + name)
        check(start <= actions["run"][0] <= actions["pass"][0] <= end, "invalid test time order: " + name)

check(selection == '^TestSDKCoreModulesConnectedE2E$/^(invoice_transfer_every_paid_recovery_boundary)$', "fixed Core selector changed")
with open(report_path) as stream:
    report = json.load(stream)
check(report.get("started_by") == "TestSDKCoreModulesE2E" and report.get("child_run") == selection, "Core report selector differs")
check(report.get("passed") is True and report.get("child_exit_code") == 0, "Core child did not PASS")
parent = events["TestSDKCoreModulesE2E"]
check(parent["run"][0] <= instant(report["finished_at"]) <= parent["pass"][0], "Core report is not from this run")
child_root = "TestSDKCoreModulesConnectedE2E"
group = child_root + "/invoice_transfer_every_paid_recovery_boundary"
checkpoints = [
    "01_issued_sender", "02_invoice_receiver", "03_prepared_sender",
    "malformed_consignment_cannot_create_ownership", "04_proxy_consignment_waiting_receiver",
    "05_proxy_positive_ACK_cold_recovery", "06_broadcast_sender", "07_settled_sender", "08_settled_receiver",
    "08b_standard_return_invoice_receiver", "08c_standard_return_prepared_sender", "08d_standard_return_ACK_receiver",
    "09_blind_invoice_receiver", "10_blind_prepared_sender", "11_blind_receiver_prepared",
    "12_blind_sender_broadcast", "13_blind_receiver_accepted",
]
expected_order = [child_root, group] + [group + "/" + name for name in checkpoints]
verdicts = report.get("verdicts", [])
check(isinstance(verdicts, list) and all(isinstance(r, dict) for r in verdicts), "invalid Core verdicts")
check(len(verdicts) == len(expected_order) and {r.get("Test") for r in verdicts} == set(expected_order), "missing, duplicate or extra invoice checkpoint")
check(all(r.get("Action") == "pass" for r in verdicts), "Core invoice parent/group/checkpoints must PASS; no SKIP")
child_events = report.get("child_events", [])
check(isinstance(child_events, list), "invalid Core child events")
actual_order = [r.get("Test") for r in child_events if isinstance(r, dict) and r.get("Action") == "run" and r.get("Test")]
check(actual_order == expected_order, "Core child ran extra cases or changed reviewed checkpoint order")
print("[core-invoice-coverage] PASSED: current timestamp-matched report; outer/native parent, invoice group and all17 checkpoints PASS (19 native verdicts); no extra cases or SKIP")
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
