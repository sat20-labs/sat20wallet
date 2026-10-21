package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/rgb11/seals"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// A peer's recovery package contains chain projection, not its device-local
// send journal. Importing it must replace stale proof state before the first
// public read, so the previous change and its confirmed successor are never
// both spendable in the projection.
func TestRGB11RecoveryProjectionMergesSuccessiveSnapshotsWithoutJournal(t *testing.T) {
	runRGB11RecoveryProjectionPeerSpend(t, rgb11ProjectionScenario{peerStartsAtS0: true})
}

func TestRGB11RecoveryProjectionFirstReadUsesPeerSpentChangeWithoutJournal(t *testing.T) {
	runRGB11RecoveryProjectionPeerSpend(t, rgb11ProjectionScenario{})
}

func TestRGB11RecoveryProjectionFirstReadRestoresChangeAfterPeerSpendReorg(t *testing.T) {
	runRGB11RecoveryProjectionPeerSpend(t, rgb11ProjectionScenario{peerSpendReorg: true})
}

func TestRGB11RecoveryProjectionReorgAllowsReplacementSpend(t *testing.T) {
	runRGB11RecoveryProjectionPeerSpend(t, rgb11ProjectionScenario{
		peerSpendReorg: true, replacementSpend: true,
	})
}

func TestRGB11RecoveryProjectionQuarantinesMissingRGBConsumer(t *testing.T) {
	runRGB11RecoveryProjectionPeerSpend(t, rgb11ProjectionScenario{bitcoinSpendOnly: true})
}

func TestRGB11RecoveryProofWitnessMustMatchFullOutpoint(t *testing.T) {
	const producer = "00000000000000000000000000000000000000000000000000000000000012c0"
	const wrongProducer = "00000000000000000000000000000000000000000000000000000000000012c1"
	proof := &rgb11wallet.AllocationProof{
		OutPoint: producer + ":0", WitnessTxID: producer,
	}
	if got, err := rgb11RecoveryProofOutpoint(proof); err != nil || got != producer {
		t.Fatalf("valid proof producer from full outpoint=%q err=%v", got, err)
	}
	proof.WitnessTxID = wrongProducer
	if _, err := rgb11RecoveryProofOutpoint(proof); err == nil {
		t.Fatal("proof with a witness txid unrelated to its full outpoint was accepted")
	}
}

func TestRGB11LockPlanMovesAndRetiresOnlySelectedReservationOwners(t *testing.T) {
	database := newMemoryKVDB()
	locker := NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
	const outpoint = "00000000000000000000000000000000000000000000000000000000000012d0:0"
	if err := locker.LockUtxo(outpoint, rgb11wallet.LockReasonRGB); err != nil {
		t.Fatal(err)
	}
	if err := locker.TryReserve([]string{outpoint}, rgb11wallet.LockReasonPending, "old-owner",
		rgb11wallet.LockReasonRGB); err != nil {
		t.Fatal(err)
	}

	apply := func(plan rgb11LockPlan) error {
		t.Helper()
		batch := database.NewWriteBatch()
		if batch == nil {
			return ErrRGB11Inconsistent
		}
		defer batch.Close()
		finish, err := locker.stageRGB11LockPlan(batch, plan)
		if err != nil {
			return err
		}
		if err := batch.Flush(); err != nil {
			if finish != nil {
				finish(false)
			}
			return err
		}
		if finish != nil {
			finish(true)
		}
		return nil
	}

	if err := apply(rgb11LockPlan{
		ReplacedOwners: map[string]struct{}{"old-owner": {}},
		DesiredOwners: map[string]rgb11ReservationLockTarget{
			outpoint: {ReservationID: "new-owner", PreviousReason: rgb11wallet.LockReasonRGB},
		},
	}); err != nil {
		t.Fatalf("replace old reservation owner in staged commit: %v", err)
	}
	lock := locker.GetLockedUtxoList()[outpoint]
	if lock == nil || lock.ReservationID != "new-owner" ||
		lock.Reason != rgb11wallet.LockReasonPending ||
		lock.ReservationPreviousReason != rgb11wallet.LockReasonRGB {
		t.Fatalf("replacement lock=%+v", lock)
	}

	if err := apply(rgb11LockPlan{
		ReplacedOwners: map[string]struct{}{"new-owner": {}},
		Reasons:        map[string]string{outpoint: rgb11wallet.LockReasonRGB},
	}); err != nil {
		t.Fatalf("retire cancelled reservation owner: %v", err)
	}
	lock = locker.GetLockedUtxoList()[outpoint]
	if lock == nil || lock.ReservationID != "" || lock.Reason != rgb11wallet.LockReasonRGB ||
		lock.ReservationPreviousReason != "" {
		t.Fatalf("retired lock=%+v", lock)
	}
}

func TestRGB11LockPlanPreservesAndRejectsUnrelatedCarrierLocks(t *testing.T) {
	const outpoint = "00000000000000000000000000000000000000000000000000000000000012d1:0"
	for _, tc := range []struct {
		name      string
		consumed  bool
		seed      func(*UtxoLocker) error
		wantError error
	}{
		{
			name: "manual lock without reservation owner",
			seed: func(locker *UtxoLocker) error {
				return locker.LockUtxo(outpoint, "user-manual")
			},
			wantError: ErrUtxoReserved,
		},
		{
			name:     "consumed carrier with external owner",
			consumed: true,
			seed: func(locker *UtxoLocker) error {
				return locker.TryReserve([]string{outpoint}, rgb11wallet.LockReasonRGB, "outside-owner")
			},
			wantError: ErrUtxoReservationOwner,
		},
		{
			name: "external reservation owner",
			seed: func(locker *UtxoLocker) error {
				return locker.TryReserve([]string{outpoint}, rgb11wallet.LockReasonPending, "outside-owner")
			},
			wantError: ErrUtxoReservationOwner,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := newMemoryKVDB()
			locker := NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
			if err := tc.seed(locker); err != nil {
				t.Fatal(err)
			}
			before := locker.GetLockedUtxoList()[outpoint]
			beforeDB, err := database.Read([]byte(GetLockedUtxoKey(L1_NETWORK_BITCOIN, outpoint)))
			if err != nil || before == nil {
				t.Fatalf("load seeded lock: lock=%+v err=%v", before, err)
			}

			batch := database.NewWriteBatch()
			if batch == nil {
				t.Fatal("new write batch returned nil")
			}
			consumed := make(map[string]struct{})
			if tc.consumed {
				consumed[outpoint] = struct{}{}
			}
			_, stageErr := locker.stageRGB11LockPlan(batch, rgb11LockPlan{
				ReplacedOwners: map[string]struct{}{},
				DesiredOwners:  map[string]rgb11ReservationLockTarget{},
				Consumed:       consumed,
				Reasons:        map[string]string{outpoint: rgb11wallet.LockReasonRGB},
			})
			batch.Close()
			if !errors.Is(stageErr, tc.wantError) {
				t.Fatalf("stage collision error=%v, want %v", stageErr, tc.wantError)
			}
			after := locker.GetLockedUtxoList()[outpoint]
			afterDB, err := database.Read([]byte(GetLockedUtxoKey(L1_NETWORK_BITCOIN, outpoint)))
			if err != nil || !reflect.DeepEqual(before, after) || !bytes.Equal(beforeDB, afterDB) {
				t.Fatalf("unrelated carrier lock changed after rejected candidate: before=%+v after=%+v readErr=%v",
					before, after, err)
			}
			manager := &Manager{utxoLockerL1: locker}
			if !manager.isL1SendInputProtected(outpoint) {
				t.Fatal("ordinary BTC input gate did not protect the locked RGB carrier")
			}
			if rgb11CarrierLockAllowsSpend(after) {
				t.Fatal("RGB selection treated an unrelated carrier lock as spendable")
			}
		})
	}
}

