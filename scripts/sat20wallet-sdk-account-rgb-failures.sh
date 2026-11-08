#!/bin/bash
set -uo pipefail

GO_BIN="/usr/local/go/bin/go"
WORKDIR="/Users/yingfeng/github/sat20wallet/sdk"
WORKSPACE="/Users/yingfeng/github"
LOG_ROOT="/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs/sat20wallet-sdk-account-rgb-failures"
SUITE="sat20wallet-sdk-account-rgb-failures"
TIMEOUT_MS="1800000"
WALLET_RUN='^(TestModuleCapacityKeepsRGBProof|TestWaitAccountManagedDataReadyCancelsAndConverges|TestDKVSReviewAccountReadyCancellationPreservesReceivingWorker|TestRGB11AddressNACKCancelsBeforeBroadcast)$'
RGB11_RUN='^TestSelectedRecoveryOnlyReleasesTerminalReservationOwner$'

if [[ "$#" -ne 0 ]]; then
  printf '%s\n' "This fixed five-case SDK runner accepts no arguments." >&2
  exit 2
fi
for executable in "$GO_BIN" /usr/bin/python3; do
  if [[ ! -x "$executable" ]]; then
    printf 'Required executable not found: %s\n' "$executable" >&2
    exit 127
  fi
done
for path in "$WORKDIR/wallet" "$WORKDIR/wallet/rgb11" "$WORKSPACE/satoshinet" "$WORKSPACE/indexer" "$WORKSPACE/rgb11"; do
  if [[ ! -d "$path" ]]; then
    printf 'Missing SDK package or dependency: %s\n' "$path" >&2
    exit 2
  fi
done
if ! /bin/mkdir -p "$LOG_ROOT"; then
  printf 'Cannot create log directory: %s\n' "$LOG_ROOT" >&2
  exit 125
fi

started_epoch="$(/bin/date '+%s')"
started_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
stamp="$(/bin/date -u '+%Y%m%dT%H%M%SZ')"
run_id="${stamp}-$$"
log_file="$LOG_ROOT/$run_id.log"
status_file="$LOG_ROOT/$run_id.status"
summary_file="$LOG_ROOT/$run_id.coverage.json"
wallet_state="PENDING"
wallet_go_exit=""
wallet_log_exit=""
rgb11_state="PENDING"
rgb11_go_exit=""
rgb11_log_exit=""
coverage_exit=""
ended_at_utc=""
duration_seconds=""
wallet_cmd=(/usr/bin/env -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS "$GO_BIN" test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=10m -run "$WALLET_RUN" ./wallet)
rgb11_cmd=(/usr/bin/env -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS "$GO_BIN" test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=10m -run "$RGB11_RUN" ./wallet/rgb11)
printf -v wallet_command '%q ' "${wallet_cmd[@]}"
printf -v rgb11_command '%q ' "${rgb11_cmd[@]}"

write_status() {
  local state="$1"
  local exit_code="$2"
  {
    printf 'state=%s\n' "$state"
    printf 'suite=%s\n' "$SUITE"
    printf 'scope=fixed five failed roots only; serial packages; not group or full acceptance\n'
    printf 'run_id=%s\n' "$run_id"
    printf 'pid=%s\n' "$$"
    printf 'working_directory=%s\n' "$WORKDIR"
    printf 'timeout_ms=%s\n' "$TIMEOUT_MS"
    printf 'package_timeout=10m\n'
    printf 'started_at_utc=%s\n' "$started_at_utc"
    printf 'started_epoch=%s\n' "$started_epoch"
    printf 'wallet_state=%s\n' "$wallet_state"
    printf 'wallet_go_exit_code=%s\n' "$wallet_go_exit"
    printf 'wallet_log_exit_code=%s\n' "$wallet_log_exit"
    printf 'wallet_command=%s\n' "$wallet_command"
    printf 'rgb11_state=%s\n' "$rgb11_state"
    printf 'rgb11_go_exit_code=%s\n' "$rgb11_go_exit"
    printf 'rgb11_log_exit_code=%s\n' "$rgb11_log_exit"
    printf 'rgb11_command=%s\n' "$rgb11_command"
    printf 'coverage_exit_code=%s\n' "$coverage_exit"
    printf 'ended_at_utc=%s\n' "$ended_at_utc"
    printf 'duration_seconds=%s\n' "$duration_seconds"
    printf 'exit_code=%s\n' "$exit_code"
    printf 'log_file=%s\n' "$log_file"
    printf 'summary_file=%s\n' "$summary_file"
    printf 'status_file=%s\n' "$status_file"
  } > "$status_file.tmp" || return 1
  /bin/mv -f "$status_file.tmp" "$status_file"
}

