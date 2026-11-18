#!/bin/zsh
set -euo pipefail

GO_BIN="/usr/local/go/bin/go"
REPO="/Users/yingfeng/github/sat20wallet"
SDK="$REPO/sdk"
DKVS="/Users/yingfeng/github/satoshinet/indexer/indexer/dkvs"
BATCH_CAS="$DKVS/batch_cas.go"
INDEXER="$DKVS/indexer.go"
FEE_USAGE="$DKVS/fee_usage.go"
FREE_LOCAL="$DKVS/free_local.go"
MAILBOX_INTERNAL="$DKVS/mailbox_internal.go"
DELETE="$DKVS/delete.go"
PATH_SNAPSHOT_APPLY="$DKVS/path_snapshot_apply.go"
ATOMIC_APPLY="$DKVS/atomic_apply.go"
PAID_RETENTION="$DKVS/paid_retention.go"
QUOTA_USAGE="$DKVS/quota_usage.go"
TOPIC_INTERNAL="$DKVS/topic_internal.go"
RPC_ADMISSION="$DKVS/rpc_admission.go"
BOUND_FIXTURE="$SDK/e2e/dkvs_bound_rpc_e2e_test.go"
FEE_FIXTURE="$SDK/e2e/dkvs_release_review_state_e2e_test.go"
EXPECTED_BATCH_CAS_SHA="24af2c2467f0dee3df929e4fb2d6f5abe35451f2670695eeab01abdea6817b73"
EXPECTED_INDEXER_SHA="120657d8c802f60506701d92d61c55da1524fea85c6ba95c9aef7c09a0db22d6"
EXPECTED_FEE_USAGE_SHA="4e92ee9d634ec46309f4ce1319d47cabf6ba6bf5d3c549a01b0ce2b6f710ab16"
EXPECTED_FREE_LOCAL_SHA="69f4a42baf61d2008e97611938a0418b2bd478d79d257d3cac31c0102d75ebc4"
EXPECTED_MAILBOX_INTERNAL_SHA="5326f68d5e99e41b7ea447eba7b0667e276da8e89833811f99b47bc288a32d1e"
EXPECTED_DELETE_SHA="1e9dbef42295a1c353bd48c3843e0edb9288595d69cb032f6114a8f116cfe381"
EXPECTED_PATH_SNAPSHOT_APPLY_SHA="4edbb33e56744cf78db15b0b220ff998f864bd205b19f98c31a2a23a7e0b82ed"
EXPECTED_ATOMIC_APPLY_SHA="15d6c485a4c46fb546c354a07e157f3372179578a3efa035d4777859d59559c7"
EXPECTED_PAID_RETENTION_SHA="07b4329072e4d223a57d126b9febfa66e0c78c5d0fc19fedad8af8885f6ce7dd"
EXPECTED_QUOTA_USAGE_SHA="46f525b6c46cf3e3b7fb29c42d3f2ef45912e8c4eedc55048a1a0ad22fff0a11"
EXPECTED_TOPIC_INTERNAL_SHA="81c8fba2e5143d4324a057ebec28d8bbf2b3939b538d94c523904277322ea2a7"
EXPECTED_RPC_SHA="6da2a5290c3043e67edfb380439c163181ebc6e78c8acc8225d05b7529d284c6"
EXPECTED_BOUND_FIXTURE_SHA="e435a3daba03f7167945992800598e0c9ffa7a91ea7498020f8e4e053d58a3c8"
EXPECTED_FEE_FIXTURE_SHA="c67b8fba68bcf47b981eac9b432464978277f4716d32c99114dfea829cac76b6"
work="$(/usr/bin/mktemp -d /private/tmp/sat20-dkvs-scan-postpatch.XXXXXX)"

cleanup() {
  exit_status=$?
  if [[ "$exit_status" -eq 0 ]]; then
    /bin/rm -rf "$work"
    print "temporary scan diagnosis cleaned: $work"
  else
    print -u2 "temporary scan diagnosis preserved for inspection: $work"
  fi
}
trap cleanup EXIT

