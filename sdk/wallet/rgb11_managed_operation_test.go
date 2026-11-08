package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const rgb11ManagedOperationTestMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

type rgb11RejectDirtyAccountCASHTTP struct {
	*rgb11MemoryDKVSHTTP
	owner            *Manager
	transportFailure bool
	rejectMu         sync.Mutex
	rejected         int
	key              string
}

func (h *rgb11RejectDirtyAccountCASHTTP) rejectIfDirty(path string, body []byte) bool {
	if h == nil || path != "/v3/dkvs/records/batch-cas" || h.owner == nil {
		return false
	}
	var request DKVSBatchCASRequest
	if json.Unmarshal(body, &request) != nil {
		return false
	}
	h.owner.mutex.RLock()
	var values []*accountLocalKV
	if h.owner.accountProfile != nil {
		values, _ = h.owner.listAccountLocal(h.owner.accountProfile.AccountID)
	}
	h.owner.mutex.RUnlock()
	dirty := false
	for _, mutation := range request.Mutations {
		if mutation.Record == nil || h.key != "" && mutation.Record.Key != h.key {
			continue
		}
		for _, value := range values {
			dirty = dirty || value.Key == mutation.Record.Key && value.Generation > value.Confirmed
		}
	}
	if !dirty {
		return false
	}
	h.rejectMu.Lock()
	h.rejected++
	h.rejectMu.Unlock()
	return true
}

func (h *rgb11RejectDirtyAccountCASHTTP) SendDKVSPost(path string, body []byte) ([]byte, error) {
	if h.rejectIfDirty(path, body) {
		if h.transportFailure {
			return nil, errors.Join(errors.New("simulated account data CAS transport failure"), context.DeadlineExceeded)
		}
		return rgb11DKVSResponse(-1, "simulated account data CAS conflict", nil,
			string(dkvsindexer.ErrorCodeWriteConflict), 0)
	}
	return h.rgb11MemoryDKVSHTTP.SendDKVSPost(path, body)
}

func (h *rgb11RejectDirtyAccountCASHTTP) SendDKVSPostContext(ctx context.Context,
	path string, body []byte) ([]byte, error) {
	if h.rejectIfDirty(path, body) {
		if h.transportFailure {
			return nil, errors.Join(errors.New("simulated account data CAS transport failure"), context.DeadlineExceeded)
		}
		return rgb11DKVSResponse(-1, "simulated account data CAS conflict", nil,
			string(dkvsindexer.ErrorCodeWriteConflict), 0)
	}
	return h.rgb11MemoryDKVSHTTP.SendDKVSPostContext(ctx, path, body)
}

func configuredRGB11ManagedOperationTestManager(t *testing.T) *Manager {
	t.Helper()
	manager, _, _, _ := accountTTLNoopFixture(t)
	manager.accountBackgroundWG.Wait()
	return manager
}

func TestRGB11ManagedOperationAggregatesMutationMarker(t *testing.T) {
	manager := &Manager{}
	manager.beginRGB11ManagedOperationState()
	if inOperation, first := manager.noteRGB11ManagedOperationMutation(); !inOperation || !first {
		t.Fatalf("first mutation state=(%v,%v)", inOperation, first)
	}
	if inOperation, first := manager.noteRGB11ManagedOperationMutation(); !inOperation || first {
		t.Fatalf("second mutation state=(%v,%v)", inOperation, first)
	}
	if !manager.endRGB11ManagedOperationState() {
		t.Fatal("operation did not retain its aggregated dirty marker")
	}
	if inOperation, first := manager.noteRGB11ManagedOperationMutation(); inOperation || first {
		t.Fatalf("mutation leaked beyond operation=(%v,%v)", inOperation, first)
	}
}

