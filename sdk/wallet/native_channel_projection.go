package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/seals"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
)

// Import exactly the authenticated current public-pool source. This projection
// is owned by the captured account, but ordinary wallet selection excludes its
// P2WSH script; only an authorized channel-send context may consume it.
func (p *rgb11Manager) importObservedNativePoolTransfer(ctx context.Context, source *NativeChannelSource) (*RGB11ImportResult, error) {
	return retryNativeProjection(ctx, func() (*RGB11ImportResult, error) {
		return p.importObservedNativePoolTransferAttempt(ctx, source)
	})
}

func (p *rgb11Manager) importObservedNativePoolTransferAttempt(ctx context.Context, source *NativeChannelSource) (*RGB11ImportResult, error) {
	if p == nil || p.projectionStore == nil || p.evidence == nil || p.wallet == nil || source == nil || source.Output == nil {
		return nil, ErrRGB11Inconsistent
	}
	material, err := nativeproof.Decode(source.PublicProof)
	if err != nil {
		return nil, err
	}
	// Projection selects only this owner's output. Preserve all already public
	// sibling disclosures in the standard RGB object so a later transfer can
	// independently reconstruct the full mapped-input / returned / burned flow.
	// The receipt below is freshly core-validated against the resulting hash;
	// the immutable source's PublicProof is never rewritten.
	material.Consignment, err = rgb11wallet.PublicChannelConsignment(material.Consignment, material.Reveals)
	if err != nil {
		return nil, err
	}
	point, err := wire.NewOutPointFromString(source.Output.OutPointStr)
	if err != nil {
		return nil, err
	}
	rawTx, err := p.evidence.GetRawTx(point.Hash.String())
	if err != nil {
		return nil, err
	}
	tx := new(wire.MsgTx)
	reader := bytes.NewReader(rawTx)
	if tx.Deserialize(reader) != nil || reader.Len() != 0 || tx.TxHash() != point.Hash {
		return nil, ErrRGB11Inconsistent
	}
	var reveals []seals.GraphBlindSeal
	for _, raw := range material.Reveals {
		seal, err := seals.DecodeGraphBlindSeal(raw)
		if err != nil {
			return nil, err
		}
		reveals = append(reveals, seal)
	}
	validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(reveals...)
	history, err := validator.ValidateHistoricalConsignmentReport(ctx, material.Consignment, p.evidence)
	if err != nil || history == nil || history.MaterialReceipt == nil {
		return nil, fmt.Errorf("validate native settlement history: %w", errRGB11ProjectionEvidence(err))
	}
	container, err := coreconsignment.Decode(material.Consignment)
	if err != nil {
		return nil, err
	}
	walletScript := source.Output.OutValue.PkScript
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	current, _, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		return nil, err
	}
	receipts, err := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(current.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	previousProofs, err := rgb11wallet.ProofsFromProjectionSnapshot(current.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	previousOutputs, err := rgb11wallet.OutputsFromProjectionSnapshot(current.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	managed, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(current.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	managedChange := make(map[string]bool)
	for _, pending := range managed {
		if pending.State.Direction != "send" || pending.ChannelSend == nil || pending.State.WitnessTxID != tx.TxID() ||
			pending.State.Asset.Name != source.Output.Assets[0].Name {
			continue
		}
		for _, point := range rgb11PendingChangeOutpoints(pending) {
			managedChange[point] = true
		}
	}
	byPoint := make(map[string]*rgb11wallet.AllocationProof, len(previousProofs))
	for _, proof := range previousProofs {
		if proof.AssetName == source.Output.Assets[0].Name {
			if byPoint[proof.OutPoint] != nil {
				return nil, rgb11wallet.ErrInvalidProof
			}
			byPoint[proof.OutPoint] = proof
		}
	}
	existing := receipts[history.MaterialReceipt.ConsignmentHash]
	if existing != nil && !sameRGB11ValidationReceiptIdentity(existing, history.MaterialReceipt) {
		return nil, rgb11wallet.ErrValidationReceipt
	}
	type ownedAllocation struct {
		allocation rgb11wallet.ValidatedAllocation
		utxo       *rgb11wallet.BitcoinUTXO
		binding    *rgb11wallet.CarrierBinding
	}
	var owned []ownedAllocation
	var needed []rgb11wallet.ValidatedAllocation
	seen := make(map[string]bool)
	for _, item := range history.Allocations {
		allocation := item.Allocation
		point, err := wire.NewOutPointFromString(allocation.OutPoint)
		if err != nil || point.Hash.String() != tx.TxID() {
			continue
		}
		if uint64(point.Index) >= uint64(len(tx.TxOut)) {
			return nil, rgb11wallet.ErrValidationReceipt
		}
		if allocation.OutPoint != source.Output.OutPointStr {
			continue
		}
		if !bytes.Equal(tx.TxOut[point.Index].PkScript, walletScript) || allocation.AssetName != source.Output.Assets[0].Name || allocation.Amount.Cmp(&source.Output.Assets[0].Amount) != 0 {
			return nil, fmt.Errorf("native pool allocation differs from authenticated full source")
		}
		if seen[allocation.OutPoint] {
			return nil, fmt.Errorf("multiple RGB11 allocations share a wallet projection carrier")
		}
		seen[allocation.OutPoint] = true
		spend, err := p.evidence.GetOutspend(allocation.OutPoint)
		if err != nil || spend == nil {
			return nil, fmt.Errorf("native settlement outspend unavailable: %w", errRGB11ProjectionEvidence(err))
		}
		if spend.Spent {
			if !rgb11KnownObservedChannelSpend(p.evidence, allocation.OutPoint, spend.SpendingTx) {
				return nil, fmt.Errorf("native settlement has no exact observed spender")
			}
			continue
		}
		if spend.SpendingTx != "" {
			return nil, fmt.Errorf("contradictory native settlement outspend")
		}
		utxo, err := p.evidence.GetUTXO(allocation.OutPoint)
		if err != nil || utxo == nil || utxo.Value != source.Output.Value() || !bytes.Equal(utxo.PkScript, walletScript) {
			return nil, fmt.Errorf("native settlement current carrier changed")
		}
		binding, err := p.rgb11CarrierBinding(allocation, utxo)
		if err != nil {
			return nil, err
		}
		// The caller has already authenticated this exact pool and owner.
		owned = append(owned, ownedAllocation{allocation, utxo, binding})
		needed = append(needed, allocation)
	}
	if len(owned) != 1 {
		return nil, fmt.Errorf("native pool source requires one complete validated allocation")
	}
	projectionReceipt, err := rgb11wallet.SelectHistoricalValidationReceipt(history, existing, needed)
	if err != nil {
		return nil, err
	}
	projectionRecords := current.ProjectionRecords
	if existing != nil {
		oldHash, err := existing.Hash()
		if err != nil {
			return nil, err
		}
		newHash, err := projectionReceipt.Hash()
		if err != nil {
			return nil, err
		}
		if oldHash != newHash {
			projectionRecords, err = rgb11wallet.RebindValidatedConsignmentReceiptSnapshot(projectionRecords, existing, projectionReceipt)
			if err != nil {
				return nil, err
			}
		}
	}
	receiptHash, err := projectionReceipt.Hash()
	if err != nil {
		return nil, err
	}
	var replacements []rgb11wallet.ProjectionReplacement
	for _, item := range owned {
		allocation := item.allocation
		if previous := byPoint[allocation.OutPoint]; previous != nil {
			// The initiating wallet may already have the exact managed-send
			// change projected from its original armored object. A canonical
			// public envelope can encode the same Opout with a different object
			// hash; do not rewrite that pending operation's immutable identity.
			retained := receipts[previous.ConsignmentHash]
			if retained == nil || retained.ContractID != history.MaterialReceipt.ContractID || retained.SchemaID != history.MaterialReceipt.SchemaID {
				return nil, rgb11wallet.ErrValidationReceipt
			}
			hash, err := retained.Hash()
			output := previousOutputs[allocation.OutPoint]
			if err != nil || hash != previous.ValidationHash || previous.WitnessTxID != tx.TxID() ||
				!sameRGB11CarrierBinding(previous.CarrierBinding, item.binding) || output == nil ||
				output.Value() != item.utxo.Value || !bytes.Equal(output.OutValue.PkScript, item.utxo.PkScript) ||
				!rgb11ProofMatchesHistoricalReceipt(previous, retained, output, history) {
				return nil, fmt.Errorf("existing native pool projection differs from independently validated history")
			}
			continue
		}
		if managedChange[allocation.OutPoint] {
			// Canonical confirmation can arrive before the originating managed
			// send has projected its local change. Let that journal install the
			// original object/receipt identity first; a public encoding must not
			// win the race and make its pending recovery material inconsistent.
			return nil, fmt.Errorf("%w: native pool change awaits its managed transfer projection", ErrNativeContractRetry)
		}
		commitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, allocation.SealDisclosure)
		proof := &rgb11wallet.AllocationProof{OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
			OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType, AssignmentIndex: allocation.AssignmentIndex,
			StateClass: allocation.StateClass, StateData: append([]byte(nil), allocation.StateData...),
			SealCommitment: hex.EncodeToString(commitment[:]), SealDisclosure: append([]byte(nil), allocation.SealDisclosure...),
			ConsignmentHash: projectionReceipt.ConsignmentHash, ValidationHash: receiptHash,
			WitnessTxID: tx.TxID(), Status: "valid", CarrierBinding: item.binding, Confirmations: item.utxo.Confirmations}
		output := indexer.NewTxOutput(item.utxo.Value)
		output.OutPointStr, output.OutValue.PkScript = allocation.OutPoint, append([]byte(nil), item.utxo.PkScript...)
		replacements = append(replacements, rgb11wallet.ProjectionReplacement{Output: output,
			Asset: &indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}, Proof: proof})
	}
	projectionRecords, err = rgb11wallet.AddValidatedConsignmentProjectionSnapshot(projectionRecords, material.Consignment, projectionReceipt, replacements)
	if err != nil {
		return nil, err
	}
	contractFile, _, err := rgb11ContractFromValidatedHistory(material.Consignment, projectionReceipt.ContractID)
	if err != nil {
		return nil, err
	}
	projectionRecords, err = rgb11wallet.AddContractObjectSnapshotReference(projectionRecords, projectionReceipt.ContractID, contractFile)
	if err != nil {
		return nil, err
	}
	// Retain exact spent Opouts for managed-send recovery. Removing a change
	// proof before its original journal is retired can make a later refresh
	// try to recreate that now-spent change. The ordinary resolver revalidates
	// the observed consumer and keeps these records non-spendable.
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(projectionRecords)
	if err != nil {
		return nil, err
	}
	spent := make(map[string]bool)
	for _, input := range tx.TxIn {
		spent[input.PreviousOutPoint.String()] = true
	}
	var updates []rgb11wallet.SnapshotProofUpdate
	for _, proof := range proofs {
		if !spent[proof.OutPoint] {
			continue
		}
		outspend, err := p.evidence.GetOutspend(proof.OutPoint)
		if err != nil || outspend == nil || !outspend.Spent || !rgb11KnownObservedChannelSpend(p.evidence, proof.OutPoint, outspend.SpendingTx) {
			return nil, fmt.Errorf("native consumed source is not independently observed")
		}
		updates = append(updates, rgb11wallet.SnapshotProofUpdate{OutPoint: proof.OutPoint, AssetName: proof.AssetName,
			Status: "spending", Confirmations: proof.Confirmations})
	}
	if len(updates) != 0 {
		projectionRecords, err = rgb11wallet.UpdateProjectionSnapshotProofs(projectionRecords, updates)
		if err != nil {
			return nil, err
		}
	}
	candidate := *current
	candidate.ProjectionRecords = projectionRecords
	changed := !sameNativeProjectionRecords(current.ProjectionRecords, projectionRecords)
	if owner := p.accountManagementOwner(); changed && owner != nil {
		scopeID, err := p.rgb11AccountManagedScopeID()
		if err != nil {
			return nil, err
		}
		if _, err := owner.preflightRGB11TerminalRecovery(scopeID, &candidate); err != nil {
			return nil, err
		}
	}
	if changed {
		if _, err := p.applyRGB11CandidateSnapshot(ctx, current, &candidate); err != nil {
			return nil, nativeProjectionPublicationError(err)
		}
		if err := p.autoBackupRGB11AfterMutation(); err != nil {
			return nil, err
		}
	}
	info, err := rgb11TickerInfoFromValidatedContract(container, projectionReceipt)
	if err != nil {
		return nil, err
	}
	if err := p.RegisterRGB11TickerInfo(info); err != nil {
		return nil, err
	}
	return &RGB11ImportResult{ContractID: container.ContractID, SchemaID: container.SchemaID, AssetName: source.Output.Assets[0].Name, Receipt: projectionReceipt, Projected: len(replacements)}, nil
}

func sameNativeProjectionRecords(left, right []rgb11wallet.SnapshotRecord) bool {
	if len(left) != len(right) {
		return false
	}
	previous := make(map[string][]byte, len(left))
	for _, record := range left {
		if _, duplicate := previous[record.Key]; duplicate {
			return false
		}
		previous[record.Key] = record.Value
	}
	for _, record := range right {
		value, exists := previous[record.Key]
		if !exists || !bytes.Equal(value, record.Value) {
			return false
		}
		delete(previous, record.Key)
	}
	return len(previous) == 0
}
