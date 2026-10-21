package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/invoicing"
	"github.com/sat20-labs/rgb11/seals"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func rgb11ReceiveRequestFromSnapshot(records []rgb11wallet.SnapshotRecord, requestID string) (*corewallet.ReceiveRequest, error) {
	key := "wallet/receive/" + requestID
	for _, record := range records {
		if record.Key != key {
			continue
		}
		request, err := corewallet.DecodeReceiveRequest(record.Value)
		if err != nil || request.RequestID != requestID {
			return nil, corewallet.ErrInvalidReceive
		}
		return request, nil
	}
	return nil, indexer.ErrKeyNotFound
}

func rgb11TransferFromSnapshot(records []rgb11wallet.SnapshotRecord,
	transferID string) (*rgb11wallet.TransferState, error) {
	transfers, err := rgb11wallet.TransfersFromProjectionSnapshot(records)
	if err != nil {
		return nil, err
	}
	for _, state := range transfers {
		if state.TransferID == transferID {
			return state, nil
		}
	}
	return nil, indexer.ErrKeyNotFound
}

// Each revealed recipient output in a validated witness batch uses the same
// local key as the sender/address receive path. Keep canonical consignment
// identity in receipts and bind the local key through the existing request.
func rgb11ReceiveLocalTransferID(request *corewallet.ReceiveRequest,
	receipt *rgb11wallet.ValidationReceipt, matched *rgb11ReceiveAllocationMatch, explicit string) (string, error) {
	canonical := receipt.TransferID
	if canonical == "" {
		canonical = receipt.ConsignmentHash
	}
	child, batch := "", false
	if request.Mode == corewallet.ReceiveWitness && matched.Vout != nil && *matched.Vout > 0 && *matched.Vout <= 32 {
		child = rgb11BatchRecipientID(canonical, *matched.Vout-1)
		outputs := make(map[string]struct{})
		for _, allocation := range receipt.Allocations {
			if allocation.WitnessTxPtr && allocation.OperationID == matched.Allocation.OperationID &&
				allocation.AssignmentType == matched.Allocation.AssignmentType &&
				allocationOutpointTxID(allocation.OutPoint) == matched.TxID {
				outputs[allocation.OutPoint] = struct{}{}
			}
		}
		batch = len(outputs) > 1
	}
	valid := func(id string) bool { return id == canonical || child != "" && id == child }
	if explicit != "" && !valid(explicit) || request.TransferID != "" && !valid(request.TransferID) ||
		explicit != "" && request.TransferID != "" && explicit != request.TransferID {
		return "", ErrRGB11InvoiceMismatch
	}
	if request.TransferID != "" {
		return request.TransferID, nil
	}
	if explicit != "" {
		return explicit, nil
	}
	if batch {
		return child, nil
	}
	return canonical, nil
}

// buildRGB11AcceptedConsignmentCandidate performs core/current validation and
// appends the complete receive closure to records without writing a store.
func (p *rgb11Manager) buildRGB11AcceptedConsignmentCandidate(ctx context.Context,
	snapshot *RGB11WalletSnapshot, requestID string, raw []byte, expectedTxID string,
	expectedVout *uint32) (*RGB11WalletSnapshot, *rgb11wallet.ValidationReceipt,
	*rgb11wallet.TransferState, error) {
	return p.buildRGB11AcceptedConsignmentCandidateWithID(ctx, snapshot, requestID, raw, expectedTxID, expectedVout, "")
}

