package wallet

import (
	"sync"
	"testing"
	"time"
)

func TestAccountRestorePostSyncNetworkWaitDoesNotBlockRGBState(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })

	remote := newRGB11MemoryDKVSHTTP()
	source, _, _ := newImportSource(t, remote)
	target, provider, _ := newImportGroup(t, remote)
	// Keep the post-sync network phase observable with a real active provider;
	// RGB recovery now uses stable module copies and does not read its cache.
	for _, manager := range []*Manager{source, target} {
		if err := manager.RegisterAccountManagedDataProvider(&accountManagedActiveDataProviderStub{
			accountManagedDataProviderStub: accountManagedDataProviderStub{id: "postsync-active"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	localImport := make(chan struct{})
	var importOnce sync.Once
	beforeImport := provider.beforeImport
	provider.beforeImport = func() {
		beforeImport()
		importOnce.Do(func() { close(localImport) })
	}

	blocked := &blockedAccountConfigHTTP{
		rgb11MemoryDKVSHTTP: remote,
		started:             make(chan struct{}), release: make(chan struct{}),
	}
	target.http = blocked
	target.SetDKVSHttpClient(blocked)

	value := managedImportRecoveryValue(t, source)
	locator, location := importGroupMaterial(t, source)
	done := make(chan error, 1)
	var stateDone chan error
	var release sync.Once
	defer func() {
		release.Do(func() { close(blocked.release) })
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("restore did not stop after the network gate was released")
		}
		if stateDone != nil {
			select {
			case <-stateDone:
			case <-time.After(3 * time.Second):
				t.Error("RGB state read did not stop after the network gate was released")
			}
		}
	}()
	go func() {
		defer close(done)
		_, err := target.RestoreAccountManagementState(
			value,
			source.accountSecret,
			"password",
			locator,
			AccountManagementRestoreOptions{
				Location:    location,
				StorageMode: AccountStorageTemporary,
				RecordTTL:   testRGB11FreeLocalTTL,
			},
		)
		done <- err
	}()

	// Wallet encryption and local restore run before the post-sync network
	// phase. Start its latency check only after provider import has begun.
	select {
	case <-localImport:
	case err := <-done:
		t.Fatalf("restore ended before local provider import: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("restore did not reach its local provider import")
	}
	select {
	case <-blocked.started:
	case err := <-done:
		t.Fatalf("restore ended before post-sync network gate: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("restore did not reach post-sync network gate")
	}
	if err := target.checkAccountManagedDataImport(); err != nil {
		t.Fatalf("post-sync network wait still inside import crash boundary: %v", err)
	}

	stateDone = make(chan error, 1)
	go func() {
		defer close(stateDone)
		_, err := target.GetRGB11State()
		stateDone <- err
	}()
	select {
	case err := <-stateDone:
		if err != nil {
			t.Fatalf("read RGB state during restore post-sync wait: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("GetRGB11State blocked behind retryable restore post-sync network work")
	}

	release.Do(func() { close(blocked.release) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("retryable post-sync network failure was reported as restore failure: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("restore did not finish after post-sync network release")
	}
}
