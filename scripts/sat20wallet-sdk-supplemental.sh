#!/bin/bash
set -uo pipefail
umask 077

# Fixed supplemental scope. The operator must first confirm no other SDK batch
# is running. Do not bypass the E2E TestMain lock or change TMPDIR.
if [[ "$#" -ne 0 ]]; then
  printf '%s\n' 'This fixed SDK supplemental runner accepts no arguments.' >&2
  exit 2
fi
ROOT="/Users/yingfeng/github/sat20wallet"
WORKDIR="$ROOT/sdk"
GO_BIN="/usr/local/go/bin/go"
FUNCTIONAL_MANIFEST="$ROOT/scripts/sdk-functional-roots.tsv"
LOG_ROOT="$ROOT/.cache/script-logs"
RUN_ID="sdk-supplemental-$(/bin/date -u '+%Y%m%dT%H%M%SZ')-$$"

# Normal native builds only; no inherited live, diagnostic, browser, recovery,
# persistent regtest, or fault-injection settings. Never print their values.
unset GOFLAGS GOOS GOARCH RGB11_HISTORY_ORACLE_DIR \
  RGB11_REGTEST_BITCOIN_CLI RGB11_REGTEST_COMPOSE_DIR RGB11_REGTEST_EVIDENCE_DIR RGB11_REGTEST_SCHEMA \
  SAT20WALLET_RUN_LIVE_NETWORK_TESTS SAT20WALLET_LEGACY_INTEGRATION SAT20WALLET_CONTRACT_TESTNET \
  SAT20WALLET_CONTRACT_TESTNET_DB SAT20WALLET_CONTRACT_TESTNET_MNEMONIC \
  SAT20WALLET_CORE_E2E_CONFIG SAT20WALLET_CORE_E2E_CHILD_RUN \
  SAT20WALLET_AUTOPAY_DEPLOY_LIVE SAT20WALLET_AUTOPAY_DEPLOY_OPERATOR_ONLY \
  SAT20WALLET_AUTOPAY_GAS_FUNDING SAT20WALLET_AUTOPAY_GAS_FUND_LIVE \
  SAT20WALLET_EVM_CLOSE_LIVE SAT20WALLET_FAUCET_DEPLOY_LIVE SAT20WALLET_FAUCET_URL \
  SAT20_LIVE_CHANNEL_HEARTBEAT SAT20_TEST_MNEMONIC SAT20_REMOTE_RECOVERY_READONLY \
  SAT20_ACCOUNT_DIAG_SNAPSHOT SAT20_ACCOUNT_DIAG_ACCOUNT_ID SAT20_RGB_DIAGNOSTIC_DIR \
  SAT20_PWA_ISOLATION_SHARED_FIXTURE SAT20_PWA_SKIP_GUARD_CHILD SAT20_PWA_DKVS_AB_CHILD SAT20_PWA_DKVS_AB_SHARED \
  SAT20_PWA_COMPLETE_POS_GROUP SAT20_PWA_DIAGNOSTIC SAT20_PWA_E2E_CASES SAT20_PWA_E2E_PORT \
  SAT20_PWA_E2E_WASM SAT20_PWA_E2E_WASM_RUNTIME SAT20_PWA_RELEASE_CONTINUE SAT20_PWA_RGB_CASE \
  SAT20_PWA_AUTOPAY_REVIEW SAT20_BROWSER_EXECUTABLE SAT20_TEST_IGNORE_INTERRUPT SAT20_TEST_LATE_OWNER \
  SAT20_E2E_LOCAL_REAL SAT20_E2E_REQUIRE_BITCOIND SAT20_SP_EVIDENCE_DIR \
  SAT20_REAL_BITCOIN_CLI SAT20_REAL_BITCOIN_DATADIR SAT20_REAL_BITCOIN_RPC_PORT SAT20_REAL_ESPLORA \
  SAT20_REAL_L1_INDEXER SAT20_REAL_PWA_EXISTING_URL SAT20_REAL_PWA_SDK_CASES SAT20_REAL_PWA_USAGE_CASE \
  SAT20_REAL_RECOVERY_FIXTURES SAT20_REAL_REGTEST_RUNTIME SAT20_REAL_SATOSHINET_ATTACH \
  SAT20_REAL_SDK_WALLET_DIR SAT20_REAL_TEST_WALLETS SATOSHINET_POS_CHECKING_INTERVAL \
  SATOSHINET_POS_MINER_INTERVAL SATOSHINET_POS_PREWARNING_INTERVAL SATOSHINET_RPCTEST_POS_V2_HEIGHT

