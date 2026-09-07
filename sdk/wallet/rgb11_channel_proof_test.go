package wallet

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestRGB11ChannelProofRebuildsValidatedAmounts(t *testing.T) {
	sender, recipient, imported, _, rpc := newRGB11GenericSendFixture(t)
	balance, err := sender.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil {
		t.Fatal(err)
	}
	prepared, _, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), RGB11AddressSendRequest{
		ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName,
		AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1,
	}, dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	proof := &wwire.RGB11SigningProof{Consignment: pending.LocalConsignment}
	for _, seal := range pending.ChangeSeals {
		raw, err := seal.StrictBytes()
		if err != nil {
			t.Fatal(err)
		}
		proof.ChangeSeals = append(proof.ChangeSeals, raw)
	}
	tx := wire.NewMsgTx(2)
	if err := tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	inputs, outputs, err := sender.RebuildRGB11TxOutput(tx, proof)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != len(tx.TxIn) || len(outputs) != len(tx.TxOut) {
		t.Fatal("lost transaction inputs or outputs")
	}
	amount := outputs[1].GetAsset(&imported.AssetName)
	if amount == nil || amount.Value.Uint64() != 20000 {
		t.Fatalf("recipient RGB amount=%v", amount)
	}
	total := balance.Clone()
	for _, output := range outputs {
		if amount := output.GetAsset(&imported.AssetName); amount != nil {
			total = total.Sub(amount)
		}
	}
	if total.Sign() != 0 {
		t.Fatalf("RGB change was lost: %s", total)
	}
	changed := tx.Copy()
	changed.TxOut[1].Value++
	if _, _, err := sender.RebuildRGB11TxOutput(changed, proof); err == nil {
		t.Fatal("proof authorized another transaction")
	}
	if _, _, err := sender.RebuildRGB11TxOutput(tx, nil); err == nil {
		t.Fatal("missing proof was accepted")
	}
	point := tx.TxIn[0].PreviousOutPoint.String()
	rpc.outputs[point].Assets = indexer.TxAssets{{Name: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: "f", Ticker: "other"}, Amount: *indexer.NewDefaultDecimal(7)}}
	if _, _, err := sender.RebuildRGB11TxOutput(tx, proof); !errors.Is(err, ErrRGB11AssetPreservation) {
		t.Fatalf("peer accepted a carrier containing another asset: %v", err)
	}
	rpc.outputs[point].Assets = nil
	allocations, err := rgb11IssueAllocations([]string{point}, []uint64{7})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := coreissuance.Issue(coreissuance.Spec{
		Kind: schemas.NIA, Network: rgb11IssuanceNetwork(&chaincfg.TestNet4Params),
		Ticker: "SHARED", Name: "Other RGB on signing input", Allocations: allocations,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := sender.ImportRGB11Contract(context.Background(), []byte(issued.Armor))
	if err != nil || other.Projected != 1 {
		t.Fatalf("import shared carrier: %+v %v", other, err)
	}
	if _, _, err := sender.RebuildRGB11TxOutput(tx, proof); !errors.Is(err, ErrRGB11AssetPreservation) {
		t.Fatalf("peer accepted a proof omitting another RGB contract: %v", err)
	}
}
