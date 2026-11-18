package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type rgb11ProjectionReadinessFailureEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	outpoint string
}

func (e *rgb11ProjectionReadinessFailureEvidence) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	if outpoint == e.outpoint {
		return nil, errors.New("injected RGB11 proof spend-evidence failure")
	}
	return e.BitcoinEvidenceProvider.GetOutspend(outpoint)
}

func TestRGBRecoveredConfirmations(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	ctx := context.Background()
	ownerWallet, ownerMnemonic, err := NewInteralWallet(&chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	receiverWallet, receiverMnemonic, err := NewInteralWallet(&chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	ownerScript, err := AddrToPkScript(ownerWallet.GetAddress(), &chaincfg.TestNet4Params)
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
	for index := 0; index < 4; index++ {
		outpoint := seedRGB11FlowEvidenceUTXO(t, evidenceBase, ownerScript, 100_000, byte(201+index))
		evidence.setStatus(allocationOutpointTxID(outpoint), rgb11wallet.BitcoinTxStatus{TxID: allocationOutpointTxID(outpoint), Confirmed: true, Confirmations: 6})
		utxo := &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: 100_000,
			PkScript: append([]byte(nil), ownerScript...), Confirmations: 6,
		}
		evidenceBase.utxos[outpoint] = utxo
		output := indexer.NewTxOutput(utxo.Value)
		output.OutPointStr = outpoint
		output.OutValue.PkScript = append([]byte(nil), ownerScript...)
		rpc.outputs[outpoint] = output
		rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
			OutPoint: outpoint, Value: utxo.Value, PkScript: append([]byte(nil), ownerScript...),
		})
	}

	remote := newRGB11MemoryDKVSHTTP()
	client := newRGB11MessageNodeClient(remote)
	owner := newRGB11ActivatedFlowAccount(t, ownerMnemonic, remote, client, evidence, rpc)
	receiver := newRGB11ActivatedFlowAccount(t, receiverMnemonic, remote, client, evidence, rpc)
	if owner.wallet.GetAddress() != ownerWallet.GetAddress() || receiver.wallet.GetAddress() != receiverWallet.GetAddress() {
		t.Fatal("activated readiness fixture changed original wallet identities")
	}
	// The first-import assertion must not be satisfied by a background refresh.
	owner.rgbManager.scopeStates.stopReconciliations()
	receiver.rgbManager.scopeStates.stopReconciliations()
	issued, err := owner.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "RRG", Name: "Projection Readiness Test",
		Precision: 2, Amounts: []uint64{100},
	})
	if err != nil {
		t.Fatalf("issue asset: %v", err)
	}
	installRGB11PublicContractFixture(t, owner, remote, issued.ContractID)
	if _, err := owner.RefreshRGB11State(ctx); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	firstInvoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "40", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	firstPrepared, err := owner.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: firstInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatalf("prepare initial transfer: %v", err)
	}
	firstPending, _ := confirmRGB11ProjectionTestTransfer(
		t, owner, evidence, rpc, firstPrepared.State.TransferID,
	)
	if _, err := receiver.AcceptRGB11Consignment(ctx, firstInvoice.RequestID, firstPending.RecipientConsignment); err != nil {
		t.Fatalf("accept initial transfer: %v", err)
	}
	if _, err := owner.RefreshRGB11State(ctx); err != nil {
		t.Fatalf("refresh after initial transfer: %v", err)
	}

	// The second scope exercises a later failure in the same provider Import.
	if err := owner.EnsureAccount(owner.GetAccountManagementStatus().RootWalletID, 1, "rollback scope", ""); err != nil {
		t.Fatal(err)
	}
	peer := r1ManagedPeer(t, owner, evidence, rpc)
	if peer.wallet.GetAddress() != ownerWallet.GetAddress() {
		t.Fatal("recovered peer changed original owner identity")
	}
	peerTransfers, err := peer.rgbManager.projectionStore.ListTransfers()
	if err != nil || len(peerTransfers) != 1 || peerTransfers[0].TransferID != firstPrepared.State.TransferID {
		t.Fatalf("recovered peer lost the complete baseline journal: count=%d err=%v", len(peerTransfers), err)
	}

	remoteInvoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "25", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	remotePrepared, err := peer.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: remoteInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatalf("prepare remote consumer: %v", err)
	}
	remotePending, remoteTxID := confirmRGB11ProjectionTestTransfer(
		t, peer, evidence, rpc, remotePrepared.State.TransferID,
	)
	if _, err := receiver.AcceptRGB11Consignment(ctx, remoteInvoice.RequestID, remotePending.RecipientConsignment); err != nil {
		t.Fatalf("accept remote consumer transfer: %v", err)
	}
	if len(remotePending.ChangeSeals) != 1 {
		t.Fatalf("remote consumer change outputs=%d", len(remotePending.ChangeSeals))
	}
	remoteChange := fmt.Sprintf("%s:%d", remoteTxID, remotePending.ChangeSeals[0].Vout)

	// The peer knows its own transfer, so the unconfirmed change is pending.
	setRGB11ProjectionTestTxConfirmations(t, evidence, remoteTxID, 0)
	if _, err := peer.RefreshRGB11State(ctx); err != nil {
		t.Fatal(err)
	}
	pendingView, err := peer.GetRGB11State()
	if err != nil || rgb11AssetAmount(pendingView.AvailableAssets, issued.AssetName) != 0 ||
		rgb11AssetAmount(pendingView.PendingAssets, issued.AssetName) == 0 {
		t.Fatalf("zero-confirmation output must stay pending: state=%v err=%v", pendingView, err)
	}
	// The owner only sees its earlier change spent in the mempool, not the
	// peer's RGB consumer. Quarantine that exact carrier without inventing a send.
	if len(firstPending.ChangeSeals) != 1 {
		t.Fatalf("first transfer change seals=%d, want 1", len(firstPending.ChangeSeals))
	}
	ownerCarrier := fmt.Sprintf("%s:%d", firstPending.State.WitnessTxID, firstPending.ChangeSeals[0].Vout)
	if !containsString(remotePending.State.InputOutPoints, ownerCarrier) {
		t.Fatalf("peer transfer did not consume the owner's change %s", ownerCarrier)
	}
	before, err := owner.rgbManager.projectionStore.ListTransfers()
	if err != nil || len(before) != 1 || before[0].TransferID != firstPrepared.State.TransferID {
		t.Fatalf("unexpected original journal before quarantine: transfers=%d err=%v", len(before), err)
	}
	evidenceBase.mu.Lock()
	broadcasts := evidenceBase.broadcastCount
	evidenceBase.mu.Unlock()
	result, refreshErr := owner.RefreshRGB11State(ctx)
	var fault rgb11wallet.ProofFault
	wantCause := fmt.Sprintf("RGB11 allocation %s has an unvalidated mempool spender", ownerCarrier)
	if !errors.Is(refreshErr, ErrRGB11Inconsistent) || !errors.As(refreshErr, &fault) ||
		fault.OutPoint != ownerCarrier || fault.AssetName != issued.AssetName || fault.Cause == nil || fault.Cause.Error() != wantCause ||
		result == nil || len(result.Inconsistent) != 1 || result.Inconsistent[0] != ownerCarrier {
		t.Fatalf("expected only the owner's unvalidated RGB carrier %s to be quarantined: result=%+v err=%v", ownerCarrier, result, refreshErr)
	}
	staleView, err := owner.GetRGB11State()
	if err != nil || staleView == nil || rgb11AssetAmount(staleView.AvailableAssets, issued.AssetName) != 0 {
		t.Fatalf("stale owner must not make the unvalidated asset spendable: err=%v", err)
	}
	after, err := owner.rgbManager.projectionStore.ListTransfers()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("unvalidated mempool spend changed the owner's persisted journal: transfers=%d err=%v", len(after), err)
	}

	walletID, err := owner.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := owner.rgbManager.exportRGB11WalletSnapshot(walletID); !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatalf("outbound backup ignored the unresolved owner proof: %v", err)
	}

	// The send requested one confirmation, so both devices may use this
	// proof once the same core/history and current-chain checks pass.
	setRGB11ProjectionTestTxConfirmations(t, evidence, remoteTxID, 1)
	if _, err := peer.RefreshRGB11State(ctx); err != nil {
		t.Fatalf("refresh remote peer at one confirmation: %v", err)
	}
	peerState, err := peer.GetRGB11State()
	if err != nil || rgb11AssetAmount(peerState.AvailableAssets, issued.AssetName) != 35 {
		t.Fatalf("one-confirmation peer did not expose its locally permitted current asset: state=%v err=%v", peerState, err)
	}
	// Before synchronization, the original device must not invent the peer's
	// new journal merely from the Bitcoin outspend.
	staleTransfers, err := owner.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		t.Fatal(err)
	}
	for _, transfer := range staleTransfers {
		if transfer.TransferID == remotePrepared.State.TransferID {
			t.Fatal("stale device invented an unsynchronized peer journal")
		}
	}
	if err := peer.SyncAccountManagementState(ctx); err != nil {
		t.Fatalf("publish the peer's confirmed journal: %v", err)
	}
	published := rgb11LocalModuleForTest(t, peer)
	if published.Baseline == nil || published.Confirmed != published.Generation || published.Restoring {
		t.Fatal("peer did not publish a confirmed account payload")
	}
	catalog, err := owner.accountManagedDataCatalog()
	if err != nil || len(catalog.Scopes) != 2 || catalog.Scopes[0].AccountIndex != 0 || catalog.Scopes[1].AccountIndex != 1 {
		t.Fatalf("missing ordered provider scopes: %v %v", catalog.Scopes, err)
	}
	// A failure after scope 0 really flushed must undo all its effects
	// (including new Q/fee rows) without removing an unrelated reservation.
	const untouched = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:0"
	if err := owner.utxoLockerL1.TryReserve([]string{untouched}, "test-reserved", "unrelated"); err != nil {
		t.Fatal(err)
	}
	base, err := owner.rgbManager.readRGB11SelectedBase(walletID)
	if err != nil {
		t.Fatal(err)
	}
	beforeLocks := owner.utxoLockerL1.GetLockedUtxoList()
	payloads, err := accountTestPayloads(peer, rgb11AccountManagedProviderID)
	if err != nil || len(payloads) != 2 {
		t.Fatalf("read authenticated peer scopes: count=%d err=%v", len(payloads), err)
	}
	failed := &failNthFlushDB{KVDB: owner.db, failAt: 2}
	func() {
		owner.db = failed
		defer func() { owner.db = failed.KVDB }()
		provider := &rgb11AccountManagedDataProvider{owner: owner}
		err = provider.Import(catalog, []AccountManagedDataPayload{payloads[catalog.Scopes[0].ID()], payloads[catalog.Scopes[1].ID()]})
	}()
	if err == nil || failed.count != 3 || failed.hits != 1 {
		t.Fatalf("expected apply, failed second flush, and rollback: flushes=%d hit=%d err=%v", failed.count, failed.hits, err)
	}
	restored, err := owner.rgbManager.readRGB11SelectedBase(walletID)
	if err != nil || rgb11SnapshotChanged(base.snapshot, restored.snapshot) || !base.matches(restored.faults) ||
		!reflect.DeepEqual(beforeLocks, owner.utxoLockerL1.GetLockedUtxoList()) || owner.GetRGB11ConsistencyStatus() != "broken" {
		t.Fatalf("later scope failure lost the previous records, quarantine or carrier locks: %v", err)
	}

	// The peer already published the one-confirmation closure, but the owner
	// still sees its witness in the mempool. Account sync must freeze this
	// exact authenticated body, not replace it with another or clear its fault.
	setRGB11ProjectionTestTxConfirmations(t, evidence, remoteTxID, 0)
	if err := owner.SyncAccountManagementState(ctx); err == nil || !accountModuleOnly(err) {
		t.Fatalf("owner accepted the confirmed journal against its unconfirmed chain view: %v", err)
	}
	frozen := rgb11LocalModuleForTest(t, owner)
	if !frozen.Restoring || !bytes.Equal(frozen.Value, published.Baseline.Value) {
		t.Fatal("failed account import did not preserve the verified remote body and restore marker")
	}
	setRGB11ProjectionTestTxConfirmations(t, evidence, remoteTxID, 1)
	if err := owner.SyncAccountManagementState(ctx); err != nil {
		t.Fatalf("first import at one confirmation, without owner refresh: %v", err)
	}
	done := rgb11LocalModuleForTest(t, owner)
	if done.Restoring || !bytes.Equal(done.Value, frozen.Value) || done.Confirmed != done.Generation {
		t.Fatal("account sync did not finish the same frozen import")
	}
	state, err := owner.GetRGB11State()
	if err != nil || state == nil || state.ConsistencyStatus != "ok" ||
		rgb11AssetAmount(state.AvailableAssets, issued.AssetName) != 35 ||
		rgb11AssetAmount(state.PendingAssets, issued.AssetName) != 0 {
		t.Fatalf("first confirmed import did not expose exactly the verified change: state=%v err=%v", state, err)
	}
	old, err := owner.rgbManager.projectionStore.LoadProof(ownerCarrier, issued.AssetName)
	if err != nil || old == nil || old.Status != "spending" {
		t.Fatalf("spent original carrier missing from history: proof=%v err=%v", old, err)
	}
	inputs, _, _, _, _, total, err := owner.rgbManager.selectRGB11AllocationsOnce(issued.ContractID, 35, 1, nil)
	if err != nil || len(inputs) != 1 || inputs[0].proof.OutPoint != remoteChange || total != 35 {
		t.Fatalf("first confirmed import selected something other than the new change: inputs=%d total=%d err=%v", len(inputs), total, err)
	}
	evidenceBase.mu.Lock()
	afterBroadcast := evidenceBase.broadcastCount
	evidenceBase.mu.Unlock()
	if afterBroadcast != broadcasts {
		t.Fatalf("recovery changed broadcasts: before=%d after=%d", broadcasts, afterBroadcast)
	}
	proof, err := owner.rgbManager.projectionStore.LoadProof(remoteChange, issued.AssetName)
	if err != nil || proof.Confirmations != 1 {
		t.Fatalf("owner remote proof confirmations=%v err=%v, want 1", proof, err)
	}
	ownerTransfers, err := owner.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		t.Fatal(err)
	}
	remoteJournalRestored := false
	for _, transfer := range ownerTransfers {
		if transfer.TransferID == remotePrepared.State.TransferID {
			remoteJournalRestored = true
			if transfer.MinConfirmations != 1 || transfer.Status != "settled" {
				t.Fatal("complete recovery lost the peer journal confirmation policy or lifecycle")
			}
		}
	}
	if !remoteJournalRestored {
		t.Fatal("complete recovery dropped the peer send journal")
	}
}

func newRGB11ProjectionReadinessTestManager(t *testing.T) *Manager {
	t.Helper()
	wallet, _, err := NewInteralWallet(&chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11FlowManager(t, wallet, nil, rgb11ManagedRecoveryEvidence{}, 580)
	pauseRGB11ProjectionReadinessReconciliation(manager)
	return manager
}

func pauseRGB11ProjectionReadinessReconciliation(manager *Manager) {
	manager.rgbManager.scopeStates.mu.Lock()
	manager.rgbManager.scopeStates.retryDelay = time.Hour
	manager.rgbManager.scopeStates.mu.Unlock()
}

func setRGB11ProjectionTestTxConfirmations(t *testing.T, evidence *rgb11AddressEvidence,
	txid string, confirmations int64) {
	t.Helper()
	evidence.rgb11FlowEvidence.mu.Lock()
	for outpoint, utxo := range evidence.rgb11FlowEvidence.utxos {
		if allocationOutpointTxID(outpoint) == txid {
			utxo.Confirmations = confirmations
		}
	}
	evidence.rgb11FlowEvidence.mu.Unlock()
	evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{
		TxID: txid, Confirmed: confirmations > 0, InMempool: confirmations == 0,
		Confirmations: confirmations,
	})
}
