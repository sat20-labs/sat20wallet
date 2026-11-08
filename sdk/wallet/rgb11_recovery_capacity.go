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
	Conservative    bool
	BackupPending   bool
	RPKRawBytes     int
	RPKEncodedBytes int
	AMDBytes        int
	ADEBytes        int
	DKB1Bytes       int
}

// preflightRGB11Exact measures this scope's independent module KV, including
// known terminal obligations. Other modules no longer share its blob limit.
// Sizing has no persistence or readiness side effects.
func (p *Manager) preflightRGB11Exact(scope string,
	snapshot *rgb11wallet.RGB11WalletSnapshot) (rgb11RecoveryBundleSize, error) {
	var size rgb11RecoveryBundleSize
	if p == nil || snapshot == nil || strings.TrimSpace(scope) == "" {
		return size, ErrRGB11Inconsistent
	}
	p.mutex.RLock()
	if p.accountProfile == nil {
		p.mutex.RUnlock()
		return size, nil
	}
	accountID := p.accountProfile.AccountID
	network := p.cfg.Chain
	secret := append([]byte(nil), p.accountSecret...)
	p.mutex.RUnlock()
	defer zeroBytes(secret)
	if len(secret) != 32 {
		return size, fmt.Errorf("account-managed recovery preflight requires an unlocked account")
	}
	catalog, err := p.accountManagedDataCatalog()
	if err != nil || catalog.AccountID != accountID {
		return size, errRGB11ProjectionEvidence(err)
	}
	selected, err := accountScopeCatalog(catalog, scope)
	if err != nil || len(selected.Scopes) != 1 {
		return size, ErrRGB11Inconsistent
	}
	accounts, err := (&rgb11AccountManagedDataProvider{owner: p}).accountsByScope()
	if err != nil {
		return size, err
	}
	accountValue, ok := accounts[scope]
	if !ok {
		return size, ErrRGB11Inconsistent
	}
	scoped, err := p.newScopedRGB11Manager(accountValue)
	if err != nil {
		return size, err
	}
	walletID, err := scoped.RGB11WalletID()
	if err != nil {
		return size, err
	}
	if snapshot.AccountIndex != selected.Scopes[0].AccountIndex || snapshot.WalletID != walletID {
		return size, ErrRGB11Inconsistent
	}
	preview, err := scoped.addRGB11PendingTerminalCapacityPreviews(snapshot)
	if err != nil {
		return size, err
	}
	recovery, err := rgb11wallet.RecoveryPackageFromSnapshot(preview, 0)
	if err != nil {
		return size, err
	}
	encoded, raw, err := rgb11wallet.EncodeRecoveryPackageWithSize(recovery)
	if err != nil {
		return size, err
	}
	size.RPKRawBytes, size.RPKEncodedBytes = raw, len(encoded)
	return measureRGB11ModuleCapacity(size, secret, accountID, network, scope, encoded)
}

