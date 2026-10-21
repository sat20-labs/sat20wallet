package rgb11wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
)

type nativeVectorEvidence struct {
	raw         []byte
	witnessTxID string
}

type nativePreparedEvidence struct {
	nativeVectorEvidence
}

type nativeGenesisEvidence struct{}

func (nativeGenesisEvidence) GetUTXO(outpoint string) (*BitcoinUTXO, error) {
	return &BitcoinUTXO{OutPoint: outpoint, Confirmations: 6}, nil
}
func (nativeGenesisEvidence) GetRawTx(string) ([]byte, error)              { return nil, nil }
func (nativeGenesisEvidence) GetTxStatus(string) (*BitcoinTxStatus, error) { return nil, nil }
func (nativeGenesisEvidence) GetOutspend(string) (*BitcoinOutspend, error) {
	return &BitcoinOutspend{}, nil
}
func (nativeGenesisEvidence) GetTip() (*BitcoinTip, error)     { return &BitcoinTip{}, nil }
func (nativeGenesisEvidence) Broadcast([]byte) (string, error) { return "", nil }

func (e nativePreparedEvidence) GetRawTx(string) ([]byte, error) {
	return nil, errors.New("transaction is not broadcast")
}

func (e nativePreparedEvidence) GetTxStatus(string) (*BitcoinTxStatus, error) {
	return nil, errors.New("transaction is not broadcast")
}

func (e nativePreparedEvidence) GetOutspend(string) (*BitcoinOutspend, error) {
	return &BitcoinOutspend{}, nil
}

