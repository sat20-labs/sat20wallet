//go:build rgb11discard

package wallet

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	btcwire "github.com/btcsuite/btcd/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestRGB11DiscardPlan(t *testing.T) {
	sender, recipient, imported, _, _ := newRGB11GenericSendFixture(t)
	ctx := context.Background()
	request := RGB11AddressSendRequest{
		ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName,
		AmountRaw: "10000", FeeRate: 2, MinConfirmations: 1,
	}
	prepared, err := runRGB11ManagedOperation(sender, ctx, rgb11ManagedOperationNew,
		func(m *rgb11Manager) (*RGB11PreparedTransfer, error) {
			return m.prepareRGB11AddressBatch(ctx, []RGB11AddressSendRequest{request},
				dkvsindexer.RecordVerificationOptions{})
		})
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
	sender, recipient, imported, _, _ := newRGB11GenericSendFixture(t)
	ctx := context.Background()
	prepared, err := runRGB11ManagedOperation(sender, ctx, rgb11ManagedOperationNew,
		func(m *rgb11Manager) (*RGB11PreparedTransfer, error) {
			return m.prepareRGB11AddressBatch(ctx, []RGB11AddressSendRequest{{
				ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName,
				AmountRaw: "10000", FeeRate: 2, MinConfirmations: 1,
			}}, dkvsindexer.RecordVerificationOptions{})
		})
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
			tombstone, err := NewDKVSSignedTombstone(dkvsTestWalletFromPriv(t, recipient), key,
				dkvsindexer.RecordOptions{Seq: 2})
			if err != nil {
				return err
			}
			_, err = transport.indexer.DeleteInternalMailbox(tombstone)
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
