package wallet

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type rgb11RecoveryBundleSize struct {
	RPKRawBytes     int
	RPKEncodedBytes int
	AMDBytes        int
	ADEBytes        int
	DKB1Bytes       int
}

// preflightRGB11TerminalRecovery measures the complete account-managed bundle
// with snapshot substituted for this RGB11 scope. It also previews the known
// terminal material for active operations in every catalog scope. It has no
// persistence or readiness side effects; successful sizing only means the
// known prospective recovery closure fits the existing encoding and blob limits.
func (p *Manager) preflightRGB11TerminalRecovery(scope string,
	snapshot *rgb11wallet.RGB11WalletSnapshot) (rgb11RecoveryBundleSize, error) {
	var size rgb11RecoveryBundleSize
	if p == nil {
		return size, fmt.Errorf("wallet manager is unavailable")
	}
	if snapshot == nil {
		return size, fmt.Errorf("RGB11 terminal recovery snapshot is required")
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return size, fmt.Errorf("RGB11 recovery scope is required")
	}

	p.mutex.RLock()
	if p.accountProfile == nil {
		p.mutex.RUnlock()
		return size, nil
	}
	accountID := p.accountProfile.AccountID
	revision := p.accountProfile.ManagedDataRevision
	secret := append([]byte(nil), p.accountSecret...)
	p.mutex.RUnlock()
	defer zeroBytes(secret)
	if len(secret) != 32 {
		return size, fmt.Errorf("account-managed recovery preflight requires an unlocked account")
	}

	catalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return size, fmt.Errorf("read account-managed recovery catalog: %w", err)
	}
	if catalog.AccountID != accountID {
		return size, fmt.Errorf("account-managed recovery catalog changed during preflight")
	}
	matchedScope := false
	for _, candidate := range catalog.Scopes {
		if candidate.ID() == scope {
			matchedScope = true
			if candidate.AccountIndex != snapshot.AccountIndex {
				return size, fmt.Errorf("RGB11 recovery snapshot account index does not match scope")
			}
			break
		}
	}
	if !matchedScope {
		return size, fmt.Errorf("RGB11 recovery scope %q is not in the account catalog", scope)
	}

	provider := &rgb11AccountManagedDataProvider{owner: p}
	accounts, err := provider.accountsByScope()
	if err != nil {
		return size, fmt.Errorf("enumerate RGB11 recovery scopes for preflight: %w", err)
	}
	encodedByScope := make(map[string][]byte, len(catalog.Scopes))
	for _, candidateScope := range catalog.Scopes {
		accountValue, ok := accounts[candidateScope.ID()]
		if !ok {
			return size, fmt.Errorf("RGB11 recovery scope %q is unavailable during preflight", candidateScope.ID())
		}
		scoped, err := p.newScopedRGB11Manager(accountValue)
		if err != nil {
			return size, fmt.Errorf("open RGB11 recovery scope %q for preflight: %w", candidateScope.ID(), err)
		}
		walletID, err := scoped.RGB11WalletID()
		if err != nil {
			return size, fmt.Errorf("resolve RGB11 wallet for scope %q: %w", candidateScope.ID(), err)
		}
		var candidateSnapshot *rgb11wallet.RGB11WalletSnapshot
		if candidateScope.ID() == scope {
			candidateSnapshot = snapshot
		} else {
			candidateSnapshot, _, err = scoped.exportRGB11WalletSnapshot(walletID)
			if err != nil {
				return size, fmt.Errorf("export RGB11 scope %q for preflight: %w", candidateScope.ID(), err)
			}
		}
		if candidateSnapshot == nil || candidateSnapshot.AccountIndex != candidateScope.AccountIndex ||
			candidateSnapshot.WalletID != walletID {
			return size, fmt.Errorf("RGB11 recovery snapshot does not match scope %q", candidateScope.ID())
		}
		candidateSnapshot, err = scoped.addRGB11PendingTerminalCapacityPreviews(candidateSnapshot)
		if err != nil {
			return size, fmt.Errorf("measure known pending RGB11 terminal material in scope %q: %w",
				candidateScope.ID(), err)
		}
		packageValue, err := rgb11wallet.RecoveryPackageFromSnapshot(candidateSnapshot, 0)
		if err != nil {
			return size, fmt.Errorf("build prospective RGB11 recovery package for scope %q: %w",
				candidateScope.ID(), err)
		}
		encoded, rawBytes, err := rgb11wallet.EncodeRecoveryPackageWithSize(packageValue)
		if err != nil {
			return size, fmt.Errorf("encode prospective RGB11 recovery package for scope %q: %w",
				candidateScope.ID(), err)
		}
		encodedByScope[candidateScope.ID()] = encoded
		if candidateScope.ID() == scope {
			size.RPKRawBytes = rawBytes
			size.RPKEncodedBytes = len(encoded)
		}
	}

	bundle, _, err := p.exportAccountManagedData(catalog, revision)
	if err != nil {
		return size, fmt.Errorf("export current account-managed bundle for preflight: %w", err)
	}
	if revision == 0 {
		bundle.Revision = 1
	} else {
		bundle.Revision = revision + 1
		if bundle.Revision == 0 {
			return size, fmt.Errorf("account-managed data revision overflow during preflight")
		}
	}
	items := make([]account.ManagedDataItem, 0, len(bundle.Items)+len(encodedByScope))
	replaced := make(map[string]bool, len(encodedByScope))
	for _, item := range bundle.Items {
		if item.Provider == rgb11AccountManagedProviderID {
			if encoded, ok := encodedByScope[item.Scope]; ok {
				if replaced[item.Scope] {
					return size, fmt.Errorf("duplicate RGB11 recovery scope %q in account bundle", item.Scope)
				}
				item.Payload = append([]byte(nil), encoded...)
				replaced[item.Scope] = true
			}
		}
		items = append(items, item)
	}
	for scopeID, encoded := range encodedByScope {
		if replaced[scopeID] {
			continue
		}
		items = append(items, account.ManagedDataItem{
			Provider: rgb11AccountManagedProviderID,
			Scope:    scopeID,
			Payload:  append([]byte(nil), encoded...),
		})
	}
	bundle.Items = items
	bundle, err = account.NormalizeManagedDataBundle(bundle)
	if err != nil {
		return size, fmt.Errorf("normalize prospective account-managed bundle: %w", err)
	}
	plain, err := account.EncodeManagedDataBundle(bundle)
	if err != nil {
		return size, fmt.Errorf("encode prospective account-managed AMD bundle: %w", err)
	}
	size.AMDBytes = len(plain)
	envelope, _, err := account.SealManagedDataBundleWithInfo(secret, accountID, bundle, nil)
	if err != nil {
		return size, fmt.Errorf("seal prospective account-managed ADE bundle (AMD=%d bytes): %w",
			size.AMDBytes, err)
	}
	size.ADEBytes = len(envelope)
	size.DKB1Bytes = size.ADEBytes + 8
	blob, err := EncodeDKVSBlobValue(envelope, nil)
	if err != nil {
		return size, fmt.Errorf("encode prospective account-managed DKB1 blob (ADE=%d bytes; DKB1 limit=%d bytes): %w",
			size.ADEBytes, swire.MaxDKVSBlobValueSize, err)
	}
	size.DKB1Bytes = len(blob)
	return size, nil
}

