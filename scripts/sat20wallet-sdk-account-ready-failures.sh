#!/bin/bash
set -uo pipefail

GO_BIN="/usr/local/go/bin/go"
WORKDIR="/Users/yingfeng/github/sat20wallet/sdk"
WORKSPACE="/Users/yingfeng/github"
LOG_ROOT="/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs/sat20wallet-sdk-account-ready-failures"
SUITE="sat20wallet-sdk-account-ready-failures"
TIMEOUT_MS="1200000"
GROUP_NAMES=(account_setup)
GROUP_PACKAGES=(./wallet)
GROUP_COUNTS=(3)
GROUP_RUNS=(
  '^(TestDKVSReviewAccountReadinessCheckUsesOnlyConfirmedReplica|TestDKVSReviewAccountReadyWaitCancelsDuringInitialSync|TestDKVSReadyNeedsWallet)$'
)

if [[ "$#" -ne 0 ]]; then
  printf '%s\n' "This fixed three-case SDK preparation runner accepts no arguments." >&2
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
if ! /bin/mkdir -p "$LOG_ROOT"; then exit 125; fi
started_epoch="$(/bin/date '+%s')"
started_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
stamp="$(/bin/date -u '+%Y%m%dT%H%M%SZ')"
run_id="${stamp}-$$"
log_file="$LOG_ROOT/$run_id.log"
status_file="$LOG_ROOT/$run_id.status"
group_states=(PENDING)
group_go_exits=("")
group_gate_exits=("")
group_logs=("$LOG_ROOT/$run_id.account_setup.jsonl")
group_reports=("$LOG_ROOT/$run_id.account_setup.coverage.json")
ended_at_utc=""
duration_seconds=""

write_status() {
  local state="$1"
  local exit_code="$2"
  local i
  {
    printf 'state=%s\nsuite=%s\nrun_id=%s\npid=%s\n' "$state" "$SUITE" "$run_id" "$$"
    printf 'scope=fixed three failed preparation roots; not full account_sync group acceptance\n'
    printf 'group_failure_policy=complete current group, stop before next group on any failure\n'
    printf 'working_directory=%s\ntimeout_ms=%s\npackage_timeout=10m\n' "$WORKDIR" "$TIMEOUT_MS"
    printf 'started_at_utc=%s\nstarted_epoch=%s\n' "$started_at_utc" "$started_epoch"
    for i in 0; do
      printf '%s_state=%s\n' "${GROUP_NAMES[i]}" "${group_states[i]}"
      printf '%s_expected_roots=%s\n' "${GROUP_NAMES[i]}" "${GROUP_COUNTS[i]}"
      printf '%s_selector=%s\n' "${GROUP_NAMES[i]}" "${GROUP_RUNS[i]}"
      printf '%s_go_exit_code=%s\n' "${GROUP_NAMES[i]}" "${group_go_exits[i]}"
      printf '%s_gate_exit_code=%s\n' "${GROUP_NAMES[i]}" "${group_gate_exits[i]}"
      printf '%s_log=%s\n' "${GROUP_NAMES[i]}" "${group_logs[i]}"
      printf '%s_coverage=%s\n' "${GROUP_NAMES[i]}" "${group_reports[i]}"
    done
    printf 'ended_at_utc=%s\nduration_seconds=%s\nexit_code=%s\n' "$ended_at_utc" "$duration_seconds" "$exit_code"
    printf 'log_file=%s\nstatus_file=%s\n' "$log_file" "$status_file"
  } > "$status_file.tmp" || return 1
  /bin/mv -f "$status_file.tmp" "$status_file"
}

if ! : > "$log_file" || ! write_status "RUNNING" ""; then exit 125; fi
printf 'run_id=%s\nlog_file=%s\nstatus_file=%s\n' "$run_id" "$log_file" "$status_file" | /usr/bin/tee -a "$log_file"
if (( $? != 0 )); then exit 125; fi

# Run every root inside a group; advance only after that entire group passes.
for i in 0; do
  group_states[i]="RUNNING"
  if ! write_status "RUNNING" ""; then exit 125; fi
  cmd=(/usr/bin/env -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS "$GO_BIN" test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=10m -run "${GROUP_RUNS[i]}" "${GROUP_PACKAGES[i]}")
  printf -v command_text '%q ' "${cmd[@]}"
  printf '[stage] group=%s expected_roots=%s state=RUNNING\n+ %s\n' "${GROUP_NAMES[i]}" "${GROUP_COUNTS[i]}" "$command_text" | /usr/bin/tee -a "$log_file"
  if (( $? != 0 )); then exit 125; fi
  (
    cd "$WORKDIR" || exit 125
    "${cmd[@]}"
  ) 2>&1 | /usr/bin/tee "${group_logs[i]}" | /usr/bin/tee -a "$log_file"
  go_codes=("${PIPESTATUS[@]}")
  group_go_exits[i]="${go_codes[0]}"

  /usr/bin/python3 - "${GROUP_NAMES[i]}" "${group_logs[i]}" "${group_reports[i]}" <<'PY_COVERAGE' 2>&1 | /usr/bin/tee -a "$log_file"
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


