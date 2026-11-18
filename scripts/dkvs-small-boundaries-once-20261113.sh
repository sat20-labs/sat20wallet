#!/bin/zsh
set -euo pipefail
umask 077

# Authorized bounded diagnosis only. Astra owns this design; Luna executes it.
# No production file/config mutation, live-node access, source-tree copy, or
# restart. The directory threshold changes only in a temporary Go overlay.
GO_BIN="/usr/local/go/bin/go"
REPO="/Users/yingfeng/github/sat20wallet"
SDK="$REPO/sdk"
E2E="$SDK/e2e"
DKVS="/Users/yingfeng/github/satoshinet/indexer/indexer/dkvs"
TASK_CACHE="/private/tmp/sat20-dkvs-validation-cache-luna-20261113"
CACHE_MARKER="$TASK_CACHE/.dkvs-validation-owner"
CACHE_TOKEN="sat20-dkvs-validation-luna-20261113"
work="$(/usr/bin/mktemp -d /private/tmp/sat20-dkvs-small-boundaries.XXXXXX)"
cleanup() {
 exit_status=$?
 print "dkvs_small_boundaries_shell_exit=$exit_status"
 if [[ "$exit_status" -eq 0 ]]; then
  /bin/rm -rf "$work"
  print "temporary DKVS small-boundary data/overlay cleaned: $work"
 else
  print -u2 "temporary DKVS small-boundary evidence preserved: $work"
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
print "scope=two exact temporary SDK E2E diagnostics; real production RPC/P2P/Pebble, no real chain payment, no live node, dynamic httptest only, PWA/XAP artifacts untouched"
print "resource_limits=GOMAXPROCS=2 go-build-p=1 test-parallel=1; not a hard RAM or end-to-end time limit"

cat > "$work/dkvs_small_boundaries_runtime_diagnostic_test.go" <<'GOEOF'
package e2e

import (
 "bytes"
 "encoding/json"
 "fmt"
 "runtime"
 "strings"
 "sync"
 "testing"
 "time"

 indexercommon "github.com/sat20-labs/indexer/common"
 indexerdb "github.com/sat20-labs/indexer/indexer/db"
 "github.com/sat20-labs/satoshinet/chaincfg"
 dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
 dkvsp2p "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs/p2p"
 "github.com/sat20-labs/satoshinet/wire"
 "github.com/stretchr/testify/require"
)

const dkvsScanBaselineRecordPrefix = "dkvs:record:"

type dkvsScanBaselineStat struct {
	Kind       string
	Prefix     string
	Rows       int64
	ElapsedNS  int64
	CallerPath []string
}

type dkvsScanBaselineProbe struct {
	mu      sync.Mutex
	enabled bool
	stats   []dkvsScanBaselineStat
}

func (p *dkvsScanBaselineProbe) setEnabled(enabled bool) {
	p.mu.Lock()
	p.enabled = enabled
	p.mu.Unlock()
}

func (p *dkvsScanBaselineProbe) isEnabled() bool {
	p.mu.Lock()
	enabled := p.enabled
	p.mu.Unlock()
	return enabled
}

func (p *dkvsScanBaselineProbe) add(stat dkvsScanBaselineStat) {
	p.mu.Lock()
	p.stats = append(p.stats, stat)
	p.mu.Unlock()
}

func (p *dkvsScanBaselineProbe) take() []dkvsScanBaselineStat {
	p.mu.Lock()
	stats := append([]dkvsScanBaselineStat(nil), p.stats...)
	p.stats = nil
	p.mu.Unlock()
	return stats
}

type dkvsScanBaselineCountingDB struct {
	indexercommon.KVDB
	probe *dkvsScanBaselineProbe
}

func (db *dkvsScanBaselineCountingDB) recordCallers() []string {
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	var found []string
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, "scanLocked") ||
			strings.Contains(frame.Function, "projectedActiveRecordsLocked") ||
			strings.Contains(frame.Function, "validateBatchStateLocked") ||
			strings.Contains(frame.Function, "ensureQuotaUsageLocked") ||
			strings.Contains(frame.Function, "validateQuotaUsageProjectionLocked") ||
			strings.Contains(frame.Function, "projectPathMetasLocked") ||
			strings.Contains(frame.Function, "prepareFeeCapacity") {
			found = append(found, frame.Function)
		}
		if !more {
			break
		}
	}
	return found
}

