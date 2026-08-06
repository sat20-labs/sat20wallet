package wallet

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/rgb11/anchors"
	"github.com/sat20-labs/rgb11/baid64"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/invoicing"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/operations"
	corepsbt "github.com/sat20-labs/rgb11/psbt"
	"github.com/sat20-labs/rgb11/rejectlist"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrRGB11Inconsistent   = rgb11wallet.ErrRGB11Inconsistent
	ErrRGB11STPUnavailable = rgb11wallet.ErrRGB11STPUnavailable
)

// rgb11Manager owns the wallet-local RGB11 runtime and synchronization state.
// It embeds the outer wallet Manager only as an infrastructure host; all RGB11
// behavior is implemented on this dedicated manager and exposed by thin API
// forwarding methods in rgb11_api.go.
type rgb11Manager struct {
	*Manager
	projectionStore   *rgb11wallet.ProjectionStore
	engineStore       *rgb11wallet.EngineStore
	engine            *corewallet.Engine
	evidence          rgb11wallet.BitcoinEvidenceProvider
	rejectLists       RGB11RejectListProvider
	consistencyStatus string
	accountOwner      *Manager
	scopeStates       *rgb11ScopeStateRegistry
}

func newRGB11Manager(owner *Manager, database indexer.KVDB, locker *UtxoLocker,
	evidence rgb11wallet.BitcoinEvidenceProvider) (*rgb11Manager, error) {
	projectionStore := rgb11wallet.NewProjectionStore(database, locker)
	engineStore := rgb11wallet.NewEngineStore(database)
	engine, err := corewallet.NewEngine(engineStore)
	if err != nil {
		return nil, err
	}
	return &rgb11Manager{
		Manager:         owner,
		projectionStore: projectionStore,
		engineStore:     engineStore,
		engine:          engine,
		evidence:        evidence,
		accountOwner:    owner,
		scopeStates:     newRGB11ScopeStateRegistry(),
	}, nil
}

func rejectRGB11STPAsset(asset *indexer.AssetName) error {
	if asset != nil && asset.Protocol == rgb11wallet.Protocol {
		return ErrRGB11STPUnavailable
	}
	return nil
}

func (p *rgb11Manager) GetRGB11ProjectionStore() *rgb11wallet.ProjectionStore {
	if p == nil || p.rgbManager == nil {
		return nil
	}
	return p.rgbManager.projectionStore
}

func (p *rgb11Manager) selectRGB11Scope() error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil ||
		p.rgbManager.engineStore == nil || p.status == nil {
		return rgb11wallet.ErrWalletScope
	}
	scope := rgb11StorageScope(p.status.CurrentWallet, p.status.CurrentAccount)
	if err := p.rgbManager.projectionStore.SetScope(scope); err != nil {
		return err
	}
	return p.rgbManager.engineStore.SetScope(scope)
}

func rgb11TransferKeepsInputsLocked(state *rgb11wallet.TransferState) bool {
	if state == nil || state.Direction != "send" {
		return false
	}
	switch state.Status {
	case "prepared", "delivered", "broadcast", "pending", "settled":
		return true
	default:
		return false
	}
}

func validateRGB11PendingTransaction(pending *rgb11wallet.PendingTransfer) error {
	if pending == nil || !rgb11TransferKeepsInputsLocked(&pending.State) ||
		pending.State.WitnessTxID == "" || len(pending.SignedTx) == 0 ||
		len(pending.State.InputOutPoints) == 0 {
		return ErrRGB11Inconsistent
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		return fmt.Errorf("%w: decode pending transaction: %v", ErrRGB11Inconsistent, err)
	}
	if tx.TxHash().String() != pending.State.WitnessTxID {
		return fmt.Errorf("%w: pending transaction id mismatch", ErrRGB11Inconsistent)
	}
	inputs := make(map[string]struct{}, len(tx.TxIn))
	for _, input := range tx.TxIn {
		inputs[input.PreviousOutPoint.String()] = struct{}{}
	}
	for _, outpoint := range pending.State.InputOutPoints {
		if _, ok := inputs[outpoint]; !ok {
			return fmt.Errorf("%w: pending transaction does not spend %s", ErrRGB11Inconsistent, outpoint)
		}
	}
	for _, outpoint := range pending.State.OutputOutPoints {
		parsed, err := wire.NewOutPointFromString(outpoint)
		if err != nil || parsed.Hash.String() != pending.State.WitnessTxID ||
			int(parsed.Index) >= len(tx.TxOut) {
			return fmt.Errorf("%w: pending transaction output %s is invalid", ErrRGB11Inconsistent, outpoint)
		}
	}
	return nil
}

func (p *rgb11Manager) rgb11ExpectedInputs() (map[string]string, error) {
	transfers, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return nil, err
	}
	expected := make(map[string]string)
	for _, state := range transfers {
		if !rgb11TransferKeepsInputsLocked(state) {
			continue
		}
		pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(state.TransferID)
		if err != nil {
			return nil, err
		}
		if err := validateRGB11PendingTransaction(pending); err != nil {
			return nil, err
		}
		for _, outpoint := range state.InputOutPoints {
			if txid := expected[outpoint]; txid != "" && txid != state.WitnessTxID {
				return nil, fmt.Errorf("%w: RGB11 input %s is reserved by multiple transactions",
					ErrRGB11Inconsistent, outpoint)
			}
			expected[outpoint] = state.WitnessTxID
		}
	}
	return expected, nil
}

func (p *rgb11Manager) rgb11ExpectedChangeOutpoints() (map[string]string, error) {
	transfers, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return nil, err
	}
	expected := make(map[string]string)
	for _, state := range transfers {
		if !rgb11TransferKeepsInputsLocked(state) {
			continue
		}
		pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(state.TransferID)
		if err != nil {
			return nil, err
		}
		if err := validateRGB11PendingTransaction(pending); err != nil {
			return nil, err
		}
		for _, seal := range pending.ChangeSeals {
			outpoint := fmt.Sprintf("%s:%d", state.WitnessTxID, seal.Vout)
			if txid := expected[outpoint]; txid != "" && txid != state.WitnessTxID {
				return nil, fmt.Errorf("%w: RGB11 change output %s is reserved by multiple transactions",
					ErrRGB11Inconsistent, outpoint)
			}
			expected[outpoint] = state.WitnessTxID
		}
	}
	return expected, nil
}

func rgb11ProofConfirmationRequirements(transfers []*rgb11wallet.TransferState) map[string]int64 {
	requirements := make(map[string]int64)
	for _, state := range transfers {
		if state == nil || state.Status == "rejected" || state.Status == "conflicted" {
			continue
		}
		required := int64(state.MinConfirmations)
		if required < 1 {
			required = 1
		}
		for _, outpoint := range state.OutputOutPoints {
			if requirements[outpoint] < required {
				requirements[outpoint] = required
			}
		}
	}
	return requirements
}

func rgb11ReservedInputOutpoints(transfers []*rgb11wallet.TransferState) map[string]bool {
	inputs := make(map[string]bool)
	for _, state := range transfers {
		if !rgb11TransferKeepsInputsLocked(state) {
			continue
		}
		for _, outpoint := range state.InputOutPoints {
			inputs[outpoint] = true
		}
	}
	return inputs
}

func rgb11ProofIsCurrent(proof *rgb11wallet.AllocationProof) bool {
	return proof != nil && proof.Status != "spending" && proof.Status != "inconsistent"
}

func rgb11ProofIsAvailable(proof *rgb11wallet.AllocationProof, requirements map[string]int64) bool {
	if !rgb11ProofIsCurrent(proof) {
		return false
	}
	required := requirements[proof.OutPoint]
	if required < 1 {
		required = 1
	}
	return proof.Confirmations >= required
}

type rgb11ActiveReservation struct {
	outpoints       map[string]struct{}
	previousReasons map[string]string
	changeOutpoints map[string]struct{}
	allSettled      bool
}

func rgb11PendingChangeOutpoints(pending *rgb11wallet.PendingTransfer) []string {
	if pending == nil {
		return nil
	}
	result := make([]string, 0, len(pending.ChangeSeals))
	for _, seal := range pending.ChangeSeals {
		result = append(result, fmt.Sprintf("%s:%d", pending.State.WitnessTxID, seal.Vout))
	}
	return result
}

func rgb11PendingReservationOutpoints(pendingList []*rgb11wallet.PendingTransfer) (string, []string, error) {
	reservationID := ""
	values := make(map[string]struct{})
	for _, pending := range pendingList {
		if pending == nil {
			continue
		}
		if pending.ReservationID != "" {
			if reservationID != "" && reservationID != pending.ReservationID {
				return "", nil, fmt.Errorf("%w: pending batch reservation owner mismatch", ErrRGB11Inconsistent)
			}
			reservationID = pending.ReservationID
		}
		for _, outpoint := range pending.State.InputOutPoints {
			values[outpoint] = struct{}{}
		}
		for _, outpoint := range rgb11PendingChangeOutpoints(pending) {
			values[outpoint] = struct{}{}
		}
	}
	outpoints := make([]string, 0, len(values))
	for outpoint := range values {
		outpoints = append(outpoints, outpoint)
	}
	sort.Strings(outpoints)
	return reservationID, outpoints, nil
}

func (p *rgb11Manager) releaseRGB11PendingReservation(pendingList []*rgb11wallet.PendingTransfer) error {
	reservationID, outpoints, err := rgb11PendingReservationOutpoints(pendingList)
	if err != nil {
		return err
	}
	if reservationID == "" {
		return fmt.Errorf("%w: pending RGB11 reservation has no owner", ErrRGB11Inconsistent)
	}
	return p.utxoLockerL1.ReleaseReservation(outpoints, reservationID)
}

func (p *rgb11Manager) finalizeRGB11PendingChangeReservation(pending *rgb11wallet.PendingTransfer) error {
	changes := rgb11PendingChangeOutpoints(pending)
	if len(changes) == 0 {
		return nil
	}
	if pending.ReservationID == "" {
		return fmt.Errorf("%w: pending RGB11 reservation has no owner", ErrRGB11Inconsistent)
	}
	return p.utxoLockerL1.FinalizeReservation(changes, pending.ReservationID, rgb11wallet.LockReasonRGB)
}

// reconcileRGB11Reservations reconstructs operation ownership from persisted
// pending transfers and receive reservations, then removes owner tokens that
// no longer correspond to an active operation.
func (p *rgb11Manager) reconcileRGB11Reservations() error {
	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return err
	}
	proofOutpoints := make(map[string]struct{}, len(proofs))
	for _, proof := range proofs {
		if proof != nil {
			proofOutpoints[proof.OutPoint] = struct{}{}
		}
	}
	transfers, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return err
	}
	groups := make(map[string]*rgb11ActiveReservation)
	witnessOwners := make(map[string]string)
	for _, state := range transfers {
		if !rgb11TransferKeepsInputsLocked(state) {
			continue
		}
		pending, loadErr := p.rgbManager.projectionStore.LoadPendingTransfer(state.TransferID)
		if loadErr != nil {
			return loadErr
		}
		if pending.ReservationID == "" {
			continue
		}
		if owner := witnessOwners[state.WitnessTxID]; owner != "" && owner != pending.ReservationID {
			return fmt.Errorf("%w: witness transaction has multiple reservation owners", ErrRGB11Inconsistent)
		}
		witnessOwners[state.WitnessTxID] = pending.ReservationID
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
			group.outpoints[outpoint] = struct{}{}
			if _, ok := proofOutpoints[outpoint]; ok {
				group.previousReasons[outpoint] = rgb11wallet.LockReasonRGB
			}
		}
		for _, outpoint := range rgb11PendingChangeOutpoints(pending) {
			group.changeOutpoints[outpoint] = struct{}{}
			if state.Status != "settled" {
				group.outpoints[outpoint] = struct{}{}
			}
		}
	}
	active := make(map[string]map[string]struct{}, len(groups))
	for reservationID, group := range groups {
		outpoints := make([]string, 0, len(group.outpoints))
		for outpoint := range group.outpoints {
			outpoints = append(outpoints, outpoint)
		}
		if err := p.utxoLockerL1.EnsureReservation(outpoints, rgb11wallet.LockReasonPending,
			reservationID, group.previousReasons); err != nil {
			return err
		}
		active[reservationID] = group.outpoints
		if group.allSettled && len(group.changeOutpoints) != 0 {
			changes := make([]string, 0, len(group.changeOutpoints))
			for outpoint := range group.changeOutpoints {
				changes = append(changes, outpoint)
			}
			if err := p.utxoLockerL1.FinalizeReservation(changes, reservationID, rgb11wallet.LockReasonRGB); err != nil {
				return err
			}
		}
	}
	receiveReservations, err := p.rgbManager.projectionStore.ListReceiveReservations()
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, reservation := range receiveReservations {
		if reservation == nil || reservation.ReservationID == "" || reservation.OutPoint == "" ||
			(reservation.Expiry != 0 && reservation.Expiry <= now) {
			continue
		}
		previous := map[string]string{}
		if err := p.utxoLockerL1.EnsureReservation([]string{reservation.OutPoint},
			rgb11wallet.LockReasonPending, reservation.ReservationID, previous); err != nil {
			return err
		}
		active[reservation.ReservationID] = map[string]struct{}{reservation.OutPoint: {}}
	}
	stale := make(map[string][]string)
	for outpoint, lock := range p.utxoLockerL1.GetLockedUtxoList() {
		if lock == nil || lock.ReservationID == "" {
			continue
		}
		if values := active[lock.ReservationID]; values != nil {
			if _, ok := values[outpoint]; ok {
				continue
			}
		}
		stale[lock.ReservationID] = append(stale[lock.ReservationID], outpoint)
	}
	for reservationID, outpoints := range stale {
		if err := p.utxoLockerL1.ReleaseReservation(outpoints, reservationID); err != nil {
			return err
		}
	}
	return nil
}

func (p *rgb11Manager) rebuildRGB11Locks() error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	if p.utxoLockerL1 == nil {
		outputs, outputErr := p.rgbManager.projectionStore.ListOutputs()
		proofs, proofErr := p.rgbManager.projectionStore.ListProofs()
		transfers, transferErr := p.rgbManager.projectionStore.ListTransfers()
		reservations, reservationErr := p.rgbManager.projectionStore.ListReceiveReservations()
		if outputErr != nil || proofErr != nil || transferErr != nil || reservationErr != nil {
			p.rgbManager.consistencyStatus = "broken"
			return ErrRGB11Inconsistent
		}
		requiresLocker := len(outputs) != 0 || len(proofs) != 0 || len(reservations) != 0
		for _, state := range transfers {
			if rgb11TransferKeepsInputsLocked(state) || (state != nil && len(state.OutputOutPoints) != 0) {
				requiresLocker = true
				break
			}
		}
		if requiresLocker {
			p.rgbManager.consistencyStatus = "broken"
			return fmt.Errorf("%w: RGB11 UTXO locker is unavailable", ErrRGB11Inconsistent)
		}
		p.rgbManager.consistencyStatus = "ok"
		return nil
	}
	if err := p.reconcileRGB11Reservations(); err != nil {
		p.rgbManager.consistencyStatus = "broken"
		return err
	}
	expectedInputs, err := p.rgb11ExpectedInputs()
	if err != nil {
		p.rgbManager.consistencyStatus = "broken"
		return err
	}
	for outpoint := range expectedInputs {
		if err := p.utxoLockerL1.SetLockReason(outpoint, rgb11wallet.LockReasonPending); err != nil {
			p.rgbManager.consistencyStatus = "broken"
			return err
		}
	}
	expectedChanges, err := p.rgb11ExpectedChangeOutpoints()
	if err != nil {
		p.rgbManager.consistencyStatus = "broken"
		return err
	}
	for outpoint := range expectedChanges {
		if err := p.utxoLockerL1.SetLockReason(outpoint, rgb11wallet.LockReasonPending); err != nil {
			p.rgbManager.consistencyStatus = "broken"
			return err
		}
	}
	outputs, err := p.rgbManager.projectionStore.ListOutputs()
	if err != nil {
		p.rgbManager.consistencyStatus = "broken"
		return err
	}
	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		p.rgbManager.consistencyStatus = "broken"
		return err
	}
	proofIndex := make(map[string]*rgb11wallet.AllocationProof, len(proofs))
	for _, proof := range proofs {
		proofIndex[proof.OutPoint+"|"+proof.AssetName.String()] = proof
	}
	transfers, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		p.rgbManager.consistencyStatus = "broken"
		return err
	}
	requirements := rgb11ProofConfirmationRequirements(transfers)
	for _, output := range outputs {
		for _, asset := range output.Assets {
			if asset.Name.Protocol != rgb11wallet.Protocol {
				continue
			}
			proof := proofIndex[output.OutPointStr+"|"+asset.Name.String()]
			if proof == nil {
				_ = p.utxoLockerL1.SetLockReason(output.OutPointStr, rgb11wallet.LockReasonRGB)
				p.rgbManager.consistencyStatus = "broken"
				return fmt.Errorf("%w: proof missing for %s %s",
					ErrRGB11Inconsistent, output.OutPointStr, asset.Name.String())
			}
			if err := p.rgbManager.projectionStore.AssertConsistent(output.OutPointStr, asset.Name); err != nil {
				_ = p.utxoLockerL1.SetLockReason(output.OutPointStr, rgb11wallet.LockReasonRGB)
				p.rgbManager.consistencyStatus = "broken"
				return fmt.Errorf("%w: %v", ErrRGB11Inconsistent, err)
			}
			if proof.Status == "inconsistent" {
				_ = p.utxoLockerL1.SetLockReason(output.OutPointStr, rgb11wallet.LockReasonRGB)
				p.rgbManager.consistencyStatus = "broken"
				return fmt.Errorf("%w: RGB11 carrier %s is inconsistent",
					ErrRGB11Inconsistent, output.OutPointStr)
			}
			reason := rgb11wallet.LockReasonPending
			if _, spending := expectedInputs[output.OutPointStr]; !spending &&
				rgb11ProofIsAvailable(proof, requirements) {
				reason = rgb11wallet.LockReasonRGB
			}
			if err := p.utxoLockerL1.SetLockReason(output.OutPointStr, reason); err != nil {
				p.rgbManager.consistencyStatus = "broken"
				return err
			}
		}
	}
	p.rgbManager.consistencyStatus = "ok"
	return nil
}

