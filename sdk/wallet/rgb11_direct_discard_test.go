//go:build rgb11discard

package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	btcwire "github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestRGB11DiscardPlan(t *testing.T) {
	sender, recipient, imported, _, _ := newRGB11SendFixture(t, true)
	ctx := context.Background()
	request := RGB11AddressSendRequest{
		ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName,
		AmountRaw: "10000", FeeRate: 2, MinConfirmations: 1,
	}
	prepared, err := sender.rgbManager.prepareRGB11AddressBatch(ctx, []RGB11AddressSendRequest{request},
		dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	senderID, err := dkvsAccountID(sender.wallet)
	if err != nil {
		t.Fatal(err)
	}
	receiverID, err := dkvsAccountID(recipient.wallet)
	if err != nil {
		t.Fatal(err)
	}
	directID := "00000000000000000000000000000001"
	direct := &swire.DirectMessage{
		SenderAccount: senderID, RecipientAccount: receiverID, MessageID: directID,
	}
	key, err := dkvsindexer.MailMsgKey(receiverID, senderID, directID)
	if err != nil {
		t.Fatal(err)
	}
	record := &swire.DKVSRecord{Version: dkvsindexer.Version, Key: key, Seq: 1}
	item := &AccountDirectMessage{
		Payload: &AccountMessagePayload{
			ApplicationID: pending.State.AddressMessageID,
			Kind:          AccountMessageKindRGB11Consignment, Body: pending.RecipientConsignment,
		},
		Direct: direct, Record: record,
	}
	spending := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	outpoint := pending.State.WitnessTxID + ":1"
	target := RGB11DiscardTarget{
		ReceiverAccountID: receiverID, SenderAccountID: senderID,
		ApplicationID: pending.State.AddressMessageID, DirectMessageID: directID,
		TransferID: pending.State.TransferID, WitnessTxID: pending.State.WitnessTxID,
		OutPoint: outpoint, SpendingTxID: spending,
	}
	source := rgb11DiscardSource{
		accountID: receiverID, wallet: recipient.wallet,
		messages: []*AccountDirectMessage{item},
		getOutspend: func(string) (*rgb11wallet.BitcoinOutspend, error) {
			return &rgb11wallet.BitcoinOutspend{Spent: true, SpendingTx: spending}, nil
		},
	}
	plan, err := buildRGB11DiscardPlan(target, source)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RecordKey != record.Key || plan.Target != target || plan.Fingerprint == "" {
		t.Fatalf("plan=%+v", plan)
	}

	bad := target
	bad.WitnessTxID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := buildRGB11DiscardPlan(bad, source); err == nil {
		t.Fatal("accepted wrong witness target")
	}
	bad = target
	bad.SpendingTxID = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, err := buildRGB11DiscardPlan(bad, source); err == nil {
		t.Fatal("accepted wrong spender target")
	}
	source.hasTransfer = func(string) (bool, error) { return true, nil }
	if _, err := buildRGB11DiscardPlan(target, source); err == nil {
		t.Fatal("accepted discard with local transfer state")
	}
	source.hasTransfer = func(string) (bool, error) { return false, errors.New("read failed") }
	if _, err := buildRGB11DiscardPlan(target, source); err == nil {
		t.Fatal("accepted discard after transfer read failed")
	}
	source.hasTransfer = nil
	source.hasProof = func(string) (bool, error) { return false, errors.New("read failed") }
	if _, err := buildRGB11DiscardPlan(target, source); err == nil {
		t.Fatal("accepted discard after proof read failed")
	}
}