func (db *dkvsScanBaselineCountingDB) track(prefix []byte, visit func([]byte, []byte) error,
	call func(func([]byte, []byte) error) error) error {
	if db.probe == nil || !db.probe.isEnabled() || !bytes.HasPrefix(prefix, []byte(dkvsScanBaselineRecordPrefix)) {
		return call(visit)
	}
	started := time.Now()
	var rows int64
	err := call(func(key, value []byte) error {
		rows++
		return visit(key, value)
	})
	kind := "record-prefix"
	if bytes.Equal(prefix, []byte(dkvsScanBaselineRecordPrefix)) {
		kind = "record-table-full"
	}
	db.probe.add(dkvsScanBaselineStat{
		Kind: kind, Prefix: string(prefix), Rows: rows,
		ElapsedNS: time.Since(started).Nanoseconds(), CallerPath: db.recordCallers(),
	})
	return err
}

func (db *dkvsScanBaselineCountingDB) BatchRead(prefix []byte, reverse bool, visit func([]byte, []byte) error) error {
	return db.track(prefix, visit, func(wrapped func([]byte, []byte) error) error {
		return db.KVDB.BatchRead(prefix, reverse, wrapped)
	})
}

func (db *dkvsScanBaselineCountingDB) BatchReadV2(prefix, seekKey []byte, reverse bool, visit func([]byte, []byte) error) error {
	return db.track(prefix, visit, func(wrapped func([]byte, []byte) error) error {
		return db.KVDB.BatchReadV2(prefix, seekKey, reverse, wrapped)
	})
}



type dkvsSmallAutopayState struct { payer string; height uint64 }
func (s dkvsSmallAutopayState) GetAutopayState(contract string) (*dkvs.AutopayContractState, error) {
 if contract != releaseReviewAutopay().PoolContract { return nil, dkvs.ErrInvalidFeeProof }
 return &dkvs.AutopayContractState{
  Contract: contract, TemplateName: "autopay.tc", Status: "active",
  CurrentBlock: int64(s.height), Delegates: map[string]dkvs.AutopayDelegateState{
   s.payer: {Status: "active", AmountPerBlock: "4", Balance: "400", LastPayHeight: int64(s.height)},
  },
 }, nil
}

