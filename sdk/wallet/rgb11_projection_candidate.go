package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/seals"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type rgb11ResolvedRecoveryProjection struct {
	snapshot      *RGB11WalletSnapshot
	lockReasons   map[string]string
	tickerInfos   []*indexer.TickerInfo
	complete      bool
	checkEvidence func() error
}

type rgb11RecoveryProofCandidate struct {
	proof           *rgb11wallet.AllocationProof
	producerTxID    string
	producerStatus  *rgb11wallet.BitcoinTxStatus
	currentStatus   string
	confirmations   int64
	prepared        bool
	outspend        *rgb11wallet.BitcoinOutspend
	activeOperation bool
}

// rgb11CandidateEvidence remembers the Bitcoin facts used to build one RGB11
// projection. A stable block tip does not freeze mempool visibility, UTXOs, or
// outspends, so the candidate rechecks these exact dependencies before publish.
type rgb11CandidateEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	utxos     map[string]*rgb11wallet.BitcoinUTXO
	statuses  map[string]*rgb11wallet.BitcoinTxStatus
	outspends map[string]*rgb11wallet.BitcoinOutspend
	rawTxs    map[string][]byte
}

func newRGB11CandidateEvidence(provider rgb11wallet.BitcoinEvidenceProvider) *rgb11CandidateEvidence {
	return &rgb11CandidateEvidence{
		BitcoinEvidenceProvider: provider,
		utxos:                   make(map[string]*rgb11wallet.BitcoinUTXO),
		statuses:                make(map[string]*rgb11wallet.BitcoinTxStatus),
		outspends:               make(map[string]*rgb11wallet.BitcoinOutspend),
		rawTxs:                  make(map[string][]byte),
	}
}

func (e *rgb11CandidateEvidence) GetUTXO(outpoint string) (*rgb11wallet.BitcoinUTXO, error) {
	if value, ok := e.utxos[outpoint]; ok {
		return cloneRGB11UTXO(value), nil
	}
	value, err := e.BitcoinEvidenceProvider.GetUTXO(outpoint)
	if err != nil {
		return value, err
	}
	e.utxos[outpoint] = cloneRGB11UTXO(value)
	return value, nil
}

func (e *rgb11CandidateEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if value, ok := e.statuses[txid]; ok {
		if value == nil {
			return nil, nil
		}
		copy := *value
		return &copy, nil
	}
	value, err := e.BitcoinEvidenceProvider.GetTxStatus(txid)
	if err != nil {
		return value, err
	}
	var copy *rgb11wallet.BitcoinTxStatus
	if value != nil {
		cloned := *value
		copy = &cloned
	}
	e.statuses[txid] = copy
	return value, nil
}

func (e *rgb11CandidateEvidence) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	if value, ok := e.outspends[outpoint]; ok {
		if value == nil {
			return nil, nil
		}
		copy := *value
		return &copy, nil
	}
	value, err := e.BitcoinEvidenceProvider.GetOutspend(outpoint)
	if err != nil {
		return value, err
	}
	var copy *rgb11wallet.BitcoinOutspend
	if value != nil {
		cloned := *value
		copy = &cloned
	}
	e.outspends[outpoint] = copy
	return value, nil
}

// Reuse transaction bytes only within this candidate. Core authenticates their
// txid-bound data; mutable status and unspentness are rechecked before publish.
func (e *rgb11CandidateEvidence) GetRawTx(txid string) ([]byte, error) {
	if raw, ok := e.rawTxs[txid]; ok {
		return append([]byte(nil), raw...), nil
	}
	raw, err := e.BitcoinEvidenceProvider.GetRawTx(txid)
	if err == nil {
		e.rawTxs[txid] = append([]byte(nil), raw...)
	}
	return raw, err
}

func (e *rgb11CandidateEvidence) verifyStable(ctx context.Context) error {
	for _, outpoint := range sortedRGB11EvidenceKeys(e.outspends) {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, err := e.BitcoinEvidenceProvider.GetOutspend(outpoint)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(e.outspends[outpoint], value) {
			return fmt.Errorf("Bitcoin outspend %s changed while resolving RGB11 candidate", outpoint)
		}
	}
	for _, outpoint := range sortedRGB11EvidenceKeys(e.utxos) {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, err := e.BitcoinEvidenceProvider.GetUTXO(outpoint)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(e.utxos[outpoint], value) {
			return fmt.Errorf("Bitcoin UTXO %s changed while resolving RGB11 candidate", outpoint)
		}
	}
	for _, txid := range sortedRGB11EvidenceKeys(e.statuses) {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, err := e.BitcoinEvidenceProvider.GetTxStatus(txid)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(e.statuses[txid], value) {
			return fmt.Errorf("Bitcoin transaction status %s changed while resolving RGB11 candidate", txid)
		}
	}
	return nil
}

func sortedRGB11EvidenceKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cloneRGB11UTXO(value *rgb11wallet.BitcoinUTXO) *rgb11wallet.BitcoinUTXO {
	if value == nil {
		return nil
	}
	copy := *value
	copy.PkScript = append([]byte(nil), value.PkScript...)
	return &copy
}