func TestRGB11CarrierAvailabilityRequiresUnreservedRGBLock(t *testing.T) {
	for _, tc := range []struct {
		name string
		lock *LockedUtxo
		want bool
	}{
		{name: "unlocked", lock: nil, want: true},
		{name: "ordinary RGB carrier", lock: &LockedUtxo{Reason: rgb11wallet.LockReasonRGB}, want: true},
		{name: "pending RGB reservation", lock: &LockedUtxo{Reason: rgb11wallet.LockReasonPending, ReservationID: "operation"}},
		{name: "manual lock", lock: &LockedUtxo{Reason: "user-manual"}},
		{name: "foreign owner even with RGB reason", lock: &LockedUtxo{Reason: rgb11wallet.LockReasonRGB, ReservationID: "outside-owner"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rgb11CarrierLockAllowsSpend(tc.lock); got != tc.want {
				t.Fatalf("carrier availability=%v, want %v for lock %+v", got, tc.want, tc.lock)
			}
		})
	}
}

func TestRGB11RecoveryProjectionStatusRPCErrorKeepsPriorProjectionAndLocks(t *testing.T) {
	runRGB11RecoveryProjectionPeerSpend(t, rgb11ProjectionScenario{statusRPCError: true})
}

func TestRGB11RecoveryProjectionTipChangeKeepsPriorProjectionAndLocks(t *testing.T) {
	runRGB11RecoveryProjectionPeerSpend(t, rgb11ProjectionScenario{tipChanges: true})
}

func TestRGB11RecoveryProjectionFinalTipChangeKeepsPriorProjectionAndLocks(t *testing.T) {
	runRGB11RecoveryProjectionPeerSpend(t, rgb11ProjectionScenario{finalTipChanges: true})
}

func TestRGB11RecoveryProjectionSameTipOutspendChangeKeepsPriorProjectionAndLocks(t *testing.T) {
	runRGB11RecoveryProjectionPeerSpend(t, rgb11ProjectionScenario{sameTipOutspendChanges: true})
}

func TestRGB11RecoveryProjectionFlushFailureKeepsPriorStateAcrossRestart(t *testing.T) {
	runRGB11RecoveryProjectionPeerSpend(t, rgb11ProjectionScenario{batchFlushFailure: true})
}

func TestRGB11RecoveryProjectionRejectsReceiptAmountMismatchAgainstConsignment(t *testing.T) {
	runRGB11RecoveryProjectionPeerSpend(t, rgb11ProjectionScenario{receiptAmountMismatch: true})
}

type rgb11ProjectionScenario struct {
	peerStartsAtS0         bool
	peerSpendReorg         bool
	statusRPCError         bool
	tipChanges             bool
	finalTipChanges        bool
	sameTipOutspendChanges bool
	batchFlushFailure      bool
	receiptAmountMismatch  bool
	replacementSpend       bool
	bitcoinSpendOnly       bool
}