func (p *rgb11Manager) RebuildRGB11Locks() error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	return p.rebuildRGB11Locks()
}

func (p *rgb11Manager) GetRGB11ConsistencyStatus() string {
	if p == nil || p.rgbManager == nil || p.rgbManager.consistencyStatus == "" {
		return "warning"
	}
	return p.rgbManager.consistencyStatus
}

func (p *rgb11Manager) ProjectRGB11Allocation(outpoint string, asset *indexer.AssetInfo, proof *rgb11wallet.AllocationProof) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || asset == nil || proof == nil || outpoint == "" {
		return rgb11wallet.ErrProjectionMismatch
	}
	if p.rgbManager.evidence == nil {
		return fmt.Errorf("RGB11 Bitcoin evidence provider is unavailable")
	}
	evidence, err := p.rgbManager.evidence.GetUTXO(outpoint)
	if err != nil {
		return err
	}
	if evidence == nil {
		return fmt.Errorf("%w: Bitcoin outpoint %s not found", ErrRGB11Inconsistent, outpoint)
	}
	output := indexer.NewTxOutput(evidence.Value)
	output.OutPointStr = outpoint
	output.OutValue.PkScript = append([]byte(nil), evidence.PkScript...)
	proof.Confirmations = evidence.Confirmations
	if proof.Confirmations > 0 {
		proof.Status = "settled"
	} else {
		proof.Status = "valid"
	}
	if err := p.rgbManager.projectionStore.CommitProjection(output, asset, proof); err != nil {
		return err
	}
	reason := rgb11wallet.LockReasonPending
	if proof.Confirmations > 0 {
		reason = rgb11wallet.LockReasonRGB
	}
	return p.utxoLockerL1.SetLockReason(outpoint, reason)
}

// getL1TxOutput is the mandatory composition point for transaction builders:
// public Bitcoin facts come from the Indexer, while locally validated RGB11
// allocations are overlaid from the wallet DB.
func (p *rgb11Manager) getL1TxOutput(outpoint string) (*TxOutput, error) {
	base, err := p.l1IndexerClient.GetTxOutput(outpoint)
	if err != nil || base == nil || p.rgbManager.projectionStore == nil {
		return base, err
	}
	projected, err := p.rgbManager.projectionStore.LoadOutput(outpoint)
	if err != nil {

		return base, nil
	}
	result := base.Clone()
	for _, asset := range projected.Assets {
		if asset.Name.Protocol != rgb11wallet.Protocol {
			continue
		}
		if err := p.rgbManager.projectionStore.AssertConsistent(outpoint, asset.Name); err != nil {
			_ = p.utxoLockerL1.LockUtxo(outpoint, rgb11wallet.LockReasonRGB)
			return nil, fmt.Errorf("%w: %v", ErrRGB11Inconsistent, err)
		}
		result.RemoveAsset(&asset.Name)
		if err := result.Assets.Add(&asset); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (p *rgb11Manager) ListRGB11Outputs() ([]*TxOutput, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgbManager.projectionStore.ListOutputs()
}

func (p *rgb11Manager) GetRGB11AssetBalance(name *indexer.AssetName) (*Decimal, error) {
	if name == nil || name.Protocol != rgb11wallet.Protocol || p.rgbManager.projectionStore == nil {
		return nil, rgb11wallet.ErrInvalidRGB11Asset
	}
	return p.rgbManager.projectionStore.Balance(*name)
}

func (p *rgb11Manager) RegisterRGB11TickerInfo(info *indexer.TickerInfo) error {
	if info == nil || info.AssetName.Protocol != rgb11wallet.Protocol {
		return rgb11wallet.ErrInvalidRGB11Asset
	}
	if err := validateRGB11TickerInfoName(info); err != nil {
		return err
	}
	if err := saveTickerInfo(p.db, info); err != nil {
		return err
	}
	p.mutex.Lock()
	p.tickerInfoMap[info.AssetName.String()] = info
	p.mutex.Unlock()
	return nil
}

// validateRGB11TickerInfoName binds imported RGB11 contract metadata to its
// canonical SAT20 asset name, including registry collision extensions.
func validateRGB11TickerInfoName(info *indexer.TickerInfo) error {
	var ext rgb11wallet.TickerExt
	if err := json.Unmarshal(info.Content, &ext); err != nil {
		return err
	}
	contractID := ext.ContractID
	if contractID == "" {
		contractID = ext.OriginalAssetID
	}
	if contractID == "" {
		return rgb11wallet.ErrInvalidRGB11Asset
	}
	if rgb11wallet.CanonicalAssetNameMatches(info.AssetName, contractID, ext.Ticker) {
		return nil
	}
	return rgb11wallet.ErrInvalidRGB11Asset
}

// RGB11Output is the serializable projection view exposed to UI clients.
// Internal TxOutput offset maps use structured keys and are intentionally not
// part of this API.

// RGB11State exposes existing SAT20 assets plus RGB-only proof sidecars.
// Assets is rebuilt from Outputs and is never a second writable balance ledger.

func (p *rgb11Manager) GetRGB11State() (*RGB11State, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	outputs, err := p.rgbManager.projectionStore.ListOutputs()
	if err != nil {
		return nil, err
	}
	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return nil, err
	}
	transfers, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return nil, err
	}
	proofIndex := make(map[string]*rgb11wallet.AllocationProof, len(proofs))
	for _, proof := range proofs {
		proofIndex[fmt.Sprintf("%s|%s", proof.OutPoint, proof.AssetName.String())] = proof
	}
	requirements := rgb11ProofConfirmationRequirements(transfers)
	reservedInputs := rgb11ReservedInputOutpoints(transfers)

	var assets indexer.TxAssets
	var availableAssets indexer.TxAssets
	var pendingAssets indexer.TxAssets
	stateOutputs := make([]*RGB11Output, 0, len(outputs))
	for _, output := range outputs {
		stateOutputs = append(stateOutputs, &RGB11Output{
			OutPointStr: output.OutPointStr,
			Assets:      output.Assets.Clone(),
		})
		for index := range output.Assets {
			asset := &output.Assets[index]
			if asset.Name.Protocol != rgb11wallet.Protocol {
				continue
			}
			proof := proofIndex[fmt.Sprintf("%s|%s", output.OutPointStr, asset.Name.String())]
			if proof == nil {
				return nil, fmt.Errorf("%w: proof missing for %s %s", ErrRGB11Inconsistent, output.OutPointStr, asset.Name.String())
			}
			if !rgb11ProofIsCurrent(proof) {
				continue
			}
			if err := assets.Add(asset); err != nil {
				return nil, err
			}
			target := &pendingAssets
			if !reservedInputs[proof.OutPoint] && rgb11ProofIsAvailable(proof, requirements) {
				target = &availableAssets
			}
			if err := target.Add(asset); err != nil {
				return nil, err
			}
		}
	}

	tickers := make([]*RGB11TickerInfo, 0)
	for index := range assets {
		info := p.getTickerInfo(&assets[index].Name)
		if info == nil {
			continue
		}
		ticker, canonicalName, contractID, fingerprint, verified := p.rgb11TickerPresentation(info)
		tickers = append(tickers, &RGB11TickerInfo{
			TickerInfo:    info,
			Ticker:        ticker,
			CanonicalName: canonicalName,
			ContractID:    contractID,
			Fingerprint:   fingerprint,
			Verified:      verified,
		})
	}

	return &RGB11State{
		Initialized:       true,
		SyncStatus:        p.rgb11ScopeState().ReconciliationState,
		ConsistencyStatus: p.GetRGB11ConsistencyStatus(),
		TickerInfos:       tickers,
		Assets:            assets,
		AvailableAssets:   availableAssets,
		PendingAssets:     pendingAssets,
		Outputs:           stateOutputs,
		Proofs:            proofs,
		Transfers:         transfers,
	}, nil
}

func (p *rgb11Manager) rgb11CarrierBinding(allocation rgb11wallet.ValidatedAllocation,
	utxo *rgb11wallet.BitcoinUTXO) (*rgb11wallet.CarrierBinding, error) {
	return p.rgb11CarrierBindingWithReceiveKey(allocation, utxo, nil)
}

func (p *rgb11Manager) rgb11CarrierBindingForRequest(allocation rgb11wallet.ValidatedAllocation,
	utxo *rgb11wallet.BitcoinUTXO, request *corewallet.ReceiveRequest) (*rgb11wallet.CarrierBinding, error) {
	if request == nil || len(request.WitnessScript) == 0 {
		return p.rgb11CarrierBinding(allocation, utxo)
	}
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, err
	}
	// Fixed-address witness invoices do not need a persisted ReceiveKey.
	// Older independently derived invoices continue through the compatibility
	// branch below and retain their change=1 signing path.
	if bytes.Equal(request.WitnessScript, walletScript) {
		return p.rgb11CarrierBinding(allocation, utxo)
	}
	key, err := p.rgbManager.projectionStore.LoadReceiveKey(request.WitnessScript)
	if err != nil {
		return nil, err
	}
	if key.RequestID != request.RequestID {
		return nil, ErrRGB11InvoiceMismatch
	}
	return p.rgb11CarrierBindingWithReceiveKey(allocation, utxo, key)
}

func (p *rgb11Manager) rgb11CarrierBindingWithReceiveKey(allocation rgb11wallet.ValidatedAllocation,
	utxo *rgb11wallet.BitcoinUTXO, receiveKey *rgb11wallet.ReceiveKey) (*rgb11wallet.CarrierBinding, error) {
	if p == nil || p.wallet == nil || utxo == nil || allocation.OutPoint != utxo.OutPoint {
		return nil, rgb11wallet.ErrInvalidProof
	}
	method := allocation.CommitmentMethod
	if method == "" {
		method = "genesis"
	}
	derivationIndex := p.wallet.GetSubAccount()
	logicalAddress := p.wallet.GetAddress()
	internalPubKey := append([]byte(nil), allocation.CarrierInternalKey...)
	if receiveKey != nil {
		if receiveKey.ScopeIndex != p.wallet.GetSubAccount() ||
			receiveKey.LogicalAddress != logicalAddress ||
			!bytes.Equal(receiveKey.WitnessScript, utxo.PkScript) {
			return nil, ErrRGB11InvoiceMismatch
		}
		switch receiveKey.Change {
		case 0:
			if receiveKey.Index != p.wallet.GetSubAccount() {
				return nil, ErrRGB11InvoiceMismatch
			}
			derivationIndex = receiveKey.Index
		case 1:
			derivationIndex = rgb11InternalReceiveIndexFlag | receiveKey.Index
		default:
			return nil, ErrRGB11InvoiceMismatch
		}
		internalPubKey = append([]byte(nil), receiveKey.InternalPubKey...)
	}
	if logicalAddress == "" {
		return nil, ErrRGB11WalletLocked
	}
	binding := &rgb11wallet.CarrierBinding{
		DerivationIndex:  derivationIndex,
		LogicalAddress:   logicalAddress,
		OutPoint:         allocation.OutPoint,
		ActualPkScript:   append([]byte(nil), utxo.PkScript...),
		InternalPubKey:   internalPubKey,
		TapretRoot:       append([]byte(nil), allocation.TapretRoot...),
		TapretProof:      append([]byte(nil), allocation.TapretProof...),
		CommitmentMethod: method,
	}
	if txscript.IsPayToTaproot(utxo.PkScript) && len(utxo.PkScript) == 34 {
		binding.ActualOutputKey = append([]byte(nil), utxo.PkScript[2:]...)
	}
	if method == "tapret1st" {
		if len(binding.InternalPubKey) != 32 || len(binding.TapretRoot) != 32 || len(binding.ActualOutputKey) != 32 {
			return nil, rgb11wallet.ErrInvalidProof
		}
		internal, err := btcec.ParsePubKey(append([]byte{0x02}, binding.InternalPubKey...))
		if err != nil {
			return nil, err
		}
		output := txscript.ComputeTaprootOutputKey(internal, binding.TapretRoot)
		expected, err := txscript.PayToTaprootScript(output)
		if err != nil || !bytes.Equal(expected, binding.ActualPkScript) {
			return nil, fmt.Errorf("RGB11 Tapret carrier binding does not match %s", allocation.OutPoint)
		}
	}
	return binding, nil
}

func (p *rgb11Manager) ownsRGB11Carrier(binding *rgb11wallet.CarrierBinding, walletScript []byte) bool {
	if p == nil || p.wallet == nil || binding == nil {
		return false
	}
	if binding.LogicalAddress == "" || binding.LogicalAddress != p.wallet.GetAddress() {
		return false
	}
	change, index := rgb11CarrierPath(binding.DerivationIndex)
	var pubkey *secp256k1.PublicKey
	if change == 0 {
		if index != p.wallet.GetSubAccount() ||
			binding.LogicalAddress != p.wallet.GetAddressByIndex(index) {
			return false
		}
		pubkey = p.wallet.GetPubKeyByIndex(index)
	} else {
		pathWallet, ok := p.wallet.(rgb11PathWallet)
		if !ok {
			return false
		}
		pubkey = pathWallet.GetPubKeyByPath(change, index)
	}
	if pubkey == nil {
		return false
	}
	if binding.CommitmentMethod != "tapret1st" {
		expected, err := GetP2TRpkScript(pubkey)
		return err == nil && bytes.Equal(expected, binding.ActualPkScript)
	}
	compressed := pubkey.SerializeCompressed()
	return len(compressed) == 33 && bytes.Equal(binding.InternalPubKey, compressed[1:])
}

// rgb11BitcoinEvidenceRPC is intentionally separate from IndexerRPCClient.
// Production clients implement it with the /v3/bitcoin evidence endpoints;
// legacy test doubles can continue using the narrow compatibility fallback.
type rgb11BitcoinEvidenceRPC interface {
	GetBitcoinUTXOStatus(outpoint string) (*indexerwire.BitcoinUTXOStatus, error)
	GetBitcoinRawTx(txid string) (*indexerwire.BitcoinRawTx, error)
	GetBitcoinTxStatus(txid string) (*indexerwire.BitcoinTxStatus, error)
	GetBitcoinOutspend(outpoint string) (*indexerwire.BitcoinOutspend, error)
	GetBitcoinTip() (*indexerwire.BitcoinTip, error)
	BroadcastBitcoinTx(rawTx []byte) (string, error)
}

// indexerBitcoinEvidenceProvider is the single Wallet-side adapter for public
// Bitcoin facts. RGB11 validation never treats Indexer asset projections as
// authoritative state.
type indexerBitcoinEvidenceProvider struct {
	client IndexerRPCClient
}

func newIndexerBitcoinEvidenceProvider(client IndexerRPCClient) rgb11wallet.BitcoinEvidenceProvider {
	return &indexerBitcoinEvidenceProvider{client: client}
}

func (p *indexerBitcoinEvidenceProvider) GetUTXO(outpoint string) (*rgb11wallet.BitcoinUTXO, error) {
	if client, ok := p.client.(rgb11BitcoinEvidenceRPC); ok {
		status, err := client.GetBitcoinUTXOStatus(outpoint)
		if err != nil {
			return nil, err
		}
		if status == nil || !status.Exists || !status.Unspent {
			return nil, fmt.Errorf("Bitcoin outpoint %s is not an unspent output", outpoint)
		}
		script, err := hex.DecodeString(status.PkScript)
		if err != nil {
			return nil, err
		}
		return &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: status.Value, PkScript: script,
			Confirmations: status.Confirmations,
		}, nil
	}
	output, err := p.client.GetTxOutput(outpoint)
	if err != nil {
		return nil, err
	}
	if output == nil {
		return nil, fmt.Errorf("Bitcoin outpoint %s not found", outpoint)
	}
	confirmations := int64(0)
	parts := strings.Split(outpoint, ":")
	if len(parts) == 2 {
		if status, statusErr := p.GetTxStatus(parts[0]); statusErr == nil {
			confirmations = status.Confirmations
		}
	}
	return &rgb11wallet.BitcoinUTXO{
		OutPoint:      outpoint,
		Value:         output.OutValue.Value,
		PkScript:      append([]byte(nil), output.OutValue.PkScript...),
		Confirmations: confirmations,
	}, nil
}

func (p *indexerBitcoinEvidenceProvider) GetRawTx(txid string) ([]byte, error) {
	if client, ok := p.client.(rgb11BitcoinEvidenceRPC); ok {
		item, err := client.GetBitcoinRawTx(txid)
		if err != nil {
			return nil, err
		}
		return hex.DecodeString(item.RawTx)
	}
	raw, err := p.client.GetRawTx(txid)
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(raw)
}

func (p *indexerBitcoinEvidenceProvider) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if client, ok := p.client.(rgb11BitcoinEvidenceRPC); ok {
		item, err := client.GetBitcoinTxStatus(txid)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return &rgb11wallet.BitcoinTxStatus{TxID: txid}, nil
		}
		return &rgb11wallet.BitcoinTxStatus{
			TxID: item.TxID, InMempool: item.InMempool, Confirmed: item.Confirmed,
			BlockHeight: item.BlockHeight, BlockHash: item.BlockHash,
			Confirmations: item.Confirmations,
		}, nil
	}
	info, err := p.client.GetTxInfo(txid)
	if err != nil {
		return nil, err
	}
	status := &rgb11wallet.BitcoinTxStatus{
		TxID:          txid,
		InMempool:     info.Confirmations == 0,
		Confirmed:     info.Confirmations > 0,
		BlockHeight:   info.BlockHeight,
		Confirmations: int64(info.Confirmations),
	}
	if status.Confirmed {
		status.BlockHash, err = p.client.GetBlockHash(int(info.BlockHeight))
		if err != nil {
			return nil, err
		}
	}
	return status, nil
}

