package wallet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func addRGB11RecoveryReceive(t *testing.T, manager *Manager, account localRGB11Account,
	recipient string) {
	t.Helper()
	scoped, err := manager.newScopedRGB11Manager(account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scoped.engine.CreateReceive(corewallet.ReceiveParams{
		Network: invoicing.BitcoinTestnet4, RecipientID: recipient,
		WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRGB11AccountManagedProviderExcludesLocalReceiveTasks(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	firstID, err := manager.ImportWallet(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"password",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.EnsureAccount(firstID, 1, "first-1", "did:first:1"); err != nil {
		t.Fatal(err)
	}
	secondID, err := manager.ImportWallet(
		"legal winner thank year wave sausage worth useful legal winner thank yellow",
		"password",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.EnsureAccount(secondID, 2, "second-2", "did:second:2"); err != nil {
		t.Fatal(err)
	}
	catalog, err := manager.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Scopes) != 5 {
		t.Fatalf("catalog scopes=%d want=5", len(catalog.Scopes))
	}
	provider := &rgb11AccountManagedDataProvider{owner: manager}
	accounts, err := provider.accountsByScope()
	if err != nil {
		t.Fatal(err)
	}
	selected := []AccountManagedDataScope{catalog.Scopes[0], catalog.Scopes[len(catalog.Scopes)-1]}
	for index, scope := range selected {
		accountValue, ok := accounts[scope.ID()]
		if !ok {
			t.Fatalf("missing local account for scope %s", scope.ID())
		}
		addRGB11RecoveryReceive(t, manager, accountValue, "managed-scope-"+string(rune('a'+index)))
	}
	payloads, err := provider.Export(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 0 {
		t.Fatalf("wallet-local receive tasks leaked into account recovery: %+v", payloads)
	}
	if err := provider.Validate(catalog, payloads); err != nil {
		t.Fatal(err)
	}
}

func TestRGB11AccountManagedProviderMissingPayloadClearsStaleScope(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	_, err := manager.ImportWallet(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"password",
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := manager.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	provider := &rgb11AccountManagedDataProvider{owner: manager}
	accounts, err := provider.accountsByScope()
	if err != nil {
		t.Fatal(err)
	}
	scope := catalog.Scopes[0]
	accountValue := accounts[scope.ID()]
	addRGB11RecoveryReceive(t, manager, accountValue, "stale-receive")
	payloads, err := provider.Export(catalog)
	if err != nil || len(payloads) != 0 {
		t.Fatalf("initial payloads=%d err=%v", len(payloads), err)
	}
	if err := provider.Import(catalog, nil); err != nil {
		t.Fatal(err)
	}
	scoped, err := manager.newScopedRGB11Manager(accountValue)
	if err != nil {
		t.Fatal(err)
	}
	walletID, err := scoped.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	full, _, err := scoped.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.EngineRecords) != 0 || len(full.ProjectionRecords) != 0 {
		t.Fatalf("stale scope survived authoritative empty import: %+v", full)
	}
}

type rgb11ManagedRecoveryValidator struct {
	receipt *rgb11wallet.ValidationReceipt
}

func (v rgb11ManagedRecoveryValidator) ValidateConsignment(context.Context, []byte,
	rgb11wallet.BitcoinEvidenceProvider) (*rgb11wallet.ValidationReceipt, error) {
	copyValue := *v.receipt
	copyValue.Allocations = append([]rgb11wallet.ValidatedAllocation(nil), v.receipt.Allocations...)
	return &copyValue, nil
}

type rgb11ManagedRecoveryEvidence struct{}

func (rgb11ManagedRecoveryEvidence) GetUTXO(string) (*rgb11wallet.BitcoinUTXO, error) {
	return nil, nil
}
func (rgb11ManagedRecoveryEvidence) GetRawTx(string) ([]byte, error) { return nil, nil }
func (rgb11ManagedRecoveryEvidence) GetTxStatus(string) (*rgb11wallet.BitcoinTxStatus, error) {
	return nil, nil
}
func (rgb11ManagedRecoveryEvidence) GetOutspend(string) (*rgb11wallet.BitcoinOutspend, error) {
	return nil, nil
}
func (rgb11ManagedRecoveryEvidence) GetTip() (*rgb11wallet.BitcoinTip, error) { return nil, nil }
func (rgb11ManagedRecoveryEvidence) Broadcast([]byte) (string, error)         { return "", nil }

func seedRGB11DurableAccountRecoveryState(t *testing.T, manager *rgb11Manager) (
	string, indexer.AssetName, string) {
	t.Helper()
	if manager == nil || manager.rgbManager == nil || manager.rgbManager.projectionStore == nil {
		t.Fatal("RGB11 projection store is unavailable")
	}
	raw := []byte("durable-rgb11-account-managed-recovery")
	digest := sha256.Sum256(raw)
	consignmentHash := hex.EncodeToString(digest[:])
	outpoint := strings.Repeat("11", 32) + ":0"
	assetName := indexer.AssetName{Protocol: rgb11wallet.Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: "managed-durable"}
	amount := indexer.NewDefaultDecimal(25)
	allocation := rgb11wallet.ValidatedAllocation{
		OutPoint: outpoint, AssetName: assetName, Amount: *amount.Clone(),
		OperationID: "managed-recovery-operation", AssignmentType: 4000,
		AssignmentIndex: 0, StateClass: "fungible", SealDisclosure: []byte{1, 2, 3},
	}
	const contractID = "managed-recovery-contract"
	receipt, err := manager.rgbManager.projectionStore.ValidateAndStoreConsignment(
		context.Background(), rgb11ManagedRecoveryValidator{receipt: &rgb11wallet.ValidationReceipt{
			Version: 1, EngineBuildID: rgb11wallet.NativeEngineBuildID,
			ConsignmentHash: consignmentHash, ContractID: contractID, SchemaID: "managed-recovery-schema",
			StateHash: [32]byte{1}, Status: "valid",
			Allocations: []rgb11wallet.ValidatedAllocation{allocation},
		}}, rgb11ManagedRecoveryEvidence{}, raw,
	)
	if err != nil {
		t.Fatal(err)
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		t.Fatal(err)
	}
	output := indexer.NewTxOutput(1_000)
	output.OutPointStr = outpoint
	output.OutValue.PkScript = []byte{0x51}
	asset := &indexer.AssetInfo{Name: assetName, Amount: *amount.Clone(), BindingSat: 0}
	proof := &rgb11wallet.AllocationProof{
		OutPoint: outpoint, AssetName: assetName, OperationID: allocation.OperationID,
		AssignmentType: allocation.AssignmentType, AssignmentIndex: allocation.AssignmentIndex,
		StateClass: allocation.StateClass, SealDisclosure: append([]byte(nil), allocation.SealDisclosure...),
		ConsignmentHash: consignmentHash, ValidationHash: receiptHash,
		WitnessTxID: strings.Repeat("22", 32), Status: "settled", Confirmations: 1,
	}
	if err := manager.rgbManager.projectionStore.CommitProjection(output, asset, proof); err != nil {
		t.Fatal(err)
	}
	return outpoint, assetName, contractID
}

func TestRGB11AccountManagedProviderRestoresDurableOwnershipWithoutLocalTasks(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	const mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

	source := newAccountManagementAutoTestManager(t)
	if _, err := source.ImportWallet(mnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	catalog, err := source.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	provider := &rgb11AccountManagedDataProvider{owner: source}
	accounts, err := provider.accountsByScope()
	if err != nil {
		t.Fatal(err)
	}
	scope := catalog.Scopes[0]
	accountValue := accounts[scope.ID()]
	addRGB11RecoveryReceive(t, source, accountValue, "local-receive-task")
	scoped, err := source.newScopedRGB11Manager(accountValue)
	if err != nil {
		t.Fatal(err)
	}
	outpoint, assetName, contractID := seedRGB11DurableAccountRecoveryState(t, scoped)

	payloads, err := provider.Export(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 1 || payloads[0].Scope != scope.ID() {
		t.Fatalf("durable RGB11 recovery payloads=%+v", payloads)
	}
	recovery, err := rgb11wallet.DecodeRecoveryPackage(payloads[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.EngineRecords) != 0 || len(recovery.ProjectionRecords) == 0 {
		t.Fatalf("recovery package engine=%d projection=%d", len(recovery.EngineRecords), len(recovery.ProjectionRecords))
	}
	seen := make(map[string]bool)
	for _, record := range recovery.ProjectionRecords {
		for _, prefix := range []string{"object-", "validation-", "output-", "proof-"} {
			if strings.HasPrefix(record.Key, prefix) {
				seen[prefix] = true
			}
		}
	}
	for _, prefix := range []string{"object-", "validation-", "output-", "proof-"} {
		if !seen[prefix] {
			t.Fatalf("durable recovery package missing %s record", prefix)
		}
	}

	target := newAccountManagementAutoTestManager(t)
	if _, err := target.ImportWallet(mnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	targetCatalog, err := target.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	ext, err := json.Marshal(rgb11wallet.TickerExt{ContractID: contractID})
	if err != nil {
		t.Fatal(err)
	}
	if err := saveTickerInfo(target.db, &indexer.TickerInfo{AssetName: assetName, Content: ext}); err != nil {
		t.Fatal(err)
	}
	targetProvider := &rgb11AccountManagedDataProvider{owner: target}
	if err := targetProvider.Import(targetCatalog, payloads); err != nil {
		t.Fatal(err)
	}
	targetAccounts, err := targetProvider.accountsByScope()
	if err != nil {
		t.Fatal(err)
	}
	targetScoped, err := target.newScopedRGB11Manager(targetAccounts[targetCatalog.Scopes[0].ID()])
	if err != nil {
		t.Fatal(err)
	}
	proof, err := targetScoped.rgbManager.projectionStore.LoadProof(outpoint, assetName)
	if err != nil || proof == nil || proof.Status != "settled" {
		t.Fatalf("restored proof=%+v err=%v", proof, err)
	}
	output, err := targetScoped.rgbManager.projectionStore.LoadOutput(outpoint)
	if err != nil || output.GetAsset(&assetName) == nil {
		t.Fatalf("restored output=%+v err=%v", output, err)
	}
	walletID, err := targetScoped.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	full, _, err := targetScoped.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.EngineRecords) != 0 {
		t.Fatalf("wallet-local receive task leaked into recovered engine state: %d records", len(full.EngineRecords))
	}
}
