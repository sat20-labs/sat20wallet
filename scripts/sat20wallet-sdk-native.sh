#!/bin/bash
set -uo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd -P)"
WORKDIR="$(cd "$SCRIPT_DIR/../sdk" && pwd -P)"
WORKSPACE="$(cd "$WORKDIR/../.." && pwd -P)"
GO_BIN="$(command -v go)"
LOG_ROOT="$WORKDIR/../.cache/script-logs/test-runs/sat20wallet-sdk-native"
SUITE="sat20wallet-sdk-native"
TIMEOUT_MS="10800000"
TAGS=""
if [[ "$#" == 1 && "$1" == "-tags=guardian" ]]; then
  TAGS="guardian"
elif [[ "$#" != 0 ]]; then
  printf '%s\n' "usage: sat20wallet-sdk-native.sh [-tags=guardian]" >&2
  exit 2
fi
if [[ -z "$GO_BIN" || ! -x "$GO_BIN" ]]; then
  printf '%s\n' "Go executable is required on PATH." >&2
  exit 127
fi
command -v python3 >/dev/null || { printf '%s\n' "Python 3 is required for result validation." >&2; exit 127; }
for dependency in satoshinet indexer rgb11 transcend; do
  [[ -d "$WORKSPACE/$dependency" ]] || { printf 'missing SDK E2E dependency: %s\n' "$WORKSPACE/$dependency" >&2; exit 2; }
done
CORE_NATIVE_RUN='^TestSDKCoreModulesConnectedE2E$/^(dkvs_real_paid_cross_instance_prefix_and_CAS|backup_multiple_wallets_and_subaccounts_control|RGB11_self_send_cold_restore_and_respend|RGB11_asset_name_and_transcend_registration_descriptor|RGB_issuance_in_multiple_subaccounts|invoice_transfer_every_paid_recovery_boundary|direct_transfer_every_paid_recovery_boundary|RGB11_same_root_three_device_cas_cancel_broadcast_and_respend|RGB11_recovery_identity_batch_and_hop_lifecycles|RGB11_fullsend_history_cold_recovery_reorg_and_competing_intent|RGB11_R23_temporary_TTL_expiry_cold_restore_and_respend|RGB11_R09_same_root_real_CAS_loser_retains_private_intent_and_tests_recovery|RGB11_recovery_capacity_boundaries|RGB11_account_recovery_failure_boundaries)$'
if [[ "$TAGS" == guardian ]]; then
  CORE_NATIVE_RUN="${CORE_NATIVE_RUN%')$'}|guardian_two_of_three_and_wrong_knowledge)$"
