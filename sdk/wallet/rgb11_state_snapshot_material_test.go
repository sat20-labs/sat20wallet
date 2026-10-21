package wallet

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/rgb11/seals"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11SnapshotRetainsPrivatePendingSigningMaterials(t *testing.T) {
	tx := wire.NewMsgTx(wire.TxVersion)
	var previous chainhash.Hash
	previous[0] = 0x71
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: previous, Index: 2}, nil, nil))
	tx.TxIn[0].Witness = wire.TxWitness{[]byte{0x30, 0x01}}
	tx.AddTxOut(wire.NewTxOut(1200, []byte{0x51}))
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	base := rgb11wallet.PendingTransfer{
		State: rgb11wallet.TransferState{
			TransferID: "snapshot-signing", Direction: "send", Status: "prepared",
			BatchTransferIDs: []string{},
			WitnessTxID:      tx.TxHash().String(), InputOutPoints: []string{tx.TxIn[0].PreviousOutPoint.String()},
			OutputOutPoints: []string{tx.TxHash().String() + ":0"},
		},
		SignedTx: append([]byte(nil), raw.Bytes()...), SignedPSBT: []byte{0x01, 0x02},
		ChangeSeals: []seals.GraphBlindSeal{},
	}

	records, err := rgb11wallet.UpsertProjectionSnapshotPending(nil, &base)
	if err != nil {
		t.Fatalf("add prepared pending material: %v", err)
	}
	assertSnapshotPendingSigningMaterial(t, records, base)

	settled := base
	settled.State.Status = "settled"
	records, err = rgb11wallet.UpsertProjectionSnapshotPending(records, &settled)
	if err != nil {
		t.Fatalf("advance pending transfer with retained signing material: %v", err)
	}
	assertSnapshotPendingSigningMaterial(t, records, settled)

	for name, candidate := range map[string]rgb11wallet.PendingTransfer{
		"prepared psbt removal": func() rgb11wallet.PendingTransfer {
			value := base
			value.SignedPSBT = nil
			return value
		}(),
		"settled psbt removal": func() rgb11wallet.PendingTransfer {
			value := settled
			value.SignedPSBT = nil
			return value
		}(),
		"changed transaction bytes": func() rgb11wallet.PendingTransfer {
			value := settled
			changed := tx.Copy()
			changed.TxIn[0].Witness = wire.TxWitness{[]byte{0x30, 0x02}}
			if changed.TxHash().String() != tx.TxHash().String() {
				t.Fatal("witness-only mutation unexpectedly changed the txid")
			}
			value.SignedTx = serializeRGB11SnapshotTestTx(t, changed)
			return value
		}(),
		"changed transaction id": func() rgb11wallet.PendingTransfer {
			value := settled
			value.State.WitnessTxID = "different-txid"
			return value
		}(),
		"missing signed transaction": func() rgb11wallet.PendingTransfer {
			value := settled
			value.SignedTx = nil
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := rgb11wallet.UpsertProjectionSnapshotPending(records, &candidate); err == nil {
				t.Fatal("candidate changed or removed private signing material")
			}
		})
	}

	for _, signed := range []bool{false, true} {
		channel := base
		channel.State.TransferID = "channel-signing"
		channel.ChannelSend = &rgb11wallet.ChannelSendData{
			ChannelID: "channel-1", WitnessScript: []byte{0x51}, PeerPubKey: []byte{0x02},
			MoreData: []byte{0x03}, Signed: signed,
		}
		channelRecords, err := rgb11wallet.UpsertProjectionSnapshotPending(nil, &channel)
		if err != nil {
			t.Fatalf("add channel pending signed=%v: %v", signed, err)
		}
		advanced := channel
		advanced.State.Status = "settled"
		channelRecords, err = rgb11wallet.UpsertProjectionSnapshotPending(channelRecords, &advanced)
		if err != nil {
			t.Fatalf("advance channel pending signed=%v: %v", signed, err)
		}
		assertSnapshotPendingSigningMaterial(t, channelRecords, advanced)

		advanced.SignedPSBT = nil
		if _, err := rgb11wallet.UpsertProjectionSnapshotPending(channelRecords, &advanced); err == nil {
			t.Fatalf("signed=%v channel candidate removed its PSBT", signed)
		}
	}
}

func TestRGB11ReceiveSnapshotBindsSealByValueAfterRoundTrip(t *testing.T) {
	txid := [32]byte{0x51, 0x52, 0x53}
	seal, err := seals.NewGraphBlindSeal(txid[:], 7, 11)
	if err != nil {
		t.Fatal(err)
	}
	request := &corewallet.ReceiveRequest{
		Version: corewallet.ReceiveVersion, Mode: corewallet.ReceiveBlind,
		RequestID: "receive-roundtrip", RecipientID: "recipient",
		Seal: seal, Invoice: "invoice", CreatedAt: 1, Expiry: 99,
		Status: corewallet.ReceivePrepared,
	}
	encoded, err := corewallet.EncodeReceiveRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := corewallet.DecodeReceiveRequest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Seal.TxID == request.Seal.TxID || decoded.Seal.Vout != request.Seal.Vout ||
		decoded.Seal.Blinding != request.Seal.Blinding || *decoded.Seal.TxID != *request.Seal.TxID {
		t.Fatalf("roundtrip fixture did not create equal values with distinct pointer identity: old=%+v new=%+v",
			request.Seal, decoded.Seal)
	}
	records := []rgb11wallet.SnapshotRecord{{Key: "wallet/receive/" + request.RequestID, Value: encoded}}
	if _, err := rgb11wallet.UpsertEngineSnapshotReceive(records, decoded); err != nil {
		t.Fatalf("same blinded seal value was rejected after strict roundtrip: %v", err)
	}

	for name, change := range map[string]func(*corewallet.ReceiveRequest){
		"txid": func(candidate *corewallet.ReceiveRequest) {
			changed := *candidate.Seal.TxID
			changed[0]++
			candidate.Seal.TxID = &changed
		},
		"vout":     func(candidate *corewallet.ReceiveRequest) { candidate.Seal.Vout++ },
		"blinding": func(candidate *corewallet.ReceiveRequest) { candidate.Seal.Blinding++ },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := *decoded
			candidate.Seal = seals.GraphBlindSeal{TxID: decoded.Seal.TxID, Vout: decoded.Seal.Vout, Blinding: decoded.Seal.Blinding}
			change(&candidate)
			if _, err := rgb11wallet.UpsertEngineSnapshotReceive(records, &candidate); err == nil {
				t.Fatal("changed immutable seal identity was accepted")
			}
		})
	}
}

func assertSnapshotPendingSigningMaterial(t *testing.T, records []rgb11wallet.SnapshotRecord,
	want rgb11wallet.PendingTransfer) {
	t.Helper()
	pendings, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(records)
	if err != nil || len(pendings) != 1 {
		t.Fatalf("decode pending candidate: count=%d err=%v", len(pendings), err)
	}
	got := pendings[0]
	if !bytes.Equal(got.SignedTx, want.SignedTx) || !bytes.Equal(got.SignedPSBT, want.SignedPSBT) ||
		got.State.WitnessTxID != want.State.WitnessTxID {
		t.Fatal("projection snapshot changed private SignedTx, PSBT, or witness txid")
	}
}

func serializeRGB11SnapshotTestTx(t *testing.T, tx *wire.MsgTx) []byte {
	t.Helper()
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}