func (p *rgb11Manager) buildRGB11AcceptedConsignmentCandidateWithID(ctx context.Context,
	snapshot *RGB11WalletSnapshot, requestID string, raw []byte, expectedTxID string,
	expectedVout *uint32, localTransferID string) (*RGB11WalletSnapshot, *rgb11wallet.ValidationReceipt,
	*rgb11wallet.TransferState, error) {
	if p == nil || snapshot == nil || p.rgbManager == nil || p.rgbManager.evidence == nil || len(raw) == 0 {
		return nil, nil, nil, ErrRGB11Inconsistent
	}
	request, err := rgb11ReceiveRequestFromSnapshot(snapshot.EngineRecords, requestID)
	if err != nil {
		return nil, nil, nil, err
	}
	var previousState *rgb11wallet.TransferState
	if request.TransferID != "" {
		state, stateErr := rgb11TransferFromSnapshot(snapshot.ProjectionRecords, request.TransferID)
		if stateErr != nil {
			return nil, nil, nil, stateErr
		}
		if state.Direction != "receive" || state.ReceiveRequestID != requestID ||
			(request.ObjectHash != "" && state.ConsignmentHash != request.ObjectHash) ||
			(request.WitnessTxID != "" && state.WitnessTxID != request.WitnessTxID) {
			return nil, nil, nil, ErrRGB11InvoiceMismatch
		}
		if request.Mode == corewallet.ReceiveWitness && expectedVout == nil {
			if len(state.OutputOutPoints) != 1 {
				return nil, nil, nil, ErrRGB11InvoiceMismatch
			}
			vout, ok := outpointVout(state.OutputOutPoints[0])
			if !ok {
				return nil, nil, nil, ErrRGB11InvoiceMismatch
			}
			expectedVout = &vout
		}
		copy := *state
		previousState = &copy
	}
	invoice, err := invoicing.Parse(request.Invoice)
	if err != nil {
		return nil, nil, nil, err
	}
	// A positive prior ACK is the existing lifecycle commitment that allows a
	// delayed witness to settle after invoice expiry. New/unacknowledged input
	// still obeys the invoice's normal expiry check.
	if request.Status != corewallet.ReceiveAcknowledged {
		if err := invoice.Validate(time.Now().Unix()); err != nil {
			return nil, nil, nil, err
		}
	}
	transportMode, err := rgb11InvoiceTransportMode(invoice)
	if err != nil {
		return nil, nil, nil, err
	}
	var validator *rgb11wallet.NativeConsensusValidator
	switch invoice.Beneficiary.Kind {
	case invoicing.BeneficiaryBlindedSeal:
		concealed, sealErr := request.Seal.Conceal()
		if sealErr != nil || !bytes.Equal(invoice.Beneficiary.BlindedSeal[:], concealed[:]) {
			return nil, nil, nil, ErrRGB11InvoiceMismatch
		}
		validator = rgb11wallet.NewNativeConsensusValidatorWithReveals(request.Seal)
	case invoicing.BeneficiaryWitnessVout:
		witnessScript, scriptErr := invoice.Beneficiary.WitnessScript()
		if scriptErr != nil || !bytes.Equal(request.WitnessScript, witnessScript) {
			return nil, nil, nil, ErrRGB11InvoiceMismatch
		}
		validator = rgb11wallet.NewNativeConsensusValidator()
	default:
		return nil, nil, nil, ErrRGB11InvoiceMismatch
	}
	receipt, err := rgb11wallet.ValidateWith(ctx, validator, raw, p.rgbManager.evidence)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("validate current accepted RGB11 consignment: %w", err)
	}
	if invoice.Contract != nil && invoice.Contract.String() != receipt.ContractID {
		return nil, nil, nil, ErrRGB11InvoiceMismatch
	}
	if invoice.Schema != nil {
		decoded, decodeErr := decodeReceiptSchema(receipt.SchemaID)
		if decodeErr != nil || decoded != *invoice.Schema {
			return nil, nil, nil, ErrRGB11InvoiceMismatch
		}
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	matched, err := p.matchRGB11ReceiveAllocation(receipt, request, invoice, nil, expectedTxID, expectedVout)
	if err != nil {
		return nil, nil, nil, err
	}
	transferID, err := rgb11ReceiveLocalTransferID(request, receipt, matched, localTransferID)
	if err != nil {
		return nil, nil, nil, err
	}
	checked, err := rgb11ReceivedPolicyOpout(matched.Allocation)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := p.checkRGB11RejectPolicy(container, checked); err != nil {
		return nil, nil, nil, err
	}
	history, err := validator.ValidateHistoricalConsignmentReport(ctx, raw, p.rgbManager.evidence)
	if err != nil || history == nil || history.MaterialReceipt == nil {
		return nil, nil, nil, fmt.Errorf("validate accepted RGB11 material history: %w",
			errRGB11ProjectionEvidence(err))
	}
	receipts, err := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load existing accepted RGB11 receipts: %w", err)
	}
	existingReceipt := receipts[receipt.ConsignmentHash]
	if existingReceipt != nil && !sameRGB11ValidationReceiptIdentity(existingReceipt, history.MaterialReceipt) {
		return nil, nil, nil, fmt.Errorf("existing accepted RGB11 receipt identity conflicts with core history: %w",
			rgb11wallet.ErrValidationReceipt)
	}
	selectedReceipt, err := rgb11wallet.SelectHistoricalValidationReceipt(history, existingReceipt,
		[]rgb11wallet.ValidatedAllocation{matched.Allocation})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("select accepted RGB11 receipt allocations: %w", err)
	}
	projectionRecords := snapshot.ProjectionRecords
	if existingReceipt != nil {
		oldHash, hashErr := existingReceipt.Hash()
		if hashErr != nil {
			return nil, nil, nil, hashErr
		}
		newHash, hashErr := selectedReceipt.Hash()
		if hashErr != nil {
			return nil, nil, nil, hashErr
		}
		if oldHash != newHash {
			projectionRecords, err = rgb11wallet.RebindValidatedConsignmentReceiptSnapshot(
				projectionRecords, existingReceipt, selectedReceipt,
			)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("rebind accepted RGB11 receipt cache: %w", err)
			}
		}
	}
	receipt = selectedReceipt
	receiptHash, err := receipt.Hash()
	if err != nil {
		return nil, nil, nil, err
	}
	allocation := matched.Allocation
	sealCommitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, allocation.SealDisclosure)
	asset := &indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}
	proof := &rgb11wallet.AllocationProof{
		OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
		OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType,
		AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
		StateData: append([]byte(nil), allocation.StateData...), SealCommitment: hex.EncodeToString(sealCommitment[:]),
		SealDisclosure:  append([]byte(nil), allocation.SealDisclosure...),
		ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
		// The proof witness is the Bitcoin transaction that created the exact
		// carrier outpoint. For blind seals, matched.TxID is the RGB transition
		// transaction used by the receive lifecycle and can differ from that
		// older carrier producer. TransferState below retains the transition ID.
		WitnessTxID: allocationOutpointTxID(allocation.OutPoint), Status: "valid", CarrierBinding: matched.Binding,
	}
	output := indexer.NewTxOutput(matched.UTXO.Value)
	output.OutPointStr = allocation.OutPoint
	output.OutValue.PkScript = append([]byte(nil), matched.UTXO.PkScript...)
	candidate := *snapshot
	candidate.ProjectionRecords = projectionRecords
	candidate.ProjectionRecords, err = rgb11wallet.AddValidatedConsignmentProjectionSnapshot(
		candidate.ProjectionRecords, raw, receipt,
		[]rgb11wallet.ProjectionReplacement{{Output: output, Asset: asset, Proof: proof}},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("add accepted RGB11 allocation projection: %w", err)
	}
	contractFile, _, err := rgb11ContractFromValidatedHistory(raw, receipt.ContractID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("derive accepted RGB11 contract object: %w", err)
	}
	candidate.ProjectionRecords, err = rgb11wallet.AddContractObjectSnapshotReference(
		candidate.ProjectionRecords, receipt.ContractID, contractFile,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reference accepted RGB11 contract object: %w", err)
	}
	expiry := int64(0)
	if invoice.Expiry != nil {
		expiry = *invoice.Expiry
	}
	state := &rgb11wallet.TransferState{
		TransferID: transferID, Direction: "receive", Asset: *asset,
		RecipientID: request.RecipientID, ReceiveRequestID: requestID, Invoice: request.Invoice,
		OutputOutPoints: []string{allocation.OutPoint}, MinConfirmations: 1, Expiry: expiry,
		ConsignmentHash: receipt.ConsignmentHash, WitnessTxID: matched.TxID,
		AckStatus: "accepted", Status: "pending", RelayRecordKey: request.RelayKey,
		AckRecordKey: request.AckKey, RelayDurability: "LOCAL_ONLY", RelayExpiry: expiry,
		TransportMode: transportMode,
	}
	if previousState != nil {
		if previousState.TransferID != state.TransferID || previousState.Direction != state.Direction ||
			previousState.ReceiveRequestID != state.ReceiveRequestID ||
			previousState.ConsignmentHash != state.ConsignmentHash || previousState.WitnessTxID != state.WitnessTxID {
			return nil, nil, nil, ErrRGB11InvoiceMismatch
		}
		// Keep durable transport identity from the prior receive, but retain
		// the accepted lifecycle stage constructed above. AckStatus is mutable
		// phase data and is not part of the stable transfer identity.
		state.TransportMode, state.Invoice = previousState.TransportMode, previousState.Invoice
		state.RelayRecordKey, state.AckRecordKey = previousState.RelayRecordKey, previousState.AckRecordKey
		state.RelayDurability, state.RelayExpiry = previousState.RelayDurability, previousState.RelayExpiry
		// Preserve the exact persisted slice shape for identity fields. Gob can
		// distinguish nil from an allocated empty slice, and the immutable
		// transfer comparison intentionally uses that encoded identity.
		state.BatchTransferIDs, state.InputOutPoints = previousState.BatchTransferIDs, previousState.InputOutPoints
		if previousState.AddressMode {
			state.AddressMode, state.AddressMessageID = true, previousState.AddressMessageID
			state.SenderAccountID, state.ReceiverAccountID = previousState.SenderAccountID, previousState.ReceiverAccountID
			state.ReceiverAddress = previousState.ReceiverAddress
			state.ReceiveCapabilityKey, state.ReceiveCapabilityHash = previousState.ReceiveCapabilityKey, previousState.ReceiveCapabilityHash
			state.DeliveryMode, state.DeliveryObjectID = previousState.DeliveryMode, previousState.DeliveryObjectID
			state.DeliveryRecordKey, state.DeliveryRecordHash = previousState.DeliveryRecordKey, previousState.DeliveryRecordHash
			state.DeliveryTemporary, state.DeliveryIssueHeight = previousState.DeliveryTemporary, previousState.DeliveryIssueHeight
			state.DeliveryTTL, state.DeliveryAcknowledged = previousState.DeliveryTTL, previousState.DeliveryAcknowledged
			state.DeliveryCacheCompacted, state.SyntheticInvoiceRemoved = previousState.DeliveryCacheCompacted, previousState.SyntheticInvoiceRemoved
		}
	}
	if request.WitnessTxID != "" {
		state.WitnessTxID = request.WitnessTxID
	}
	if state.WitnessTxID == "" {
		state.WitnessTxID = allocationOutpointTxID(allocation.OutPoint)
	}
	candidate.ProjectionRecords, err = rgb11wallet.UpsertProjectionSnapshotTransfer(candidate.ProjectionRecords, state)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("upsert accepted RGB11 transfer state: %w", err)
	}
	candidate.ProjectionRecords, err = rgb11wallet.RemoveProjectionSnapshotKeys(candidate.ProjectionRecords,
		"prepared-receive-"+transferID, "receive-reservation-"+requestID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("remove prepared RGB11 receive records: %w", err)
	}
	request.Status = corewallet.ReceiveAccepted
	request.TransferID = transferID
	request.ObjectHash = receipt.ConsignmentHash
	candidate.EngineRecords, err = rgb11wallet.UpsertEngineSnapshotReceive(candidate.EngineRecords, request)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("upsert accepted RGB11 engine request: %w", err)
	}
	return &candidate, receipt, state, nil
}

