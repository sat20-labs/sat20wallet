#!/bin/zsh
set -euo pipefail
umask 077

# Authorized bounded diagnosis only. Astra owns this design; Luna executes it.
# No production file/config mutation, live-node access, source-tree copy, or
# restart. Only the waiting/in-flight queue edge is diagnosed; completed directory cases are not repeated.
GO_BIN="/usr/local/go/bin/go"
REPO="/Users/yingfeng/github/sat20wallet"
SDK="$REPO/sdk"
E2E="$SDK/e2e"
DKVS="/Users/yingfeng/github/satoshinet/indexer/indexer/dkvs"
TASK_CACHE="/private/tmp/sat20-dkvs-validation-cache-luna-20261113"
CACHE_MARKER="$TASK_CACHE/.dkvs-validation-owner"
CACHE_TOKEN="sat20-dkvs-validation-luna-20261113"
work="$(/usr/bin/mktemp -d /private/tmp/sat20-dkvs-queue-boundary.XXXXXX)"
cleanup() {
 exit_status=$?
 print "dkvs_queue_boundary_shell_exit=$exit_status"
 if [[ "$exit_status" -eq 0 ]]; then
  /bin/rm -rf "$work"
  print "temporary DKVS queue-boundary data/overlay cleaned: $work"
 else
  print -u2 "temporary DKVS queue-boundary evidence preserved: $work"
 fi
 print "dedicated DKVS GOCACHE retained: $TASK_CACHE"
}
trap cleanup EXIT
/bin/mkdir -p "$work/go-tmp" "$work/tmp"
export GOTMPDIR="$work/go-tmp"
export TMPDIR="$work/tmp"
export GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly GOTOOLCHAIN=local
export GOMAXPROCS=2
export PATH="/usr/local/go/bin:$PATH"
unset SAT20WALLET_E2E_ARTIFACTS || true
check_sha() {
 file="$1"
 expected="$2"
 actual="$(/usr/bin/shasum -a 256 "$file" | /usr/bin/awk '{print $1}')"
 print "source_sha256=$actual path=$file"
 if [[ "$actual" != "$expected" ]]; then
  print -u2 "unexpected source SHA: $file"
  exit 91
 fi
}
verify_sources() {
check_sha "$DKVS/account.go" "6230456daf1071d658a7dd09be158fb7062df6ded7c93c1b5c07b4733b76957c"
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
check_sha "$E2E/dkvs_active_network_e2e_test.go" "f5a69544df2e66ca4af8d0c08a58e39dbc6a6c5cdb0807536966fb4933e184de"
check_sha "$E2E/dkvs_release_review_state_e2e_test.go" "c67b8fba68bcf47b981eac9b432464978277f4716d32c99114dfea829cac76b6"
check_sha "$E2E/dkvs_bound_rpc_e2e_test.go" "e435a3daba03f7167945992800598e0c9ffa7a91ea7498020f8e4e053d58a3c8"

check_sha "$DKVS/p2p/state.go" "f407ac666e53afd1ba5b345d97edd2808dcaf9742601861278116f0e77e0e4dd"
check_sha "$DKVS/p2p/current_store.go" "36278ef34c3b88500c2ff825e8ac8059f029d6e9ddd3629b19c12bec81836678"
check_sha "$DKVS/p2p/node_sync.go" "13cc9604549c90854f8f42adafc830e1f42782b78fcaf382db1d346f958acf0a"
check_sha "$DKVS/p2p/handler.go" "c6585575d42a5816ce848e24b418485bc83e103dbc98c55e6b849b1e147a78d1"
check_sha "$DKVS/p2p/path_sync.go" "06c318714cc4927eac2231e9b9d15a96b8a763e08ec63c8c48d5e64c6eaff981"
check_sha "$DKVS/active_network.go" "d7bb8d0bf612aa2a622a3a7d79f6ded0664ca4ebd83766b319e6b7efba98ef11"
check_sha "$DKVS/write_validation.go" "7334d5a9e5d97736e01a2b0d351571a666fbf197787849e6a54e4771ed3d0d5e"
check_sha "$DKVS/fee_autopay.go" "75f0b19505f92f6351d57521cb8a61e982d9b0993ddb8aa5f54bf3f0909b5a8d"
check_sha "$E2E/dkvs_release_review_peer_e2e_test.go" "a0a1cd3fb3b3d5d98ac89468919d2daef541a858c32be01afa36d5f1a9f6a710"
}
verify_sources
if [[ -L "$TASK_CACHE" ]]; then
  print -u2 "refusing symlink task GOCACHE: $TASK_CACHE"
  exit 92