fi
# Only the Core child harness and its explicit convenience wrapper are omitted.
# All other default-build tests in every package run, including internal packages.
SKIP_PATTERN='^(TestSDKCoreModulesConnectedE2E|TestSDKCoreModulesValidation)$'
# Never inherit permission to use live or retained acceptance networks. Native
# real-chain fixtures allocate their own directories/ports inside their group.
safe_env=(env
  -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS -u SAT20WALLET_LEGACY_INTEGRATION -u SAT20WALLET_CONTRACT_TESTNET
  -u SAT20_LIVE_CHANNEL_HEARTBEAT -u SAT20WALLET_CORE_E2E_CONFIG -u SAT20WALLET_CORE_E2E_CHILD_RUN
  -u SAT20WALLET_E2E_ARTIFACTS -u SAT20_REAL_L1_INDEXER -u SAT20_REAL_ESPLORA
  -u SAT20_REAL_SATOSHINET_ATTACH -u SAT20_REAL_SATOSHINET_PORT_BASE -u SAT20_REAL_REGTEST_RUNTIME
  -u SAT20_REAL_SDK_WALLET_DIR -u SAT20_REAL_TEST_WALLETS -u SAT20_REAL_RECOVERY_FIXTURES
  -u SAT20_REAL_BITCOIN_CLI -u SAT20_REAL_BITCOIN_DATADIR -u SAT20_REAL_BITCOIN_RPC_PORT
  -u SAT20_REAL_RGB_CROSS_LAYER -u SAT20_E2E_LOCAL_REAL -u SAT20_ACCOUNT_UI_CONFIG
  -u SAT20_ACCOUNT_UI_FIXTURE -u GOFLAGS -u GOOS
  -u GOARCH -u RGB11_HISTORY_ORACLE_DIR -u RGB11_REGTEST_BITCOIN_CLI
  -u RGB11_REGTEST_COMPOSE_DIR -u RGB11_REGTEST_EVIDENCE_DIR -u RGB11_REGTEST_SCHEMA
  -u SAT20WALLET_CONTRACT_TESTNET_DB -u SAT20WALLET_CONTRACT_TESTNET_MNEMONIC -u SAT20WALLET_AUTOPAY_DEPLOY_LIVE
  -u SAT20WALLET_AUTOPAY_DEPLOY_OPERATOR_ONLY -u SAT20WALLET_AUTOPAY_GAS_FUNDING -u SAT20WALLET_AUTOPAY_GAS_FUND_LIVE
  -u SAT20WALLET_EVM_CLOSE_LIVE -u SAT20WALLET_FAUCET_DEPLOY_LIVE -u SAT20WALLET_FAUCET_URL
  -u SAT20_TEST_MNEMONIC -u SAT20_REMOTE_RECOVERY_READONLY -u SAT20_ACCOUNT_DIAG_SNAPSHOT
  -u SAT20_ACCOUNT_DIAG_ACCOUNT_ID -u SAT20_RGB_DIAGNOSTIC_DIR -u SAT20_PWA_ISOLATION_SHARED_FIXTURE
  -u SAT20_PWA_SKIP_GUARD_CHILD -u SAT20_PWA_DKVS_AB_CHILD -u SAT20_PWA_DKVS_AB_SHARED
  -u SAT20_PWA_COMPLETE_POS_GROUP -u SAT20_PWA_DIAGNOSTIC -u SAT20_PWA_E2E_CASES
  -u SAT20_PWA_E2E_PORT -u SAT20_PWA_E2E_WASM -u SAT20_PWA_E2E_WASM_RUNTIME
  -u SAT20_PWA_RELEASE_CONTINUE -u SAT20_PWA_RGB_CASE -u SAT20_PWA_AUTOPAY_REVIEW
  -u SAT20_BROWSER_EXECUTABLE -u SAT20_TEST_IGNORE_INTERRUPT -u SAT20_TEST_LATE_OWNER
  -u SAT20_SP_EVIDENCE_DIR -u SAT20_REAL_PWA_EXISTING_URL -u SAT20_REAL_PWA_SDK_CASES
  -u SAT20_REAL_PWA_USAGE_CASE -u SATOSHINET_POS_CHECKING_INTERVAL -u SATOSHINET_POS_MINER_INTERVAL
  -u SATOSHINET_POS_PREWARNING_INTERVAL -u SATOSHINET_RPCTEST_POS_V2_HEIGHT)
mkdir -p "$LOG_ROOT" || exit 2
started_epoch="$(date '+%s')"
started_at_utc="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
run_id="$(date -u '+%Y%m%dT%H%M%SZ')-$$"
log_file="$LOG_ROOT/$run_id.log"
events_file="$LOG_ROOT/$run_id.events.jsonl"
packages_file="$LOG_ROOT/$run_id.packages"
status_file="$LOG_ROOT/$run_id.status"
cmd=("${safe_env[@]}" "SAT20WALLET_CORE_E2E_CHILD_RUN=$CORE_NATIVE_RUN" SAT20_E2E_REQUIRE_BITCOIND=1
  "$GO_BIN" test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout=150m
  "-tags=$TAGS" -skip "$SKIP_PATTERN" ./...)
printf -v command_text '%q ' "${cmd[@]}"
write_status() {
  local state="$1" code="$2"
  {
    printf 'state=%s\nsuite=%s\nrun_id=%s\n' "$state" "$SUITE" "$run_id"
    printf 'working_directory=%s\ntimeout_ms=%s\nstarted_at_utc=%s\nstarted_epoch=%s\n' "$WORKDIR" "$TIMEOUT_MS" "$started_at_utc" "$started_epoch"
    printf 'command=%s\nlog_file=%s\nevents_file=%s\nstatus_file=%s\n' "$command_text" "$log_file" "$events_file" "$status_file"
    printf 'exit_code=%s\n' "$code"
    if [[ "$state" != RUNNING ]]; then
      printf 'ended_at_utc=%s\nduration_seconds=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$(( $(date '+%s') - started_epoch ))"
    fi
  } > "$status_file.tmp" && mv "$status_file.tmp" "$status_file"
}
write_status RUNNING "" || exit 125
printf 'run_id=%s\nstatus_file=%s\nlog_file=%s\nevents_file=%s\n+ %s\n' "$run_id" "$status_file" "$log_file" "$events_file" "$command_text" | tee "$log_file"
test_exit=0
(cd "$WORKDIR" && "${safe_env[@]}" "$GO_BIN" list -mod=readonly "-tags=$TAGS" ./...) > "$packages_file" 2>> "$log_file" || test_exit=125
if (( test_exit == 0 )); then
  (cd "$WORKDIR" && exec "${cmd[@]}") > "$events_file" 2>&1 || test_exit=1
  python3 - "$events_file" "$packages_file" <<'PY_PACKAGES' | tee -a "$log_file"