func TestRGB11DiscardSpendFallback(t *testing.T) {
	sender, recipient, imported, _, _ := newRGB11SendFixture(t, true)
	ctx := context.Background()
	prepared, err := sender.rgbManager.prepareRGB11AddressBatch(ctx, []RGB11AddressSendRequest{{
		ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName,
		AmountRaw: "10000", FeeRate: 2, MinConfirmations: 1,
	}}, dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	senderID, _ := dkvsAccountID(sender.wallet)
	receiverID, _ := dkvsAccountID(recipient.wallet)
	directID := "00000000000000000000000000000001"
	key, _ := dkvsindexer.MailMsgKey(receiverID, senderID, directID)
	item := &AccountDirectMessage{
		Payload: &AccountMessagePayload{ApplicationID: pending.State.AddressMessageID,
			Kind: AccountMessageKindRGB11Consignment, Body: pending.RecipientConsignment},
		Direct: &swire.DirectMessage{SenderAccount: senderID, RecipientAccount: receiverID, MessageID: directID},
		Record: &swire.DKVSRecord{Version: dkvsindexer.Version, Key: key, Seq: 1},
	}
	witnessHash, err := chainhash.NewHashFromStr(pending.State.WitnessTxID)
	if err != nil {
		t.Fatal(err)
	}
	spender := btcwire.NewMsgTx(btcwire.TxVersion)
	dummyHash := chainhash.Hash{1}
	spender.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Hash: dummyHash, Index: 0}, nil, nil))
	spender.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Hash: *witnessHash, Index: 1}, nil, nil))
	spender.AddTxOut(btcwire.NewTxOut(1, []byte{0x51}))
	var raw bytes.Buffer
	if err := spender.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	target := RGB11DiscardTarget{
		ReceiverAccountID: receiverID, SenderAccountID: senderID,
		ApplicationID: pending.State.AddressMessageID, DirectMessageID: directID,
		TransferID: pending.State.TransferID, WitnessTxID: pending.State.WitnessTxID,
		OutPoint: pending.State.WitnessTxID + ":1", SpendingTxID: spender.TxHash().String(),
	}
	source := rgb11DiscardSource{
		accountID: receiverID, wallet: recipient.wallet, messages: []*AccountDirectMessage{item},
		getOutspend: func(string) (*rgb11wallet.BitcoinOutspend, error) {
			return &rgb11wallet.BitcoinOutspend{Spent: true}, nil
		},
		getRawTx: func(string) ([]byte, error) { return raw.Bytes(), nil },
		getTxStatus: func(string) (*rgb11wallet.BitcoinTxStatus, error) {
			return &rgb11wallet.BitcoinTxStatus{
				TxID: target.SpendingTxID, Confirmed: true, Confirmations: 6,
				BlockHeight: 123, BlockHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			}, nil
		},
	}
	plan, err := buildRGB11DiscardPlan(target, source)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SpendEvidence != "confirmed-raw-tx" || plan.SpendingVin != 1 || plan.SpendBlockHeight != 123 {
		t.Fatalf("fallback plan=%+v", plan)
	}
	source.getTxStatus = func(string) (*rgb11wallet.BitcoinTxStatus, error) {
		return &rgb11wallet.BitcoinTxStatus{TxID: target.SpendingTxID}, nil
	}
	if _, err := buildRGB11DiscardPlan(target, source); err == nil {
		t.Fatal("unconfirmed fallback accepted")
	}
	source.getTxStatus = func(string) (*rgb11wallet.BitcoinTxStatus, error) {
		return &rgb11wallet.BitcoinTxStatus{
			TxID: target.SpendingTxID, Confirmed: true, Confirmations: 6,
			BlockHeight: 123, BlockHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}, nil
	}
	source.getOutspend = func(string) (*rgb11wallet.BitcoinOutspend, error) {
		return &rgb11wallet.BitcoinOutspend{Spent: true, SpendingTx: "different"}, nil
	}
	if _, err := buildRGB11DiscardPlan(target, source); err == nil {
		t.Fatal("explicit different spender accepted through fallback")
	}
	wrong := btcwire.NewMsgTx(btcwire.TxVersion)
	wrong.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Hash: dummyHash, Index: 0}, nil, nil))
	wrong.AddTxOut(btcwire.NewTxOut(1, []byte{0x51}))
	var wrongRaw bytes.Buffer
	if err := wrong.Serialize(&wrongRaw); err != nil {
		t.Fatal(err)
	}
	missing := target
	missing.SpendingTxID = wrong.TxHash().String()
	source.getOutspend = func(string) (*rgb11wallet.BitcoinOutspend, error) {
		return &rgb11wallet.BitcoinOutspend{Spent: true}, nil
	}
	source.getRawTx = func(string) ([]byte, error) { return wrongRaw.Bytes(), nil }
	source.getTxStatus = func(string) (*rgb11wallet.BitcoinTxStatus, error) {
		return &rgb11wallet.BitcoinTxStatus{
			TxID: missing.SpendingTxID, Confirmed: true, Confirmations: 6,
			BlockHeight: 123, BlockHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}, nil
	}
	if _, err := buildRGB11DiscardPlan(missing, source); err == nil {
		t.Fatal("confirmed transaction without target input accepted")
	}
}