func runRGB11RecoveryProjectionPeerSpend(t *testing.T, scenario rgb11ProjectionScenario) {
	t.Helper()
	ctx := context.Background()
	ownerWallet, _, err := NewInteralWallet(&chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	receiverWallet, _, err := NewInteralWallet(&chaincfg.TestNet4Params)
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
		seedTx := wire.NewMsgTx(wire.TxVersion)
		seedTx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: wire.MaxPrevOutIndex},
			[]byte{1, byte(index + 1)}, nil))
		seedTx.AddTxOut(wire.NewTxOut(100_000, ownerScript))
		var rawSeed bytes.Buffer
		if err := seedTx.Serialize(&rawSeed); err != nil {
			t.Fatal(err)
		}
		producerTxID := seedTx.TxHash().String()
		outpoint := fmt.Sprintf("%s:%d", producerTxID, 0)
		utxo := &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: 100_000,
			PkScript: append([]byte(nil), ownerScript...), Confirmations: 6,
		}
		evidenceBase.utxos[outpoint] = utxo
		output := indexer.NewTxOutput(utxo.Value)
		output.OutPointStr = outpoint
		output.OutValue.PkScript = append([]byte(nil), ownerScript...)
		rpc.outputs[outpoint] = output
		evidenceBase.rawTx[producerTxID] = append([]byte(nil), rawSeed.Bytes()...)
		evidence.setStatus(producerTxID, rgb11wallet.BitcoinTxStatus{
			TxID: producerTxID, Confirmed: true, Confirmations: 6,
		})
		rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
			OutPoint: outpoint, Value: utxo.Value, PkScript: append([]byte(nil), ownerScript...),
		})
	}

	ownerDBPath := t.TempDir()
	owner := newRGB11FlowManagerAt(t, ownerWallet, rpc, evidence, 480, ownerDBPath)
	receiver := newRGB11FlowManager(t, receiverWallet, rpc, evidence, 482)
	for _, manager := range []*Manager{owner, receiver} {
		manager.rgbManager.scopeStates.stopReconciliations()
		manager.rgbManager.scopeStates.mu.Lock()
		manager.rgbManager.scopeStates.retryDelay = time.Hour
		manager.rgbManager.scopeStates.mu.Unlock()
	}

	// Exercise projection/state-machine behavior below the public account-managed
	// facade. The facade's recovery gate is covered separately.
	issued, err := owner.rgbManager.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "RRG", Name: "Recovery Projection Test",
		Precision: 2, Amounts: []uint64{100},
	})
	if err != nil {
		t.Fatalf("issue asset: %v", err)
	}
	if _, err := owner.RefreshRGB11State(ctx); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	s0 := exportRGB11RecoveryForTest(t, owner)
	var peer *Manager
	if scenario.peerStartsAtS0 {
		peer = newRGB11FlowManager(t, ownerWallet.Clone(), rpc, evidence, 481)
		peer.rgbManager.scopeStates.stopReconciliations()
		peer.rgbManager.scopeStates.mu.Lock()
		peer.rgbManager.scopeStates.retryDelay = time.Hour
		peer.rgbManager.scopeStates.mu.Unlock()
		if err := importRGB11ProjectionSnapshot(t, peer, s0); err != nil {
			t.Fatalf("import initial recovery snapshot: %v", err)
		}
		peerTransfers, err := peer.rgbManager.projectionStore.ListTransfers()
		if err != nil || len(peerTransfers) != 0 {
			t.Fatalf("cold peer has local transfers: count=%d err=%v", len(peerTransfers), err)
		}
	}

	firstInvoice, err := receiver.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "40", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	firstPrepared, err := owner.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: firstInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatalf("prepare first transfer: %v", err)
	}
	firstPending, firstTxID := confirmRGB11ProjectionTestTransfer(
		t, owner, evidence, rpc, firstPrepared.State.TransferID,
	)
	if _, err := receiver.rgbManager.AcceptRGB11Consignment(ctx, firstInvoice.RequestID, firstPending.RecipientConsignment); err != nil {
		t.Fatalf("accept first transfer: %v", err)
	}
	if _, err := owner.RefreshRGB11State(ctx); err != nil {
		t.Fatalf("refresh after first transfer: %v", err)
	}
	if len(firstPending.ChangeSeals) != 1 {
		t.Fatalf("first transfer change outputs=%d", len(firstPending.ChangeSeals))
	}
	pOutpoint := fmt.Sprintf("%s:%d", firstTxID, firstPending.ChangeSeals[0].Vout)
	firstBalance, err := owner.GetRGB11AssetBalance(&issued.AssetName)
	if err != nil || firstBalance == nil || firstBalance.Value.Uint64() != 60 {
		t.Fatalf("owner balance after first transfer: balance=%v err=%v", firstBalance, err)
	}
	s1 := exportRGB11RecoveryForTest(t, owner)
	if !scenario.peerStartsAtS0 {
		// This peer has never seen S0. It restores the complete selected closure,
		// then spends the confirmed change from that snapshot.
		peer = newRGB11FlowManager(t, ownerWallet.Clone(), rpc, evidence, 481)
		peer.rgbManager.scopeStates.stopReconciliations()
		peer.rgbManager.scopeStates.mu.Lock()
		peer.rgbManager.scopeStates.retryDelay = time.Hour
		peer.rgbManager.scopeStates.mu.Unlock()
	}
	if err := importRGB11ProjectionSnapshot(t, peer, s1); err != nil {
		t.Fatalf("import recovery snapshot: %v", err)
	}
	peerTransfers, err := peer.rgbManager.projectionStore.ListTransfers()
	if err != nil || len(peerTransfers) != 1 || peerTransfers[0].Direction != "send" ||
		peerTransfers[0].TransferID != firstPrepared.State.TransferID || peerTransfers[0].Status != "settled" {
		t.Fatalf("recovered peer transfer closure mismatch: transfers=%+v err=%v", peerTransfers, err)
	}
	if !rgb11ProjectionTestHasSettledProof(t, peer, pOutpoint) {
		t.Fatalf("peer did not recover the confirmed change proof")
	}

	secondInvoice, err := receiver.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "25", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	secondPrepared, err := peer.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: secondInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatalf("prepare peer transfer: %v", err)
	}
	secondPending, secondTxID := confirmRGB11ProjectionTestTransfer(
		t, peer, evidence, rpc, secondPrepared.State.TransferID,
	)
	usesP := false
	for _, input := range secondPending.State.InputOutPoints {
		usesP = usesP || input == pOutpoint
	}
	if !usesP {
		t.Fatalf("peer transfer did not spend the recovered change output")
	}
	if _, err := receiver.rgbManager.AcceptRGB11Consignment(ctx, secondInvoice.RequestID, secondPending.RecipientConsignment); err != nil {
		t.Fatalf("accept peer transfer: %v", err)
	}
	if _, err := peer.RefreshRGB11State(ctx); err != nil {
		t.Fatalf("peer refresh after transfer: %v", err)
	}
	if len(secondPending.ChangeSeals) != 1 {
		t.Fatalf("peer transfer change outputs=%d", len(secondPending.ChangeSeals))
	}
	qOutpoint := fmt.Sprintf("%s:%d", secondTxID, secondPending.ChangeSeals[0].Vout)
	peerBalance, err := peer.GetRGB11AssetBalance(&issued.AssetName)
	if err != nil || peerBalance == nil || peerBalance.Value.Uint64() != 35 {
		t.Fatalf("peer confirmed balance: balance=%v err=%v", peerBalance, err)
	}
	if !rgb11ProjectionTestHasSettledProof(t, peer, qOutpoint) {
		t.Fatalf("peer did not retain its confirmed change proof")
	}
	peerSnapshot := exportRGB11RecoveryForTest(t, peer)
	completePeerSnapshot := append([]byte(nil), peerSnapshot...)
	if scenario.bitcoinSpendOnly {
		// The current Bitcoin spender is confirmed, but this older complete
		// snapshot contains no consignment proving an RGB transition consumed P.
		peerSnapshot = s1
	}
	if scenario.receiptAmountMismatch {
		forgedSource := newRGB11FlowManager(t, ownerWallet.Clone(), rpc, evidence, 483)
		if err := forgedSource.rgbManager.projectionStore.SetScope("receipt-amount-mismatch"); err != nil {
			t.Fatalf("select independent forged-snapshot scope: %v", err)
		}
		peerSnapshot = makeRGB11ProjectionReceiptAmountMismatchSnapshot(
			t, peer, forgedSource, evidence, issued.AssetName, qOutpoint, secondPending.ChangeSeals,
		)
	}
	var priorState *RGB11State
	var priorBalance uint64
	var priorLocks map[string]*LockedUtxo
	var priorRecoverySnapshot *RGB11WalletSnapshot
	var priorPersistedRecords map[string][]byte
	if scenario.statusRPCError || scenario.tipChanges || scenario.finalTipChanges || scenario.sameTipOutspendChanges || scenario.batchFlushFailure || scenario.receiptAmountMismatch ||
		scenario.bitcoinSpendOnly {
		priorState, err = owner.GetRGB11State()
		if err != nil {
			t.Fatalf("capture prior state: %v", err)
		}
		priorDecimal, balanceErr := owner.GetRGB11AssetBalance(&issued.AssetName)
		if balanceErr != nil || priorDecimal == nil {
			t.Fatalf("capture prior balance: balance=%v err=%v", priorDecimal, balanceErr)
		}
		priorBalance = priorDecimal.Value.Uint64()
		priorLocks = owner.utxoLockerL1.GetLockedUtxoList()
		priorPersistedRecords = snapshotRGB11ProjectionDB(t, owner.db)
		priorRecoverySnapshot = decodeRGB11ProjectionRecoverySnapshot(t, exportRGB11RecoveryForTest(t, owner))
		if scenario.batchFlushFailure {
			if priorBalance != 60 {
				t.Fatalf("pre-failure owner balance=%d, want old projection 60", priorBalance)
			}
			if priorLocks[pOutpoint] == nil {
				t.Fatal("pre-failure owner did not retain the previous-change carrier lock")
			}
		}
	}
	if scenario.peerSpendReorg {
		setRGB11ProjectionPeerSpendReorg(t, evidence, rpc, firstPending, pOutpoint, secondPending, secondTxID)
	}
	var statusFault *rgb11ProjectionStatusErrorEvidence
	var tipChange *rgb11ProjectionTipChangeEvidence
	var finalTipChange *rgb11ProjectionFinalTipChangeEvidence
	var outspendChange *rgb11ProjectionSameTipOutspendEvidence
	if scenario.statusRPCError {
		statusFault = &rgb11ProjectionStatusErrorEvidence{
			BitcoinEvidenceProvider: evidence, txid: secondTxID,
		}
		owner.rgbManager.evidence = statusFault
	}
	if scenario.tipChanges {
		tipChange = &rgb11ProjectionTipChangeEvidence{BitcoinEvidenceProvider: evidence}
		owner.rgbManager.evidence = tipChange
	}
	if scenario.finalTipChanges {
		finalTipChange = &rgb11ProjectionFinalTipChangeEvidence{BitcoinEvidenceProvider: evidence}
		owner.rgbManager.evidence = finalTipChange
	}
	if scenario.sameTipOutspendChanges {
		outspendChange = &rgb11ProjectionSameTipOutspendEvidence{
			// Mutate at the publication recheck after the resolver's initial read.
			BitcoinEvidenceProvider: evidence, target: qOutpoint, changeAt: 2,
			spendingTx: secondTxID,
		}
		owner.rgbManager.evidence = outspendChange
	}

	var failDB *rgb11ProjectionFailOnceDB
	if scenario.batchFlushFailure {
		failDB = &rgb11ProjectionFailOnceDB{KVDB: owner.db}
		owner.db = failDB
	}
	importErr := importRGB11ProjectionSnapshot(t, owner, peerSnapshot)
	if scenario.bitcoinSpendOnly {
		if importErr != nil {
			t.Fatalf("safe incomplete-history import: %v", importErr)
		}
		proof, err := owner.rgbManager.projectionStore.LoadProof(pOutpoint, issued.AssetName)
		if err != nil || proof.Status != "inconsistent" || owner.GetRGB11ConsistencyStatus() != "broken" {
			t.Fatal("missing consumer was treated as spendable ownership")
		}
		if rgb11ProjectionTestHasProof(t, owner, qOutpoint) {
			t.Fatal("missing consignment fabricated a successor allocation")
		}
		state, err := owner.GetRGB11State()
		if err != nil || rgb11AssetAmount(state.AvailableAssets, issued.AssetName) != 0 {
			t.Fatal("quarantined ownership was spendable")
		}
		// This was a real RGB spend all along: absence from the older bundle
		// must be recoverable, not mislabeled irreversible non-RGB burn.
		if err := importRGB11ProjectionSnapshot(t, owner, completePeerSnapshot); err != nil {
			t.Fatalf("later complete consumer history: %v", err)
		}
		proof, err = owner.rgbManager.projectionStore.LoadProof(pOutpoint, issued.AssetName)
		if err != nil || proof.Status != "spending" || owner.GetRGB11ConsistencyStatus() != "ok" {
			t.Fatal("later valid consumer did not repair classification")
		}
		balance, err := owner.GetRGB11AssetBalance(&issued.AssetName)
		if err != nil || balance == nil || balance.Value.Uint64() != 35 {
			t.Fatal("complete history did not restore exact existing balance 35")
		}
		return
	}
	if scenario.batchFlushFailure {
		if !errors.Is(importErr, errRGB11ProjectionInjectedFlushFailure) {
			t.Fatalf("recovery import did not return the injected batch failure: %v", importErr)
		}
		if calls := failDB.flushCount(); calls != 1 {
			t.Fatalf("recovery import flush attempts=%d, want exactly one", calls)
		}
		touches := failDB.failedBatchTouches()
		if touches.projection == 0 || touches.carrierLock == 0 {
			t.Fatalf("injected transaction did not stage projection and carrier lock together: projection=%d locks=%d",
				touches.projection, touches.carrierLock)
		}
		owner.db = failDB.KVDB
		assertRGB11ProjectionUnchanged(t, owner, issued.AssetName, priorState, priorBalance,
			priorLocks, priorPersistedRecords, priorRecoverySnapshot)

		// Reopen the same isolated DB from disk. The candidate import must not
		// have exposed a partial generation or released its prior carrier lock.
		owner.Close()
		if err := owner.db.Close(); err != nil {
			t.Fatalf("close owner DB before restart: %v", err)
		}
		owner.db = nil // prevent the fixture cleanup from closing this handle twice.
		reopened := newRGB11FlowManagerAt(t, ownerWallet.Clone(), rpc, evidence, 480, ownerDBPath)
		// The fixture helper normally marks new test managers verified. A
		// restart after a failed remote projection commit must instead start
		// unverified until its chain evidence is reconciled.
		reopened.rgbManager.consistencyStatus = ""
		reopened.utxoLockerL1.Init()
		reopenedState, stateErr := reopened.GetRGB11State()
		reopenedBalance, balanceErr := reopened.GetRGB11AssetBalance(&issued.AssetName)
		if stateErr != nil || balanceErr != nil || reopenedBalance == nil || reopenedBalance.Value.Uint64() != 60 {
			var got uint64
			if reopenedBalance != nil {
				got = reopenedBalance.Value.Uint64()
			}
			t.Fatalf("reopened projection balance=%d, want old 60: stateErr=%v balanceErr=%v",
				got, stateErr, balanceErr)
		}
		assertRGB11ProjectionCachedState(t, reopenedState, issued.AssetName, priorState.Assets, 60)
		if !reflect.DeepEqual(priorPersistedRecords, snapshotRGB11ProjectionDB(t, reopened.db)) {
			t.Fatal("reopened DB contains a partial recovery projection or changed durable records")
		}
		if !reflect.DeepEqual(priorLocks, reopened.utxoLockerL1.GetLockedUtxoList()) {
			t.Fatal("reopened DB changed prior carrier locks after failed import")
		}
		if !reflect.DeepEqual(priorRecoverySnapshot,
			decodeRGB11ProjectionRecoverySnapshot(t, exportRGB11RecoveryForTest(t, reopened))) {
			t.Fatal("reopened DB published a partial stable recovery snapshot")
		}
		return
	}
	if scenario.receiptAmountMismatch {
		if importErr == nil {
			t.Fatal("accepted a structurally valid snapshot whose allocation amount differs from its signed consignment")
		}
		assertRGB11ProjectionUnchanged(t, owner, issued.AssetName, priorState, priorBalance,
			priorLocks, priorPersistedRecords, priorRecoverySnapshot)
		return
	}
	if scenario.statusRPCError || scenario.tipChanges || scenario.finalTipChanges || scenario.sameTipOutspendChanges {
		var refreshErr error
		if scenario.sameTipOutspendChanges && importErr == nil {
			t.Fatal("same-tip changing outspend evidence was published")
		}
		if importErr == nil && !scenario.sameTipOutspendChanges {
			_, refreshErr = owner.RefreshRGB11State(ctx)
		}
		if statusFault != nil && statusFault.callCount() == 0 {
			t.Fatal("recovery projection did not query the peer spender status")
		}
		if tipChange != nil && tipChange.callCount() < 2 {
			t.Fatalf("recovery projection did not bracket evidence with a stable tip: calls=%d", tipChange.callCount())
		}
		if finalTipChange != nil && finalTipChange.callCount() < 3 {
			t.Fatalf("recovery projection did not recheck the tip at the publication boundary: calls=%d",
				finalTipChange.callCount())
		}
		if outspendChange != nil && outspendChange.callCount() < outspendChange.changeAt {
			t.Fatalf("same-tip outspend mutation was not observed during projection: calls=%d", outspendChange.callCount())
		}
		assertRGB11ProjectionUnchanged(t, owner, issued.AssetName, priorState, priorBalance,
			priorLocks, priorPersistedRecords, priorRecoverySnapshot)
		if importErr == nil && refreshErr == nil {
			t.Fatal("recovery projection accepted an incomplete or changing evidence view")
		}
		return
	}
	if importErr != nil {
		t.Fatalf("merge peer recovery snapshot: %v", importErr)
	}
	ownerTransfers, err := owner.rgbManager.projectionStore.ListTransfers()
	if err != nil || len(ownerTransfers) != 2 {
		t.Fatalf("owner recovery closure transfer count=%d, want local send plus selected peer successor: err=%v",
			len(ownerTransfers), err)
	}
	seenTransfers := make(map[string]bool, len(ownerTransfers))
	for _, transfer := range ownerTransfers {
		seenTransfers[transfer.TransferID] = true
	}
	if !seenTransfers[firstPrepared.State.TransferID] || !seenTransfers[secondPrepared.State.TransferID] {
		t.Fatalf("recovery closure omitted local or selected peer transfer: local=%v peer=%v rows=%+v",
			seenTransfers[firstPrepared.State.TransferID], seenTransfers[secondPrepared.State.TransferID], ownerTransfers)
	}
	pSpend, err := evidence.GetOutspend(pOutpoint)
	if scenario.peerSpendReorg {
		if err != nil || pSpend == nil || pSpend.Spent || pSpend.SpendingTx != "" {
			t.Fatalf("reorg did not restore previous change to unspent: spent=%v err=%v", pSpend != nil && pSpend.Spent, err)
		}
	} else if err != nil || pSpend == nil || !pSpend.Spent || pSpend.SpendingTx != secondTxID {
		t.Fatalf("previous change spend evidence mismatch: spent=%v err=%v", pSpend != nil && pSpend.Spent, err)
	}
	pSpendStatus, err := evidence.GetTxStatus(secondTxID)
	if scenario.peerSpendReorg {
		if err != nil || pSpendStatus == nil || pSpendStatus.TxID != secondTxID || pSpendStatus.Confirmed || pSpendStatus.InMempool {
			t.Fatalf("peer spender reorg status was not explicit: status=%+v err=%v", pSpendStatus, err)
		}
		if utxo, utxoErr := evidence.GetUTXO(pOutpoint); utxoErr != nil || utxo == nil || utxo.Confirmations < 6 {
			t.Fatalf("previous change is not freshly present after reorg: present=%v err=%v", utxo != nil, utxoErr)
		}
	} else if err != nil || pSpendStatus == nil || !pSpendStatus.Confirmed || pSpendStatus.Confirmations != 6 {
		t.Fatalf("peer spender is not confirmed: confirmed=%v err=%v", pSpendStatus != nil && pSpendStatus.Confirmed, err)
	}

	// This is deliberately the first public observation after recovery import.
	// No Refresh call may be needed to repair the projection before this read.
	state, stateErr := owner.GetRGB11State()
	balance, balanceErr := owner.GetRGB11AssetBalance(&issued.AssetName)
	wantBalance := uint64(35)
	if scenario.peerSpendReorg {
		wantBalance = 60
	}
	if stateErr != nil || balanceErr != nil || balance == nil || balance.Value.Uint64() != wantBalance ||
		(!scenario.peerSpendReorg && owner.GetRGB11ConsistencyStatus() != "ok") {
		var got uint64
		if balance != nil {
			got = balance.Value.Uint64()
		}
		t.Fatalf("first recovery read has balance=%d want=%d: stateErr=%v balanceErr=%v statePresent=%v consistency=%s",
			got, wantBalance, stateErr, balanceErr, state != nil, owner.GetRGB11ConsistencyStatus())
	}
	if scenario.peerSpendReorg {
		if !rgb11ProjectionTestHasProof(t, owner, qOutpoint) {
			t.Fatal("orphaned peer successor proof was deleted")
		}
		if err := owner.rgbManager.rebuildRGB11Locks(); err != nil {
			t.Fatalf("rebuild locks while peer spender remains reorged: %v", err)
		}
		_, _ = owner.RefreshRGB11State(ctx)
		assertRGB11ProjectionBalance(t, owner, issued.AssetName, 60)
		if !rgb11ProjectionTestHasProof(t, owner, qOutpoint) {
			t.Fatal("refresh deleted the orphaned peer successor proof")
		}
		if err := owner.rgbManager.rebuildRGB11Locks(); err != nil {
			t.Fatalf("rebuild locks while peer spender remains reorged: %v", err)
		}
		if scenario.replacementSpend {
			replacementInvoice, invoiceErr := receiver.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{
				Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
				AmountRaw: "25", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
			})
			if invoiceErr != nil {
				t.Fatal(invoiceErr)
			}
			replacement, prepareErr := owner.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{
				Invoice: replacementInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
			})
			if prepareErr != nil {
				t.Fatalf("prepare replacement spender: %v", prepareErr)
			}
			replacementPending, replacementTxID := confirmRGB11ProjectionTestTransfer(
				t, owner, evidence, rpc, replacement.State.TransferID,
			)
			usesP := false
			for _, input := range replacementPending.State.InputOutPoints {
				usesP = usesP || input == pOutpoint
			}
			if !usesP {
				t.Fatal("replacement spender did not consume the restored previous change")
			}
			if _, acceptErr := receiver.rgbManager.AcceptRGB11Consignment(ctx, replacementInvoice.RequestID, replacementPending.RecipientConsignment); acceptErr != nil {
				t.Fatalf("accept replacement spender: %v", acceptErr)
			}
			if _, refreshErr := owner.RefreshRGB11State(ctx); refreshErr != nil {
				t.Fatalf("refresh replacement spender: %v", refreshErr)
			}
			assertRGB11ProjectionBalance(t, owner, issued.AssetName, 35)
			if replacementTxID == secondTxID {
				t.Fatal("replacement spender reused the reorged transaction identity")
			}
			return
		}
		setRGB11ProjectionTestSpend(t, evidence, rpc, pOutpoint, secondPending, secondTxID)
		if _, refreshErr := owner.RefreshRGB11State(ctx); refreshErr != nil {
			t.Fatalf("refresh after peer spender reconfirmation: %v", refreshErr)
		}
		assertRGB11ProjectionBalance(t, owner, issued.AssetName, 35)
		return
	}

	refresh, err := owner.RefreshRGB11State(ctx)
	if err != nil || refresh == nil || len(refresh.Inconsistent) != 0 {
		t.Fatalf("post-import refresh found inconsistent state: resultPresent=%v inconsistent=%d err=%v",
			refresh != nil, func() int {
				if refresh == nil {
					return 0
				}
				return len(refresh.Inconsistent)
			}(), err)
	}
	finalBalance, err := owner.GetRGB11AssetBalance(&issued.AssetName)
	if err != nil || finalBalance == nil || finalBalance.Value.Uint64() != 35 {
		t.Fatalf("owner balance after refresh: balance=%v err=%v", finalBalance, err)
	}
}

