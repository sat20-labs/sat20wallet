package wallet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// rgb11SnapshotConsumerInput is a wallet-side index of a typed core report;
// it is not parsed from RGB wire fields in this package.
type rgb11SnapshotConsumerInput struct {
	ConsignmentHash     string
	ContractID          string
	WitnessTxID         string
	InputOperationID    string
	AssignmentType      uint32
	AssignmentIndex     uint32
	ConsumerOperationID string
	WitnessState        uint8
}

// rgb11SnapshotConsignmentConsumers asks core to validate each complete
// historical graph against fresh Bitcoin evidence. Invalid/unresolved objects
// contribute no consumer edges; if an outpoint is positively reported spent
// by one of them, the caller will fail closed because no validated edge binds
// that spender to the exact input Opout.
func rgb11SnapshotConsignmentConsumers(ctx context.Context, validator *rgb11wallet.NativeConsensusValidator,
	evidence rgb11wallet.BitcoinEvidenceProvider, objects map[string][]byte) (
	[]rgb11SnapshotConsumerInput, map[string]*rgb11wallet.HistoricalValidation, error) {
	if validator == nil || evidence == nil {
		return nil, nil, rgb11wallet.ErrConsensusValidatorUnavailable
	}
	consumers := make([]rgb11SnapshotConsumerInput, 0)
	validated := make(map[string]*rgb11wallet.HistoricalValidation)
	for objectHash, raw := range objects {
		digest := sha256.Sum256(raw)
		if objectHash != hex.EncodeToString(digest[:]) {
			return nil, nil, rgb11wallet.ErrInvalidProof
		}
		report, err := validator.ValidateHistoricalConsignmentReport(ctx, raw, evidence)
		if err != nil {
			continue
		}
		validated[objectHash] = report
		for _, edge := range report.Consumers {
			consumers = append(consumers, rgb11SnapshotConsumerInput{
				ConsignmentHash: edge.ConsignmentHash, ContractID: edge.ContractID,
				WitnessTxID: edge.WitnessTxID, InputOperationID: edge.InputOperationID,
				AssignmentType: edge.AssignmentType, AssignmentIndex: edge.AssignmentIndex,
				ConsumerOperationID: edge.ConsumerOperationID, WitnessState: edge.WitnessState,
			})
		}
	}
	sort.Slice(consumers, func(i, j int) bool {
		left, right := consumers[i], consumers[j]
		if left.ContractID != right.ContractID {
			return left.ContractID < right.ContractID
		}
		if left.InputOperationID != right.InputOperationID {
			return left.InputOperationID < right.InputOperationID
		}
		if left.AssignmentType != right.AssignmentType {
			return left.AssignmentType < right.AssignmentType
		}
		if left.AssignmentIndex != right.AssignmentIndex {
			return left.AssignmentIndex < right.AssignmentIndex
		}
		if left.ConsumerOperationID != right.ConsumerOperationID {
			return left.ConsumerOperationID < right.ConsumerOperationID
		}
		if left.WitnessTxID != right.WitnessTxID {
			return left.WitnessTxID < right.WitnessTxID
		}
		return left.ConsignmentHash < right.ConsignmentHash
	})
	return consumers, validated, nil
}
