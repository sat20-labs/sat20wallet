package wallet

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func installRGB11DirtyCASRejector(t *testing.T, manager *Manager) (*rgb11MemoryDKVSHTTP, *rgb11RejectDirtyAccountCASHTTP) {
	t.Helper()
	remote, ok := manager.http.(*rgb11MemoryDKVSHTTP)
	if !ok || manager.dkvs == nil {
		t.Fatal("managed RGB11 fixture does not use the expected account DKVS transport")
	}
	beforeRecords := snapshotRGB11MemoryRecords(remote)
	// Catalog edits are real account-managed mutations and persist locally before
	// their scheduled sync job runs. This fixture intentionally leaves the DKVS
	// worker stopped, so the test can install the CAS fault before synchronization.
	manager.dkvs.mu.Lock()
	workerRunning := manager.dkvs.stop != nil
	manager.dkvs.mu.Unlock()
	if workerRunning {
		t.Fatal("managed RGB11 test manager unexpectedly has a running DKVS worker")
	}
	if err := manager.EnsureAccount(manager.status.CurrentWallet, 1,
		"Unpublished RGB baseline", "did:rgb11-cas-rejector"); err != nil {
		t.Fatal(err)
	}
	manager.mutex.RLock()
	profile := manager.accountProfile
	profileHasPending := profile != nil && profile.ManagedDataDirty
	mutationFound := false
	if profile != nil {
		for _, mutation := range profile.Pending {
			if mutation.Type == accountMutationEnsureAccount && mutation.Account == 1 &&
				mutation.Name == "Unpublished RGB baseline" && mutation.DID == "did:rgb11-cas-rejector" {
				mutationFound = true
				break
			}
		}
	}
	info := manager.walletInfoMap[manager.status.CurrentWallet]
	accountMaterialized := info != nil && info.Accounts >= 2 &&
		info.AccountNames[1] == "Unpublished RGB baseline" &&
		info.AccountDIDs[1] == "did:rgb11-cas-rejector"
	manager.mutex.RUnlock()
	if !profileHasPending || !mutationFound || !accountMaterialized {
		t.Fatalf("fixture account catalog edit was not persisted locally: dirty=%t mutation=%t account=%t",
			profileHasPending, mutationFound, accountMaterialized)
	}
	if afterRecords := snapshotRGB11MemoryRecords(remote); !reflect.DeepEqual(beforeRecords, afterRecords) {
		t.Fatal("fixture account catalog edit reached the authoritative remote before the injected CAS failure")
	}
	fault := &rgb11RejectDirtyAccountCASHTTP{rgb11MemoryDKVSHTTP: remote, owner: manager}
	manager.http = fault
	manager.dkvs.mu.Lock()
	for _, client := range manager.dkvs.clients {
		client.Http = fault
	}
	manager.dkvs.mu.Unlock()
	return remote, fault
}

func snapshotRGB11MemoryRecords(remote *rgb11MemoryDKVSHTTP) map[string]*swire.DKVSRecord {
	remote.mu.Lock()
	defer remote.mu.Unlock()
	result := make(map[string]*swire.DKVSRecord, len(remote.records))
	for key, record := range remote.records {
		if record == nil {
			result[key] = nil
			continue
		}
		copyRecord := *record
		copyRecord.Value = append([]byte(nil), record.Value...)
		copyRecord.PubKey = append([]byte(nil), record.PubKey...)
		copyRecord.Signature = append([]byte(nil), record.Signature...)
		copyRecord.FeeProof = append([]byte(nil), record.FeeProof...)
		result[key] = &copyRecord
	}
	return result
}