func (p *rgb11Manager) buildRGB11PreparedReceiveCandidate(ctx context.Context,
	snapshot *RGB11WalletSnapshot, requestID string, raw []byte, expectedTxID string,
	expectedVout *uint32, localTransferID string) (*RGB11WalletSnapshot,
	*rgb11wallet.ValidationReceipt, error) {
	if p == nil || snapshot == nil || p.rgbManager == nil || p.rgbManager.evidence == nil || len(raw) == 0 {
		return nil, nil, ErrRGB11Inconsistent
	}
	request, err := rgb11ReceiveRequestFromSnapshot(snapshot.EngineRecords, requestID)
	if err != nil {
		return nil, nil, err
	}
	if request.Status != corewallet.ReceivePrepared && request.Status != corewallet.ReceiveRelayed &&
		request.Status != corewallet.ReceiveAcknowledged {
		return nil, nil, corewallet.ErrReceiveState
	}
	invoice, err := invoicing.Parse(request.Invoice)
	if err != nil {
		return nil, nil, err
	}
	if err := invoice.Validate(time.Now().Unix()); err != nil {
		return nil, nil, err
	}
	var validator *rgb11wallet.NativeConsensusValidator
	switch invoice.Beneficiary.Kind {
	case invoicing.BeneficiaryBlindedSeal:
		concealed, sealErr := request.Seal.Conceal()
		if sealErr != nil || !bytes.Equal(invoice.Beneficiary.BlindedSeal[:], concealed[:]) {
			return nil, nil, ErrRGB11InvoiceMismatch
		}
		validator = rgb11wallet.NewNativeConsensusValidatorWithReveals(request.Seal)
	case invoicing.BeneficiaryWitnessVout:
		witnessScript, scriptErr := invoice.Beneficiary.WitnessScript()
		if scriptErr != nil || !bytes.Equal(request.WitnessScript, witnessScript) {
			return nil, nil, ErrRGB11InvoiceMismatch
		}
		validator = rgb11wallet.NewNativeConsensusValidator()
	default:
		return nil, nil, ErrRGB11InvoiceMismatch
	}
	prepared, err := rgb11wallet.ValidatePreparedWith(ctx, validator, raw, p.rgbManager.evidence)
	if err != nil {
		return nil, nil, err
	}
	receipt := prepared.Receipt
	if invoice.Contract != nil && invoice.Contract.String() != receipt.ContractID {
		return nil, nil, ErrRGB11InvoiceMismatch
	}
	if invoice.Schema != nil {
		decoded, decodeErr := decodeReceiptSchema(receipt.SchemaID)
		if decodeErr != nil || decoded != *invoice.Schema {
			return nil, nil, ErrRGB11InvoiceMismatch
		}
	}
	actualTxID := prepared.WitnessTxIDs[0]
	if actualTxID == "" || (expectedTxID != "" && expectedTxID != actualTxID) {
		return nil, nil, errors.New("RGB11 proxy metadata does not match the prepared consignment")
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return nil, nil, err
	}
	matchTxID := ""
	matchVout := (*uint32)(nil)
	if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
		matchTxID, matchVout = actualTxID, expectedVout
	}
	matched, err := p.matchRGB11ReceiveAllocation(receipt, request, invoice,
		prepared.Outputs, matchTxID, matchVout)
	if err != nil {
		return nil, nil, err
	}
	if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout && matched.TxID != actualTxID {
		return nil, nil, errors.New("RGB11 proxy metadata does not match the prepared consignment")
	}
	checked, err := rgb11ReceivedPolicyOpout(matched.Allocation)
	if err != nil {
		return nil, nil, err
	}
	if err := p.checkRGB11RejectPolicy(container, checked); err != nil {
		return nil, nil, err
	}
	transferID, err := rgb11ReceiveLocalTransferID(request, receipt, matched, localTransferID)
	if err != nil {
		return nil, nil, err
	}
	if request.Status == corewallet.ReceiveAcknowledged {
		previous, err := rgb11TransferFromSnapshot(snapshot.ProjectionRecords, transferID)
		if err != nil || request.TransferID != transferID || request.ObjectHash != receipt.ConsignmentHash ||
			request.WitnessTxID != actualTxID || previous.Status != "awaiting_broadcast" ||
			previous.Direction != "receive" || previous.ReceiveRequestID != requestID ||
			previous.RecipientID != request.RecipientID || previous.Invoice != request.Invoice ||
			previous.Asset.Name != matched.Allocation.AssetName || previous.Asset.Amount.Cmp(&matched.Allocation.Amount) != 0 ||
			previous.ConsignmentHash != receipt.ConsignmentHash ||
			previous.WitnessTxID != actualTxID || len(previous.OutputOutPoints) != 1 ||
			previous.OutputOutPoints[0] != matched.Allocation.OutPoint {
			return nil, nil, ErrRGB11InvoiceMismatch
		}
		// The same ACKed material is already durable. Preserve its complete
		// immutable row (including transport metadata) on a repeated prepare.
		return snapshot, receipt, nil
	}
	candidate := *snapshot
	candidate.ProjectionRecords, err = rgb11wallet.AddPreparedConsignmentObjectSnapshot(
		candidate.ProjectionRecords, raw,
	)
	if err != nil {
		return nil, nil, err
	}
	expiry := int64(0)
	if invoice.Expiry != nil {
		expiry = *invoice.Expiry
	}
	receiveTransport, err := rgb11InvoiceTransportMode(invoice)
	if err != nil {
		return nil, nil, err
	}
	relayDurability := "STANDARD_PROXY"
	if receiveTransport == "out-of-band" {
		relayDurability = "LOCAL_ONLY"
	}
	state := &rgb11wallet.TransferState{
		TransferID: transferID, Direction: "receive",
		Asset: indexer.AssetInfo{Name: matched.Allocation.AssetName,
			Amount: *matched.Allocation.Amount.Clone(), BindingSat: 0},
		RecipientID: request.RecipientID, ReceiveRequestID: requestID, Invoice: request.Invoice,
		OutputOutPoints: []string{matched.Allocation.OutPoint}, MinConfirmations: 1, Expiry: expiry,
		ConsignmentHash: receipt.ConsignmentHash, WitnessTxID: actualTxID,
		AckStatus: "accepted", Status: "awaiting_broadcast", RelayRecordKey: request.RelayKey,
		AckRecordKey: request.AckKey, RelayDurability: relayDurability, RelayExpiry: expiry,
		TransportMode: receiveTransport,
	}
	candidate.ProjectionRecords, err = rgb11wallet.UpsertProjectionSnapshotTransfer(candidate.ProjectionRecords, state)
	if err != nil {
		return nil, nil, err
	}
	candidate.ProjectionRecords, err = rgb11wallet.UpsertProjectionSnapshotPreparedReceive(
		candidate.ProjectionRecords, transferID, requestID,
	)
	if err != nil {
		return nil, nil, err
	}
	request.Status = corewallet.ReceiveAcknowledged
	request.TransferID = transferID
	request.ObjectHash = receipt.ConsignmentHash
	request.WitnessTxID = actualTxID
	candidate.EngineRecords, err = rgb11wallet.UpsertEngineSnapshotReceive(candidate.EngineRecords, request)
	if err != nil {
		return nil, nil, err
	}
	return &candidate, receipt, nil
}