func (p *indexerBitcoinEvidenceProvider) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	if client, ok := p.client.(rgb11BitcoinEvidenceRPC); ok {
		item, err := client.GetBitcoinOutspend(outpoint)
		if err != nil {
			return nil, err
		}
		if item == nil || !item.Exists {
			return nil, fmt.Errorf("Bitcoin outpoint %s does not exist", outpoint)
		}
		return &rgb11wallet.BitcoinOutspend{Spent: item.Spent, SpendingTx: item.SpendingTx}, nil
	}
	txid, err := p.client.GetUtxoSpentTx(outpoint)
	if err != nil {
		return nil, err
	}
	return &rgb11wallet.BitcoinOutspend{Spent: txid != "", SpendingTx: txid}, nil
}

func (p *indexerBitcoinEvidenceProvider) GetTip() (*rgb11wallet.BitcoinTip, error) {
	if client, ok := p.client.(rgb11BitcoinEvidenceRPC); ok {
		item, err := client.GetBitcoinTip()
		if err != nil {
			return nil, err
		}
		return &rgb11wallet.BitcoinTip{Height: item.Height, BlockHash: item.BlockHash}, nil
	}
	height := p.client.GetBestHeight()
	if height < 0 {
		return nil, fmt.Errorf("Bitcoin tip is unavailable")
	}
	hash, err := p.client.GetBlockHash(int(height))
	if err != nil {
		return nil, err
	}
	return &rgb11wallet.BitcoinTip{Height: height, BlockHash: hash}, nil
}

func (p *indexerBitcoinEvidenceProvider) Broadcast(rawTx []byte) (string, error) {
	if client, ok := p.client.(rgb11BitcoinEvidenceRPC); ok {
		return client.BroadcastBitcoinTx(rawTx)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(rawTx)); err != nil {
		return "", err
	}
	return p.client.BroadCastTx(tx)
}

var (
	ErrRGB11IssueUTXOUnavailable = errors.New("not enough confirmed plain Bitcoin UTXOs for RGB11 issuance")
	ErrRGB11IFAMainnet           = errors.New("IFA issuance is disabled on Bitcoin mainnet by the frozen wallet API")
)

// UnmarshalJSON accepts atomic u64 amounts as either JSON numbers or decimal
// strings. PWA callers use strings so values above JavaScript's safe-integer
// range remain exact.

// IssueRGB11Asset selects wallet-owned confirmed plain UTXOs, creates a
// canonical standard-schema genesis, validates it against Bitcoin evidence,
// and imports its allocations into the native wallet projection.
func (p *rgb11Manager) IssueRGB11Asset(ctx context.Context, request RGB11IssueRequest) (*RGB11IssueResult, error) {
	started := time.Now()
	defer func() {
		Log.Infof("RGB11 issue finished in %v", time.Since(started))
	}()
	if p == nil || p.wallet == nil || p.l1IndexerClient == nil || p.rgbManager.evidence == nil || p.utxoLockerL1 == nil {
		return nil, ErrRGB11Inconsistent
	}
	kind, err := parseRGB11IssueSchema(request.Schema)
	if err != nil {
		return nil, err
	}
	params := GetChainParam()
	if kind == schemas.IFA && params.Net == chaincfg.MainNetParams.Net {
		return nil, ErrRGB11IFAMainnet
	}
	if request.MinConfirmations <= 0 {
		request.MinConfirmations = 1
	}
	amountCount := len(request.Amounts) + len(request.InflationAmounts)
	if kind == schemas.UDA {
		if len(request.Amounts) == 0 {
			request.Amounts = []uint64{1}
		}
		amountCount = len(request.Amounts)
	}
	phaseStarted := time.Now()
	selected, err := p.selectRGB11IssueOutpoints(amountCount, request.MinConfirmations)
	Log.Infof("RGB11 issue UTXO selection finished in %v (selected=%d, err=%v)", time.Since(phaseStarted), len(selected), err)
	if err != nil {
		return nil, err
	}
	phaseStarted = time.Now()
	allocations, err := rgb11IssueAllocations(selected[:len(request.Amounts)], request.Amounts)
	if err != nil {
		return nil, err
	}
	inflation, err := rgb11IssueAllocations(selected[len(request.Amounts):], request.InflationAmounts)
	if err != nil {
		return nil, err
	}
	Log.Infof("RGB11 issue allocation build finished in %v", time.Since(phaseStarted))
	phaseStarted = time.Now()
	issued, err := coreissuance.Issue(coreissuance.Spec{
		Kind: kind, Network: rgb11IssuanceNetwork(params),
		Ticker: request.Ticker, Name: request.Name, Details: request.Details,
		Precision: request.Precision, Terms: request.Terms,
		Allocations: allocations, InflationRights: inflation, RejectListURL: request.RejectListURL,
	})
	Log.Infof("RGB11 issue contract build finished in %v (err=%v)", time.Since(phaseStarted), err)
	if err != nil {
		return nil, err
	}
	phaseStarted = time.Now()
	imported, err := p.ImportRGB11Contract(ctx, []byte(issued.Armor))
	Log.Infof("RGB11 issue contract import finished in %v (err=%v)", time.Since(phaseStarted), err)
	if err != nil {
		return nil, err
	}
	if imported.Projected != len(selected) {
		return nil, fmt.Errorf("%w: issued %d allocations but projected %d", ErrRGB11Inconsistent, len(selected), imported.Projected)
	}
	if issued.Container == nil || issued.Container.Armor == nil || len(issued.Container.Armor.Data) == 0 {
		return nil, fmt.Errorf("%w: issued contract has no strict consignment", ErrRGB11Inconsistent)
	}
	contractFile, err := coreconsignment.EncodeFile(issued.Container)
	if err != nil {
		return nil, err
	}
	return &RGB11IssueResult{
		ContractID: imported.ContractID, SchemaID: imported.SchemaID, AssetName: imported.AssetName,
		Armor:                     issued.Armor,
		ContractConsignmentBase64: base64.StdEncoding.EncodeToString(contractFile),
		OutPoints:                 selected, Receipt: imported.Receipt, Projected: imported.Projected,
	}, nil
}

func parseRGB11IssueSchema(value string) (schemas.Kind, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "NIA":
		return schemas.NIA, nil
	case "IFA":
		return schemas.IFA, nil
	case "UDA":
		return schemas.UDA, nil
	default:
		return "", coreissuance.ErrUnsupportedSchema
	}
}

func (p *rgb11Manager) selectRGB11IssueOutpoints(count int, minConfirmations int64) ([]string, error) {
	if count <= 0 {
		return nil, coreissuance.ErrInvalidSpec
	}
	address := p.wallet.GetAddress()
	walletScript, err := AddrToPkScript(address, GetChainParam())
	if err != nil {
		return nil, err
	}
	candidates := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].OutPoint < candidates[j].OutPoint })
	reservedChanges, err := p.rgb11ExpectedChangeOutpoints()
	if err != nil {
		return nil, err
	}
	p.utxoLockerL1.Reload(address)
	selected := make([]string, 0, count)
	for _, candidate := range candidates {
		if candidate == nil || candidate.OutPoint == "" || p.utxoLockerL1.IsLocked(candidate.OutPoint) ||
			reservedChanges[candidate.OutPoint] != "" {
			continue
		}
		if _, err := p.rgbManager.projectionStore.LoadOutput(candidate.OutPoint); err == nil {
			continue
		} else if !errors.Is(err, indexer.ErrKeyNotFound) {
			return nil, err
		}
		evidence, err := p.rgbManager.evidence.GetUTXO(candidate.OutPoint)
		if err != nil || evidence == nil {
			continue
		}
		if evidence.Confirmations < minConfirmations || !bytes.Equal(evidence.PkScript, walletScript) {
			continue
		}
		if _, err := wire.NewOutPointFromString(candidate.OutPoint); err != nil {
			continue
		}
		selected = append(selected, candidate.OutPoint)
		if len(selected) == count {
			return selected, nil
		}
	}
	return nil, fmt.Errorf("%w: need %d, found %d", ErrRGB11IssueUTXOUnavailable, count, len(selected))
}

func rgb11IssueAllocations(outpoints []string, amounts []uint64) ([]coreissuance.Allocation, error) {
	if len(outpoints) != len(amounts) {
		return nil, coreissuance.ErrInvalidSpec
	}
	allocations := make([]coreissuance.Allocation, 0, len(outpoints))
	for index, outpointText := range outpoints {
		outpoint, err := wire.NewOutPointFromString(outpointText)
		if err != nil {
			return nil, err
		}
		var entropy [8]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return nil, err
		}
		seal, err := seals.NewGraphBlindSeal(outpoint.Hash[:], outpoint.Index, binary.LittleEndian.Uint64(entropy[:]))
		if err != nil {
			return nil, err
		}
		allocations = append(allocations, coreissuance.Allocation{Seal: seal, Amount: amounts[index]})
	}
	return allocations, nil
}

func rgb11IssuanceNetwork(params *chaincfg.Params) coreissuance.ChainNet {
	if params != nil && params.Net == chaincfg.MainNetParams.Net {
		return coreissuance.BitcoinMainnet
	}
	if params != nil && params.Net == chaincfg.TestNet3Params.Net {
		return coreissuance.BitcoinTestnet3
	}
	if params != nil && params.Net == chaincfg.RegressionNetParams.Net {
		return coreissuance.BitcoinRegtest
	}
	if params != nil && params.Net == chaincfg.SigNetParams.Net {
		return coreissuance.BitcoinSignet
	}
	return coreissuance.BitcoinTestnet4
}

// ImportRGB11Contract validates a complete contract consignment and imports
// only revealed allocations whose Bitcoin output is controlled by the active
// wallet. The Indexer contributes UTXO facts only; it never contributes RGB
// balances or allocation state.
func (p *rgb11Manager) ImportRGB11Contract(ctx context.Context, raw []byte) (*RGB11ImportResult, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil || p.wallet == nil {
		return nil, ErrRGB11Inconsistent
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return nil, err
	}
	return p.importRGB11Contract(ctx, raw, container)
}

func (p *rgb11Manager) ImportRGB11ContractFile(ctx context.Context, raw []byte) (*RGB11ImportResult, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil || p.wallet == nil {
		return nil, ErrRGB11Inconsistent
	}
	container, err := coreconsignment.DecodeFile(raw)
	if err != nil {
		return nil, err
	}
	return p.importRGB11Contract(ctx, raw, container)
}

func (p *rgb11Manager) importRGB11Contract(ctx context.Context, raw []byte,
	container *coreconsignment.Container) (*RGB11ImportResult, error) {
	if container.Armor == nil || container.Armor.Type != "contract" {
		return nil, fmt.Errorf("RGB11 import requires a contract consignment")
	}
	receipt, err := p.rgbManager.projectionStore.ValidateAndStoreConsignment(
		ctx, rgb11wallet.NewNativeConsensusValidator(), p.rgbManager.evidence, raw,
	)
	if err != nil {
		return nil, err
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return nil, err
	}
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, err
	}

	projected := 0
	for _, allocation := range receipt.Allocations {
		utxo, err := p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
		if err != nil {
			return nil, err
		}
		if utxo == nil {
			continue
		}
		binding, err := p.rgb11CarrierBinding(allocation, utxo)
		if err != nil {
			return nil, err
		}
		if !p.ownsRGB11Carrier(binding, walletScript) {
			continue
		}
		commitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, allocation.SealDisclosure)
		asset := &indexer.AssetInfo{
			Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0,
		}
		proof := &rgb11wallet.AllocationProof{
			OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
			OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType,
			AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
			StateData:       append([]byte(nil), allocation.StateData...),
			SealCommitment:  hex.EncodeToString(commitment[:]),
			SealDisclosure:  append([]byte(nil), allocation.SealDisclosure...),
			ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
			WitnessTxID: allocationOutpointTxID(allocation.OutPoint), Status: "valid",
			CarrierBinding: binding,
		}
		if err := p.ProjectRGB11Allocation(allocation.OutPoint, asset, proof); err != nil {
			return nil, err
		}
		projected++
	}

	info, err := rgb11TickerInfoFromValidatedContract(container, receipt)
	if err != nil {
		return nil, err
	}
	if err := p.RegisterRGB11TickerInfo(info); err != nil {
		return nil, err
	}
	result := &RGB11ImportResult{
		ContractID: container.ContractID, SchemaID: container.SchemaID,
		AssetName: info.AssetName, Receipt: receipt, Projected: projected,
	}
	p.autoBackupRGB11AfterMutation()
	return result, nil
}

func rgb11TickerInfoFromValidatedContract(container *coreconsignment.Container,
	receipt *rgb11wallet.ValidationReceipt) (*indexer.TickerInfo, error) {

	if container == nil || receipt == nil || container.ContractID == "" ||
		container.ContractID != receipt.ContractID || container.SchemaID != receipt.SchemaID {
		return nil, rgb11wallet.ErrValidationReceipt
	}
	descriptor, err := schemas.ByKind(container.GenesisReport.Kind)
	if err != nil {
		return nil, err
	}
	assetType := indexer.ASSET_TYPE_FT
	if !descriptor.Fungible {
		assetType = indexer.ASSET_TYPE_NFT
	}
	schemaValue, _ := container.Value.Field("schema")
	typeSystem, _ := container.Value.Field("types")
	genesisValue, _ := container.Value.Field("genesis")
	metadata, err := schemas.ExtractGenesisAssetMetadata(schemaValue, typeSystem, genesisValue)
	if err != nil {
		return nil, err
	}
	assetName, err := rgb11wallet.NewCanonicalAssetName(container.ContractID, metadata.Ticker, assetType)
	if err != nil {
		return nil, err
	}
	fingerprint, err := rgb11wallet.ContractFingerprint(container.ContractID, rgb11wallet.DefaultFingerprintLength)
	if err != nil {
		return nil, err
	}
	ext := rgb11wallet.TickerExt{
		AssetName: assetName, Ticker: metadata.Ticker,
		CanonicalName: assetName.String(), NormalizedTicker: rgb11wallet.NormalizeTicker(metadata.Ticker),
		Fingerprint: fingerprint, DisplayTicker: rgb11wallet.DisplayTicker(metadata.Ticker, fingerprint, false),
		OriginalAssetID: container.ContractID,
		SchemaID:        container.SchemaID, ContractID: container.ContractID,
		ContractHash: receipt.ConsignmentHash, RejectListURL: metadata.RejectListURL,
		ControlMode: descriptor.DefaultControlMode,
		STPAllowed:  false, ValidationStatus: "valid",
	}
	extContent, err := json.Marshal(ext)
	if err != nil {
		return nil, err
	}
	return &indexer.TickerInfo{
		AssetName: assetName, DisplayName: metadata.DisplayName, Divisibility: int(metadata.Precision),
		DeployTx:    allocationOutpointTxID(firstAllocationOutpoint(receipt.Allocations)),
		TotalMinted: fmt.Sprintf("%d", metadata.IssuedSupply), MaxSupply: fmt.Sprintf("%d", metadata.MaxSupply), Content: extContent,
	}, nil
}

func (p *rgb11Manager) rgb11TickerPresentation(info *indexer.TickerInfo) (ticker, canonicalName, contractID, fingerprint string, verified bool) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || info == nil {
		return "", "", "", "", false
	}
	var ext rgb11wallet.TickerExt
	if json.Unmarshal(info.Content, &ext) != nil {
		return "", info.AssetName.String(), "", "", false
	}
	contractID = ext.ContractID
	if contractID == "" {
		contractID = ext.OriginalAssetID
	}
	canonicalName = ext.CanonicalName
	if canonicalName == "" {
		canonicalName = info.AssetName.String()
	}
	fingerprint = ext.Fingerprint
	verified = ext.PrimaryVerified
	if ext.DisplayTicker != "" {
		return ext.DisplayTicker, canonicalName, contractID, fingerprint, verified
	}
	if ext.Ticker != "" {
		if fingerprint == "" && contractID != "" {
			fingerprint, _ = rgb11wallet.ContractFingerprint(contractID, rgb11wallet.DefaultFingerprintLength)
		}
		return rgb11wallet.DisplayTicker(ext.Ticker, fingerprint, verified), canonicalName, contractID, fingerprint, verified
	}
	if ext.ContractHash == "" {
		return "", canonicalName, contractID, fingerprint, verified
	}
	raw, err := p.rgbManager.projectionStore.LoadObject(ext.ContractHash)
	if err != nil {
		return "", canonicalName, contractID, fingerprint, verified
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return "", canonicalName, contractID, fingerprint, verified
	}
	schemaValue, _ := container.Value.Field("schema")
	typeSystem, _ := container.Value.Field("types")
	genesisValue, _ := container.Value.Field("genesis")
	metadata, err := schemas.ExtractGenesisAssetMetadata(schemaValue, typeSystem, genesisValue)
	if err != nil {
		return "", canonicalName, contractID, fingerprint, verified
	}
	if fingerprint == "" && contractID != "" {
		fingerprint, _ = rgb11wallet.ContractFingerprint(contractID, rgb11wallet.DefaultFingerprintLength)
	}
	return rgb11wallet.DisplayTicker(metadata.Ticker, fingerprint, verified), canonicalName, contractID, fingerprint, verified
}

// rgb11ContractIDForAssetName resolves the full RGB contract id via local
// contract metadata. A canonical SAT20 name deliberately cannot be reversed
// into a contract id.
func (p *rgb11Manager) rgb11ContractIDForAssetName(name indexer.AssetName) (string, error) {
	if p == nil || name.Protocol != rgb11wallet.Protocol {
		return "", rgb11wallet.ErrInvalidRGB11Asset
	}
	p.mutex.RLock()
	info := p.tickerInfoMap[name.String()]
	p.mutex.RUnlock()
	if info != nil {
		var ext rgb11wallet.TickerExt
		if err := json.Unmarshal(info.Content, &ext); err != nil {
			return "", err
		}
		contractID := ext.ContractID
		if contractID == "" {
			contractID = ext.OriginalAssetID
		}
		if contractID != "" {
			if rgb11wallet.CanonicalAssetNameMatches(name, contractID, ext.Ticker) {
				return contractID, nil
			}
		}
	}
	return "", rgb11wallet.ErrInvalidRGB11Asset
}

