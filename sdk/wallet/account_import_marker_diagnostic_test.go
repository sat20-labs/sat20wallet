package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func stageRemoteImportWithRGBPayload(t *testing.T, target *Manager, payload []byte) (*accountLocalKV, error) {
	t.Helper()
	entry, err := accountEntry(target.accountProfile.AccountID, rgb11AccountManagedProviderID, AccountManagedDataGlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := sealAccountPayload(target.accountSecret, target.accountProfile.AccountID, _chain, entry, payload)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := target.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	local, err := target.readAccountLocal(target.accountProfile.AccountID, entry.Provider, entry.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if local == nil {
		local = &accountLocalKV{Version: 1, AccountID: target.accountProfile.AccountID, Network: target.cfg.Chain,
			Provider: entry.Provider, Scope: entry.Scope, Key: entry.Key, Codec: entry.Codec, Generation: 1}
	}
	importErr := target.importAccountValue(entry, catalog, local, nil, encoded, payload, false)
	if importErr != nil {
		importErr = target.accountModuleFailure(entry, importErr, nil)
	}
	stored, err := target.readAccountLocal(target.accountProfile.AccountID, entry.Provider, entry.Scope)
	if err != nil {
		t.Fatal(err)
	}
	return stored, importErr
}

func TestRemoteImportMarkerKeepsCommittedTargetWhenPublicReferenceIsInvalid(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	target, provider := managedImportTestManager(t, remote)
	if err := restoreManagedImportTestWallet(t, target, source); err != nil {
		t.Fatal(err)
	}
	rgbProvider := &managedImportTestProvider{accountManagedDataProviderStub: accountManagedDataProviderStub{id: rgb11AccountManagedProviderID}}
	target.managedDataProviders[rgb11AccountManagedProviderID] = rgbProvider
	oldPayloads := append([]AccountManagedDataPayload(nil), provider.payloads...)
	remoteBefore := snapshotRGB11MemoryRecords(remote)
	local, err := stageRemoteImportWithRGBPayload(t, target, []byte("invalid RGB recovery package"))
	if !errors.Is(err, ErrRGB11PublicIdentityMismatch) {
		t.Fatalf("invalid public reference was imported: %v", err)
	}
	if local == nil || !local.Restoring || len(local.Value) == 0 || local.LastError == "" ||
		rgbProvider.imports != 0 || !reflect.DeepEqual(oldPayloads, provider.payloads) {
		t.Fatal("public verification failure lost encrypted target or changed provider data")
	}
	if err := target.CheckAccountRestore(rgb11AccountManagedProviderID, AccountManagedDataGlobalScope); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
		t.Fatalf("invalid RGB reference no longer blocks its module: %v", err)
	}
	if err := target.CheckAccountRestore(provider.id, AccountManagedDataGlobalScope); err != nil {
		t.Fatalf("invalid RGB reference blocked unrelated provider: %v", err)
	}
	if !reflect.DeepEqual(remoteBefore, snapshotRGB11MemoryRecords(remote)) {
		t.Fatal("failed public verification changed remote records")
	}
}

func TestRemoteImportMarkerRetriesAfterPublicContractReturns(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	target, _ := managedImportTestManager(t, remote)
	useRGB11TestCoreAuthority(t, target)
	if err := restoreManagedImportTestWallet(t, target, source); err != nil {
		t.Fatal(err)
	}
	// Issue through the actual RGB wallet, then use its valid recovery closure
	// as the authenticated target payload. The stub provider isolates the
	// account import boundary from RGB chain refresh in this test.
	chain := newCoreE2EChain()
	rpc := &coreE2EL1Indexer{chain: chain}
	wallet := NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", &chaincfg.TestNet4Params)
	chain.fund(t, wallet.GetAddress(), 6)
	rgbSource := newRGB11FlowManager(t, wallet, rpc, chain, 1510, accountGroupUnprepared)
	prepareRGBEndpoint(t, rgbSource, remote)
	issued, err := rgbSource.rgbManager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "PUB", Name: "Public replay fixture", Amounts: []uint64{100},
	})
	if err != nil {
		t.Fatal(err)
	}
	record := installRGB11PublicContractFixture(t, rgbSource, remote, issued.ContractID)
	registryKey := record.Key
	catalog, err := rgbSource.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := (&rgb11AccountManagedDataProvider{owner: rgbSource}).Export(catalog)
	if err != nil || len(payloads) == 0 {
		t.Fatalf("export RGB recovery closure: %v", err)
	}
	payload := payloads[len(payloads)-1].Payload
	if _, err := rgb11wallet.DecodeRecoveryPackage(payload); err != nil {
		t.Fatal(err)
	}
	// The public object exists initially, then disappears before a committed
	// target is replayed. Its return must permit the exact same marker to
	// finish without another private write or new state revision.
	remote.records[registryKey] = record
	bundle := account.ManagedDataBundle{Version: account.ManagedDataBundleVersion, Revision: 1,
		Items: []account.ManagedDataItem{{Provider: rgb11AccountManagedProviderID,
			Scope: AccountManagedDataGlobalScope, Payload: payload}}}
	if err := target.verifyRGB11PublicRecoveryReferences(bundle); err != nil {
		t.Fatalf("valid public reference fixture is unreadable: %v", err)
	}
	rgbProvider := &managedImportTestProvider{accountManagedDataProviderStub: accountManagedDataProviderStub{id: rgb11AccountManagedProviderID}}
	target.managedDataProviders[rgb11AccountManagedProviderID] = rgbProvider
	delete(remote.records, registryKey)
	beforeRemote := snapshotRGB11MemoryRecords(remote)
	blocked, err := stageRemoteImportWithRGBPayload(t, target, payload)
	if !errors.Is(err, ErrRGB11PublicRegistrationRequired) {
		t.Fatalf("missing public object did not block import: %v", err)
	}
	if blocked == nil || !blocked.Restoring || len(blocked.Value) == 0 || rgbProvider.imports != 0 {
		t.Fatal("unavailable public reference advanced the module import")
	}
	if !reflect.DeepEqual(beforeRemote, snapshotRGB11MemoryRecords(remote)) {
		t.Fatal("blocked module import changed remote records")
	}
	remote.records[registryKey] = record
	beforeRemote = snapshotRGB11MemoryRecords(remote)
	entry, err := accountEntry(target.accountProfile.AccountID, rgb11AccountManagedProviderID, AccountManagedDataGlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err = target.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	// Resume the exact encrypted target through the production module importer,
	// not a reconstructed bundle or a fabricated import-marker implementation.
	if err := target.importAccountValue(entry, catalog, blocked, nil, blocked.Value, payload, false); err != nil {
		t.Fatalf("replay after public object returns: %v", err)
	}
	after, err := target.readAccountLocal(target.accountProfile.AccountID, entry.Provider, entry.Scope)
	if err != nil || after == nil || after.Restoring || after.LastError != "" || rgbProvider.imports != 1 ||
		!bytes.Equal(after.Value, blocked.Value) {
		t.Fatalf("public reference recovery did not finish exact module import: imports=%d err=%v", rgbProvider.imports, err)
	}
	if !reflect.DeepEqual(beforeRemote, snapshotRGB11MemoryRecords(remote)) {
		t.Fatal("module retry republished private data")
	}
}