func TestRGB11DiscardApply(t *testing.T) {
	approved := RGB11DiscardPlan{
		Target: RGB11DiscardTarget{ApplicationID: "app"}, RecordKey: "/mail/receiver/msg/sender/id",
	}
	var err error
	approved.Fingerprint, err = rgb11DiscardFingerprint(approved)
	if err != nil {
		t.Fatal(err)
	}
	deleted := false
	deleteCalls, markCalls := 0, 0
	ops := rgb11DiscardApplyOps{
		plan: func() (*RGB11DiscardPlan, error) {
			if deleted {
				return nil, errRGB11DiscardGone
			}
			copy := approved
			return &copy, nil
		},
		delete: func(string) error { deleteCalls++; deleted = true; return nil },
		confirm: func(string) error {
			if !deleted {
				return errors.New("remote record remains active")
			}
			return nil
		},
		cleanup: func() error { return nil },
		mark:    func() error { markCalls++; return nil },
	}
	if err := applyRGB11Discard(approved, ops); err != nil {
		t.Fatal(err)
	}
	if err := applyRGB11Discard(approved, ops); err != nil {
		t.Fatal(err)
	}
	if deleteCalls != 1 || markCalls != 2 {
		t.Fatalf("delete=%d mark=%d", deleteCalls, markCalls)
	}
	wrong := approved
	wrong.Fingerprint = "wrong"
	deleted = false
	if err := applyRGB11Discard(wrong, ops); err == nil || errors.Is(err, errRGB11DiscardGone) {
		t.Fatalf("wrong approved plan err=%v", err)
	}
}

func TestRGB11DiscardRemoteAck(t *testing.T) {
	transport := newSatoshiNetDKVSTestTransport()
	recipient, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	sender, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	receiverID := dkvsindexer.AccountID(recipient.PubKey().SerializeCompressed())
	senderID := dkvsindexer.AccountID(sender.PubKey().SerializeCompressed())
	key, err := dkvsindexer.MailMsgKey(receiverID, senderID, "00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	record := &swire.DKVSRecord{
		Version: dkvsindexer.Version, Key: key, Value: []byte("ciphertext"),
		Seq: 1, IssueHeight: 1, TTL: 100,
	}
	proof, err := dkvsindexer.NewFreeLocalFeeProof(key, "mail",
		uint32(dkvsindexer.RecordSize(record)), record.IssueHeight+record.TTL)
	if err != nil {
		t.Fatal(err)
	}
	record.FeeProof, err = dkvsindexer.EncodeFeeProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.indexer.PutInternalMailbox(record); err != nil {
		t.Fatal(err)
	}
	approved := RGB11DiscardPlan{RecordKey: key, RecordHash: dkvsindexer.RecordHash(record).String()}
	approved.Fingerprint, err = rgb11DiscardFingerprint(approved)
	if err != nil {
		t.Fatal(err)
	}
	cleaned, marked := false, false
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", transport)
	ops := rgb11DiscardApplyOps{
		plan: func() (*RGB11DiscardPlan, error) { copy := approved; return &copy, nil },
		delete: func(string) error {
			command, err := NewDKVSDeleteCommand(dkvsTestWalletFromPriv(t, recipient), record, 1)
			if err != nil {
				return err
			}
			_, err = transport.indexer.DeleteInternalMailbox(command)
			return err
		},
		confirm: func(string) error {
			return confirmRGB11DiscardGone(client, key)
		},
		cleanup: func() error { cleaned = true; return nil },
		mark:    func() error { marked = true; return nil },
	}
	if err := applyRGB11Discard(approved, ops); err != nil {
		t.Fatal(err)
	}
	if !cleaned || !marked {
		t.Fatalf("cleaned=%v marked=%v", cleaned, marked)
	}
	state, err := transport.indexer.GetKeyState(key)
	if err != nil || state.Status != dkvsindexer.KeyStateNeverSeen {
		t.Fatalf("remote record survived: state=%+v err=%v", state, err)
	}
}