mkdir -p "$work/gocache" "$work/go-tmp" "$work/tmp"
export GOCACHE="$work/gocache"
export GOTMPDIR="$work/go-tmp"
export TMPDIR="$work/tmp"
export GOPROXY=off
export GOSUMDB=off
export GOFLAGS=-mod=readonly
export GOTOOLCHAIN=local
export PATH="/usr/local/go/bin:$PATH"
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
check_sha "$BATCH_CAS" "$EXPECTED_BATCH_CAS_SHA"
check_sha "$INDEXER" "$EXPECTED_INDEXER_SHA"
check_sha "$FEE_USAGE" "$EXPECTED_FEE_USAGE_SHA"
check_sha "$FREE_LOCAL" "$EXPECTED_FREE_LOCAL_SHA"
check_sha "$MAILBOX_INTERNAL" "$EXPECTED_MAILBOX_INTERNAL_SHA"
check_sha "$DELETE" "$EXPECTED_DELETE_SHA"
check_sha "$PATH_SNAPSHOT_APPLY" "$EXPECTED_PATH_SNAPSHOT_APPLY_SHA"
check_sha "$ATOMIC_APPLY" "$EXPECTED_ATOMIC_APPLY_SHA"
check_sha "$PAID_RETENTION" "$EXPECTED_PAID_RETENTION_SHA"
check_sha "$QUOTA_USAGE" "$EXPECTED_QUOTA_USAGE_SHA"
check_sha "$TOPIC_INTERNAL" "$EXPECTED_TOPIC_INTERNAL_SHA"
check_sha "$RPC_ADMISSION" "$EXPECTED_RPC_SHA"
check_sha "$BOUND_FIXTURE" "$EXPECTED_BOUND_FIXTURE_SHA"
check_sha "$FEE_FIXTURE" "$EXPECTED_FEE_FIXTURE_SHA"
print "isolated_tmp=$work"
print "go_version=$("$GO_BIN" version)"
print "measurement=SDK PutSignedRecordFreeLocal -> httptest HTTP -> production WalletRPCAdmission -> Pebble; restart scan/allocation separate from first and hot writes"
cd "$SDK"

cat > "$work/dkvs_scan_baseline_runtime_diagnostic_test.go" <<'GOEOF'
package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
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

type dkvsScanBaselineSample struct {
	Label           string
	Seq             uint64
	LatencyMS       float64
	TotalAllocBytes uint64
	HeapDeltaBeforeGCAfterWriteBytes int64
	HeapObjectsDelta int64
	FullScans       int
	FullRows        int64
	FullScanMS      float64
	PrefixScans     int
	PrefixRows      int64
	PrefixScanMS    float64
	FullScanCallers  []string
	PrefixScanCallers []string
	FullScanStats    []dkvsScanBaselineStat
	PrefixScanStats  []dkvsScanBaselineStat
	AckHash          string
	StoredHash       string
	StoredSeq        uint64
}