func TestDKVSMixedHeightQuotaRuntimeDiagnostic(t *testing.T) {
 owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
 payer, err := dkvs.P2TRAddressFromPubKeyBytes(owner.Wallet.GetPubKey().SerializeCompressed(), &chaincfg.TestNetParams)
 require.NoError(t, err)
 key := func(suffix string) string {
  k, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), suffix)
  require.NoError(t, err)
  return k
 }
 for _, tip := range []uint64{100, 101} {
  t.Run(fmt.Sprintf("indexed_100_tip_%d", tip), func(t *testing.T) {
   raw := indexerdb.NewKVDB(t.TempDir())
   require.NotNil(t, raw)
   t.Cleanup(func() { require.NoError(t, raw.Close()) })
   probe := &dkvsScanBaselineProbe{}
   db := &dkvsScanBaselineCountingDB{KVDB: raw, probe: probe}
   f := newBoundRPCFixtureDB(t, db)
   verifier := dkvs.LocalCacheAutopayFeeVerifier{
    AllowFreeLocal: true,
    AutopayFeeVerifier: dkvs.AutopayFeeVerifier{
     StateProvider: dkvsSmallAutopayState{payer: payer, height: tip},
     Contract: releaseReviewAutopay().PoolContract,
     FullRecordFeePerBlock: "1", AddressParams: &chaincfg.TestNetParams,
    },
   }
   var _ dkvs.IndexedFeeCapacityVerifier = verifier
   f.store = dkvs.New(db, dkvs.Config{
    EndpointID: f.coreID, AllowFreeLocal: true, FreeLocalCache: dkvs.DefaultFreeLocalCachePolicy(),
    CurrentHeight: f.height.Load, TipHeight: func() uint64 { return tip }, FeeVerifier: verifier,
   })
   f.policy = &dkvs.WalletRPCAdmission{Indexer: f.store, IsCoreNode: f.core.Load, CurrentBinding: f.currentBinding}
   f.bind(t, owner, f.coreID)
   source := newReleaseReviewStore(t)
   source.backend.SetFeeVerifier(verifier)
   node, peer := &dkvsp2p.NodeState{}, &dkvsp2p.PeerState{}
   node.SetReady(true)
   t.Cleanup(peer.Close)
   var warnings []string
   receiver := dkvsp2p.Handler{
    Store: activePeerStore{releaseReviewPeerStore{f.store}}, Node: node, Peer: peer,
    TrustedSource: true, ValidatorID: f.coreID,
    LocalServices: wire.SFNodeMiner, RemoteServices: wire.SFNodeMiner,
    Warnf: func(format string, args ...interface{}) { warnings = append(warnings, fmt.Sprintf(format, args...)) },
   }
   rpcKey, p2pKey := key("mixed-height/rpc"), key("mixed-height/p2p")
   rpcPut := func(k, value string) (*wire.DKVSRecord, error) {
    return f.client.PutSignedRecordWithAutopay(owner.Wallet, k, []byte(value),
     dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
   }
   sourcePut := func(k, value string) *wire.DKVSRecord {
    r, err := source.client.PutSignedRecordWithAutopay(owner.Wallet, k, []byte(value),
     dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
    require.NoError(t, err)
    return r
   }
   accept := func(r *wire.DKVSRecord) (*wire.DKVSRecord, error) {
    warnings = nil
    receiver.OnNotify(dkvsp2p.NotifyForRecord(r))
    if len(warnings) != 0 { return r, fmt.Errorf("P2P warnings: %s", strings.Join(warnings, "; ")) }
    return r, nil
   }
   for i := 0; i < 2; i++ {
    _, err := rpcPut(key(fmt.Sprintf("mixed-height/seed-%d", i)), "seed")
    require.NoError(t, err)
   }
   _, err = rpcPut(rpcKey, "initial")
   require.NoError(t, err)
   _, err = accept(sourcePut(p2pKey, "initial"))
   require.NoError(t, err)
   oracle := func() int {
    count := 0
    prefix := []byte(dkvsScanBaselineRecordPrefix)
    require.NoError(t, raw.BatchReadV2(prefix, prefix, false, func(_, value []byte) error {
     r, err := dkvs.UnmarshalRecord(value)
     if err != nil { return err }
     proof, err := dkvs.ParseFeeProof(r.FeeProof)
     if err == nil && proof.Mode == dkvs.FeeModeAutopay && !dkvs.IsTombstone(r.Flags) && !dkvs.IsExpired(r, 100) {
      require.NoError(t, dkvs.VerifySignature(r))
      require.Equal(t, owner.Wallet.GetPubKey().SerializeCompressed(), r.PubKey)
      require.Equal(t, releaseReviewAutopay().PoolContract, proof.PoolContract)
      count++
     }
     return nil
    }))
    return count
   }
   require.Equal(t, 4, oracle(), "independent persisted-row oracle at exact paid capacity")
   measure := func(label string, op func() (*wire.DKVSRecord, error)) int {
    probe.take()
    probe.setEnabled(true)
    started := time.Now()
    ack, opErr := op()
    elapsed := time.Since(started)
    probe.setEnabled(false)
    stats := probe.take()
    full, rows := 0, int64(0)
    for _, stat := range stats {
     if stat.Kind == "record-table-full" { full++; rows += stat.Rows }
    }
    var ackHash, storedHash string
    var seq uint64
    if ack != nil {
     ackHash, seq = dkvs.RecordHash(ack).String(), ack.Seq
     stored, readErr := f.store.Get(ack.Key)
     require.NoError(t, readErr)
     storedHash = dkvs.RecordHash(stored).String()
    }
    evidence, jsonErr := json.Marshal(map[string]any{
     "label": label, "indexed_height": 100, "tip_height": tip, "full_scans": full, "full_rows": rows,
     "latency_ms": float64(elapsed.Nanoseconds())/1e6, "scan_stats": stats,
     "ack_hash": ackHash, "stored_hash": storedHash, "seq": seq, "operation_error": fmt.Sprint(opErr),
    })
    require.NoError(t, jsonErr)
    t.Logf("dkvs-mixed-height-sample: %s", evidence)
    require.NoError(t, opErr)
    require.NotNil(t, ack)
    require.Equal(t, ackHash, storedHash)
    require.Equal(t, 4, oracle())
    return full
   }
   // Same-method RPC traffic is the control before alternating ingress.
   for i := 0; i < 3; i++ {
    n := measure(fmt.Sprintf("rpc-only-%d", i), func() (*wire.DKVSRecord, error) { return rpcPut(rpcKey, fmt.Sprintf("rpc-only-%d", i)) })
    require.Zero(t, n, "warm RPC-only writes must avoid whole-table rebuilding")
   }
   p2pScans, rpcScans := 0, 0
   for i := 0; i < 3; i++ {
    incoming := sourcePut(p2pKey, fmt.Sprintf("p2p-%d", i))
    p2pScans += measure(fmt.Sprintf("alternating-p2p-%d", i), func() (*wire.DKVSRecord, error) { return accept(incoming) })
    rpcScans += measure(fmt.Sprintf("alternating-rpc-%d", i), func() (*wire.DKVSRecord, error) { return rpcPut(rpcKey, fmt.Sprintf("rpc-%d", i)) })
   }
   t.Logf("dkvs-mixed-height-result: indexed=100 tip=%d rounds=3 p2p_full_scans=%d rpc_full_scans=%d persisted_paid_records=%d", tip, p2pScans, rpcScans, oracle())
   if tip == 100 { require.Zero(t, p2pScans); require.Zero(t, rpcScans) }
   // A diagnostic reports the gap's observed scan count, not a new acceptable
   // performance threshold. No assertion masks or blesses repeated rebuilding.
   overflow := key("mixed-height/overflow")
   _, err = rpcPut(overflow, "must-not-commit")
   require.ErrorIs(t, err, dkvs.ErrFeeCapacityExceeded)
   _, err = f.store.Get(overflow)
   require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
   incoming := sourcePut(overflow, "must-not-commit")
   warnings = nil
   receiver.OnNotify(dkvsp2p.NotifyForRecord(incoming))
   require.Contains(t, strings.Join(warnings, "; "), dkvs.ErrFeeCapacityExceeded.Error())
   _, err = f.store.Get(overflow)
   require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
   require.Equal(t, 4, oracle(), "both admission routes must reject the fifth paid key")
   require.Zero(t, f.unguarded.Load())
  })
 }
}

func TestDKVSDirectorySmallLimitRuntimeDiagnostic(t *testing.T) {
 require.Equal(t, 10, dkvsp2p.MaxPendingChanges, "only the isolated overlay may lower the production threshold")
 owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
 cases := []struct { name string; remote, local int; deleteLocal bool; expectedWarning string }{
  {name: "remote_9", remote: 9},
  {name: "remote_10", remote: 10},
  {name: "remote_11", remote: 11, expectedWarning: dkvsp2p.ErrSyncStagingLimit.Error()},
  {name: "remote_6_local_5_including_empty", remote: 6, local: 5, deleteLocal: true,
   expectedWarning: "too many DKVS current paths in one discovery"},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   p := newActivePeerPair(t)
   p.receiver.Node.SetReady(false)
   seed := func(store *releaseReviewStore, label string, count int) []*wire.DKVSRecord {
    var records []*wire.DKVSRecord
    paths := map[string]bool{}
    for i := 0; i < count; i++ {
     k, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), fmt.Sprintf("small-dir-%s-%02d/value", label, i))
     require.NoError(t, err)
     r, err := store.client.PutSignedRecordWithAutopay(owner.Wallet, k, []byte("seed"),
      dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
     require.NoError(t, err)
     path, err := dkvs.CollectionPathForKey(k)
     require.NoError(t, err)
     require.False(t, paths[path], "records must be distinct collection paths, not keys in one path")
     paths[path] = true
     records = append(records, r)
    }
    return records
   }
   remote := seed(p.source, "remote", tc.remote)
   local := seed(p.target, "local", tc.local)
   if tc.deleteLocal {
    _, err := p.target.client.DeleteCurrentRecord(owner.Wallet, local[len(local)-1].Key, 100)
    require.NoError(t, err)
    _, err = p.target.client.GetRecord(local[len(local)-1].Key)
    require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
   }
   sourcePaths, err := p.source.backend.NetworkPaths()
   require.NoError(t, err)
   targetPaths, err := p.target.backend.NetworkPaths()
   require.NoError(t, err)
   require.Len(t, sourcePaths, tc.remote)
   require.Len(t, targetPaths, tc.local, "physically deleted local key retains its synchronization directory")
   rounds := 1
   if tc.expectedWarning != "" { rounds = 2 }
   for round := 0; round < rounds; round++ {
    if round != 0 {
     p.targetPeer = &dkvsp2p.PeerState{}
     p.receiver.Peer = p.targetPeer
     p.receiver.Node = &dkvsp2p.NodeState{}
     p.serving.Peer = &dkvsp2p.PeerState{}
     p.requests, p.responses = nil, nil
    }
    t.Cleanup(p.receiver.Peer.Close)
    t.Cleanup(p.serving.Peer.Close)
    var warnings []string
    p.receiver.Warnf = func(format string, args ...interface{}) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
    p.receiver.OnTrustedConnected()
    // Existing fixture's guard is 128 rounds; <= 11 directories require far
    // fewer. It is never increased or used as the expected failure.
    p.deliver(t)
    ready := p.receiver.Node.Ready()
    p.receiver.Peer.Close() // cancel only this fixture's pending retry timers
    p.serving.Peer.Close()
    joined := strings.Join(warnings, "; ")
    received := 0
    for _, r := range remote {
     actual, err := p.target.client.GetRecord(r.Key)
     if err == nil { require.Equal(t, dkvs.RecordHash(r), dkvs.RecordHash(actual)); received++ } else { require.ErrorIs(t, err, dkvs.ErrRecordNotFound) }
    }
    t.Logf("dkvs-directory-result: threshold=%d remote_paths=%d local_paths=%d local_empty=%t reconnect_round=%d ready=%t received=%d warning=%q",
     dkvsp2p.MaxPendingChanges, len(sourcePaths), len(targetPaths), tc.deleteLocal, round, ready, received, joined)
    if tc.expectedWarning == "" {
     require.Empty(t, warnings)
     require.True(t, ready)
     require.Equal(t, tc.remote, received)
    } else {
     require.Contains(t, joined, tc.expectedWarning)
     require.False(t, ready, "baseline diagnosis: inventory/union cap prevents completion")
     require.Zero(t, received, "discovery inventory is not installed as current state")
    }
   }
  })
 }
}

