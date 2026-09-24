//go:build rgb11discard

package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11StaleOwnerPlan(t *testing.T) {
	sender, _, _, _, _ := newRGB11GenericSendFixture(t)
	accountID, err := dkvsAccountID(sender.wallet)
	if err != nil {
		t.Fatal(err)
	}
	targetInput := wire.OutPoint{Hash: chainhash.Hash{2}, Index: 1}
	feeInput, err := wire.NewOutPointFromString(rgb11OwnerFeeInput)
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&targetInput, nil, nil))
	tx.AddTxIn(wire.NewTxIn(feeInput, nil, nil))
	tx.AddTxOut(wire.NewTxOut(1, []byte{0x51}))
	tx.AddTxOut(wire.NewTxOut(1, []byte{0x51}))
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	txid := tx.TxHash().String()
	asset := *indexer.NewAssetNameFromString(rgb11StaleLockOnlyTarget.AssetName)
	keepAsset := *indexer.NewAssetNameFromString(rgb11OwnerKeepAsset)
	target := RGB11StaleLockTarget{
		ReceiverAccountID: accountID, TransferID: "rgb:csg:test#stale-owner",
		AssetName: asset.String(), OutPoint: targetInput.String(), SpendingTxID: txid,
	}
	state := rgb11wallet.TransferState{
		TransferID: target.TransferID, Direction: "send", Status: "settled", AckStatus: "accepted",
		Asset:       indexer.AssetInfo{Name: asset, Amount: *indexer.NewDefaultDecimal(1)},
		WitnessTxID: txid, InputOutPoints: []string{target.OutPoint, rgb11OwnerFeeInput},
		OutputOutPoints: []string{txid + ":1"}, RelayDurability: "STANDARD_PROXY",
	}
	pending := &rgb11wallet.PendingTransfer{
		State: state, SignedTx: raw.Bytes(), ReservationID: "stale-owner", CreatedAt: time.Now().Unix(),
	}
	protectedOutput := indexer.NewTxOutput(1)
	protectedOutput.OutPointStr = rgb11OwnerKeepPoint
	protectedOutput.Assets = append(protectedOutput.Assets, indexer.AssetInfo{
		Name: keepAsset, Amount: *indexer.NewDefaultDecimal(1),
	})
	status := &rgb11wallet.BitcoinTxStatus{
		TxID: txid, Confirmed: true, Confirmations: 6, BlockHeight: 123,
		BlockHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	evidence := expectedSpendEvidence{status: status, raw: raw.Bytes()}
	source := rgb11OwnerSource{
		accountID: accountID, wallet: sender.wallet, walletID: 1,
		pending: pending, transfers: []*rgb11wallet.TransferState{&state},
		targetProof: &rgb11wallet.AllocationProof{
			OutPoint: target.OutPoint, AssetName: asset, Status: "spending",
		},
		protectedProof: &rgb11wallet.AllocationProof{
			OutPoint: rgb11OwnerKeepPoint, AssetName: keepAsset, Status: "settled",
		},
		protectedOutput: protectedOutput,
		protectedLock: &LockedUtxo{
			LockedTime: 123, Reason: rgb11wallet.LockReasonRGB,
		},
		protectedAmount: indexer.NewDefaultDecimal(1),
		getOutspend: func(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
			if outpoint == rgb11OwnerKeepPoint {
				return &rgb11wallet.BitcoinOutspend{}, nil
			}
			return evidence.GetOutspend(outpoint)
		}, getRawTx: evidence.GetRawTx,
		getTxStatus: evidence.GetTxStatus,
	}
	plan, err := buildRGB11OwnerPlan(target, source, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetLockState != "absent" || plan.SpendingVin != 0 ||
		plan.ProtectedAmountRaw != "1" || plan.ReservationIDHash == "" {
		t.Fatalf("plan=%+v", plan)
	}
	pending.ReservationID = ""
	after, err := buildRGB11OwnerPlan(target, source, plan.ReservationIDHash)
	if err != nil || after == nil || *after != *plan {
		t.Fatalf("cleared plan changed: after=%+v err=%v", after, err)
	}
	source.protectedAmount = indexer.NewDefaultDecimal(0)
	if _, err := buildRGB11OwnerPlan(target, source, plan.ReservationIDHash); err == nil {
		t.Fatal("protected c57 balance mutation was accepted")
	}
}

func TestRGB11ClearStaleOwner(t *testing.T) {
	fixture := seedRGB11StaleOwner(t)
	proof, err := fixture.manager.rgbManager.projectionStore.LoadProof(
		fixture.input, fixture.pending.State.Asset.Name,
	)
	if err != nil {
		t.Fatal(err)
	}
	proof.Status = "spending"
	if err := fixture.manager.rgbManager.projectionStore.SaveProofState(proof); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(fixture.pending.ReservationID))
	ownerHash := hex.EncodeToString(digest[:])
	if err := fixture.manager.clearRGB11StaleOwner(
		fixture.pending.State.TransferID, ownerHash,
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.clearRGB11StaleOwner(
		fixture.pending.State.TransferID, ownerHash,
	); err != nil {
		t.Fatalf("idempotent owner clear: %v", err)
	}
	result, err := fixture.manager.RefreshRGB11State(context.Background())
	if err != nil || result == nil || result.Unresolved != 0 ||
		fixture.manager.GetRGB11ConsistencyStatus() != "ok" {
		t.Fatalf("refresh after owner clear: result=%+v err=%v consistency=%s",
			result, err, fixture.manager.GetRGB11ConsistencyStatus())
	}
	stored, err := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(
		fixture.pending.State.TransferID,
	)
	if err != nil || stored.ReservationID != "" || stored.State.Status != "settled" ||
		stored.State.WitnessTxID != fixture.pending.State.WitnessTxID {
		t.Fatalf("settled journal changed: pending=%+v err=%v", stored, err)
	}
	if lock := fixture.manager.utxoLockerL1.GetLockedUtxoList()[fixture.input]; lock != nil {
		t.Fatalf("spent input lock was recreated: %+v", lock)
	}
	proof, err = fixture.manager.rgbManager.projectionStore.LoadProof(
		fixture.input, fixture.pending.State.Asset.Name,
	)
	if err != nil || proof.Status != "spending" {
		t.Fatalf("historical proof changed: proof=%+v err=%v", proof, err)
	}
}

func TestRGB11OwnerClearRejects(t *testing.T) {
	fixture := seedRGB11StaleOwner(t)
	wrong := sha256.Sum256([]byte("another-owner"))
	if err := fixture.manager.clearRGB11StaleOwner(
		fixture.pending.State.TransferID, hex.EncodeToString(wrong[:]),
	); err == nil {
		t.Fatal("different reservation owner was cleared")
	}
	stored, err := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(
		fixture.pending.State.TransferID,
	)
	if err != nil || stored.ReservationID != fixture.pending.ReservationID {
		t.Fatalf("rejected clear changed journal: pending=%+v err=%v", stored, err)
	}
	if lock := fixture.manager.utxoLockerL1.GetLockedUtxoList()[fixture.input]; lock != nil &&
		lock.Reason == rgb11wallet.LockReasonRGB {
		t.Fatalf("rejected clear changed lock: %+v", lock)
	}
}
