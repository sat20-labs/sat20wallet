package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// These fixtures use one root wallet and account 0. Read the real per-module
// row; no protocol or acknowledgement logic is implemented in this helper.
func rgb11LocalModuleForTest(t *testing.T, manager *Manager) *accountLocalKV {
	t.Helper()
	scope := (AccountManagedDataScope{WalletFingerprint: manager.accountProfile.RootFingerprint, AccountIndex: 0, Network: _chain}).ID()
	value, err := manager.readAccountLocal(manager.accountProfile.AccountID, rgb11AccountManagedProviderID, scope)
	require.NoError(t, err)
	require.NotNil(t, value)
	return value
}

func rgb11LocalModulePendingForTest(t *testing.T, manager *Manager) bool {
	value := rgb11LocalModuleForTest(t, manager)
	return value.Generation > value.Confirmed
}

func rgb11LocalModuleHashForTest(t *testing.T, manager *Manager) string {
	value := rgb11LocalModuleForTest(t, manager)
	require.NotNil(t, value.Baseline)
	return accountStateDigest(value.Baseline.Value)
}

// Failure-only diagnostics expose readiness counters and equality checks,
// without printing account keys, recovery envelopes or private RGB material.
func logRGB11LocalRecoveryGate(t *testing.T, manager *Manager) {
	t.Helper()
	manager.mutex.RLock()
	defer manager.mutex.RUnlock()
	client, err := manager.dkvs.primaryClient()
	if err != nil {
		t.Logf("local recovery gate client error: %v", err)
		return
	}
	replica := newDKVSReplicaStore(manager.db)
	state, stateErr := replica.LoadSubscriptionState(client.replicaNamespace)
	entries, outboxErr := replica.LoadOutbox(client.replicaNamespace)
	registered, registeredErr := replica.LoadRegisteredPrefixes(client.replicaNamespace)
	states := make([]string, 0, len(entries))
	for _, entry := range entries {
		states = append(states, entry.OriginDomain+":"+entry.State)
	}
	status, prefixCount, generationCount := "", 0, 0
	if state != nil {
		status, prefixCount, generationCount = state.Status, len(state.Prefixes), len(state.Generations)
	}
	t.Logf("local recovery gate: read=%t write=%t gate_error=%v baseline_error=%v subscription_status=%s prefix_count=%d subscription_generation_count=%d subscription_error=%v registered_count=%d registered_error=%v managed_count=%d outbox=%v outbox_error=%v dirty=%t generation=%d",
		manager.dkvs.scopeReady(manager.dkvs.sessionKey(client, "read")), manager.dkvs.scopeReady(manager.dkvs.sessionKey(client, "write")),
		manager.requireAccountManagedWriteLocked(), manager.requireAccountManagedBaselineLocked(client), status, prefixCount, generationCount, stateErr, len(registered), registeredErr, len(manager.dkvs.managedPaths()), states, outboxErr,
		rgb11LocalModulePendingForTest(t, manager), rgb11LocalModuleForTest(t, manager).Generation)
}

func TestRGB11LocalCommitDuringContinuousDKVSRefresh(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	remote := newRGB11MemoryDKVSHTTP()
	chain := newCoreE2EChain()
	manager := newRGB11AccountScopeRecoveryManager(t, remote, chain)

	entered, release, watching := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var blocking atomic.Bool
	var delayACK atomic.Bool
	var publications atomic.Int32
	firstACK, releaseACK := make(chan struct{}), make(chan struct{})
	nextPublication, releaseNext := make(chan struct{}), make(chan struct{})
	var ackReleaseOnce, nextReleaseOnce sync.Once
	releasePublications := func() {
		ackReleaseOnce.Do(func() { close(releaseACK) })
		nextReleaseOnce.Do(func() { close(releaseNext) })
	}
	defer releasePublications()
	var enterOnce, releaseOnce, watchOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/records/batch-cas" && delayACK.Load() {
			var request DKVSBatchCASRequest
			if err := json.Unmarshal(body, &request); err != nil {
				return nil, err
			}
			accountPublication := false
			for _, mutation := range request.Mutations {
				accountPublication = accountPublication || coreCASKeyRole(mutation.Record.Key) == "managed-module"
			}
			if accountPublication {
				switch publications.Add(1) {
				case 1:
					response, err := remote.SendDKVSPostContext(ctx, path, body)
					if err != nil {
						return nil, err
					}
					close(firstACK)
					select {
					case <-releaseACK:
						return response, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				case 2:
					close(nextPublication)
					select {
					case <-releaseNext:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
			}
		}
		if path == "/v3/dkvs/active/watch" {
			watchOnce.Do(func() { close(watching) })
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if path == "/v3/dkvs/active/sync" && blocking.Load() {
			enterOnce.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	configureRGB11DKVSTestManager(manager, http)
	activateRGB11AccountScopeRecovery(t, manager, coreE2ESenderMnemonic)
	chain.fund(t, manager.GetWallet().GetAddress(), 6)
	asset, err := manager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "LOCAL", Name: "Local commit fixture", Amounts: []uint64{100},
	})
	require.NoError(t, err)
	installRGB11PublicContractFixture(t, manager, remote, asset.ContractID)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))

	require.NoError(t, manager.StartDKVSSync())
	defer manager.StopDKVSSync()
	// Cleanup must unblock transport before Stop waits for the real worker.
	defer unblock()
	defer releasePublications()
	select {
	case <-watching:
	case <-time.After(5 * time.Second):
		code, message, _ := manager.dkvs.lastSyncErrorStatus()
		t.Fatalf("continuous worker did not reach its initial watch: code=%s error=%s", code, message)
	}
	require.Eventually(t, func() bool {
		manager.mutex.RLock()
		defer manager.mutex.RUnlock()
		return manager.requireAccountManagedWriteLocked() == nil
	}, 5*time.Second, 10*time.Millisecond)
	blocking.Store(true)
	manager.dkvs.wakeSync()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("continuous worker did not reach controlled refresh")
	}
	manager.mutex.RLock()
	generation := rgb11LocalModuleForTest(t, manager).Generation
	manager.mutex.RUnlock()
	type result struct {
		invoice *corewallet.ReceiveRequest
		err     error
	}
	done := make(chan result, 1)
	go func() {
		invoice, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", ContractID: asset.ContractID, AmountRaw: "1",
		})
		done <- result{invoice: invoice, err: err}
	}()
	select {
	case got := <-done:
		require.NoError(t, got.err, "an online refresh must not revoke local business eligibility")
		require.NotNil(t, got.invoice)
		require.NotEmpty(t, got.invoice.Invoice)
	case <-time.After(2 * time.Second):
		unblock()
		<-done
		t.Fatal("local RGB commit waited for network backup")
	}
	manager.mutex.RLock()
	dirty, after := rgb11LocalModulePendingForTest(t, manager), rgb11LocalModuleForTest(t, manager).Generation
	manager.mutex.RUnlock()
	require.True(t, dirty)
	require.Greater(t, after, generation)
	unblock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, manager.WaitAccountManagedDataReady(ctx), "the same live worker must back up the committed invoice")

	// Delay ACK N after the remote accepted it, then make a newer local edit.
	// Hold publication N+1 before transport so N cannot be mistaken for a full
	// backup of that newer edit.
	delayACK.Store(true)
	first, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", ContractID: asset.ContractID, AmountRaw: "2"})
	require.NoError(t, err)
	select {
	case <-firstACK:
	case <-time.After(accountManagedBackupQuiet + 5*time.Second):
		t.Fatal("worker did not publish generation N")
	}
	second, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", ContractID: asset.ContractID, AmountRaw: "3"})
	require.NoError(t, err, "a newer local generation must not wait for the older ACK")
	manager.mutex.RLock()
	newestGeneration := rgb11LocalModuleForTest(t, manager).Generation
	manager.mutex.RUnlock()
	ackReleaseOnce.Do(func() { close(releaseACK) })
	select {
	case <-nextPublication:
	case <-time.After(accountManagedBackupQuiet + 5*time.Second):
		t.Fatal("worker did not retain and publish generation N+1")
	}
	manager.mutex.RLock()
	require.True(t, rgb11LocalModulePendingForTest(t, manager), "ACK N cleared newer local dirty state")
	require.Equal(t, newestGeneration, rgb11LocalModuleForTest(t, manager).Generation)
	manager.mutex.RUnlock()
	var durable accountLocalKV
	module := rgb11LocalModuleForTest(t, manager)
	encoded, err := manager.db.Read(accountLocalKey(module.AccountID, module.Provider, module.Scope))
	require.NoError(t, err)
	require.NoError(t, DecodeFromBytes(encoded, &durable))
	require.Greater(t, durable.Generation, durable.Confirmed)
	require.Equal(t, newestGeneration, durable.Generation)
	delayACK.Store(false)
	nextReleaseOnce.Do(func() { close(releaseNext) })
	finalCtx, cancelFinal := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelFinal()
	require.NoError(t, manager.WaitAccountManagedDataReady(finalCtx))
	for _, invoice := range []*corewallet.ReceiveRequest{first, second} {
		restored, err := manager.rgbManager.engine.LoadReceive(invoice.RequestID)
		require.NoError(t, err)
		require.Equal(t, invoice.Invoice, restored.Invoice)
	}
}