import collections
import json
import pathlib
import sys

def check(condition, message):
    if not condition:
        raise SystemExit("[package-coverage] FAILED: " + message)

log, inventory = sys.argv[1:]
expected = pathlib.Path(inventory).read_text().splitlines()
check(expected and len(expected) == len(set(expected)), "empty or duplicate package inventory")
packages = collections.defaultdict(list)
runs = collections.Counter()
verdicts = collections.defaultdict(list)
for line in pathlib.Path(log).read_text().splitlines():
    try:
        event = json.loads(line)
    except ValueError:
        continue
    package, name, action = event.get("Package"), event.get("Test"), event.get("Action")
    if not package:
        continue
    check(package in expected, "unexpected package: " + package)
    if name and action == "run":
        runs[(package, name)] += 1
    if name and action in {"pass", "fail", "skip"}:
        verdicts[(package, name)].append(action)
    if not name and action in {"pass", "fail", "skip"}:
        packages[package].append(action)
parents = {(p, n.rsplit("/", 1)[0]) for p, n in runs if "/" in n}
roots = collections.Counter(v[0] for (p,n),v in verdicts.items() if "/" not in n)
leaves = collections.Counter(v[0] for key,v in verdicts.items() if key not in parents)
skipped = [p + "/" + n for (p,n),v in verdicts.items() if v == ["skip"]]
print("[package-coverage] packages=" + str(len(expected)) + " results=" + json.dumps(dict(collections.Counter(v[0] for v in packages.values()))))
print("[package-coverage] roots=" + json.dumps(dict(roots)) + " leaves=" + json.dumps(dict(leaves)))
print("[package-coverage] SKIP=" + json.dumps(skipped))
print("[package-coverage] NOTRUN=Guardian business scenarios unless -tags=guardian; PWA UI/WASM bridge unless -tags=pwa; explicit live/retained-network cases remain gated")
print("[package-coverage] FAIL=" + json.dumps([p + "/" + n for (p,n),v in verdicts.items() if v == ["fail"]]))
print("[package-coverage] UNFINISHED=" + json.dumps([p + "/" + n for p,n in runs if (p,n) not in verdicts]))
check(set(packages) == set(expected), "missing package terminal results")
check(all(len(v) == 1 and v[0] in {"pass", "skip"} for v in packages.values()), "failed or duplicate package results")
check(runs and all(v == 1 for v in runs.values()), "no tests or duplicate executions")
check(set(runs) == set(verdicts), "test panic/unfinished execution or missing terminal verdict")
check(all(len(v) == 1 for v in verdicts.values()), "duplicate test verdicts")
check(not any(v == ["fail"] for v in verdicts.values()), "a test failed")
print("[package-coverage] PASSED: every discovered Go package completed; SKIP is never PASS")
PY_PACKAGES
  (( $? == 0 )) || test_exit=1
  python3 - "$events_file" "$WORKDIR/review-evidence/core-modules-e2e-latest.json" "$CORE_NATIVE_RUN" <<'PY_CORE' | tee -a "$log_file"
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
if (len(segments) != 2 or len(ordered) not in (14, 15)
        or ordered[9] != "RGB11_fullsend_history_cold_recovery_reorg_and_competing_intent"
        or report.get("shared_budget_seconds") != 5100 or report.get("budget_exhausted") is not False):
    check(False, "Core did not complete all selected native groups in two environments within the shared 85-minute budget")
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
check(len(expected) == len(ordered) and len(groups) == len(ordered) and set(actual) == expected, "Core native groups are missing, duplicated or unexpected")
check(all(action == "pass" for action in actual.values()), "a selected Core native group failed or skipped")
check(not any(row.get("Action") == "skip" and row.get("Test", "").startswith(prefix) for row in verdicts), "a selected Core child was skipped; SKIP is not PASS")
print("[core-native-coverage] PASSED: all selected native groups executed; no child skips")
PY_CORE
  (( $? == 0 )) || test_exit=1
fi
if (( test_exit == 0 )); then final_state=PASSED; else final_state=FAILED; fi
write_status "$final_state" "$test_exit" || exit 125
printf '[test-run] state=%s exit_code=%s run_id=%s status_file=%s log_file=%s\n' "$final_state" "$test_exit" "$run_id" "$status_file" "$log_file" | tee -a "$log_file"
exit "$test_exit"
