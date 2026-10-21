package wallet

import (
	"context"
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
}

func (h *rgb11RejectDirtyAccountCASHTTP) rejectIfDirty(path string) bool {
	if h == nil || path != "/v3/dkvs/records/batch-cas" || h.owner == nil {
		return false
	}
	h.owner.mutex.RLock()
	dirty := h.owner.accountProfile != nil && h.owner.accountProfile.ManagedDataDirty
	h.owner.mutex.RUnlock()
	if !dirty {
		return false
	}
	h.rejectMu.Lock()
	h.rejected++
	h.rejectMu.Unlock()
	return true
}

func (h *rgb11RejectDirtyAccountCASHTTP) SendDKVSPost(path string, body []byte) ([]byte, error) {
	if h.rejectIfDirty(path) {
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
	if h.rejectIfDirty(path) {
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
	manager := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(manager, newRGB11MemoryDKVSHTTP())
	if _, err := manager.ImportWallet(rgb11ManagedOperationTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	// ImportWallet creates the local account profile, but remote recovery is not
	// active until the user completes account-storage activation. These tests
	// exercise the post-activation RGB transaction boundary, so make that
	// precondition explicit without constructing an unrelated recovery package.
	manager.mutex.Lock()
	manager.accountProfile.RecoveryConfigured = true
	if err := manager.saveAccountManagementProfileLocked(); err != nil {
		manager.mutex.Unlock()
		t.Fatal(err)
	}
	manager.mutex.Unlock()
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
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
	owner := &Manager{}
	manager := &rgb11Manager{
		Manager:      owner,
		accountOwner: owner,
		scopeStates:  newRGB11ScopeStateRegistry(),
	}
	owner.rgbManager = manager
	owner.dkvs = &dkvsManager{owner: owner, jobs: make(map[string]func(*dkvsStore) error)}

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

func TestRGB11ManagedOperationPublishesOneStableGeneration(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager := configuredRGB11ManagedOperationTestManager(t)
	initialGeneration := manager.accountProfile.ManagedDataGeneration
	_, err := runRGB11ManagedOperation(manager, context.Background(), rgb11ManagedOperationNew,
		func(rgb *rgb11Manager) (struct{}, error) {
			seedRGB11DurableAccountRecoveryState(t, rgb)
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
	if manager.accountProfile.ManagedDataDirty {
		t.Fatalf("stable ACK left account-managed data dirty (generation=%d)",
			manager.accountProfile.ManagedDataGeneration)
	}
	if got := manager.accountProfile.ManagedDataGeneration; got != initialGeneration+1 {
		t.Fatalf("managed generation=%d want=%d", got, initialGeneration+1)
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

func TestRGB11NewInvoiceIsNotReturnedUntilStableCASIsConfirmed(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	for _, tc := range []struct {
		name             string
		transportFailure bool
		wantOutbox       bool
	}{
		{name: "cas-conflict", wantOutbox: false},
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
			root, err := manager.accountManagementRootWallet()
			if err != nil {
				t.Fatal(err)
			}
			dataKey, err := manager.accountManagedDataBlobKey(root)
			if err != nil {
				t.Fatal(err)
			}
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
			if createErr == nil || receive != nil {
				t.Fatalf("invoice escaped before its complete stable recovery CAS: returned=%t error=%v",
					receive != nil, createErr)
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
			if !manager.accountProfile.ManagedDataDirty {
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