func (e nativePreparedEvidence) GetUTXO(outpoint string) (*BitcoinUTXO, error) {
	return &BitcoinUTXO{OutPoint: outpoint, Confirmations: 6}, nil
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
	return &BitcoinOutspend{Spent: true}, nil
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
	container, err := coreconsignment.Decode(armored)
	if err != nil {
		t.Fatal(err)
	}
	schemaValue, _ := container.Value.Field("schema")
	typeSystem, _ := container.Value.Field("types")
	genesisValue, _ := container.Value.Field("genesis")
	_, err = schemas.ExtractGenesisAssetMetadata(schemaValue, typeSystem, genesisValue)
	if err != nil {
		t.Fatal(err)
	}
	expectedAssetName, err := NewContractAssetKey(receipt.ContractID, "f")
	if err != nil {
		t.Fatal(err)
	}
	if allocation.AssetName != expectedAssetName ||
		allocation.Amount.Precision != 8 || allocation.Amount.Value.String() != "100000" || allocation.Amount.String() != "0.001" ||
		allocation.OperationID != "1e986e8714b4d3be6835797190a218be42831629d516cc26ebcf329e25716ad1" {
		t.Fatalf("unexpected allocation: %+v", allocation)
	}
	binaryReceipt, err := ValidateWith(context.Background(), NewNativeConsensusValidator(), container.Armor.Data, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if binaryReceipt.ContractID != receipt.ContractID || binaryReceipt.StateHash != receipt.StateHash {
		t.Fatalf("strict-binary receipt mismatch: %+v", binaryReceipt)
	}
}

func TestNativeReceiptOrdersSameCarrierFullOpoutsDeterministically(t *testing.T) {
	txid := bytes.Repeat([]byte{0x55}, 32)
	assetSeal, err := seals.NewGraphBlindSeal(txid, 7, 11)
	if err != nil {
		t.Fatal(err)
	}
	inflationSeal, err := seals.NewGraphBlindSeal(txid, 7, 22)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := issuance.Issue(issuance.Spec{
		Kind: schemas.IFA, Network: issuance.BitcoinTestnet4,
		Ticker: "SIFA", Name: "SAT20 IFA", Precision: 2,
		Allocations:     []issuance.Allocation{{Seal: assetSeal, Amount: 100}},
		InflationRights: []issuance.Allocation{{Seal: inflationSeal, Amount: 900}},
		Timestamp:       1_700_001_000,
	})
	if err != nil {
		t.Fatal(err)
	}

	var wantStateHash [32]byte
	for attempt := 0; attempt < 64; attempt++ {
		receipt, err := ValidateWith(context.Background(), NewNativeConsensusValidator(),
			issued.Container.Armor.Data, nativeGenesisEvidence{})
		if err != nil {
			t.Fatal(err)
		}
		if len(receipt.Allocations) != 2 {
			t.Fatalf("IFA receipt allocations=%d, want asset and inflation-right states", len(receipt.Allocations))
		}
		first, second := receipt.Allocations[0], receipt.Allocations[1]
		if first.OutPoint != second.OutPoint || first.OperationID != second.OperationID ||
			first.AssignmentIndex != 0 || second.AssignmentIndex != 0 ||
			first.AssignmentType != 4000 || second.AssignmentType != 4010 {
			t.Fatalf("receipt did not sort the complete Opout tuple: %+v", receipt.Allocations)
		}
		if attempt == 0 {
			wantStateHash = receipt.StateHash
		} else if receipt.StateHash != wantStateHash {
			t.Fatalf("receipt state hash changed across identical core validation: got %x want %x", receipt.StateHash, wantStateHash)
		}
	}
	history, err := NewNativeConsensusValidator().ValidateHistoricalConsignmentReport(
		context.Background(), issued.Container.Armor.Data, nativeGenesisEvidence{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Allocations) != 2 ||
		history.Allocations[0].Allocation.AssignmentType != 4000 ||
		history.Allocations[1].Allocation.AssignmentType != 4010 {
		t.Fatalf("historical allocations are not deterministically ordered by full Opout: %+v", history.Allocations)
	}
}

func TestNativePreparedValidatorDoesNotProduceValidReceipt(t *testing.T) {
	armored, err := os.ReadFile("../../../../rgb11/testvectors/rc11/nia-transfer.rgba")
	if err != nil {
		t.Fatal(err)
	}
	evidence := nativePreparedEvidence{}
	if _, err := ValidateWith(
		context.Background(), NewNativeConsensusValidator(), armored, evidence,
	); !errors.Is(err, coreconsignment.ErrWitnessUnresolved) {
		t.Fatalf("normal validation accepted an unbroadcast witness: %v", err)
	}
	prepared, err := ValidatePreparedWith(
		context.Background(), NewNativeConsensusValidator(), armored, evidence,
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Receipt.Status != "prepared" || len(prepared.Outputs) != 1 ||
		len(prepared.WitnessTxIDs) != 1 {
		t.Fatalf("unexpected prepared result: %+v", prepared)
	}
}

func TestNativePreparedHistoryReportReturnsValidatedGraphWithoutOwnership(t *testing.T) {
	armored, err := os.ReadFile("../../../../rgb11/testvectors/rc11/nia-transfer.rgba")
	if err != nil {
		t.Fatal(err)
	}
	report, err := NewNativeConsensusValidator().ValidatePreparedHistoryConsignmentReport(
		context.Background(), armored, nativePreparedEvidence{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if report == nil || len(report.Allocations) != 1 || len(report.HistoricalAllocations) != 2 || len(report.Outputs) != 1 ||
		len(report.WitnessTxIDs) != 1 || len(report.Consumers) != 1 {
		t.Fatalf("prepared history report omitted its validated graph facts: %+v", report)
	}
	allocation := report.Allocations[0]
	preparedOutput := report.Outputs[allocation.OutPoint]
	if allocation.Amount.Sign() <= 0 || allocation.WitnessTxID != report.WitnessTxIDs[0] ||
		preparedOutput == nil || allocation.OutPoint != preparedOutput.OutPoint ||
		report.Consumers[0].WitnessTxID != allocation.WitnessTxID ||
		report.Consumers[0].WitnessState != uint8(coreconsignment.WitnessPrepared) {
		t.Fatalf("prepared history report has inconsistent graph output and consumer edge: %+v", report)
	}
	var preparedWitnessRetained bool
	for _, historical := range report.HistoricalAllocations {
		if historical.Allocation.OperationID == report.Consumers[0].ConsumerOperationID {
			preparedWitnessRetained = historical.WitnessState == uint8(coreconsignment.WitnessPrepared) &&
				historical.Allocation.WitnessTxID == report.WitnessTxIDs[0]
		}
	}
	if !preparedWitnessRetained {
		t.Fatalf("historical graph mapping lost WitnessPrepared evidence: %+v", report.HistoricalAllocations)
	}
}

func TestNativeHistoricalReportReturnsExactSelectedOpoutMaterial(t *testing.T) {
	armored, err := os.ReadFile("../../../../rgb11/testvectors/rc11/nia-transfer.rgba")
	if err != nil {
		t.Fatal(err)
	}
	rawTx, err := hex.DecodeString("0200000001c568200c10c4ca3c351108bffc3d1e4238f94d94c06d28b6cd91a1b15b5d29140100000000fdffffff020000000000000000226a208bef6db012dbd42088e5af8ac1df536ff8de140e82fe34a0bbb3e13b912b55b322020000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(rawTx)); err != nil {
		t.Fatal(err)
	}
	validator := NewNativeConsensusValidator()
	report, err := validator.ValidateHistoricalConsignmentReport(context.Background(), armored,
		nativeVectorEvidence{raw: rawTx, witnessTxID: tx.TxHash().String()})
	if err != nil {
		t.Fatal(err)
	}
	if report == nil || len(report.Allocations) != 2 || len(report.Consumers) != 1 {
		t.Fatalf("historical report omitted selected material or consumer: %+v", report)
	}
	consumer := report.Consumers[0]
	var input, terminal *HistoricalAllocation
	for index := range report.Allocations {
		allocation := &report.Allocations[index]
		if allocation.Allocation.OperationID == consumer.InputOperationID &&
			allocation.Allocation.AssignmentType == consumer.AssignmentType &&
			allocation.Allocation.AssignmentIndex == consumer.AssignmentIndex {
			input = allocation
		}
		if allocation.Allocation.OperationID == consumer.ConsumerOperationID {
			terminal = allocation
		}
	}
	if input == nil || terminal == nil || input.WitnessState != uint8(coreconsignment.WitnessUnknown) ||
		terminal.WitnessState != uint8(coreconsignment.WitnessTentative) || terminal.Allocation.WitnessTxID != tx.TxHash().String() {
		t.Fatalf("historical Opout/witness closure mismatch: input=%+v terminal=%+v report=%+v", input, terminal, report)
	}
	if terminal.Allocation.Amount.Value == nil || terminal.Allocation.Amount.Value.Sign() <= 0 ||
		len(terminal.Allocation.SealDisclosure) == 0 || terminal.Allocation.OutPoint == "" {
		t.Fatalf("historical terminal omitted exact allocation identity: %+v", terminal.Allocation)
	}
}