func allocationOutpointTxID(outpoint string) string {
	for index := len(outpoint) - 1; index >= 0; index-- {
		if outpoint[index] == ':' {
			return outpoint[:index]
		}
	}
	return ""
}

func firstAllocationOutpoint(allocations []rgb11wallet.ValidatedAllocation) string {
	if len(allocations) == 0 {
		return ""
	}
	return allocations[0].OutPoint
}

const (
	RGB11RejectReasonList = "reject-list"
	RGB11RejectReasonUser = "user-rejected"
)

var (
	ErrRGB11RejectListUnavailable = errors.New("RGB11 reject list is unavailable")
	ErrRGB11Rejected              = rgb11wallet.ErrRGB11Rejected
)

// RGB11RejectListProvider makes the network policy injectable for deterministic
// wallet tests. The default implementation permits plain HTTP only on loopback
// while the wallet is configured for regtest.

func (p *rgb11Manager) rgb11RejectListProvider() RGB11RejectListProvider {
	if p != nil && p.rgbManager != nil && p.rgbManager.rejectLists != nil {
		return p.rgbManager.rejectLists
	}
	params := GetChainParam()
	allowLoopback := params != nil && params.Net == chaincfg.RegressionNetParams.Net
	return rejectlist.Client{AllowLoopbackHTTP: allowLoopback}
}

func rgb11ContainerRejectListURL(container *coreconsignment.Container) (string, error) {
	if container == nil {
		return "", coreconsignment.ErrContainerType
	}
	schema, okSchema := container.Value.Field("schema")
	types, okTypes := container.Value.Field("types")
	genesis, okGenesis := container.Value.Field("genesis")
	if !okSchema || !okTypes || !okGenesis {
		return "", coreconsignment.ErrContainerType
	}
	metadata, err := schemas.ExtractGenesisAssetMetadata(schema, types, genesis)
	if err != nil {
		return "", err
	}
	return metadata.RejectListURL, nil
}

func (p *rgb11Manager) checkRGB11RejectPolicy(container *coreconsignment.Container,
	checked []operations.Opout) error {
	url, err := rgb11ContainerRejectListURL(container)
	if err != nil || url == "" {
		return err
	}
	list, err := p.rgb11RejectListProvider().Fetch(url)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRGB11RejectListUnavailable, err)
	}
	dag, err := container.OperationDAG()
	if err != nil {
		return err
	}
	for _, opout := range checked {
		if rejected, ok := list.RejectedAncestor(opout, dag); ok {
			return &RGB11RejectListViolation{Checked: opout, Rejected: rejected}
		}
	}
	return nil
}

func rgb11ProofOpout(proof *rgb11wallet.AllocationProof) (operations.Opout, error) {
	if proof == nil || proof.AssignmentType > 0xffff || proof.AssignmentIndex > 0xffff {
		return operations.Opout{}, rgb11wallet.ErrInvalidProof
	}
	operationID, err := hex.DecodeString(proof.OperationID)
	if err != nil || len(operationID) != 32 {
		return operations.Opout{}, rgb11wallet.ErrInvalidProof
	}
	return operations.ParseOpout(fmt.Sprintf("%s/%d/%d", proof.OperationID, proof.AssignmentType, proof.AssignmentIndex))
}

var ErrRGB11WalletLocked = errors.New("RGB11 wallet must be unlocked")

const rgb11InternalReceiveIndexFlag uint32 = 1 << 31

type rgb11PathWallet interface {
	GetPubKeyByPath(change, index uint32) *secp256k1.PublicKey
	GetAddressByPath(change, index uint32) string
	SignRGB11Psbt(*psbt.Packet, map[int]RGB11InputSigningKey) error
}

func rgb11CarrierPath(locator uint32) (change, index uint32) {
	if locator&rgb11InternalReceiveIndexFlag != 0 {
		return 1, locator &^ rgb11InternalReceiveIndexFlag
	}
	return 0, locator
}

func (p *rgb11Manager) newRGB11ReceiveKey() (*rgb11wallet.ReceiveKey, error) {
	pathWallet, ok := p.wallet.(rgb11PathWallet)
	if !ok {
		return nil, errors.New("wallet does not support independent RGB11 receive keys")
	}
	logicalAddress := p.wallet.GetAddress()
	if logicalAddress == "" {
		return nil, ErrRGB11WalletLocked
	}
	for attempt := 0; attempt < 16; attempt++ {
		var entropy [4]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return nil, err
		}
		index := binary.BigEndian.Uint32(entropy[:]) &^ rgb11InternalReceiveIndexFlag
		pubkey := pathWallet.GetPubKeyByPath(1, index)
		address := pathWallet.GetAddressByPath(1, index)
		if pubkey == nil || address == "" {
			return nil, errors.New("derive independent RGB11 receive key")
		}
		script, err := AddrToPkScript(address, GetChainParam())
		if err != nil {
			return nil, err
		}
		if _, err := p.rgbManager.projectionStore.LoadReceiveKey(script); err == nil {
			continue
		} else if !errors.Is(err, indexer.ErrKeyNotFound) {
			return nil, err
		}
		compressed := pubkey.SerializeCompressed()
		return &rgb11wallet.ReceiveKey{
			Version: 1, ScopeIndex: p.wallet.GetSubAccount(), Change: 1, Index: index,
			LogicalAddress: logicalAddress, WitnessScript: script,
			InternalPubKey: append([]byte(nil), compressed[1:]...),
		}, nil
	}
	return nil, errors.New("allocate independent RGB11 receive key")
}

func (p *rgb11Manager) newRGB11BlindReceiveSeal() (*seals.GraphBlindSeal, string, error) {
	selected, err := p.selectRGB11IssueOutpoints(1, 1)
	if err != nil || len(selected) != 1 {
		return nil, "", fmt.Errorf("standard RGB11 blind receive requires one confirmed plain UTXO: %w", err)
	}
	outpoint, err := wire.NewOutPointFromString(selected[0])
	if err != nil {
		return nil, "", err
	}
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return nil, "", err
	}
	seal, err := seals.NewGraphBlindSeal(
		outpoint.Hash[:], outpoint.Index, binary.LittleEndian.Uint64(entropy[:]),
	)
	if err != nil {
		return nil, "", err
	}
	return &seal, selected[0], nil
}

func releaseRGB11ReceiveReservationValue(locker *UtxoLocker,
	reservation *rgb11wallet.ReceiveReservation, deleteRecord func() error) error {

	if locker == nil || reservation == nil || strings.TrimSpace(reservation.OutPoint) == "" ||
		strings.TrimSpace(reservation.ReservationID) == "" || deleteRecord == nil {
		return fmt.Errorf("%w: receive reservation has no owner", ErrRGB11Inconsistent)
	}
	if err := locker.ReleaseReservation(
		[]string{reservation.OutPoint}, reservation.ReservationID,
	); err != nil {
		return err
	}
	return deleteRecord()
}

func (p *rgb11Manager) releaseRGB11ReceiveReservation(requestID string) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	reservation, err := p.rgbManager.projectionStore.LoadReceiveReservation(requestID)
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return releaseRGB11ReceiveReservationValue(p.utxoLockerL1, reservation, func() error {
		return p.rgbManager.projectionStore.DeleteReceiveReservation(requestID)
	})
}

func (p *rgb11Manager) releaseExpiredRGB11ReceiveReservations(now int64) error {
	reservations, err := p.rgbManager.projectionStore.ListReceiveReservations()
	if err != nil {
		return err
	}
	for _, reservation := range reservations {
		if reservation.Expiry > now {
			continue
		}
		if err := p.releaseRGB11ReceiveReservation(reservation.RequestID); err != nil {
			return err
		}
	}
	return nil
}

func (p *rgb11Manager) CreateRGB11Invoice(request RGB11InvoiceRequest) (*corewallet.ReceiveRequest, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.engine == nil || p.wallet == nil {
		return nil, ErrRGB11WalletLocked
	}
	if request.Expiry == 0 {
		request.Expiry = time.Now().Add(24 * time.Hour).Unix()
	}
	network := rgb11InvoiceNetwork(GetChainParam())
	pubkey := p.wallet.GetPubKey()
	if pubkey == nil {
		return nil, ErrRGB11WalletLocked
	}
	amount, err := strconv.ParseUint(request.AmountRaw, 10, 64)
	if err != nil {
		return nil, err
	}
	mode := corewallet.ReceiveMode(strings.ToLower(strings.TrimSpace(request.Mode)))
	if mode == "" {
		mode = corewallet.ReceiveBlind
	}
	transports, standardOnly, err := rgb11ReceiveTransports(request)
	if err != nil {
		return nil, err
	}
	var witnessScript []byte
	var internalXOnly *[32]byte
	var receiveKey *rgb11wallet.ReceiveKey
	var blindSeal *seals.GraphBlindSeal
	var reservedOutpoint string
	var reservationID string
	if mode == corewallet.ReceiveWitness {
		if standardOnly {
			receiveKey, err = p.newRGB11ReceiveKey()
			if err != nil {
				return nil, err
			}
			witnessScript = append([]byte(nil), receiveKey.WitnessScript...)
			if len(receiveKey.InternalPubKey) != 32 {
				return nil, errors.New("invalid independent RGB11 receive public key")
			}
			var xonly [32]byte
			copy(xonly[:], receiveKey.InternalPubKey)
			internalXOnly = &xonly
		} else {
			logicalAddress := p.wallet.GetAddress()
			if logicalAddress == "" {
				return nil, ErrRGB11WalletLocked
			}
			witnessScript, err = AddrToPkScript(logicalAddress, GetChainParam())
			if err != nil {
				return nil, err
			}
			compressed := pubkey.SerializeCompressed()
			if len(compressed) != 33 {
				return nil, ErrRGB11WalletLocked
			}
			var xonly [32]byte
			copy(xonly[:], compressed[1:])
			internalXOnly = &xonly
		}
	} else if mode == corewallet.ReceiveBlind && standardOnly {
		blindSeal, reservedOutpoint, err = p.newRGB11BlindReceiveSeal()
		if err != nil {
			return nil, err
		}
		reservationID, err = newRGB11ReservationID(nil)
		if err != nil {
			return nil, err
		}
		if err := p.utxoLockerL1.TryReserve([]string{reservedOutpoint}, rgb11wallet.LockReasonPending, reservationID); err != nil {
			return nil, err
		}
	}
	receive, err := p.rgbManager.engine.CreateReceive(corewallet.ReceiveParams{
		Mode: mode, BlindSeal: blindSeal,
		ContractID: request.ContractID, SchemaID: request.SchemaID, Network: network,
		Amount: &amount, AssignmentName: request.AssignmentName,
		RecipientID: hex.EncodeToString(pubkey.SerializeCompressed()),
		WitnessVout: request.WitnessVout, WitnessScript: witnessScript,
		InternalXOnly: internalXOnly, Expiry: request.Expiry,
		Transports: transports, StandardOnly: standardOnly,
	})
	if err != nil {
		if reservedOutpoint != "" {
			_ = p.utxoLockerL1.ReleaseReservation([]string{reservedOutpoint}, reservationID)
		}
		return nil, err
	}
	if reservedOutpoint != "" {
		err = p.rgbManager.projectionStore.SaveReceiveReservation(&rgb11wallet.ReceiveReservation{
			Version: 1, RequestID: receive.RequestID,
			OutPoint: reservedOutpoint, ReservationID: reservationID, Expiry: request.Expiry,
		})
		if err != nil {
			_ = p.utxoLockerL1.ReleaseReservation([]string{reservedOutpoint}, reservationID)
			return nil, err
		}
	}
	if receiveKey != nil {
		receiveKey.RequestID = receive.RequestID
		if err := p.rgbManager.projectionStore.SaveReceiveKey(receiveKey); err != nil {
			return nil, err
		}
	}
	p.autoBackupRGB11AfterMutation()
	return receive, nil
}

func rgb11InvoiceNetwork(params *chaincfg.Params) invoicing.ChainNet {
	if params == nil {
		return invoicing.BitcoinTestnet4
	}
	switch params.Net {
	case chaincfg.MainNetParams.Net:
		return invoicing.BitcoinMainnet
	case chaincfg.TestNet3Params.Net:
		return invoicing.BitcoinTestnet3
	case chaincfg.RegressionNetParams.Net:
		return invoicing.BitcoinRegtest
	case chaincfg.SigNetParams.Net:
		return invoicing.BitcoinSignet
	default:
		return invoicing.BitcoinTestnet4
	}
}

func (p *rgb11Manager) GetRGB11ReceiveRequest(requestID string) (*corewallet.ReceiveRequest, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.engine == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgbManager.engine.LoadReceive(requestID)
}

var (
	ErrRGB11InvoiceMismatch = errors.New("RGB11 consignment does not satisfy the wallet invoice")
	ErrRGB11NoAllocation    = errors.New("RGB11 consignment has no allocation for this wallet")
)

// ValidateRGB11Consignment validates and stores an immutable receipt without
// projecting any balance. It is useful for contract import and diagnostics.
func (p *rgb11Manager) ValidateRGB11Consignment(ctx context.Context, raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgbManager.projectionStore.ValidateAndStoreConsignment(ctx, rgb11wallet.NewNativeConsensusValidator(), p.rgbManager.evidence, raw)
}

// AcceptRGB11Consignment validates the complete client-side history and then
// projects only the allocation matching the wallet's pre-persisted invoice
// seal. A valid consignment for another wallet never becomes local balance.
func (p *rgb11Manager) AcceptRGB11Consignment(ctx context.Context, requestID string, raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	return p.acceptRGB11Consignment(ctx, requestID, raw, true, "", nil)
}

// PrepareRGB11Consignment validates and stages a standard out-of-band
// consignment before its witness transaction is broadcast. It does not project
// a spendable balance; AcceptRGB11Consignment finalizes the receive after the
// witness becomes visible to Bitcoin evidence.
func (p *rgb11Manager) PrepareRGB11Consignment(ctx context.Context, requestID string,
	raw []byte) (*rgb11wallet.ValidationReceipt, error) {

	if p == nil || p.rgbManager == nil || p.rgbManager.engine == nil {
		return nil, ErrRGB11Inconsistent
	}
	request, err := p.rgbManager.engine.LoadReceive(requestID)
	if err != nil {
		return nil, err
	}
	invoice, err := invoicing.Parse(request.Invoice)
	if err != nil {
		return nil, err
	}
	_, _, _, _, _, transport, err := validateRGB11SendInvoice(invoice, nil, nil)
	if err != nil || transport != "out-of-band" {
		return nil, ErrRGB11OutOfBandRequired
	}
	return p.prepareRGB11Consignment(ctx, requestID, raw, "", nil, true)
}

func (p *rgb11Manager) acceptRGB11Consignment(ctx context.Context, requestID string, raw []byte,
	autoBackup bool, expectedTxID string, expectedVout *uint32) (*rgb11wallet.ValidationReceipt, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.engine == nil {
		return nil, ErrRGB11Inconsistent
	}
	request, err := p.rgbManager.engine.LoadReceive(requestID)
	if err != nil {
		return nil, err
	}
	invoice, err := invoicing.Parse(request.Invoice)
	if err != nil {
		return nil, err
	}
	if err := invoice.Validate(time.Now().Unix()); err != nil {
		return nil, err
	}
	transportMode, err := rgb11InvoiceTransportMode(invoice)
	if err != nil {
		return nil, err
	}
	var validator rgb11wallet.ConsensusValidator
	switch invoice.Beneficiary.Kind {
	case invoicing.BeneficiaryBlindedSeal:
		concealed, err := request.Seal.Conceal()
		if err != nil || !bytes.Equal(invoice.Beneficiary.BlindedSeal[:], concealed[:]) {
			return nil, ErrRGB11InvoiceMismatch
		}
		validator = rgb11wallet.NewNativeConsensusValidatorWithReveals(request.Seal)
	case invoicing.BeneficiaryWitnessVout:
		witnessScript, err := invoice.Beneficiary.WitnessScript()
		if err != nil || !bytes.Equal(request.WitnessScript, witnessScript) {
			return nil, ErrRGB11InvoiceMismatch
		}
		validator = rgb11wallet.NewNativeConsensusValidator()
	default:
		return nil, ErrRGB11InvoiceMismatch
	}
	receipt, err := p.rgbManager.projectionStore.ValidateAndStoreConsignment(ctx, validator, p.rgbManager.evidence, raw)
	if err != nil {
		return nil, err
	}
	if invoice.Contract != nil && invoice.Contract.String() != receipt.ContractID {
		return nil, ErrRGB11InvoiceMismatch
	}
	if invoice.Schema != nil {
		decoded, decodeErr := decodeReceiptSchema(receipt.SchemaID)
		if decodeErr != nil || decoded != *invoice.Schema {
			return nil, ErrRGB11InvoiceMismatch
		}
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return nil, err
	}
	matched, err := p.matchRGB11ReceiveAllocation(
		receipt, request, invoice, nil, expectedTxID, expectedVout,
	)
	if err != nil {
		return nil, err
	}
	checked, err := rgb11ReceivedPolicyOpout(matched.Allocation)
	if err != nil {
		return nil, err
	}
	if err := p.checkRGB11RejectPolicy(container, checked); err != nil {
		return nil, err
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return nil, err
	}
	allocation := matched.Allocation
	receivedAsset := &indexer.AssetInfo{
		Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0,
	}
	proof := &rgb11wallet.AllocationProof{
		OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
		OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType,
		AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
		StateData:       append([]byte(nil), allocation.StateData...),
		SealCommitment:  matched.SealCommitment,
		SealDisclosure:  append([]byte(nil), allocation.SealDisclosure...),
		ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
		WitnessTxID: matched.TxID, Status: "valid",
		CarrierBinding: matched.Binding,
	}
	if err := p.ProjectRGB11Allocation(allocation.OutPoint, receivedAsset, proof); err != nil {
		return nil, err
	}
	receivedOutpoint := allocation.OutPoint
	transferID := receipt.TransferID
	if transferID == "" {
		transferID = receipt.ConsignmentHash
	}
	if err := p.rgbManager.engine.MarkRelayAccepted(requestID, transferID, receipt.ConsignmentHash); err != nil {
		return nil, fmt.Errorf("mark RGB11 receive accepted: %w", err)
	}
	expiry := int64(0)
	if invoice.Expiry != nil {
		expiry = *invoice.Expiry
	}
	state := &rgb11wallet.TransferState{
		TransferID: transferID, Direction: "receive", Asset: *receivedAsset,
		RecipientID: request.RecipientID, Invoice: request.Invoice,
		OutputOutPoints: []string{receivedOutpoint}, MinConfirmations: 1, Expiry: expiry,
		ConsignmentHash: receipt.ConsignmentHash, WitnessTxID: matched.TxID,
		AckStatus: "accepted", Status: "pending", RelayRecordKey: request.RelayKey,
		AckRecordKey: request.AckKey, RelayDurability: "LOCAL_ONLY", RelayExpiry: expiry,
		TransportMode: transportMode,
	}
	if request.WitnessTxID != "" {
		state.WitnessTxID = request.WitnessTxID
	}
	if state.WitnessTxID == "" {
		state.WitnessTxID = allocationOutpointTxID(receivedOutpoint)
	}
	if proof, err := p.rgbManager.projectionStore.LoadProof(receivedOutpoint, receivedAsset.Name); err == nil {
		status, statusErr := p.rgbManager.evidence.GetTxStatus(state.WitnessTxID)
		if statusErr == nil && status != nil {
			proof.Confirmations = status.Confirmations
		} else if allocationOutpointTxID(receivedOutpoint) != state.WitnessTxID {
			proof.Confirmations = 0
		}
		if status != nil && status.Confirmed &&
			status.Confirmations >= int64(state.MinConfirmations) {
			state.Status = "settled"
			proof.Status = "settled"
			if err := p.utxoLockerL1.SetLockReason(receivedOutpoint, rgb11wallet.LockReasonRGB); err != nil {
				return nil, err
			}
		} else {
			proof.Status = "valid"
			if err := p.utxoLockerL1.SetLockReason(receivedOutpoint, rgb11wallet.LockReasonPending); err != nil {
				return nil, err
			}
		}
		if err := p.rgbManager.projectionStore.SaveProofState(proof); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		return nil, err
	}
	if err := p.rgbManager.projectionStore.DeletePreparedReceive(transferID); err != nil &&
		!errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, err
	}
	if err := p.rgbManager.projectionStore.DeleteReceiveReservation(requestID); err != nil &&
		!errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, err
	}
	if autoBackup {
		p.autoBackupRGB11AfterMutation()
	}
	return receipt, nil
}