wallet_excluded_roots=(
  TestSDKCoreModulesConnectedE2E TestSDKCoreModulesValidation
  TestDiagnosticGuardianPackageMatch TestDiagnosticRemoteAccountRecoveryReadOnly
  TestLiveChannelHeartbeatRestore TestLaunchPoolContract TestSwapContract
  TestAmmContract TestAmmContract_Runes TestSmartContractDeploymentRefundRedEvidence
  TestSmartContractModuleValidation TestSmartContractAdmissionValidation
  TestSmartContractReleaseDependencyResolution TestSmartContractReleaseFixValidation
  TestSmartContractRawEscrowValidation TestReviewScopedValidation
  TestSmartContractScopedFinalValidation TestSmartContractAwaitFullSuite
  TestReviewConcurrencyStress TestReviewRedGreenEvidence TestSmartContractReleaseArtifactAudit
  TestReviewFullSuiteLogSummary TestSmartContractResumeEvidence TestSmartContractSourceReview
  TestSmartContractReleaseDiffAudit TestReviewBaselinePublicationEvidence
)
e2e_roots=(
  TestCoreE2EChildSummaryNormalPass
  TestCoreE2EChildSummaryPackageFailureAndNoTestVerdict
  TestCoreE2EChildSummaryEarlyExitAfterRun
  TestCoreE2EChildSummaryRetainsPartialEventsOnOversizedOutput
  TestCoreE2EChildSummaryOuterDeadlineVersusChildTimeout
  TestCoreE2EChildSummaryNeverCopiesOutputOrTrustsPassMetric
  TestCoreE2EStructuredOutputClassification TestCoreE2ESegmentsNativeKeepsHistoryIsolated
  TestEVMGasPlanUsesGasAssetConversion TestStageSatoshiNetNodeConfig
  TestPOSPWAL1IndexerSharedBRC20AndRunes
  TestPOSPWAL1IndexerPreservesRealSignedTransactionAndAssetFlow
  TestPWAGroupL1FixturesDoNotSharePending TestSDKPWARegtestBrowserPortSelection
  TestRealRegtestPeerAddressesRequireLoopback
  TestSDKPWARegtestAttachConfigRequiresExistingIsolatedEndpoints
  TestSDKPWARegtestChannelPreparationDecision TestSDKPWARegtestExistingRecoveryFixtureIdentity
  TestSDKPWARegtestRecoveryAnswersUseExactPackagePrompts TestSDKPWARegtestMixedRuneInscriptionBuild
  TestSDKPWARegtestSgasBudgetUsesWholeAssetUnits TestSatoshiNetRuntimeDirectoryReuse
  TestSatoshiNetRuntimeDirectoryUpdatesChangedFiles TestSolidityFixturesLoadWithoutCompiler
  TestSolidityFixturesRejectStaleOrInvalidOutput
)

if [[ ! -x "$GO_BIN" || ! -x /usr/bin/python3 ]]; then
  printf '%s\n' 'The fixed Go executable and Python 3 are required.' >&2
  exit 127
fi
# Source-only selection checks, not go test -list. In particular, wallet must
# retain an empty -run: enumerating all roots can activate opt-in review tests.
selection="$(/usr/bin/python3 - "$WORKDIR" "$FUNCTIONAL_MANIFEST" \
  "${wallet_excluded_roots[@]}" "${e2e_roots[@]}" <<'PY_SELECTION'
import pathlib
import re
import sys

workdir, manifest = map(pathlib.Path, sys.argv[1:3])
extra, e2e = sys.argv[3:29], sys.argv[29:]
if len(extra) != 26 or len(set(extra)) != 26 or len(e2e) != 25 or len(set(e2e)) != 25:
    raise SystemExit("supplemental root count or uniqueness mismatch")
wallet = []
for raw in manifest.read_text().splitlines():
    if not raw or raw.startswith("#"):
        continue
    package, name, kind, source = raw.split("\t")
    if package != "wallet":
        continue
    if (kind not in {"interface", "e2e"} or not re.fullmatch(r"Test[A-Za-z0-9_]+", name)
            or name in wallet or source.split("/", 1)[0] != "wallet"):
        raise SystemExit("invalid wallet functional manifest entry")
    if not re.search(r"^func " + name + r"\(t \*testing.T\)", (workdir / source).read_text(), re.M):
        raise SystemExit("wallet functional root missing from source: " + name)
    wallet.append(name)