// The diagnostic fields AMDBytes/ADEBytes now describe the module plaintext
// and AMOE envelope. Use the actual account codec rather than duplicated sizes.
func measureRGB11ModuleCapacity(size rgb11RecoveryBundleSize, secret []byte,
	accountID, network, scope string, payload []byte) (rgb11RecoveryBundleSize, error) {
	envelope, err := account.SealManagedModule(secret, accountID, network, account.ManagedModule{
		Provider: rgb11AccountManagedProviderID, Scope: scope, Codec: "v1", Payload: payload,
	}, nil)
	if err != nil {
		return size, fmt.Errorf("seal prospective RGB11 module: %w", err)
	}
	size.ADEBytes = len(envelope)
	// AMOE v1 magic (5), GCM nonce (12), and authentication tag (16).
	size.AMDBytes = size.ADEBytes - 33
	size.DKB1Bytes = size.ADEBytes + 8
	blob, err := EncodeDKVSBlobValue(envelope, nil)
	if err != nil {
		return size, fmt.Errorf("encode prospective RGB11 module blob (limit=%d): %w", swire.MaxDKVSBlobValueSize, err)
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
			candidate.ProjectionRecords, err = rgb11wallet.RebindLocalReceipt(
				candidate.ProjectionRecords, existingReceipt, receipt,
			)
			if err != nil {
				return nil, err
			}
		}
	}
	candidate.ProjectionRecords, err = rgb11wallet.AddLocalConsignment(
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
	candidate.ProjectionRecords, err = rgb11wallet.AddLocalContract(
		candidate.ProjectionRecords, receipt.ContractID, contractFile,
	)
	if err != nil {
		return nil, err
	}
	completed := *state
	completed.Status = "pending"
	candidate.ProjectionRecords, err = rgb11wallet.UpsertLocalTransfer(
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
	candidate.ProjectionRecords, err = rgb11wallet.RemoveLocalKeys(candidate.ProjectionRecords,
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
	outpoints := make(map[string]string, len(pending.ChangeSeals))
	for _, seal := range pending.ChangeSeals {
		strict, err := seal.StrictBytes()
		if err != nil {
			return false
		}
		key := string(strict)
		if _, duplicate := covered[key]; duplicate {
			return false
		}
		covered[key] = false
		outpoints[key] = fmt.Sprintf("%s:%d", pending.State.WitnessTxID, seal.Vout)
	}
	for _, proof := range proofs {
		if proof != nil && proof.WitnessTxID == pending.State.WitnessTxID &&
			proof.ConsignmentHash == pending.LocalObjectHash {
			key := string(proof.SealDisclosure)
			if _, ok := covered[key]; ok && proof.OutPoint == outpoints[key] {
				covered[key] = true
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

func (p *Manager) preflightRGB11TerminalRecovery(scope string, snapshot *rgb11wallet.RGB11WalletSnapshot) (rgb11RecoveryBundleSize, error) {
	size, err := p.preflightRGB11Exact(scope, snapshot)
	if err == nil {
		return size, nil
	}
	bounded, found, boundErr := p.preflightRGB11Bounded(scope, snapshot)
	if !found {
		return size, err
	}
	if boundErr != nil {
		return bounded, fmt.Errorf("RGB11 complete local capacity unavailable: %w", boundErr)
	}
	return bounded, nil
}

// This path is entered only for positively located local faults. It creates no
// recovery payload and never clears an account's pending backup generation.
func (p *Manager) preflightRGB11Bounded(scope string, snapshot *RGB11WalletSnapshot) (rgb11RecoveryBundleSize, bool, error) {
	var size rgb11RecoveryBundleSize
	if p == nil || snapshot == nil || strings.TrimSpace(scope) == "" {
		return size, false, ErrRGB11Inconsistent
	}
	p.mutex.RLock()
	if p.accountProfile == nil {
		p.mutex.RUnlock()
		return size, false, nil
	}
	accountID, network := p.accountProfile.AccountID, p.cfg.Chain
	secret := append([]byte(nil), p.accountSecret...)
	p.mutex.RUnlock()
	defer zeroBytes(secret)
	if len(secret) != 32 {
		return size, false, ErrRGB11Inconsistent
	}
	catalog, err := p.accountManagedDataCatalog()
	if err != nil || catalog.AccountID != accountID {
		return size, false, errRGB11ProjectionEvidence(err)
	}
	selected, err := accountScopeCatalog(catalog, scope)
	if err != nil || len(selected.Scopes) != 1 {
		return size, false, ErrRGB11Inconsistent
	}
	accounts, err := (&rgb11AccountManagedDataProvider{owner: p}).accountsByScope()
	if err != nil {
		return size, false, err
	}
	accountValue, ok := accounts[scope]
	if !ok {
		return size, false, ErrRGB11Inconsistent
	}
	rgb, err := p.newScopedRGB11Manager(accountValue)
	if err != nil {
		return size, false, err
	}
	walletID, err := rgb.RGB11WalletID()
	if err != nil {
		return size, false, err
	}
	if snapshot.WalletID != walletID || snapshot.AccountIndex != selected.Scopes[0].AccountIndex {
		return size, false, ErrRGB11Inconsistent
	}
	view, err := rgb11wallet.SplitLocalProjection(snapshot.ProjectionRecords, rgb.rgb11ScopeState().ProofFaults...)
	if err != nil {
		return size, false, err
	}
	if len(view.Faults) == 0 {
		return size, false, ErrRGB11Inconsistent
	}
	// Keep lifecycle rows only for sizing. Damaged proofs and outputs cannot
	// assert ownership; their complete original bytes remain in the bound.
	work := *snapshot
	work.ProjectionRecords = append([]rgb11wallet.SnapshotRecord(nil), view.Records...)
	for _, record := range view.Frozen {
		if !strings.HasPrefix(record.Key, "proof-") && !strings.HasPrefix(record.Key, "output-") {
			work.ProjectionRecords = append(work.ProjectionRecords, record)
		}
	}
	if err := rgb11wallet.ValidateWalletSnapshot(&work); err != nil {
		return size, true, err
	}
	preview, err := rgb.addRGB11PendingTerminalCapacityPreviews(&work)
	if err != nil {
		return size, true, err
	}
	raw, bound, err := rgb11wallet.LocalRecoveryBound(snapshot, preview)
	if err != nil {
		return size, true, err
	}
	if bound <= 0 || bound > account.ManagedObjectMaxBytes {
		return size, true, fmt.Errorf("RGB11 module capacity bound %d exceeds limit %d", bound, account.ManagedObjectMaxBytes)
	}
	size.Conservative, size.BackupPending = true, true
	size.RPKRawBytes, size.RPKEncodedBytes = raw, bound
	// AMOD/AMOE do not compress payloads, so a placeholder of the conservative
	// bound has exactly the same framing size without exporting damaged data.
	size, err = measureRGB11ModuleCapacity(size, secret, accountID, network, scope, make([]byte, bound))
	return size, true, err
}
