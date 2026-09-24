//go:build rgb11discard

package wallet

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11LockDiscardPlan(t *testing.T) {
	_, recipient, _, _, _ := newRGB11GenericSendFixture(t)
	receiverID, err := dkvsAccountID(recipient.wallet)
	if err != nil {
		t.Fatal(err)
	}
	sourceHash := chainhash.Hash{1}
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: sourceHash, Index: 1}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(1, []byte{0x51}))
	tx.AddTxOut(wire.NewTxOut(1, []byte{0x51}))
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	outpoint := tx.TxIn[0].PreviousOutPoint.String()
	txid := tx.TxHash().String()
	asset := *indexer.NewAssetNameFromString("rgb11:f:r2direct@gvml24je")
	target := RGB11StaleLockTarget{
		ReceiverAccountID: receiverID, TransferID: "rgb:csg:test#lock-discard",
		AssetName: asset.String(), OutPoint: outpoint, SpendingTxID: txid,
	}
	transfer := &rgb11wallet.TransferState{
		TransferID: target.TransferID, Direction: "send",
		Asset:          indexer.AssetInfo{Name: asset, Amount: *indexer.NewDefaultDecimal(1)},
		InputOutPoints: []string{outpoint}, OutputOutPoints: []string{txid + ":1"},
		WitnessTxID: txid, Status: "settled", AckStatus: "accepted",
		RelayDurability: "STANDARD_PROXY",
	}
	source := rgb11LockDiscardSource{
		accountID: receiverID, wallet: recipient.wallet,
		lock:     &LockedUtxo{LockedTime: 123, Reason: rgb11wallet.LockReasonRGB},
		transfer: transfer, transfers: []*rgb11wallet.TransferState{transfer},
		getOutspend: func(string) (*rgb11wallet.BitcoinOutspend, error) {
			return &rgb11wallet.BitcoinOutspend{Spent: true}, nil
		},
		getRawTx: func(string) ([]byte, error) { return raw.Bytes(), nil },
		getTxStatus: func(string) (*rgb11wallet.BitcoinTxStatus, error) {
			return &rgb11wallet.BitcoinTxStatus{
				TxID: txid, Confirmed: true, Confirmations: 6, BlockHeight: 123,
				BlockHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			}, nil
		},
	}
	plan, err := buildRGB11LockPlan(target, source)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SpendingVin != 0 || plan.TransferAmountRaw != "1" || plan.LockReason != "rgb" {
		t.Fatalf("plan=%+v", plan)
	}

	source.lock.Assets = append(source.lock.Assets, indexer.AssetInfo{Name: asset})
	if _, err := buildRGB11LockPlan(target, source); err == nil {
		t.Fatal("lock carrying assets was accepted")
	}
	source.lock.Assets = nil
	source.transfer.Status = "pending"
	if _, err := buildRGB11LockPlan(target, source); err == nil {
		t.Fatal("pending transfer was accepted")
	}
	source.transfer.Status = "settled"
	source.balance = indexer.NewDefaultDecimal(1)
	if _, err := buildRGB11LockPlan(target, source); err == nil {
		t.Fatal("nonzero target balance was accepted")
	}
	source.balance = nil
	source.reservationJSON = []string{"reserved " + outpoint}
	if _, err := buildRGB11LockPlan(target, source); err == nil {
		t.Fatal("matching reservation was accepted")
	}
}

func TestRGB11LockDiscardApply(t *testing.T) {
	approved := RGB11StaleLockPlan{Target: RGB11StaleLockTarget{OutPoint: "target:1"}}
	var err error
	approved.Fingerprint, err = rgb11LockFingerprint(approved)
	if err != nil {
		t.Fatal(err)
	}
	present := true
	deleteCalls := 0
	ops := rgb11LockApplyOps{
		plan: func() (*RGB11StaleLockPlan, error) {
			if !present {
				return nil, errRGB11LockGone
			}
			copy := approved
			return &copy, nil
		},
		delete: func() error { deleteCalls++; present = false; return nil },
		absent: func() bool { return !present },
	}
	if err := applyRGB11LockDiscard(approved, ops); err != nil {
		t.Fatal(err)
	}
	if err := applyRGB11LockDiscard(approved, ops); err != nil {
		t.Fatal(err)
	}
	if deleteCalls != 1 {
		t.Fatalf("delete calls=%d", deleteCalls)
	}
	ops.absent = func() bool { return false }
	if err := applyRGB11LockDiscard(approved, ops); err == nil || errors.Is(err, errRGB11LockGone) {
		t.Fatalf("missing readback rejection err=%v", err)
	}
}

