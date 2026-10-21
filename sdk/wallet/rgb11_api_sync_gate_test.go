package wallet

import (
	"errors"
	"reflect"
	"testing"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
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

	t.Run("unconfigured-invoice-does-not-create-local-request", func(t *testing.T) {
		manager := newAccountManagementAutoTestManager(t)
		if _, err := manager.ImportWallet(rgb11ManagedOperationTestMnemonic, "password"); err != nil {
			t.Fatal(err)
		}
		if err := manager.InitializeAccountManagement("password"); err != nil {
			t.Fatal(err)
		}
		if manager.accountManagedRecoveryConfigured() {
			t.Fatal("fixture unexpectedly has account recovery configured")
		}
		receive, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", AmountRaw: "1",
			WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		if !errors.Is(err, ErrDKVSPathNotSynced) || receive != nil {
			t.Fatalf("unconfigured invoice result=%v error=%v", receive != nil, err)
		}
		if count := countRGB11Receives(t, manager.db); count != 0 {
			t.Fatalf("unconfigured invoice persisted %d receive requests", count)
		}
	})

	t.Run("projection-does-not-run-before-baseline-cas", func(t *testing.T) {
		manager := configuredRGB11ManagedOperationTestManager(t)
		remote, fault := installRGB11DirtyCASRejector(t, manager)
		beforeRecords := snapshotRGB11MemoryRecords(remote)
		beforeOutputs, err := manager.rgbManager.projectionStore.ListOutputs()
		if err != nil {
			t.Fatal(err)
		}
		beforeLocks := manager.utxoLockerL1.GetLockedUtxoList()

		err = manager.ProjectRGB11Allocation("", nil, nil)
		if err == nil || errors.Is(err, rgb11wallet.ErrProjectionMismatch) {
			t.Fatalf("ProjectRGB11Allocation ran before rejecting its baseline: %v", err)
		}
		fault.rejectMu.Lock()
		rejected := fault.rejected
		fault.rejectMu.Unlock()
		if rejected == 0 {
			t.Fatal("projection did not reach the authoritative account CAS gate")
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

	t.Run("direct-receive-mapping-does-not-run-before-baseline-cas", func(t *testing.T) {
		manager := configuredRGB11ManagedOperationTestManager(t)
		remote, fault := installRGB11DirtyCASRejector(t, manager)
		beforeRecords := snapshotRGB11MemoryRecords(remote)

		endpoint, err := manager.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
		if err == nil || endpoint != nil {
			t.Fatalf("Direct receive escaped before its baseline CAS: endpoint=%v error=%v", endpoint != nil, err)
		}
		fault.rejectMu.Lock()
		rejected := fault.rejected
		fault.rejectMu.Unlock()
		if rejected == 0 {
			t.Fatal("Direct receive did not reach the authoritative account CAS gate")
		}
		if afterRecords := snapshotRGB11MemoryRecords(remote); !reflect.DeepEqual(beforeRecords, afterRecords) {
			t.Fatal("rejected Direct receive changed account DKVS records")
		}
	})

	t.Run("ack-refresh-does-not-run-before-baseline-cas", func(t *testing.T) {
		manager := configuredRGB11ManagedOperationTestManager(t)
		remote, fault := installRGB11DirtyCASRejector(t, manager)
		beforeRecords := snapshotRGB11MemoryRecords(remote)

		ack, err := manager.RefreshRGB11AddressACK(&swire.DKVSRecord{}, dkvsindexer.RecordVerificationOptions{})
		if err == nil || ack != nil {
			t.Fatalf("ACK refresh escaped before its baseline CAS: ack=%v error=%v", ack != nil, err)
		}
		fault.rejectMu.Lock()
		rejected := fault.rejected
		fault.rejectMu.Unlock()
		if rejected == 0 {
			t.Fatal("ACK refresh did not reach the authoritative account CAS gate")
		}
		if afterRecords := snapshotRGB11MemoryRecords(remote); !reflect.DeepEqual(beforeRecords, afterRecords) {
			t.Fatal("rejected ACK refresh changed account DKVS records")
		}
	})
}
