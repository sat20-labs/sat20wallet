package wallet

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11ProjectionReadinessRequiresFreshAndStableCurrentProofs(t *testing.T) {
	manager := newRGB11ProjectionReadinessTestManager(t)
	if manager.rgbManager.rgb11ProjectionAvailable() {
		t.Fatal("new manager treated restored state as freshly verified")
	}
	if manager.rgbManager.rgb11ProjectionStableConfirmed() {
		t.Fatal("unverified projection was eligible for stable publication")
	}

	// A freshly checked empty scope is stable; issuance or invoice work in a
	// scope with no ownership proof must not be blocked by the confirmation gate.
	manager.rgbManager.markRGB11ProjectionVerified()
	if !manager.rgbManager.rgb11ProjectionAvailable() || !manager.rgbManager.rgb11ProjectionStableConfirmed() {
		t.Fatal("fresh empty scope was not available for stable publication")
	}

	outpoint, assetName, _ := seedRGB11DurableAccountRecoveryState(t, manager.rgbManager)
	transfers, err := manager.rgbManager.projectionStore.ListTransfers()
	if err != nil || len(transfers) != 0 {
		t.Fatalf("test proof unexpectedly has a local transfer journal: count=%d err=%v", len(transfers), err)
	}
	proof, err := manager.rgbManager.projectionStore.LoadProof(outpoint, assetName)
	if err != nil {
		t.Fatal(err)
	}
	for _, confirmations := range []int64{0} {
		proof.Confirmations = confirmations
		if err := manager.rgbManager.projectionStore.SaveProofState(proof); err != nil {
			t.Fatal(err)
		}
		if manager.rgbManager.rgb11ProjectionStableConfirmed() {
			t.Fatalf("current proof with %d confirmations was spend-ready without meeting the existing minimum", confirmations)
		}
	}
	proof.Confirmations = 1
	if err := manager.rgbManager.projectionStore.SaveProofState(proof); err != nil {
		t.Fatal(err)
	}
	if !manager.rgbManager.rgb11ProjectionStableConfirmed() {
		t.Fatal("current proof meeting the default one-confirmation requirement was rejected")
	}

	for _, status := range []string{"spending", "inconsistent", "orphaned"} {
		proof.Status = status
		proof.Confirmations = 0
		if err := manager.rgbManager.projectionStore.SaveProofState(proof); err != nil {
			t.Fatal(err)
		}
		if !manager.rgbManager.rgb11ProjectionStableConfirmed() {
			t.Fatalf("non-current proof status %q blocked the stable frontier", status)
		}
	}

	manager.rgbManager.markRGB11ProjectionUnavailable()
	if manager.rgbManager.rgb11ProjectionAvailable() || manager.rgbManager.rgb11ProjectionStableConfirmed() {
		t.Fatal("unavailable projection remained available for use or stable publication")
	}
}

func TestRGB11ProjectionReadinessFailedRefreshRemainsUnavailable(t *testing.T) {
	manager := newRGB11ProjectionReadinessTestManager(t)
	outpoint, _, _ := seedRGB11DurableAccountRecoveryState(t, manager.rgbManager)
	manager.rgbManager.evidence = &rgb11ProjectionReadinessFailureEvidence{
		BitcoinEvidenceProvider: rgb11ManagedRecoveryEvidence{},
		outpoint:                outpoint,
	}
	manager.rgbManager.markRGB11ProjectionVerified()

	if _, err := manager.RefreshRGB11State(context.Background()); err == nil {
		t.Fatal("refresh with unavailable proof evidence unexpectedly succeeded")
	}
	if manager.rgbManager.rgb11ProjectionAvailable() {
		t.Fatal("failed refresh left cached ownership marked verified")
	}
}

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