func (p *rgb11Manager) prepareRGB11Consignment(ctx context.Context, requestID string, raw []byte,
	expectedTxID string, expectedVout *uint32, autoBackup bool) (*rgb11wallet.ValidationReceipt, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.engine == nil {
		return nil, ErrRGB11Inconsistent
	}
	request, err := p.rgbManager.engine.LoadReceive(requestID)
	if err != nil {
		return nil, err
	}
	invoice, err := invoicing.Parse(request.Invoice)
	if err != nil {
		return nil, err
	}
	if err := invoice.Validate(time.Now().Unix()); err != nil {
		return nil, err
	}
	var validator *rgb11wallet.NativeConsensusValidator
	switch invoice.Beneficiary.Kind {
	case invoicing.BeneficiaryBlindedSeal:
		concealed, err := request.Seal.Conceal()
		if err != nil || !bytes.Equal(invoice.Beneficiary.BlindedSeal[:], concealed[:]) {
			return nil, ErrRGB11InvoiceMismatch
		}
		validator = rgb11wallet.NewNativeConsensusValidatorWithReveals(request.Seal)
	case invoicing.BeneficiaryWitnessVout:
		witnessScript, err := invoice.Beneficiary.WitnessScript()
		if err != nil || !bytes.Equal(request.WitnessScript, witnessScript) {
			return nil, ErrRGB11InvoiceMismatch
		}
		validator = rgb11wallet.NewNativeConsensusValidator()
	default:
		return nil, ErrRGB11InvoiceMismatch
	}
	prepared, err := rgb11wallet.ValidatePreparedWith(ctx, validator, raw, p.rgbManager.evidence)
	if err != nil {
		return nil, err
	}
	receipt := prepared.Receipt
	if invoice.Contract != nil && invoice.Contract.String() != receipt.ContractID {
		return nil, ErrRGB11InvoiceMismatch
	}
	if invoice.Schema != nil {
		decoded, decodeErr := decodeReceiptSchema(receipt.SchemaID)
		if decodeErr != nil || decoded != *invoice.Schema {
			return nil, ErrRGB11InvoiceMismatch
		}
	}
	actualTxID := prepared.WitnessTxIDs[0]
	if actualTxID == "" || (expectedTxID != "" && expectedTxID != actualTxID) {
		return nil, errors.New("RGB11 proxy metadata does not match the prepared consignment")
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return nil, err
	}
	matchTxID := ""
	matchVout := (*uint32)(nil)
	if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
		matchTxID = actualTxID
		matchVout = expectedVout
	}
	matched, err := p.matchRGB11ReceiveAllocation(
		receipt, request, invoice, prepared.Outputs, matchTxID, matchVout,
	)
	if err != nil {
		return nil, err
	}
	if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout && matched.TxID != actualTxID {
		return nil, errors.New("RGB11 proxy metadata does not match the prepared consignment")
	}
	checked, err := rgb11ReceivedPolicyOpout(matched.Allocation)
	if err != nil {
		return nil, err
	}
	if err := p.checkRGB11RejectPolicy(container, checked); err != nil {
		return nil, err
	}
	allocation := matched.Allocation
	receivedAsset := &indexer.AssetInfo{
		Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0,
	}
	receivedOutpoint := allocation.OutPoint
	transferID := receipt.TransferID
	if transferID == "" {
		transferID = receipt.ConsignmentHash
	}
	if err := p.rgbManager.projectionStore.SavePreparedObject(receipt.ConsignmentHash, raw); err != nil {
		return nil, err
	}
	expiry := int64(0)
	if invoice.Expiry != nil {
		expiry = *invoice.Expiry
	}
	receiveTransport, transportErr := rgb11InvoiceTransportMode(invoice)
	if transportErr != nil {
		return nil, transportErr
	}
	relayDurability := "STANDARD_PROXY"
	if receiveTransport == "out-of-band" {
		relayDurability = "LOCAL_ONLY"
	}
	state := &rgb11wallet.TransferState{
		TransferID: transferID, Direction: "receive", Asset: *receivedAsset,
		RecipientID: request.RecipientID, Invoice: request.Invoice,
		OutputOutPoints: []string{receivedOutpoint}, MinConfirmations: 1, Expiry: expiry,
		ConsignmentHash: receipt.ConsignmentHash, WitnessTxID: actualTxID,
		AckStatus: "accepted", Status: "awaiting_broadcast",
		RelayRecordKey: request.RelayKey, AckRecordKey: request.AckKey,
		RelayDurability: relayDurability, RelayExpiry: expiry, TransportMode: receiveTransport,
	}
	if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		return nil, err
	}
	if err := p.rgbManager.projectionStore.SavePreparedReceive(transferID, requestID); err != nil {
		return nil, err
	}
	if err := p.rgbManager.engine.MarkRelayAcknowledged(
		requestID, transferID, receipt.ConsignmentHash, actualTxID,
	); err != nil {
		return nil, fmt.Errorf("mark RGB11 receive acknowledged: %w", err)
	}
	if err := p.rgbManager.projectionStore.DeleteReceiveReservation(requestID); err != nil &&
		!errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, err
	}
	if autoBackup {
		p.autoBackupRGB11AfterMutation()
	}
	return receipt, nil
}

type rgb11ReceiveAllocationMatch struct {
	Allocation     rgb11wallet.ValidatedAllocation
	UTXO           *rgb11wallet.BitcoinUTXO
	Binding        *rgb11wallet.CarrierBinding
	TxID           string
	Vout           *uint32
	SealCommitment string
}

func (p *rgb11Manager) matchRGB11ReceiveAllocation(receipt *rgb11wallet.ValidationReceipt,
	request *corewallet.ReceiveRequest, invoice *invoicing.Invoice,
	preparedOutputs map[string]*rgb11wallet.BitcoinUTXO, expectedTxID string,
	expectedVout *uint32) (*rgb11ReceiveAllocationMatch, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.evidence == nil ||
		receipt == nil || request == nil || invoice == nil {
		return nil, ErrRGB11Inconsistent
	}
	if expectedTxID != "" && !validRGB11TxID(expectedTxID) {
		return nil, ErrRGB11InvoiceMismatch
	}
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, err
	}
	var invoiceSeal [32]byte
	var witnessScript []byte
	switch invoice.Beneficiary.Kind {
	case invoicing.BeneficiaryBlindedSeal:
		concealed, err := request.Seal.Conceal()
		if err != nil || !bytes.Equal(invoice.Beneficiary.BlindedSeal[:], concealed[:]) {
			return nil, ErrRGB11InvoiceMismatch
		}
		invoiceSeal = [32]byte(concealed)
	case invoicing.BeneficiaryWitnessVout:
		witnessScript, err = invoice.Beneficiary.WitnessScript()
		if err != nil || !bytes.Equal(request.WitnessScript, witnessScript) {
			return nil, ErrRGB11InvoiceMismatch
		}
	default:
		return nil, ErrRGB11InvoiceMismatch
	}

	matches := make([]*rgb11ReceiveAllocationMatch, 0, 1)
	for _, allocation := range receipt.Allocations {
		if allocation.AssignmentType != 4000 {
			continue
		}
		allocationTxID, _, ok := strings.Cut(allocation.OutPoint, ":")
		vout, validVout := outpointVout(allocation.OutPoint)
		if !ok || !validVout || !validRGB11TxID(allocationTxID) {
			continue
		}
		candidateTxID := allocationTxID
		candidateVout := (*uint32)(nil)
		sealCommitment := ""
		switch invoice.Beneficiary.Kind {
		case invoicing.BeneficiaryBlindedSeal:
			matched, err := rgb11AllocationMatchesBlindSeal(allocation, request.Seal, invoiceSeal)
			if err != nil {
				return nil, err
			}
			if !matched {
				continue
			}
			if validRGB11TxID(allocation.WitnessTxID) {
				candidateTxID = allocation.WitnessTxID
			}
			sealCommitment = hex.EncodeToString(invoiceSeal[:])
		case invoicing.BeneficiaryWitnessVout:
			if !allocation.WitnessTxPtr {
				continue
			}
			if allocation.WitnessTxID != "" &&
				(!validRGB11TxID(allocation.WitnessTxID) || allocation.WitnessTxID != allocationTxID) {
				continue
			}
			if expectedTxID != "" && allocationTxID != expectedTxID {
				continue
			}
			if expectedVout != nil && vout != *expectedVout {
				continue
			}
			value := vout
			candidateVout = &value
		}
		if expectedTxID != "" && candidateTxID != expectedTxID {
			continue
		}

		utxo := preparedOutputs[allocation.OutPoint]
		if utxo == nil {
			utxo, err = p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
			if err != nil {
				return nil, err
			}
		}
		if utxo == nil || utxo.OutPoint != allocation.OutPoint ||
			(invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout &&
				!bytes.Equal(utxo.PkScript, witnessScript)) {
			continue
		}
		binding, err := p.rgb11CarrierBindingForRequest(allocation, utxo, request)
		if err != nil || !p.ownsRGB11Carrier(binding, walletScript) {
			if invoice.Beneficiary.Kind == invoicing.BeneficiaryBlindedSeal && err != nil {
				return nil, err
			}
			continue
		}
		if invoice.Assignment != nil {
			switch invoice.Assignment.Kind {
			case invoicing.StateAmount:
				if allocation.Amount.Value.Sign() < 0 || !allocation.Amount.Value.IsUint64() ||
					allocation.Amount.Value.Uint64() != uint64(invoice.Assignment.Amount) {
					continue
				}
			case invoicing.StateAllocation:
				if allocation.Amount.Value.Cmp(indexer.NewDefaultDecimal(1).Value) != 0 {
					continue
				}
			}
		}
		matches = append(matches, &rgb11ReceiveAllocationMatch{
			Allocation: allocation, UTXO: utxo, Binding: binding,
			TxID: candidateTxID, Vout: candidateVout, SealCommitment: sealCommitment,
		})
	}
	if len(matches) == 0 {
		return nil, ErrRGB11NoAllocation
	}
	if len(matches) != 1 {
		return nil, ErrRGB11InvoiceMismatch
	}
	return matches[0], nil
}

func rgb11ReceivedPolicyOpout(allocation rgb11wallet.ValidatedAllocation) ([]operations.Opout, error) {
	opout, err := operations.ParseOpout(fmt.Sprintf("%s/%d/%d",
		allocation.OperationID, allocation.AssignmentType, allocation.AssignmentIndex))
	if err != nil {
		return nil, err
	}
	return []operations.Opout{opout}, nil
}

func rgb11AllocationMatchesBlindSeal(allocation rgb11wallet.ValidatedAllocation,
	expected seals.GraphBlindSeal, invoiceSeal [32]byte) (bool, error) {
	if expected.TxID == nil {
		if !allocation.WitnessTxPtr {
			return false, nil
		}
		vout, ok := outpointVout(allocation.OutPoint)
		if !ok || vout != expected.Vout || allocation.SealBlinding != expected.Blinding {
			return false, nil
		}
		actual := seals.NewWitnessBlindSeal(vout, allocation.SealBlinding)
		concealed, err := actual.Conceal()
		if err != nil {
			return false, err
		}
		return bytes.Equal(concealed[:], invoiceSeal[:]), nil
	}
	actual, err := seals.DecodeGraphBlindSeal(allocation.SealDisclosure)
	if err != nil {
		return false, err
	}
	actualBytes, err := actual.StrictBytes()
	if err != nil {
		return false, err
	}
	expectedBytes, err := expected.StrictBytes()
	if err != nil {
		return false, err
	}
	if !bytes.Equal(actualBytes, expectedBytes) {
		return false, nil
	}
	concealed, err := actual.Conceal()
	if err != nil {
		return false, err
	}
	return bytes.Equal(concealed[:], invoiceSeal[:]), nil
}

func outpointVout(outpoint string) (uint32, bool) {
	_, text, ok := strings.Cut(outpoint, ":")
	if !ok {
		return 0, false
	}
	value, err := strconv.ParseUint(text, 10, 32)
	return uint32(value), err == nil
}

func decodeReceiptSchema(schemaID string) ([32]byte, error) {
	return baid64.Decode32(schemaID, baid64.SchemaIDOptions())
}

const rgb11CarrierValue int64 = 330

var (
	ErrRGB11InsufficientBalance = errors.New("insufficient RGB11 balance")
	ErrRGB11HistoryMerge        = errors.New("selected RGB11 allocations require a history merge")
	ErrRGB11AckRequired         = errors.New("valid recipient ACK is required before broadcast")
	ErrRGB11BatchAckRequired    = errors.New("all RGB11 batch recipient ACKs are required before broadcast")
	ErrRGB11SAT20RelayRequired  = errors.New("RGB11 transfer does not use SAT20 relay delivery")
	ErrRGB11OutOfBandRequired   = errors.New("RGB11 transfer is not a prepared out-of-band transfer")
	ErrRGB11AlreadyBroadcast    = errors.New("RGB11 transfer witness is already visible on Bitcoin")
	ErrRGB11AssetPreservation   = errors.New("RGB11 input contains another asset that cannot be preserved")
)

type rgb11SendRecipient struct {
	raw         string
	invoice     *invoicing.Invoice
	amount      uint64
	recipientID string
	relayKey    string
	ackKey      string
	script      []byte
	vout        uint32
	transport   string
}

type rgb11SpendAllocation struct {
	proof  *rgb11wallet.AllocationProof
	asset  *indexer.AssetInfo
	target bool
}