fixed = {'account_setup': ('github.com/sat20-labs/sat20wallet/sdk/wallet',
                   ['TestDKVSReviewAccountReadinessCheckUsesOnlyConfirmedReplica',
                    'TestDKVSReviewAccountReadyWaitCancelsDuringInitialSync',
                    'TestDKVSReadyNeedsWallet'])}
group, log_path, report_path = sys.argv[1:]
if group not in fixed:
    raise SystemExit("unexpected fixed group")
package, roots = fixed[group]
expected = set(roots)
events = collections.defaultdict(lambda: collections.defaultdict(list))
issues = []
try:
    with open(log_path) as stream:
        for line in stream:
            try:
                event = json.loads(line)
            except ValueError:
                continue
            name, action = event.get("Test", ""), event.get("Action")
            if event.get("Package") != package:
                issues.append("unexpected package event")
                continue
            if name and name.split("/")[0] not in expected:
                issues.append("unexpected test: " + name)
            if action in {"skip", "fail"}:
                issues.append(action + ": " + (name or package))
            if action in {"start", "run", "pass", "fail", "skip"}:
                events[name][action].append(instant(event["Time"]))
except (OSError, TypeError, ValueError, KeyError) as exc:
    issues.append("invalid or unreadable test stream: " + str(exc))

package_events = events[""]
if len(package_events["start"]) != 1 or len(package_events["pass"]) != 1:
    issues.append("package did not start and PASS exactly once")
elif package_events["pass"][0] < package_events["start"][0]:
    issues.append("invalid package time order")
rows = []
for name in roots:
    case = events[name]
    passed = len(case["run"]) == 1 and len(case["pass"]) == 1
    if not passed:
        issues.append("missing or duplicate root run/PASS: " + name)
    rows.append({"test": name, "passed": passed})
for name, case in list(events.items()):
    if not name:
        continue
    if len(case["run"]) != 1 or len(case["pass"]) != 1:
        issues.append("test did not run and PASS exactly once: " + name)
        continue
    if len(package_events["start"]) == 1 and len(package_events["pass"]) == 1:
        if not package_events["start"][0] <= case["run"][0] <= case["pass"][0] <= package_events["pass"][0]:
            issues.append("invalid test time order: " + name)
report = {"group": group, "scope": "fixed three failed preparation roots; not full account_sync group acceptance", "package": package,
          "expected_roots": len(expected), "passed_roots": sum(row["passed"] for row in rows),
          "passed": not issues, "roots": rows, "issues": sorted(set(issues)), "log": log_path}
try:
    with open(report_path, "w") as stream:
        json.dump(report, stream, indent=2)
        stream.write("\n")
except OSError as exc:
    print("[group-coverage] FAILED: cannot persist report: " + str(exc))
    raise SystemExit(1)
print("[group-coverage] " + group + (" PASSED: every fixed root/nested case PASS; no SKIP" if not issues else " FAILED: " + "; ".join(report["issues"])))
raise SystemExit(0 if not issues else 1)
PY_COVERAGE
  gate_codes=("${PIPESTATUS[@]}")
  group_gate_exits[i]="${gate_codes[0]}"
  if (( go_codes[0] == 0 && go_codes[1] == 0 && go_codes[2] == 0 && gate_codes[0] == 0 && gate_codes[1] == 0 )); then
    group_states[i]="PASSED"
  else
    group_states[i]="FAILED"
    break
  fi
  if ! write_status "RUNNING" ""; then exit 125; fi
done

final_state="PASSED"
final_exit="0"
for i in 0; do
  if [[ "${group_states[i]}" == PENDING ]]; then group_states[i]="NOTRUN"; fi
  if [[ "${group_states[i]}" != PASSED ]]; then final_state="FAILED"; final_exit="1"; fi
done
ended_epoch="$(/bin/date '+%s')"
ended_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
duration_seconds=$(( ended_epoch - started_epoch ))
if ! write_status "$final_state" "$final_exit"; then exit 125; fi
printf '[test-run] state=%s exit_code=%s run_id=%s status_file=%s\n' "$final_state" "$final_exit" "$run_id" "$status_file" | /usr/bin/tee -a "$log_file"
if (( $? != 0 )); then exit 125; fi
exit "$final_exit"