func (p *rgb11Manager) rgb11SnapshotLockPlan(current, candidate *RGB11WalletSnapshot,
	reasons map[string]string) (rgb11LockPlan, error) {
	plan := rgb11LockPlan{
		ReplacedOwners: make(map[string]struct{}),
		DesiredOwners:  make(map[string]rgb11ReservationLockTarget),
		Consumed:       make(map[string]struct{}),
		Reasons:        make(map[string]string, len(reasons)),
	}
	for outpoint, reason := range reasons {
		plan.Reasons[outpoint] = reason
	}
	if current == nil || candidate == nil {
		return plan, ErrRGB11Inconsistent
	}
	oldPending, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(current.ProjectionRecords)
	if err != nil {
		return plan, err
	}
	oldReceives, err := rgb11wallet.ReceiveReservationsFromProjectionSnapshot(current.ProjectionRecords)
	if err != nil {
		return plan, err
	}
	for _, pending := range oldPending {
		if pending.ReservationID != "" {
			plan.ReplacedOwners[pending.ReservationID] = struct{}{}
		}
	}
	for _, reservation := range oldReceives {
		if reservation.ReservationID != "" {
			plan.ReplacedOwners[reservation.ReservationID] = struct{}{}
		}
	}
	// The existing common RGB11 reservation keeps the owner needed to retire a
	// stale lock even if a compacted projection no longer has its private pending
	// row. It is already part of this wallet's pre-import lifecycle state.
	oldReservations, err := p.loadRGB11Reservations()
	if err != nil {
		return plan, err
	}
	for _, reservation := range oldReservations {
		if reservation != nil && reservation.LockOwner != "" {
			plan.ReplacedOwners[reservation.LockOwner] = struct{}{}
		}
	}

	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(candidate.ProjectionRecords)
	if err != nil {
		return plan, err
	}
	proofOutpoints := make(map[string]struct{}, len(proofs))
	proofsByOutpoint := make(map[string][]*rgb11wallet.AllocationProof, len(proofs))
	for _, proof := range proofs {
		proofOutpoints[proof.OutPoint] = struct{}{}
		proofsByOutpoint[proof.OutPoint] = append(proofsByOutpoint[proof.OutPoint], proof)
	}
	markConsumedRGBInput := func(outpoint string) error {
		carrierProofs := proofsByOutpoint[outpoint]
		for _, proof := range carrierProofs {
			if proof.Status != "spending" {
				return fmt.Errorf("%w: settled RGB11 journal contradicts resolved input %s", ErrRGB11Inconsistent, outpoint)
			}
		}
		if len(carrierProofs) != 0 {
			plan.Consumed[outpoint] = struct{}{}
		}
		return nil
	}
	transfers, err := rgb11wallet.TransfersFromProjectionSnapshot(candidate.ProjectionRecords)
	if err != nil {
		return plan, err
	}
	pendings, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(candidate.ProjectionRecords)
	if err != nil {
		return plan, err
	}
	pendingByID := make(map[string]*rgb11wallet.PendingTransfer, len(pendings))
	for _, pending := range pendings {
		pendingByID[pending.State.TransferID] = pending
	}
	groups := make(map[string]*rgb11ActiveReservation)
	inputOwners := make(map[string]string)
	inputWitnesses := make(map[string]string)
	for _, state := range transfers {
		if !rgb11TransferHasExpectedSpend(state) {
			continue
		}
		pending := pendingByID[state.TransferID]
		if pending == nil {
			return plan, fmt.Errorf("active RGB11 transfer %s lacks its reservation closure", state.TransferID)
		}
		if err := validateRGB11PendingTransaction(pending); err != nil {
			return plan, err
		}
		if pending.ReservationID == "" {
			// Maintenance may retire a settled owner's private lock reference.
			// Its journal remains history, never a new active owner. The journal
			// may still say settled after reorg, so only freshly resolved spending
			// proofs can authorize retiring a carrier's lock.
			if state.Status == "settled" {
				for _, outpoint := range state.InputOutPoints {
					if err := markConsumedRGBInput(outpoint); err != nil {
						return plan, err
					}
				}
				continue
			}
			return plan, fmt.Errorf("active RGB11 transfer %s lacks its reservation owner", state.TransferID)
		}
		group := groups[pending.ReservationID]
		if group == nil {
			group = &rgb11ActiveReservation{
				outpoints: make(map[string]struct{}), previousReasons: make(map[string]string),
				changeOutpoints: make(map[string]struct{}), allSettled: true,
			}
			groups[pending.ReservationID] = group
		}
		if state.Status != "settled" {
			group.allSettled = false
		}
		for _, outpoint := range state.InputOutPoints {
			if owner := inputOwners[outpoint]; owner != "" &&
				(owner != pending.ReservationID || inputWitnesses[outpoint] != state.WitnessTxID) {
				return plan, fmt.Errorf("%w: RGB11 input has multiple reservation owners: %s", ErrRGB11Inconsistent, outpoint)
			}
			inputOwners[outpoint] = pending.ReservationID
			inputWitnesses[outpoint] = state.WitnessTxID
			group.outpoints[outpoint] = struct{}{}
			if _, hasProof := proofOutpoints[outpoint]; hasProof {
				group.previousReasons[outpoint] = rgb11wallet.LockReasonRGB
			}
		}
		for _, outpoint := range rgb11PendingChangeOutpoints(pending) {
			listed := false
			for _, output := range state.OutputOutPoints {
				if output == outpoint {
					listed = true
					break
				}
			}
			if !listed {
				return plan, fmt.Errorf("%w: RGB11 change is not a transaction output: %s", ErrRGB11Inconsistent, outpoint)
			}
			group.changeOutpoints[outpoint] = struct{}{}
			if state.Status != "settled" {
				group.outpoints[outpoint] = struct{}{}
			}
		}
	}
	setDesired := func(outpoint, reservationID, previous string) error {
		if old, exists := plan.DesiredOwners[outpoint]; exists && old.ReservationID != reservationID {
			return fmt.Errorf("%w: RGB11 carrier has multiple reservation owners: %s", ErrRGB11Inconsistent, outpoint)
		}
		plan.DesiredOwners[outpoint] = rgb11ReservationLockTarget{
			ReservationID: reservationID, PreviousReason: previous,
		}
		return nil
	}
	for reservationID, group := range groups {
		if group.allSettled {
			for outpoint := range group.outpoints {
				if err := markConsumedRGBInput(outpoint); err != nil {
					return plan, err
				}
			}
			for outpoint := range group.changeOutpoints {
				if plan.Reasons[outpoint] == "" {
					plan.Reasons[outpoint] = rgb11wallet.LockReasonRGB
				}
			}
			continue
		}
		for outpoint := range group.outpoints {
			if err := setDesired(outpoint, reservationID, group.previousReasons[outpoint]); err != nil {
				return plan, err
			}
		}
	}

	receiveReservations, err := rgb11wallet.ReceiveReservationsFromProjectionSnapshot(candidate.ProjectionRecords)
	if err != nil {
		return plan, err
	}
	requests := make(map[string]*corewallet.ReceiveRequest)
	for _, record := range candidate.EngineRecords {
		if !strings.HasPrefix(record.Key, "wallet/receive/") {
			continue
		}
		request, decodeErr := corewallet.DecodeReceiveRequest(record.Value)
		if decodeErr != nil || request == nil || record.Key != "wallet/receive/"+request.RequestID {
			return plan, corewallet.ErrInvalidReceive
		}
		requests[request.RequestID] = request
	}
	now := time.Now().Unix()
	for _, reservation := range receiveReservations {
		if reservation == nil {
			continue
		}
		if reservation.Expiry != 0 && reservation.Expiry <= now {
			request := requests[reservation.RequestID]
			unusedExpired := request != nil && request.Status == corewallet.ReceivePrepared &&
				request.TransferID == "" && request.WitnessTxID == ""
			if unusedExpired {
				continue
			}
		}
		if err := setDesired(reservation.OutPoint, reservation.ReservationID, ""); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

func rgb11ProofMatchesValidatedAllocation(proof *rgb11wallet.AllocationProof,
	receipt *rgb11wallet.ValidationReceipt, output *indexer.TxOutput) bool {
	return rgb11ValidatedAllocationForProof(proof, receipt, output) != nil
}

func rgb11ProofMatchesHistoricalReceipt(proof *rgb11wallet.AllocationProof,
	base *rgb11wallet.ValidationReceipt, output *indexer.TxOutput,
	history *rgb11wallet.HistoricalValidation) bool {
	if base == nil || history == nil {
		return false
	}
	matches := 0
	for _, allocation := range history.MaterialReceipt.Allocations {
		receipt := *base
		receipt.Allocations = []rgb11wallet.ValidatedAllocation{allocation}
		if rgb11ProofMatchesValidatedAllocation(proof, &receipt, output) {
			matches++
		}
	}
	return matches == 1
}

func sameRGB11CarrierBinding(left, right *rgb11wallet.CarrierBinding) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.DerivationIndex == right.DerivationIndex &&
		left.LogicalAddress == right.LogicalAddress && left.OutPoint == right.OutPoint &&
		left.CommitmentMethod == right.CommitmentMethod &&
		bytes.Equal(left.ActualPkScript, right.ActualPkScript) &&
		bytes.Equal(left.ActualOutputKey, right.ActualOutputKey) &&
		bytes.Equal(left.InternalPubKey, right.InternalPubKey) &&
		bytes.Equal(left.TapretRoot, right.TapretRoot) &&
		bytes.Equal(left.TapretProof, right.TapretProof)
}

// rgb11ValidatedAllocationForProof returns the exact core-validated full
// Opout for a cached proof. Callers can use runtime-only core facts such as
// WitnessTxID without persisting a duplicate field in the receipt codec.
func rgb11ValidatedAllocationForProof(proof *rgb11wallet.AllocationProof,
	receipt *rgb11wallet.ValidationReceipt, output *indexer.TxOutput) *rgb11wallet.ValidatedAllocation {
	if proof == nil || receipt == nil || output == nil || output.OutPointStr != proof.OutPoint ||
		!rgb11wallet.ContractAssetKeyMatches(proof.AssetName, receipt.ContractID) || proof.CarrierBinding == nil ||
		proof.CarrierBinding.OutPoint != proof.OutPoint {
		return nil
	}
	asset := output.GetAsset(&proof.AssetName)
	if asset == nil {
		return nil
	}
	commitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, proof.SealDisclosure)
	if proof.SealCommitment != hex.EncodeToString(commitment[:]) {
		return nil
	}
	var matched *rgb11wallet.ValidatedAllocation
	for index := range receipt.Allocations {
		allocation := &receipt.Allocations[index]
		if allocation.OutPoint != proof.OutPoint || allocation.AssetName != proof.AssetName ||
			allocation.OperationID != proof.OperationID || allocation.AssignmentType != proof.AssignmentType ||
			allocation.AssignmentIndex != proof.AssignmentIndex || allocation.StateClass != proof.StateClass ||
			!bytes.Equal(allocation.StateData, proof.StateData) ||
			!bytes.Equal(allocation.SealDisclosure, proof.SealDisclosure) ||
			asset.Cmp(&allocation.Amount) != 0 || matched != nil {
			continue
		}
		method := allocation.CommitmentMethod
		if method == "" {
			method = "genesis"
		}
		if proof.CarrierBinding.CommitmentMethod != method ||
			!bytes.Equal(proof.CarrierBinding.TapretRoot, allocation.TapretRoot) ||
			!bytes.Equal(proof.CarrierBinding.TapretProof, allocation.TapretProof) ||
			(method == "tapret1st" &&
				!bytes.Equal(proof.CarrierBinding.InternalPubKey, allocation.CarrierInternalKey)) {
			continue
		}
		matched = allocation
	}
	return matched
}

