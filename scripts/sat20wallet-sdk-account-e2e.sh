#!/bin/bash
set -uo pipefail

GO_BIN="/usr/local/go/bin/go"
WORKDIR="/Users/yingfeng/github/sat20wallet/sdk"
WORKSPACE="/Users/yingfeng/github"
LOG_ROOT="/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs/sat20wallet-sdk-account-e2e"
SUITE="sat20wallet-sdk-account-e2e"
TIMEOUT_MS="6000000"
PATTERN='^(TestSDKAccountNoPluginRecoverySharedNetworkGroup|TestSDKPaidAccountDKVSSharedNetworkGroup|TestSDKAccountDKVSSyncGate|TestSDKAccountFailureRegressions|TestRealSatoshiNetAccountManagementLifecycleAndConcurrentDevices|TestSDKAccountPWAEntryPoints|TestSDKAccountPWAOfflineRestart|TestSDKAccountPWAIndependentGuardian|TestSDKAccountPWACommitResponseLossRestart|TestSDKAccountPWAPaidFundingWithChildSelected|TestSDKAccountAutopayPreparedTransactionColdRetry|TestSDKAccountPWATemporaryExpiry|TestSDKAccountSameModeReconfigurationConverges|TestSDKAccountTemporaryTTLPolicyChangeConverges|TestSDKAccountRevertedMetadataClearsPending|TestSDKAccountReviewAuthorization|TestSDKAccountReviewServerSignatureVerification|TestSDKAccountReviewMultiDevice|TestSDKAccountReviewPasswordRestart|TestSDKAccountSilentPayRemoteBackupIndependentRecovery_NotUI)$'

if [[ "$#" -ne 0 ]]; then
  printf '%s\n' "This fixed account E2E runner accepts no arguments." >&2
  exit 2
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
cmd=(/usr/bin/env -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS "$GO_BIN" test -json -p=1 -parallel=1 ./e2e -run "$PATTERN" "-timeout=${TIMEOUT_MS}ms" -count=1)
printf -v command_args '%q ' "${cmd[@]}"
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
  printf '%s\n' "$command_text"
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

(
  cd "$WORKDIR" || exit 125
  "${cmd[@]}"
) 2>&1 | /usr/bin/tee -a "$log_file"
test_exit=$?

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

if ! printf '[test-run] state=%s exit_code=%s run_id=%s status_file=%s log_file=%s\n' "$final_state" "$test_exit" "$run_id" "$status_file" "$log_file" | /usr/bin/tee -a "$log_file"; then
  if (( test_exit == 0 )); then
    exit 125
  fi
fi
exit "$test_exit"
