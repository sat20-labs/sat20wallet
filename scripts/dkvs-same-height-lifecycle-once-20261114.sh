#!/bin/zsh
set -euo pipefail
umask 077
print "dkvs_lifecycle_runner_entered pid=$ start_utc=$(/bin/date -u +%FT%TZ)"

# Authorized bounded diagnosis only. Astra owns this design; Luna executes it.
# No production file/config mutation, live-node access, source-tree copy, or
# existing service restart. Same-height quota lifecycle only; no old Business-B selector.
# Owner approved creating/reviewing this runner on 2026-11-14, not immediate
# execution. Luna may execute only after the PWA resource window is coordinated.
GO_BIN="/usr/local/go/bin/go"
REPO="/Users/yingfeng/github/sat20wallet"
SDK="$REPO/sdk"
E2E="$SDK/e2e"
DKVS="/Users/yingfeng/github/satoshinet/indexer/indexer/dkvs"
TASK_CACHE="/private/tmp/sat20-dkvs-validation-cache-luna-20261113"
CACHE_MARKER="$TASK_CACHE/.dkvs-validation-owner"
CACHE_TOKEN="sat20-dkvs-validation-luna-20261113"
work="$(/usr/bin/mktemp -d /private/tmp/sat20-dkvs-same-height-lifecycle.XXXXXX)"
cleanup() {
 exit_status=$?
 print "dkvs_same_height_lifecycle_shell_exit=$exit_status"
 if [[ "$exit_status" -eq 0 ]]; then
  /bin/rm -rf "$work"
  print "temporary DKVS same-height-lifecycle data/overlay cleaned: $work"
 else
  print -u2 "temporary DKVS same-height-lifecycle evidence preserved: $work"
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
print "scope=one temporary same-height SDK quota lifecycle regression and exactly two existing state leaves; real SDK/HTTP/P2P/Pebble, synthetic payment state, no live node, dynamic httptest, no PWA/XAP output or old Business-B selector"
print "resource_limits=GOMAXPROCS=2 go-build-p=1 test-parallel=1; not a hard RAM or end-to-end time limit"

cat > "$work/dkvs_quota_same_height_lifecycle_runtime_regression_test.go" <<'GOEOF'
package e2e

import (
 "bytes"
 "encoding/json"
 "fmt"
 "runtime"
 "strings"
 "sync"
 "sync/atomic"
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

const dkvsLifecycleRecordPrefix = "dkvs:record:"

type dkvsLifecycleStat struct {
	Kind       string
	Prefix     string
	Rows       int64
	ElapsedNS  int64
	CallerPath []string
}

type dkvsLifecycleProbe struct {
	mu      sync.Mutex
	enabled bool
	stats   []dkvsLifecycleStat
}

func (p *dkvsLifecycleProbe) setEnabled(enabled bool) {
	p.mu.Lock()
	p.enabled = enabled
	p.mu.Unlock()
}

func (p *dkvsLifecycleProbe) isEnabled() bool {
	p.mu.Lock()
	enabled := p.enabled
	p.mu.Unlock()
	return enabled
}

func (p *dkvsLifecycleProbe) add(stat dkvsLifecycleStat) {
	p.mu.Lock()
	p.stats = append(p.stats, stat)
	p.mu.Unlock()
}

func (p *dkvsLifecycleProbe) take() []dkvsLifecycleStat {
	p.mu.Lock()
	stats := append([]dkvsLifecycleStat(nil), p.stats...)
	p.stats = nil
	p.mu.Unlock()
	return stats
}

type dkvsLifecycleCountingDB struct {
	indexercommon.KVDB
	probe *dkvsLifecycleProbe
}

func (db *dkvsLifecycleCountingDB) recordCallers() []string {
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

func (db *dkvsLifecycleCountingDB) track(prefix []byte, visit func([]byte, []byte) error,
	call func(func([]byte, []byte) error) error) error {
	if db.probe == nil || !db.probe.isEnabled() || !bytes.HasPrefix(prefix, []byte(dkvsLifecycleRecordPrefix)) {
		return call(visit)
	}
	started := time.Now()
	var rows int64
	err := call(func(key, value []byte) error {
		rows++
		return visit(key, value)
	})
	kind := "record-prefix"
	if bytes.Equal(prefix, []byte(dkvsLifecycleRecordPrefix)) {
		kind = "record-table-full"
	}
	db.probe.add(dkvsLifecycleStat{
		Kind: kind, Prefix: string(prefix), Rows: rows,
		ElapsedNS: time.Since(started).Nanoseconds(), CallerPath: db.recordCallers(),
	})
	return err
}

func (db *dkvsLifecycleCountingDB) BatchRead(prefix []byte, reverse bool, visit func([]byte, []byte) error) error {
	return db.track(prefix, visit, func(wrapped func([]byte, []byte) error) error {
		return db.KVDB.BatchRead(prefix, reverse, wrapped)
	})
}

func (db *dkvsLifecycleCountingDB) BatchReadV2(prefix, seekKey []byte, reverse bool, visit func([]byte, []byte) error) error {
	return db.track(prefix, visit, func(wrapped func([]byte, []byte) error) error {
		return db.KVDB.BatchReadV2(prefix, seekKey, reverse, wrapped)
	})
}




type dkvsLifecycleAutopayState struct {
 payer string
 height *atomic.Uint64
}
func (s dkvsLifecycleAutopayState) GetAutopayState(contract string) (*dkvs.AutopayContractState, error) {
 if contract != releaseReviewAutopay().PoolContract { return nil, dkvs.ErrInvalidFeeProof }
 height := int64(s.height.Load())
 return &dkvs.AutopayContractState{
  Contract: contract, TemplateName: "autopay.tc", Status: "active",
  CurrentBlock: height, Delegates: map[string]dkvs.AutopayDelegateState{
   s.payer: {Status: "active", AmountPerBlock: "4", Balance: "400", LastPayHeight: height},
  },
 }, nil
}

// Cross-layer regression: SDK signing -> real HTTP admission / P2P Handler ->
// Pebble, with both heights supplied by the same callback as the default node.
// No production hooks or cache reads. The oracle scans raw persisted rows only
// outside the measured writes; it does not consult the new quota aggregate.
func TestDKVSQuotaSameHeightLifecycleRuntimeRegression(t *testing.T) {
 owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
 signer := owner.Wallet.GetPubKey().SerializeCompressed()
 payer, err := dkvs.P2TRAddressFromPubKeyBytes(signer, &chaincfg.TestNetParams)
 require.NoError(t, err)
 key := func(suffix string) string {
  k, err := dkvs.PersonalKey(signer, "quota-lifecycle/"+suffix)
  require.NoError(t, err)
  return k
 }
 dbDir := t.TempDir()
 raw := indexerdb.NewKVDB(dbDir)
 require.NotNil(t, raw)
 t.Cleanup(func() { if raw != nil { require.NoError(t, raw.Close()) } })
 probe := &dkvsLifecycleProbe{}
 db := &dkvsLifecycleCountingDB{KVDB: raw, probe: probe}
 f := newBoundRPCFixtureDB(t, db)
 verifier := dkvs.LocalCacheAutopayFeeVerifier{
  AllowFreeLocal: true,
  AutopayFeeVerifier: dkvs.AutopayFeeVerifier{
   StateProvider: dkvsLifecycleAutopayState{payer: payer, height: &f.height},
   Contract: releaseReviewAutopay().PoolContract,
   FullRecordFeePerBlock: "1", AddressParams: &chaincfg.TestNetParams,
  },
 }
 var _ dkvs.IndexedFeeCapacityVerifier = verifier
 policy := dkvs.DefaultFreeLocalCachePolicy()
 policy.MaxRecordsPerSigner, policy.MaxTotalRecords = 2, 2
 cfg := dkvs.Config{
  EndpointID: f.coreID, AllowFreeLocal: true, FreeLocalCache: policy,
  CurrentHeight: f.height.Load, TipHeight: f.height.Load, FeeVerifier: verifier,
 }
 resetIndexer := func() {
  f.store = dkvs.New(db, cfg)
  f.policy = &dkvs.WalletRPCAdmission{Indexer: f.store, IsCoreNode: f.core.Load, CurrentBinding: f.currentBinding}
 }
 resetIndexer()
 binding := f.bind(t, owner, f.coreID)
 type storedState struct { Hash string; Seq uint64 }
 expected := map[string]storedState{}
 remember := func(r *wire.DKVSRecord) { expected[r.Key] = storedState{Hash: dkvs.RecordHash(r).String(), Seq: r.Seq} }
 remember(binding)
 oracle := func(label string, wantPaid, wantFreeActive, wantFreePhysical int) map[string]storedState {
  t.Helper()
  actual := map[string]storedState{}
  paid, freeActive, freePhysical := 0, 0, 0
  prefix := []byte(dkvsLifecycleRecordPrefix)
  require.NoError(t, raw.BatchReadV2(prefix, prefix, false, func(_, value []byte) error {
   r, err := dkvs.UnmarshalRecord(value)
   if err != nil { return err }
   actual[r.Key] = storedState{Hash: dkvs.RecordHash(r).String(), Seq: r.Seq}
   require.False(t, dkvs.IsTombstone(r.Flags), "physical delete must not leave a counted tombstone")
   proof, proofErr := dkvs.ParseFeeProof(r.FeeProof)
   if proofErr == nil && (proof.Mode == dkvs.FeeModeAutopay || proof.Mode == dkvs.FeeModeFreeLocal) {
    require.NoError(t, dkvs.VerifySignature(r))
    gotSigner, signerErr := dkvs.RecordSignerPubKey(r)
    require.NoError(t, signerErr)
    require.Equal(t, signer, gotSigner)
    if proof.Mode == dkvs.FeeModeAutopay {
     require.Equal(t, releaseReviewAutopay().PoolContract, proof.PoolContract)
     require.Zero(t, r.TTL)
     if !dkvs.IsExpired(r, f.height.Load()) { paid++ }
    } else {
     freePhysical++
     if !dkvs.IsExpired(r, f.height.Load()) { freeActive++ }
    }
   }
   return nil
  }))
  t.Logf("dkvs-lifecycle-oracle label=%s height=%d paid=%d free_active=%d free_physical=%d total_physical=%d",
   label, f.height.Load(), paid, freeActive, freePhysical, len(actual))
  require.Equal(t, expected, actual, "all physical row hashes and sequences must match acknowledged operations")
  require.Equal(t, wantPaid, paid)
  require.Equal(t, wantFreeActive, freeActive)
  require.Equal(t, wantFreePhysical, freePhysical)
  return actual
 }
 measure := func(label string, wantFull int, op func() error) []dkvsLifecycleStat {
  t.Helper()
  probe.take()
  probe.setEnabled(true)
  started := time.Now()
  opErr := op()
  elapsed := time.Since(started)
  probe.setEnabled(false)
  stats := probe.take()
  full, rows := 0, int64(0)
  for _, stat := range stats {
   if stat.Kind == "record-table-full" { full++; rows += stat.Rows }
  }
  evidence, jsonErr := json.Marshal(map[string]any{
   "label": label, "height": f.height.Load(), "full_scans": full, "full_rows": rows,
   "latency_ms": float64(elapsed.Nanoseconds())/1e6, "scan_stats": stats, "operation_error": fmt.Sprint(opErr),
  })
  require.NoError(t, jsonErr)
  t.Logf("dkvs-lifecycle-sample: %s", evidence)
  require.NoError(t, opErr)
  require.Equal(t, wantFull, full, "cold quota initialization is separate; every warm write must have zero full scans")
  if wantFull == 0 { require.Zero(t, rows) }
  return stats
 }
 put := func(label string, wantFull int, op func() (*wire.DKVSRecord, error)) (*wire.DKVSRecord, []dkvsLifecycleStat) {
  t.Helper()
  var ack *wire.DKVSRecord
  stats := measure(label, wantFull, func() error {
   var opErr error
   ack, opErr = op()
   return opErr
  })
  require.NotNil(t, ack)
  require.NoError(t, dkvs.VerifySignature(ack))
  got, err := f.store.Get(ack.Key)
  require.NoError(t, err)
  t.Logf("dkvs-lifecycle-ack label=%s seq=%d ack_hash=%s stored_hash=%s", label, ack.Seq, dkvs.RecordHash(ack).String(), dkvs.RecordHash(got).String())
  require.Equal(t, dkvs.RecordHash(ack), dkvs.RecordHash(got))
  require.Equal(t, ack.Seq, got.Seq)
  remember(ack)
  return ack, stats
 }
 paidPut := func(k, value string) (*wire.DKVSRecord, error) {
  return f.client.PutSignedRecordWithAutopay(owner.Wallet, k, []byte(value),
   dkvs.RecordOptions{IssueHeight: f.height.Load()}, releaseReviewAutopay())
 }
 freePut := func(k, value string, ttl uint64) (*wire.DKVSRecord, error) {
  return f.client.PutSignedRecordFreeLocal(owner.Wallet, k, []byte(value),
   dkvs.RecordOptions{IssueHeight: f.height.Load(), TTL: ttl})
 }
 deny := func(label, k string, op func() (*wire.DKVSRecord, error)) {
  t.Helper()
  var opErr error
  measure(label, 0, func() error { _, opErr = op(); return nil })
  require.Error(t, opErr)
  require.Equal(t, dkvs.ErrorCodeQuotaExceeded, dkvs.ErrorCodeOf(opErr))
  _, err := f.store.Get(k)
  require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
  t.Logf("dkvs-lifecycle-denied label=%s error=%v", label, opErr)
 }
 remove := func(label, k string) {
  t.Helper()
  var ack *wire.DKVSRecord
  measure(label, 0, func() error {
   var err error
   ack, err = f.client.DeleteCurrentRecord(owner.Wallet, k, f.height.Load())
   return err
  })
  require.NotNil(t, ack)
  _, err := f.store.Get(k)
  require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
  delete(expected, k)
  t.Logf("dkvs-lifecycle-delete label=%s seq=%d key=%s", label, ack.Seq, k)
 }
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
 rpcKey, p2pKey := key("paid-rpc"), key("paid-p2p")
 sourcePut := func(value string) *wire.DKVSRecord {
  source.height.Store(f.height.Load())
  record, err := source.client.PutSignedRecordWithAutopay(owner.Wallet, p2pKey, []byte(value),
   dkvs.RecordOptions{IssueHeight: f.height.Load()}, releaseReviewAutopay())
  require.NoError(t, err)
  return record
 }
 accept := func(record *wire.DKVSRecord) (*wire.DKVSRecord, error) {
  warnings = nil
  receiver.OnNotify(dkvsp2p.NotifyForRecord(record))
  if len(warnings) != 0 { return record, fmt.Errorf("P2P warnings: %s", strings.Join(warnings, "; ")) }
  return record, nil
 }
 for _, k := range []string{rpcKey, key("paid-seed-a"), key("paid-seed-b")} {
  ack, err := paidPut(k, "initial")
  require.NoError(t, err)
  remember(ack)
 }
 incoming := sourcePut("initial")
 _, err = accept(incoming)
 require.NoError(t, err)
 remember(incoming)
 oracle("seeded-exact-capacity", 4, 0, 0)
 for _, height := range []uint64{100, 101, 102} {
  f.height.Store(height)
  put(fmt.Sprintf("rpc-height-%d", height), 0, func() (*wire.DKVSRecord, error) { return paidPut(rpcKey, fmt.Sprintf("rpc-%d", height)) })
  incoming = sourcePut(fmt.Sprintf("p2p-%d", height))
  put(fmt.Sprintf("p2p-height-%d", height), 0, func() (*wire.DKVSRecord, error) { return accept(incoming) })
  oracle("height-advanced", 4, 0, 0)
 }
 paidOverflow := key("paid-overflow")
 deny("paid-capacity-still-four", paidOverflow, func() (*wire.DKVSRecord, error) { return paidPut(paidOverflow, "reject") })

 a, b, c, d, e := key("free-a"), key("free-b"), key("free-c"), key("free-d"), key("free-e")
 original, _ := put("free-a-expiry-104", 0, func() (*wire.DKVSRecord, error) { return freePut(a, "a", 2) })
 put("free-b-expiry-108", 0, func() (*wire.DKVSRecord, error) { return freePut(b, "b", 6) })
 oracle("free-capacity-two", 4, 2, 2)
 deny("free-third-denied", c, func() (*wire.DKVSRecord, error) { return freePut(c, "reject", 10) })
 f.height.Store(103)
 renewed, _ := put("renew-a-expiry-108", 0, func() (*wire.DKVSRecord, error) {
  return f.client.RenewRecord(owner.Wallet, original, dkvs.RecordOptions{IssueHeight: 103, TTL: 5})
 })
 require.Equal(t, original.Seq+1, renewed.Seq)
 require.Equal(t, uint64(108), dkvs.RecordExpiryHeight(renewed))
 f.height.Store(104)
 incoming = sourcePut("old-expiry-passed")
 put("p2p-at-old-free-expiry", 0, func() (*wire.DKVSRecord, error) { return accept(incoming) })
 deny("renewal-must-not-release-capacity-at-old-expiry", c, func() (*wire.DKVSRecord, error) { return freePut(c, "reject", 10) })
 oracle("renewed-expiry-preserved", 4, 2, 2)

 f.height.Store(108)
 put("expiry-releases-before-prune", 0, func() (*wire.DKVSRecord, error) { return freePut(c, "accepted-after-expiry", 10) })
 oracle("logical-expiry-before-physical-prune", 4, 1, 3)
 probe.take()
 probe.setEnabled(true)
 pruned, pruneErr := f.store.PruneExpiredAt(f.height.Load())
 probe.setEnabled(false)
 pruneStats := probe.take()
 t.Logf("dkvs-lifecycle-maintenance height=108 pruned=%d error=%v scan_stats=%+v (separate expiry-index initialization, not a write)", pruned, pruneErr, pruneStats)
 require.NoError(t, pruneErr)
 require.Equal(t, 2, pruned)
 delete(expected, a)
 delete(expected, b)
 oracle("physical-expiry-pruned-once", 4, 1, 1)
 remove("delete-free", c)
 oracle("free-delete-releases-capacity", 4, 0, 0)
 put("free-after-delete", 0, func() (*wire.DKVSRecord, error) { return freePut(d, "free-after-delete", 10) })
 remove("delete-paid", rpcKey)
 oracle("paid-delete-releases-capacity", 3, 1, 1)
 replacement := key("paid-replacement")
 put("paid-after-delete", 0, func() (*wire.DKVSRecord, error) { return paidPut(replacement, "paid-after-delete") })
 beforeReopen := oracle("before-real-db-reopen", 4, 1, 1)
 require.Len(t, beforeReopen, 6, "binding + four paid + one free")

 // No metadata rewrite, truncation or cache carry-over. Reopen the same Pebble
 // files and make a fresh Indexer while retaining only the fixture's chain state.
 require.NoError(t, raw.Close())
 raw = indexerdb.NewKVDB(dbDir)
 require.NotNil(t, raw)
 db.KVDB = raw
 resetIndexer()
 receiver.Store = activePeerStore{releaseReviewPeerStore{f.store}}
 require.Equal(t, beforeReopen, oracle("after-real-db-reopen", 4, 1, 1))
 _, coldStats := put("restart-cold-first-write", 1, func() (*wire.DKVSRecord, error) { return paidPut(replacement, "cold") })
 for _, stat := range coldStats {
  if stat.Kind != "record-table-full" { continue }
  require.Equal(t, int64(6), stat.Rows)
  callers := strings.Join(stat.CallerPath, "|")
  require.Contains(t, callers, "ensureQuotaUsageLocked")
  require.NotContains(t, callers, "projectedActiveRecordsLocked")
 }
 put("restart-warm-rpc", 0, func() (*wire.DKVSRecord, error) { return paidPut(replacement, "warm") })
 incoming = sourcePut("after-restart")
 put("restart-warm-p2p", 0, func() (*wire.DKVSRecord, error) { return accept(incoming) })
 put("restart-second-free", 0, func() (*wire.DKVSRecord, error) { return freePut(e, "second-free", 10) })
 deny("restart-third-free-denied", c, func() (*wire.DKVSRecord, error) { return freePut(c, "reject", 10) })
 deny("restart-fifth-paid-denied", paidOverflow, func() (*wire.DKVSRecord, error) { return paidPut(paidOverflow, "reject") })
 oracle("final-capacity-correct", 4, 2, 2)
 require.Zero(t, f.unguarded.Load())
}

GOEOF
"/usr/local/go/bin/gofmt" -w "$work/dkvs_quota_same_height_lifecycle_runtime_regression_test.go"
/usr/bin/python3 - "$SDK" "$work" <<'PYEOF'
import json, pathlib, sys
sdk, work = map(pathlib.Path, sys.argv[1:])
name = "dkvs_quota_same_height_lifecycle_runtime_regression_test.go"
replace = {str(sdk / "e2e" / name): str(work / name)}
(work / "lifecycle-overlay.json").write_text(json.dumps({"Replace": replace}))
print("overlay_change=one temporary SDK cross-layer regression file only; no production override")
PYEOF
for file in "$work/dkvs_quota_same_height_lifecycle_runtime_regression_test.go" "$work/lifecycle-overlay.json"; do
 print "overlay_sha256=$(/usr/bin/shasum -a 256 "$file" | /usr/bin/awk '{print $1}') path=$file"
done
cd "$SDK"
discovery="$("$GO_BIN" list -overlay="$work/lifecycle-overlay.json" -f '{{.Dir}}|{{join .TestGoFiles ","}}' ./e2e)"
case "$discovery" in
 "$E2E|"*dkvs_quota_same_height_lifecycle_runtime_regression_test.go*) print "overlay_discovery=expected_package_and_test_file" ;;
 *) print -u2 "unexpected overlay package discovery: $discovery"; exit 93 ;;
esac
print "running only TestDKVSQuotaSameHeightLifecycleRuntimeRegression start_utc=$(/bin/date -u +%FT%TZ)"
"$GO_BIN" test -v -failfast -p=1 -parallel=1 -count=1 -timeout=3m -overlay="$work/lifecycle-overlay.json" -run='^TestDKVSQuotaSameHeightLifecycleRuntimeRegression$' ./e2e
print "TestDKVSQuotaSameHeightLifecycleRuntimeRegression_exit=0 end_utc=$(/bin/date -u +%FT%TZ)"
print "running only two existing TestSDKDKVSReleaseReviewState leaves start_utc=$(/bin/date -u +%FT%TZ)"
"$GO_BIN" test -v -failfast -p=1 -parallel=1 -count=1 -timeout=3m -run='^TestSDKDKVSReleaseReviewState$/(^BasicRenewalAndStaleCASProtection$|^OfflineReplicaAcceptsRecreationAfterNaturalExpiry$)' ./e2e
print "TestSDKDKVSReleaseReviewState_two_leaves_exit=0 end_utc=$(/bin/date -u +%FT%TZ)"
verify_sources
print "dkvs_same_height_lifecycle_exit=0 end_utc=$(/bin/date -u +%FT%TZ)"