var errRGB11LocalCommitInjected = errors.New("injected local recovery commit failure")

type rgb11LocalCommitFailureDB struct {
	indexer.KVDB
	fail                 atomic.Int32
	profileFailures      atomic.Uint32
	profileFailureTarget atomic.Value
	flushFaults          atomic.Uint32
	stageFaults          atomic.Uint32
	flushTarget          atomic.Value
	logFault             func(string, ...any)
}

func (db *rgb11LocalCommitFailureDB) noteProfileFailure(value []byte) {
	target, _ := db.profileFailureTarget.Load().(string)
	var profile accountManagementProfile
	if target != "" && DecodeFromBytes(value, &profile) == nil && profile.StateHash == target {
		db.profileFailures.Add(1)
		return
	}
	var module accountLocalKV
	if target != "" && DecodeFromBytes(value, &module) == nil && module.Baseline != nil && accountStateDigest(module.Baseline.Value) == target {
		db.profileFailures.Add(1)
	}
}

func (db *rgb11LocalCommitFailureDB) Write(key, value []byte) error {
	target, _ := db.flushTarget.Load().(string)
	if db.fail.Load() == 1 && target == "" && (bytes.Equal(key, accountManagementProfileKey()) || strings.Contains(string(key), "account/")) {
		db.noteProfileFailure(value)
		return errRGB11LocalCommitInjected
	}
	return db.KVDB.Write(key, value)
}

func (db *rgb11LocalCommitFailureDB) NewWriteBatch() indexer.WriteBatch {
	return &rgb11LocalCommitFailureBatch{WriteBatch: db.KVDB.NewWriteBatch(), owner: db}
}

func (db *rgb11LocalCommitFailureDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return &rgb11LocalCommitFailureBatch{WriteBatch: db.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch(), owner: db, atomicBatch: true}
}

type rgb11LocalCommitFailureBatch struct {
	indexer.WriteBatch
	owner         *rgb11LocalCommitFailureDB
	atomicBatch   bool
	targetRGB     bool
	targetProfile bool
}

func (b *rgb11LocalCommitFailureBatch) Put(key, value []byte) error {
	target, _ := b.owner.flushTarget.Load().(string)
	if target != "" && strings.HasPrefix(string(key), target) {
		b.targetRGB = true
	}
	if bytes.Equal(key, accountManagementProfileKey()) || strings.Contains(string(key), "account/") {
		b.targetProfile = true
		if b.owner.fail.Load() == 1 && (target == "" || b.atomicBatch && b.targetRGB) {
			b.owner.stageFaults.Add(1)
			b.owner.noteProfileFailure(value)
			return errRGB11LocalCommitInjected
		}
	}
	return b.WriteBatch.Put(key, value)
}

func (b *rgb11LocalCommitFailureBatch) Flush() error {
	target, _ := b.owner.flushTarget.Load().(string)
	selected := target == "" || b.atomicBatch && b.targetRGB && b.targetProfile
	if b.owner.fail.Load() == 2 && selected {
		hit := b.owner.flushFaults.Add(1)
		if b.owner.logFault != nil {
			if hit <= 16 {
				pcs := make([]uintptr, 12)
				count := runtime.Callers(2, pcs)
				frames := runtime.CallersFrames(pcs[:count])
				var callers []string
				for {
					frame, more := frames.Next()
					callers = append(callers, frame.Function)
					if !more {
						break
					}
				}
				b.owner.logFault("G1 injected fault stage=flush atomic=%t hit=%d callers=%v", b.atomicBatch, hit, callers)
			}
		}
		return errRGB11LocalCommitInjected
	}
	return b.WriteBatch.Flush()
}