// PrepareRGB11Transfer builds one client-side state transition for one asset.
// A request may contain multiple witness invoices; all recipients share one
// Bitcoin transaction and one official RGB consignment, while transport and
// ACK state remains recipient-specific. It intentionally does not relay or
// broadcast. RBF replacement is outside the first release scope.
func (p *rgb11Manager) PrepareRGB11Transfer(ctx context.Context, request RGB11SendRequest) (*RGB11PreparedTransfer, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil || p.wallet == nil {
		return nil, ErrRGB11Inconsistent
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	invoiceTexts := append([]string(nil), request.Invoices...)
	if len(invoiceTexts) == 0 && strings.TrimSpace(request.Invoice) != "" {
		invoiceTexts = []string{request.Invoice}
	}
	if len(invoiceTexts) == 0 || len(invoiceTexts) > 32 {
		return nil, invoicing.ErrInvalidInvoice
	}
	var fallbackContract *consensus.ContractID
	var fallbackAmount *uint64
	if strings.TrimSpace(request.ContractID) != "" || strings.TrimSpace(request.AmountRaw) != "" {
		if len(invoiceTexts) != 1 {
			return nil, invoicing.ErrInvalidInvoice
		}
	}
	if value := strings.TrimSpace(request.ContractID); value != "" {
		contract, err := consensus.ParseContractID(value)
		if err != nil {
			return nil, invoicing.ErrInvalidInvoice
		}
		fallbackContract = &contract
	}
	if value := strings.TrimSpace(request.AmountRaw); value != "" {
		amount, err := strconv.ParseUint(value, 10, 64)
		if err != nil || amount == 0 {
			return nil, invoicing.ErrInvalidInvoice
		}
		fallbackAmount = &amount
	}
	recipients := make([]rgb11SendRecipient, 0, len(invoiceTexts))
	seenRecipient := make(map[string]struct{}, len(invoiceTexts))
	seenRelay := make(map[string]struct{}, len(invoiceTexts))
	seenAck := make(map[string]struct{}, len(invoiceTexts))
	var contractID consensus.ContractID
	var totalAmount uint64
	nextRecipientVout := uint32(1)
	for index, raw := range invoiceTexts {
		trimmed := strings.TrimSpace(raw)
		invoice, err := invoicing.Parse(trimmed)
		if err != nil {
			return nil, err
		}
		if err := invoice.Validate(time.Now().Unix()); err != nil {
			return nil, err
		}
		currentContract, amount, recipientID, relayKey, ackKey, transport, err :=
			validateRGB11SendInvoice(invoice, fallbackContract, fallbackAmount)
		if err != nil {
			return nil, err
		}
		if index == 0 {
			contractID = currentContract
		} else if currentContract != contractID {
			return nil, invoicing.ErrInvalidInvoice
		}
		if len(invoiceTexts) > 1 && invoice.Beneficiary.Kind != invoicing.BeneficiaryWitnessVout {
			return nil, fmt.Errorf("RGB11 batch send requires witness invoices")
		}
		if _, ok := seenRecipient[recipientID]; ok {
			return nil, fmt.Errorf("RGB11 batch contains duplicate recipient")
		}
		if index > 0 && transport != recipients[0].transport {
			return nil, fmt.Errorf("RGB11 batch cannot mix transport modes")
		}
		if relayKey != "" {
			if _, ok := seenRelay[relayKey]; ok {
				return nil, fmt.Errorf("RGB11 batch contains duplicate relay key")
			}
			seenRelay[relayKey] = struct{}{}
		}
		if ackKey != "" {
			if _, ok := seenAck[ackKey]; ok {
				return nil, fmt.Errorf("RGB11 batch contains duplicate ACK key")
			}
			seenAck[ackKey] = struct{}{}
		}
		seenRecipient[recipientID] = struct{}{}
		if ^uint64(0)-totalAmount < amount {
			return nil, rgb11wallet.ErrInvalidProof
		}
		totalAmount += amount
		var script []byte
		var recipientVout uint32
		if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
			script, err = invoice.Beneficiary.WitnessScript()
			recipientVout = nextRecipientVout
			nextRecipientVout++
		} else if transport == "sat20-dkvs" {
			recipientPubKey, _ := hex.DecodeString(recipientID)
			script, err = HexPubKeyToP2TRPkScript(recipientPubKey)
			recipientVout = nextRecipientVout
			nextRecipientVout++
		}
		if err != nil {
			return nil, err
		}
		recipients = append(recipients, rgb11SendRecipient{
			raw: trimmed, invoice: invoice, amount: amount, recipientID: recipientID,
			relayKey: relayKey, ackKey: ackKey, script: script, vout: recipientVout, transport: transport,
		})
	}
	if request.MinConfirmations == 0 {
		request.MinConfirmations = 1
	}

	selected, _, targetAsset, targetClass, targetType, targetTotal, err :=
		p.selectRGB11Allocations(contractID.String(), totalAmount, request.MinConfirmations)
	if err != nil {
		return nil, err
	}
	if len(recipients) > 1 && targetClass != "fungible" {
		return nil, fmt.Errorf("RGB11 batch send supports fungible state only")
	}
	base, parentStateHash, err := p.mergeRGB11SpendHistories(selected)
	if err != nil {
		return nil, err
	}

	reveals := make([]seals.GraphBlindSeal, 0, len(selected))
	inputs := make([]operations.TransitionInput, 0, len(selected))
	for _, allocation := range selected {
		operationID, err := hex.DecodeString(allocation.proof.OperationID)
		if err != nil || len(operationID) != 32 || allocation.proof.AssignmentType > 0xffff || allocation.proof.AssignmentIndex > 0xffff {
			return nil, rgb11wallet.ErrInvalidProof
		}
		var operation [32]byte
		copy(operation[:], operationID)
		inputs = append(inputs, operations.TransitionInput{
			OperationID: operation, AssignmentType: uint16(allocation.proof.AssignmentType),
			Index: uint16(allocation.proof.AssignmentIndex),
		})
		graph, err := seals.DecodeGraphBlindSeal(allocation.proof.SealDisclosure)
		if err != nil {
			return nil, rgb11wallet.ErrInvalidProof
		}
		reveals = append(reveals, graph)
	}
	if len(reveals) > 0 {
		if _, err := base.RevealGraphSeals(reveals); err != nil {
			return nil, err
		}
	}

	changeSeals := make([]seals.GraphBlindSeal, 0)
	changeSecrets := make([][32]byte, 0)
	recipientSecrets := make([][32]byte, 0, 1)
	outputs := make([]operations.TransitionOutput, 0, len(recipients)+1)
	var structuredData []byte
	if targetClass == "structured" {
		for _, allocation := range selected {
			if allocation.target {
				structuredData = append([]byte(nil), allocation.proof.StateData...)
				break
			}
		}
	}
	for _, recipient := range recipients {
		output := operations.TransitionOutput{
			AssignmentType: uint16(targetType), Class: targetClass, Amount: recipient.amount,
			Data: append([]byte(nil), structuredData...),
		}
		switch recipient.invoice.Beneficiary.Kind {
		case invoicing.BeneficiaryBlindedSeal:
			copy(output.SecretSeal[:], recipient.invoice.Beneficiary.BlindedSeal[:])
			recipientSecrets = append(recipientSecrets, [32]byte(recipient.invoice.Beneficiary.BlindedSeal))
		case invoicing.BeneficiaryWitnessVout:
			seal, err := seals.RandomWitnessBlindSeal(recipient.vout)
			if err != nil {
				return nil, err
			}
			output.RevealedSeal = &seal
		default:
			return nil, invoicing.ErrInvalidInvoice
		}
		outputs = append(outputs, output)
	}
	changeVout := nextRecipientVout
	if targetTotal > totalAmount {
		if targetClass != "fungible" {
			return nil, ErrRGB11InsufficientBalance
		}
		change, secret, err := newRGB11ChangeOutput(changeVout, uint16(targetType), targetClass, targetTotal-totalAmount, nil)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, change)
		changeSeals = append(changeSeals, secret.seal)
		changeSecrets = append(changeSecrets, secret.secret)
	}
	for _, allocation := range selected {
		if allocation.target {
			continue
		}
		stateAmount, err := decimalUint64(&allocation.asset.Amount)
		if err != nil {
			return nil, err
		}
		change, secret, err := newRGB11ChangeOutput(
			changeVout, uint16(allocation.proof.AssignmentType), allocation.proof.StateClass,
			stateAmount, allocation.proof.StateData,
		)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, change)
		changeSeals = append(changeSeals, secret.seal)
		changeSecrets = append(changeSecrets, secret.secret)
	}

	transition, transitionCommitment, err := operations.BuildTransition(operations.TransitionSpec{
		ContractID: [32]byte(contractID), Nonce: uint64(time.Now().UnixNano()),
		TransitionType: 10_000, Inputs: inputs, Outputs: outputs,
	})
	if err != nil {
		return nil, err
	}
	bundleValue, bundleCommitment, err := operations.BuildBundle(inputs, transition)
	if err != nil {
		return nil, err
	}
	mpcProof, err := anchors.NewMPCProof([32]byte(contractID), 3)
	if err != nil {
		return nil, err
	}
	mpcCommitment, err := anchors.ConvolveMPC(mpcProof, [32]byte(contractID), bundleCommitment.BundleID)
	if err != nil {
		return nil, err
	}

	recipientScripts := make([][]byte, 0, len(recipients))
	for _, recipient := range recipients {
		if len(recipient.script) != 0 {
			recipientScripts = append(recipientScripts, recipient.script)
		}
	}
	changeScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, err
	}
	tx, prevFetcher, inputOutpoints, taprootRoots, _, err := p.buildRGB11WitnessTx(
		selected, recipientScripts, changeScript, anchors.OpretScript(mpcCommitment), request.FeeRate,
	)
	if err != nil {
		return nil, err
	}
	reservationID, err := newRGB11ReservationID(nil)
	if err != nil {
		return nil, err
	}
	reserved := append([]string(nil), inputOutpoints...)
	if err := p.utxoLockerL1.TryReserve(
		reserved, rgb11wallet.LockReasonPending, reservationID, rgb11wallet.LockReasonRGB,
	); err != nil {
		return nil, err
	}
	reservationCommitted := false
	defer func() {
		if !reservationCommitted {
			_ = p.utxoLockerL1.ReleaseReservation(reserved, reservationID)
		}
	}()
	packet, signedTx, signedPSBT, err := p.signRGB11PSBT(
		tx, prevFetcher, [32]byte(contractID), transitionCommitment.OperationID,
		transition.Encoded, bundleCommitment.BundleID, mpcProof, mpcCommitment, inputs, taprootRoots,
	)
	if err != nil {
		return nil, err
	}
	_ = packet
	witnessBundle, err := operations.BuildOpretWitnessBundleWithTx(signedTx, bundleValue, mpcProof)
	if err != nil {
		return nil, err
	}
	recipientContainer, err := coreconsignment.BuildTransfer(base, witnessBundle, recipientSecrets)
	if err != nil {
		return nil, err
	}
	localContainer, err := coreconsignment.BuildTransfer(base, witnessBundle, changeSecrets)
	if err != nil {
		return nil, err
	}
	recipientArmor, err := coreconsignment.EncodeArmor(recipientContainer.Value)
	if err != nil {
		return nil, err
	}
	localArmor, err := coreconsignment.EncodeArmor(localContainer.Value)
	if err != nil {
		return nil, err
	}
	recipientDecoded, err := coreconsignment.DecodeArmor(recipientArmor)
	if err != nil {
		return nil, err
	}

	var signedRaw bytes.Buffer
	if err := signedTx.Serialize(&signedRaw); err != nil {
		return nil, err
	}
	objectHash := sha256.Sum256([]byte(recipientArmor))
	targetPrecision := 0
	for _, allocation := range selected {
		if allocation.target {
			targetPrecision = allocation.asset.Amount.Precision
			break
		}
	}
	batchID := recipientDecoded.Armor.ID
	transferIDs := make([]string, len(recipients))
	for index := range recipients {
		if len(recipients) == 1 {
			transferIDs[index] = batchID
			continue
		}
		child := sha256.Sum256([]byte(fmt.Sprintf("SAT20-RGB11-BATCH-RECIPIENT-V1:%s:%d", batchID, index)))
		transferIDs[index] = hex.EncodeToString(child[:])
	}
	pendingList := make([]*rgb11wallet.PendingTransfer, 0, len(recipients))
	states := make([]*rgb11wallet.TransferState, 0, len(recipients))
	for index, recipient := range recipients {
		outputOutPoints := make([]string, 0, 2)
		if len(recipient.script) != 0 {
			outputOutPoints = append(outputOutPoints, fmt.Sprintf("%s:%d", signedTx.TxID(), recipient.vout))
		}
		outputOutPoints = append(outputOutPoints, fmt.Sprintf("%s:%d", signedTx.TxID(), changeVout))
		state := rgb11wallet.TransferState{
			TransferID: transferIDs[index], Direction: "send",
			Asset: indexer.AssetInfo{Name: targetAsset, Amount: indexer.Decimal{
				Precision: targetPrecision, Value: new(big.Int).SetUint64(recipient.amount),
			}, BindingSat: 0},
			RecipientID: recipient.recipientID, Invoice: recipient.raw, InputOutPoints: append([]string(nil), inputOutpoints...),
			OutputOutPoints:  outputOutPoints,
			MinConfirmations: request.MinConfirmations, Expiry: *recipient.invoice.Expiry,
			ConsignmentHash: hex.EncodeToString(objectHash[:]), WitnessTxID: signedTx.TxID(),
			AckStatus: "awaiting", Status: "prepared", RelayRecordKey: recipient.relayKey, AckRecordKey: recipient.ackKey,
			RelayDurability: "LOCAL_ONLY", RelayExpiry: *recipient.invoice.Expiry,
			ParentStateHash: parentStateHash, RecipientVout: recipient.vout, BatchSize: len(recipients),
			BatchTransferIDs: append([]string(nil), transferIDs...), TransportMode: recipient.transport,
		}
		if len(recipients) > 1 {
			state.BatchID = batchID
		}
		pending := &rgb11wallet.PendingTransfer{
			State: state, RecipientConsignment: []byte(recipientArmor), LocalConsignment: []byte(localArmor),
			SignedTx: append([]byte(nil), signedRaw.Bytes()...), SignedPSBT: append([]byte(nil), signedPSBT...),
			ChangeSeals:   append([]seals.GraphBlindSeal(nil), changeSeals...),
			ReservationID: reservationID, CreatedAt: time.Now().Unix(),
		}
		pendingList = append(pendingList, pending)
		states = append(states, &pending.State)
	}
	changeOutpoints := make([]string, 0, len(changeSeals))
	for _, seal := range changeSeals {
		changeOutpoints = append(changeOutpoints, fmt.Sprintf("%s:%d", signedTx.TxID(), seal.Vout))
	}
	if len(changeOutpoints) != 0 {
		if err := p.utxoLockerL1.TryReserve(changeOutpoints, rgb11wallet.LockReasonPending, reservationID); err != nil {
			return nil, err
		}
		reserved = append(reserved, changeOutpoints...)
	}
	if err := p.rgbManager.projectionStore.SavePendingTransfers(pendingList); err != nil {
		return nil, err
	}
	transferFile, err := coreconsignment.EncodeFile(recipientDecoded)
	if err != nil {
		return nil, err
	}
	reservationCommitted = true
	p.autoBackupRGB11AfterMutation()
	return &RGB11PreparedTransfer{
		State: states[0], States: states, RecipientConsignment: recipientArmor,
		RecipientConsignmentBase64: base64.StdEncoding.EncodeToString(transferFile),
		SignedPSBT:                 hex.EncodeToString(signedPSBT), TxID: signedTx.TxID(),
	}, nil
}

func validateRGB11SendInvoice(invoice *invoicing.Invoice, fallbackContract *consensus.ContractID,
	fallbackAmount *uint64) (consensus.ContractID, uint64, string, string, string, string, error) {
	if invoice == nil || invoice.Expiry == nil ||
		(invoice.Beneficiary.Kind != invoicing.BeneficiaryBlindedSeal && invoice.Beneficiary.Kind != invoicing.BeneficiaryWitnessVout) ||
		(invoice.Assignment != nil && invoice.Assignment.Kind != invoicing.StateAmount) {
		return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
	}
	var contract consensus.ContractID
	switch {
	case invoice.Contract != nil:
		contract = *invoice.Contract
		if fallbackContract != nil && *fallbackContract != contract {
			return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
		}
	case fallbackContract != nil:
		contract = *fallbackContract
	default:
		return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
	}
	var amount uint64
	switch {
	case invoice.Assignment != nil:
		amount = uint64(invoice.Assignment.Amount)
		if fallbackAmount != nil && *fallbackAmount != amount {
			return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
		}
	case fallbackAmount != nil:
		amount = *fallbackAmount
	default:
		return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
	}
	wantNetwork := rgb11InvoiceNetwork(GetChainParam())
	if invoice.Beneficiary.Network != wantNetwork || amount == 0 {
		return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
	}
	values := make(map[string]string, len(invoice.UnknownQuery))
	for _, param := range invoice.UnknownQuery {
		values[param.Key] = param.Value
	}
	hasSAT20 := values["sat20_recipient"] != "" || values["sat20_relay"] != "" || values["sat20_ack"] != ""
	recipientID, relayKey, ackKey, transport := values["sat20_recipient"], values["sat20_relay"], values["sat20_ack"], "sat20-dkvs"
	if hasSAT20 {
		if recipientID == "" || relayKey == "" || ackKey == "" {
			return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
		}
		if invoice.Beneficiary.Kind == invoicing.BeneficiaryBlindedSeal && values["sat20_vout"] != "1" {
			return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
		}
		pubkey, err := hex.DecodeString(recipientID)
		if err != nil || len(pubkey) != 33 {
			return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
		}
		if _, err := HexPubKeyToP2TRPkScript(pubkey); err != nil {
			return consensus.ContractID{}, 0, "", "", "", "", err
		}
	} else if len(invoice.Transports) > 0 {
		if _, err := rgb11ProxyEndpoints(invoice); err != nil {
			return consensus.ContractID{}, 0, "", "", "", "", err
		}
		recipientID = invoice.Beneficiary.String()
		transport = RGB11ProxyTransport
	} else {
		if invoice.Beneficiary.Kind != invoicing.BeneficiaryWitnessVout {
			return consensus.ContractID{}, 0, "", "", "", "", fmt.Errorf("RGB11 external send requires an out-of-band witness invoice")
		}
		recipientID = invoice.Beneficiary.String()
		transport = "out-of-band"
	}
	if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
		if _, err := invoice.Beneficiary.WitnessScript(); err != nil {
			return consensus.ContractID{}, 0, "", "", "", "", err
		}
	}
	return contract, amount, recipientID, relayKey, ackKey, transport, nil
}

func rgb11InvoiceTransportMode(invoice *invoicing.Invoice) (string, error) {
	if invoice == nil {
		return "", invoicing.ErrInvalidInvoice
	}
	values := make(map[string]string, len(invoice.UnknownQuery))
	for _, param := range invoice.UnknownQuery {
		values[param.Key] = param.Value
	}
	hasSAT20 := values["sat20_recipient"] != "" || values["sat20_relay"] != "" || values["sat20_ack"] != ""
	if hasSAT20 {
		if values["sat20_recipient"] == "" || values["sat20_relay"] == "" || values["sat20_ack"] == "" ||
			len(invoice.Transports) != 0 {
			return "", invoicing.ErrInvalidInvoice
		}
		return "sat20-dkvs", nil
	}
	if len(invoice.Transports) != 0 {
		if _, err := rgb11ProxyEndpoints(invoice); err != nil {
			return "", err
		}
		return RGB11ProxyTransport, nil
	}
	if invoice.Beneficiary.Kind != invoicing.BeneficiaryWitnessVout {
		return "", invoicing.ErrInvalidInvoice
	}
	return "out-of-band", nil
}

