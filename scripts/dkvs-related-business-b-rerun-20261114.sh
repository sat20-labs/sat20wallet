#!/bin/zsh
set -euo pipefail
umask 077

# Owner explicitly requested re-running the previously result-less Business-B
# cases on 2026-11-14. Astra designs this bounded runner; Luna executes once.
# Keep the original runner/log untouched. No production/test-source edit,
# source-tree copy, WASM build, node/service restart, or PWA session control.
GO_BIN="/usr/local/go/bin/go"
REPO="/Users/yingfeng/github/sat20wallet"
SDK="$REPO/sdk"
E2E="$SDK/e2e"
DKVS="/Users/yingfeng/github/satoshinet/indexer/indexer/dkvs"
TASK_CACHE="/private/tmp/sat20-dkvs-validation-cache-luna-20261113"
CACHE_MARKER="$TASK_CACHE/.dkvs-validation-owner"
CACHE_TOKEN="sat20-dkvs-validation-luna-20261113"
print "dkvs_business_b_rerun_entered runner_pid=$$ start_utc=$(/bin/date -u +%FT%TZ)"
work="$(/usr/bin/mktemp -d /private/tmp/sat20-dkvs-business-b-rerun.XXXXXX)"
cleanup() {
  exit_status=$?
  print "dkvs_business_b_rerun_shell_exit=$exit_status end_utc=$(/bin/date -u +%FT%TZ)"
  if [[ "$exit_status" -eq 0 ]]; then
    /bin/rm -rf "$work"
    print "temporary owned DKVS business-B rerun data/binary cleaned: $work"
  else
    print -u2 "temporary owned DKVS business-B rerun evidence preserved: $work"
  fi
  print "dedicated DKVS GOCACHE retained: $TASK_CACHE"
}
trap cleanup EXIT
/bin/mkdir -p "$work/go-tmp" "$work/tmp"
export GOTMPDIR="$work/go-tmp" TMPDIR="$work/tmp"
export GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly GOTOOLCHAIN=local
export GOMAXPROCS=2
export PATH="/usr/local/go/bin:$PATH"
export DKVS_PHASE_RECEIPTS="$work/phase-receipts.jsonl"
unset SAT20WALLET_E2E_ARTIFACTS || true