fi
if [[ -e "$TASK_CACHE" ]]; then
  if [[ ! -f "$CACHE_MARKER" ]] || [[ "$(/bin/cat "$CACHE_MARKER")" != "$CACHE_TOKEN" ]]; then
    print -u2 "refusing to reuse unowned task GOCACHE: $TASK_CACHE"
    exit 92
  fi
else
  /bin/mkdir -m 700 "$TASK_CACHE"
  /usr/bin/printf '%s\n' "$CACHE_TOKEN" > "$CACHE_MARKER"
fi
export GOCACHE="$TASK_CACHE"


print "runner_pid=$$ start_utc=$(/bin/date -u +%FT%TZ)"
print "isolated_group_tmp=$work"
print "task_gocache=$GOCACHE"
print "go_version=$("$GO_BIN" version)"
print "scope=one exact temporary SDK P2P queue-close diagnostic; real production RPC/P2P/Pebble, no real chain payment, no live node, dynamic httptest only, PWA/XAP artifacts untouched"
print "resource_limits=GOMAXPROCS=2 go-build-p=1 test-parallel=1; not a hard RAM or end-to-end time limit"

cat > "$work/dkvs_queue_close_runtime_diagnostic_test.go" <<'GOEOF'
package e2e

import (
 "fmt"
 "testing"
 dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
 dkvsp2p "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs/p2p"
 "github.com/sat20-labs/satoshinet/wire"
 "github.com/stretchr/testify/require"
)

func TestDKVSQueueCloseBoundaryRuntimeDiagnostic(t *testing.T) {
 require.Equal(t, 10, dkvsp2p.MaxPendingChanges)
 p := newActivePeerPair(t)
 owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
 seed := func(n int) *wire.DKVSRecord {
  key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), fmt.Sprintf("queue-close-%02d/value", n))
  require.NoError(t, err)
  record, err := p.source.client.PutSignedRecordWithAutopay(owner.Wallet, key, []byte("seed"),
   dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
  require.NoError(t, err)
  return record
 }
 var records []*wire.DKVSRecord
 for i := 0; i < 11; i++ { records = append(records, seed(i)) }
 var warnings []string
 p.receiver.Warnf = func(format string, args ...interface{}) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
 for _, record := range records {
  path, err := dkvs.CollectionPathForKey(record.Key)
  require.NoError(t, err)
  p.receiver.QueuePathSync(path)
 }
 require.Len(t, p.requests, 1, "one P2P sync session, remaining paths wait")
 waiting, outstanding, buffered, active, ready, overflow := p.receiver.Node.DKVSQueueDiagnostic()
 t.Logf("dkvs-queue-boundary: phase=before-close waiting=%d outstanding=%d buffered=%d active=%t ready=%t overflow=%t", waiting, outstanding, buffered, active, ready, overflow)
 require.Equal(t, 10, waiting, "baseline queue guard counts only waiting paths")
 require.Equal(t, 11, outstanding)
 require.True(t, active)
 require.False(t, ready)
 require.False(t, overflow)

 // A valid realtime update arriving during sync must survive the disconnect.
 live := seed(11)
 p.receiver.OnNotify(dkvsp2p.NotifyForRecord(live))
 _, _, buffered, _, ready, _ = p.receiver.Node.DKVSQueueDiagnostic()
 require.Equal(t, 1, buffered)
 require.False(t, ready)

 // Close invokes the same failed-job requeue as a failed snapshot.
 p.targetPeer.Close()
 p.serving.Peer.Close()
 waiting, outstanding, buffered, active, ready, overflow = p.receiver.Node.DKVSQueueDiagnostic()
 t.Logf("dkvs-queue-boundary: phase=after-close waiting=%d outstanding=%d buffered=%d active=%t ready=%t overflow=%t", waiting, outstanding, buffered, active, ready, overflow)
 require.Equal(t, 11, waiting, "baseline evidence: returning the in-flight job exceeds the waiting limit by one")
 require.Equal(t, 11, outstanding)
 require.Equal(t, 1, buffered)
 require.False(t, active)
 require.False(t, ready)
 require.False(t, overflow)

 p.targetPeer = &dkvsp2p.PeerState{}
 p.receiver.Peer = p.targetPeer
 p.serving.Peer = &dkvsp2p.PeerState{}
 t.Cleanup(p.targetPeer.Close)
 t.Cleanup(p.serving.Peer.Close)
 p.requests, p.responses = nil, nil
 p.receiver.OnTrustedConnected()
 p.deliver(t)
 for _, want := range append(records, live) { p.requireValue(t, want) }
 waiting, outstanding, buffered, active, ready, overflow = p.receiver.Node.DKVSQueueDiagnostic()
 t.Logf("dkvs-queue-boundary: phase=reconnected waiting=%d outstanding=%d buffered=%d active=%t ready=%t overflow=%t received=%d warnings=%q", waiting, outstanding, buffered, active, ready, overflow, len(records)+1, warnings)
 require.Zero(t, waiting)
 require.Zero(t, outstanding)
 require.Zero(t, buffered)
 require.False(t, active)
 require.True(t, ready)
 require.False(t, overflow)
 require.Empty(t, warnings)
}

