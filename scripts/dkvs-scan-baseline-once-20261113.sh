#!/bin/zsh
set -euo pipefail

GO_BIN="/usr/local/go/bin/go"
REPO="/Users/yingfeng/github/sat20wallet"
SDK="$REPO/sdk"
BATCH_CAS="/Users/yingfeng/github/satoshinet/indexer/indexer/dkvs/batch_cas.go"
INDEXER="/Users/yingfeng/github/satoshinet/indexer/indexer/dkvs/indexer.go"
RPC_ADMISSION="/Users/yingfeng/github/satoshinet/indexer/indexer/dkvs/rpc_admission.go"
BOUND_FIXTURE="$SDK/e2e/dkvs_bound_rpc_e2e_test.go"
FEE_FIXTURE="$SDK/e2e/dkvs_release_review_state_e2e_test.go"
EXPECTED_BATCH_CAS_SHA="64a75297924bfe2c0464d0c20f5c774f22ae3e7bdcbc0bea3516a26932a6600d"
EXPECTED_INDEXER_SHA="07b08a17aaa26d9a9c3b8870597bdb6f10db63dc6ebb4e20c91d7e47e27b0de1"
EXPECTED_RPC_SHA="6da2a5290c3043e67edfb380439c163181ebc6e78c8acc8225d05b7529d284c6"
EXPECTED_BOUND_FIXTURE_SHA="e435a3daba03f7167945992800598e0c9ffa7a91ea7498020f8e4e053d58a3c8"
EXPECTED_FEE_FIXTURE_SHA="c67b8fba68bcf47b981eac9b432464978277f4716d32c99114dfea829cac76b6"
work="$(/usr/bin/mktemp -d /private/tmp/sat20-dkvs-scan-baseline.XXXXXX)"

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
check_sha "$RPC_ADMISSION" "$EXPECTED_RPC_SHA"
check_sha "$BOUND_FIXTURE" "$EXPECTED_BOUND_FIXTURE_SHA"
check_sha "$FEE_FIXTURE" "$EXPECTED_FEE_FIXTURE_SHA"
print "isolated_tmp=$work"
print "go_version=$("$GO_BIN" version)"
print "measurement=SDK PutSignedRecordFreeLocal -> httptest HTTP -> production WalletRPCAdmission -> Pebble"
cd "$SDK"

cat > "$work/dkvs_scan_baseline_runtime_diagnostic_test.go" <<'GOEOF'
package e2e

import (
	"bytes"
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
			strings.Contains(frame.Function, "validateBatchStateLocked") {
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
	Callers         []string
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
	callers := make(map[string]bool)
	for _, stat := range stats {
		switch stat.Kind {
		case "record-table-full":
			sample.FullScans++
			sample.FullRows += stat.Rows
			sample.FullScanMS += float64(stat.ElapsedNS) / 1e6
		case "record-prefix":
			sample.PrefixScans++
			sample.PrefixRows += stat.Rows
			sample.PrefixScanMS += float64(stat.ElapsedNS) / 1e6
		}
		for _, caller := range stat.CallerPath {
			callers[caller] = true
		}
	}
	for caller := range callers {
		sample.Callers = append(sample.Callers, caller)
	}
	sort.Strings(sample.Callers)
	require.GreaterOrEqual(t, sample.FullScans, 1, "SDK write must traverse the full dkvs:record: table")
	require.GreaterOrEqual(t, sample.FullRows, minimumFullRows, "full-table callback count must include all seeded rows and the existing target when hot")
	foundProjectedActiveRecords := false
	for _, caller := range sample.Callers {
		if strings.Contains(caller, "projectedActiveRecordsLocked") {
			foundProjectedActiveRecords = true
		}
	}
	require.True(t, foundProjectedActiveRecords, "full scan must be attributed to projectedActiveRecordsLocked: %v", sample.Callers)

	stored, err := f.store.Get(targetKey)
	require.NoError(t, err)
	require.Equal(t, dkvs.RecordHash(ack), dkvs.RecordHash(stored), "SDK ACK must match the committed record")
	if label != "cold" {
		require.Equal(t, expectedSeq, stored.Seq)
	}
	return sample
}

func TestDKVSAdmissionScanBaselineRuntimeDiagnostic(t *testing.T) {
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	pubKey := owner.Wallet.GetPubKey().SerializeCompressed()
	for _, seedCount := range []int{100, 1000} {
		t.Run(fmt.Sprintf("unrelated_records_%d", seedCount), func(t *testing.T) {
			rawDB := indexerdb.NewKVDB(t.TempDir())
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
			for i := 0; i < seedCount; i++ {
				key, err := dkvs.PersonalKey(pubKey, fmt.Sprintf("scan-baseline-unrelated-%05d/value", i))
				require.NoError(t, err)
				collection, err := dkvs.CollectionPathForKey(key)
				require.NoError(t, err)
				require.False(t, collectionPaths[collection], "seed records must use distinct collection paths")
				collectionPaths[collection] = true
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
			probe.setEnabled(true)

			targetKey, err := dkvs.PersonalKey(pubKey, "scan-baseline-target/value")
			require.NoError(t, err)
			targetCollection, err := dkvs.CollectionPathForKey(targetKey)
			require.NoError(t, err)
			require.False(t, collectionPaths[targetCollection], "target collection must be independent of seeds")

			cold := dkvsScanBaselineMeasureWrite(t, f, owner, probe, targetKey, "cold", 1, int64(seedCount+1))
			t.Logf("dkvs-scan-sample: %s", mustDKVSScanBaselineJSON(cold))
			warmup := dkvsScanBaselineMeasureWrite(t, f, owner, probe, targetKey, "warmup", 2, int64(seedCount+2))
			t.Logf("dkvs-scan-sample: %s", mustDKVSScanBaselineJSON(warmup))

			const hotSamples = 20
			hot := make([]dkvsScanBaselineSample, 0, hotSamples)
			for i := 0; i < hotSamples; i++ {
				sample := dkvsScanBaselineMeasureWrite(t, f, owner, probe, targetKey, fmt.Sprintf("hot-%02d", i+1), uint64(i+3), int64(seedCount+2))
				hot = append(hot, sample)
				t.Logf("dkvs-scan-sample: %s", mustDKVSScanBaselineJSON(sample))
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
				"cold_sample": cold,
				"warmup_sample": warmup,
				"hot_summary": summary,
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