GOEOF
"/usr/local/go/bin/gofmt" -w "$work/dkvs_small_boundaries_runtime_diagnostic_test.go"
/usr/bin/python3 - "$SDK" "$DKVS" "$work" <<'PYEOF'
import json, pathlib, re, sys
sdk, dkvs, work = map(pathlib.Path, sys.argv[1:])
original = dkvs / "p2p" / "state.go"
text = original.read_text()
pattern = r"(MaxPendingChanges\s*=\s*)10000\b"
assert len(re.findall(pattern, text)) == 1, "unexpected threshold declaration"
patched = re.sub(pattern, lambda m: m.group(1) + "10", text)
assert patched != text
(work / "state_directory_limit_10.go").write_text(patched)
test_file = "dkvs_small_boundaries_runtime_diagnostic_test.go"
base = {str(sdk / "e2e" / test_file): str(work / test_file)}
(work / "mixed-overlay.json").write_text(json.dumps({"Replace": base}))
directory = dict(base)
directory[str(original)] = str(work / "state_directory_limit_10.go")
(work / "directory-overlay.json").write_text(json.dumps({"Replace": directory}))
print("overlay_change=MaxPendingChanges 10000->10 only in temporary compiler input; original untouched")
PYEOF
for file in "$work/dkvs_small_boundaries_runtime_diagnostic_test.go" "$work/state_directory_limit_10.go" "$work/directory-overlay.json" "$work/mixed-overlay.json"; do
 print "overlay_sha256=$(/usr/bin/shasum -a 256 "$file" | /usr/bin/awk '{print $1}') path=$file"