func validRGB11ObservedTxStatus(status *rgb11wallet.BitcoinTxStatus, txid string) bool {
	return status != nil && status.TxID == txid && status.Confirmations >= 0 &&
		!(status.Confirmed && (status.Confirmations == 0 || status.InMempool)) &&
		(status.Confirmed || status.Confirmations == 0)
}

// verifyRGB11OrphanedJournalInputs applies the refresh settled-to-orphaned
// rule to all actual Bitcoin inputs, including fee inputs. The caller must first
// bind this journal to an owned, core-validated prepared successor. Reads use the
// candidate evidence tracker so owner retirement has the same precommit barrier.
func verifyRGB11OrphanedJournalInputs(ctx context.Context, evidence *rgb11CandidateEvidence,
	pending *rgb11wallet.PendingTransfer) error {
	if pending == nil || pending.State.Direction != "send" ||
		(pending.State.Status != "settled" && pending.State.Status != "orphaned") {
		return ErrRGB11Inconsistent
	}
	state := &pending.State
	witness := wire.NewMsgTx(wire.TxVersion)
	reader := bytes.NewReader(pending.SignedTx)
	if err := witness.Deserialize(reader); err != nil || reader.Len() != 0 ||
		witness.TxHash().String() != state.WitnessTxID || len(witness.TxIn) == 0 {
		return fmt.Errorf("%w: orphaned RGB11 journal has no matching signed witness", ErrRGB11Inconsistent)
	}
	listed := make(map[string]bool, len(state.InputOutPoints))
	for _, outpoint := range state.InputOutPoints {
		if listed[outpoint] {
			return fmt.Errorf("%w: duplicate RGB11 journal input", ErrRGB11Inconsistent)
		}
		listed[outpoint] = true
	}
	if len(listed) != len(witness.TxIn) {
		return fmt.Errorf("%w: RGB11 journal does not cover all Bitcoin inputs", ErrRGB11Inconsistent)
	}
	for _, outpoint := range state.OutputOutPoints {
		parsed, err := wire.NewOutPointFromString(outpoint)
		if err != nil || parsed.Hash.String() != state.WitnessTxID || int(parsed.Index) >= len(witness.TxOut) {
			return fmt.Errorf("%w: invalid orphaned RGB11 journal output", ErrRGB11Inconsistent)
		}
	}
	status, err := evidence.GetTxStatus(state.WitnessTxID)
	if err != nil || !validRGB11ObservedTxStatus(status, state.WitnessTxID) || status.Confirmed || status.InMempool {
		return fmt.Errorf("%w: orphaned RGB11 journal witness is not positively absent", ErrRGB11Inconsistent)
	}
	for _, input := range witness.TxIn {
		if err := ctx.Err(); err != nil {
			return err
		}
		outpoint := input.PreviousOutPoint.String()
		if !listed[outpoint] {
			return fmt.Errorf("%w: missing or repeated RGB11 journal input %s", ErrRGB11Inconsistent, outpoint)
		}
		delete(listed, outpoint)
		producerID := input.PreviousOutPoint.Hash.String()
		producerStatus, err := evidence.GetTxStatus(producerID)
		if err != nil || !validRGB11ObservedTxStatus(producerStatus, producerID) || !producerStatus.Confirmed {
			return fmt.Errorf("%w: orphaned RGB11 input %s producer is not confirmed", ErrRGB11Inconsistent, outpoint)
		}
		raw, err := evidence.GetRawTx(producerID)
		producer := wire.NewMsgTx(wire.TxVersion)
		reader := bytes.NewReader(raw)
		if err != nil || producer.Deserialize(reader) != nil || reader.Len() != 0 ||
			producer.TxHash().String() != producerID || int(input.PreviousOutPoint.Index) >= len(producer.TxOut) {
			return fmt.Errorf("%w: orphaned RGB11 input %s has no authentic producer", ErrRGB11Inconsistent, outpoint)
		}
		spend, err := evidence.GetOutspend(outpoint)
		if err != nil || spend == nil {
			return fmt.Errorf("%w: orphaned RGB11 input %s has unknown spend state", ErrRGB11Inconsistent, outpoint)
		}
		if spend.Spent {
			if spend.SpendingTx == "" || spend.SpendingTx == state.WitnessTxID {
				return fmt.Errorf("%w: orphaned RGB11 input %s still claims its absent witness", ErrRGB11Inconsistent, outpoint)
			}
			spenderStatus, verified := verifyRGB11ExpectedSpend(evidence, outpoint, spend.SpendingTx)
			if !verified || !validRGB11ObservedTxStatus(spenderStatus, spend.SpendingTx) {
				return fmt.Errorf("%w: orphaned RGB11 input %s replacement is not confirmed", ErrRGB11Inconsistent, outpoint)
			}
			continue
		}
		utxo, err := evidence.GetUTXO(outpoint)
		output := producer.TxOut[input.PreviousOutPoint.Index]
		if spend.SpendingTx != "" || err != nil || utxo == nil || utxo.OutPoint != outpoint ||
			utxo.Confirmations < 1 || utxo.Value != output.Value || !bytes.Equal(utxo.PkScript, output.PkScript) {
			return fmt.Errorf("%w: orphaned RGB11 input %s is not a confirmed unspent output", ErrRGB11Inconsistent, outpoint)
		}
	}
	return nil
}

func rgb11SnapshotOutputMatchesUTXO(output *indexer.TxOutput, utxo *rgb11wallet.BitcoinUTXO) bool {
	return output != nil && utxo != nil && output.OutPointStr == utxo.OutPoint &&
		output.OutValue.Value == utxo.Value && bytes.Equal(output.OutValue.PkScript, utxo.PkScript)
}

func rgb11RecoveryProofOutpoint(proof *rgb11wallet.AllocationProof) (string, error) {
	if proof == nil || proof.OutPoint == "" {
		return "", rgb11wallet.ErrInvalidProof
	}
	parsed, err := wire.NewOutPointFromString(proof.OutPoint)
	if err != nil || parsed == nil {
		return "", fmt.Errorf("parse RGB11 allocation outpoint %q: %w", proof.OutPoint, errRGB11ProjectionEvidence(err))
	}
	txid := parsed.Hash.String()
	if proof.WitnessTxID != "" && proof.WitnessTxID != txid {
		return "", fmt.Errorf("RGB11 allocation %s has a mismatched producer witness", proof.OutPoint)
	}
	return txid, nil
}

func rgb11ProofIndexKey(contractID, operationID string, assignmentType, assignmentIndex uint32) string {
	return fmt.Sprintf("%s|%s|%d|%d", contractID, operationID, assignmentType, assignmentIndex)
}

func rgb11ProofOutpointKey(contractID, operationID string, assignmentType, assignmentIndex uint32) string {
	return rgb11ProofIndexKey(contractID, operationID, assignmentType, assignmentIndex)
}

func rgb11ConsumerInputKey(consumer rgb11SnapshotConsumerInput) (string, error) {
	assetKey, err := rgb11wallet.NewContractAssetKey(consumer.ContractID, indexer.ASSET_TYPE_FT)
	if err != nil {
		return "", err
	}
	return rgb11ProofIndexKey(assetKey.Ticker, consumer.InputOperationID,
		consumer.AssignmentType, consumer.AssignmentIndex), nil
}