if ! : > "$log_file" || ! write_status "RUNNING" ""; then
  printf 'Cannot initialize run log/status: %s\n' "$run_id" >&2
  exit 125
fi
printf 'run_id=%s\nlog_file=%s\nstatus_file=%s\nsummary_file=%s\n' "$run_id" "$log_file" "$status_file" "$summary_file" | /usr/bin/tee -a "$log_file"
if (( $? != 0 )); then exit 125; fi

# Fixed serial stages. Collect both packages even if one selected case fails.
for stage in wallet rgb11; do
  if [[ "$stage" == wallet ]]; then
    wallet_state="RUNNING"
    command_text="$wallet_command"
    cmd=("${wallet_cmd[@]}")
  else
    rgb11_state="RUNNING"
    command_text="$rgb11_command"
    cmd=("${rgb11_cmd[@]}")
  fi
  if ! write_status "RUNNING" ""; then exit 125; fi
  printf '[stage] name=%s state=RUNNING\n+ %s\n' "$stage" "$command_text" | /usr/bin/tee -a "$log_file"
  if (( $? != 0 )); then exit 125; fi
  (
    cd "$WORKDIR" || exit 125
    "${cmd[@]}"
  ) 2>&1 | /usr/bin/tee -a "$log_file"
  stage_codes=("${PIPESTATUS[@]}")
  stage_state="FAILED"
  if (( stage_codes[0] == 0 && stage_codes[1] == 0 )); then stage_state="PASSED"; fi
  if [[ "$stage" == wallet ]]; then
    wallet_state="$stage_state"
    wallet_go_exit="${stage_codes[0]}"
    wallet_log_exit="${stage_codes[1]}"
  else
    rgb11_state="$stage_state"
    rgb11_go_exit="${stage_codes[0]}"
    rgb11_log_exit="${stage_codes[1]}"
  fi
  if ! write_status "RUNNING" ""; then exit 125; fi
done

/usr/bin/python3 - "$log_file" "$summary_file" <<'PY_COVERAGE' 2>&1 | /usr/bin/tee -a "$log_file"
import collections
import datetime
import decimal
import json
import re
import sys


def instant(value):
    stamp = re.fullmatch(
        r"([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})"
        r"(?:\.([0-9]{1,9}))?(Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])", value)
    if stamp is None:
        raise ValueError("invalid RFC3339 timestamp")
    whole = datetime.datetime.fromisoformat(stamp[1] + stamp[3].replace("Z", "+00:00"))
    elapsed = whole - datetime.datetime(1970, 1, 1, tzinfo=datetime.timezone.utc)
    return decimal.Decimal(elapsed.days * 86400 + elapsed.seconds) + decimal.Decimal("0." + (stamp[2] or "0"))


