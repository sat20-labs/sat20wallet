#!/bin/bash
set -uo pipefail
umask 077

GO_BIN="/usr/local/go/bin/go"
WORKDIR="/Users/yingfeng/github/sat20wallet/sdk"
WORKSPACE="/Users/yingfeng/github"
LOG_ROOT="/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs/sat20wallet-sdk-native"
SUITE="sat20wallet-sdk-native"
TIMEOUT_MS="10800000"
# Default execution is limited to reviewed public-interface and E2E roots.
# Internal module tests remain separately runnable; see sdk/docs/testing-scope.md.
FUNCTIONAL_MANIFEST="$WORKSPACE/sat20wallet/scripts/sdk-functional-roots.tsv"
# Browser roots run in staged plan Stage 3; focused wrappers use full registries.
# Persistent real-regtest automation remains NOTRUN, not PASS.
# Direct Core harness wrappers are excluded here; the parent runs 16 native groups.
BROWSER_SKIP='^(TestSDKAccountPWAConnectedBrowser|TestPWAAccountRecovery|TestPWAAccountSwitch|TestPWAAccountRemoteDelete|TestPWAPasswordTxRollback|TestPWATabAccountSwitch|TestPWATabWalletSwitch|TestPWASingleManager|TestPWATabPasswordHandoff|TestPWAGuardianPaidQuote|TestPWAGuardianRecovery|TestPWABackupStatus|TestPWAStatusReadRetry|TestSDKWalletPWAConnectedBrowser|TestSDKWalletPWAAssetContextStability|TestSDKWASMInterfacesE2E|TestSDKWalletPWAForceCloseCSV|TestSDKAccountGuardianPaidPWABatchReview|TestPWAProductionReleaseE2E|TestPWARegtestReleaseE2E|TestSPSilentPayBrowserE2E|TestPWACancellationReapsOwnedChromium|TestPWACancellationForceFallbackReapsOwnedChromium|TestPWAOwnedLateRegistration|TestPWAOwnedUnregisteredCancellation)$'
REAL_REGTEST_SKIP='^(TestSDKPWARealRegtest.*|TestSDKPWARegtestExistingRecoveryMaterialReadback)$'
CORE_NATIVE_RUN='^TestSDKCoreModulesConnectedE2E$/^(dkvs_real_paid_cross_instance_prefix_and_CAS|backup_multiple_wallets_and_subaccounts_control|RGB11_self_send_cold_restore_and_respend|RGB11_asset_name_and_transcend_registration_descriptor|RGB_issuance_in_multiple_subaccounts|invoice_transfer_every_paid_recovery_boundary|direct_transfer_every_paid_recovery_boundary|RGB11_same_root_three_device_cas_cancel_broadcast_and_respend|RGB11_recovery_identity_batch_and_hop_lifecycles|RGB11_fullsend_history_cold_recovery_reorg_and_competing_intent|RGB11_R11_stable_only_cold_restore_ignores_valid_and_corrupt_legacy_cache|RGB11_R23_temporary_TTL_expiry_cold_restore_and_respend|RGB11_R09_same_root_real_CAS_loser_retains_private_intent_and_tests_recovery|RGB11_recovery_capacity_boundaries|RGB11_account_recovery_failure_boundaries|guardian_two_of_three_and_wrong_knowledge)$'
SKIP_PATTERN="${BROWSER_SKIP}|${REAL_REGTEST_SKIP}|^TestSDKCoreModulesConnectedE2E$|^TestSDKCoreModulesValidation$"

if [[ "$#" -ne 0 ]]; then
  printf '%s\n' "This fixed SDK native runner accepts no arguments." >&2
  exit 2
fi
if [[ ! -x /usr/bin/python3 ]]; then
  printf '%s\n' "Python 3 is required to verify the existing Core child summary." >&2
  exit 127
fi
if [[ ! -x "$GO_BIN" ]]; then
  printf '%s\n' "Go executable not found: $GO_BIN" >&2
  exit 127
fi
for dependency in satoshinet indexer rgb11 transcend; do
  if [[ ! -d "$WORKSPACE/$dependency" ]]; then
    printf '%s\n' "missing dependency required by SDK E2E: $WORKSPACE/$dependency" >&2
    exit 2
  fi
done
selection="$(/usr/bin/python3 - "$WORKDIR" "$FUNCTIONAL_MANIFEST" <<'PY_SELECTION'
import pathlib
import re
import sys