func (p *rgb11Manager) selectRGB11Allocations(contractID string, amount uint64, minConfirmations uint8) (
	[]rgb11SpendAllocation, string, indexer.AssetName, string, uint32, uint64, error,
) {
	excluded := make(map[string]struct{})
	for {
		selected, baseHash, targetAsset, targetClass, targetType, total, err :=
			p.selectRGB11AllocationsOnce(contractID, amount, minConfirmations, excluded)
		if err != nil {
			return nil, "", indexer.AssetName{}, "", 0, total, err
		}
		base, _, err := p.mergeRGB11SpendHistories(selected)
		if err != nil {
			return nil, "", indexer.AssetName{}, "", 0, 0, err
		}
		checked := make([]operations.Opout, 0, len(selected))
		byOpout := make(map[operations.Opout]*rgb11wallet.AllocationProof)
		for _, allocation := range selected {
			if !allocation.target {
				continue
			}
			opout, err := rgb11ProofOpout(allocation.proof)
			if err != nil {
				return nil, "", indexer.AssetName{}, "", 0, 0, err
			}
			checked = append(checked, opout)
			byOpout[opout] = allocation.proof
		}
		err = p.checkRGB11RejectPolicy(base, checked)
		var violation *RGB11RejectListViolation
		if errors.As(err, &violation) {
			proof := byOpout[violation.Checked]
			if proof == nil {
				return nil, "", indexer.AssetName{}, "", 0, 0, err
			}
			proof.PolicyStatus = "rejected"
			proof.PolicyReason = violation.Rejected.String()
			_ = p.rgbManager.projectionStore.SaveProofState(proof)
			excluded[proof.OutPoint] = struct{}{}
			continue
		}
		if err != nil {
			for _, proof := range byOpout {
				proof.PolicyStatus = "unknown"
				proof.PolicyReason = err.Error()
				_ = p.rgbManager.projectionStore.SaveProofState(proof)
			}
			return nil, "", indexer.AssetName{}, "", 0, 0, err
		}
		for _, proof := range byOpout {
			proof.PolicyStatus = "allowed"
			proof.PolicyReason = ""
			_ = p.rgbManager.projectionStore.SaveProofState(proof)
		}
		return selected, baseHash, targetAsset, targetClass, targetType, total, nil
	}
}

func (p *rgb11Manager) selectRGB11AllocationsOnce(contractID string, amount uint64, minConfirmations uint8,
	excluded map[string]struct{}) (
	[]rgb11SpendAllocation, string, indexer.AssetName, string, uint32, uint64, error,
) {
	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return nil, "", indexer.AssetName{}, "", 0, 0, err
	}
	byOutpoint := make(map[string][]*rgb11wallet.AllocationProof)
	var targets []*rgb11wallet.AllocationProof
	for _, proof := range proofs {
		if proof.Status != "valid" && proof.Status != "settled" {
			continue
		}
		byOutpoint[proof.OutPoint] = append(byOutpoint[proof.OutPoint], proof)
		official, err := p.rgb11ContractIDForAssetName(proof.AssetName)
		if err == nil && official == contractID && proof.AssignmentType == 4000 && proof.AssetName.Type != "control" {
			targets = append(targets, proof)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].OutPoint < targets[j].OutPoint })
	selectedOutpoints := make(map[string]bool)
	selected := make([]rgb11SpendAllocation, 0)
	var baseHash, targetClass string
	var targetAsset indexer.AssetName
	var targetType uint32
	var total uint64
	locked := p.utxoLockerL1.GetLockedUtxoList()
	for _, target := range targets {
		if _, skip := excluded[target.OutPoint]; skip {
			continue
		}
		if lock := locked[target.OutPoint]; lock != nil && lock.Reason == rgb11wallet.LockReasonPending {
			continue
		}
		if selectedOutpoints[target.OutPoint] {
			continue
		}
		utxo, err := p.rgbManager.evidence.GetUTXO(target.OutPoint)
		if err != nil || utxo == nil || utxo.Confirmations < int64(minConfirmations) {
			continue
		}
		group := byOutpoint[target.OutPoint]
		for _, proof := range group {
			official, err := p.rgb11ContractIDForAssetName(proof.AssetName)
			if err != nil || official != contractID {
				return nil, "", indexer.AssetName{}, "", 0, 0, ErrRGB11HistoryMerge
			}
			if baseHash == "" {
				baseHash = proof.ConsignmentHash
			}
			output, err := p.rgbManager.projectionStore.LoadOutput(proof.OutPoint)
			if err != nil {
				return nil, "", indexer.AssetName{}, "", 0, 0, err
			}
			amount := output.GetAsset(&proof.AssetName)
			if amount == nil {
				return nil, "", indexer.AssetName{}, "", 0, 0, rgb11wallet.ErrInvalidProof
			}
			asset := &indexer.AssetInfo{Name: proof.AssetName, Amount: *amount.Clone(), BindingSat: 0}
			isTarget := proof.AssetName == target.AssetName && proof.AssignmentType == target.AssignmentType
			selected = append(selected, rgb11SpendAllocation{proof: proof, asset: asset, target: isTarget})
			if isTarget {
				value, err := decimalUint64(&asset.Amount)
				if err != nil || ^uint64(0)-total < value {
					return nil, "", indexer.AssetName{}, "", 0, 0, rgb11wallet.ErrInvalidProof
				}
				if targetClass == "" {
					targetClass, targetType, targetAsset = proof.StateClass, proof.AssignmentType, proof.AssetName
				}
				if proof.StateClass != targetClass || proof.AssignmentType != targetType || proof.AssetName != targetAsset {
					return nil, "", indexer.AssetName{}, "", 0, 0, ErrRGB11HistoryMerge
				}
				total += value
			}
		}
		selectedOutpoints[target.OutPoint] = true
		if total >= amount {
			break
		}
	}
	if total < amount || len(selected) == 0 {
		return nil, "", indexer.AssetName{}, "", 0, total, ErrRGB11InsufficientBalance
	}
	return selected, baseHash, targetAsset, targetClass, targetType, total, nil
}

func (p *rgb11Manager) mergeRGB11SpendHistories(selected []rgb11SpendAllocation) (*coreconsignment.Container, string, error) {
	hashes := make(map[string]struct{})
	for _, allocation := range selected {
		if allocation.proof == nil || allocation.proof.ConsignmentHash == "" {
			return nil, "", rgb11wallet.ErrInvalidProof
		}
		hashes[allocation.proof.ConsignmentHash] = struct{}{}
	}
	ordered := make([]string, 0, len(hashes))
	for hash := range hashes {
		ordered = append(ordered, hash)
	}
	sort.Strings(ordered)
	containers := make([]*coreconsignment.Container, 0, len(ordered))
	stateHasher := sha256.New()
	for _, hash := range ordered {
		raw, err := p.rgbManager.projectionStore.LoadObject(hash)
		if err != nil {
			return nil, "", err
		}
		container, err := coreconsignment.Decode(raw)
		if err != nil {
			return nil, "", err
		}
		receipt, err := p.rgbManager.projectionStore.LoadValidationReceipt(hash)
		if err != nil {
			return nil, "", err
		}
		containers = append(containers, container)
		stateHasher.Write(receipt.StateHash[:])
	}
	merged, err := coreconsignment.MergeHistories(containers...)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrRGB11HistoryMerge, err)
	}
	return merged, hex.EncodeToString(stateHasher.Sum(nil)), nil
}

type rgb11ChangeSecret struct {
	seal   seals.GraphBlindSeal
	secret [32]byte
}

func newRGB11ChangeOutput(vout uint32, assignmentType uint16, class string, amount uint64, data []byte) (operations.TransitionOutput, rgb11ChangeSecret, error) {
	seal, err := seals.RandomWitnessBlindSeal(vout)
	if err != nil {
		return operations.TransitionOutput{}, rgb11ChangeSecret{}, err
	}
	secret, err := seal.Conceal()
	if err != nil {
		return operations.TransitionOutput{}, rgb11ChangeSecret{}, err
	}
	return operations.TransitionOutput{
		AssignmentType: assignmentType, Class: class, Amount: amount,
		Data: append([]byte(nil), data...), SecretSeal: [32]byte(secret),
	}, rgb11ChangeSecret{seal: seal, secret: [32]byte(secret)}, nil
}

func decimalUint64(value *indexer.Decimal) (uint64, error) {
	if value == nil || value.Value == nil || value.Value.Sign() < 0 || !value.Value.IsUint64() {
		return 0, rgb11wallet.ErrInvalidProof
	}
	return value.Value.Uint64(), nil
}

func (p *rgb11Manager) buildRGB11WitnessTx(selected []rgb11SpendAllocation, recipientScripts [][]byte, changeScript, opretScript []byte, feeRate int64) (
	*wire.MsgTx, *txscript.MultiPrevOutFetcher, []string, map[int]RGB11InputSigningKey, int64, error,
) {
	if len(recipientScripts) > 32 {
		return nil, nil, nil, nil, 0, invoicing.ErrInvalidInvoice
	}
	if feeRate <= 0 {
		feeRate = p.GetFeeRate()
	}
	if feeRate <= 0 {
		feeRate = 1
	}
	unique := make(map[string]int)
	inputOutpoints := make([]string, 0)
	signingKeys := make(map[int]RGB11InputSigningKey)
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	tx := wire.NewMsgTx(2)
	var inputValue int64
	addInput := func(outpoint string, value int64, pkScript []byte) (int, error) {
		if index, ok := unique[outpoint]; ok {
			return index, nil
		}
		wireOutpoint, err := wire.NewOutPointFromString(outpoint)
		if err != nil {
			return 0, err
		}
		index := len(tx.TxIn)
		txIn := wire.NewTxIn(wireOutpoint, nil, nil)
		txIn.Sequence = wire.MaxTxInSequenceNum - 2
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*wireOutpoint, &wire.TxOut{Value: value, PkScript: append([]byte(nil), pkScript...)})
		unique[outpoint] = index
		inputOutpoints = append(inputOutpoints, outpoint)
		inputValue += value
		return index, nil
	}
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, nil, nil, nil, 0, err
	}
	for _, allocation := range selected {
		view, err := p.getL1TxOutput(allocation.proof.OutPoint)
		if err != nil {
			return nil, nil, nil, nil, 0, fmt.Errorf("resolve complete asset view for %s: %w", allocation.proof.OutPoint, err)
		}
		if view == nil {
			return nil, nil, nil, nil, 0, fmt.Errorf("resolve complete asset view for %s: %w", allocation.proof.OutPoint, ErrRGB11Inconsistent)
		}
		for index := range view.Assets {
			assetName := &view.Assets[index].Name
			if assetName.Protocol == rgb11wallet.Protocol || indexer.IsPlainAsset(assetName) {
				continue
			}
			return nil, nil, nil, nil, 0, fmt.Errorf("%w: %s carries %s", ErrRGB11AssetPreservation,
				allocation.proof.OutPoint, assetName.String())
		}
		utxo, err := p.rgbManager.evidence.GetUTXO(allocation.proof.OutPoint)
		if err != nil || utxo == nil {
			return nil, nil, nil, nil, 0, fmt.Errorf("resolve RGB11 carrier %s: %w", allocation.proof.OutPoint, err)
		}
		signingKey := RGB11InputSigningKey{Change: 0, Index: p.wallet.GetSubAccount()}
		if binding := allocation.proof.CarrierBinding; binding != nil && binding.CommitmentMethod == "tapret1st" {
			if len(binding.TapretRoot) != sha256.Size || !bytes.Equal(binding.ActualPkScript, utxo.PkScript) ||
				!p.ownsRGB11Carrier(binding, walletScript) {
				return nil, nil, nil, nil, 0, fmt.Errorf("RGB11 Tapret carrier %s is not controlled by active wallet", allocation.proof.OutPoint)
			}
			signingKey.Change, signingKey.Index = rgb11CarrierPath(binding.DerivationIndex)
			signingKey.TaprootMerkleRoot = append([]byte(nil), binding.TapretRoot...)
		} else if binding := allocation.proof.CarrierBinding; binding != nil {
			if !p.ownsRGB11Carrier(binding, walletScript) {
				return nil, nil, nil, nil, 0, fmt.Errorf("RGB11 carrier %s is not controlled by active wallet", allocation.proof.OutPoint)
			}
			signingKey.Change, signingKey.Index = rgb11CarrierPath(binding.DerivationIndex)
		} else if !bytes.Equal(utxo.PkScript, walletScript) {
			return nil, nil, nil, nil, 0, fmt.Errorf("RGB11 carrier %s is not controlled by active wallet", allocation.proof.OutPoint)
		}
		inputIndex, err := addInput(utxo.OutPoint, utxo.Value, utxo.PkScript)
		if err != nil {
			return nil, nil, nil, nil, 0, err
		}
		if existing, ok := signingKeys[inputIndex]; ok {
			if existing.Change != signingKey.Change || existing.Index != signingKey.Index ||
				!bytes.Equal(existing.TaprootMerkleRoot, signingKey.TaprootMerkleRoot) {
				return nil, nil, nil, nil, 0, rgb11wallet.ErrInvalidProof
			}
		} else {
			signingKeys[inputIndex] = signingKey
		}
	}
	tx.AddTxOut(wire.NewTxOut(0, opretScript))
	for _, recipientScript := range recipientScripts {
		if len(recipientScript) == 0 {
			return nil, nil, nil, nil, 0, invoicing.ErrInvalidInvoice
		}
		tx.AddTxOut(wire.NewTxOut(rgb11CarrierValue, recipientScript))
	}
	tx.AddTxOut(wire.NewTxOut(0, changeScript))
	changeIndex := len(tx.TxOut) - 1
	recipientValue := int64(len(recipientScripts)) * rgb11CarrierValue

	plain := p.l1IndexerClient.GetUtxoListWithTicker(p.wallet.GetAddress(), &indexer.ASSET_PLAIN_SAT)
	p.utxoLockerL1.Reload(p.wallet.GetAddress())
	plainIndex := len(plain) - 1
	for {
		var estimate utils.TxWeightEstimator
		for range tx.TxIn {
			estimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
		for _, output := range tx.TxOut {
			estimate.AddTxOutput(output)
		}
		fee := estimate.Fee(feeRate)
		if inputValue >= recipientValue+rgb11CarrierValue+fee {
			tx.TxOut[changeIndex].Value = inputValue - recipientValue - fee
			return tx, prevFetcher, inputOutpoints, signingKeys, fee, nil
		}
		var candidateFound bool
		for plainIndex >= 0 {
			candidate := plain[plainIndex]
			plainIndex--
			if _, used := unique[candidate.OutPoint]; used || p.utxoLockerL1.IsLocked(candidate.OutPoint) ||
				!bytes.Equal(candidate.PkScript, walletScript) {
				continue
			}
			if _, err := addInput(candidate.OutPoint, candidate.Value, candidate.PkScript); err != nil {
				return nil, nil, nil, nil, 0, err
			}
			candidateFound = true
			break
		}
		if !candidateFound {
			return nil, nil, nil, nil, 0, fmt.Errorf("insufficient plain sats for RGB11 fee")
		}
	}
}