func rgb11LocalChangeAllocations(allocations []rgb11wallet.ValidatedAllocation,
	pending *rgb11wallet.PendingTransfer) (map[string]rgb11wallet.ValidatedAllocation, error) {
	if pending == nil || pending.State.WitnessTxID == "" || len(pending.ChangeSeals) == 0 {
		return nil, rgb11wallet.ErrInvalidProof
	}
	wanted := make(map[string]seals.GraphBlindSeal, len(pending.ChangeSeals))
	for _, seal := range pending.ChangeSeals {
		outpoint := fmt.Sprintf("%s:%d", pending.State.WitnessTxID, seal.Vout)
		if _, duplicate := wanted[outpoint]; duplicate {
			return nil, fmt.Errorf("%w: duplicate local change seal outpoint %s", rgb11wallet.ErrInvalidProof, outpoint)
		}
		wanted[outpoint] = seal
	}
	matched := make(map[string]rgb11wallet.ValidatedAllocation, len(wanted))
	for _, allocation := range allocations {
		seal, expected := wanted[allocation.OutPoint]
		if !expected {
			continue
		}
		strict, err := seal.StrictBytes()
		if err != nil || !allocation.WitnessTxPtr || allocation.WitnessTxID != pending.State.WitnessTxID ||
			allocation.SealBlinding != seal.Blinding || !bytes.Equal(strict, allocation.SealDisclosure) {
			return nil, fmt.Errorf("RGB11 local change %s does not match its exact seal: %w",
				allocation.OutPoint, rgb11wallet.ErrInvalidProof)
		}
		key := allocation.OutPoint + "|" + allocation.AssetName.String()
		if _, duplicate := matched[key]; duplicate {
			return nil, fmt.Errorf("%w: same-asset multiple Opouts cannot share projection key %s",
				rgb11wallet.ErrInvalidProof, key)
		}
		matched[key] = allocation
	}
	if len(matched) != len(wanted) {
		return nil, fmt.Errorf("%w: core history does not contain every local change seal", rgb11wallet.ErrInvalidProof)
	}
	return matched, nil
}