func decodeRGB11ProjectionRecoverySnapshot(t *testing.T, encoded []byte) *RGB11WalletSnapshot {
	t.Helper()
	recovery, err := rgb11wallet.DecodeRecoveryPackage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := recovery.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

var errRGB11ProjectionInjectedFlushFailure = errors.New("injected RGB11 recovery batch flush failure")

type rgb11ProjectionFailOnceDB struct {
	indexer.KVDB
	mu            sync.Mutex
	calls         int
	failed        bool
	failedTouches rgb11ProjectionBatchTouches
}

type rgb11ProjectionFailOnceBatch struct {
	indexer.WriteBatch
	owner   *rgb11ProjectionFailOnceDB
	touches rgb11ProjectionBatchTouches
}

type rgb11ProjectionBatchTouches struct {
	projection  int
	engine      int
	reservation int
	carrierLock int
}

func (db *rgb11ProjectionFailOnceDB) NewWriteBatch() indexer.WriteBatch {
	return &rgb11ProjectionFailOnceBatch{WriteBatch: db.KVDB.NewWriteBatch(), owner: db}
}

func (db *rgb11ProjectionFailOnceDB) flushCount() int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.calls
}

func (batch *rgb11ProjectionFailOnceBatch) recordKey(key []byte) {
	if bytes.HasPrefix(key, []byte("rgb11-engine-")) {
		batch.touches.engine++
	} else if bytes.HasPrefix(key, []byte("rgb11-")) {
		batch.touches.projection++
	}
	if bytes.HasPrefix(key, []byte(GetDBKeyPrefix()+DB_KEY_RESV+RESV_TYPE_RGB11+"-")) {
		batch.touches.reservation++
	}
	if bytes.HasPrefix(key, []byte(GetDBKeyPrefix()+DB_KEY_LOCKEDUTXO)) {
		batch.touches.carrierLock++
	}
}