// Only known public sentinel identities are logged; unknown error text can
// contain private record keys or values and is deliberately not formatted.
func logRGB11CommitError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Log("G1 returned_error=nil")
		return
	}
	type errorNode struct {
		err    error
		parent int
	}
	nodes := []errorNode{{err: err, parent: -1}}
	for i := 0; i < len(nodes) && i < 16; i++ {
		node := nodes[i]
		label := "unrecognized_text_hidden"
		switch node.err {
		case errRGB11LocalCommitInjected:
			label = "errRGB11LocalCommitInjected"
		case ErrDKVSPathNotSynced:
			label = "ErrDKVSPathNotSynced"
		case errAccountSnapshotChanged:
			label = "errAccountSnapshotChanged"
		case context.Canceled:
			label = "context.Canceled"
		case context.DeadlineExceeded:
			label = "context.DeadlineExceeded"
		}
		t.Logf("G1 returned_error node=%d parent=%d type=%T sentinel=%s", i, node.parent, node.err, label)
		switch wrapped := node.err.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range wrapped.Unwrap() {
				if child != nil {
					nodes = append(nodes, errorNode{err: child, parent: i})
				}
			}
		case interface{ Unwrap() error }:
			if child := wrapped.Unwrap(); child != nil {
				nodes = append(nodes, errorNode{err: child, parent: i})
			}
		}
	}
	if len(nodes) > 16 {
		t.Log("G1 returned_error diagnostic limit reached")
	}
}

func TestRGB11LocalRecoveryCommitIsAtomicAndSurvivesRestart(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	for _, mode := range []int32{1, 2} {
		t.Run(map[int32]string{1: "profile-stage", 2: "transaction-flush"}[mode], func(t *testing.T) {
			directory := t.TempDir()
			database := &rgb11LocalCommitFailureDB{KVDB: indexerdb.NewKVDB(directory)}
			if mode == 2 {
				database.logFault = t.Logf
			}
			remote, chain := newRGB11MemoryDKVSHTTP(), newCoreE2EChain()
			manager := coreNewE2EManagerWithDB(t, coreE2EConfig{
				Core: AccountIndexerLocation{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"},
			}, chain, database, "")
			configureRGB11DKVSTestManager(manager, remote)
			client := newRGB11MessageNodeClient(remote)
			manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
			t.Cleanup(func() {
				manager.Close()
				if manager.db != nil {
					_ = manager.db.Close()
					manager.db = nil
				}
			})
			activateRGB11AccountScopeRecovery(t, manager, coreE2ESenderMnemonic)
			chain.fund(t, manager.GetWallet().GetAddress(), 6)
			asset, err := manager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
				Schema: "NIA", Ticker: "ATOMIC", Name: "Atomic commit fixture", Amounts: []uint64{100},
			})
			require.NoError(t, err)
			installRGB11PublicContractFixture(t, manager, remote, asset.ContractID)
			require.NoError(t, manager.SyncAccountManagementState(context.Background()))
			walletID, err := manager.RGB11WalletID()
			require.NoError(t, err)
			before, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
			require.NoError(t, err)
			profileBefore, err := database.Read(accountManagementProfileKey())
			require.NoError(t, err)
			generation := rgb11LocalModuleForTest(t, manager).Generation
			if mode == 1 || mode == 2 {
				// Fault only the real invoice+dirty atomic batch, never receiving
				// or binding transactions that establish its online baseline.
				database.flushTarget.Store("rgb11-engine-" + manager.rgbManager.rgb11ScopeKey() + "-wallet/receive/")
				t.Logf("G1 phase=before-invoice fail_mode=%d flush_faults=%d", mode, database.flushFaults.Load())
			}
			database.fail.Store(mode)
			invoice, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
				Mode: "witness", ContractID: asset.ContractID, AmountRaw: "1",
			})
			if mode == 2 {
				t.Logf("G1 phase=after-invoice fail_mode=%d flush_faults=%d injected_match=%t invoice_nil=%t", mode, database.flushFaults.Load(), errors.Is(err, errRGB11LocalCommitInjected), invoice == nil)
				logRGB11CommitError(t, err)
			}
			require.ErrorIs(t, err, errRGB11LocalCommitInjected)
			if mode == 1 {
				require.Equal(t, uint32(1), database.stageFaults.Load(), "the invoice atomic batch must hit its module marker fault once")
			}
			if mode == 2 {
				require.Equal(t, uint32(1), database.flushFaults.Load(), "the target RGB atomic transaction must hit the fault exactly once")
			}
			require.Nil(t, invoice)
			after, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
			require.NoError(t, err)
			require.Equal(t, before.EngineRecords, after.EngineRecords, "failed dirty persistence must not install receive secrets or advance invoice indexes")
			require.Equal(t, before.ProjectionRecords, after.ProjectionRecords)
			profileAfter, err := database.Read(accountManagementProfileKey())
			require.NoError(t, err)
			require.Equal(t, profileBefore, profileAfter)
			require.Equal(t, generation, rgb11LocalModuleForTest(t, manager).Generation)
			require.False(t, rgb11LocalModulePendingForTest(t, manager))

			database.fail.Store(0)
			invoice, err = manager.CreateRGB11Invoice(RGB11InvoiceRequest{
				Mode: "witness", ContractID: asset.ContractID, AmountRaw: "1",
			})
			require.NoError(t, err)
			require.NotNil(t, invoice)
			require.True(t, rgb11LocalModulePendingForTest(t, manager), "local success must remain dirty until the background ACK")
			committedGeneration := rgb11LocalModuleForTest(t, manager).Generation
			require.Greater(t, committedGeneration, generation)
			manager.Close()
			require.NoError(t, database.Close())
			manager.db = nil
			reopened := indexerdb.NewKVDB(directory)
			defer reopened.Close()
			restarted := &Manager{db: reopened, cfg: manager.cfg}
			require.NoError(t, restarted.loadAccountManagementProfileLocked())
			require.True(t, rgb11LocalModulePendingForTest(t, restarted))
			require.Equal(t, committedGeneration, rgb11LocalModuleForTest(t, restarted).Generation)
			record, err := reopened.Read([]byte("rgb11-engine-" + manager.rgbManager.rgb11ScopeKey() + "-wallet/receive/" + invoice.RequestID))
			require.NoError(t, err)
			receive, err := corewallet.DecodeReceiveRequest(record)
			require.NoError(t, err)
			require.Equal(t, invoice.Invoice, receive.Invoice)
		})
	}
}