func (p *rgb11Manager) addRGB11LocalChangeCandidate(ctx context.Context,
	projectionRecords []rgb11wallet.SnapshotRecord, pending *rgb11wallet.PendingTransfer) ([]rgb11wallet.SnapshotRecord, error) {
	if pending == nil || len(pending.ChangeSeals) == 0 || len(pending.LocalConsignment) == 0 {
		return projectionRecords, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if pending.LocalObjectHash == "" {
		return nil, rgb11wallet.ErrValidationReceipt
	}
	digest := sha256.Sum256(pending.LocalConsignment)
	if hex.EncodeToString(digest[:]) != pending.LocalObjectHash {
		return nil, fmt.Errorf("local RGB11 consignment hash mismatch: %w", rgb11wallet.ErrValidationReceipt)
	}
	validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(pending.ChangeSeals...)
	history, err := validator.ValidateHistoricalConsignmentReport(
		ctx, pending.LocalConsignment, p.rgbManager.evidence,
	)
	if err != nil || history == nil || history.MaterialReceipt == nil {
		return nil, fmt.Errorf("validate RGB11 local change history: %w", errRGB11ProjectionEvidence(err))
	}
	allocations, err := rgb11LocalChangeAllocations(history.MaterialReceipt.Allocations, pending)
	if err != nil {
		return nil, err
	}
	receipts, err := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(projectionRecords)
	if err != nil {
		return nil, err
	}
	existingReceipt := receipts[pending.LocalObjectHash]
	var existingReceiptHash string
	if existingReceipt != nil {
		if !sameRGB11ValidationReceiptIdentity(existingReceipt, history.MaterialReceipt) {
			return nil, fmt.Errorf("local RGB11 receipt conflicts with its core history: %w",
				rgb11wallet.ErrValidationReceipt)
		}
		authenticated, authErr := rgb11wallet.SelectHistoricalValidationReceipt(history, existingReceipt, nil)
		if authErr != nil {
			return nil, fmt.Errorf("authenticate existing local RGB11 receipt against core history: %w", authErr)
		}
		existingReceiptHash, err = existingReceipt.Hash()
		if err != nil {
			return nil, err
		}
		authenticatedHash, hashErr := authenticated.Hash()
		if hashErr != nil || authenticatedHash != existingReceiptHash {
			return nil, fmt.Errorf("existing local RGB11 receipt is not bound to its selected core allocations: %w",
				rgb11wallet.ErrValidationReceipt)
		}
	}
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(projectionRecords)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]*rgb11wallet.AllocationProof, len(proofs))
	for _, proof := range proofs {
		key := proof.OutPoint + "|" + proof.AssetName.String()
		if existing[key] != nil {
			return nil, rgb11wallet.ErrInvalidProof
		}
		existing[key] = proof
	}
	outputs, err := rgb11wallet.OutputsFromProjectionSnapshot(projectionRecords)
	if err != nil {
		return nil, err
	}
	type newChange struct {
		allocation rgb11wallet.ValidatedAllocation
		utxo       *rgb11wallet.BitcoinUTXO
		binding    *rgb11wallet.CarrierBinding
	}
	newChanges := make([]newChange, 0, len(allocations))
	needed := make([]rgb11wallet.ValidatedAllocation, 0, len(allocations))
	for key, allocation := range allocations {
		proof := existing[key]
		if proof != nil {
			if existingReceipt == nil || proof.ConsignmentHash != pending.LocalObjectHash ||
				proof.ValidationHash != existingReceiptHash ||
				!rgb11ProofMatchesHistoricalReceipt(proof, existingReceipt, outputs[proof.OutPoint], history) {
				return nil, fmt.Errorf("%w: existing local-change proof conflicts with core history %s",
					rgb11wallet.ErrInvalidProof, key)
			}
			continue
		}
		producerTxID := allocationOutpointTxID(allocation.OutPoint)
		status, statusErr := p.rgbManager.evidence.GetTxStatus(producerTxID)
		if statusErr != nil || !validRGB11ObservedTxStatus(status, producerTxID) ||
			(!status.Confirmed && !status.InMempool) {
			return nil, fmt.Errorf("RGB11 local change %s producer status is unknown: %w",
				allocation.OutPoint, errRGB11ProjectionEvidence(statusErr))
		}
		outspend, spendErr := p.rgbManager.evidence.GetOutspend(allocation.OutPoint)
		if spendErr != nil || outspend == nil || outspend.Spent || outspend.SpendingTx != "" {
			return nil, fmt.Errorf("RGB11 local change %s is not positively unspent: %w",
				allocation.OutPoint, errRGB11ProjectionEvidence(spendErr))
		}
		utxo, utxoErr := p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
		if utxoErr != nil || utxo == nil || utxo.OutPoint != allocation.OutPoint {
			return nil, fmt.Errorf("resolve current RGB11 change %s: %w",
				allocation.OutPoint, errRGB11ProjectionEvidence(utxoErr))
		}
		binding, bindErr := p.rgb11CarrierBinding(allocation, utxo)
		if bindErr != nil {
			return nil, bindErr
		}
		newChanges = append(newChanges, newChange{allocation: allocation, utxo: utxo, binding: binding})
		needed = append(needed, allocation)
	}
	if len(newChanges) == 0 {
		return projectionRecords, nil
	}
	selectedReceipt, err := rgb11wallet.SelectHistoricalValidationReceipt(history, existingReceipt, needed)
	if err != nil {
		return nil, fmt.Errorf("select core-authenticated RGB11 local-change receipt: %w", err)
	}
	selectedHash, err := selectedReceipt.Hash()
	if err != nil {
		return nil, err
	}
	if existingReceipt != nil && selectedHash != existingReceiptHash {
		projectionRecords, err = rgb11wallet.RebindValidatedConsignmentReceiptSnapshot(
			projectionRecords, existingReceipt, selectedReceipt,
		)
		if err != nil {
			return nil, err
		}
	}
	replacements := make([]rgb11wallet.ProjectionReplacement, 0, len(newChanges))
	for _, change := range newChanges {
		allocation := change.allocation
		commitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, allocation.SealDisclosure)
		asset := &indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}
		newProof := &rgb11wallet.AllocationProof{
			OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
			OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType,
			AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
			StateData:      append([]byte(nil), allocation.StateData...),
			SealCommitment: hex.EncodeToString(commitment[:]), SealDisclosure: append([]byte(nil), allocation.SealDisclosure...),
			ConsignmentHash: selectedReceipt.ConsignmentHash, ValidationHash: selectedHash,
			WitnessTxID: allocationOutpointTxID(allocation.OutPoint), Status: "valid", CarrierBinding: change.binding,
		}
		output := indexer.NewTxOutput(change.utxo.Value)
		output.OutPointStr = allocation.OutPoint
		output.OutValue.PkScript = append([]byte(nil), change.utxo.PkScript...)
		replacements = append(replacements, rgb11wallet.ProjectionReplacement{Output: output, Asset: asset, Proof: newProof})
	}
	return rgb11wallet.AddValidatedConsignmentProjectionSnapshot(
		projectionRecords, pending.LocalConsignment, selectedReceipt, replacements,
	)
}