func TestRGB11ManagedOperationWithoutRecoveryKeepsChainLockFree(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	owner := newAccountManagementAutoTestManager(t)
	if _, err := owner.ImportWallet(rgb11ManagedOperationTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	prepareRGBEndpoint(t, owner, owner.http.(*rgb11MemoryDKVSHTTP))
	manager := owner.rgbManager
	if owner.accountManagedRecoveryConfigured() {
		t.Fatal("local lock-operation group unexpectedly activated account backup")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	observedUnlocked := false
	observedFirstMutation := false
	_, err := runRGB11ManagedOperationWithManager[struct{}](owner, ctx,
		rgb11ManagedOperationRefresh,
		func() (*rgb11Manager, error) { return manager, nil },
		func(current *rgb11Manager) (struct{}, error) {
			inOperation, first := current.Manager.noteRGB11ManagedOperationMutation()
			observedFirstMutation = inOperation && first

			unlock, lockErr := current.lockRGB11ChainRefresh(ctx)
			if lockErr != nil {
				return struct{}{}, lockErr
			}
			unlock()
			observedUnlocked = true
			return struct{}{}, nil
		})
	if err != nil {
		t.Fatalf("operation unexpectedly held a chain-refresh lock: %v", err)
	}
	if !observedUnlocked {
		t.Fatal("operation callback could not take the short local chain-refresh lock")
	}
	if !observedFirstMutation {
		t.Fatal("operation callback did not aggregate its first mutation")
	}
	owner.rgbManagedOperationMu.Lock()
	active := owner.rgbManagedOperationActive
	owner.rgbManagedOperationMu.Unlock()
	if active {
		t.Fatal("managed-operation lock ownership marker leaked after operation")
	}
	if !manager.scopeStates.chainRefresh.TryLock() {
		t.Fatal("operation did not release the chain-refresh lock")
	}
	manager.scopeStates.chainRefresh.Unlock()
	owner.dkvs.mu.Lock()
	deferredSync := owner.dkvs.jobs[accountManagedStateJobID]
	owner.dkvs.mu.Unlock()
	if deferredSync == nil {
		t.Fatal("dirty operation did not schedule the existing account-state sync")
	}
}

func TestRGB11ManagedOperationCommitsOneGenerationBeforeBackup(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager := configuredRGB11ManagedOperationTestManager(t)
	initialGeneration := rgb11LocalModuleForTest(t, manager).Generation
	manager.rgbManager.evidence = newCoreE2EChain()
	_, err := runRGB11ManagedOperation(manager, context.Background(), rgb11ManagedOperationNew,
		func(rgb *rgb11Manager) (struct{}, error) {
			if _, err := rgb.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", AmountRaw: "1", Expiry: time.Now().Add(time.Hour).Unix()}); err != nil {
				return struct{}{}, err
			}
			// Multiple low-level mutation notifications inside one business
			// operation must still produce one managed generation and one final
			// account snapshot.
			rgb.autoBackupRGB11AfterMutation()
			rgb.autoBackupRGB11AfterMutation()
			return struct{}{}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !rgb11LocalModulePendingForTest(t, manager) {
		t.Fatal("local commit must remain dirty until the asynchronous backup is acknowledged")
	}
	if got := rgb11LocalModuleForTest(t, manager).Generation; got != initialGeneration+1 {
		t.Fatalf("managed generation=%d want=%d", got, initialGeneration+1)
	}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rgb11LocalModulePendingForTest(t, manager) {
		t.Fatal("successful backup did not clear its acknowledged generation")
	}
	payloads, err := manager.currentAccountManagedProviderPayloads(rgb11AccountManagedProviderID)
	if err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 1 {
		t.Fatalf("ACK-confirmed RGB11 recovery payloads=%d want=1", len(payloads))
	}
}

func TestAccountManagedSyncPublishesActiveRGB11TransitionToStableClosure(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager := configuredRGB11ManagedOperationTestManager(t)
	initialGeneration := manager.accountProfile.ManagedDataGeneration
	saveRGB11StatusTransfer(t, manager, "active-managed-sync", "prepared")
	if err := manager.markAccountManagedDataDirtyDeferred(rgb11AccountManagedProviderID); err != nil {
		t.Fatal(err)
	}
	generation := manager.accountProfile.ManagedDataGeneration
	if generation <= initialGeneration {
		t.Fatalf("deferred recovery mutation did not advance generation: before=%d after=%d",
			initialGeneration, generation)
	}

	active, err := manager.hasAccountManagedRGB11Transition()
	if err != nil || !active {
		t.Fatalf("pending transition active=%t error=%v", active, err)
	}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatalf("stable closure sync rejected recoverable pending work: %v", err)
	}
	if manager.accountProfile.ManagedDataDirty ||
		manager.accountProfile.ManagedDataGeneration != generation {
		t.Fatalf("stable sync changed marker generation or left dirty state: dirty=%t generation=%d marker=%d",
			manager.accountProfile.ManagedDataDirty, manager.accountProfile.ManagedDataGeneration, generation)
	}
	payloads, err := manager.currentAccountManagedProviderPayloads(rgb11AccountManagedProviderID)
	if err != nil || len(payloads) != 1 {
		t.Fatalf("stable recovery payload count=%d err=%v", len(payloads), err)
	}
	catalog, err := manager.accountManagedDataCatalog()
	if err != nil || len(catalog.Scopes) != 1 {
		t.Fatalf("stable recovery catalog scopes=%d err=%v", len(catalog.Scopes), err)
	}
	payload, ok := payloads[catalog.Scopes[0].ID()]
	if !ok {
		t.Fatalf("stable recovery payload omitted scope %q", catalog.Scopes[0].ID())
	}
	recovery, err := rgb11wallet.DecodeRecoveryPackage(payload.Payload)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range recovery.ProjectionRecords {
		if record.Key == "pending-active-managed-sync" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("stable recovery package omitted the active pending operation")
	}
}

func TestRGB11NewInvoicePersistsLocallyBeforeAsynchronousBackup(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	for _, tc := range []struct {
		name             string
		transportFailure bool
		wantOutbox       bool
	}{
		{name: "cas-conflict", wantOutbox: true},
		{name: "transport-unconfirmed", transportFailure: true, wantOutbox: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := configuredRGB11ManagedOperationTestManager(t)
			// A receive request makes this a nonempty recovery scope. Keep real
			// controlled Bitcoin evidence so publication reaches the injected CAS.
			manager.rgbManager.evidence = newCoreE2EChain()
			store, err := manager.accountDKVSStore()
			if err != nil {
				t.Fatal(err)
			}
			dataKey := rgb11LocalModuleForTest(t, manager).Key
			before, err := store.GetAuthoritative(dataKey)
			if err != nil {
				t.Fatal(err)
			}

			remote, ok := manager.http.(*rgb11MemoryDKVSHTTP)
			if !ok || manager.dkvs == nil {
				t.Fatal("managed account test did not use the expected local DKVS transport")
			}
			fault := &rgb11RejectDirtyAccountCASHTTP{
				rgb11MemoryDKVSHTTP: remote,
				owner:               manager,
				transportFailure:    tc.transportFailure,
				key:                 dataKey,
			}
			manager.http = fault
			manager.dkvs.mu.Lock()
			for _, client := range manager.dkvs.clients {
				client.Http = fault
			}
			manager.dkvs.mu.Unlock()

			receive, createErr := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
				Mode: "witness", TransportMode: "out-of-band",
				AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
			})
			if createErr != nil || receive == nil {
				t.Fatalf("local invoice must not wait for its backup: returned=%t error=%v", receive != nil, createErr)
			}
			fault.rejectMu.Lock()
			beforeSyncRejected := fault.rejected
			fault.rejectMu.Unlock()
			if beforeSyncRejected != 0 {
				t.Fatal("local invoice synchronously attempted its backup")
			}
			if err := manager.SyncAccountManagementState(context.Background()); err == nil {
				t.Fatal("explicit backup did not report the injected failure")
			}
			fault.rejectMu.Lock()
			rejected := fault.rejected
			fault.rejectMu.Unlock()
			if rejected == 0 {
				t.Fatalf("invoice test did not reach dirty account-data CAS: create error=%v", createErr)
			}
			if count := countRGB11Receives(t, manager.db); count != 1 {
				t.Fatalf("failed publication discarded the local resumable receive request: count=%d", count)
			}
			if !rgb11LocalModulePendingForTest(t, manager) {
				t.Fatal("failed stable publication cleared the local recovery dirty marker")
			}
			after, err := store.GetAuthoritative(dataKey)
			if err != nil {
				t.Fatal(err)
			}
			if before.Hash != after.Hash {
				t.Fatal("rejected account-data CAS changed the authoritative recovery blob")
			}
			outbox, err := newDKVSReplicaStore(manager.db).LoadOutbox(store.client.replicaNamespace)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(outbox) > 0; got != tc.wantOutbox {
				t.Fatalf("outbox pending=%t want=%t (entries=%d)", got, tc.wantOutbox, len(outbox))
			}
		})
	}
}