func (db *rgb11ProjectionFailOnceDB) failedBatchTouches() rgb11ProjectionBatchTouches {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.failedTouches
}

func (batch *rgb11ProjectionFailOnceBatch) Put(key, value []byte) error {
	batch.recordKey(key)
	return batch.WriteBatch.Put(key, value)
}

func (batch *rgb11ProjectionFailOnceBatch) Delete(key []byte) error {
	batch.recordKey(key)
	return batch.WriteBatch.Delete(key)
}

func (batch *rgb11ProjectionFailOnceBatch) Flush() error {
	batch.owner.mu.Lock()
	batch.owner.calls++
	fail := !batch.owner.failed
	if fail {
		batch.owner.failed = true
	}
	batch.owner.mu.Unlock()
	if fail {
		batch.owner.mu.Lock()
		batch.owner.failedTouches = batch.touches
		batch.owner.mu.Unlock()
		return errRGB11ProjectionInjectedFlushFailure
	}
	return batch.WriteBatch.Flush()
}

func snapshotRGB11ProjectionDB(t *testing.T, db indexer.KVDB) map[string][]byte {
	t.Helper()
	if db == nil {
		t.Fatal("RGB11 persistence DB is unavailable")
	}
	records := make(map[string][]byte)
	err := db.Scan(indexer.ScanOptions{CopyKey: true, CopyValue: true}, func(key, value []byte) error {
		records[string(key)] = append([]byte(nil), value...)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot RGB11 persistence records: %v", err)
	}
	return records
}

func makeRGB11ProjectionReceiptAmountMismatchSnapshot(t *testing.T, source, target *Manager,
	evidence rgb11wallet.BitcoinEvidenceProvider, assetName indexer.AssetName, outpoint string,
	reveals []seals.GraphBlindSeal) []byte {
	t.Helper()
	proof, err := source.rgbManager.projectionStore.LoadProof(outpoint, assetName)
	if err != nil {
		t.Fatalf("load real S1 allocation proof: %v", err)
	}
	output, err := source.rgbManager.projectionStore.LoadOutput(outpoint)
	if err != nil {
		t.Fatalf("load real S1 output: %v", err)
	}
	asset := output.GetAsset(&assetName)
	if asset == nil || asset.Value == nil || asset.Value.Sign() <= 0 {
		t.Fatal("real S1 output has no positive allocation for the target asset")
	}
	assetInfo, err := output.Assets.Find(&assetName)
	if err != nil {
		t.Fatalf("find source asset metadata: %v", err)
	}
	raw, err := source.rgbManager.projectionStore.LoadObject(proof.ConsignmentHash)
	if err != nil {
		t.Fatalf("load real signed S1 consignment: %v", err)
	}
	rawHash := sha256.Sum256(raw)
	if hex.EncodeToString(rawHash[:]) != proof.ConsignmentHash {
		t.Fatal("real S1 consignment does not match its persisted proof hash")
	}
	currentReceipt, err := rgb11wallet.ValidateWith(context.Background(),
		rgb11wallet.NewNativeConsensusValidatorWithReveals(reveals...), raw, evidence)
	if err != nil {
		t.Fatalf("validate real S1 consignment with native consensus: %v", err)
	}
	allocationIndex := -1
	for i := range currentReceipt.Allocations {
		allocation := &currentReceipt.Allocations[i]
		if allocation.OutPoint == outpoint && allocation.AssetName == assetName {
			allocationIndex = i
			break
		}
	}
	if allocationIndex < 0 || currentReceipt.Allocations[allocationIndex].Amount.Cmp(asset) != 0 {
		t.Fatal("native S1 allocation and persisted output do not agree")
	}

	// Keep every receipt field bound to the real raw consignment except for
	// the current allocation amount. Public storage APIs accept this
	// self-consistent cache; they must not treat it as native validation.
	forgedReceipt := *currentReceipt
	forgedReceipt.Allocations = append([]rgb11wallet.ValidatedAllocation(nil), currentReceipt.Allocations...)
	forgedAmount := indexer.NewDecimalWithScale(
		int64(asset.Value.Uint64()+1), asset.Precision,
	)
	if forgedAmount.Cmp(asset) == 0 {
		t.Fatal("forged receipt amount unexpectedly equals the real allocation")
	}
	forgedReceipt.Allocations[allocationIndex].Amount = *forgedAmount.Clone()
	storedReceipt, err := target.rgbManager.projectionStore.ValidateAndStoreConsignment(
		context.Background(), rgb11ProjectionReceiptValidator{receipt: &forgedReceipt}, evidence, raw,
	)
	if err != nil {
		t.Fatalf("store structurally valid forged receipt through public validator interface: %v", err)
	}
	receiptHash, err := storedReceipt.Hash()
	if err != nil {
		t.Fatalf("hash forged receipt: %v", err)
	}
	forgedOutput := output.Clone()
	forgedAsset := &indexer.AssetInfo{
		Name: assetName, Amount: *forgedAmount.Clone(), BindingSat: assetInfo.BindingSat,
	}
	forgedProof := *proof
	forgedProof.ValidationHash = receiptHash
	if err := target.rgbManager.projectionStore.CommitProjection(forgedOutput, forgedAsset, &forgedProof); err != nil {
		t.Fatalf("commit self-consistent forged projection through public store API: %v", err)
	}

	walletID, err := target.RGB11WalletID()
	if err != nil {
		t.Fatalf("read forged snapshot wallet identity: %v", err)
	}
	snapshot, _, err := target.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatalf("export forged snapshot: %v", err)
	}
	if err := rgb11wallet.ValidateWalletSnapshot(snapshot); err != nil {
		t.Fatalf("forged snapshot should be structurally self-consistent: %v", err)
	}
	recovery, err := rgb11wallet.RecoveryPackageFromSnapshot(snapshot, time.Now().Unix())
	if err != nil {
		t.Fatalf("create forged recovery package: %v", err)
	}
	encoded, err := rgb11wallet.EncodeRecoveryPackage(recovery)
	if err != nil {
		t.Fatalf("encode forged recovery snapshot: %v", err)
	}
	return encoded
}