check_sha() {
  file="$1"
  expected="$2"
  actual="$(/usr/bin/shasum -a 256 "$file" | /usr/bin/awk '{print $1}')"
  print "source_sha256=$actual path=$file"
  if [[ "$actual" != "$expected" ]]; then
    print -u2 "unexpected source SHA for $file: $actual"
    exit 91
  fi
}
verify_sources() {
check_sha "$DKVS/batch_cas.go" "24af2c2467f0dee3df929e4fb2d6f5abe35451f2670695eeab01abdea6817b73"
check_sha "$DKVS/indexer.go" "120657d8c802f60506701d92d61c55da1524fea85c6ba95c9aef7c09a0db22d6"
check_sha "$DKVS/fee_usage.go" "4e92ee9d634ec46309f4ce1319d47cabf6ba6bf5d3c549a01b0ce2b6f710ab16"
check_sha "$DKVS/free_local.go" "69f4a42baf61d2008e97611938a0418b2bd478d79d257d3cac31c0102d75ebc4"
check_sha "$DKVS/mailbox_internal.go" "5326f68d5e99e41b7ea447eba7b0667e276da8e89833811f99b47bc288a32d1e"
check_sha "$DKVS/delete.go" "1e9dbef42295a1c353bd48c3843e0edb9288595d69cb032f6114a8f116cfe381"
check_sha "$DKVS/path_snapshot_apply.go" "4edbb33e56744cf78db15b0b220ff998f864bd205b19f98c31a2a23a7e0b82ed"
check_sha "$DKVS/atomic_apply.go" "15d6c485a4c46fb546c354a07e157f3372179578a3efa035d4777859d59559c7"
check_sha "$DKVS/paid_retention.go" "07b4329072e4d223a57d126b9febfa66e0c78c5d0fc19fedad8af8885f6ce7dd"
check_sha "$DKVS/quota_usage.go" "46f525b6c46cf3e3b7fb29c42d3f2ef45912e8c4eedc55048a1a0ad22fff0a11"
check_sha "$DKVS/topic_internal.go" "81c8fba2e5143d4324a057ebec28d8bbf2b3939b538d94c523904277322ea2a7"
check_sha "$DKVS/rpc_admission.go" "6da2a5290c3043e67edfb380439c163181ebc6e78c8acc8225d05b7529d284c6"
check_sha "$SDK/wallet/dkvs_subscription_sync.go" "6810e2f8631952ce77d67ae41313f2382815a7ddd982e8e2a667decdf424b097"
check_sha "$E2E/dkvs_launch_review_maintenance_e2e_test.go" "fd8de8eb80c875166a6d8fbc1260fd632d651f0d91a47b3c9c6af46651b7aa15"
check_sha "$E2E/dkvs_active_network_e2e_test.go" "f5a69544df2e66ca4af8d0c08a58e39dbc6a6c5cdb0807536966fb4933e184de"
check_sha "$E2E/dkvs_active_applications_e2e_test.go" "3ac00a86c4648a295dcce1468cdac583ef6aa8fb6b6e658b5d4fa0e0e6cf5d77"
check_sha "$E2E/dkvs_generation_peer_fixture_test.go" "f21bbcce568f0d366d42e72e07fa93d17add7de171173358ae018fad4b7424fc"
check_sha "$E2E/dkvs_release_review_state_e2e_test.go" "c67b8fba68bcf47b981eac9b432464978277f4716d32c99114dfea829cac76b6"
check_sha "$E2E/dkvs_bound_rpc_e2e_test.go" "e435a3daba03f7167945992800598e0c9ffa7a91ea7498020f8e4e053d58a3c8"
check_sha "$DKVS/account.go" "6230456daf1071d658a7dd09be158fb7062df6ded7c93c1b5c07b4733b76957c"
check_sha "$DKVS/p2p/state.go" "b05d529ff5ad524bdeafb2a65bba02500d18e99b924e02fffc61397d39fd327c"
check_sha "$DKVS/p2p/current_store.go" "66a50bc9891322a611017717be257c353d2668d6a44f60d85b5c233b47763bc1"
check_sha "$DKVS/p2p/node_sync.go" "5d27b2800d706a8316ec9f911b9df4df9bb4a9d8d5fb155f11479cb70609d6a1"
check_sha "$DKVS/p2p/handler.go" "a604db41478257fd636ff40a29f26be808c3c4b58ba2c9f7099af0ceb610d44b"
check_sha "$DKVS/p2p/path_sync.go" "06c318714cc4927eac2231e9b9d15a96b8a763e08ec63c8c48d5e64c6eaff981"
check_sha "$DKVS/active_network.go" "d7bb8d0bf612aa2a622a3a7d79f6ded0664ca4ebd83766b319e6b7efba98ef11"
check_sha "$DKVS/write_validation.go" "7334d5a9e5d97736e01a2b0d351571a666fbf197787849e6a54e4771ed3d0d5e"
check_sha "$DKVS/fee_autopay.go" "75f0b19505f92f6351d57521cb8a61e982d9b0993ddb8aa5f54bf3f0909b5a8d"
check_sha "$E2E/dkvs_release_review_peer_e2e_test.go" "a0a1cd3fb3b3d5d98ac89468919d2daef541a858c32be01afa36d5f1a9f6a710"
check_sha "$DKVS/../indexermgr.go" "53e2a8e8b0bde3e77d938d646887221fd831913147618d8104bf998c2a4144b1"
check_sha "$DKVS/../handle.go" "c6cdecd38ec0c0cdcd195a201c1f9b0f7840e16631ac24f9d391a1b57d96d7b8"
check_sha "$DKVS/expiry.go" "370410bba184e26e890b5998ef6302d25088e07502bdb01c8270f64da85fdf24"
check_sha "$SDK/wallet/dkvs_client_final.go" "6b1474c28d94a800e1c16a27fe34b258f426bff3895f7ab3890fb8a697c3cf16"
check_sha "$SDK/wallet/dkvs_active_client.go" "ca754ae200d723d2c4093c20450a70b1efdff1100a491168ba8b7b2c0fdb8e24"
check_sha "$DKVS/types.go" "e42c15b83db001f06851fcabaa68550865520df0bc1d06e22f8edb9b1db744ee"
check_sha "$E2E/runtime_lock_unix_test.go" "52460744d5076426923a1cbd3ba07e85d137f6e2e88dba9e8d6317c011c75384"
check_sha "$E2E/runtime_dirs_test.go" "76c1cfb92f33301d1812b0acdd78dd237c053ae54eecc0faeca404e947a6e521"
check_sha "$SDK/wallet/account_ui.go" "4a04c191a45ddd69f608bda30a1c6f6afc5da029d527fcc866ae5ff05c0236e8"
check_sha "$SDK/wallet/manager.go" "d079d8d3902edad45de7148c114864a81ae34ad96dfca75b3d343837e6c90d59"
check_sha "/Users/yingfeng/github/sat20wallet/sdk/go.mod" "9b5dbacfea02df8ae953fcdb0742cfa86924ec1d6fafe62a483aadb504a0be0e"
check_sha "/Users/yingfeng/github/sat20wallet/sdk/go.sum" "ed4e6771285942bf7a21f8389359449334af98c665bd45760bb5b69adee81602"
check_sha "/Users/yingfeng/github/satoshinet/go.mod" "3a6e13d184215e070194a8e025e5a6651ec3a814e05386629ef9a9bc885167b5"
check_sha "/Users/yingfeng/github/satoshinet/go.sum" "4761373955225cabcf0f1c05d01fdc7e2ac1097743830dba3c52d6b72cd77888"
}
verify_sources