func TestRGB11AcknowledgedBackupFailureCompletesLocallyBeforeNewCommit(t *testing.T) {
	testRGB11AcknowledgedBackupLocalCommit(t, true)
}

func TestRGB11AcknowledgedBackupBeforeReceiveAllowsLocalCommit(t *testing.T) {
	testRGB11AcknowledgedBackupLocalCommit(t, false)
}

func testRGB11AcknowledgedBackupLocalCommit(t *testing.T, waitForFailedProfile bool) {
	t.Helper()
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	remote, chain := newRGB11MemoryDKVSHTTP(), newCoreE2EChain()
	manager := newRGB11AccountScopeRecoveryManager(t, remote, chain)
	database := &rgb11LocalCommitFailureDB{KVDB: manager.db}
	manager.db = database
	var failPublication, holdReceiver atomic.Bool
	var originalID atomic.Value
	var originalBody atomic.Value
	var originalAttempts atomic.Int32
	var changedOriginal atomic.Bool
	published := make(chan DKVSBatchCASRequest, 1)
	watching, receiving, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var watchOnce, receiveOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/active/watch" {
			watchOnce.Do(func() { close(watching) })
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if path == "/v3/dkvs/active/sync" && holdReceiver.Load() {
			receiveOnce.Do(func() { close(receiving) })
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		var request DKVSBatchCASRequest
		selected := false
		if path == "/v3/dkvs/records/batch-cas" {
			if err := json.Unmarshal(body, &request); err != nil {
				return nil, err
			}
			for _, mutation := range request.Mutations {
				if coreCASKeyRole(mutation.Record.Key) == "managed-module" && failPublication.CompareAndSwap(true, false) {
					selected = true
					originalID.Store(request.RequestID)
					originalBody.Store(sha256.Sum256(body))
					if waitForFailedProfile {
						database.profileFailureTarget.Store(accountStateDigest(mutation.Record.Value))
					}
					break
				}
			}
			if id, _ := originalID.Load().(string); id != "" && id == request.RequestID {
				originalAttempts.Add(1)
				expected, _ := originalBody.Load().([32]byte)
				if sha256.Sum256(body) != expected {
					changedOriginal.Store(true)
				}
			}
		}
		response, err := remote.SendDKVSPostContext(ctx, path, body)
		if selected && err == nil {
			if waitForFailedProfile {
				database.fail.Store(1)
			} else {
				holdReceiver.Store(true)
			}
			published <- request
		}
		return response, err
	}
	configureRGB11DKVSTestManager(manager, http)
	activateRGB11AccountScopeRecovery(t, manager, coreE2ESenderMnemonic)
	// Activation schedules the first private backup asynchronously. Keep the
	// already running production worker and wait for its actual confirmation;
	// do not manufacture a new synchronous recovery or a cached ready state.
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer readyCancel()
	require.NoError(t, manager.WaitAccountManagedDataReady(readyCtx), "actual first private backup before ACK test asset setup")
	status := manager.GetAccountManagementStatus()
	t.Logf("ACK fixture first backup: configured=%t dirty=%t pending=%d write_allowed=%t", status.RecoveryConfigured, status.ManagedDataDirty, status.PendingChanges, status.DKVSWriteAllowed)
	chain.fund(t, manager.GetWallet().GetAddress(), 6)
	asset, err := manager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "ACKLOCAL", Name: "ACK local completion fixture", Amounts: []uint64{100},
	})
	require.NoError(t, err)
	installRGB11PublicContractFixture(t, manager, remote, asset.ContractID)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	require.NoError(t, manager.StartDKVSSync())
	defer manager.StopDKVSSync()
	defer unblock()
	defer database.fail.Store(0)
	select {
	case <-watching:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach watch")
	}
	manager.mutex.RLock()
	baseHash := rgb11LocalModuleHashForTest(t, manager)
	manager.mutex.RUnlock()
	failPublication.Store(true)
	first, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", ContractID: asset.ContractID, AmountRaw: "1"})
	require.NoError(t, err)
	var request DKVSBatchCASRequest
	select {
	case request = <-published:
	case <-time.After(accountManagedBackupQuiet + 5*time.Second):
		t.Fatal("worker did not publish original account request")
	}
	stateKey, targetHash := "", ""
	var targetSeq uint64
	for _, mutation := range request.Mutations {
		if coreCASKeyRole(mutation.Record.Key) == "managed-module" {
			stateKey, targetHash, targetSeq = mutation.Record.Key, accountStateDigest(mutation.Record.Value), mutation.Record.Seq
		}
	}
	require.NotEmpty(t, stateKey)
	require.True(t, baseHash != targetHash)
	client, err := manager.dkvs.primaryClient()
	require.NoError(t, err)
	walletID, err := manager.RGB11WalletID()
	require.NoError(t, err)
	if waitForFailedProfile {
		// The exact ACK is durable, but confirming its watermark locally failed.
		// Live business N is still present; another invoice must not bypass failure.
		require.Eventually(t, func() bool {
			entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(client.replicaNamespace)
			if err != nil || database.profileFailures.Load() == 0 || !errors.Is(manager.accountDBFailure(), ErrAccountDBStopped) {
				return false
			}
			for _, entry := range entries {
				if entry.RequestID == request.RequestID && entry.State == DKVSOutboxAcknowledged {
					return true
				}
			}
			return false
		}, 5*time.Second, 10*time.Millisecond)
		manager.mutex.RLock()
		unchanged := rgb11LocalModuleHashForTest(t, manager) == baseHash && rgb11LocalModulePendingForTest(t, manager)
		generation := rgb11LocalModuleForTest(t, manager).Generation
		manager.mutex.RUnlock()
		require.True(t, unchanged)
		before, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
		require.NoError(t, err)
		rejected, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", ContractID: asset.ContractID, AmountRaw: "2"})
		require.ErrorIs(t, err, ErrAccountDBStopped)
		require.Nil(t, rejected)
		after, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
		require.NoError(t, err)
		require.True(t, reflect.DeepEqual(before.EngineRecords, after.EngineRecords) && reflect.DeepEqual(before.ProjectionRecords, after.ProjectionRecords), "rejected invoice changed current local N")
		manager.mutex.RLock()
		sameGeneration := rgb11LocalModuleForTest(t, manager).Generation == generation
		manager.mutex.RUnlock()
		require.True(t, sameGeneration)
		// Restoring storage does not revive the failed Manager. Restart over the
		// same durable DB, then gate the new session's real receive.
		database.fail.Store(0)
		require.ErrorIs(t, manager.StartDKVSSync(), ErrAccountDBStopped)
		service := manager.serverNode
		manager.Close()
		holdReceiver.Store(true)
		manager = coreNewE2EManagerWithDB(t, coreE2EConfig{
			Core: AccountIndexerLocation{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"},
		}, chain, database, "", http)
		t.Cleanup(manager.Close)
		configureRGB11DKVSTestManager(manager, http)
		useRGB11TestCoreAuthority(t, manager)
		manager.serverNode = service
		_, err = manager.UnlockWallet("password")
		require.NoError(t, err)
		client, err = manager.dkvs.primaryClient()
		require.NoError(t, err)
		restartedWalletID, err := manager.RGB11WalletID()
		require.NoError(t, err)
		require.Equal(t, walletID, restartedWalletID)
	}
	select {
	case <-receiving:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach controlled receive")
	}
	if waitForFailedProfile {
		// The restarted session must complete real synchronization before a new
		// invoice; its durable ACK cannot manufacture receive qualification.
		generation := rgb11LocalModuleForTest(t, manager).Generation
		before, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
		require.NoError(t, err)
		rejected, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", ContractID: asset.ContractID, AmountRaw: "2"})
		require.ErrorIs(t, err, ErrDKVSPathNotSynced, "failed sync must recover successfully before another invoice")
		require.Nil(t, rejected)
		after, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
		require.NoError(t, err)
		require.Equal(t, before.EngineRecords, after.EngineRecords)
		require.Equal(t, before.ProjectionRecords, after.ProjectionRecords)
		require.Equal(t, generation, rgb11LocalModuleForTest(t, manager).Generation)
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, manager.SyncAccountManagementState(ctx))
		deadline, _ := ctx.Deadline()
		require.Eventually(t, func() bool {
			entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(client.replicaNamespace)
			if err != nil {
				return false
			}
			for _, entry := range entries {
				if entry.RequestID == request.RequestID {
					return false
				}
			}
			return true
		}, time.Until(deadline), 10*time.Millisecond, "the existing worker did not finish the acknowledged request within the original sync budget")
	}
	manager.mutex.RLock()
	confirmed := rgb11LocalModuleHashForTest(t, manager) == targetHash
	confirmedGeneration := rgb11LocalModuleForTest(t, manager).Generation
	manager.mutex.RUnlock()
	require.True(t, confirmed, "verified ACK did not persist exact server watermark N after local completion")
	module := rgb11LocalModuleForTest(t, manager)
	encoded, err := manager.db.Read(accountLocalKey(module.AccountID, module.Provider, module.Scope))
	require.NoError(t, err)
	var durable accountLocalKV
	require.NoError(t, DecodeFromBytes(encoded, &durable))
	require.NotNil(t, durable.Baseline)
	require.Equal(t, targetSeq, durable.Baseline.Seq, "ACK completion changed the accepted remote sequence")
	require.Equal(t, targetHash, accountStateDigest(durable.Baseline.Value))
	received, err := newDKVSReplicaStore(manager.db).ReadSnapshot(client.replicaNamespace, []string{stateKey})
	require.NoError(t, err)
	require.NotNil(t, received[stateKey])
	if waitForFailedProfile {
		require.Equal(t, targetSeq, received[stateKey].Seq, "successful synchronization advanced the original request twice")
		require.Equal(t, targetHash, accountStateDigest(received[stateKey].Value), "successful synchronization did not receive exact N")
	} else {
		require.Equal(t, baseHash, accountStateDigest(received[stateKey].Value), "received O was fabricated from the ACK")
	}
	entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(client.replicaNamespace)
	require.NoError(t, err)
	for _, entry := range entries {
		require.True(t, entry.RequestID != request.RequestID, "completed exact ACK was not independently cleaned")
	}
	require.Equal(t, int32(1), originalAttempts.Load(), "durable ACK was resubmitted")
	require.False(t, changedOriginal.Load(), "original signed request changed")
	// A normal delayed receive keeps its old healthy qualification. The failed
	// branch reaches this point only after real successful synchronization.
	done := make(chan error, 1)
	var second *corewallet.ReceiveRequest
	go func() {
		var err error
		second, err = manager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", ContractID: asset.ContractID, AmountRaw: "2"})
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		unblock()
		<-done
		t.Fatal("qualified new local commit waited for network backup")
	}
	require.NotNil(t, second)
	manager.mutex.RLock()
	preserved := rgb11LocalModuleHashForTest(t, manager) == targetHash && rgb11LocalModulePendingForTest(t, manager) && rgb11LocalModuleForTest(t, manager).Generation > confirmedGeneration
	manager.mutex.RUnlock()
	require.True(t, preserved, "old ACK lost the newer local generation")
	unblock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, manager.WaitAccountManagedDataReady(ctx))
	for _, invoice := range []*corewallet.ReceiveRequest{first, second} {
		receive, err := manager.rgbManager.engine.LoadReceive(invoice.RequestID)
		require.NoError(t, err)
		require.True(t, invoice.Invoice == receive.Invoice, "local invoice N or newer invoice was lost")
	}
	current, err := client.GetRecordDirect(stateKey)
	require.NoError(t, err)
	require.GreaterOrEqual(t, current.Seq, targetSeq, "remote sequence rolled back behind accepted N")
	require.Equal(t, int32(1), originalAttempts.Load(), "original request replayed a business action")
	require.False(t, changedOriginal.Load())
}