type rgb11ProjectionReceiptValidator struct {
	receipt *rgb11wallet.ValidationReceipt
}

func (v rgb11ProjectionReceiptValidator) ValidateConsignment(context.Context, []byte,
	rgb11wallet.BitcoinEvidenceProvider) (*rgb11wallet.ValidationReceipt, error) {
	if v.receipt == nil {
		return nil, rgb11wallet.ErrValidationReceipt
	}
	copyReceipt := *v.receipt
	copyReceipt.Allocations = append([]rgb11wallet.ValidatedAllocation(nil), v.receipt.Allocations...)
	return &copyReceipt, nil
}

func importRGB11ProjectionSnapshot(t *testing.T, manager *Manager, encoded []byte) error {
	t.Helper()
	recovery, err := rgb11wallet.DecodeRecoveryPackage(encoded)
	if err != nil {
		return err
	}
	snapshot, err := recovery.WalletSnapshot()
	if err != nil {
		return err
	}
	return manager.rgbManager.importSelectedRGB11AccountRecoverySnapshot(snapshot)
}

func confirmRGB11ProjectionTestTransfer(t *testing.T, sender *Manager, evidence *rgb11AddressEvidence,
	rpc *rgb11FlowIndexer, transferID string) (*rgb11wallet.PendingTransfer, string) {
	t.Helper()
	if _, err := sender.rgbManager.BroadcastRGB11OutOfBand([]string{transferID}); err != nil {
		t.Fatalf("broadcast test transfer: %v", err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		t.Fatal(err)
	}
	pending.State.Status, pending.State.AckStatus = "broadcast", "accepted"
	if err := sender.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	witness := wire.NewMsgTx(wire.TxVersion)
	if err := witness.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	witnessTxID := witness.TxHash().String()
	base := evidence.rgb11FlowEvidence
	base.mu.Lock()
	base.rawTx[witnessTxID] = append([]byte(nil), pending.SignedTx...)
	for _, input := range witness.TxIn {
		outpoint := input.PreviousOutPoint.String()
		base.spendingTx[outpoint] = witnessTxID
		delete(base.utxos, outpoint)
		delete(rpc.outputs, outpoint)
	}
	rpc.plain = nil
	for vout, output := range witness.TxOut {
		outpoint := fmt.Sprintf("%s:%d", witnessTxID, vout)
		base.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: output.Value,
			PkScript: append([]byte(nil), output.PkScript...), Confirmations: 6,
		}
		view := indexer.NewTxOutput(output.Value)
		view.OutPointStr = outpoint
		view.OutValue.PkScript = append([]byte(nil), output.PkScript...)
		rpc.outputs[outpoint] = view
		rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
			OutPoint: outpoint, Value: output.Value, PkScript: append([]byte(nil), output.PkScript...),
		})
	}
	base.mu.Unlock()
	evidence.setStatus(witnessTxID, rgb11wallet.BitcoinTxStatus{
		TxID: witnessTxID, Confirmed: true, Confirmations: 6,
	})
	return pending, witnessTxID
}