func (p *rgb11Manager) signRGB11PSBT(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	contractID, transitionID [32]byte, transition []byte, bundleID [32]byte,
	mpcProof anchors.MPCProof, mpcCommitment [32]byte,
	inputs []operations.TransitionInput, signingKeys map[int]RGB11InputSigningKey) (*psbt.Packet, *wire.MsgTx, []byte, error) {
	packet, err := CreatePsbt(tx, prevFetcher, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	transitionKey, err := corepsbt.RGBTransition(transitionID).RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	closeKey, err := corepsbt.RGBCloseMethod().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	packet.Unknowns = append(packet.Unknowns,
		&psbt.Unknown{Key: transitionKey, Value: append([]byte(nil), transition...)},
		&psbt.Unknown{Key: closeKey, Value: []byte{2}},
	)
	consumedKey, err := corepsbt.RGBConsumedBy(contractID).RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	consumed := make([]byte, 2+36*len(inputs))
	binary.LittleEndian.PutUint16(consumed[:2], uint16(len(inputs)))
	for index, input := range inputs {
		offset := 2 + index*36
		copy(consumed[offset:offset+32], input.OperationID[:])
		binary.LittleEndian.PutUint16(consumed[offset+32:offset+34], input.AssignmentType)
		binary.LittleEndian.PutUint16(consumed[offset+34:offset+36], input.Index)
	}
	packet.Unknowns = append(packet.Unknowns, &psbt.Unknown{Key: consumedKey, Value: consumed})
	if len(packet.Outputs) == 0 {
		return nil, nil, nil, fmt.Errorf("RGB11 PSBT has no commitment output")
	}
	messageKey, err := corepsbt.MPCMessage(contractID).RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	depthKey, err := corepsbt.MPCMinTreeDepth().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	commitmentKey, err := corepsbt.MPCCommitment().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	proofKey, err := corepsbt.MPCProof().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	opretHostKey, err := corepsbt.OpretHost().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	opretCommitmentKey, err := corepsbt.OpretCommitment().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	proofValue := make([]byte, 7+32*len(mpcProof.Path))
	binary.LittleEndian.PutUint32(proofValue[:4], mpcProof.Position)
	binary.LittleEndian.PutUint16(proofValue[4:6], mpcProof.Cofactor)
	proofValue[6] = byte(len(mpcProof.Path))
	for index, node := range mpcProof.Path {
		copy(proofValue[7+index*32:], node[:])
	}
	packet.Outputs[0].Unknowns = append(packet.Outputs[0].Unknowns,
		&psbt.Unknown{Key: messageKey, Value: append([]byte(nil), bundleID[:]...)},
		&psbt.Unknown{Key: depthKey, Value: []byte{byte(len(mpcProof.Path))}},
		&psbt.Unknown{Key: commitmentKey, Value: append([]byte(nil), mpcCommitment[:]...)},
		&psbt.Unknown{Key: proofKey, Value: proofValue},
		&psbt.Unknown{Key: opretHostKey, Value: []byte{1}},
		&psbt.Unknown{Key: opretCommitmentKey, Value: append([]byte(nil), mpcCommitment[:]...)},
	)
	if len(signingKeys) == 0 {
		if err := p.wallet.SignPsbt(packet); err != nil {
			return nil, nil, nil, err
		}
	} else {
		signer, ok := p.wallet.(interface {
			SignRGB11Psbt(*psbt.Packet, map[int]RGB11InputSigningKey) error
		})
		if !ok {
			return nil, nil, nil, fmt.Errorf("active wallet does not support RGB11 input signing")
		}
		if err := signer.SignRGB11Psbt(packet, signingKeys); err != nil {
			return nil, nil, nil, err
		}
	}
	if err := psbt.MaybeFinalizeAll(packet); err != nil {
		return nil, nil, nil, err
	}
	var encoded bytes.Buffer
	if err := packet.Serialize(&encoded); err != nil {
		return nil, nil, nil, err
	}
	finalTx, err := psbt.Extract(packet)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := VerifySignedTx(finalTx, prevFetcher); err != nil {
		return nil, nil, nil, err
	}
	return packet, finalTx, encoded.Bytes(), nil
}

func queryValue(invoice *invoicing.Invoice, key string) string {
	if invoice == nil {
		return ""
	}
	for _, param := range invoice.UnknownQuery {
		if param.Key == key {
			return param.Value
		}
	}
	return ""
}

func parseOutpointVout(outpoint string) (uint32, error) {
	separator := strings.LastIndexByte(outpoint, ':')
	if separator < 0 {
		return 0, fmt.Errorf("invalid outpoint")
	}
	value, err := strconv.ParseUint(outpoint[separator+1:], 10, 32)
	return uint32(value), err
}

func (p *rgb11Manager) expectedRGB11TransactionStatus(
	pending *rgb11wallet.PendingTransfer) (*rgb11wallet.BitcoinTxStatus, bool) {
	status, _ := p.rgbManager.evidence.GetTxStatus(pending.State.WitnessTxID)
	if status != nil && (status.InMempool || status.Confirmed) {
		return status, true
	}
	for _, outpoint := range pending.State.OutputOutPoints {
		utxo, err := p.rgbManager.evidence.GetUTXO(outpoint)
		if err != nil || utxo == nil {
			continue
		}
		status = &rgb11wallet.BitcoinTxStatus{
			TxID: pending.State.WitnessTxID, InMempool: utxo.Confirmations == 0,
			Confirmed: utxo.Confirmations > 0, Confirmations: utxo.Confirmations,
		}
		return status, true
	}
	if status == nil {
		status = &rgb11wallet.BitcoinTxStatus{TxID: pending.State.WitnessTxID}
	}
	return status, false
}

func (p *rgb11Manager) rollbackRGB11LocalChange(pending *rgb11wallet.PendingTransfer) error {
	if pending == nil {
		return ErrRGB11Inconsistent
	}
	derived := make([]string, 0, len(pending.State.OutputOutPoints))
	for _, outpoint := range pending.State.OutputOutPoints {
		if allocationOutpointTxID(outpoint) == pending.State.WitnessTxID {
			derived = append(derived, outpoint)
		}
	}
	if err := p.rgbManager.projectionStore.DeleteProjections(derived); err != nil {
		return err
	}
	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return err
	}
	inputs := make(map[string]bool, len(pending.State.InputOutPoints))
	for _, outpoint := range pending.State.InputOutPoints {
		inputs[outpoint] = true
	}
	for _, proof := range proofs {
		if !inputs[proof.OutPoint] || proof.Status != "spending" {
			continue
		}
		utxo, err := p.rgbManager.evidence.GetUTXO(proof.OutPoint)
		if err != nil || utxo == nil {
			return fmt.Errorf("restore RGB11 carrier %s after reorg: %w", proof.OutPoint, err)
		}
		proof.Status = "valid"
		proof.Confirmations = utxo.Confirmations
		if proof.Confirmations > 0 {
			proof.Status = "settled"
		}
		if err := p.rgbManager.projectionStore.SaveProofState(proof); err != nil {
			return err
		}
		reason := rgb11wallet.LockReasonPending
		if proof.Confirmations > 0 {
			reason = rgb11wallet.LockReasonRGB
		}
		if err := p.utxoLockerL1.SetLockReason(proof.OutPoint, reason); err != nil {
			return err
		}
	}
	return nil
}

// RefreshRGB11State advances locally restored RGB11 state using the expected
// signed transaction and Bitcoin facts. It never requires the Indexer to name
// the spending transaction: unknown spends remain fail-closed and unresolved.
func (p *rgb11Manager) RefreshRGB11State(ctx context.Context) (*RGB11RefreshResult, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil {
		return nil, ErrRGB11Inconsistent
	}
	unlock, err := p.lockRGB11ChainRefresh()
	if err != nil {
		return nil, err
	}
	defer unlock()
	result := &RGB11RefreshResult{}
	if err := p.releaseExpiredRGB11ReceiveReservations(time.Now().Unix()); err != nil {
		return nil, err
	}
	expectedSpends, err := p.rgb11ExpectedInputs()
	if err != nil {
		p.rgbManager.consistencyStatus = "broken"
		return nil, err
	}
	transfers, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return nil, err
	}
	conflictedInputs := make(map[string]bool)
	unresolvedInputs := make(map[string]bool)
	for _, state := range transfers {
		if state.Direction == "receive" {
			if state.Status == "awaiting_broadcast" {
				raw, loadErr := p.rgbManager.projectionStore.LoadObject(state.ConsignmentHash)
				if loadErr != nil {
					return nil, loadErr
				}
				requestID, loadErr := p.rgbManager.projectionStore.LoadPreparedReceive(state.TransferID)
				if loadErr != nil {
					return nil, loadErr
				}
				if _, acceptErr := p.acceptRGB11Consignment(ctx, requestID, raw, true, "", nil); acceptErr != nil {
					if errors.Is(acceptErr, coreconsignment.ErrWitnessUnresolved) ||
						errors.Is(acceptErr, coreconsignment.ErrOutpointUnknown) {
						result.Pending++
						continue
					}
					p.rgbManager.consistencyStatus = "broken"
					return nil, acceptErr
				}
				updated, loadErr := p.rgbManager.projectionStore.LoadTransferState(state.TransferID)
				if loadErr != nil {
					return nil, loadErr
				}
				state = updated
			}
			status, err := p.rgbManager.evidence.GetTxStatus(state.WitnessTxID)
			if err != nil {
				status = &rgb11wallet.BitcoinTxStatus{TxID: state.WitnessTxID}
				for _, outpoint := range state.OutputOutPoints {
					if allocationOutpointTxID(outpoint) == state.WitnessTxID {
						if utxo, utxoErr := p.rgbManager.evidence.GetUTXO(outpoint); utxoErr == nil && utxo != nil {
							status.InMempool = utxo.Confirmations == 0
							status.Confirmed = utxo.Confirmations > 0
							status.Confirmations = utxo.Confirmations
							break
						}
					}
				}
			}
			settled := status != nil && status.Confirmed &&
				status.Confirmations >= max(int64(state.MinConfirmations), 1)
			if settled {
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
			lockReason := rgb11wallet.LockReasonPending
			if settled {
				lockReason = rgb11wallet.LockReasonRGB
			}
			for _, outpoint := range state.OutputOutPoints {
				if err := p.utxoLockerL1.SetLockReason(outpoint, lockReason); err != nil {
					return nil, err
				}
			}
			if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
				return nil, err
			}
			continue
		}
		if state.Status != "broadcast" && state.Status != "pending" && state.Status != "settled" {
			continue
		}
		pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(state.TransferID)
		if err != nil {
			return nil, err
		}
		if err := validateRGB11PendingTransaction(pending); err != nil {
			p.rgbManager.consistencyStatus = "broken"
			return nil, err
		}
		status, visible := p.expectedRGB11TransactionStatus(pending)
		if !visible {
			knownExpected := false
			knownConflict := false
			unknownSpent := false
			allUnspent := true
			for _, outpoint := range state.InputOutPoints {
				outspend, err := p.rgbManager.evidence.GetOutspend(outpoint)
				if err != nil {
					return nil, err
				}
				if outspend == nil || !outspend.Spent {
					continue
				}
				allUnspent = false
				switch outspend.SpendingTx {
				case state.WitnessTxID:
					knownExpected = true
				case "", "unknown":
					unknownSpent = true
					unresolvedInputs[outpoint] = true
				default:
					knownConflict = true
					conflictedInputs[outpoint] = true
					result.Inconsistent = append(result.Inconsistent, outpoint)
				}
			}
			if knownExpected {
				status = &rgb11wallet.BitcoinTxStatus{TxID: state.WitnessTxID, InMempool: true}
				visible = true
			} else if knownConflict {
				pending.State.Status = "conflicted"
				pending.State.AckStatus = "invalidated"
				result.Conflicted++
			} else if unknownSpent {
				result.Unresolved++
				result.Pending++
			} else if allUnspent {
				if pending.State.Status == "settled" || pending.State.Status == "pending" {
					if err := p.rollbackRGB11LocalChange(pending); err != nil {
						return nil, err
					}
					pending.State.Status = "broadcast"
					result.Reorged++
				} else {
					result.Pending++
				}
			}
			if !visible {
				if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
					return nil, err
				}
				if pending.State.Status == "conflicted" {
					if err := p.releaseRGB11PendingReservation([]*rgb11wallet.PendingTransfer{pending}); err != nil {
						return nil, err
					}
				}
				continue
			}
		}
		if err := p.applyRGB11LocalChange(ctx, pending, status); err != nil {
			pending.State.Status = "pending"
			_ = p.rgbManager.projectionStore.SavePendingTransferState(pending)
			result.Pending++
			continue
		}
		if status.Confirmed && status.Confirmations >= max(int64(state.MinConfirmations), 1) {
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
		if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
			return nil, err
		}
		if pending.State.Status == "settled" {
			if err := p.finalizeRGB11PendingChangeReservation(pending); err != nil {
				return nil, err
			}
		}
		if pending.State.Status == "settled" &&
			(!pending.State.AddressMode || pending.State.DeliveryAcknowledged) {
			transferIDs := pending.State.BatchTransferIDs
			if len(transferIDs) == 0 {
				transferIDs = []string{pending.State.TransferID}
			}
			if pending.State.AddressMode {
				pending.State.DeliveryCacheCompacted = true
				if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
					return nil, err
				}
			}
			if err := p.rgbManager.projectionStore.CompactSettledRecipientConsignments(transferIDs); err != nil {
				return nil, err
			}
		}
	}

	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return nil, err
	}
	requirements := rgb11ProofConfirmationRequirements(transfers)
	for _, proof := range proofs {
		if conflictedInputs[proof.OutPoint] {
			proof.Status = "inconsistent"
			if err := p.rgbManager.projectionStore.SaveProofState(proof); err != nil {
				return nil, err
			}
			_ = p.utxoLockerL1.SetLockReason(proof.OutPoint, rgb11wallet.LockReasonRGB)
			continue
		}
		if _, expected := expectedSpends[proof.OutPoint]; expected && proof.Status == "spending" {
			_ = p.utxoLockerL1.SetLockReason(proof.OutPoint, rgb11wallet.LockReasonPending)
			continue
		}
		outspend, err := p.rgbManager.evidence.GetOutspend(proof.OutPoint)
		if err != nil {
			return nil, err
		}
		if outspend != nil && outspend.Spent {
			if _, expected := expectedSpends[proof.OutPoint]; expected {
				proof.Status = "spending"
				if outspend.SpendingTx == "" || outspend.SpendingTx == "unknown" {
					unresolvedInputs[proof.OutPoint] = true
				}
				_ = p.utxoLockerL1.SetLockReason(proof.OutPoint, rgb11wallet.LockReasonPending)
			} else {
				proof.Status = "inconsistent"
				result.Inconsistent = append(result.Inconsistent, proof.OutPoint)
				_ = p.utxoLockerL1.SetLockReason(proof.OutPoint, rgb11wallet.LockReasonRGB)
			}
			if err := p.rgbManager.projectionStore.SaveProofState(proof); err != nil {
				return nil, err
			}
			continue
		}
		status, statusErr := p.rgbManager.evidence.GetTxStatus(proof.WitnessTxID)
		if statusErr != nil || status == nil {
			if allocationOutpointTxID(proof.OutPoint) == proof.WitnessTxID {
				if utxo, utxoErr := p.rgbManager.evidence.GetUTXO(proof.OutPoint); utxoErr == nil && utxo != nil {
					status = &rgb11wallet.BitcoinTxStatus{
						TxID: proof.WitnessTxID, InMempool: utxo.Confirmations == 0,
						Confirmed: utxo.Confirmations > 0, Confirmations: utxo.Confirmations,
					}
				}
			}
			if status == nil {
				status = &rgb11wallet.BitcoinTxStatus{
					TxID: proof.WitnessTxID,
				}
			}
		}
		wasSettled := proof.Status == "settled"
		required := requirements[proof.OutPoint]
		if required < 1 {
			required = 1
		}
		if status != nil && status.Confirmed && status.Confirmations >= required {
			proof.Status = "settled"
			proof.Confirmations = status.Confirmations
			if err := p.utxoLockerL1.SetLockReason(proof.OutPoint, rgb11wallet.LockReasonRGB); err != nil {
				return nil, err
			}
		} else {
			proof.Status = "valid"
			if status != nil {
				proof.Confirmations = status.Confirmations
			} else {
				proof.Confirmations = 0
			}
			if err := p.utxoLockerL1.SetLockReason(proof.OutPoint, rgb11wallet.LockReasonPending); err != nil {
				return nil, err
			}
			if wasSettled {
				result.Reorged++
			}
		}
		if err := p.rgbManager.projectionStore.SaveProofState(proof); err != nil {
			return nil, err
		}
	}
	if len(result.Inconsistent) > 0 {
		p.rgbManager.consistencyStatus = "broken"
		return result, fmt.Errorf("%w: unknown or conflicting RGB11 spend", ErrRGB11Inconsistent)
	}
	if len(unresolvedInputs) > 0 {
		p.rgbManager.consistencyStatus = "warning"
	} else {
		p.rgbManager.consistencyStatus = "ok"
	}
	p.autoBackupRGB11AfterMutation()
	return result, nil
}

func (p *rgb11Manager) applyRGB11LocalChange(ctx context.Context, pending *rgb11wallet.PendingTransfer,
	status *rgb11wallet.BitcoinTxStatus) error {
	validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(pending.ChangeSeals...)
	receipt, err := p.rgbManager.projectionStore.ValidateAndStoreConsignment(ctx, validator, p.rgbManager.evidence, pending.LocalConsignment)
	if err != nil {
		return err
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return err
	}
	replacements := make([]rgb11wallet.ProjectionReplacement, 0)
	wantPrefix := pending.State.WitnessTxID + ":"
	for _, allocation := range receipt.Allocations {
		if !strings.HasPrefix(allocation.OutPoint, wantPrefix) || !allocation.WitnessTxPtr {
			continue
		}
		matched := false
		for _, changeSeal := range pending.ChangeSeals {
			if changeSeal.Vout == outpointVoutMust(allocation.OutPoint) && changeSeal.Blinding == allocation.SealBlinding {
				strict, strictErr := changeSeal.StrictBytes()
				matched = strictErr == nil && bytes.Equal(strict, allocation.SealDisclosure)
				if matched {
					break
				}
			}
		}
		if !matched {
			continue
		}
		utxo, err := p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
		if err != nil || utxo == nil {
			return fmt.Errorf("resolve RGB11 change %s: %w", allocation.OutPoint, err)
		}
		output := indexer.NewTxOutput(utxo.Value)
		output.OutPointStr = allocation.OutPoint
		output.OutValue.PkScript = append([]byte(nil), utxo.PkScript...)
		binding, err := p.rgb11CarrierBinding(allocation, utxo)
		if err != nil {
			return err
		}
		asset := &indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}
		commitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, allocation.SealDisclosure)
		proofStatus := "valid"
		confirmations := int64(0)
		if status != nil && status.Confirmed &&
			status.Confirmations >= max(int64(pending.State.MinConfirmations), 1) {
			proofStatus, confirmations = "settled", status.Confirmations
		} else if status != nil {
			confirmations = status.Confirmations
		}
		proof := &rgb11wallet.AllocationProof{
			OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
			OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType,
			AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
			StateData:       append([]byte(nil), allocation.StateData...),
			SealCommitment:  hex.EncodeToString(commitment[:]),
			SealDisclosure:  append([]byte(nil), allocation.SealDisclosure...),
			ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
			WitnessTxID: pending.State.WitnessTxID, Status: proofStatus, Confirmations: confirmations,
			CarrierBinding: binding,
		}
		replacements = append(replacements, rgb11wallet.ProjectionReplacement{Output: output, Asset: asset, Proof: proof})
	}
	if len(replacements) > 0 {
		if err := p.rgbManager.projectionStore.StageProjections(replacements); err != nil {
			return err
		}
		for _, replacement := range replacements {
			reason := rgb11wallet.LockReasonPending
			if replacement.Proof.Status == "settled" {
				reason = rgb11wallet.LockReasonRGB
			}
			if err := p.utxoLockerL1.SetLockReason(replacement.Proof.OutPoint, reason); err != nil {
				return err
			}
		}
	}
	inputs := make(map[string]bool, len(pending.State.InputOutPoints))
	for _, outpoint := range pending.State.InputOutPoints {
		inputs[outpoint] = true
	}
	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return err
	}
	for _, proof := range proofs {
		if !inputs[proof.OutPoint] || proof.Status == "spending" {
			continue
		}
		proof.Status = "spending"
		if err := p.rgbManager.projectionStore.SaveProofState(proof); err != nil {
			return err
		}
		if err := p.utxoLockerL1.SetLockReason(proof.OutPoint, rgb11wallet.LockReasonPending); err != nil {
			return err
		}
	}
	return nil
}

func outpointVoutMust(outpoint string) uint32 {
	vout, _ := outpointVout(outpoint)
	return vout
}