func (p *rgb11Manager) resolveRGB11RecoveryProjection(ctx context.Context,
	snapshot *RGB11WalletSnapshot, expandedReceipts ...string) (*rgb11ResolvedRecoveryProjection, error) {
	if p == nil || p.rgbManager == nil ||
		p.rgbManager.projectionStore == nil || snapshot == nil {
		return nil, ErrRGB11Inconsistent
	}
	if err := rgb11wallet.ValidateWalletSnapshot(snapshot); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A genuinely empty RGB scope has no Bitcoin facts to resolve. Check the
	// live scope too: an empty candidate must not bypass validation of local
	// obligations that a replacing snapshot import could otherwise remove.
	if !rgb11SnapshotHasState(snapshot) {
		walletID, err := p.RGB11WalletID()
		if err != nil {
			return nil, err
		}
		current, _, err := p.exportRGB11WalletSnapshot(walletID)
		if err != nil {
			return nil, err
		}
		reservations, err := p.loadRGB11Reservations()
		if err != nil {
			return nil, err
		}
		if !rgb11SnapshotHasState(current) && len(reservations) == 0 &&
			snapshot.WalletID == current.WalletID && snapshot.AccountIndex == current.AccountIndex &&
			snapshot.EngineBuildID == current.EngineBuildID {
			resolved := *snapshot
			return &rgb11ResolvedRecoveryProjection{snapshot: &resolved,
				lockReasons: make(map[string]string), complete: true, checkEvidence: ctx.Err}, nil
		}
	}
	if p.rgbManager.evidence == nil {
		return nil, ErrRGB11Inconsistent
	}
	projectionRecords := snapshot.ProjectionRecords
	evidence := newRGB11CandidateEvidence(p.rgbManager.evidence)
	before, err := evidence.GetTip()
	if err != nil || before == nil || before.Height < 0 || before.BlockHash == "" {
		return nil, fmt.Errorf("read RGB11 projection starting tip: %w", errRGB11ProjectionEvidence(err))
	}
	expectedTip := *before

	// Derive confirmed conflicts for both local refresh and cold imports.
	// The same tracker covers every actual Bitcoin input, including fee inputs.
	journals, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(projectionRecords)
	if err != nil {
		return nil, err
	}
	for _, pending := range journals {
		wasConflicted := pending.State.Status == "conflicted"
		if pending.State.Direction != "send" || (!wasConflicted && !rgb11TransferHasExpectedSpend(&pending.State)) {
			continue
		}
		checked := *pending
		checked.State.Status = "pending"
		if err := validateRGB11PendingTransaction(&checked); err != nil {
			return nil, err
		}
		status, err := evidence.GetTxStatus(pending.State.WitnessTxID)
		if err != nil || !validRGB11ObservedTxStatus(status, pending.State.WitnessTxID) {
			// Preserve normal prepared/unknown obligations on lookup failure.
			// A previously terminal journal needs positive evidence to change.
			if !wasConflicted {
				continue
			}
			return nil, fmt.Errorf("read RGB11 journal witness: %w", errRGB11ProjectionEvidence(err))
		}
		if status.Confirmed || status.InMempool {
			if !wasConflicted {
				continue
			}
			raw, err := evidence.GetRawTx(pending.State.WitnessTxID)
			var witness wire.MsgTx
			reader := bytes.NewReader(raw)
			if err != nil || witness.Deserialize(reader) != nil || reader.Len() != 0 || witness.TxHash().String() != pending.State.WitnessTxID {
				return nil, fmt.Errorf("conflicted RGB11 witness raw identity is invalid")
			}
			pending.RecipientConsignment = rgb11SnapshotObject(projectionRecords, pending.RecipientObjectHash)
			pending.LocalConsignment = rgb11SnapshotObject(projectionRecords, pending.LocalObjectHash)
			projectionRecords, err = p.addRGB11LocalChangeCandidate(ctx, projectionRecords, pending)
			if err != nil {
				return nil, err
			}
			pending.State.Status = "pending"
			if status.Confirmed && status.Confirmations >= max(int64(pending.State.MinConfirmations), 1) {
				pending.State.Status = "settled"
			}
		} else {
			confirmedRival := false
			for _, outpoint := range pending.State.InputOutPoints {
				spend, err := evidence.GetOutspend(outpoint)
				if err != nil || spend == nil {
					return nil, errRGB11ProjectionEvidence(err)
				}
				if !spend.Spent || spend.SpendingTx == "" || spend.SpendingTx == "unknown" || spend.SpendingTx == pending.State.WitnessTxID {
					continue
				}
				rival, err := evidence.GetTxStatus(spend.SpendingTx)
				if err != nil || !validRGB11ObservedTxStatus(rival, spend.SpendingTx) {
					return nil, errRGB11ProjectionEvidence(err)
				}
				confirmedRival = confirmedRival || rival.Confirmed
			}
			if !wasConflicted && !confirmedRival {
				continue
			}
			checked.State.Status = "orphaned"
			if err := verifyRGB11OrphanedJournalInputs(ctx, evidence, &checked); err != nil {
				return nil, err
			}
			if confirmedRival {
				if pending.State.Status == "settled" {
					pending.State.Status = "orphaned"
				} else {
					pending.State.Status, pending.State.AckStatus = "conflicted", "invalidated"
				}
			} else {
				pending.State.Status = "pending"
			}
		}
		// A reorg never fabricates a fresh positive ACK.
		projectionRecords, err = rgb11wallet.UpsertProjectionSnapshotPending(projectionRecords, pending)
		if err != nil {
			return nil, err
		}
	}

	view := *snapshot
	view.ProjectionRecords = projectionRecords
	snapshot = &view

	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	outputs, err := rgb11wallet.OutputsFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	receipts, err := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	objects := make(map[string][]byte)
	for _, record := range snapshot.ProjectionRecords {
		if strings.HasPrefix(record.Key, "object-") {
			objects[strings.TrimPrefix(record.Key, "object-")] = append([]byte(nil), record.Value...)
		}
	}
	transfers, err := rgb11wallet.TransfersFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	activeInputs := rgb11ReservedInputOutpoints(transfers)
	requirements := rgb11ProofConfirmationRequirements(transfers)
	proofsByIdentity := make(map[string]*rgb11wallet.AllocationProof, len(proofs))
	proofsByOutpoint := make(map[string]*rgb11wallet.AllocationProof, len(proofs))
	for _, proof := range proofs {
		if proof == nil || proof.AssetName.Ticker == "" || proof.OperationID == "" {
			return nil, rgb11wallet.ErrInvalidProof
		}
		contractID, contractErr := rgb11ContractIDFromAssetName(proof.AssetName)
		if contractErr != nil {
			return nil, contractErr
		}
		identity := rgb11ProofIndexKey(contractID, proof.OperationID, proof.AssignmentType, proof.AssignmentIndex)
		if proofsByIdentity[identity] != nil || proofsByOutpoint[proof.OutPoint+"|"+proof.AssetName.String()] != nil {
			return nil, rgb11wallet.ErrInvalidProof
		}
		proofsByIdentity[identity] = proof
		proofsByOutpoint[proof.OutPoint+"|"+proof.AssetName.String()] = proof
	}

	reveals := make([]seals.GraphBlindSeal, 0, len(proofs))
	for _, proof := range proofs {
		seal, sealErr := seals.DecodeGraphBlindSeal(proof.SealDisclosure)
		if sealErr != nil {
			return nil, fmt.Errorf("decode RGB11 recovery seal: %w", sealErr)
		}
		reveals = append(reveals, seal)
	}
	validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(reveals...)
	consumers, validatedHistories, err := rgb11SnapshotConsignmentConsumers(ctx, validator, evidence, objects)
	if err != nil {
		return nil, err
	}
	consumersByInput := make(map[string][]rgb11SnapshotConsumerInput)
	for _, consumer := range consumers {
		inputKey, err := rgb11ConsumerInputKey(consumer)
		if err != nil {
			return nil, err
		}
		consumersByInput[inputKey] = append(consumersByInput[inputKey], consumer)
	}
	preparedHistories := make(map[string]*rgb11wallet.PreparedHistoryValidation)
	historicalReports := validatedHistories
	validateHistoricalConsignment := func(hash string) (*rgb11wallet.HistoricalValidation, error) {
		if report := historicalReports[hash]; report != nil {
			return report, nil
		}
		raw := objects[hash]
		if len(raw) == 0 {
			return nil, fmt.Errorf("RGB11 historical consignment %s is missing", hash)
		}
		report, err := validator.ValidateHistoricalConsignmentReport(ctx, raw, evidence)
		if err != nil {
			return nil, err
		}
		historicalReports[hash] = report
		return report, nil
	}
	// A selected peer may extend a receipt even when no current proof uses the
	// new allocation. Authenticate every added fact under this candidate's same
	// Bitcoin evidence/commit barrier, rather than trusting the peer's cache.
	for _, hash := range expandedReceipts {
		history, err := validateHistoricalConsignment(hash)
		if err != nil {
			return nil, fmt.Errorf("validate expanded RGB11 receipt history: %w", err)
		}
		stored := receipts[hash]
		authenticated, err := rgb11wallet.SelectHistoricalValidationReceipt(history, stored, nil)
		if err != nil || stored == nil {
			return nil, rgb11wallet.ErrValidationReceipt
		}
		storedHash, storedErr := stored.Hash()
		verifiedHash, verifiedErr := authenticated.Hash()
		if storedErr != nil || verifiedErr != nil || storedHash != verifiedHash {
			return nil, rgb11wallet.ErrValidationReceipt
		}
	}
	validateStoredProof := func(proof *rgb11wallet.AllocationProof) error {
		raw := objects[proof.ConsignmentHash]
		stored := receipts[proof.ConsignmentHash]
		if len(raw) == 0 || stored == nil || stored.ConsignmentHash != proof.ConsignmentHash ||
			!rgb11wallet.ContractAssetKeyMatches(proof.AssetName, stored.ContractID) {
			return fmt.Errorf("RGB11 proof %s is missing its immutable validated object", proof.OutPoint)
		}
		objectHash := sha256.Sum256(raw)
		if hex.EncodeToString(objectHash[:]) != proof.ConsignmentHash {
			return fmt.Errorf("RGB11 proof %s object hash mismatch", proof.OutPoint)
		}
		storedHash, hashErr := stored.Hash()
		if hashErr != nil || storedHash != proof.ValidationHash {
			return fmt.Errorf("RGB11 proof %s stored receipt mismatch", proof.OutPoint)
		}
		return nil
	}
	validatePreparedHistory := func(hash string) (*rgb11wallet.PreparedHistoryValidation, error) {
		if prepared := preparedHistories[hash]; prepared != nil {
			return prepared, nil
		}
		raw := objects[hash]
		if len(raw) == 0 {
			return nil, fmt.Errorf("RGB11 prepared consignment %s is missing", hash)
		}
		prepared, validateErr := validator.ValidatePreparedHistoryConsignmentReport(ctx, raw, evidence)
		if validateErr != nil || prepared == nil {
			return nil, fmt.Errorf("validate prepared RGB11 history %s: %w", hash,
				errRGB11ProjectionEvidence(validateErr))
		}
		preparedHistories[hash] = prepared
		return prepared, nil
	}
	validateCarrier := func(proof *rgb11wallet.AllocationProof, output *indexer.TxOutput,
		utxo *rgb11wallet.BitcoinUTXO) error {
		if !rgb11SnapshotOutputMatchesUTXO(output, utxo) || proof.CarrierBinding == nil ||
			proof.CarrierBinding.OutPoint != proof.OutPoint ||
			!bytes.Equal(proof.CarrierBinding.ActualPkScript, utxo.PkScript) {
			return fmt.Errorf("RGB11 allocation %s carrier value or script mismatch", proof.OutPoint)
		}
		if txscript.IsPayToTaproot(utxo.PkScript) && len(utxo.PkScript) == 34 {
			if len(proof.CarrierBinding.ActualOutputKey) != 0 &&
				!bytes.Equal(proof.CarrierBinding.ActualOutputKey, utxo.PkScript[2:]) {
				return fmt.Errorf("RGB11 allocation %s carrier output key mismatch", proof.OutPoint)
			}
		} else if len(proof.CarrierBinding.ActualOutputKey) != 0 {
			return fmt.Errorf("RGB11 allocation %s has an unexpected carrier output key", proof.OutPoint)
		}
		return nil
	}
	validateRawCarrier := func(proof *rgb11wallet.AllocationProof, output *indexer.TxOutput,
		txid string, preparedOutput *rgb11wallet.BitcoinUTXO) error {
		if preparedOutput != nil {
			if preparedOutput.OutPoint != proof.OutPoint || output == nil ||
				output.OutValue.Value != preparedOutput.Value ||
				!bytes.Equal(output.OutValue.PkScript, preparedOutput.PkScript) {
				return fmt.Errorf("RGB11 allocation %s prepared carrier mismatch", proof.OutPoint)
			}
			return validateCarrier(proof, output, preparedOutput)
		}
		rawTx, rawErr := evidence.GetRawTx(txid)
		if rawErr != nil {
			return fmt.Errorf("read RGB11 allocation transaction %s: %w", txid, rawErr)
		}
		var tx wire.MsgTx
		reader := bytes.NewReader(rawTx)
		if err := tx.Deserialize(reader); err != nil || reader.Len() != 0 || tx.TxHash().String() != txid {
			return fmt.Errorf("decode RGB11 allocation transaction %s: %w", txid, errRGB11ProjectionEvidence(err))
		}
		parsed, err := wire.NewOutPointFromString(proof.OutPoint)
		if err != nil || parsed == nil || uint64(parsed.Index) >= uint64(len(tx.TxOut)) || output == nil {
			return fmt.Errorf("RGB11 allocation %s transaction output is invalid", proof.OutPoint)
		}
		actual := tx.TxOut[parsed.Index]
		utxo := &rgb11wallet.BitcoinUTXO{
			OutPoint: proof.OutPoint, Value: actual.Value,
			PkScript: append([]byte(nil), actual.PkScript...),
		}
		return validateCarrier(proof, output, utxo)
	}
	validateForeignPreparedParent := func(row rgb11wallet.ValidatedConsumer,
		allocations []rgb11wallet.HistoricalAllocation) error {
		// These allocations come from core's complete HistoricalStates report:
		// registerOutputs derived the full outpoint and canonical seal disclosure
		// only after validating the operation's DBC witness. Match the exact
		// Opout and authenticated contract without reconstructing its asset type.
		var matched *rgb11wallet.HistoricalAllocation
		for i := range allocations {
			allocation := &allocations[i]
			if allocation.Allocation.OperationID != row.InputOperationID ||
				allocation.Allocation.AssignmentType != row.AssignmentType ||
				allocation.Allocation.AssignmentIndex != row.AssignmentIndex ||
				!rgb11wallet.ContractAssetKeyMatches(allocation.Allocation.AssetName, row.ContractID) {
				continue
			}
			if matched != nil {
				return fmt.Errorf("prepared RGB11 parent Opout has multiple historical allocations")
			}
			matched = allocation
		}
		if matched == nil || matched.WitnessState != uint8(coreconsignment.WitnessMined) ||
			matched.Allocation.OutPoint == "" || matched.Allocation.WitnessTxID == "" {
			return fmt.Errorf("prepared RGB11 parent Opout lacks a unique confirmed core allocation")
		}
		assetName := matched.Allocation.AssetName

		outpoint := matched.Allocation.OutPoint
		parsed, parseErr := wire.NewOutPointFromString(outpoint)
		if parseErr != nil || parsed == nil {
			return fmt.Errorf("parse prepared RGB11 parent outpoint: %w", errRGB11ProjectionEvidence(parseErr))
		}
		producerTxID := parsed.Hash.String()
		rawTx, rawErr := evidence.GetRawTx(producerTxID)
		if rawErr != nil {
			return fmt.Errorf("read prepared RGB11 parent carrier transaction: %w", errRGB11ProjectionEvidence(rawErr))
		}
		var producer wire.MsgTx
		reader := bytes.NewReader(rawTx)
		if err := producer.Deserialize(reader); err != nil || reader.Len() != 0 || producer.TxHash().String() != producerTxID ||
			uint64(parsed.Index) >= uint64(len(producer.TxOut)) {
			return fmt.Errorf("prepared RGB11 parent carrier transaction is invalid: %w", errRGB11ProjectionEvidence(err))
		}
		producerStatus, producerErr := evidence.GetTxStatus(producerTxID)
		if producerErr != nil || !validRGB11ObservedTxStatus(producerStatus, producerTxID) ||
			!producerStatus.Confirmed || producerStatus.Confirmations <= 0 || producerStatus.InMempool {
			return fmt.Errorf("prepared RGB11 parent carrier is not confirmed: %w", errRGB11ProjectionEvidence(producerErr))
		}
		witnessStatus, witnessErr := evidence.GetTxStatus(matched.Allocation.WitnessTxID)
		if witnessErr != nil || !validRGB11ObservedTxStatus(witnessStatus, matched.Allocation.WitnessTxID) ||
			!witnessStatus.Confirmed || witnessStatus.Confirmations <= 0 || witnessStatus.InMempool {
			return fmt.Errorf("prepared RGB11 parent assignment witness is not confirmed: %w", errRGB11ProjectionEvidence(witnessErr))
		}

		spend, spendErr := evidence.GetOutspend(outpoint)
		if spendErr != nil || spend == nil {
			return fmt.Errorf("read prepared RGB11 parent spend: %w", errRGB11ProjectionEvidence(spendErr))
		}
		carrierOutput := producer.TxOut[parsed.Index]
		if !spend.Spent {
			if spend.SpendingTx != "" {
				return fmt.Errorf("prepared RGB11 parent has contradictory unspent evidence")
			}
			utxo, utxoErr := evidence.GetUTXO(outpoint)
			if utxoErr != nil || utxo == nil || utxo.OutPoint != outpoint || utxo.Confirmations <= 0 ||
				utxo.Value != carrierOutput.Value || !bytes.Equal(utxo.PkScript, carrierOutput.PkScript) {
				return fmt.Errorf("prepared RGB11 parent is not a positively verified UTXO: %w", errRGB11ProjectionEvidence(utxoErr))
			}
			return nil
		}

		parentInput := rgb11ProofIndexKey(assetName.Ticker, row.InputOperationID,
			row.AssignmentType, row.AssignmentIndex)
		consumerRows := consumersByInput[parentInput]
		spenderID, consumerOperation := "", ""
		spenderHashes := make(map[string]struct{})
		for _, consumer := range consumerRows {
			if consumer.WitnessState != uint8(coreconsignment.WitnessMined) ||
				(consumer.WitnessTxID != spend.SpendingTx && spend.SpendingTx != "" && spend.SpendingTx != "unknown") {
				continue
			}
			if consumer.WitnessTxID == "" || consumer.ConsumerOperationID == "" ||
				!rgb11wallet.ContractAssetKeyMatches(assetName, consumer.ContractID) {
				return fmt.Errorf("prepared RGB11 parent has an incomplete confirmed consumer edge")
			}
			if spenderID != "" && (spenderID != consumer.WitnessTxID || consumerOperation != consumer.ConsumerOperationID) {
				return fmt.Errorf("prepared RGB11 parent has ambiguous confirmed consumers")
			}
			spenderID, consumerOperation = consumer.WitnessTxID, consumer.ConsumerOperationID
			spenderHashes[consumer.ConsignmentHash] = struct{}{}
		}
		if spenderID == "" {
			return fmt.Errorf("prepared RGB11 parent spend has no confirmed core consumer")
		}
		if _, verified := verifyRGB11ExpectedSpend(evidence, outpoint, spenderID); !verified {
			return fmt.Errorf("prepared RGB11 parent consumer has no confirmed raw witness")
		}
		for hash := range spenderHashes {
			if _, historyErr := validateHistoricalConsignment(hash); historyErr != nil {
				return fmt.Errorf("validate prepared RGB11 parent consumer history: %w", historyErr)
			}
			container, decodeErr := coreconsignment.Decode(objects[hash])
			if decodeErr != nil || container == nil || !rgb11wallet.ContractAssetKeyMatches(assetName, container.ContractID) {
				return fmt.Errorf("prepared RGB11 parent consumer contract mismatch: %w", errRGB11ProjectionEvidence(decodeErr))
			}
		}
		return nil
	}

	proofStates := make(map[string]*rgb11RecoveryProofCandidate, len(proofs))
	orderedProofs := append([]*rgb11wallet.AllocationProof(nil), proofs...)
	for _, proof := range orderedProofs {
		if err := validateStoredProof(proof); err != nil {
			return nil, err
		}
		output := outputs[proof.OutPoint]
		if output == nil || !rgb11ProofMatchesValidatedAllocation(proof, receipts[proof.ConsignmentHash], output) {
			return nil, fmt.Errorf("RGB11 proof %s does not match its stored allocation", proof.OutPoint)
		}
		producerTxID, txidErr := rgb11RecoveryProofOutpoint(proof)
		if txidErr != nil {
			return nil, txidErr
		}
		outspend, spendErr := evidence.GetOutspend(proof.OutPoint)
		if spendErr != nil || outspend == nil {
			return nil, fmt.Errorf("read RGB11 allocation spend %s: %w", proof.OutPoint,
				errRGB11ProjectionEvidence(spendErr))
		}
		status, statusErr := evidence.GetTxStatus(producerTxID)
		if statusErr != nil || !validRGB11ObservedTxStatus(status, producerTxID) {
			return nil, fmt.Errorf("read RGB11 allocation producer %s status: %w", producerTxID,
				errRGB11ProjectionEvidence(statusErr))
		}
		state := &rgb11RecoveryProofCandidate{
			proof: proof, producerTxID: producerTxID, producerStatus: status,
			activeOperation: activeInputs[proof.OutPoint], outspend: outspend,
		}
		if status.Confirmed || status.InMempool {
			// Authenticate the immutable full Opout against core-validated history.
			// A sibling terminal in this old object may since have been spent;
			// current ownership is determined below from this exact carrier.
			history, historyErr := validateHistoricalConsignment(proof.ConsignmentHash)
			if historyErr != nil {
				return nil, fmt.Errorf("validate RGB11 allocation history %s: %w",
					proof.OutPoint, historyErr)
			}
			stored := receipts[proof.ConsignmentHash]
			if history == nil || history.MaterialReceipt == nil || stored == nil ||
				!rgb11ProofMatchesHistoricalReceipt(proof, stored, output, history) ||
				!sameRGB11ValidationReceiptIdentity(stored, history.MaterialReceipt) {
				return nil, fmt.Errorf("RGB11 proof %s does not match core-validated full Opout history: %w",
					proof.OutPoint, rgb11wallet.ErrInvalidProof)
			}
			authenticated, authErr := rgb11wallet.SelectHistoricalValidationReceipt(history, stored, nil)
			if authErr != nil || authenticated == nil {
				return nil, fmt.Errorf("authenticate RGB11 receipt against core history: %w",
					rgb11wallet.ErrValidationReceipt)
			}
			storedHash, storedHashErr := stored.Hash()
			authenticatedHash, authenticatedHashErr := authenticated.Hash()
			if storedHashErr != nil || authenticatedHashErr != nil ||
				storedHash != authenticatedHash || proof.ValidationHash != storedHash {
				return nil, rgb11wallet.ErrValidationReceipt
			}
			if err := validateRawCarrier(proof, output, producerTxID, nil); err != nil {
				return nil, err
			}
			if outspend.Spent {
				spenderID := outspend.SpendingTx
				rows := consumersByInput[rgb11ProofIndexKey(
					mustRGB11ContractID(proof.AssetName), proof.OperationID, proof.AssignmentType, proof.AssignmentIndex)]
				if spenderID == "" || spenderID == "unknown" {
					candidateWitness, candidateOperation := "", ""
					for _, row := range rows {
						if row.WitnessState != uint8(coreconsignment.WitnessMined) {
							continue
						}
						if row.WitnessTxID == "" || row.ConsumerOperationID == "" ||
							!rgb11wallet.ContractAssetKeyMatches(proof.AssetName, row.ContractID) {
							return nil, fmt.Errorf("RGB11 allocation %s has an incomplete core consumer edge", proof.OutPoint)
						}
						if candidateWitness != "" && (candidateWitness != row.WitnessTxID ||
							candidateOperation != row.ConsumerOperationID) {
							return nil, fmt.Errorf("RGB11 allocation %s has ambiguous confirmed core consumers", proof.OutPoint)
						}
						candidateWitness, candidateOperation = row.WitnessTxID, row.ConsumerOperationID
					}
					if candidateWitness == "" {
						return nil, fmt.Errorf("RGB11 allocation %s has an unidentified spender", proof.OutPoint)
					}
					if _, verified := verifyRGB11ExpectedSpend(evidence, proof.OutPoint, candidateWitness); !verified {
						return nil, fmt.Errorf("RGB11 allocation %s has no confirmed raw consumer for its exact Opout", proof.OutPoint)
					}
					spenderID = candidateWitness
				}
				spenderStatus, spenderErr := evidence.GetTxStatus(spenderID)
				if spenderErr != nil || spenderStatus == nil || spenderStatus.TxID != spenderID ||
					(!spenderStatus.Confirmed && !spenderStatus.InMempool) ||
					(spenderStatus.Confirmed && (spenderStatus.Confirmations <= 0 || spenderStatus.InMempool)) ||
					(!spenderStatus.Confirmed && spenderStatus.Confirmations != 0) {
					return nil, fmt.Errorf("RGB11 allocation %s spender is not current: %w", proof.OutPoint,
						errRGB11ProjectionEvidence(spenderErr))
				}
				if spenderStatus.Confirmed && !status.Confirmed {
					return nil, fmt.Errorf("%w: RGB11 producer %s (%s) is unconfirmed but its spender %s is confirmed",
						ErrRGB11Inconsistent, producerTxID, proof.OutPoint, spenderID)
				}
				if (outspend.SpendingTx == "" || outspend.SpendingTx == "unknown") &&
					(!spenderStatus.Confirmed || spenderStatus.Confirmations <= 0 || spenderStatus.InMempool) {
					return nil, fmt.Errorf("RGB11 allocation %s inferred spender is not confirmed", proof.OutPoint)
				}
				consumerOperation := ""
				hashes := make(map[string]bool)
				for _, row := range rows {
					if row.WitnessTxID != spenderID {
						continue
					}
					if row.ConsumerOperationID == "" || !rgb11wallet.ContractAssetKeyMatches(proof.AssetName, row.ContractID) {
						return nil, fmt.Errorf("RGB11 allocation %s consumer contract or operation mismatch", proof.OutPoint)
					}
					if consumerOperation != "" && consumerOperation != row.ConsumerOperationID {
						return nil, fmt.Errorf("RGB11 allocation %s has competing consumers in one spender", proof.OutPoint)
					}
					consumerOperation = row.ConsumerOperationID
					hashes[row.ConsignmentHash] = true
				}
				if consumerOperation == "" || len(hashes) == 0 {
					if !spenderStatus.Confirmed {
						return nil, fmt.Errorf("RGB11 allocation %s has an unvalidated mempool spender", proof.OutPoint)
					}
					if _, verified := verifyRGB11ExpectedSpend(evidence, proof.OutPoint, spenderID); !verified {
						return nil, fmt.Errorf("RGB11 allocation %s has no exact confirmed raw spender", proof.OutPoint)
					}
					// This is unavailable ownership, not proof of irreversible burn.
					state.currentStatus, state.confirmations = "inconsistent", status.Confirmations
					proofStates[proof.OutPoint+"|"+proof.AssetName.String()] = state
					continue
				}
				for hash := range hashes {
					if _, err := validateHistoricalConsignment(hash); err != nil {
						return nil, fmt.Errorf("validate RGB11 allocation consumer %s: %w", hash,
							err)
					}
					container, decodeErr := coreconsignment.Decode(objects[hash])
					if decodeErr != nil || container == nil ||
						!rgb11wallet.ContractAssetKeyMatches(proof.AssetName, container.ContractID) {
						return nil, fmt.Errorf("RGB11 allocation consumer %s contract mismatch: %w", hash,
							errRGB11ProjectionEvidence(decodeErr))
					}
				}
				state.currentStatus = "spending"
				state.confirmations = status.Confirmations
				state.outspend = outspend
				if outspend.SpendingTx != spenderID {
					resolvedSpend := *outspend
					resolvedSpend.SpendingTx = spenderID
					state.outspend = &resolvedSpend
				}
			} else {
				if outspend.SpendingTx != "" {
					return nil, fmt.Errorf("RGB11 allocation %s has contradictory unspent evidence", proof.OutPoint)
				}
				allocation := rgb11ValidatedAllocationForProof(proof, history.MaterialReceipt, output)
				if allocation == nil {
					return nil, rgb11wallet.ErrInvalidProof
				}
				// Require a positive current UTXO even for an observed mempool
				// producer, as the previous current-validation path did.
				utxo, utxoErr := evidence.GetUTXO(proof.OutPoint)
				if utxoErr != nil || utxo == nil || utxo.OutPoint != proof.OutPoint ||
					(status.Confirmed && utxo.Confirmations <= 0) {
					return nil, fmt.Errorf("verify unspent RGB11 allocation %s: %w", proof.OutPoint,
						errRGB11ProjectionEvidence(utxoErr))
				}
				if err := validateCarrier(proof, output, utxo); err != nil {
					return nil, err
				}
				// The carrier producer and RGB assignment witness differ for
				// graph blind seals. Carrier evidence above proves the Bitcoin
				// outpoint; only the core-validated witness for this exact Opout
				// controls confirmation depth and spendability.
				assignmentTxID := allocation.WitnessTxID
				assignmentStatus := status
				if assignmentTxID == "" {
					assignmentTxID = producerTxID
				}
				if assignmentTxID != producerTxID {
					assignmentStatus, statusErr = evidence.GetTxStatus(assignmentTxID)
					if statusErr != nil || !validRGB11ObservedTxStatus(assignmentStatus, assignmentTxID) ||
						(!assignmentStatus.Confirmed && !assignmentStatus.InMempool) {
						return nil, fmt.Errorf("read RGB11 assignment witness %s status: %w", assignmentTxID,
							errRGB11ProjectionEvidence(statusErr))
					}
				}
				state.currentStatus = "valid"
				state.confirmations = 0
				if assignmentStatus.Confirmed {
					state.confirmations = assignmentStatus.Confirmations
					required := requirements[proof.OutPoint]
					if required < 1 {
						required = 1
					}
					if assignmentStatus.Confirmations >= required {
						state.currentStatus = "settled"
					}
				}
			}
		} else {
			if outspend.Spent || outspend.SpendingTx != "" {
				return nil, fmt.Errorf("RGB11 allocation %s has a spender but its producer is absent", proof.OutPoint)
			}
			prepared, validationErr := validatePreparedHistory(proof.ConsignmentHash)
			var preparedReceipt *rgb11wallet.ValidationReceipt
			if prepared != nil {
				stored := receipts[proof.ConsignmentHash]
				if stored == nil || !rgb11wallet.ContractAssetKeyMatches(proof.AssetName, stored.ContractID) {
					return nil, fmt.Errorf("RGB11 prepared proof %s has no canonical stored contract identity: %w",
						proof.OutPoint, rgb11wallet.ErrValidationReceipt)
				}
				preparedReceipt = &rgb11wallet.ValidationReceipt{
					ContractID: stored.ContractID, Allocations: prepared.Allocations,
				}
			}
			preparedAllocations := 0
			preparedWitnessMatches := false
			preparedProofMatches := false
			if prepared != nil {
				preparedAllocations = len(prepared.Allocations)
				preparedWitnessMatches = containsString(prepared.WitnessTxIDs, producerTxID)
				preparedProofMatches = rgb11ProofMatchesValidatedAllocation(proof, preparedReceipt, output)
			}
			if validationErr != nil || prepared == nil ||
				!preparedWitnessMatches || !preparedProofMatches {
				return nil, fmt.Errorf("RGB11 allocation %s is absent without a matching prepared proof (historyErr=%v, witness=%v, allocations=%d): %w",
					proof.OutPoint, validationErr, preparedWitnessMatches, preparedAllocations,
					rgb11wallet.ErrInvalidProof)
			}
			preparedOutput := prepared.Outputs[proof.OutPoint]
			if preparedOutput == nil {
				return nil, fmt.Errorf("RGB11 allocation %s prepared output is missing", proof.OutPoint)
			}
			if err := validateRawCarrier(proof, output, producerTxID, preparedOutput); err != nil {
				return nil, err
			}
			state.currentStatus = "orphaned"
			state.prepared = true
		}
		proofStates[proof.OutPoint+"|"+proof.AssetName.String()] = state
	}

	// An absent transition output is orphaned only when core validates its
	// embedded prepared branch and every exact parent Opout has positive current
	// unspent or replacement-consumer evidence above. An absent issuance has no
	// such parent evidence and remains unknown.
	for _, state := range proofStates {
		if !state.prepared {
			continue
		}
		proof := state.proof
		prepared := preparedHistories[proof.ConsignmentHash]
		if prepared == nil {
			return nil, fmt.Errorf("absent RGB11 producer %s has no prepared history report", proof.OperationID)
		}
		var parentRows []rgb11wallet.ValidatedConsumer
		for _, row := range prepared.Consumers {
			if row.ConsumerOperationID == proof.OperationID && row.WitnessTxID == state.producerTxID &&
				row.WitnessState == uint8(coreconsignment.WitnessPrepared) {
				parentRows = append(parentRows, row)
			}
		}
		if len(parentRows) == 0 {
			return nil, fmt.Errorf("absent RGB11 transition %s has no bound parent inputs", proof.OperationID)
		}
		for _, row := range parentRows {
			assetKey, keyErr := rgb11wallet.NewContractAssetKey(row.ContractID, indexer.ASSET_TYPE_FT)
			if keyErr != nil {
				return nil, keyErr
			}
			parentKey := rgb11ProofIndexKey(assetKey.Ticker, row.InputOperationID,
				row.AssignmentType, row.AssignmentIndex)
			parent := proofsByIdentity[parentKey]
			if parent == nil {
				if err := validateForeignPreparedParent(row, prepared.HistoricalAllocations); err != nil {
					return nil, fmt.Errorf("absent RGB11 transition %s foreign parent %s is not positively verified: %w",
						proof.OperationID, parentKey, err)
				}
				continue
			}
			parentState := proofStates[parent.OutPoint+"|"+parent.AssetName.String()]
			if parentState == nil {
				return nil, fmt.Errorf("absent RGB11 transition %s has an unverified parent %s", proof.OperationID, parent.OutPoint)
			}
			confirmedUnspent := (parentState.currentStatus == "valid" || parentState.currentStatus == "settled") &&
				parentState.producerStatus != nil && parentState.producerStatus.Confirmed &&
				parentState.outspend != nil && !parentState.outspend.Spent && parentState.outspend.SpendingTx == ""
			validatedReplacement := parentState.currentStatus == "spending" &&
				parentState.outspend != nil && parentState.outspend.Spent && parentState.outspend.SpendingTx != ""
			quarantinedParent := parentState.currentStatus == "inconsistent" &&
				parentState.outspend != nil && parentState.outspend.Spent && parentState.outspend.SpendingTx != ""
			// Every prepared parent's own bound parents are checked in this loop;
			// core full-Opout validation above authenticates the dependency edges.
			if !confirmedUnspent && !validatedReplacement && !quarantinedParent && !parentState.prepared {
				return nil, fmt.Errorf("absent RGB11 transition %s lacks positive parent chain evidence for %s",
					proof.OperationID, parent.OutPoint)
			}
		}
	}

	// Reconcile only journals bound to the prepared owned successors just
	// verified above. Missing-successor/full-send recovery retains the settled
	// guards below; Bitcoin input absence alone cannot authorize RGB rollback.
	pendings, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(projectionRecords)
	if err != nil {
		return nil, err
	}
	for _, pending := range pendings {
		if pending.State.Direction != "send" ||
			(pending.State.Status != "settled" && pending.State.Status != "orphaned") {
			continue
		}
		bound := false
		for _, proofState := range proofStates {
			if proofState.prepared && proofState.producerTxID == pending.State.WitnessTxID &&
				proofState.proof.ConsignmentHash == pending.LocalObjectHash &&
				containsString(pending.State.OutputOutPoints, proofState.proof.OutPoint) &&
				containsString(rgb11PendingChangeOutpoints(pending), proofState.proof.OutPoint) {
				bound = true
				break
			}
		}
		if !bound {
			continue
		}
		if err := verifyRGB11OrphanedJournalInputs(ctx, evidence, pending); err != nil {
			return nil, err
		}
		if pending.State.Status == "orphaned" {
			continue
		}
		// Match refresh's terminal orphaned state, not pending: restored inputs
		// must no longer be reserved for an absent previously-settled witness.
		pending.State.Status = "orphaned"
		projectionRecords, err = rgb11wallet.UpsertProjectionSnapshotPending(projectionRecords, pending)
		if err != nil {
			return nil, err
		}
	}
	// The precommit own-witness check must observe these same journal updates.
	// Both settled and orphaned exclude activeInputs and have identical output
	// confirmation requirements, so those earlier derived maps remain valid.
	transfers, err = rgb11wallet.TransfersFromProjectionSnapshot(projectionRecords)
	if err != nil {
		return nil, err
	}

	updates := make([]rgb11wallet.SnapshotProofUpdate, 0, len(proofs))
	lockReasons := make(map[string]string, len(proofs))
	complete := true
	for _, proof := range proofs {
		state := proofStates[proof.OutPoint+"|"+proof.AssetName.String()]
		if state == nil {
			return nil, rgb11wallet.ErrInvalidProof
		}
		updates = append(updates, rgb11wallet.SnapshotProofUpdate{
			OutPoint: proof.OutPoint, AssetName: proof.AssetName,
			Status: state.currentStatus, Confirmations: state.confirmations,
		})
		reason := rgb11wallet.LockReasonPending
		current := *proof
		current.Status = state.currentStatus
		current.Confirmations = state.confirmations
		if !state.activeOperation && rgb11ProofIsAvailable(&current, requirements) {
			reason = rgb11wallet.LockReasonRGB
		}
		if previous := lockReasons[proof.OutPoint]; previous == rgb11wallet.LockReasonPending {
			continue
		}
		lockReasons[proof.OutPoint] = reason
	}
	for outpoint := range activeInputs {
		lockReasons[outpoint] = rgb11wallet.LockReasonPending
	}
	updatedRecords, err := rgb11wallet.UpdateProjectionSnapshotProofs(projectionRecords, updates)
	if err != nil {
		return nil, err
	}
	resolved := *snapshot
	resolved.ProjectionRecords = updatedRecords
	tickerInfos, err := p.tickerInfosFromRGB11Snapshot(&resolved)
	if err != nil {
		return nil, err
	}
	for index, info := range tickerInfos {
		var ext rgb11wallet.TickerExt
		if err := json.Unmarshal(info.Content, &ext); err != nil {
			return nil, err
		}
		info, err = p.withRGB11NamingOriginFromRaw(info, objects[ext.ContractHash])
		if err != nil {
			return nil, err
		}
		tickerInfos[index] = info
	}
	after, err := evidence.GetTip()
	if err != nil || after == nil || after.Height != expectedTip.Height || after.BlockHash != expectedTip.BlockHash {
		return nil, fmt.Errorf("RGB11 chain tip changed while building projection: %w", errRGB11ProjectionEvidence(err))
	}
	return &rgb11ResolvedRecoveryProjection{
		snapshot: &resolved, lockReasons: lockReasons, tickerInfos: tickerInfos,
		complete: complete, checkEvidence: func() error {
			// A different RGB consumer can prove a carrier spent without proving
			// this journal's fee inputs spent. Retire its owner only while its own
			// signed witness still meets the settlement confirmation requirement.
			for _, state := range transfers {
				if !rgb11TransferHasExpectedSpend(state) || state.Status != "settled" {
					continue
				}
				status, err := evidence.GetTxStatus(state.WitnessTxID)
				required := int64(state.MinConfirmations)
				if required < 1 {
					required = 1
				}
				if err != nil || !validRGB11ObservedTxStatus(status, state.WitnessTxID) ||
					!status.Confirmed || status.Confirmations < required {
					return fmt.Errorf("%w: settled RGB11 journal witness is not confirmed", ErrRGB11Inconsistent)
				}
			}
			if err := evidence.verifyStable(ctx); err != nil {
				return fmt.Errorf("Bitcoin evidence changed while building RGB11 projection: %w", err)
			}
			// The resolver's after-tip read precedes local lock-plan calculation
			// and batch staging. Recheck at the publication boundary so a block
			// transition during that interval cannot publish a mixed-generation
			// projection even when the exact UTXO/outspend/status dependencies
			// happen to remain unchanged.
			finalTip, err := evidence.GetTip()
			if err != nil || finalTip == nil || finalTip.Height != expectedTip.Height ||
				finalTip.BlockHash != expectedTip.BlockHash {
				return fmt.Errorf("RGB11 chain tip changed before projection commit: %w", errRGB11ProjectionEvidence(err))
			}
			return nil
		},
	}, nil
}

func mustRGB11ContractID(asset indexer.AssetName) string {
	return asset.Ticker
}

func rgb11ContractIDFromAssetName(asset indexer.AssetName) (string, error) {
	if asset.Protocol != rgb11wallet.Protocol || asset.Ticker == "" {
		return "", rgb11wallet.ErrInvalidRGB11Asset
	}
	id, err := consensus.IDFromHex(asset.Ticker)
	if err != nil {
		return "", rgb11wallet.ErrInvalidRGB11Asset
	}
	contractID := consensus.ContractID(id).String()
	canonical, err := rgb11wallet.NewContractAssetKey(contractID, asset.Type)
	if err != nil || canonical != asset {
		return "", rgb11wallet.ErrInvalidRGB11Asset
	}
	return canonical.Ticker, nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func errRGB11ProjectionEvidence(err error) error {
	if err != nil {
		return err
	}
	return ErrRGB11Inconsistent
}

func rgb11SnapshotInconsistentOutpoints(snapshot *RGB11WalletSnapshot) ([]string, error) {
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	points := make(map[string]bool)
	for _, proof := range proofs {
		if proof.Status == "inconsistent" {
			points[proof.OutPoint] = true
		}
	}
	return sortedRGB11EvidenceKeys(points), nil
}
