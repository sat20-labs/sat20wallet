#!/bin/bash
set -uo pipefail
umask 077

GO_BIN="/usr/local/go/bin/go"
WORKDIR="/Users/yingfeng/github/sat20wallet/sdk"
WORKSPACE="/Users/yingfeng/github"
SUITE="sat20wallet-sdk-final-failures"
LOG_ROOT="/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs/$SUITE"
TIMEOUT_MS="9000000"
NATIVE_RUN='^(TestDKVSReviewFundingPausedOutboxStillReceives|TestDKVSThreeFixOfflineRecoveryBlocksLocalEdits|TestRGB11RejectedCompactionSurvivesOlderACKAndRestart|TestRGB11AddressBroadcastUsesDurableIntent|TestRGB11DirectLifecycleAuthorityIsSDKOwned)$'
PLUGIN_RUN='^(TestRealSatoshiNetAgentPredictionTenBettors|TestRealSatoshiNetAgentPredictionPayoutByShare)$'
CORE_OUTER_RUN='^TestSDKCoreModulesE2E$'
CORE_NATIVE_RUN='^TestSDKCoreModulesConnectedE2E$/^(invoice_transfer_every_paid_recovery_boundary)$'

if [[ "$#" -ne 0 ]]; then
  printf '%s\n' "This fixed failure-only SDK runner accepts no arguments." >&2
  exit 2
fi
for executable in "$GO_BIN" /usr/bin/python3; do
  if [[ ! -x "$executable" ]]; then
    printf 'Required executable not found: %s\n' "$executable" >&2
    exit 127
  fi
done
for dependency in sat20wallet/sdk/wallet sat20wallet/sdk/e2e satoshinet indexer rgb11 transcend; do
  if [[ ! -d "$WORKSPACE/$dependency" ]]; then
    printf 'Missing SDK package or dependency: %s\n' "$dependency" >&2
    exit 2
  fi
done
/bin/mkdir -p "$LOG_ROOT" || exit 125
started_epoch="$(/bin/date '+%s')"
started_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
run_id="$(/bin/date -u '+%Y%m%dT%H%M%SZ')-$$"
log_file="$LOG_ROOT/$run_id.log"
status_file="$LOG_ROOT/$run_id.status"
report_file="$WORKDIR/review-evidence/core-modules-e2e-latest.json"
stages=(native plugin core)
states=(NOTRUN NOTRUN NOTRUN)
go_exits=("" "" "")
coverage_exits=("" "" "")
logs=("$LOG_ROOT/$run_id.native.log" "$LOG_ROOT/$run_id.plugin.log" "$LOG_ROOT/$run_id.core.log")
commands=("" "" "")
ended_at_utc=""
duration_seconds=""
safe_env=(/usr/bin/env -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS
  -u SAT20WALLET_LEGACY_INTEGRATION -u SAT20WALLET_CONTRACT_TESTNET
  -u SAT20_LIVE_CHANNEL_HEARTBEAT -u SAT20WALLET_CORE_E2E_CONFIG
  -u SAT20WALLET_CORE_E2E_CHILD_RUN)

write_status() {
  local state="$1" exit_code="$2" i
  {
    printf 'state=%s\nsuite=%s\nrun_id=%s\npid=%s\n' "$state" "$SUITE" "$run_id" "$$"
    printf 'scope=fixed native5 then Agent2 then complete Core invoice group; failure stops later stages; not full acceptance\n'
    printf 'working_directory=%s\ntimeout_ms=%s\nstarted_at_utc=%s\n' "$WORKDIR" "$TIMEOUT_MS" "$started_at_utc"
    for i in 0 1 2; do
      printf '%s_state=%s\n' "${stages[$i]}" "${states[$i]}"
      printf '%s_go_exit_code=%s\n' "${stages[$i]}" "${go_exits[$i]}"
      printf '%s_coverage_exit_code=%s\n' "${stages[$i]}" "${coverage_exits[$i]}"
      printf '%s_log_file=%s\n' "${stages[$i]}" "${logs[$i]}"
      printf '%s_command=%s\n' "${stages[$i]}" "${commands[$i]}"
    done
    printf 'ended_at_utc=%s\nduration_seconds=%s\nexit_code=%s\n' "$ended_at_utc" "$duration_seconds" "$exit_code"
    printf 'log_file=%s\nstatus_file=%s\n' "$log_file" "$status_file"
  } > "$status_file.tmp" || return 1
  /bin/mv -f "$status_file.tmp" "$status_file"
}