// Pause local ACK publication, let another device publish from that committed
// target, and receive the newer value before finishing the original request.
func TestAccountManagedACKFinalizesAfterRemoteAdvance(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	for _, stateOnly := range []bool{true, false} {
		for _, newerLocal := range []bool{false, true} {
			t.Run(fmt.Sprintf("wallet-module-%t/newer-local-%t", stateOnly, newerLocal), func(t *testing.T) {
				remote, chain := newRGB11MemoryDKVSHTTP(), newCoreE2EChain()
				manager := newRGB11AccountScopeRecoveryManager(t, remote, chain)
				database := &rgb11LocalCommitFailureDB{KVDB: manager.db}
				manager.db = database
				var pause atomic.Bool
				var targetKey string
				accepted, release := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				defer unblock()
				defer database.fail.Store(0)
				http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
				http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
					response, err := remote.SendDKVSPostContext(ctx, path, body)
					if err != nil || path != "/v3/dkvs/records/batch-cas" {
						return response, err
					}
					var request DKVSBatchCASRequest
					if err := json.Unmarshal(body, &request); err != nil {
						return nil, err
					}
					for _, mutation := range request.Mutations {
						if mutation.Record.Key == targetKey && pause.CompareAndSwap(true, false) {
							database.profileFailureTarget.Store(accountStateDigest(mutation.Record.Value))
							close(accepted)
							select {
							case <-release:
							case <-ctx.Done():
								return nil, ctx.Err()
							}
							database.fail.Store(1)
						}
					}
					return response, nil
				}
				configureRGB11DKVSTestManager(manager, http)
				pkg, auth, secret := activateRGB11AccountScopeRecovery(t, manager, coreE2ESenderMnemonic)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				require.NoError(t, manager.WaitAccountManagedDataReady(ctx))
				chain.fund(t, manager.GetWallet().GetAddress(), 6)
				asset, err := manager.IssueRGB11Asset(ctx, RGB11IssueRequest{Schema: "NIA", Ticker: "ACKADV", Name: "ACK advance fixture", Amounts: []uint64{100}})
				require.NoError(t, err)
				installRGB11PublicContractFixture(t, manager, remote, asset.ContractID)
				require.NoError(t, manager.SyncAccountManagementState(ctx))
				accountID := manager.accountProfile.AccountID
				provider, scope := rgb11AccountManagedProviderID, (AccountManagedDataScope{WalletFingerprint: manager.accountProfile.RootFingerprint, AccountIndex: 0, Network: _chain}).ID()
				if stateOnly {
					provider, scope = "wallets", AccountManagedDataGlobalScope
				}
				entry, err := accountEntry(accountID, provider, scope)
				require.NoError(t, err)
				targetKey = entry.Key
				readLocal := func(m *Manager) *accountLocalKV {
					t.Helper()
					m.mutex.RLock()
					defer m.mutex.RUnlock()
					value, err := m.readAccountLocal(accountID, provider, scope)
					require.NoError(t, err)
					require.NotNil(t, value)
					return value
				}
				database.profileFailureTarget.Store(manager.accountProfile.StateHash)
				edit := func(m *Manager, label string) *corewallet.ReceiveRequest {
					t.Helper()
					if stateOnly {
						require.NoError(t, m.UpdateWalletName(m.GetAccountManagementStatus().RootWalletID, label))
						return nil
					}
					invoice, err := m.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", ContractID: asset.ContractID, AmountRaw: label})
					require.NoError(t, err)
					return invoice
				}
				publishedInvoice := edit(manager, "1")
				durableN := readLocal(manager)
				require.Greater(t, durableN.Generation, durableN.Confirmed)
				pause.Store(true)
				done := make(chan error, 1)
				go func() { done <- manager.SyncAccountManagementState(ctx); close(done) }()
				defer func() { unblock(); <-done }()
				select {
				case <-accepted:
				case <-ctx.Done():
					t.Fatal("module N never reached its signed ACK boundary")
				}
				var localInvoice *corewallet.ReceiveRequest
				if newerLocal {
					localInvoice = edit(manager, "2")
				}
				localGeneration := readLocal(manager).Generation
				unblock()
				syncErr := <-done
				require.Error(t, syncErr)
				client, err := manager.dkvs.primaryClient()
				require.NoError(t, err)
				replica := newDKVSReplicaStore(manager.db)
				entries, err := replica.LoadOutbox(client.replicaNamespace)
				require.NoError(t, err)
				var ack *DKVSBatchOutboxEntry
				for _, candidate := range entries {
					if candidate.OriginKey == targetKey && candidate.State == DKVSOutboxAcknowledged {
						ack = candidate
					}
				}
				require.NotNil(t, ack)
				require.Equal(t, accountKVOutboxDomain, ack.OriginDomain)
				require.Equal(t, durableN.Generation, ack.OriginGeneration)
				mutations, err := ack.DecodeMutations()
				require.NoError(t, err)
				require.Len(t, mutations, 1, "each module owns a single signed CAS")
				targetRecord := mutations[0].Record
				require.Equal(t, targetKey, targetRecord.Key)
				_, err = openAccountPayload(secret, accountID, _chain, entry, targetRecord.Value)
				require.NoError(t, err)
				acceptedN := snapshotRGB11MemoryRecords(remote)
				require.Equal(t, dkvsindexer.RecordHash(targetRecord), dkvsindexer.RecordHash(acceptedN[targetKey]))
				require.Positive(t, database.profileFailures.Load(), "the real atomic receipt write must hit the injected fault")

				peer := newRGB11AccountScopeRecoveryManager(t, remote, chain)
				value, err := peer.LoadAccountManagementStateForRecovery(auth.Location, pkg.Envelope.Locator, secret, coreE2ESenderMnemonic)
				require.NoError(t, err)
				_, err = peer.RestoreAccountManagementState(*value, secret, "password", pkg.Envelope.Locator,
					AccountManagementRestoreOptions{Location: auth.Location, StorageMode: AccountStorageTemporary, RecordTTL: auth.RecordOptions.TTL})
				require.NoError(t, err)
				require.NoError(t, peer.SyncAccountManagementState(ctx), "cold peer must establish an online session before new edits")
				remoteInvoice := edit(peer, "3")
				require.NoError(t, peer.SyncAccountManagementState(ctx))
				acceptedM := snapshotRGB11MemoryRecords(remote)
				remoteRecord := acceptedM[targetKey]
				require.NotNil(t, remoteRecord)
				require.Greater(t, remoteRecord.Seq, targetRecord.Seq)
				require.NotEqual(t, targetRecord.Value, remoteRecord.Value)
				database.fail.Store(0)
				require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, manager.dkvs.managedPaths()))
				observed, err := replica.ReadSnapshot(client.replicaNamespace, []string{targetKey})
				require.NoError(t, err)
				require.Equal(t, remoteRecord.Value, observed[targetKey].Value)
				manager.mutex.Lock()
				err = manager.finishReadyAccountACKsLocked()
				manager.mutex.Unlock()
				require.NoError(t, err)
				current := readLocal(manager)
				require.Equal(t, localGeneration, current.Generation)
				require.GreaterOrEqual(t, current.Confirmed, ack.OriginGeneration)
				require.NotNil(t, current.Baseline)
				require.Contains(t, []uint64{targetRecord.Seq, remoteRecord.Seq}, current.Baseline.Seq)
				_, err = openAccountPayload(secret, accountID, _chain, entry, current.Baseline.Value)
				require.NoError(t, err, "receipt keeps an authentic module baseline")
				if newerLocal {
					require.Greater(t, current.Generation, current.Confirmed, "ACK N cleared N+1")
				}
				observedAfter, err := replica.ReadSnapshot(client.replicaNamespace, []string{targetKey})
				require.NoError(t, err)
				require.Equal(t, observed, observedAfter, "ACK must not roll the received M replica back")
				if newerLocal {
					require.ErrorIs(t, manager.SyncAccountManagementState(ctx), dkvsindexer.ErrWriteConflict)
					require.Greater(t, readLocal(manager).Generation, readLocal(manager).Confirmed)
				} else {
					require.NoError(t, manager.SyncAccountManagementState(ctx))
					require.Equal(t, remoteRecord.Value, readLocal(manager).Baseline.Value)
					require.Equal(t, readLocal(manager).Generation, readLocal(manager).Confirmed)
				}
				for _, invoice := range []*corewallet.ReceiveRequest{publishedInvoice, localInvoice} {
					if invoice == nil {
						continue
					}
					stored, err := manager.rgbManager.engine.LoadReceive(invoice.RequestID)
					require.NoError(t, err)
					require.Equal(t, invoice.Invoice, stored.Invoice)
				}
				if !newerLocal && remoteInvoice != nil {
					stored, err := manager.rgbManager.engine.LoadReceive(remoteInvoice.RequestID)
					require.NoError(t, err)
					require.Equal(t, remoteInvoice.Invoice, stored.Invoice)
				}
				serverAfter := snapshotRGB11MemoryRecords(remote)
				require.Equal(t, dkvsindexer.RecordHash(remoteRecord), dkvsindexer.RecordHash(serverAfter[targetKey]), "late ACK must not overwrite server M")
			})
		}
	}
}