workdir, manifest = map(pathlib.Path, sys.argv[1:])
roots = {"wallet": [], "e2e": []}
for raw in manifest.read_text().splitlines():
    if not raw or raw.startswith("#"):
        continue
    package, name, kind, source = raw.split("\t")
    if package not in roots or kind not in {"interface", "e2e"} or not re.fullmatch(r"Test[A-Za-z0-9_]+", name):
        raise SystemExit("invalid functional manifest entry")
    if name in roots[package] or source.split("/", 1)[0] != package:
        raise SystemExit("duplicate root or mismatched source in functional manifest")
    if not re.search(r"^func " + name + r"\(t \*testing.T\)", (workdir / source).read_text(), re.M):
        raise SystemExit("functional root missing from source: " + name)
    roots[package].append(name)
for package in ("wallet", "e2e"):
    if not roots[package]:
        raise SystemExit("empty functional selection: " + package)
    print("^(" + "|".join(roots[package]) + ")$")
PY_SELECTION
)" || exit 2
NATIVE_RUN="${selection%%$'\n'*}"
E2E_RUN="${selection#*$'\n'}"
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
native_log="$LOG_ROOT/$run_id.native.log"
e2e_log="$LOG_ROOT/$run_id.e2e.log"
MODULE="github.com/sat20-labs/sat20wallet/sdk"
packages=("$MODULE" "$MODULE/account" "$MODULE/account/fuzzy" "$MODULE/account/internal/shamir"
  "$MODULE/common" "$MODULE/config" "$MODULE/e2e" "$MODULE/e2e/cmd/solidity-compile"
  "$MODULE/plugin" "$MODULE/testutil/fakeindexer" "$MODULE/wallet" "$MODULE/wallet/dkvs"
  "$MODULE/wallet/lightnode" "$MODULE/wallet/rgb11" "$MODULE/wallet/supernode"
  "$MODULE/wallet/utils" "$MODULE/wasm" "$MODULE/wire")
native_packages=("$MODULE/wallet")
# Wallet globals are process-local. Its normal HTTP fixtures use ephemeral ports.
# Disable all live/legacy entry gates, including the fixture that removes ../db.
# The native lane cannot inherit a Core configuration or launch either wrapper.
safe_env=(/usr/bin/env -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS
  -u SAT20WALLET_LEGACY_INTEGRATION -u SAT20WALLET_CONTRACT_TESTNET
  -u SAT20_LIVE_CHANNEL_HEARTBEAT -u SAT20WALLET_CORE_E2E_CONFIG
  -u SAT20WALLET_CORE_E2E_CHILD_RUN)
test_args=("$GO_BIN" test -mod=readonly -json -p=1 -parallel=1 -count=1
  -timeout=150m -skip "$SKIP_PATTERN")
native_cmd=("${safe_env[@]}" "${test_args[@]}" -run "$NATIVE_RUN" "${native_packages[@]}")
e2e_cmd=("${safe_env[@]}" "SAT20WALLET_CORE_E2E_CHILD_RUN=$CORE_NATIVE_RUN"
  "${test_args[@]}" -run "$E2E_RUN" "$MODULE/e2e")
printf -v command_args '%q ' "${native_cmd[@]}"
native_command="+ ${command_args% }"
printf -v command_args '%q ' "${e2e_cmd[@]}"
e2e_command="+ ${command_args% }"
command_text="two independent Go processes; selected wallet interfaces and E2E; 18-package source inventory and production build; internal tests NOTRUN"
native_pid=""
e2e_pid=""
native_state="PENDING"
e2e_state="PENDING"
native_exit=""
e2e_exit=""
package_coverage_exit=""
core_coverage_exit=""
build_exit=""

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
    printf 'run_id=%s\n' "$run_id"
    printf 'working_directory=%s\n' "$WORKDIR"
    printf 'timeout_ms=%s\n' "$TIMEOUT_MS"
    printf 'started_at_utc=%s\n' "$started_at_utc"
    printf 'started_at_local=%s\n' "$started_at_local"
    printf 'started_epoch=%s\n' "$started_epoch"
    printf 'ended_at_utc=%s\n' "$ended_at_utc"
    printf 'ended_at_local=%s\n' "$ended_at_local"
    printf 'ended_epoch=%s\n' "$ended_epoch_value"
    printf 'duration_seconds=%s\n' "$duration"
    printf 'exit_code=%s\n' "$exit_code"
    printf 'command=%s\n' "$command_text"
    printf 'native_command=%s\n' "$native_command"
    printf 'e2e_command=%s\n' "$e2e_command"
    printf 'native_pid=%s\n' "$native_pid"
    printf 'e2e_pid=%s\n' "$e2e_pid"
    printf 'native_state=%s\n' "$native_state"
    printf 'e2e_state=%s\n' "$e2e_state"
    printf 'native_exit_code=%s\n' "$native_exit"
    printf 'e2e_exit_code=%s\n' "$e2e_exit"
    printf 'package_coverage_exit_code=%s\n' "$package_coverage_exit"
    printf 'core_coverage_exit_code=%s\n' "$core_coverage_exit"
    printf 'production_build_exit_code=%s\nfunctional_manifest=%s\n' "$build_exit" "$FUNCTIONAL_MANIFEST"
    printf 'native_log_file=%s\n' "$native_log"
    printf 'e2e_log_file=%s\n' "$e2e_log"
    printf 'log_file=%s\n' "$log_file"
    printf 'status_file=%s\n' "$status_file"
  } > "$status_tmp" || return 1
  /bin/mv -f "$status_tmp" "$status_file"
}