// addRGB11PreparedChangeCapacityPreview adds the core-validated terminal
// shape only to an ephemeral sizing snapshot. This also handles a prepared
// journal whose witness has since been observed. The snapshot is never passed
// to projection resolution or persistence.
func (p *rgb11Manager) addRGB11PreparedChangeCapacityPreview(ctx context.Context,
	snapshot *RGB11WalletSnapshot, pending *rgb11wallet.PendingTransfer) (*RGB11WalletSnapshot, error) {
	if snapshot == nil || pending == nil || len(pending.ChangeSeals) == 0 || len(pending.LocalConsignment) == 0 {
		return snapshot, nil
	}
	validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(pending.ChangeSeals...)
	capacity, err := p.rgb11TerminalCapacityMaterial(ctx, validator, pending.LocalConsignment,
		pending.State.WitnessTxID, pending.SignedTx)
	if err != nil {
		return nil, fmt.Errorf("validate RGB11 change for recovery capacity: %w", errRGB11ProjectionEvidence(err))
	}
	wantHash := sha256.Sum256(pending.LocalConsignment)
	if hex.EncodeToString(wantHash[:]) != pending.LocalObjectHash && pending.LocalObjectHash != "" {
		return nil, rgb11wallet.ErrValidationReceipt
	}
	history := capacity.History
	allocations, err := rgb11LocalChangeAllocations(history.MaterialReceipt.Allocations, pending)
	if err != nil {
		return nil, fmt.Errorf("match core RGB11 change history for recovery capacity: %w", err)
	}
	receipts, err := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	existingReceipt := receipts[pending.LocalObjectHash]
	if existingReceipt != nil && !sameRGB11ValidationReceiptIdentity(existingReceipt, history.MaterialReceipt) {
		return nil, rgb11wallet.ErrValidationReceipt
	}
	needed := make([]rgb11wallet.ValidatedAllocation, 0, len(allocations))
	for _, allocation := range allocations {
		needed = append(needed, allocation)
	}
	receipt, err := rgb11wallet.SelectHistoricalValidationReceipt(history, existingReceipt, needed)
	if err != nil {
		return nil, fmt.Errorf("select RGB11 change receipt for recovery capacity: %w", err)
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return nil, err
	}
	candidate := *snapshot
	if existingReceipt != nil {
		oldHash, hashErr := existingReceipt.Hash()
		if hashErr != nil {
			return nil, hashErr
		}
		if oldHash != receiptHash {
			candidate.ProjectionRecords, err = rgb11wallet.RebindValidatedConsignmentReceiptSnapshot(
				candidate.ProjectionRecords, existingReceipt, receipt,
			)
			if err != nil {
				return nil, err
			}
		}
	}
	seenProofs := make(map[string]bool, len(allocations))
	replacements := make([]rgb11wallet.ProjectionReplacement, 0, len(allocations))
	for _, allocation := range allocations {
		key := allocation.OutPoint + "|" + allocation.AssetName.String()
		if seenProofs[key] {
			return nil, fmt.Errorf("%w: multiple core Opouts map to one projection key %s", rgb11wallet.ErrInvalidProof, key)
		}
		seenProofs[key] = true
		utxo := capacity.Outputs[allocation.OutPoint]
		if utxo == nil {
			return nil, fmt.Errorf("prepared RGB11 change output %s is missing", allocation.OutPoint)
		}
		binding, err := p.rgb11CarrierBinding(allocation, utxo)
		if err != nil {
			return nil, err
		}
		commitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, allocation.SealDisclosure)
		proof := &rgb11wallet.AllocationProof{
			OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
			OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType,
			AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
			StateData:      append([]byte(nil), allocation.StateData...),
			SealCommitment: hex.EncodeToString(commitment[:]), SealDisclosure: append([]byte(nil), allocation.SealDisclosure...),
			ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
			WitnessTxID: allocationOutpointTxID(allocation.OutPoint), Status: "valid", CarrierBinding: binding,
		}
		output := indexer.NewTxOutput(utxo.Value)
		output.OutPointStr = allocation.OutPoint
		output.OutValue.PkScript = append([]byte(nil), utxo.PkScript...)
		asset := &indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}
		replacements = append(replacements, rgb11wallet.ProjectionReplacement{Output: output, Asset: asset, Proof: proof})
	}
	if len(replacements) == 0 {
		return snapshot, nil
	}
	candidate.ProjectionRecords, err = rgb11wallet.AddValidatedConsignmentProjectionSnapshot(
		candidate.ProjectionRecords, pending.LocalConsignment, receipt, replacements,
	)
	if err != nil {
		return nil, err
	}
	return &candidate, nil
}