: > "$log_file" || exit 125
write_status RUNNING "" || exit 125
printf 'run_id=%s\nstatus_file=%s\nlog_file=%s\n' "$run_id" "$status_file" "$log_file" | /usr/bin/tee -a "$log_file"
if (( $? != 0 )); then exit 125; fi
final_exit=0
for i in 0 1 2; do
  stage="${stages[$i]}"
  case "$stage" in
    native) cmd=("${safe_env[@]}" "$GO_BIN" test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=10m -run "$NATIVE_RUN" ./wallet) ;;
    plugin) cmd=("${safe_env[@]}" "$GO_BIN" test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=20m -run "$PLUGIN_RUN" ./e2e) ;;
    core) cmd=("${safe_env[@]}" "SAT20WALLET_CORE_E2E_CHILD_RUN=$CORE_NATIVE_RUN" "$GO_BIN" test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=100m -run "$CORE_OUTER_RUN" ./e2e) ;;
  esac
  printf -v command_text '%q ' "${cmd[@]}"
  commands[$i]="$command_text"
  states[$i]="RUNNING"
  write_status RUNNING "" || exit 125
  printf '[stage] name=%s state=RUNNING log_file=%s\n+ %s\n' "$stage" "${logs[$i]}" "${commands[$i]}" | /usr/bin/tee -a "$log_file"
  if (( $? != 0 )); then exit 125; fi
  (cd "$WORKDIR" && exec "${cmd[@]}") > "${logs[$i]}" 2>&1
  go_exits[$i]=$?
  /usr/bin/python3 - "$stage" "${logs[$i]}" "$report_file" "$CORE_NATIVE_RUN" <<'PY_COVERAGE' 2>&1 | /usr/bin/tee -a "$log_file"
import collections
import datetime
import decimal
import json
import re
import sys


def check(condition, message):
    if not condition:
        print("[failure-coverage] FAILED: " + message)
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


stage, log_path, report_path, selection = sys.argv[1:]
expected = {
    "native": {"TestDKVSReviewFundingPausedOutboxStillReceives", "TestDKVSThreeFixOfflineRecoveryBlocksLocalEdits",
               "TestRGB11RejectedCompactionSurvivesOlderACKAndRestart", "TestRGB11AddressBroadcastUsesDurableIntent",
               "TestRGB11DirectLifecycleAuthorityIsSDKOwned"},
    "plugin": {"TestRealSatoshiNetAgentPredictionTenBettors", "TestRealSatoshiNetAgentPredictionPayoutByShare"},
    "core": {"TestSDKCoreModulesE2E"},
}[stage]
package = "github.com/sat20-labs/sat20wallet/sdk/" + ("wallet" if stage == "native" else "e2e")
events = collections.defaultdict(lambda: collections.defaultdict(list))
with open(log_path) as stream:
    for line in stream:
        try:
            event = json.loads(line)
        except ValueError:
            continue
        check(event.get("Package") == package, "unexpected package in " + stage)
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

if stage == "core":
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
print("[failure-coverage] PASSED: " + stage + " exact roots=" + str(len(expected)) + "; all nested cases PASS; no SKIP; current Core report checked when applicable")
PY_COVERAGE
  coverage_codes=("${PIPESTATUS[@]}")
  coverage_exits[$i]="${coverage_codes[0]}"
  states[$i]="FAILED"
  if (( go_exits[$i] == 0 && coverage_codes[0] == 0 && coverage_codes[1] == 0 )); then states[$i]="PASSED"; fi
  write_status RUNNING "" || exit 125
  printf '[stage] name=%s state=%s go_exit_code=%s coverage_exit_code=%s\n' "$stage" "${states[$i]}" "${go_exits[$i]}" "${coverage_exits[$i]}" | /usr/bin/tee -a "$log_file"
  if (( $? != 0 )); then exit 125; fi
  if [[ "${states[$i]}" != PASSED ]]; then final_exit=1; break; fi
done
ended_epoch="$(/bin/date '+%s')"
ended_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
duration_seconds=$(( ended_epoch - started_epoch ))
final_state="FAILED"
if (( final_exit == 0 )); then final_state="PASSED"; fi
write_status "$final_state" "$final_exit" || exit 125
printf '[test-run] state=%s exit_code=%s run_id=%s status_file=%s\n' "$final_state" "$final_exit" "$run_id" "$status_file" | /usr/bin/tee -a "$log_file"
if (( $? != 0 )); then exit 125; fi
exit "$final_exit"