done
cd "$SDK"
discovery="$("$GO_BIN" list -overlay="$work/mixed-overlay.json" -f '{{.Dir}}|{{join .TestGoFiles ","}}' ./e2e)"
case "$discovery" in
 "$E2E|"*dkvs_small_boundaries_runtime_diagnostic_test.go*) print "overlay_discovery=expected_package_and_test_file" ;;
 *) print -u2 "unexpected overlay package discovery: $discovery"; exit 93 ;;
esac
print "running only TestDKVSDirectorySmallLimitRuntimeDiagnostic"
"$GO_BIN" test -v -failfast -p=1 -parallel=1 -count=1 -timeout=3m -overlay="$work/directory-overlay.json" -run='^TestDKVSDirectorySmallLimitRuntimeDiagnostic$' ./e2e
print "TestDKVSDirectorySmallLimitRuntimeDiagnostic_exit=0"
print "running only TestDKVSMixedHeightQuotaRuntimeDiagnostic"
"$GO_BIN" test -v -failfast -p=1 -parallel=1 -count=1 -timeout=3m -overlay="$work/mixed-overlay.json" -run='^TestDKVSMixedHeightQuotaRuntimeDiagnostic$' ./e2e
print "TestDKVSMixedHeightQuotaRuntimeDiagnostic_exit=0"
verify_sources
print "dkvs_small_boundaries_exit=0 end_utc=$(/bin/date -u +%FT%TZ)"
