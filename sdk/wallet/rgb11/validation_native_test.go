package rgb11wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
)

type nativeVectorEvidence struct {
	raw         []byte
	witnessTxID string
}

func (e nativeVectorEvidence) GetUTXO(outpoint string) (*BitcoinUTXO, error) {
	return &BitcoinUTXO{OutPoint: outpoint, Confirmations: 0}, nil
}

func (e nativeVectorEvidence) GetRawTx(txid string) ([]byte, error) {
	return append([]byte(nil), e.raw...), nil
}

func (e nativeVectorEvidence) GetTxStatus(txid string) (*BitcoinTxStatus, error) {
	return &BitcoinTxStatus{TxID: txid, InMempool: true}, nil
}

func (e nativeVectorEvidence) GetOutspend(outpoint string) (*BitcoinOutspend, error) {
	if strings.HasPrefix(outpoint, e.witnessTxID+":") {
		return &BitcoinOutspend{}, nil
	}
	return &BitcoinOutspend{Spent: true, SpendingTx: e.witnessTxID}, nil
}

func (e nativeVectorEvidence) GetTip() (*BitcoinTip, error)     { return &BitcoinTip{}, nil }
func (e nativeVectorEvidence) Broadcast([]byte) (string, error) { return e.witnessTxID, nil }

func TestNativeValidatorProducesReceiptFromOfficialTransfer(t *testing.T) {
	armored, err := os.ReadFile("../../../../rgb11/testvectors/rc11/nia-transfer.rgba")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString("0200000001c568200c10c4ca3c351108bffc3d1e4238f94d94c06d28b6cd91a1b15b5d29140100000000fdffffff020000000000000000226a208bef6db012dbd42088e5af8ac1df536ff8de140e82fe34a0bbb3e13b912b55b322020000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	evidence := nativeVectorEvidence{raw: raw, witnessTxID: tx.TxHash().String()}
	receipt, err := ValidateWith(context.Background(), NewNativeConsensusValidator(), armored, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.EngineBuildID != NativeEngineBuildID || receipt.ContractID != "rgb:k0vsa6zj-CLYfnru-63unuJv-qZ2IVJ5-zlENzlF-MkiJNuw" || len(receipt.Allocations) != 1 {
		t.Fatalf("unexpected native receipt: %+v", receipt)
	}
	allocation := receipt.Allocations[0]
	if allocation.AssetName.Ticker != "k0vsa6zj-CLYfnru-63unuJv-qZ2IVJ5-zlENzlF-MkiJNuw" ||
		allocation.Amount.Precision != 8 || allocation.Amount.Value.String() != "100000" || allocation.Amount.String() != "0.001" ||
		allocation.OperationID != "1e986e8714b4d3be6835797190a218be42831629d516cc26ebcf329e25716ad1" {
		t.Fatalf("unexpected allocation: %+v", allocation)
	}
	container, err := coreconsignment.Decode(armored)
	if err != nil {
		t.Fatal(err)
	}
	binaryReceipt, err := ValidateWith(context.Background(), NewNativeConsensusValidator(), container.Armor.Data, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if binaryReceipt.ContractID != receipt.ContractID || binaryReceipt.StateHash != receipt.StateHash {
		t.Fatalf("strict-binary receipt mismatch: %+v", binaryReceipt)
	}
}