func TestAccountManagedImportMarkerRecordsRemoteApplyFailure(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	remote := newRGB11MemoryDKVSHTTP()
	source, sourceProvider := managedImportTestSource(t, remote)
	target, targetProvider := managedImportTestManager(t, remote)
	if err := restoreManagedImportTestWallet(t, target, source); err != nil {
		t.Fatal(err)
	}
	stageManagedImportTestPayload(t, source, sourceProvider, []byte("new remote provider state"))
	if err := source.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	importFailure := errors.New("diagnostic provider import failure")
	targetProvider.importErr = importFailure
	beforeRemote := snapshotRGB11MemoryRecords(remote)
	if err := target.SyncAccountManagementState(context.Background()); !errors.Is(err, importFailure) || !accountModuleOnly(err) {
		t.Fatalf("expected module-local provider import failure: %v", err)
	}
	assertManagedImportModuleProtected(t, target, targetProvider.id)
	local, err := target.readAccountLocal(target.accountProfile.AccountID, targetProvider.id, AccountManagedDataGlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	if local.Baseline == nil || local.Baseline.Seq == 0 || local.LastError == "" ||
		!bytes.Equal(local.Value, local.Baseline.Value) {
		t.Fatal("failed module import lacks its exact authenticated remote target")
	}
	if !reflect.DeepEqual(beforeRemote, snapshotRGB11MemoryRecords(remote)) {
		t.Fatal("failed remote module apply rewrote remote records")
	}
}

func TestMalformedAccountManagedModuleCopyRemainsFailClosed(t *testing.T) {
	remote := newRGB11MemoryDKVSHTTP()
	manager, _ := managedImportTestSource(t, remote)
	key := accountLocalKey(manager.accountProfile.AccountID, "test", AccountManagedDataGlobalScope)
	if err := manager.db.Write(key, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckAccountRestore("test", AccountManagedDataGlobalScope); err == nil {
		t.Fatal("malformed module copy no longer fails closed")
	}
	if err := manager.CheckAccountWrite("test", AccountManagedDataGlobalScope); err == nil {
		t.Fatal("malformed module copy allows new business")
	}
	if err := manager.CheckAccountRestore("wallets", AccountManagedDataGlobalScope); err != nil {
		t.Fatalf("malformed module copy blocked an unrelated module: %v", err)
	}
}

func TestAccountManagedImportMarkerRecordsExplicitRestoreFailure(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	target, provider := managedImportTestManager(t, remote)
	provider.importErr = errors.New("restore provider import failure")
	beforeRemote := snapshotRGB11MemoryRecords(remote)
	if err := restoreManagedImportTestWallet(t, target, source); err != nil {
		t.Fatalf("module failure aborted wallet restoration: %v", err)
	}
	assertManagedImportModuleProtected(t, target, provider.id)
	local, err := target.readAccountLocal(target.accountProfile.AccountID, provider.id, AccountManagedDataGlobalScope)
	if err != nil || local == nil || local.LastError == "" {
		t.Fatalf("module failure diagnostic missing: %v", err)
	}
	if !bytes.Equal(target.accountSecret, source.accountSecret) || len(target.GetWalletCatalog()) == 0 {
		t.Fatal("module failure discarded the restored account identity")
	}
	if !reflect.DeepEqual(beforeRemote, snapshotRGB11MemoryRecords(remote)) {
		t.Fatal("partial restore modified remote data")
	}
}
