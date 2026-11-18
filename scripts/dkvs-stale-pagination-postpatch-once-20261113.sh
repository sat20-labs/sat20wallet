#!/bin/zsh
set -euo pipefail

GO_BIN="/usr/local/go/bin/go"
REPO="/Users/yingfeng/github/sat20wallet"
SDK="$REPO/sdk"
EXPECTED_SYNC_SHA="6810e2f8631952ce77d67ae41313f2382815a7ddd982e8e2a667decdf424b097"
work="$(/usr/bin/mktemp -d /private/tmp/sat20-dkvs-stale-postpatch.XXXXXX)"
mkdir -p "$work/gocache" "$work/go-tmp" "$work/tmp"

cleanup() {
  exit_status=$?
  if [[ "$exit_status" -eq 0 ]]; then
    /bin/rm -rf "$work"
    print "temporary diagnosis cleaned: $work"
  else
    print -u2 "temporary diagnosis preserved for inspection: $work"
  fi
}
trap cleanup EXIT

export GOCACHE="$work/gocache"
export GOTMPDIR="$work/go-tmp"
export TMPDIR="$work/tmp"
export GOPROXY=off
export GOSUMDB=off
export GOFLAGS=-mod=readonly
export GOTOOLCHAIN=local
export PATH="/usr/local/go/bin:$PATH"
unset SAT20WALLET_E2E_ARTIFACTS || true

OLD_TMP="/private/tmp/sat20-dkvs-stale-postpatch.HZLHYU"
if [[ -d "$OLD_TMP" && ! -L "$OLD_TMP" ]]; then
  /bin/rm -rf "$OLD_TMP"
  print "previous setup directory cleaned: $OLD_TMP"
fi

actual_sync_sha="$(/usr/bin/shasum -a 256 "$SDK/wallet/dkvs_subscription_sync.go" | /usr/bin/awk '{print $1}')"
if [[ "$actual_sync_sha" != "$EXPECTED_SYNC_SHA" ]]; then
  print -u2 "unexpected sync file SHA: $actual_sync_sha"
  exit 90
fi
print "sync_file_sha256=$actual_sync_sha"
print "isolated_tmp=$work"
print "go_version=$("$GO_BIN" version)"
print "running wallet overlay diagnostic plus exact rollback/manager cases"
cd "$SDK"

cat > "$work/dkvs_stale_pagination_runtime_diagnostic_test.go" <<'GOEOF'
package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type stalePaginationDiagnosticHTTP struct {
	*rgb11MemoryDKVSHTTP
	seed *SatsNetDKVSClient
	late *swire.DKVSRecord

	injectOnce sync.Once
	injectionMu sync.Mutex
	injectionErr error

	syncRequests       atomic.Int64
	partialFirstPages  atomic.Int64
	staleContinuations atomic.Int64
	watchRequests      atomic.Int64
}