log_path, summary_path = sys.argv[1:]
wallet = "github.com/sat20-labs/sat20wallet/sdk/wallet"
rgb11 = wallet + "/rgb11"
expected = {
    wallet: {
        "TestModuleCapacityKeepsRGBProof",
        "TestWaitAccountManagedDataReadyCancelsAndConverges",
        "TestDKVSReviewAccountReadyCancellationPreservesReceivingWorker",
        "TestRGB11AddressNACKCancelsBeforeBroadcast",
    },
    rgb11: {"TestSelectedRecoveryOnlyReleasesTerminalReservationOwner"},
}
events = collections.defaultdict(lambda: collections.defaultdict(list))
issues = []
try:
    with open(log_path) as stream:
        for line in stream:
            try:
                event = json.loads(line)
            except ValueError:
                continue
            package, name, action = event.get("Package"), event.get("Test", ""), event.get("Action")
            if package not in expected:
                issues.append("unexpected package event")
                continue
            if name and name.split("/")[0] not in expected[package]:
                issues.append("unexpected test: " + name)
            if action in {"skip", "fail"}:
                issues.append(action + ": " + (name or package))
            if action in {"start", "run", "pass", "fail", "skip"}:
                events[(package, name)][action].append(instant(event["Time"]))
except (OSError, TypeError, ValueError, KeyError) as exc:
    issues.append("invalid or unreadable test stream: " + str(exc))

rows = []
for package, names in expected.items():
    package_events = events[(package, "")]
    if len(package_events["start"]) != 1 or len(package_events["pass"]) != 1:
        issues.append("missing or duplicate package start/PASS: " + package)
    elif package_events["pass"][0] < package_events["start"][0]:
        issues.append("invalid package time order: " + package)
    for name in sorted(names):
        case = events[(package, name)]
        passed = len(case["run"]) == 1 and len(case["pass"]) == 1
        if not passed:
            issues.append("missing or duplicate root run/PASS: " + name)
        rows.append({"package": package, "test": name, "passed": passed})

for (package, name), case in list(events.items()):
    if not name:
        continue
    if len(case["run"]) != 1 or len(case["pass"]) != 1:
        issues.append("test did not run and PASS exactly once: " + name)
        continue
    package_events = events[(package, "")]
    if len(package_events["start"]) == 1 and len(package_events["pass"]) == 1:
        if not package_events["start"][0] <= case["run"][0] <= case["pass"][0] <= package_events["pass"][0]:
            issues.append("invalid test time order: " + name)
if len(events[(wallet, "")]["pass"]) == 1 and len(events[(rgb11, "")]["start"]) == 1:
    if events[(wallet, "")]["pass"][0] > events[(rgb11, "")]["start"][0]:
        issues.append("package stages did not run serially")

summary = {"scope": "fixed five failed roots only; not group or full acceptance", "passed": not issues,
           "expected_roots": 5, "passed_roots": sum(row["passed"] for row in rows),
           "roots": rows, "issues": sorted(set(issues)), "log": log_path}
try:
    with open(summary_path, "w") as stream:
        json.dump(summary, stream, indent=2)
        stream.write("\n")
except OSError as exc:
    print("[five-case-coverage] FAILED: cannot persist summary: " + str(exc))
    raise SystemExit(1)
print("[five-case-coverage] " + ("PASSED: all 5 roots and every nested case PASS; no SKIP" if not issues else "FAILED: " + "; ".join(summary["issues"])))
raise SystemExit(0 if not issues else 1)
PY_COVERAGE
coverage_codes=("${PIPESTATUS[@]}")
coverage_exit="${coverage_codes[0]}"
final_state="FAILED"
final_exit="1"
if [[ "$wallet_state" == PASSED && "$rgb11_state" == PASSED ]] && (( coverage_exit == 0 && coverage_codes[1] == 0 )); then
  final_state="PASSED"
  final_exit="0"
fi
ended_epoch="$(/bin/date '+%s')"
ended_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
duration_seconds=$(( ended_epoch - started_epoch ))
if ! write_status "$final_state" "$final_exit"; then exit 125; fi
printf '[test-run] state=%s exit_code=%s run_id=%s status_file=%s summary_file=%s\n' "$final_state" "$final_exit" "$run_id" "$status_file" "$summary_file" | /usr/bin/tee -a "$log_file"
if (( $? != 0 )); then exit 125; fi
exit "$final_exit"