if ! write_status "RUNNING" "" "" "" "" ""; then
  printf '%s\n' "cannot write run status: $status_file" >&2
  exit 125
fi

{
  printf 'run_id=%s\n' "$run_id"
  printf 'status_file=%s\n' "$status_file"
  printf 'log_file=%s\n' "$log_file"
  printf 'started_at_utc=%s\n' "$started_at_utc"
  printf '%s\n' "$command_text"
  printf 'native_log_file=%s\ne2e_log_file=%s\n' "$native_log" "$e2e_log"
  printf '%s\n%s\n' "$native_command" "$e2e_command"
} | /usr/bin/tee -a "$log_file"
header_exit=$?
if (( header_exit != 0 )); then
  ended_epoch="$(/bin/date '+%s')"
  ended_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
  ended_at_local="$(/bin/date '+%Y-%m-%dT%H:%M:%S%z')"
  duration_seconds=$(( ended_epoch - started_epoch ))
  write_status "FAILED" "125" "$ended_at_utc" "$ended_at_local" "$ended_epoch" "$duration_seconds"
  exit 125
fi

test_exit=0
status_error=0
# Discover once, then require the known package inventory before selecting tests.
# A new or missing package must be reviewed rather than silently omitted.
package_list="$(cd "$WORKDIR" && "${safe_env[@]}" "$GO_BIN" list -mod=readonly ./... 2>> "$log_file")"
list_exit=$?
expected_list="$(printf '%s\n' "${packages[@]}" | /usr/bin/sort)"
actual_list="$(printf '%s\n' "$package_list" | /usr/bin/sort)"
if (( list_exit == 0 )) && [[ "$actual_list" == "$expected_list" ]]; then
  (cd "$WORKDIR" && "${safe_env[@]}" "$GO_BIN" build -mod=readonly ./...) >> "$log_file" 2>&1
  build_exit=$?
fi
if (( list_exit != 0 )) || [[ "$actual_list" != "$expected_list" || "$build_exit" != 0 ]]; then
  printf '[package-inventory] FAILED: expected 18 source packages and a successful production build; go_list_exit=%s build_exit=%s\n' "$list_exit" "$build_exit" | /usr/bin/tee -a "$log_file"
  test_exit=125
else
  # Exactly two lanes; package tests within each lane remain serial (-p=1).
  # exec makes the recorded lane PID the Go process, not an extra shell wrapper.
  (cd "$WORKDIR" && exec "${native_cmd[@]}") > "$native_log" 2>&1 &
  native_pid=$!
  native_state="RUNNING"
  (cd "$WORKDIR" && exec "${e2e_cmd[@]}") > "$e2e_log" 2>&1 &
  e2e_pid=$!
  e2e_state="RUNNING"
  write_status "RUNNING" "" "" "" "" "" || status_error=1

  wait "$native_pid"
  native_exit=$?
  if (( native_exit == 0 )); then native_state="PASSED"; else native_state="FAILED"; fi
  write_status "RUNNING" "" "" "" "" "" || status_error=1
  wait "$e2e_pid"
  e2e_exit=$?
  if (( e2e_exit == 0 )); then e2e_state="PASSED"; else e2e_state="FAILED"; fi
  printf '[lane] name=native state=%s exit_code=%s log_file=%s\n[lane] name=e2e state=%s exit_code=%s log_file=%s\n' \
    "$native_state" "$native_exit" "$native_log" "$e2e_state" "$e2e_exit" "$e2e_log" | /usr/bin/tee -a "$log_file"
  if (( native_exit != 0 || e2e_exit != 0 )); then test_exit=1; fi

  /usr/bin/python3 - "$native_log" "$e2e_log" "$FUNCTIONAL_MANIFEST" "${packages[@]}" <<'PY_PACKAGES' | /usr/bin/tee -a "$log_file"