func TestRGB11LockDiscardStore(t *testing.T) {
	database := newMemoryKVDB()
	locker := NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
	locker.Init()
	target := "69fa:1"
	protected := "c57:1"
	if err := locker.LockUtxo(target, rgb11wallet.LockReasonRGB); err != nil {
		t.Fatal(err)
	}
	if err := locker.LockUtxo(protected, rgb11wallet.LockReasonRGB); err != nil {
		t.Fatal(err)
	}
	lock := locker.GetLockedUtxoList()[target]
	plan := RGB11StaleLockPlan{
		Target: RGB11StaleLockTarget{OutPoint: target}, LockTime: lock.LockedTime,
		LockReason: lock.Reason, LockValue: lock.Value,
	}
	manager := &Manager{db: database, utxoLockerL1: locker}
	if err := manager.discardRGB11RawLock(plan); err != nil {
		t.Fatal(err)
	}
	if current := locker.GetLockedUtxoList(); current[target] != nil || current[protected] == nil {
		t.Fatalf("locks after exact discard=%+v", current)
	}
	restarted := NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
	restarted.Init()
	if current := restarted.GetLockedUtxoList(); current[target] != nil || current[protected] == nil {
		t.Fatalf("persisted locks after exact discard=%+v", current)
	}
	manager.utxoLockerL1 = restarted
	if err := manager.discardRGB11RawLock(plan); err != nil {
		t.Fatalf("idempotent exact discard: %v", err)
	}
}

func TestRGB11MaintReadsProdLock(t *testing.T) {
	oldMode, oldEnv, oldChain := _mode, _env, _chain
	_mode, _env, _chain = LIGHT_NODE, "prd", "testnet"
	t.Cleanup(func() { _mode, _env, _chain = oldMode, oldEnv, oldChain })
	database := newMemoryKVDB()
	outpoint := rgb11StaleLockOnlyTarget.OutPoint
	production := NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
	production.Init()
	if err := production.LockUtxo(outpoint, rgb11wallet.LockReasonRGB); err != nil {
		t.Fatal(err)
	}
	want := production.GetLockedUtxoList()[outpoint]
	maintenance := NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
	maintenance.Init()
	got := maintenance.GetLockedUtxoList()[outpoint]
	if got == nil || got.LockedTime != want.LockedTime || got.Reason != want.Reason ||
		got.Value != want.Value || len(got.Assets) != 0 {
		t.Fatalf("maintenance lock=%+v production lock=%+v", got, want)
	}
}

func TestRGB11MaintUnlockLockView(t *testing.T) {
	sender, recipient, imported, _, _ := newRGB11GenericSendFixture(t)
	balance, err := sender.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil || balance == nil || balance.Value.Sign() <= 0 {
		t.Fatalf("balance=%v err=%v", balance, err)
	}
	request, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
		AmountRaw: balance.Value.String(), WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
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
	pending.State.Status = "settled"
	pending.State.AckStatus = "accepted"
	if err := sender.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	input := pending.State.InputOutPoints[0]
	if err := sender.utxoLockerL1.UnlockUtxo(input); err != nil {
		t.Fatal(err)
	}
	if err := sender.utxoLockerL1.LockUtxo(input, rgb11wallet.LockReasonRGB); err != nil {
		t.Fatal(err)
	}
	if err := sender.rgbManager.rebuildRGB11Locks(); !errors.Is(err, ErrUtxoReservationOwner) {
		t.Fatalf("rebuild err=%v", err)
	}
	lock := sender.utxoLockerL1.GetLockedUtxoList()[input]
	if lock == nil || lock.Reason != rgb11wallet.LockReasonRGB || lock.ReservationID != "" {
		t.Fatalf("maintenance rebuild hid production lock: %+v", lock)
	}
}