func TestRGB11MaintenanceMutationsRequireRecoveryBaseline(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	operations := []struct {
		name string
		call func(*Manager) error
	}{
		{name: "direct-discard", call: func(manager *Manager) error {
			return manager.ApplyRGB11Discard(RGB11DiscardPlan{Target: rgb11DiscardOnlyTarget})
		}},
		{name: "stale-owner", call: func(manager *Manager) error {
			return manager.ApplyRGB11StaleOwner(RGB11StaleOwnerPlan{Target: rgb11StaleLockOnlyTarget})
		}},
		{name: "stale-lock", call: func(manager *Manager) error {
			return manager.ApplyRGB11StaleLock(RGB11StaleLockPlan{Target: rgb11StaleLockOnlyTarget})
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name+"/unconfigured", func(t *testing.T) {
			manager := configuredRGB11ManagedOperationTestManager(t)
			manager.mutex.Lock()
			manager.accountProfile.RecoveryConfigured = false
			if err := manager.saveAccountManagementProfileLocked(); err != nil {
				manager.mutex.Unlock()
				t.Fatal(err)
			}
			manager.mutex.Unlock()
			remote, ok := manager.http.(*rgb11MemoryDKVSHTTP)
			if !ok {
				t.Fatal("managed RGB11 fixture does not use the in-memory account transport")
			}
			beforeRemote := snapshotRGB11MemoryRecords(remote)
			beforeLocal := snapshotRGB11MaintenanceState(t, manager)
			if err := operation.call(manager); !errors.Is(err, ErrDKVSPathNotSynced) {
				t.Fatalf("unconfigured maintenance operation error=%v, want %v", err, ErrDKVSPathNotSynced)
			}
			if after := snapshotRGB11MemoryRecords(remote); !reflect.DeepEqual(beforeRemote, after) {
				t.Fatal("unconfigured maintenance operation changed remote account/mailbox records")
			}
			assertRGB11MaintenanceStateUnchanged(t, beforeLocal, snapshotRGB11MaintenanceState(t, manager))
		})
		t.Run(operation.name+"/rejected-baseline-cas", func(t *testing.T) {
			manager := configuredRGB11ManagedOperationTestManager(t)
			remote, fault := installRGB11DirtyCASRejector(t, manager)
			beforeRemote := snapshotRGB11MemoryRecords(remote)
			beforeLocal := snapshotRGB11MaintenanceState(t, manager)
			if err := operation.call(manager); err == nil {
				t.Fatal("maintenance mutation crossed a rejected account baseline CAS")
			}
			fault.rejectMu.Lock()
			rejected := fault.rejected
			fault.rejectMu.Unlock()
			if rejected == 0 {
				t.Fatal("maintenance mutation did not reach the authoritative account CAS gate")
			}
			if after := snapshotRGB11MemoryRecords(remote); !reflect.DeepEqual(beforeRemote, after) {
				t.Fatal("rejected maintenance operation deleted or changed a remote record")
			}
			assertRGB11MaintenanceStateUnchanged(t, beforeLocal, snapshotRGB11MaintenanceState(t, manager))
		})
	}
}

type rgb11MaintenanceStateSnapshot struct {
	locks                map[string]*LockedUtxo
	transfers            []*rgb11wallet.TransferState
	proofs               []*rgb11wallet.AllocationProof
	outputs              []*indexer.TxOutput
	receives             []*rgb11wallet.ReceiveReservation
	ownerPending         *rgb11wallet.PendingTransfer
	ownerPendingPresent  bool
	staleLock            *LockedUtxo
	staleLockPresent     bool
	processedMetadata    []byte
	processedMetadataSet bool
}

func snapshotRGB11MaintenanceState(t *testing.T, manager *Manager) rgb11MaintenanceStateSnapshot {
	t.Helper()
	store := manager.rgbManager.projectionStore
	snapshot := rgb11MaintenanceStateSnapshot{locks: manager.utxoLockerL1.GetLockedUtxoList()}
	var err error
	if snapshot.transfers, err = store.ListTransfers(); err != nil {
		t.Fatal(err)
	}
	if snapshot.proofs, err = store.ListProofs(); err != nil {
		t.Fatal(err)
	}
	if snapshot.outputs, err = store.ListOutputs(); err != nil {
		t.Fatal(err)
	}
	if snapshot.receives, err = store.ListReceiveReservations(); err != nil {
		t.Fatal(err)
	}
	if snapshot.ownerPending, err = store.LoadPendingTransfer(rgb11StaleLockOnlyTarget.TransferID); err == nil {
		snapshot.ownerPendingPresent = true
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatal(err)
	}
	if snapshot.staleLock, err = loadRGB11RawLock(manager.db, rgb11StaleLockOnlyTarget.OutPoint); err == nil {
		snapshot.staleLockPresent = true
	} else if !errors.Is(err, errRGB11LockGone) {
		t.Fatal(err)
	}
	if snapshot.processedMetadata, err = store.LoadLocalMetadata(
		rgb11AddressProcessedMetadata("consignment", rgb11DiscardOnlyTarget.ApplicationID)); err == nil {
		snapshot.processedMetadataSet = true
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatal(err)
	}
	return snapshot
}

func assertRGB11MaintenanceStateUnchanged(t *testing.T,
	before, after rgb11MaintenanceStateSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected maintenance operation changed local RGB ownership, reservations, locks, or processed metadata")
	}
}