func (p *rgb11Manager) buildRGB11RefreshCandidate(ctx context.Context,
	current *RGB11WalletSnapshot, now int64) (*RGB11WalletSnapshot, *RGB11RefreshResult, error) {
	if p == nil || current == nil || p.rgbManager == nil || p.rgbManager.evidence == nil {
		return nil, nil, ErrRGB11Inconsistent
	}
	candidate := *current
	result := &RGB11RefreshResult{}
	transfers, err := rgb11wallet.TransfersFromProjectionSnapshot(candidate.ProjectionRecords)
	if err != nil {
		return nil, nil, err
	}
	pendings, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(candidate.ProjectionRecords)
	if err != nil {
		return nil, nil, err
	}
	pendingByID := make(map[string]*rgb11wallet.PendingTransfer, len(pendings))
	for _, pending := range pendings {
		pending.RecipientConsignment = rgb11SnapshotObject(candidate.ProjectionRecords, pending.RecipientObjectHash)
		pending.LocalConsignment = rgb11SnapshotObject(candidate.ProjectionRecords, pending.LocalObjectHash)
		if pending.RecipientObjectHash != "" && len(pending.RecipientConsignment) == 0 ||
			pending.LocalObjectHash != "" && len(pending.LocalConsignment) == 0 {
			return nil, nil, fmt.Errorf("%w: pending RGB11 consignment object is missing", ErrRGB11Inconsistent)
		}
		pendingByID[pending.State.TransferID] = pending
	}

	// Expire only unused receive reservations. The existing request and key
	// remain durable history; only the reservation row leaves this candidate.
	requests := make(map[string]*corewallet.ReceiveRequest)
	for _, record := range candidate.EngineRecords {
		if !bytes.HasPrefix([]byte(record.Key), []byte("wallet/receive/")) {
			continue
		}
		request, decodeErr := corewallet.DecodeReceiveRequest(record.Value)
		if decodeErr != nil || request == nil || record.Key != "wallet/receive/"+request.RequestID {
			return nil, nil, corewallet.ErrInvalidReceive
		}
		requests[request.RequestID] = request
	}
	usedRequests := make(map[string]bool)
	for _, state := range transfers {
		if state.Direction == "receive" && state.ReceiveRequestID != "" {
			usedRequests[state.ReceiveRequestID] = true
		}
	}
	receiveReservations, err := rgb11wallet.ReceiveReservationsFromProjectionSnapshot(candidate.ProjectionRecords)
	if err != nil {
		return nil, nil, err
	}
	for _, reservation := range receiveReservations {
		if reservation.Expiry == 0 || reservation.Expiry > now {
			continue
		}
		request := requests[reservation.RequestID]
		if request == nil {
			return nil, nil, fmt.Errorf("%w: receive reservation has no request", ErrRGB11Inconsistent)
		}
		used := usedRequests[reservation.RequestID] || request.Status != corewallet.ReceivePrepared ||
			request.TransferID != "" || request.WitnessTxID != ""
		if used {
			continue
		}
		candidate.ProjectionRecords, err = rgb11wallet.RemoveProjectionSnapshotKeys(
			candidate.ProjectionRecords, "receive-reservation-"+reservation.RequestID,
		)
		if err != nil {
			return nil, nil, err
		}
	}

	for _, state := range transfers {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if state.Direction == "receive" {
			if state.Status == "rejected" {
				status, statusErr := p.rgbManager.evidence.GetTxStatus(state.WitnessTxID)
				if statusErr != nil {
					return nil, nil, fmt.Errorf("RGB11 rejected receive %s status is unknown: %w", state.TransferID, statusErr)
				}
				if status != nil && (status.InMempool || status.Confirmed) {
					return nil, nil, fmt.Errorf("%w: rejected RGB11 receive %s witness is visible", ErrRGB11Inconsistent, state.TransferID)
				}
				continue
			}
			if state.Status == "awaiting_broadcast" {
				requestID := ""
				for _, record := range candidate.ProjectionRecords {
					if record.Key == "prepared-receive-"+state.TransferID {
						requestID = string(record.Value)
						break
					}
				}
				raw := rgb11SnapshotObject(candidate.ProjectionRecords, state.ConsignmentHash)
				if requestID == "" || len(raw) == 0 {
					return nil, nil, fmt.Errorf("%w: prepared receive closure is incomplete", ErrRGB11Inconsistent)
				}
				accepted, _, _, acceptErr := p.buildRGB11AcceptedConsignmentCandidate(
					ctx, &candidate, requestID, raw, "", nil,
				)
				if acceptErr != nil {
					if errors.Is(acceptErr, coreconsignment.ErrWitnessUnresolved) ||
						errors.Is(acceptErr, coreconsignment.ErrOutpointUnknown) {
						result.Pending++
						continue
					}
					return nil, nil, fmt.Errorf("accept prepared RGB11 receive %s: %w", state.TransferID, acceptErr)
				}
				candidate = *accepted
				state, err = rgb11TransferFromSnapshot(candidate.ProjectionRecords, state.TransferID)
				if err != nil {
					return nil, nil, err
				}
			}
			status, statusErr := p.rgbManager.evidence.GetTxStatus(state.WitnessTxID)
			if statusErr != nil {
				return nil, nil, fmt.Errorf("RGB11 receive %s witness status is unknown: %w", state.TransferID, statusErr)
			}
			if status == nil || (!status.InMempool && !status.Confirmed) {
				for _, outpoint := range state.OutputOutPoints {
					if allocationOutpointTxID(outpoint) != state.WitnessTxID {
						continue
					}
					utxo, utxoErr := p.rgbManager.evidence.GetUTXO(outpoint)
					if utxoErr != nil {
						return nil, nil, fmt.Errorf("RGB11 receive %s output status is unknown: %w", state.TransferID, utxoErr)
					}
					if utxo != nil {
						status = &rgb11wallet.BitcoinTxStatus{TxID: state.WitnessTxID,
							InMempool: utxo.Confirmations == 0, Confirmed: utxo.Confirmations > 0,
							Confirmations: utxo.Confirmations}
						break
					}
				}
			}
			if status == nil || (!status.InMempool && !status.Confirmed) {
				result.Unresolved++
				continue
			}
			required := max(int64(state.MinConfirmations), 1)
			if status.Confirmed && status.Confirmations >= required {
				if state.Status != "settled" {
					result.Settled++
				}
				state.Status = "settled"
			} else {
				if state.Status == "settled" {
					result.Reorged++
				}
				state.Status = "pending"
				result.Pending++
			}
			candidate.ProjectionRecords, err = rgb11wallet.UpsertProjectionSnapshotTransfer(
				candidate.ProjectionRecords, state,
			)
			if err != nil {
				return nil, nil, err
			}
			continue
		}

		pending := pendingByID[state.TransferID]
		if pending == nil || !rgb11TransferHasExpectedSpend(state) {
			continue
		}
		pending.State = *state
		if err := validateRGB11PendingTransaction(pending); err != nil {
			return nil, nil, err
		}
		status, statusErr := p.rgbManager.evidence.GetTxStatus(state.WitnessTxID)
		visible := statusErr == nil && status != nil && status.TxID == state.WitnessTxID &&
			(status.InMempool || status.Confirmed)
		if !visible && statusErr == nil {
			for _, outpoint := range state.OutputOutPoints {
				if allocationOutpointTxID(outpoint) != state.WitnessTxID {
					continue
				}
				utxo, utxoErr := p.rgbManager.evidence.GetUTXO(outpoint)
				if utxoErr != nil {
					continue
				}
				if utxo != nil {
					status = &rgb11wallet.BitcoinTxStatus{TxID: state.WitnessTxID,
						InMempool: utxo.Confirmations == 0, Confirmed: utxo.Confirmations > 0,
						Confirmations: utxo.Confirmations}
					visible = true
					break
				}
			}
		}
		// A lagging outspend/output index must not hide an exact confirmed witness.
		// Reuse the existing raw transaction and input identity verification once.
		if !visible && statusErr == nil && len(state.InputOutPoints) > 0 {
			if verified, ok := verifyRGB11ExpectedSpend(p.rgbManager.evidence, state.InputOutPoints[0], state.WitnessTxID); ok {
				status, visible = verified, true
			}
		}
		if visible {
			updatedRecords, changeErr := p.addRGB11LocalChangeCandidate(ctx, candidate.ProjectionRecords, pending)
			if changeErr != nil {
				return nil, nil, fmt.Errorf("RGB11 transfer %s local change is not current: %w", state.TransferID, changeErr)
			}
			candidate.ProjectionRecords = updatedRecords
			required := max(int64(state.MinConfirmations), 1)
			if status.Confirmed && status.Confirmations >= required {
				if pending.State.Status != "settled" {
					result.Settled++
				}
				pending.State.Status = "settled"
			} else {
				if pending.State.Status == "settled" {
					result.Reorged++
				}
				pending.State.Status = "pending"
				result.Pending++
			}
			candidate.ProjectionRecords, err = rgb11wallet.UpsertProjectionSnapshotPending(
				candidate.ProjectionRecords, pending,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("update settled RGB11 pending %s: %w", state.TransferID, err)
			}
			continue
		}

		if statusErr != nil {
			result.Unresolved++
			continue
		}
		allInputsUnspent := true
		unknownSpend := false
		for _, outpoint := range state.InputOutPoints {
			spend, spendErr := p.rgbManager.evidence.GetOutspend(outpoint)
			if spendErr != nil || spend == nil {
				unknownSpend = true
				allInputsUnspent = false
				continue
			}
			if !spend.Spent {
				continue
			}
			allInputsUnspent = false
			if spend.SpendingTx == state.WitnessTxID {
				if verified, ok := verifyRGB11ExpectedSpend(p.rgbManager.evidence, outpoint, state.WitnessTxID); ok {
					status, visible = verified, true
				}
				unknownSpend = !visible
				continue
			}
			if spend.SpendingTx == "" || spend.SpendingTx == "unknown" {
				unknownSpend = true
				continue
			}
			// The unified resolver verifies replacement spends and journal retirement.
			unknownSpend = true
		}
		if visible {
			updatedRecords, changeErr := p.addRGB11LocalChangeCandidate(ctx, candidate.ProjectionRecords, pending)
			if changeErr != nil {
				return nil, nil, fmt.Errorf("RGB11 transfer %s local change remains unresolved: %w", state.TransferID, changeErr)
			}
			candidate.ProjectionRecords = updatedRecords
			pending.State.Status = "pending"
			result.Pending++
			candidate.ProjectionRecords, err = rgb11wallet.UpsertProjectionSnapshotPending(candidate.ProjectionRecords, pending)
			if err != nil {
				return nil, nil, fmt.Errorf("update pending RGB11 transfer %s: %w", state.TransferID, err)
			}
			continue
		}

		if allInputsUnspent && !unknownSpend && state.Status == "settled" {
			positiveParents := true
			for _, outpoint := range state.InputOutPoints {
				utxo, utxoErr := p.rgbManager.evidence.GetUTXO(outpoint)
				if utxoErr != nil || utxo == nil || utxo.Confirmations <= 0 {
					positiveParents = false
					break
				}
			}
			if positiveParents {
				pending.State.Status = "orphaned"
				result.Reorged++
				candidate.ProjectionRecords, err = rgb11wallet.UpsertProjectionSnapshotPending(candidate.ProjectionRecords, pending)
				if err != nil {
					return nil, nil, err
				}
				continue
			}
		}
		result.Unresolved++
	}
	return &candidate, result, nil
}

func rgb11SnapshotChanged(left, right *RGB11WalletSnapshot) bool {
	if left == nil || right == nil || left.WalletID != right.WalletID ||
		left.AccountIndex != right.AccountIndex || left.EngineBuildID != right.EngineBuildID {
		return true
	}
	return !sameRGB11SnapshotRecords(left.ProjectionRecords, right.ProjectionRecords) ||
		!sameRGB11SnapshotRecords(left.EngineRecords, right.EngineRecords)
}

func sameRGB11SnapshotRecords(left, right []rgb11wallet.SnapshotRecord) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Key != right[i].Key || !bytes.Equal(left[i].Value, right[i].Value) {
			return false
		}
	}
	return true
}