if [[ -L "$TASK_CACHE" ]]; then
  print -u2 "refusing symlink task GOCACHE: $TASK_CACHE"
  exit 92
fi
if [[ -e "$TASK_CACHE" ]]; then
  if [[ ! -f "$CACHE_MARKER" ]] || [[ -L "$CACHE_MARKER" ]] || [[ "$(/bin/cat "$CACHE_MARKER")" != "$CACHE_TOKEN" ]]; then
    print -u2 "refusing to reuse unowned task GOCACHE: $TASK_CACHE"
    exit 92
  fi
else
  /bin/mkdir -m 700 "$TASK_CACHE"
  /usr/bin/printf '%s\n' "$CACHE_TOKEN" > "$CACHE_MARKER"
fi
export GOCACHE="$TASK_CACHE"

print "isolated_group_tmp=$work"
print "task_gocache=$GOCACHE"
print "go_version=$("$GO_BIN" version)"
print "scope=the original 3 Business-B top-level tests only (3+5+3 existing leaves), serial/fail-fast, unchanged assertions"
print "isolation=fresh Pebble/temp paths, dynamic httptest, in-process P2P; no live node/fixed port/PWA/XAP artifacts"
print "resource_limits=GOMAXPROCS=2 build-p=1 test-parallel=1; no hard RSS claim"
print "timeouts=compile hard 300s; each existing test retains Go timeout 8m, wrapper hard 500s; 30s heartbeat; only this runner's own child process group may be stopped"