func TestRGB11PublicOperationsRequireRecoveryBeforeMutation(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	t.Run("unconfigured-invoice-stays-local", func(t *testing.T) {
		manager := newAccountManagementAutoTestManager(t)
		remote := manager.http.(*rgb11MemoryDKVSHTTP)
		if _, err := manager.ImportWallet(rgb11ManagedOperationTestMnemonic, "password"); err != nil {
			t.Fatal(err)
		}
		if err := manager.InitializeAccountManagement("password"); err != nil {
			t.Fatal(err)
		}
		prepareRGBEndpoint(t, manager, remote)
		if err := manager.rgbManager.selectRGB11Scope(); err != nil {
			t.Fatal(err)
		}
		manager.rgbManager.evidence = newCoreE2EChain()
		if manager.accountManagedRecoveryConfigured() {
			t.Fatal("fixture unexpectedly has account recovery configured")
		}
		beforeRemote := snapshotRGB11MemoryRecords(remote)
		scope, err := manager.rgbManager.rgb11AccountManagedScopeID()
		if err != nil {
			t.Fatal(err)
		}
		previous, err := manager.readAccountLocal(manager.accountProfile.AccountID, rgb11AccountManagedProviderID, scope)
		if err != nil {
			t.Fatal(err)
		}
		var beforeGen uint64
		if previous != nil {
			beforeGen = previous.Generation
		}
		receive, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", AmountRaw: "1",
			WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		if err != nil || receive == nil {
			t.Fatalf("unconfigured invoice result=%v error=%v", receive != nil, err)
		}
		if count := countRGB11Receives(t, manager.db); count != 1 {
			t.Fatalf("unconfigured invoice persisted %d receive requests", count)
		}
		if !rgb11LocalModulePendingForTest(t, manager) ||
			rgb11LocalModuleForTest(t, manager).Generation <= beforeGen {
			t.Fatal("local receive request did not advance the durable backup generation")
		}
		if afterRemote := snapshotRGB11MemoryRecords(remote); !reflect.DeepEqual(beforeRemote, afterRemote) {
			t.Fatal("unactivated invoice reached account DKVS")
		}
		restarted, err := newRGB11Manager(manager, manager.db, manager.utxoLockerL1, manager.rgbManager.evidence)
		if err != nil {
			t.Fatal(err)
		}
		manager.rgbManager = restarted
		if err := restarted.selectRGB11Scope(); err != nil {
			t.Fatal(err)
		}
		loaded, err := restarted.engine.LoadReceive(receive.RequestID)
		if err != nil || loaded == nil || loaded.Invoice != receive.Invoice ||
			loaded.RelayKey != receive.RelayKey || loaded.AckKey != receive.AckKey ||
			!reflect.DeepEqual(loaded.WitnessScript, receive.WitnessScript) {
			t.Fatalf("local RGB receive material did not survive module restart: loaded=%t error=%v",
				loaded != nil, err)
		}
	})

	t.Run("projection-does-not-run-while-offline", func(t *testing.T) {
		manager := configuredRGB11ManagedOperationTestManager(t)
		remote := observeRGB11OfflineSession(t, manager)
		beforeRecords := snapshotRGB11MemoryRecords(remote)
		beforeOutputs, err := manager.rgbManager.projectionStore.ListOutputs()
		if err != nil {
			t.Fatal(err)
		}
		beforeLocks := manager.utxoLockerL1.GetLockedUtxoList()

		err = manager.ProjectRGB11Allocation("", nil, nil)
		if !errors.Is(err, ErrDKVSPathNotSynced) {
			t.Fatalf("ProjectRGB11Allocation ran before rejecting its baseline: %v", err)
		}
		if afterRecords := snapshotRGB11MemoryRecords(remote); !reflect.DeepEqual(beforeRecords, afterRecords) {
			t.Fatal("rejected projection changed the authoritative account baseline")
		}
		afterOutputs, err := manager.rgbManager.projectionStore.ListOutputs()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(beforeOutputs, afterOutputs) ||
			!reflect.DeepEqual(beforeLocks, manager.utxoLockerL1.GetLockedUtxoList()) {
			t.Fatal("rejected projection changed local outputs or input locks")
		}
	})

	t.Run("direct-receive-mapping-does-not-run-while-offline", func(t *testing.T) {
		manager := configuredRGB11ManagedOperationTestManager(t)
		remote := observeRGB11OfflineSession(t, manager)
		beforeRecords := snapshotRGB11MemoryRecords(remote)

		endpoint, err := manager.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
		if !errors.Is(err, ErrDKVSPathNotSynced) || endpoint != nil {
			t.Fatalf("Direct receive escaped before its baseline CAS: endpoint=%v error=%v", endpoint != nil, err)
		}
		if afterRecords := snapshotRGB11MemoryRecords(remote); !reflect.DeepEqual(beforeRecords, afterRecords) {
			t.Fatal("rejected Direct receive changed account DKVS records")
		}
	})

	t.Run("ack-refresh-does-not-run-while-offline", func(t *testing.T) {
		manager := configuredRGB11ManagedOperationTestManager(t)
		remote := observeRGB11OfflineSession(t, manager)
		beforeRecords := snapshotRGB11MemoryRecords(remote)

		ack, err := manager.RefreshRGB11AddressACK(&swire.DKVSRecord{}, dkvsindexer.RecordVerificationOptions{})
		if !errors.Is(err, ErrDKVSPathNotSynced) || ack != nil {
			t.Fatalf("ACK refresh escaped before its baseline CAS: ack=%v error=%v", ack != nil, err)
		}
		if afterRecords := snapshotRGB11MemoryRecords(remote); !reflect.DeepEqual(beforeRecords, afterRecords) {
			t.Fatal("rejected ACK refresh changed account DKVS records")
		}
	})
}

func observeRGB11OfflineSession(t *testing.T, manager *Manager) *rgb11MemoryDKVSHTTP {
	t.Helper()
	manager.accountBackgroundWG.Wait()
	remote, ok := manager.http.(*rgb11MemoryDKVSHTTP)
	if !ok {
		t.Fatal("unexpected test transport")
	}
	fault := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	fault.post = func(context.Context, string, []byte) ([]byte, error) {
		return nil, &net.DNSError{Err: "offline test transport", IsTimeout: true}
	}
	client, err := manager.GetDKVSClient()
	if err != nil {
		t.Fatal(err)
	}
	client.Http = fault
	root, err := manager.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	key, err := manager.accountManagedStateKey(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := manager.accountDKVSStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SyncCurrent(key); err == nil {
		t.Fatal("fixture did not observe an actual offline synchronization failure")
	}
	return remote
}