GOEOF
cat > "$work/dkvs_queue_diagnostic.go" <<'PROBEEOF'
package p2p

// Temporary compiler-overlay observation for the SDK integration diagnosis.
// It changes no state and is never installed in the production source tree.
func (s *NodeState) DKVSQueueDiagnostic() (waiting, outstanding, buffered int, active, ready, overflow bool) {
 s.pendingMtx.Lock()
 defer s.pendingMtx.Unlock()
 return len(s.pendingPathSync), len(s.pendingPathSyncSet), len(s.pending), s.syncPeer != nil, s.Ready(), s.pendingOverflow
}

PROBEEOF
"/usr/local/go/bin/gofmt" -w "$work/dkvs_queue_close_runtime_diagnostic_test.go" "$work/dkvs_queue_diagnostic.go"
/usr/bin/python3 - "$SDK" "$DKVS" "$work" <<'PYEOF'
import json, pathlib, re, sys
sdk, dkvs, work = map(pathlib.Path, sys.argv[1:])
original = dkvs / "p2p" / "state.go"
text = original.read_text()
pattern = r"(MaxPendingChanges\s*=\s*)10000\b"
assert len(re.findall(pattern, text)) == 1
(work / "state_limit_10.go").write_text(re.sub(pattern, lambda m: m.group(1) + "10", text))
replace = {
 str(sdk / "e2e" / "dkvs_queue_close_runtime_diagnostic_test.go"): str(work / "dkvs_queue_close_runtime_diagnostic_test.go"),
 str(dkvs / "p2p" / "dkvs_queue_diagnostic.go"): str(work / "dkvs_queue_diagnostic.go"),
 str(original): str(work / "state_limit_10.go"),
}
(work / "overlay.json").write_text(json.dumps({"Replace": replace}))
print("overlay_scope=temporary threshold 10 plus read-locked queue counters for this SDK P2P case; no production writes or process status helper")
PYEOF
for file in "$work/dkvs_queue_close_runtime_diagnostic_test.go" "$work/dkvs_queue_diagnostic.go" "$work/state_limit_10.go" "$work/overlay.json"; do
 print "overlay_sha256=$(/usr/bin/shasum -a 256 "$file" | /usr/bin/awk '{print $1}') path=$file"
done
cd "$SDK"
discovery="$("$GO_BIN" list -overlay="$work/overlay.json" -f '{{.Dir}}|{{join .TestGoFiles ","}}' ./e2e)"
case "$discovery" in
 "$E2E|"*dkvs_queue_close_runtime_diagnostic_test.go*) print "overlay_discovery=expected_sdk_package_and_test_file" ;;
 *) print -u2 "unexpected overlay SDK discovery: $discovery"; exit 93 ;;
esac
p2p_discovery="$("$GO_BIN" list -overlay="$work/overlay.json" -f '{{.Dir}}|{{join .GoFiles ","}}' github.com/sat20-labs/satoshinet/indexer/indexer/dkvs/p2p)"
case "$p2p_discovery" in
 "$DKVS/p2p|"*dkvs_queue_diagnostic.go*) print "overlay_discovery=expected_p2p_package_and_readonly_probe" ;;
 *) print -u2 "unexpected overlay P2P discovery: $p2p_discovery"; exit 94 ;;
esac
print "running only TestDKVSQueueCloseBoundaryRuntimeDiagnostic"
"$GO_BIN" test -v -failfast -p=1 -parallel=1 -count=1 -timeout=3m -overlay="$work/overlay.json" -run='^TestDKVSQueueCloseBoundaryRuntimeDiagnostic$' ./e2e
print "TestDKVSQueueCloseBoundaryRuntimeDiagnostic_exit=0"
verify_sources
print "dkvs_queue_boundary_exit=0 end_utc=$(/bin/date -u +%FT%TZ)"