func (p *rgb11Manager) addRGB11PendingTerminalCapacityPreviews(
	snapshot *rgb11wallet.RGB11WalletSnapshot) (*rgb11wallet.RGB11WalletSnapshot, error) {
	if p == nil || snapshot == nil || p.rgbManager == nil {
		return nil, ErrRGB11Inconsistent
	}
	pendings, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	current := snapshot
	for _, pending := range pendings {
		if pending.State.Direction != "send" || !rgb11CapacityPreviewStatus(pending.State.Status) ||
			len(pending.ChangeSeals) == 0 {
			continue
		}
		pending.LocalConsignment = rgb11SnapshotObject(current.ProjectionRecords, pending.LocalObjectHash)
		if len(pending.LocalConsignment) == 0 {
			return nil, fmt.Errorf("pending transfer %s has change seals but no local consignment object",
				pending.State.TransferID)
		}
		if rgb11PendingCapacityPreviewComplete(current.ProjectionRecords, pending) {
			continue
		}
		preview, err := p.addRGB11PreparedChangeCapacityPreview(context.Background(), current, pending)
		if err != nil {
			return nil, fmt.Errorf("pending transfer %s: %w", pending.State.TransferID, err)
		}
		if preview != nil {
			current = preview
		}
	}
	transfers, err := rgb11wallet.TransfersFromProjectionSnapshot(current.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	for _, state := range transfers {
		if state.Direction != "receive" || !rgb11ReceiveCapacityPreviewStatus(state.Status) ||
			state.ReceiveRequestID == "" || state.ConsignmentHash == "" {
			continue
		}
		if rgb11ReceiveCapacityPreviewComplete(current.ProjectionRecords, state) {
			continue
		}
		raw := rgb11SnapshotObject(current.ProjectionRecords, state.ConsignmentHash)
		if len(raw) == 0 {
			return nil, fmt.Errorf("receive transfer %s has no prepared consignment for terminal recovery sizing",
				state.TransferID)
		}
		preview, err := p.addRGB11PreparedReceiveCapacityPreview(context.Background(), current, state, raw)
		if err != nil {
			return nil, fmt.Errorf("receive transfer %s: %w", state.TransferID, err)
		}
		if preview != nil {
			current = preview
		}
	}
	return current, nil
}

func rgb11CapacityPreviewStatus(status string) bool {
	switch status {
	case "prepared", "delivered", "relayed", "broadcast-attempted", "broadcast", "pending":
		return true
	default:
		return false
	}
}

func rgb11ReceiveCapacityPreviewStatus(status string) bool {
	switch status {
	case "awaiting_broadcast", "pending":
		return true
	default:
		return false
	}
}

func rgb11ReceiveCapacityPreviewComplete(records []rgb11wallet.SnapshotRecord,
	state *rgb11wallet.TransferState) bool {
	if state == nil || state.ConsignmentHash == "" || len(state.OutputOutPoints) == 0 {
		return false
	}
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(records)
	if err != nil {
		return false
	}
	for _, outpoint := range state.OutputOutPoints {
		found := false
		for _, proof := range proofs {
			if proof != nil && proof.OutPoint == outpoint && proof.ConsignmentHash == state.ConsignmentHash {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, record := range records {
		if record.Key == "validation-"+state.ConsignmentHash {
			return true
		}
	}
	return false
}

// rgb11CapacityMaterial is exclusively an ephemeral sizing input. Historical
// material and raw carrier outputs never assert current ownership or unspentness.
type rgb11CapacityMaterial struct {
	Receipt *rgb11wallet.ValidationReceipt
	Outputs map[string]*rgb11wallet.BitcoinUTXO
	History *rgb11wallet.HistoricalValidation
}

func (p *rgb11Manager) rgb11TerminalCapacityMaterial(ctx context.Context,
	validator *rgb11wallet.NativeConsensusValidator, raw []byte, witnessTxID string,
	signedWitness []byte) (*rgb11CapacityMaterial, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.evidence == nil ||
		validator == nil || !validRGB11TxID(witnessTxID) {
		return nil, ErrRGB11Inconsistent
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	evidence := p.rgbManager.evidence
	prepared, preparedErr := rgb11wallet.ValidatePreparedWith(ctx, validator, raw, evidence)
	if preparedErr == nil {
		if prepared == nil || prepared.Receipt == nil || len(prepared.WitnessTxIDs) == 0 ||
			prepared.WitnessTxIDs[0] != witnessTxID {
			return nil, rgb11wallet.ErrValidationReceipt
		}
		branch, err := validator.ValidatePreparedHistoryConsignmentReport(ctx, raw, evidence)
		if err != nil || branch == nil {
			return nil, fmt.Errorf("validate prepared RGB11 history for capacity: %w", errRGB11ProjectionEvidence(err))
		}
		// Preserve the original prepared receipt and outputs for matching. Only
		// the sizing history uses the terminal status and complete material.
		material := *prepared.Receipt
		material.Status = "valid"
		material.Allocations = append([]rgb11wallet.ValidatedAllocation(nil), prepared.Receipt.Allocations...)
		for _, historical := range branch.HistoricalAllocations {
			found := false
			for _, existing := range material.Allocations {
				if existing.OperationID == historical.Allocation.OperationID &&
					existing.AssignmentType == historical.Allocation.AssignmentType &&
					existing.AssignmentIndex == historical.Allocation.AssignmentIndex {
					found = true
					break
				}
			}
			if !found {
				material.Allocations = append(material.Allocations, historical.Allocation)
			}
		}
		history := &rgb11wallet.HistoricalValidation{MaterialReceipt: &material,
			Allocations: branch.HistoricalAllocations, Consumers: branch.Consumers}
		return &rgb11CapacityMaterial{Receipt: prepared.Receipt, Outputs: prepared.Outputs, History: history}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A stale journal can still say prepared after its own witness is observed.
	// Only positive evidence plus a successful core history permits this fallback.
	status, err := evidence.GetTxStatus(witnessTxID)
	if err != nil || !validRGB11ObservedTxStatus(status, witnessTxID) ||
		(!status.Confirmed && !status.InMempool) {
		return nil, fmt.Errorf("capacity witness is not positively observed: %w", preparedErr)
	}
	history, err := validator.ValidateHistoricalConsignmentReport(ctx, raw, evidence)
	if err != nil || history == nil || history.MaterialReceipt == nil {
		return nil, fmt.Errorf("validate observed RGB11 history for capacity: %w", errRGB11ProjectionEvidence(err))
	}
	transactions := make(map[string]*wire.MsgTx)
	loadTransaction := func(txid string, supplied []byte) (*wire.MsgTx, error) {
		if tx := transactions[txid]; tx != nil {
			return tx, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		transactionRaw := supplied
		if len(transactionRaw) == 0 {
			var err error
			transactionRaw, err = evidence.GetRawTx(txid)
			if err != nil {
				return nil, err
			}
		}
		var tx wire.MsgTx
		reader := bytes.NewReader(transactionRaw)
		if err := tx.Deserialize(reader); err != nil || reader.Len() != 0 || tx.TxHash().String() != txid {
			return nil, fmt.Errorf("invalid capacity carrier transaction %s: %w", txid, errRGB11ProjectionEvidence(err))
		}
		transactions[txid] = &tx
		return &tx, nil
	}
	// Prefer the persisted signed witness for sends, checking even trailing bytes.
	if _, err := loadTransaction(witnessTxID, signedWitness); err != nil {
		return nil, err
	}
	receipt := *history.MaterialReceipt
	receipt.Allocations = nil
	outputs := make(map[string]*rgb11wallet.BitcoinUTXO)
	for _, allocation := range history.MaterialReceipt.Allocations {
		if allocation.WitnessTxID != witnessTxID {
			continue
		}
		parsed, err := wire.NewOutPointFromString(allocation.OutPoint)
		if err != nil || parsed == nil {
			return nil, rgb11wallet.ErrValidationReceipt
		}
		// A blinded absolute seal can use a carrier created by a different tx.
		// Core authenticates its assignment; authenticate that carrier separately.
		tx, err := loadTransaction(parsed.Hash.String(), nil)
		if err != nil {
			return nil, err
		}
		if uint64(parsed.Index) >= uint64(len(tx.TxOut)) {
			return nil, rgb11wallet.ErrValidationReceipt
		}
		output := tx.TxOut[parsed.Index]
		receipt.Allocations = append(receipt.Allocations, allocation)
		outputs[allocation.OutPoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: allocation.OutPoint, Value: output.Value,
			PkScript: append([]byte(nil), output.PkScript...),
		}
	}
	if len(receipt.Allocations) == 0 {
		return nil, rgb11wallet.ErrValidationReceipt
	}
	return &rgb11CapacityMaterial{Receipt: &receipt, Outputs: outputs, History: history}, nil
}

// addRGB11PreparedReceiveCapacityPreview constructs only the byte shape of a
// future accepted receive. It uses the already stored prepared consignment and
// receive request, but it neither applies chain ownership nor persists a
// current proof. Fresh evidence and policy checks still govern real acceptance.
func (p *rgb11Manager) addRGB11PreparedReceiveCapacityPreview(ctx context.Context,
	snapshot *rgb11wallet.RGB11WalletSnapshot, state *rgb11wallet.TransferState,
	raw []byte) (*rgb11wallet.RGB11WalletSnapshot, error) {
	if p == nil || snapshot == nil || state == nil || len(raw) == 0 ||
		p.rgbManager == nil || p.rgbManager.evidence == nil {
		return nil, ErrRGB11Inconsistent
	}
	request, err := rgb11ReceiveRequestFromSnapshot(snapshot.EngineRecords, state.ReceiveRequestID)
	if err != nil {
		return nil, err
	}
	invoice, err := invoicing.Parse(request.Invoice)
	if err != nil {
		return nil, err
	}
	var validator *rgb11wallet.NativeConsensusValidator
	switch invoice.Beneficiary.Kind {
	case invoicing.BeneficiaryBlindedSeal:
		concealed, sealErr := request.Seal.Conceal()
		if sealErr != nil || !bytes.Equal(invoice.Beneficiary.BlindedSeal[:], concealed[:]) {
			return nil, ErrRGB11InvoiceMismatch
		}
		validator = rgb11wallet.NewNativeConsensusValidatorWithReveals(request.Seal)
	case invoicing.BeneficiaryWitnessVout:
		witnessScript, scriptErr := invoice.Beneficiary.WitnessScript()
		if scriptErr != nil || !bytes.Equal(request.WitnessScript, witnessScript) {
			return nil, ErrRGB11InvoiceMismatch
		}
		validator = rgb11wallet.NewNativeConsensusValidator()
	default:
		return nil, ErrRGB11InvoiceMismatch
	}
	material, err := p.rgb11TerminalCapacityMaterial(ctx, validator, raw, state.WitnessTxID, nil)
	if err != nil {
		return nil, fmt.Errorf("validate receive for capacity: %w", errRGB11ProjectionEvidence(err))
	}
	if material.Receipt.ConsignmentHash != state.ConsignmentHash {
		return nil, rgb11wallet.ErrValidationReceipt
	}
	matchTxID := ""
	var matchVout *uint32
	if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
		if len(state.OutputOutPoints) != 1 {
			return nil, rgb11wallet.ErrValidationReceipt
		}
		outpoint, err := wire.NewOutPointFromString(state.OutputOutPoints[0])
		if err != nil || outpoint == nil || outpoint.Hash.String() != state.WitnessTxID {
			return nil, rgb11wallet.ErrValidationReceipt
		}
		matchTxID = state.WitnessTxID
		matchVout = &outpoint.Index
	}
	matched, err := p.matchRGB11ReceiveAllocation(material.Receipt, request, invoice,
		material.Outputs, matchTxID, matchVout)
	if err != nil {
		return nil, fmt.Errorf("match prepared receive for capacity: %w", errRGB11ProjectionEvidence(err))
	}
	if matched == nil || matched.UTXO == nil || matched.Binding == nil {
		return nil, rgb11wallet.ErrValidationReceipt
	}
	if request.TransferID != state.TransferID || request.ObjectHash != state.ConsignmentHash ||
		request.WitnessTxID != state.WitnessTxID ||
		state.Asset.Name != matched.Allocation.AssetName || state.Asset.Amount.Value == nil ||
		matched.Allocation.Amount.Value == nil ||
		state.Asset.Amount.Precision != matched.Allocation.Amount.Precision ||
		state.Asset.Amount.Value.Cmp(matched.Allocation.Amount.Value) != 0 ||
		(len(state.OutputOutPoints) != 0 && (len(state.OutputOutPoints) != 1 ||
			state.OutputOutPoints[0] != matched.Allocation.OutPoint)) {
		return nil, rgb11wallet.ErrValidationReceipt
	}
	history := material.History
	receipts, err := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	existingReceipt := receipts[state.ConsignmentHash]
	if existingReceipt != nil && !sameRGB11ValidationReceiptIdentity(existingReceipt, history.MaterialReceipt) {
		return nil, rgb11wallet.ErrValidationReceipt
	}
	receipt, err := rgb11wallet.SelectHistoricalValidationReceipt(history, existingReceipt,
		[]rgb11wallet.ValidatedAllocation{matched.Allocation})
	if err != nil {
		return nil, fmt.Errorf("select prepared receive receipt for capacity: %w", err)
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return nil, err
	}
	allocation := matched.Allocation
	commitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, allocation.SealDisclosure)
	proof := &rgb11wallet.AllocationProof{
		OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
		OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType,
		AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
		StateData:       append([]byte(nil), allocation.StateData...),
		SealCommitment:  fmt.Sprintf("%x", commitment[:]),
		SealDisclosure:  append([]byte(nil), allocation.SealDisclosure...),
		ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
		WitnessTxID: allocationOutpointTxID(allocation.OutPoint),
		Status:      "valid", CarrierBinding: matched.Binding,
	}
	output := indexer.NewTxOutput(matched.UTXO.Value)
	output.OutPointStr = allocation.OutPoint
	output.OutValue.PkScript = append([]byte(nil), matched.UTXO.PkScript...)
	asset := &indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}
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
	candidate.ProjectionRecords, err = rgb11wallet.AddValidatedConsignmentProjectionSnapshot(
		candidate.ProjectionRecords, raw, receipt,
		[]rgb11wallet.ProjectionReplacement{{Output: output, Asset: asset, Proof: proof}},
	)
	if err != nil {
		return nil, err
	}
	contractFile, _, err := rgb11ContractFromValidatedHistory(raw, receipt.ContractID)
	if err != nil {
		return nil, err
	}
	candidate.ProjectionRecords, err = rgb11wallet.AddContractObjectSnapshotReference(
		candidate.ProjectionRecords, receipt.ContractID, contractFile,
	)
	if err != nil {
		return nil, err
	}
	completed := *state
	completed.Status = "pending"
	candidate.ProjectionRecords, err = rgb11wallet.UpsertProjectionSnapshotTransfer(
		candidate.ProjectionRecords, &completed,
	)
	if err != nil {
		return nil, err
	}
	request.Status = corewallet.ReceiveAccepted
	request.TransferID = state.TransferID
	request.ObjectHash = state.ConsignmentHash
	candidate.EngineRecords, err = rgb11wallet.UpsertEngineSnapshotReceive(candidate.EngineRecords, request)
	if err != nil {
		return nil, err
	}
	candidate.ProjectionRecords, err = rgb11wallet.RemoveProjectionSnapshotKeys(candidate.ProjectionRecords,
		"prepared-receive-"+state.TransferID, "receive-reservation-"+state.ReceiveRequestID)
	if err != nil {
		return nil, err
	}
	return &candidate, nil
}

func rgb11PendingCapacityPreviewComplete(records []rgb11wallet.SnapshotRecord,
	pending *rgb11wallet.PendingTransfer) bool {
	if pending == nil || pending.State.WitnessTxID == "" || pending.LocalObjectHash == "" ||
		len(pending.ChangeSeals) == 0 {
		return false
	}
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(records)
	if err != nil {
		return false
	}
	covered := make(map[string]bool, len(pending.ChangeSeals))
	for _, seal := range pending.ChangeSeals {
		covered[fmt.Sprintf("%s:%d", pending.State.WitnessTxID, seal.Vout)] = false
	}
	for _, proof := range proofs {
		if proof != nil && proof.WitnessTxID == pending.State.WitnessTxID &&
			proof.ConsignmentHash == pending.LocalObjectHash {
			if _, ok := covered[proof.OutPoint]; ok {
				covered[proof.OutPoint] = true
			}
		}
	}
	for _, present := range covered {
		if !present {
			return false
		}
	}
	return len(covered) != 0
}