import collections
import json
import pathlib
import sys


def check(condition, message):
    if not condition:
        print("[functional-coverage] FAILED: " + message)
        raise SystemExit(1)


native_log, e2e_log, manifest, *inventory = sys.argv[1:]
module = "github.com/sat20-labs/sat20wallet/sdk"
selected = collections.defaultdict(set)
for raw in pathlib.Path(manifest).read_text().splitlines():
    if raw and not raw.startswith("#"):
        package, name, kind, source = raw.split("\t")
        selected[module + "/" + package].add(name)
check(len(inventory) == len(set(inventory)) == 18, "expected exactly 18 source packages")
check(set(selected) == {module + "/wallet", module + "/e2e"}, "unexpected selected package")
optional_l1 = {"TestSPBitcoinCoreRegtestIndexerE2E", "TestSPManagerBitcoinCoreSendReceiveE2E"}
root_results = collections.Counter()
skipped = []
failed = []
test_names = set()
test_verdicts = {}
for path, expected_package in [(native_log, module + "/wallet"), (e2e_log, module + "/e2e")]:
    runs = collections.Counter()
    roots = collections.defaultdict(list)
    packages = []
    missing_bitcoind = set()
    with open(path) as stream:
        for line in stream:
            try:
                event = json.loads(line)
            except ValueError:
                continue
            name, action = event.get("Test"), event.get("Action")
            check(event.get("Package") == expected_package, "unexpected package in lane")
            if name and action == "run":
                test_names.add((expected_package, name))
            if name and action in {"pass", "fail", "skip"}:
                test_verdicts[(expected_package, name)] = action
            if name and action == "run" and "/" not in name:
                runs[name] += 1
            if name and "/" not in name and "bitcoind unavailable" in event.get("Output", ""):
                missing_bitcoind.add(name)
            if name and action == "fail":
                failed.append(name)
            if name and action == "skip":
                skipped.append(name)
            if name and "/" not in name and action in {"pass", "fail", "skip"}:
                roots[name].append(action)
                root_results[action] += 1
            if not name and action in {"pass", "fail", "skip"}:
                packages.append(action)
    check(set(runs) == selected[expected_package] and all(n == 1 for n in runs.values()),
          "missing, duplicated or extra functional root in " + expected_package)
    check(set(roots) == selected[expected_package] and all(len(v) == 1 for v in roots.values()),
          "missing or duplicate root verdict in " + expected_package)
    for name, actions in roots.items():
        check(actions == ["pass"] or (
            actions == ["skip"] and name in optional_l1 and name in missing_bitcoind),
            "root did not pass: " + name + " verdicts=" + str(actions))
    check(packages == ["pass"], "selected package did not pass: " + expected_package)
print("[functional-coverage] roots=" + json.dumps(dict(root_results), sort_keys=True)
      + " selected=" + str(sum(map(len, selected.values()))) + " tested_packages=2 source_packages=18")
parents = {(package, name.rsplit("/", 1)[0]) for package, name in test_names if "/" in name}
leaves = collections.Counter(action for name, action in test_verdicts.items() if name not in parents)
print("[functional-coverage] leaves=" + json.dumps(dict(leaves), sort_keys=True))
print("[functional-coverage] SKIP=" + json.dumps(skipped))
print("[functional-coverage] NOTRUN=module internal roots outside the manifest; browser/persistent-regtest separate")
check(not failed, "failed test: " + ", ".join(failed))
print("[functional-coverage] PASSED: exact functional roots once; optional L1 SKIP is reported, never counted PASS")
PY_PACKAGES
  package_coverage_exit=$?
  if (( package_coverage_exit != 0 )); then test_exit=1; fi
fi

# A successful Go exit alone cannot prove that the selected Core groups ran.
# Reuse the parent harness's existing redacted report; never accept an old report.
if [[ "$e2e_exit" == "0" ]]; then
  /usr/bin/python3 - "$e2e_log" "$WORKDIR/review-evidence/core-modules-e2e-latest.json" "$CORE_NATIVE_RUN" <<'PY_CORE' | /usr/bin/tee -a "$log_file"
import datetime
import decimal
import json
import re
import sys


def check(condition, message):
    if not condition:
        print("[core-native-coverage] FAILED: " + message)
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
outer = {}
with open(log_path) as stream:
    for line in stream:
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if event.get("Package") == "github.com/sat20-labs/sat20wallet/sdk/e2e" and event.get("Test") == root:
            outer[event.get("Action")] = event.get("Time")