if len(wallet) != 70 or set(wallet) & set(extra):
    raise SystemExit("expected 70 functional wallet roots disjoint from 26 exclusions")
for package, names in (("wallet", extra), ("e2e", e2e)):
    source = "\n".join(path.read_text() for path in (workdir / package).glob("*_test.go"))
    for name in names:
        if not re.fullmatch(r"Test[A-Za-z0-9_]+", name):
            raise SystemExit("invalid supplemental root")
        if len(re.findall(r"^func " + name + r"\(t \*testing.T\)", source, re.M)) != 1:
            raise SystemExit("supplemental root missing or duplicated: " + name)
print("^(" + "|".join(wallet + extra) + ")$")
print("^(" + "|".join(e2e) + ")$")
PY_SELECTION
)" || exit 2
WALLET_SKIP="${selection%%$'\n'*}"
E2E_RUN="${selection#*$'\n'}"

/bin/mkdir -p "$LOG_ROOT" || exit 125
cd "$WORKDIR" || exit 125
phase_names=(modules wallet e2e-lightweight)
phase_timeouts=(10m 150m 10m)

write_status() {
  local phase="$1" state="$2" started="$3" ended="$4" result="$5" duration="$6"
  local status="$LOG_ROOT/$RUN_ID.$phase.status"
  {
    printf 'state=%s\nrun_id=%s\npid=%s\nphase=%s\n' "$state" "$RUN_ID" "$$" "$phase"
    printf 'started_at_utc=%s\nended_at_utc=%s\nexit_code=%s\nduration_seconds=%s\n' \
      "$started" "$ended" "$result" "$duration"
    printf 'log_file=%s\nstatus_file=%s\n' "$LOG_ROOT/$RUN_ID.$phase.jsonl" "$status"
  } > "$status.tmp" && /bin/mv -f "$status.tmp" "$status"
}

# Later phases remain explicitly NOTRUN after a failure. No retries or cleanup
# of shared data/processes are performed by this runner.
for phase in "${phase_names[@]}"; do
  : > "$LOG_ROOT/$RUN_ID.$phase.jsonl" || exit 125
  write_status "$phase" NOTRUN '' '' '' '' || exit 125
done
printf 'run_id=%s\nlog_root=%s\n' "$RUN_ID" "$LOG_ROOT"

run_phase() {
  local phase="$1" timeout="$2"
  shift 2
  local started started_epoch ended_epoch result state
  started="$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')"
  started_epoch="$(/bin/date '+%s')"
  write_status "$phase" RUNNING "$started" '' '' '' || return 125
  "$GO_BIN" test -mod=readonly -json -p=1 -parallel=1 -count=1 -timeout="$timeout" \
    "$@" > "$LOG_ROOT/$RUN_ID.$phase.jsonl" 2>&1
  result=$?
  ended_epoch="$(/bin/date '+%s')"
  state=FAILED
  if [[ "$result" -eq 0 ]]; then state=PASSED; fi
  write_status "$phase" "$state" "$started" "$(/bin/date -u '+%Y-%m-%dT%H:%M:%SZ')" \
    "$result" "$((ended_epoch - started_epoch))" || return 125
  printf 'phase=%s\nstate=%s\nexit_code=%s\nduration_seconds=%s\nlog_file=%s\nstatus_file=%s\n' \
    "$phase" "$state" "$result" "$((ended_epoch - started_epoch))" \
    "$LOG_ROOT/$RUN_ID.$phase.jsonl" "$LOG_ROOT/$RUN_ID.$phase.status"
  return "$result"
}

# Existing focused runners use 10m per package; the full wallet phase retains
# the native runner's 150m budget. These are Go test timeouts, not build timers.
run_phase "${phase_names[0]}" "${phase_timeouts[0]}" -run '' \
  ./account ./account/fuzzy ./account/internal/shamir ./wallet/dkvs \
  ./wallet/lightnode ./wallet/rgb11 ./testutil/fakeindexer ./wasm || exit "$?"
run_phase "${phase_names[1]}" "${phase_timeouts[1]}" -run '' -skip "$WALLET_SKIP" ./wallet || exit "$?"
run_phase "${phase_names[2]}" "${phase_timeouts[2]}" -run "$E2E_RUN" ./e2e || exit "$?"
exit 0