func TestDiscardFreshSpentView(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })

	manager := newAccountManagementAutoTestManager(t)
	if _, err := manager.ImportWallet(rgb11ManagedOperationTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	outpoint, assetName, _ := seedRGB11DurableAccountRecoveryState(t, manager.rgbManager)
	proof, err := manager.rgbManager.projectionStore.LoadProof(outpoint, assetName)
	if err != nil {
		t.Fatal(err)
	}
	proof.Status = "spending"
	if err := manager.rgbManager.projectionStore.SaveProofState(proof); err != nil {
		t.Fatal(err)
	}
	parsed, err := wire.NewOutPointFromString(outpoint)
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(parsed, nil, nil))
	tx.AddTxOut(wire.NewTxOut(900, []byte{0x51}))
	tx.AddTxOut(wire.NewTxOut(800, []byte{0x51}))
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	txid := tx.TxHash().String()
	receiverID, err := dkvsAccountID(manager.wallet)
	if err != nil {
		t.Fatal(err)
	}
	target := RGB11StaleLockTarget{
		ReceiverAccountID: receiverID, TransferID: "fresh-spent-view",
		AssetName: assetName.String(), OutPoint: outpoint, SpendingTxID: txid,
	}
	oldTarget := rgb11StaleLockOnlyTarget
	rgb11StaleLockOnlyTarget = target
	t.Cleanup(func() { rgb11StaleLockOnlyTarget = oldTarget })
	pending := &rgb11wallet.PendingTransfer{
		State: rgb11wallet.TransferState{
			TransferID: "fresh-spent-view", Direction: "send", Status: "settled",
			AckStatus: "accepted", Asset: indexer.AssetInfo{
				Name: assetName, Amount: *indexer.NewDefaultDecimal(1),
			},
			WitnessTxID: txid, InputOutPoints: []string{outpoint},
			OutputOutPoints: []string{txid + ":1"},
			RelayDurability: "STANDARD_PROXY",
		},
		SignedTx: raw.Bytes(), ReservationID: "fresh-spent-owner", CreatedAt: time.Now().Unix(),
	}
	if err := manager.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	if err := manager.utxoLockerL1.UnlockUtxo(outpoint); err != nil {
		t.Fatal(err)
	}
	protected := "c57fb2b8b73f2ee5febb0a593eb681c90eb813c6f7d3500f04d46b711a28589d:1"
	if err := manager.utxoLockerL1.LockUtxo(protected, rgb11wallet.LockReasonRGB); err != nil {
		t.Fatal(err)
	}
	rawLock := &LockedUtxo{LockedTime: 1790102034, Reason: rgb11wallet.LockReasonRGB}
	if err := saveLockedUtxo(manager.db, L1_NETWORK_BITCOIN, outpoint, rawLock); err != nil {
		t.Fatal(err)
	}
	manager.wallet = nil
	for _, info := range manager.walletInfoMap {
		info.Wallet = nil
	}
	manager.clearAccountManagementSession()

	if _, err := manager.UnlockRGB11Discard("password"); err != nil {
		t.Fatal(err)
	}
	if lock := manager.utxoLockerL1.GetLockedUtxoList()[outpoint]; lock != nil {
		t.Fatalf("fixture locker view unexpectedly saw raw stale lock: %+v", lock)
	}
	base := &rgb11FlowEvidence{
		utxos: make(map[string]*rgb11wallet.BitcoinUTXO),
		rawTx: map[string][]byte{txid: raw.Bytes()}, spendingTx: map[string]string{outpoint: "unknown"},
	}
	manager.rgbManager.evidence = &rgb11AddressEvidence{
		rgb11FlowEvidence: base,
		statuses: map[string]*rgb11wallet.BitcoinTxStatus{txid: {
			TxID: txid, Confirmed: true, Confirmations: 6, BlockHeight: 123,
			BlockHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
	}
	plan, err := manager.PlanRGB11StaleLock(target)
	if err != nil {
		t.Fatalf("fresh first stale plan: %v", err)
	}
	if plan.LockTime != rawLock.LockedTime || plan.LockReason != rawLock.Reason {
		t.Fatalf("plan raw lock mismatch: %+v", plan)
	}
	if err := manager.ApplyRGB11StaleLock(*plan); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyRGB11StaleLock(*plan); err != nil {
		t.Fatalf("idempotent stale apply: %v", err)
	}
	restarted := NewUtxoLocker(manager.db, nil, L1_NETWORK_BITCOIN)
	restarted.Init()
	locks := restarted.GetLockedUtxoList()
	if locks[outpoint] != nil || locks[protected] == nil {
		t.Fatalf("restarted raw locks=%+v", locks)
	}
}
