package wallet

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func waitRGB11Reconciliation(t *testing.T, manager *Manager,
	condition func(running bool, attempts uint64) bool) {

	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	scope := manager.rgbManager.rgb11ScopeKey()
	var lastRunning bool
	var lastAttempts uint64
	for time.Now().Before(deadline) {
		manager.rgbManager.scopeStates.mu.RLock()
		item := manager.rgbManager.scopeStates.reconciliations[scope]
		running := item != nil && item.running
		var attempts uint64
		if item != nil {
			attempts = item.attempts
		}
		lastRunning, lastAttempts = running, attempts
		manager.rgbManager.scopeStates.mu.RUnlock()
		if condition(running, attempts) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, _ := manager.rgbManager.projectionStore.ListTransfers()
	t.Fatalf("timed out waiting for RGB11 chain reconciliation: running=%v attempts=%d transfers=%+v",
		lastRunning, lastAttempts, state)
}

func rgb11AssetAmount(assets indexer.TxAssets, name indexer.AssetName) uint64 {
	for index := range assets {
		if assets[index].Name == name {
			return assets[index].Amount.Value.Uint64()
		}
	}
	return 0
}

func exportRGB11RecoveryForTest(t *testing.T, manager *Manager) []byte {
	t.Helper()
	walletID, err := manager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	full, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := rgb11wallet.RecoveryPackageFromSnapshot(full, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := rgb11wallet.EncodeRecoveryPackage(recovery)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func importRGB11RecoveryForTest(t *testing.T, manager *Manager, encoded []byte) {
	t.Helper()
	recovery, err := rgb11wallet.DecodeRecoveryPackage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := recovery.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.rgbManager.importRGB11WalletSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := manager.rgbManager.rebuildRGB11Locks(); err != nil {
		t.Fatal(err)
	}
}

func TestRGB11AccountManagedRecoveryReconcilesBroadcastAfterRestart(t *testing.T) {
	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	recipientWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	if senderWallet == nil || recipientWallet == nil {
		t.Fatal("create RGB11 reconciliation wallets")
	}
	senderScript, err := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}

	evidenceBase := &rgb11FlowEvidence{
		utxos: make(map[string]*rgb11wallet.BitcoinUTXO),
		rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	evidence := &rgb11AddressEvidence{
		rgb11FlowEvidence: evidenceBase,
		statuses:          make(map[string]*rgb11wallet.BitcoinTxStatus),
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	for index := 0; index < 3; index++ {
		outpoint := fmt.Sprintf("%064x:0", 34+index)
		utxo := &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: 100_000, PkScript: append([]byte(nil), senderScript...), Confirmations: 6,
		}
		evidenceBase.utxos[outpoint] = utxo
		output := indexer.NewTxOutput(utxo.Value)
		output.OutPointStr = outpoint
		output.OutValue.PkScript = append([]byte(nil), senderScript...)
		rpc.outputs[outpoint] = output
		rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
			OutPoint: outpoint, Value: utxo.Value, PkScript: append([]byte(nil), senderScript...),
		})
	}

	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 34)
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, 35)
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "RECON", Name: "Reconciliation Fixture", Amounts: []uint64{10},
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "5", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: request.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	witness := wire.NewMsgTx(wire.TxVersion)
	if err := witness.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	txID, err := sender.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID})
	if err != nil {
		t.Fatal(err)
	}

	// Account management persists only the minimum RGB recovery package. A new
	// device imports it and rebuilds locks/cache before chain reconciliation.
	broadcastRecovery := exportRGB11RecoveryForTest(t, sender)
	sender.rgbManager.scopeStates.stopReconciliations()
	restoredWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	restored := newRGB11FlowManager(t, restoredWallet, rpc, evidence, 340)
	restored.rgbManager.scopeStates.mu.Lock()
	restored.rgbManager.scopeStates.retryDelay = time.Hour
	restored.rgbManager.scopeStates.mu.Unlock()
	importRGB11RecoveryForTest(t, restored, broadcastRecovery)
	restored.rgbManager.scheduleRGB11ChainReconciliation()
	waitRGB11Reconciliation(t, restored, func(running bool, attempts uint64) bool {
		return running && attempts >= 1
	})
	state, err := restored.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	if available, pendingAmount := rgb11AssetAmount(state.AvailableAssets, issued.AssetName),
		rgb11AssetAmount(state.PendingAssets, issued.AssetName); available != 0 || pendingAmount != 10 {
		t.Fatalf("invisible evidence balance available=%d pending=%d", available, pendingAmount)
	}

	evidenceBase.mu.Lock()
	evidenceBase.rawTx[txID] = append([]byte(nil), pending.SignedTx...)
	for _, outpoint := range pending.State.InputOutPoints {
		evidenceBase.spendingTx[outpoint] = txID
	}
	for _, outpoint := range pending.State.OutputOutPoints {
		parsed, parseErr := wire.NewOutPointFromString(outpoint)
		if parseErr != nil {
			evidenceBase.mu.Unlock()
			t.Fatal(parseErr)
		}
		evidenceBase.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: witness.TxOut[parsed.Index].Value,
			PkScript: append([]byte(nil), witness.TxOut[parsed.Index].PkScript...), Confirmations: 1,
		}
	}
	evidenceBase.mu.Unlock()
	evidence.setStatus(txID, rgb11wallet.BitcoinTxStatus{TxID: txID, Confirmed: true, Confirmations: 1})
	restored.rgbManager.scheduleRGB11ChainReconciliation()
	waitRGB11Reconciliation(t, restored, func(running bool, attempts uint64) bool {
		return !running && attempts >= 1
	})
	state, err = restored.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	if available, pendingAmount := rgb11AssetAmount(state.AvailableAssets, issued.AssetName),
		rgb11AssetAmount(state.PendingAssets, issued.AssetName); available != 5 || pendingAmount != 0 {
		t.Fatalf("confirmed change balance available=%d pending=%d", available, pendingAmount)
	}

	// Persist the new minimum recovery state and verify another restart restores
	// the confirmed allocation without carrying the completed transfer cache.
	confirmedRecovery := exportRGB11RecoveryForTest(t, restored)
	restartedWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	restarted := newRGB11FlowManager(t, restartedWallet, rpc, evidence, 341)
	importRGB11RecoveryForTest(t, restarted, confirmedRecovery)
	restartedState, err := restarted.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	if available := rgb11AssetAmount(restartedState.AvailableAssets, issued.AssetName); available != 5 ||
		restartedState.SyncStatus != "idle" {
		t.Fatalf("restart state available=%d sync=%s", available, restartedState.SyncStatus)
	}
	transfers, err := restarted.rgbManager.projectionStore.ListTransfers()
	if err != nil || len(transfers) != 0 {
		t.Fatalf("completed transfer history leaked into recovery package: transfers=%+v err=%v", transfers, err)
	}
}