func TestRGB11ProjectionReadinessRemoteConsumerUsesExistingMinConfirmations(t *testing.T) {
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
	pauseRGB11ProjectionReadinessReconciliation(owner)
	pauseRGB11ProjectionReadinessReconciliation(receiver)
	issued, err := owner.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "RRG", Name: "Projection Readiness Test",
		Precision: 2, Amounts: []uint64{100},
	})
	if err != nil {
		t.Fatalf("issue asset: %v", err)
	}
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

	peerRemote := newRGB11MemoryDKVSHTTP()
	peer := newRGB11ActivatedFlowAccount(t, ownerMnemonic, peerRemote, newRGB11MessageNodeClient(peerRemote), evidence, rpc)
	if peer.wallet.GetAddress() != ownerWallet.GetAddress() {
		t.Fatal("activated peer changed original owner identity")
	}
	pauseRGB11ProjectionReadinessReconciliation(peer)
	if err := importRGB11ProjectionSnapshot(t, peer, exportRGB11RecoveryForTest(t, owner)); err != nil {
		t.Fatalf("peer recovery import: %v", err)
	}
	peerTransfers, err := peer.rgbManager.projectionStore.ListTransfers()
	if err != nil || len(peerTransfers) != 1 || peerTransfers[0].TransferID != firstPrepared.State.TransferID {
		t.Fatalf("recovered peer lost the complete baseline journal: count=%d err=%v", len(peerTransfers), err)
	}
	if !peer.rgbManager.rgb11ProjectionAvailable() || !peer.rgbManager.rgb11ProjectionStableConfirmed() {
		t.Fatal("confirmed recovery import did not establish a fresh stable frontier")
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

	// A zero-confirmation output is pending and unavailable for spending on both
	// devices, including the stale device that has no new peer transfer journal.
	setRGB11ProjectionTestTxConfirmations(t, evidence, remoteTxID, 0)
	_, peerRefreshErr := peer.RefreshRGB11State(ctx)
	if peerRefreshErr != nil {
		if peer.rgbManager.rgb11ProjectionAvailable() || peer.rgbManager.rgb11ProjectionStableConfirmed() {
			t.Fatalf("failed zero-confirmation peer refresh left a publishable projection: %v", peerRefreshErr)
		}
	} else {
		if !peer.rgbManager.rgb11ProjectionAvailable() {
			t.Fatal("successful zero-confirmation peer refresh did not mark its current frontier fresh")
		}
		peerState, stateErr := peer.GetRGB11State()
		if stateErr != nil || rgb11AssetAmount(peerState.AvailableAssets, issued.AssetName) != 0 ||
			rgb11AssetAmount(peerState.PendingAssets, issued.AssetName) == 0 {
			t.Fatalf("zero-confirmation peer did not expose only pending ownership: state=%v err=%v", peerState, stateErr)
		}
		if peer.rgbManager.rgb11ProjectionStableConfirmed() {
			t.Fatal("zero-confirmation peer projection passed the stable publication gate")
		}
	}
	_, ownerRefreshErr := owner.RefreshRGB11State(ctx)
	if ownerRefreshErr != nil {
		if owner.rgbManager.rgb11ProjectionAvailable() || owner.rgbManager.rgb11ProjectionStableConfirmed() {
			t.Fatalf("failed zero-confirmation refresh without the peer journal remained publishable: %v", ownerRefreshErr)
		}
	} else if !owner.rgbManager.rgb11ProjectionAvailable() {
		t.Fatal("successful zero-confirmation refresh did not mark its complete frontier fresh")
	}
	if owner.rgbManager.rgb11ProjectionStableConfirmed() {
		t.Fatal("zero-confirmation remote spend without a local journal passed the stable publication gate")
	}

	// The invoice requested one confirmation, so both devices may use this
	// proof once the same core/history and current-chain checks pass.
	setRGB11ProjectionTestTxConfirmations(t, evidence, remoteTxID, 1)
	if _, err := peer.RefreshRGB11State(ctx); err != nil {
		t.Fatalf("refresh remote peer at one confirmation: %v", err)
	}
	if !peer.rgbManager.rgb11ProjectionAvailable() || !peer.rgbManager.rgb11ProjectionStableConfirmed() {
		t.Fatal("one-confirmation peer frontier did not meet the existing minimum")
	}
	peerState, err := peer.GetRGB11State()
	if err != nil || rgb11AssetAmount(peerState.AvailableAssets, issued.AssetName) == 0 {
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
	encoded := exportRGB11RecoveryForTest(t, peer)
	if err := importRGB11ProjectionSnapshot(t, owner, encoded); err != nil {
		t.Fatalf("owner import remote projection at one confirmation: %v", err)
	}
	if !owner.rgbManager.rgb11ProjectionAvailable() {
		t.Fatal("confirmed one-confirmation recovery import did not establish a fresh frontier")
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
	if !owner.rgbManager.rgb11ProjectionStableConfirmed() {
		t.Fatal("one-confirmation remote consumer projection did not meet the existing minimum after complete peer journal recovery")
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