func dkvsScanBaselinePercentile(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := int(math.Ceil(percentile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func dkvsScanBaselineSummary(samples []dkvsScanBaselineSample) map[string]any {
	latency := make([]float64, 0, len(samples))
	alloc := make([]float64, 0, len(samples))
	heap := make([]float64, 0, len(samples))
	scan := make([]float64, 0, len(samples))
	fullCalls, fullRows, prefixCalls, prefixRows := 0, int64(0), 0, int64(0)
	for _, sample := range samples {
		latency = append(latency, sample.LatencyMS)
		alloc = append(alloc, float64(sample.TotalAllocBytes))
		heap = append(heap, float64(sample.HeapDeltaBeforeGCAfterWriteBytes))
		scan = append(scan, sample.FullScanMS)
		fullCalls += sample.FullScans
		fullRows += sample.FullRows
		prefixCalls += sample.PrefixScans
		prefixRows += sample.PrefixRows
	}
	return map[string]any{
		"sample_count": len(samples),
		"latency_p50_ms": dkvsScanBaselinePercentile(latency, 0.50),
		"latency_p95_ms": dkvsScanBaselinePercentile(latency, 0.95),
		"total_alloc_p50_bytes": dkvsScanBaselinePercentile(alloc, 0.50),
		"total_alloc_p95_bytes": dkvsScanBaselinePercentile(alloc, 0.95),
		"heap_delta_before_gc_p50_bytes": dkvsScanBaselinePercentile(heap, 0.50),
		"heap_delta_before_gc_p95_bytes": dkvsScanBaselinePercentile(heap, 0.95),
		"full_scan_ms_p50": dkvsScanBaselinePercentile(scan, 0.50),
		"full_scan_ms_p95": dkvsScanBaselinePercentile(scan, 0.95),
		"full_scan_calls_total": fullCalls,
		"full_scan_rows_total": fullRows,
		"record_prefix_calls_total": prefixCalls,
		"record_prefix_rows_total": prefixRows,
	}
}

func dkvsScanBaselineMakeAutopayRecord(t *testing.T, owner *dkvsKeyPathActor, key string, value []byte) *swire.DKVSRecord {
	t.Helper()
	record, err := wallet.NewDKVSSignedRecord(owner.Wallet, key, value, dkvs.RecordOptions{Seq: 1, IssueHeight: 100})
	require.NoError(t, err)
	parsed, err := dkvs.ParseKey(key)
	require.NoError(t, err)
	proof, err := dkvs.NewAutopayFeeProof(key, parsed.Namespace, swire.MaxDKVSRecordSize,
		dkvs.RecordExpiryHeight(record), releaseReviewAutopay().PoolContract, "")
	require.NoError(t, err)
	require.NoError(t, wallet.AttachDKVSFeeProof(record, proof))
	require.NoError(t, wallet.SignDKVSRecord(owner.Wallet, record))
	parsedProof, err := dkvs.ParseFeeProof(record.FeeProof)
	require.NoError(t, err)
	require.Equal(t, dkvs.FeeModeAutopay, parsedProof.Mode)
	return record
}

func dkvsScanBaselineStateHash(t *testing.T, store *dkvs.Indexer, keys []string) string {
	t.Helper()
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	h := sha256.New()
	for _, key := range sorted {
		record, err := store.Get(key)
		require.NoError(t, err)
		_, _ = h.Write([]byte(key))
		_, _ = h.Write([]byte{0})
		recordHash := dkvs.RecordHash(record)
		_, _ = h.Write(recordHash[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func dkvsScanBaselineCountRows(t *testing.T, db indexercommon.KVDB) int64 {
	t.Helper()
	var rows int64
	prefix := []byte(dkvsScanBaselineRecordPrefix)
	require.NoError(t, db.BatchReadV2(prefix, prefix, false, func(_, _ []byte) error {
		rows++
		return nil
	}))
	return rows
}

func dkvsScanBaselineMeasureWrite(t *testing.T, f *boundRPCFixture, owner *dkvsKeyPathActor,
	probe *dkvsScanBaselineProbe, targetKey string, label string, expectedSeq uint64, minimumFullRows int64) dkvsScanBaselineSample {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	ack, err := f.client.PutSignedRecordFreeLocal(owner.Wallet, targetKey,
		[]byte("fixed-target-payload-32-bytes!!"),
		dkvs.RecordOptions{IssueHeight: 100, TTL: 10})
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	require.NoError(t, err, "bound RPC write %s", label)
	require.NotNil(t, ack)
	require.Equal(t, expectedSeq, ack.Seq)

	stats := probe.take()
	sample := dkvsScanBaselineSample{
		Label: label, Seq: ack.Seq,
		LatencyMS: float64(elapsed.Nanoseconds()) / 1e6,
		TotalAllocBytes: after.TotalAlloc - before.TotalAlloc,
		HeapDeltaBeforeGCAfterWriteBytes: int64(after.HeapAlloc) - int64(before.HeapAlloc),
		HeapObjectsDelta: int64(after.HeapObjects) - int64(before.HeapObjects),
	}
	fullCallers, prefixCallers := make(map[string]bool), make(map[string]bool)
	for _, stat := range stats {
		switch stat.Kind {
		case "record-table-full":
			sample.FullScans++
			sample.FullRows += stat.Rows
			sample.FullScanMS += float64(stat.ElapsedNS) / 1e6
			sample.FullScanStats = append(sample.FullScanStats, stat)
			for _, caller := range stat.CallerPath {
				fullCallers[caller] = true
			}
		case "record-prefix":
			sample.PrefixScans++
			sample.PrefixRows += stat.Rows
			sample.PrefixScanMS += float64(stat.ElapsedNS) / 1e6
			sample.PrefixScanStats = append(sample.PrefixScanStats, stat)
			for _, caller := range stat.CallerPath {
				prefixCallers[caller] = true
			}
		}
	}
	for caller := range fullCallers {
		sample.FullScanCallers = append(sample.FullScanCallers, caller)
	}
	for caller := range prefixCallers {
		sample.PrefixScanCallers = append(sample.PrefixScanCallers, caller)
	}
	sort.Strings(sample.FullScanCallers)
	sort.Strings(sample.PrefixScanCallers)

	stored, storedErr := f.store.Get(targetKey)
	if stored != nil {
		sample.StoredHash = dkvs.RecordHash(stored).String()
		sample.StoredSeq = stored.Seq
	}
	sample.AckHash = dkvs.RecordHash(ack).String()
	t.Logf("dkvs-scan-sample: %s", mustDKVSScanBaselineJSON(sample))
	require.NoError(t, storedErr)
	require.NotNil(t, stored)
	require.Equal(t, sample.AckHash, sample.StoredHash, "SDK ACK must match the committed record")
	if label != "cold" {
		require.Equal(t, expectedSeq, stored.Seq)
	}
	if label == "cold" {
		require.Equal(t, 1, sample.FullScans, "first write after DB reopen must rebuild quota state exactly once")
		require.EqualValues(t, minimumFullRows, sample.FullRows, "cold rebuild should read seed plus binding rows")
		foundQuotaRebuild := false
		foundOldProjection := false
		for _, caller := range sample.FullScanCallers {
			if strings.Contains(caller, "ensureQuotaUsageLocked") {
				foundQuotaRebuild = true
			}
			if strings.Contains(caller, "projectedActiveRecordsLocked") {
				foundOldProjection = true
			}
		}
		require.True(t, foundQuotaRebuild, "cold full scan should be the quota aggregate rebuild: %v", sample.FullScanCallers)
		require.False(t, foundOldProjection, "cold full scan must not use legacy projected-record projection: %v", sample.FullScanCallers)
	} else {
		require.Zero(t, sample.FullScans, "warm SDK writes must not traverse the full dkvs:record: table")
		require.Zero(t, sample.FullRows, "warm SDK writes must not enumerate full-table rows")
	}
	return sample
}

func TestDKVSAdmissionScanBaselineRuntimeDiagnostic(t *testing.T) {
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	pubKey := owner.Wallet.GetPubKey().SerializeCompressed()
	for _, seedCount := range []int{100, 1000} {
		t.Run(fmt.Sprintf("unrelated_records_%d", seedCount), func(t *testing.T) {
			dbPath := t.TempDir()
			rawDB := indexerdb.NewKVDB(dbPath)
			require.NotNil(t, rawDB)
			t.Cleanup(func() { require.NoError(t, rawDB.Close()) })
			probe := &dkvsScanBaselineProbe{}
			measuredDB := &dkvsScanBaselineCountingDB{KVDB: rawDB, probe: probe}
			f := newBoundRPCFixtureDB(t, measuredDB)
			binding := f.bind(t, owner, f.coreID)
			require.NotNil(t, binding)

			buildStarted := time.Now()
			mutations := make([]dkvs.CASMutation, 0, seedCount)
			collectionPaths := make(map[string]bool, seedCount)
			seedKeys := make([]string, 0, seedCount)
			for i := 0; i < seedCount; i++ {
				key, err := dkvs.PersonalKey(pubKey, fmt.Sprintf("scan-baseline-unrelated-%05d/value", i))
				require.NoError(t, err)
				collection, err := dkvs.CollectionPathForKey(key)
				require.NoError(t, err)
				require.False(t, collectionPaths[collection], "seed records must use distinct collection paths")
				collectionPaths[collection] = true
				seedKeys = append(seedKeys, key)
				record := dkvsScanBaselineMakeAutopayRecord(t, owner, key, []byte("unrelated-autopay-seed"))
				mutations = append(mutations, dkvs.CASMutation{
					Record: record, Precondition: dkvs.WritePrecondition{ExpectAbsent: true},
				})
			}
			buildElapsed := time.Since(buildStarted)
			probe.setEnabled(false)
			commitStarted := time.Now()
			for start := 0; start < len(mutations); start += dkvs.MaxBatchCASMutations {
				end := start + dkvs.MaxBatchCASMutations
				if end > len(mutations) {
					end = len(mutations)
				}
				_, err := f.store.PutLocalBatchCASResultWithOptions(mutations[start:end], dkvs.BatchCASOptions{
					EndpointID: f.coreID,
					RequestID: fmt.Sprintf("scan-baseline-seed-%d-%d", seedCount, start),
				})
				require.NoError(t, err, "seed batch starting at %d", start)
			}
			commitElapsed := time.Since(commitStarted)
			require.Len(t, collectionPaths, seedCount)
			rowsBeforeTarget := dkvsScanBaselineCountRows(t, rawDB)
			require.EqualValues(t, seedCount+1, rowsBeforeTarget, "seed rows plus current account binding")
			require.EqualValues(t, seedCount, len(mutations))
			seedStateHashBeforeRestart := dkvsScanBaselineStateHash(t, f.store, seedKeys)

			// Separate persisted-state reconstruction from hot write samples.
			runtime.GC()
			var restartMemBefore, restartMemAfter runtime.MemStats
			runtime.ReadMemStats(&restartMemBefore)
			restartStarted := time.Now()
			probe.setEnabled(true)
			require.NoError(t, rawDB.Close())
				rawDB = indexerdb.NewKVDB(dbPath)
			require.NotNil(t, rawDB)
			measuredDB.KVDB = rawDB
			f.store = dkvs.New(measuredDB, dkvs.Config{
				EndpointID: f.coreID, AllowFreeLocal: true,
				FreeLocalCache: dkvs.DefaultFreeLocalCachePolicy(), CurrentHeight: f.height.Load,
				FeeVerifier: releaseReviewFeeVerifier{JSONFeeVerifier: dkvs.JSONFeeVerifier{AllowFreeLocal: true}, height: &f.height},
			})
			require.NotNil(t, f.store)
			f.policy = &dkvs.WalletRPCAdmission{Indexer: f.store, IsCoreNode: f.core.Load, CurrentBinding: f.currentBinding}
			restartElapsed := time.Since(restartStarted)
			runtime.ReadMemStats(&restartMemAfter)
			restartStats := probe.take()
			probe.setEnabled(false)
			seedStateHashAfterRestart := dkvsScanBaselineStateHash(t, f.store, seedKeys)
			require.Equal(t, seedStateHashBeforeRestart, seedStateHashAfterRestart, "seed records must survive DB reopen")
			rowsAfterRestart := dkvsScanBaselineCountRows(t, rawDB)
			require.EqualValues(t, rowsBeforeTarget, rowsAfterRestart, "record rows must be unchanged by DB reopen")
			probe.take() // keep validation reads separate from measured writes
			probe.setEnabled(true)

			targetKey, err := dkvs.PersonalKey(pubKey, "scan-baseline-target/value")
			require.NoError(t, err)
			targetCollection, err := dkvs.CollectionPathForKey(targetKey)
			require.NoError(t, err)
			require.False(t, collectionPaths[targetCollection], "target collection must be independent of seeds")

			cold := dkvsScanBaselineMeasureWrite(t, f, owner, probe, targetKey, "cold", 1, int64(seedCount+1))
			warmup := dkvsScanBaselineMeasureWrite(t, f, owner, probe, targetKey, "warmup", 2, int64(seedCount+2))

			const hotSamples = 20
			hot := make([]dkvsScanBaselineSample, 0, hotSamples)
			for i := 0; i < hotSamples; i++ {
				sample := dkvsScanBaselineMeasureWrite(t, f, owner, probe, targetKey, fmt.Sprintf("hot-%02d", i+1), uint64(i+3), int64(seedCount+2))
				hot = append(hot, sample)
			}
			summary := dkvsScanBaselineSummary(hot)
			report := map[string]any{
				"seed_records": seedCount,
				"same_signer_free_local_seed_count": 0,
				"seed_data_mode": "signed AUTOPAY records, distinct collection paths, committed in bounded PutLocalBatchCAS batches",
				"seed_build_ms": float64(buildElapsed.Nanoseconds()) / 1e6,
				"seed_commit_ms": float64(commitElapsed.Nanoseconds()) / 1e6,
				"db_record_rows_before_target": rowsBeforeTarget,
				"target_key": targetKey,
				"target_collection": targetCollection,
				"target_value_bytes": len([]byte("fixed-target-payload-32-bytes!!")),
				"target_issue_height": 100,
				"restart_reopen_ms": float64(restartElapsed.Nanoseconds()) / 1e6,
				"restart_total_alloc_bytes": restartMemAfter.TotalAlloc - restartMemBefore.TotalAlloc,
				"restart_heap_delta_bytes": int64(restartMemAfter.HeapAlloc) - int64(restartMemBefore.HeapAlloc),
				"restart_scan_stats": restartStats,
				"restart_preserved_seed_state_sha256": seedStateHashAfterRestart,
				"db_record_rows_after_restart": rowsAfterRestart,
				"first_write_after_restart": cold,
				"warmup_sample": warmup,
				"hot_summary": summary,
				"memory_interpretation": "TotalAlloc is cumulative allocation during each request; HeapDeltaBeforeGCAfterWrite is measured after GC-before but before GC-after, not retained or peak memory.",
				"interpretation": "End-to-end SDK->HTTP->production WalletRPCAdmission write latency; BatchRead(V2) callback timing is instrumented scan time inside the Indexer critical section, not an independent lock-wait measurement.",
			}
			t.Logf("dkvs-scan-scale-result: %s", mustDKVSScanBaselineJSON(report))
		})
	}
}

func mustDKVSScanBaselineJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
GOEOF

cat > "$work/overlay.json" <<EOF
{"Replace":{"$SDK/e2e/dkvs_scan_baseline_runtime_diagnostic_test.go":"$work/dkvs_scan_baseline_runtime_diagnostic_test.go"}}
EOF

print "overlay_source_sha256=$(/usr/bin/shasum -a 256 "$work/dkvs_scan_baseline_runtime_diagnostic_test.go" | /usr/bin/awk '{print $1}')"
print "overlay_json_sha256=$(/usr/bin/shasum -a 256 "$work/overlay.json" | /usr/bin/awk '{print $1}')"
print "running only the temporary SDK e2e scan-baseline diagnostic"
"$GO_BIN" test -v -p=1 -parallel=1 -count=1 -timeout=8m -overlay="$work/overlay.json" -run='^TestDKVSAdmissionScanBaselineRuntimeDiagnostic$' ./e2e
print "sdk_e2e_scan_baseline_exit=0"