run_bounded() {
  /usr/bin/python3 - "$@" <<'PYEOF'
import datetime
import json
import os
import pathlib
import signal
import subprocess
import sys
import time

label, limit, cwd = sys.argv[1], float(sys.argv[2]), sys.argv[3]
argv = sys.argv[4:]
receipt_path = pathlib.Path(os.environ["DKVS_PHASE_RECEIPTS"])
def utc():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()
def emit(record):
    line = json.dumps(record, sort_keys=True)
    print("dkvs_phase " + line, flush=True)
    with receipt_path.open("a", encoding="utf-8") as f:
        f.write(line + "\n")
        f.flush()

class Interrupted(Exception):
    def __init__(self, signum):
        self.signum = signum

def interrupt(signum, _frame):
    raise Interrupted(signum)
signal.signal(signal.SIGTERM, interrupt)
signal.signal(signal.SIGINT, interrupt)
proc = None
start = time.monotonic()
timed_out = False
rc = 1
emit({"event": "start", "stage": label, "utc": utc(), "cwd": cwd,
      "argv": argv, "hard_timeout_seconds": limit, "supervisor_pid": os.getpid()})

def stop_owned_group():
    if proc is None:
        return
    # start_new_session=True makes this exact child PID its own process-group
    # leader. No name matching, shared-node kill, or external process control.
    try:
        os.killpg(proc.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        proc.wait()

try:
    proc = subprocess.Popen(argv, cwd=cwd, start_new_session=True)
    emit({"event": "spawned", "stage": label, "utc": utc(),
          "pid": proc.pid, "owned_pgid": proc.pid})
    deadline = start + limit
    while True:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            timed_out = True
            emit({"event": "hard_timeout", "stage": label, "utc": utc(),
                  "pid": proc.pid})
            stop_owned_group()
            rc = 124
            break
        try:
            rc = proc.wait(timeout=min(30, remaining))
            if rc < 0:
                rc = 128 - rc
            break
        except subprocess.TimeoutExpired:
            emit({"event": "heartbeat", "stage": label, "utc": utc(),
                  "pid": proc.pid, "elapsed_seconds": round(time.monotonic()-start, 3)})
except Interrupted as exc:
    # Finish only this newly launched stage, never an earlier/foreign run.
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    stop_owned_group()
    rc = 128 + exc.signum
except Exception as exc:
    stop_owned_group()
    emit({"event": "supervisor_error", "stage": label, "utc": utc(),
          "error": repr(exc)})
    rc = 125
finally:
    emit({"event": "exit", "stage": label, "utc": utc(), "exit_code": rc,
          "timed_out": timed_out, "elapsed_seconds": round(time.monotonic()-start, 3)})
sys.exit(rc)
PYEOF
}

# Compile once. Go's -timeout applies only after a test binary starts; this
# separate bounded phase also covers compiler/linker stalls.
compile_rc=0
run_bounded "compile" 300 "$SDK" "$GO_BIN" test -c -p=1 -o "$work/dkvs-business-b.test" ./e2e || compile_rc=$?
print "compile_exit_observed=$compile_rc"
# Check shared inputs even when compilation fails. A changed input is not a
# DKVS business failure and must never cause an automatic retry.
verify_sources
if [[ "$compile_rc" -ne 0 ]]; then
  print "sdk_e2e_business_b_rerun_exit=$compile_rc stopped_at=compile"
  exit "$compile_rc"
fi
print "compiled_test_binary_sha256=$(/usr/bin/shasum -a 256 "$work/dkvs-business-b.test" | /usr/bin/awk '{print $1}')"
run_test() {
  test_name="$1"
  # Match go test's package working directory, not the module root.
  case_rc=0
  run_bounded "$test_name" 500 "$E2E" "$work/dkvs-business-b.test" -test.v -test.failfast -test.parallel=1 -test.count=1 -test.timeout=8m "-test.run=^$test_name$" || case_rc=$?
  print "case_exit_observed=$case_rc selector=$test_name"
  verify_sources
  if [[ "$case_rc" -ne 0 ]]; then
    print "sdk_e2e_business_b_rerun_exit=$case_rc stopped_at=$test_name"
    exit "$case_rc"
  fi
}
run_test "TestSDKDKVSLaunchReviewMaintenance"
run_test "TestSDKDKVSActiveNetwork"
run_test "TestSDKDKVSActiveApplications"
print "sdk_e2e_business_b_rerun_exit=0 end_utc=$(/bin/date -u +%FT%TZ)"
