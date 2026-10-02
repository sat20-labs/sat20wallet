package wallet

import (
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

func TestAccountRestorePostSyncNetworkWaitDoesNotBlockRGBState(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	target, _ := managedImportTestManager(t, remote)

	blocked := &blockedAccountConfigHTTP{
		rgb11MemoryDKVSHTTP: remote,
		started: make(chan struct{}), release: make(chan struct{}),
	}
	target.http = blocked
	target.dkvs.mu.Lock()
	target.dkvs.clients = make(map[string]*SatsNetDKVSClient)
	target.dkvs.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := target.RestoreAccountManagementState(
			managedImportRecoveryValue(t, source),
			source.accountSecret,
			"password",
			account.Locator{AccountID: source.accountProfile.AccountID},
			AccountManagementRestoreOptions{
				StorageMode: AccountStorageTemporary,
				RecordTTL: testRGB11FreeLocalTTL,
			},
		)
		done <- err
	}()

	select {
	case <-blocked.started:
	case err := <-done:
		t.Fatalf("restore ended before post-sync network gate: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("restore did not reach post-sync network gate")
	}
	if err := target.checkAccountManagedDataImport(); err != nil {
		close(blocked.release)
		<-done
		t.Fatalf("post-sync network wait still inside import crash boundary: %v", err)
	}

	stateDone := make(chan error, 1)
	go func() {
		_, err := target.GetRGB11State()
		stateDone <- err
	}()
	select {
	case err := <-stateDone:
		if err != nil {
			t.Fatalf("read RGB state during restore post-sync wait: %v", err)
		}
	case <-time.After(time.Second):
		close(blocked.release)
		<-done
		t.Fatal("GetRGB11State blocked behind retryable restore post-sync network work")
	}

	close(blocked.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("retryable post-sync network failure was reported as restore failure: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("restore did not finish after post-sync network release")
	}
}