func TestRGB11RejectedCompactionSurvivesOlderACKAndRestart(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	directory := t.TempDir()
	remote, chain := newRGB11MemoryDKVSHTTP(), newCoreE2EChain()
	cfg := coreE2EConfig{Core: AccountIndexerLocation{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
	database := &rgb11LocalCommitFailureDB{KVDB: indexerdb.NewKVDB(directory)}
	manager := coreNewE2EManagerWithDB(t, cfg, chain, database, "")
	t.Cleanup(func() {
		manager.Close()
		if manager.db != nil {
			_ = manager.db.Close()
			manager.db = nil
		}
	})
	var pause atomic.Bool
	var gateMu sync.Mutex
	var targetKey, requestN string
	accepted, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	http.post = func(requestCtx context.Context, path string, body []byte) ([]byte, error) {
		if path != "/v3/dkvs/records/batch-cas" {
			return remote.SendDKVSPostContext(requestCtx, path, body)
		}
		var request DKVSBatchCASRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		gateMu.Lock()
		matches := false
		for _, mutation := range request.Mutations {
			matches = matches || mutation.Record != nil && targetKey != "" && mutation.Record.Key == targetKey
		}
		holdACK := matches && pause.CompareAndSwap(true, false)
		if holdACK {
			requestN = request.RequestID
		}
		holdNext := matches && requestN != "" && request.RequestID != requestN
		gateMu.Unlock()
		if holdNext {
			// N+1 may be scheduled normally, but cannot acquire its own ACK
			// before the old receipt and cold-restart boundary are inspected.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-requestCtx.Done():
				return nil, requestCtx.Err()
			}
		}
		response, err := remote.SendDKVSPostContext(requestCtx, path, body)
		if err == nil && holdACK {
			var reply dkvsWriteResultData
			if err := decodeDKVSApplicationResponse(response, &reply); err != nil {
				return nil, err
			}
			// FREE_LOCAL acknowledgements are valid for this temporary fixture;
			// use the SDK verifier rather than the paid-Core-only observer.
			if reply.Data == nil || reply.Data.EndpointID != request.EndpointID || reply.Data.RequestID != request.RequestID {
				return nil, errors.New("held ACK response identity mismatch")
			}
			mutations := make([]dkvsindexer.CASMutation, len(request.Mutations))
			for i, item := range request.Mutations {
				if item.Record == nil {
					return nil, dkvsindexer.ErrInvalidRecord
				}
				mutations[i].Record = item.Record
			}
			if err := dkvscore.VerifyWriteResult(mutations, request.RequestID, reply.Data); err != nil {
				return nil, err
			}
			close(accepted)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-requestCtx.Done():
				return nil, requestCtx.Err()
			}
		}
		return response, err
	}
	configureRGB11DKVSTestManager(manager, http)
	client := newRGB11MessageNodeClient(remote)
	manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	activateRGB11AccountScopeRecovery(t, manager, coreE2ESenderMnemonic)
	chain.fund(t, manager.GetWallet().GetAddress(), 6)
	asset, err := manager.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "COMPACT", Name: "Compaction generation fixture", Amounts: []uint64{100},
	})
	require.NoError(t, err)
	installRGB11PublicContractFixture(t, manager, remote, asset.ContractID)
	peer := newRGB11AccountScopeRecoveryManager(t, remote, chain)
	activateRGB11AccountScopeRecovery(t, peer, coreE2EReceiverMnemonic)
	invoice, err := peer.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: asset.ContractID, AmountRaw: "10", WitnessVout: 1,
	})
	require.NoError(t, err)
	prepared, err := manager.PrepareRGB11Transfer(ctx, RGB11SendRequest{Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1})
	require.NoError(t, err)
	store := manager.rgbManager.projectionStore
	pending, err := store.LoadPendingTransfer(prepared.State.TransferID)
	require.NoError(t, err)
	pending.State.Status, pending.State.AckStatus = "rejected", "rejected"
	require.NoError(t, store.SavePendingTransferState(pending))
	// Export the terminal, uncompressed record as N while its ACK is held.
	moduleKey := rgb11LocalModuleForTest(t, manager).Key
	gateMu.Lock()
	targetKey = moduleKey
	gateMu.Unlock()
	pause.Store(true)
	done := make(chan error, 1)
	go func() { done <- manager.SyncAccountManagementState(ctx); close(done) }()
	defer func() {
		unblock()
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("account sync did not stop after releasing the test barrier")
		}
	}()
	select {
	case <-accepted:
	case syncErr := <-done:
		t.Fatalf("terminal backup N exited before its verified ACK boundary: %v", syncErr)
	case <-ctx.Done():
		t.Fatal("terminal backup N never reached its ACK boundary")
	}
	clientN, err := manager.dkvs.primaryClient()
	require.NoError(t, err)
	entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(clientN.replicaNamespace)
	require.NoError(t, err)
	gateMu.Lock()
	frozenID, frozenKey := requestN, targetKey
	gateMu.Unlock()
	var generationN uint64
	for _, entry := range entries {
		if entry.RequestID == frozenID {
			require.Equal(t, frozenKey, entry.OriginKey)
			generationN = entry.OriginGeneration
		}
	}
	require.NotZero(t, generationN, "the held request must identify its actual frozen generation")
	require.Equal(t, generationN, rgb11LocalModuleForTest(t, manager).Generation)
	before, err := store.ExportSnapshot()
	require.NoError(t, err)
	profileBefore, err := manager.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	for _, failure := range []int32{1, 2} {
		database.fail.Store(failure)
		require.ErrorIs(t, store.CompactRejectedTransfers([]string{pending.State.TransferID}), errRGB11LocalCommitInjected)
		afterFailure, err := store.ExportSnapshot()
		require.NoError(t, err)
		require.Equal(t, before, afterFailure, "compaction and generation must both abort")
		profileAfter, err := manager.db.Read(accountManagementProfileKey())
		require.NoError(t, err)
		require.Equal(t, profileBefore, profileAfter)
		require.Equal(t, generationN, rgb11LocalModuleForTest(t, manager).Generation)
	}
	database.fail.Store(0)
	require.NoError(t, store.CompactRejectedTransfers([]string{pending.State.TransferID}))
	generationAfterCompaction := rgb11LocalModuleForTest(t, manager).Generation
	require.Greater(t, generationAfterCompaction, generationN)
	unblock()
	require.Eventually(t, func() bool {
		value := rgb11LocalModuleForTest(t, manager)
		return value.Confirmed == generationN
	}, 2*time.Second, 10*time.Millisecond, "the exact old request receipt must be durable")
	require.True(t, rgb11LocalModulePendingForTest(t, manager), "ACK N must not clear the later compaction")
	require.Equal(t, generationAfterCompaction, rgb11LocalModuleForTest(t, manager).Generation)
	scope := manager.rgbManager.rgb11ScopeKey()
	// Join the foreground call before Close clears the account session.
	// N+1 may be waiting at the gate; only its explicit cancellation is allowed.
	cancel()
	select {
	case syncErr := <-done:
		if syncErr != nil {
			require.ErrorIs(t, syncErr, context.Canceled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("account sync did not join before closing its database")
	}
	manager.Close()
	require.NoError(t, database.Close())
	manager.db = nil
	reopened := indexerdb.NewKVDB(directory)
	restarted := coreNewE2EManagerWithDB(t, cfg, chain, reopened, "")
	configureRGB11DKVSTestManager(restarted, remote)
	useRGB11TestCoreAuthority(t, restarted)
	restarted.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	t.Cleanup(func() { restarted.Close(); _ = reopened.Close() })
	require.True(t, rgb11LocalModulePendingForTest(t, restarted))
	require.Equal(t, generationAfterCompaction, rgb11LocalModuleForTest(t, restarted).Generation)
	_, err = restarted.UnlockWallet("password")
	require.NoError(t, err)
	require.Equal(t, scope, restarted.rgbManager.rgb11ScopeKey())
	compacted, err := restarted.rgbManager.projectionStore.LoadPendingTransfer(pending.State.TransferID)
	require.NoError(t, err)
	require.Equal(t, "rejected", compacted.State.Status)
	require.Empty(t, compacted.RecipientConsignment)
	require.Empty(t, compacted.LocalConsignment)
	require.Empty(t, compacted.SignedTx)
	require.Empty(t, compacted.SignedPSBT)
	require.Empty(t, compacted.ChangeSeals)
	require.Empty(t, compacted.RecipientObjectHash)
	require.Empty(t, compacted.LocalObjectHash)
	recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer recoveryCancel()
	require.NoError(t, restarted.SyncAccountManagementState(recoveryCtx))
	require.False(t, rgb11LocalModulePendingForTest(t, restarted))
	module := rgb11LocalModuleForTest(t, restarted)
	entry, err := accountEntry(module.AccountID, module.Provider, module.Scope)
	require.NoError(t, err)
	remote.mu.Lock()
	dataRecord := cloneRGB11DKVSRecord(remote.records[entry.Key])
	remote.mu.Unlock()
	require.NotNil(t, dataRecord)
	payload, err := openAccountPayload(restarted.accountSecret, module.AccountID, _chain, entry, dataRecord.Value)
	require.NoError(t, err)
	found := false
	for _, encoded := range [][]byte{payload} {
		material, err := rgb11wallet.DecodeRecoveryPackage(encoded)
		require.NoError(t, err)
		snapshot, err := material.WalletSnapshot()
		require.NoError(t, err)
		transfers, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(snapshot.ProjectionRecords)
		require.NoError(t, err)
		for _, transfer := range transfers {
			if transfer.State.TransferID == pending.State.TransferID {
				require.Equal(t, "rejected", transfer.State.Status)
				require.Empty(t, transfer.RecipientConsignment)
				require.Empty(t, transfer.LocalConsignment)
				require.Empty(t, transfer.SignedTx)
				require.Empty(t, transfer.SignedPSBT)
				require.Empty(t, transfer.ChangeSeals)
				require.Empty(t, transfer.RecipientObjectHash)
				require.Empty(t, transfer.LocalObjectHash)
				found = true
			}
		}
	}
	require.True(t, found)
}