func rgb11ProjectionTestHasSettledProof(t *testing.T, manager *Manager, outpoint string) bool {
	t.Helper()
	proofs, err := manager.rgbManager.projectionStore.ListProofs()
	if err != nil {
		t.Fatal(err)
	}
	for _, proof := range proofs {
		if proof != nil && proof.OutPoint == outpoint && proof.Status == "settled" && proof.Confirmations >= 6 {
			return true
		}
	}
	return false
}

func rgb11ProjectionTestHasProof(t *testing.T, manager *Manager, outpoint string) bool {
	t.Helper()
	proofs, err := manager.rgbManager.projectionStore.ListProofs()
	if err != nil {
		t.Fatal(err)
	}
	for _, proof := range proofs {
		if proof != nil && proof.OutPoint == outpoint {
			return true
		}
	}
	return false
}

func setRGB11ProjectionPeerSpendReorg(t *testing.T, evidence *rgb11AddressEvidence, rpc *rgb11FlowIndexer,
	firstPending *rgb11wallet.PendingTransfer, pOutpoint string,
	secondPending *rgb11wallet.PendingTransfer, secondTxID string) {
	t.Helper()
	if firstPending == nil || len(firstPending.ChangeSeals) != 1 || secondPending == nil {
		t.Fatal("missing confirmed transfer fixture for reorg")
	}
	firstTx := wire.NewMsgTx(wire.TxVersion)
	if err := firstTx.Deserialize(bytes.NewReader(firstPending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	vout := firstPending.ChangeSeals[0].Vout
	if int(vout) >= len(firstTx.TxOut) || fmt.Sprintf("%s:%d", firstTx.TxHash().String(), vout) != pOutpoint {
		t.Fatal("first transfer change outpoint does not match its signed transaction")
	}
	secondTx := wire.NewMsgTx(wire.TxVersion)
	if err := secondTx.Deserialize(bytes.NewReader(secondPending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	if secondTx.TxHash().String() != secondTxID {
		t.Fatal("second transfer txid does not match its signed transaction")
	}
	// Confirmation consumed every actual input, including fee inputs. Restore
	// each from its real confirmed producer, not just the RGB change carrier.
	restored := make(map[string]*rgb11wallet.BitcoinUTXO, len(secondTx.TxIn))
	for _, input := range secondTx.TxIn {
		outpoint := input.PreviousOutPoint.String()
		producerID := input.PreviousOutPoint.Hash.String()
		raw, err := evidence.GetRawTx(producerID)
		if err != nil {
			t.Fatalf("read reorg input producer %s: %v", outpoint, err)
		}
		producer := wire.NewMsgTx(wire.TxVersion)
		if err := producer.Deserialize(bytes.NewReader(raw)); err != nil ||
			producer.TxHash().String() != producerID || int(input.PreviousOutPoint.Index) >= len(producer.TxOut) {
			t.Fatalf("invalid reorg input producer %s: %v", outpoint, err)
		}
		status, err := evidence.GetTxStatus(producerID)
		if err != nil || !validRGB11ObservedTxStatus(status, producerID) || !status.Confirmed {
			t.Fatalf("reorg input producer %s is not confirmed: status=%+v err=%v", outpoint, status, err)
		}
		spend, err := evidence.GetOutspend(outpoint)
		if err != nil || spend == nil || !spend.Spent || spend.SpendingTx != secondTxID {
			t.Fatalf("reorg input %s was not spent by second transaction: spend=%+v err=%v", outpoint, spend, err)
		}
		if restored[outpoint] != nil {
			t.Fatalf("duplicate second transaction input %s", outpoint)
		}
		output := producer.TxOut[input.PreviousOutPoint.Index]
		restored[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: output.Value,
			PkScript: append([]byte(nil), output.PkScript...), Confirmations: status.Confirmations,
		}
	}
	if restored[pOutpoint] == nil {
		t.Fatal("second transaction does not spend the first RGB change")
	}
	base := evidence.rgb11FlowEvidence
	base.mu.Lock()
	for outpoint, utxo := range restored {
		delete(base.spendingTx, outpoint)
		base.utxos[outpoint] = utxo
	}
	for vout := range secondTx.TxOut {
		delete(base.utxos, fmt.Sprintf("%s:%d", secondTxID, vout))
	}
	base.mu.Unlock()
	evidence.setStatus(secondTxID, rgb11wallet.BitcoinTxStatus{
		TxID: secondTxID, Confirmed: false, InMempool: false,
	})
	rebuildRGB11ProjectionTestIndexerUTXOs(evidence.rgb11FlowEvidence, rpc)
}

func setRGB11ProjectionTestSpend(t *testing.T, evidence *rgb11AddressEvidence, rpc *rgb11FlowIndexer,
	pOutpoint string, pending *rgb11wallet.PendingTransfer, txid string) {
	t.Helper()
	if pending == nil {
		t.Fatal("missing confirmed transfer fixture")
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	if tx.TxHash().String() != txid {
		t.Fatal("spender txid does not match its signed transaction")
	}
	spendsChange := false
	for _, input := range tx.TxIn {
		spendsChange = spendsChange || input.PreviousOutPoint.String() == pOutpoint
	}
	if !spendsChange {
		t.Fatal("spender does not consume the expected RGB change")
	}
	base := evidence.rgb11FlowEvidence
	base.mu.Lock()
	for _, input := range tx.TxIn {
		outpoint := input.PreviousOutPoint.String()
		base.spendingTx[outpoint] = txid
		delete(base.utxos, outpoint)
	}
	for vout, output := range tx.TxOut {
		outpoint := fmt.Sprintf("%s:%d", txid, vout)
		base.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: output.Value,
			PkScript: append([]byte(nil), output.PkScript...), Confirmations: 6,
		}
	}
	base.mu.Unlock()
	evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{
		TxID: txid, Confirmed: true, Confirmations: 6,
	})
	rebuildRGB11ProjectionTestIndexerUTXOs(base, rpc)
}

func rebuildRGB11ProjectionTestIndexerUTXOs(base *rgb11FlowEvidence, rpc *rgb11FlowIndexer) {
	base.mu.Lock()
	points := make([]string, 0, len(base.utxos))
	for outpoint := range base.utxos {
		points = append(points, outpoint)
	}
	sort.Strings(points)
	rpc.outputs = make(map[string]*TxOutput, len(points))
	rpc.plain = make([]*indexerwire.TxOutputInfo, 0, len(points))
	for _, outpoint := range points {
		utxo := base.utxos[outpoint]
		view := indexer.NewTxOutput(utxo.Value)
		view.OutPointStr = outpoint
		view.OutValue.PkScript = append([]byte(nil), utxo.PkScript...)
		rpc.outputs[outpoint] = view
		rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
			OutPoint: outpoint, Value: utxo.Value, PkScript: append([]byte(nil), utxo.PkScript...),
		})
	}
	base.mu.Unlock()
}

func assertRGB11ProjectionBalance(t *testing.T, manager *Manager, name indexer.AssetName, want uint64) {
	t.Helper()
	balance, err := manager.GetRGB11AssetBalance(&name)
	if err != nil || balance == nil || balance.Value.Uint64() != want {
		t.Fatalf("RGB11 projection balance=%v want=%d err=%v", balance, want, err)
	}
}

func assertRGB11ProjectionCachedState(t *testing.T, after *RGB11State, name indexer.AssetName,
	beforeAssets indexer.TxAssets, wantBalance uint64) {
	t.Helper()
	if after == nil {
		t.Fatal("RGB11 state is unavailable after rejected evidence")
	}
	if !reflect.DeepEqual(beforeAssets, after.Assets) || rgb11AssetAmount(after.Assets, name) != wantBalance {
		t.Fatalf("cached RGB11 assets changed after rejected evidence: asset balance=%d want=%d",
			rgb11AssetAmount(after.Assets, name), wantBalance)
	}
	if rgb11AssetAmount(after.AvailableAssets, name) != 0 {
		t.Fatalf("unverified cached RGB11 balance remains spendable: available=%d",
			rgb11AssetAmount(after.AvailableAssets, name))
	}
}

func assertRGB11ProjectionUnchanged(t *testing.T, manager *Manager, name indexer.AssetName,
	before *RGB11State, beforeBalance uint64, beforeLocks map[string]*LockedUtxo,
	beforePersistedRecords map[string][]byte, beforeRecoverySnapshot *RGB11WalletSnapshot) {
	t.Helper()
	after, err := manager.GetRGB11State()
	if err != nil {
		t.Fatalf("read projection after rejected evidence: %v", err)
	}
	assertRGB11ProjectionCachedState(t, after, name, before.Assets, beforeBalance)
	assertRGB11ProjectionBalance(t, manager, name, beforeBalance)
	if !reflect.DeepEqual(beforePersistedRecords, snapshotRGB11ProjectionDB(t, manager.db)) {
		t.Fatal("durable projection, engine, ticker, reservation, or lock records changed after incomplete evidence")
	}
	if !reflect.DeepEqual(beforeLocks, manager.utxoLockerL1.GetLockedUtxoList()) {
		t.Fatal("UTXO locks changed after incomplete evidence")
	}
	if !reflect.DeepEqual(beforeRecoverySnapshot,
		decodeRGB11ProjectionRecoverySnapshot(t, exportRGB11RecoveryForTest(t, manager))) {
		t.Fatal("stable recovery snapshot changed after incomplete evidence")
	}
}

type rgb11ProjectionStatusErrorEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	txid  string
	mu    sync.Mutex
	calls int
}