func (h *stalePaginationDiagnosticHTTP) SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error) {
	if path == "/v3/dkvs/active/watch" {
		h.watchRequests.Add(1)
		return h.rgb11MemoryDKVSHTTP.SendDKVSPostContext(ctx, path, body)
	}
	if path != "/v3/dkvs/active/sync" {
		return h.rgb11MemoryDKVSHTTP.SendDKVSPostContext(ctx, path, body)
	}

	h.syncRequests.Add(1)
	var request dkvsindexer.ActiveSyncRequest
	_ = json.Unmarshal(body, &request)
	raw, err := h.rgb11MemoryDKVSHTTP.SendDKVSPostContext(ctx, path, body)
	if err != nil {
		return raw, err
	}
	var response struct {
		ErrorCode string                 `json:"error_code"`
		Data      *dkvsindexer.ActivePage `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return raw, nil
	}
	if request.Cursor != nil && response.ErrorCode == "DKVS_STALE_GENERATION" {
		h.staleContinuations.Add(1)
	}
	if request.Cursor == nil && response.Data != nil && !response.Data.Complete {
		h.partialFirstPages.Add(1)
		h.injectOnce.Do(func() {
			_, writeErr := h.seed.PutRecord(h.late)
			h.injectionMu.Lock()
			h.injectionErr = writeErr
			h.injectionMu.Unlock()
		})
	}
	return raw, nil
}

func (h *stalePaginationDiagnosticHTTP) getInjectionErr() error {
	h.injectionMu.Lock()
	defer h.injectionMu.Unlock()
	return h.injectionErr
}

func TestDKVSStalePaginationAutoResumesRuntimeDiagnostic(t *testing.T) {
	privateKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	manager := newRGB11MultiDeviceManager(t, privateKey, 712)
	remote := newRGB11MemoryDKVSHTTP()
	diagnostic := &stalePaginationDiagnosticHTTP{rgb11MemoryDKVSHTTP: remote}
	diagnostic.seed = finalDKVSSeedClient(manager, remote)
	configureRGB11DKVSTestManager(manager, diagnostic)

	prefix := ""
	mutations := make([]dkvsindexer.CASMutation, 0, dkvsindexer.ActiveSyncPageRecords+1)
	for i := 0; i <= dkvsindexer.ActiveSyncPageRecords; i++ {
		key := accountTestKey(t, manager, fmt.Sprintf("wallet/stale-pagination-%04d", i))
		record := canonicalTestRecord(t, manager, key, 1, fmt.Sprintf("seed-%04d", i))
		currentPrefix, _, err := dkvsManagedPathForKey(key)
		require.NoError(t, err)
		if prefix == "" {
			prefix = currentPrefix
		}
		require.Equal(t, prefix, currentPrefix, "all seed records must share one active prefix")
		mutations = append(mutations, dkvsindexer.CASMutation{
			Record: record, Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true},
		})
	}
	for start := 0; start < len(mutations); start += 16 {
		end := start + 16
		if end > len(mutations) {
			end = len(mutations)
		}
		_, err := diagnostic.seed.PutRecordBatchCAS(mutations[start:end])
		require.NoError(t, err)
	}

	lateKey := accountTestKey(t, manager, "wallet/stale-pagination-late")
	latePrefix, _, err := dkvsManagedPathForKey(lateKey)
	require.NoError(t, err)
	require.Equal(t, prefix, latePrefix)
	diagnostic.late = canonicalTestRecord(t, manager, lateKey, 1, "late-after-first-page")
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.StartDKVSSync())
	t.Cleanup(manager.StopDKVSSync)

	require.Eventually(t, func() bool {
		code, _, _ := manager.dkvs.lastSyncErrorStatus()
		return code == "DKVS_STALE_GENERATION" && diagnostic.staleContinuations.Load() > 0
	}, 20*time.Second, 25*time.Millisecond, "initial page collection must observe the injected stale continuation")

	injectionErr := diagnostic.getInjectionErr()
	require.NoError(t, injectionErr, "remote update injected after the first partial page")
	require.Greater(t, diagnostic.partialFirstPages.Load(), int64(0))
	staleAt := time.Now()
	manager.dkvs.mu.Lock()
	wakeQueueAtStale := len(manager.dkvs.wake)
	pendingJobsAtStale := len(manager.dkvs.jobs)
	manager.dkvs.mu.Unlock()
	require.Zero(t, wakeQueueAtStale, "no explicit wake may cause the retry")
	require.Zero(t, pendingJobsAtStale, "no background job may wake the worker")

	client, err := manager.GetDKVSClient()
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		state, stateErr := manager.GetDKVSSubscriptionStatus()
		if stateErr != nil || state.Status != string(DKVSSubscriptionReady) {
			return false
		}
		record, readErr := client.GetRecord(lateKey)
		return readErr == nil && string(record.Value) == "late-after-first-page"
	}, 30*time.Second, 40*time.Millisecond, "the background manager must recover without RetryDKVSSync or wakeSync")
	recoveryElapsed := time.Since(staleAt)
	require.GreaterOrEqual(t, recoveryElapsed, 4500*time.Millisecond,
		"recovery should be caused by the existing five-second timed retry, not a wake")
	require.Greater(t, diagnostic.syncRequests.Load(), int64(2), "a fresh active-page collection must run after stale failure")

	require.Eventually(t, func() bool { return diagnostic.watchRequests.Load() > 0 },
		10*time.Second, 25*time.Millisecond, "the manager must resume active watch after automatic recovery")
	manager.dkvs.mu.Lock()
	wakeQueueBeforeWatchUpdate := len(manager.dkvs.wake)
	pendingJobsBeforeWatchUpdate := len(manager.dkvs.jobs)
	manager.dkvs.mu.Unlock()
	require.Zero(t, wakeQueueBeforeWatchUpdate, "no wake may be queued before the watch update")
	require.Zero(t, pendingJobsBeforeWatchUpdate)

	watchKey := accountTestKey(t, manager, "wallet/stale-pagination-watch")
	watchPrefix, _, err := dkvsManagedPathForKey(watchKey)
	require.NoError(t, err)
	require.Equal(t, prefix, watchPrefix)
	watchRecord := canonicalTestRecord(t, manager, watchKey, 1, "delivered-by-watch")
	_, err = diagnostic.seed.PutRecord(watchRecord)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		state, stateErr := manager.GetDKVSSubscriptionStatus()
		if stateErr != nil || state.Status != string(DKVSSubscriptionReady) {
			return false
		}
		record, readErr := client.GetRecord(watchKey)
		return readErr == nil && string(record.Value) == "delivered-by-watch"
	}, 10*time.Second, 25*time.Millisecond, "a later remote update must arrive through the resumed watch")
	require.Zero(t, diagnostic.getWakeQueueLength(manager), "no manual manager wake should have been used")
	t.Logf("stale-pagination-auto-resume: partial_first_pages=%d stale_continuations=%d sync_requests=%d watch_requests=%d recovery_elapsed=%s late_value=%q watch_value=%q",
		diagnostic.partialFirstPages.Load(), diagnostic.staleContinuations.Load(), diagnostic.syncRequests.Load(),
		diagnostic.watchRequests.Load(), recoveryElapsed,
		string(diagnostic.late.Value), string(watchRecord.Value))
}

func (h *stalePaginationDiagnosticHTTP) getWakeQueueLength(manager *Manager) int {
	manager.dkvs.mu.Lock()
	defer manager.dkvs.mu.Unlock()
	return len(manager.dkvs.wake)
}
GOEOF

cat > "$work/overlay.json" <<EOF
{"Replace":{"$SDK/wallet/dkvs_stale_pagination_runtime_diagnostic_test.go":"$work/dkvs_stale_pagination_runtime_diagnostic_test.go"}}
EOF

print "overlay_diagnostic_sha256=$(/usr/bin/shasum -a 256 "$work/dkvs_stale_pagination_runtime_diagnostic_test.go" | /usr/bin/awk '{print $1}')"
print "overlay_json_sha256=$(/usr/bin/shasum -a 256 "$work/overlay.json" | /usr/bin/awk '{print $1}')"

"$GO_BIN" test -p=1 -parallel=1 -count=1 -timeout=5m -overlay="$work/overlay.json" -run='^(TestDKVSStalePaginationAutoResumesRuntimeDiagnostic|TestDKVSCurrentSyncRejectsSourceGenerationRollback|TestDKVSFinalManagedPrefixesUseCurrentGenerationSync)$' ./wallet

print "wallet_overlay_and_rollback_exit=0"
print "running exact existing sdk/e2e ActiveManager and ActiveState cases; isolated node data/runtime and local 20000-29999 port probe range"

"$GO_BIN" test -p=1 -parallel=1 -count=1 -timeout=12m ./e2e -run='^TestSDKDKVSActive(Manager|State)$'

print "sdk_e2e_active_manager_and_state_exit=0"