check("run" in outer and "pass" in outer, "Core parent did not run and pass")
with open(report_path) as stream:
    report = json.load(stream)
check(report.get("started_by") == root and report.get("child_run") == selection, "Core report selector differs")
check(report.get("passed") is True and report.get("child_exit_code") == 0, "Core child did not pass")
check(int(instant(outer["run"])) <= instant(report["finished_at"]) <= instant(outer["pass"]), "Core report is not from this run")
expected = set(selection.split("/^(", 1)[1][:-2].split("|"))
prefix = "TestSDKCoreModulesConnectedE2E/"
segments = report.get("segments", [])
ordered = selection.split("/^(", 1)[1][:-2].split("|")
plan = [("ordinary-recovery", ordered[:9] + ordered[10:]), ("long-history", ordered[9:10])]
if (len(segments) != 2 or len(ordered) != 16
        or ordered[9] != "RGB11_fullsend_history_cold_recovery_reorg_and_competing_intent"
        or report.get("shared_budget_seconds") != 5100 or report.get("budget_exhausted") is not False):
    check(False, "Core did not complete all 16 native groups in two environments within the shared 85-minute budget")
endpoints = []
for segment, (name, names) in zip(segments, plan):
    run = "^TestSDKCoreModulesConnectedE2E$/^(" + "|".join(names) + ")$"
    if (segment.get("name") != name or segment.get("child_run") != run
            or segment.get("passed") is not True or segment.get("child_exit_code") != 0):
        check(False, "Core segment selector or terminal status differs: " + name)
    endpoint = segment.get("node_rpc")
    if not isinstance(endpoint, str) or not endpoint:
        check(False, "Core segment has no actual node RPC endpoint: " + name)
    endpoints.append(endpoint)
    roots = [row for row in segment.get("verdicts", [])
             if row.get("Test") == "TestSDKCoreModulesConnectedE2E"]
    if len(roots) != 1 or roots[0].get("Action") != "pass":
        check(False, "Core segment child parent did not PASS exactly once: " + name)
    actual = [row.get("Test", "")[len(prefix):] for row in segment.get("verdicts", [])
              if row.get("Test", "").startswith(prefix) and "/" not in row["Test"][len(prefix):]]
    if len(actual) != len(names) or set(actual) != set(names):
        check(False, "Core segment has missing, duplicate or unexpected groups: " + name)
if len(set(endpoints)) != 2:
    check(False, "Core ordinary/recovery and long-history groups did not use two isolated node RPC endpoints")
for field in ("verdicts", "child_events"):
    combined = [row for segment in segments for row in segment.get(field, [])]
    if report.get(field) != combined:
        check(False, "Core aggregate differs from the actual segment " + field)

verdicts = report.get("verdicts", [])
groups = [row for row in verdicts if row.get("Test", "").startswith(prefix) and "/" not in row["Test"][len(prefix):]]
actual = {row["Test"][len(prefix):]: row["Action"] for row in groups}
check(len(expected) == 16 and len(groups) == 16 and set(actual) == expected, "Core native groups are missing, duplicated or unexpected")
check(all(action == "pass" for action in actual.values()), "a selected Core native group failed or skipped")
check(not any(row.get("Action") == "skip" and row.get("Test", "").startswith(prefix) for row in verdicts), "a selected Core child was skipped; SKIP is not PASS")
print("[core-native-coverage] PASSED: all 16 native groups executed; no child skips")
PY_CORE
  core_coverage_exit=$?
  if (( core_coverage_exit != 0 )); then
    test_exit=1
  fi
fi
if (( status_error != 0 )); then test_exit=125; fi

ended_epoch="$(/bin/date '+%s')"
ended_at_utc="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
ended_at_local="$(/bin/date '+%Y-%m-%dT%H:%M:%S%z')"
duration_seconds=$(( ended_epoch - started_epoch ))
if (( test_exit == 0 )); then
  final_state="PASSED"
else
  final_state="FAILED"
fi

if ! write_status "$final_state" "$test_exit" "$ended_at_utc" "$ended_at_local" "$ended_epoch" "$duration_seconds"; then
  printf '%s\n' "could not persist final status: $status_file" >&2
  if (( test_exit == 0 )); then
    exit 125
  fi
fi

if ! printf '[test-run] state=%s exit_code=%s ended_at_utc=%s run_id=%s status_file=%s log_file=%s\n' "$final_state" "$test_exit" "$ended_at_utc" "$run_id" "$status_file" "$log_file" | /usr/bin/tee -a "$log_file"; then
  if (( test_exit == 0 )); then
    exit 125
  fi
fi
exit "$test_exit"