func (e *rgb11ProjectionStatusErrorEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if txid != e.txid {
		return e.BitcoinEvidenceProvider.GetTxStatus(txid)
	}
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	return nil, errors.New("synthetic Bitcoin status read failure")
}

func (e *rgb11ProjectionStatusErrorEvidence) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type rgb11ProjectionTipChangeEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	mu    sync.Mutex
	calls int
}

type rgb11ProjectionFinalTipChangeEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	mu    sync.Mutex
	calls int
}

type rgb11ProjectionSameTipOutspendEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	mu         sync.Mutex
	target     string
	changeAt   int
	spendingTx string
	calls      int
}

func (e *rgb11ProjectionSameTipOutspendEvidence) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	e.mu.Lock()
	if outpoint == e.target {
		e.calls++
		if e.calls >= e.changeAt {
			e.mu.Unlock()
			return &rgb11wallet.BitcoinOutspend{Spent: true, SpendingTx: e.spendingTx}, nil
		}
	}
	e.mu.Unlock()
	return e.BitcoinEvidenceProvider.GetOutspend(outpoint)
}

func (e *rgb11ProjectionSameTipOutspendEvidence) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (e *rgb11ProjectionTipChangeEvidence) GetTip() (*rgb11wallet.BitcoinTip, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if e.calls == 1 {
		return &rgb11wallet.BitcoinTip{Height: 100, BlockHash: "projection-tip-a"}, nil
	}
	return &rgb11wallet.BitcoinTip{Height: 101, BlockHash: "projection-tip-b"}, nil
}

func (e *rgb11ProjectionTipChangeEvidence) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (e *rgb11ProjectionFinalTipChangeEvidence) GetTip() (*rgb11wallet.BitcoinTip, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if e.calls <= 2 {
		return &rgb11wallet.BitcoinTip{Height: 100, BlockHash: "projection-tip-a"}, nil
	}
	return &rgb11wallet.BitcoinTip{Height: 101, BlockHash: "projection-tip-b"}, nil
}

func (e *rgb11ProjectionFinalTipChangeEvidence) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}
