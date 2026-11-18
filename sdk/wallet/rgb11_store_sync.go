package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"

	indexer "github.com/sat20-labs/indexer/common"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func rgb11SnapshotHasState(snapshot *RGB11WalletSnapshot) bool {
	return snapshot != nil && (len(snapshot.ProjectionRecords) != 0 ||
		len(snapshot.EngineRecords) != 0)
}

func (p *rgb11Manager) rgb11AccountManagedScopeID() (string, error) {
	if p == nil || p.status == nil || p.wallet == nil || p.status.CurrentWallet == 0 ||
		p.wallet.GetSubAccount() != p.status.CurrentAccount {
		return "", ErrRGB11Inconsistent
	}
	owner := p.accountManagementOwner()
	if owner == nil {
		return "", ErrRGB11Inconsistent
	}
	catalog, err := owner.accountManagedDataCatalog()
	if err != nil {
		return "", err
	}
	fingerprint := walletFingerprint(p.wallet)
	for _, scope := range catalog.Scopes {
		if scope.WalletID == p.status.CurrentWallet && scope.AccountIndex == p.status.CurrentAccount &&
			scope.WalletFingerprint == fingerprint {
			return scope.ID(), nil
		}
	}
	return "", ErrRGB11Inconsistent
}

// importRGB11WalletSnapshot restores only module-owned local state. DKVS,
// retention, AUTOPAY and wallet/account catalog lifecycle are owned by account
// management and are deliberately absent from this method.
func (p *rgb11Manager) importRGB11WalletSnapshot(snapshot *RGB11WalletSnapshot) error {
	if p == nil || snapshot == nil {
		return ErrRGB11Inconsistent
	}
	manager, err := p.fixedRGB11NetworkScope()
	if err != nil {
		return err
	}
	return manager.importRGB11WalletSnapshotLocal(snapshot)
}

// importRGB11WalletSnapshotLocal validates the local material before restore.
// Optional naming evidence during restore is fetched without a shared lock.
func (p *rgb11Manager) importRGB11WalletSnapshotLocal(snapshot *RGB11WalletSnapshot) error {
	if p == nil || snapshot == nil || p.rgbManager == nil ||
		p.rgbManager.engineStore == nil || p.rgbManager.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	if len(snapshot.ProjectionRecords) != 0 || len(snapshot.EngineRecords) != 0 {
		if err := rgb11wallet.ValidateWalletSnapshot(snapshot); err != nil {
			return err
		}
	}
	return p.restoreRGB11AccountSnapshotLocal(snapshot)
}

// restoreRGB11AccountSnapshot is the ordinary exact-rollback entrypoint.
// It captures the source scope and compares the local baseline at commit.
func (p *rgb11Manager) restoreRGB11AccountSnapshot(snapshot *RGB11WalletSnapshot) error {
	if p == nil || snapshot == nil {
		return ErrRGB11Inconsistent
	}
	manager, err := p.fixedRGB11NetworkScope()
	if err != nil {
		return err
	}
	return manager.restoreRGB11AccountSnapshotLocal(snapshot)
}

// restoreRGB11AccountSnapshotLocal restores an exact previously exported
// snapshot after a later scope in a multi-scope apply failed. It does not
// select or merge lifecycle history. Every restored RGB carrier is protected
// as pending until the next fresh chain reconciliation.
func (p *rgb11Manager) restoreRGB11AccountSnapshotLocal(snapshot *RGB11WalletSnapshot) error {
	return p.restoreRGB11AccountSnapshotIfCurrent(snapshot, nil)
}

func (p *rgb11Manager) restoreRGB11AccountSnapshotIfCurrent(snapshot, expected *RGB11WalletSnapshot) error {
	return p.restoreRGB11AccountSnapshotIfCurrentMode(snapshot, expected, true, nil)
}

func (p *rgb11Manager) restoreRGB11AccountSnapshotIfCurrentMode(snapshot, expected *RGB11WalletSnapshot,
	localMutation bool, swap *rgb11ImportSwap) error {
	if p == nil || snapshot == nil || p.rgbManager == nil ||
		p.rgbManager.engineStore == nil || p.rgbManager.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	if len(snapshot.ProjectionRecords) != 0 || len(snapshot.EngineRecords) != 0 {
		if err := rgb11wallet.ValidateWalletSnapshot(snapshot); err != nil {
			return err
		}
	}
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return err
	}
	current := expected
	if swap == nil {
		current, _, err = p.exportRGB11WalletSnapshot(walletID)
		if err != nil {
			return err
		}
		if expected != nil && rgb11SnapshotChanged(expected, current) {
			return errAccountSnapshotChanged
		}
	} else if current == nil || current.WalletID != walletID || current != swap.from.snapshot || snapshot != swap.to.snapshot {
		return ErrRGB11Inconsistent
	}
	tickerInfos, err := p.tickerInfosFromRGB11Snapshot(snapshot)
	if err != nil {
		return err
	}
	for index, info := range tickerInfos {
		var ext rgb11wallet.TickerExt
		if err := json.Unmarshal(info.Content, &ext); err != nil {
			return err
		}
		info, err = p.withRGB11NamingOriginFromRaw(info, rgb11SnapshotObject(snapshot.ProjectionRecords, ext.ContractHash))
		if err != nil {
			return err
		}
		tickerInfos[index] = info
	}
	reasons, err := rgb11SnapshotCarrierLockReasons(snapshot.ProjectionRecords)
	if err != nil {
		return err
	}
	lockPlan, err := p.rgb11SnapshotLockPlan(current, snapshot, reasons)
	if err != nil {
		return err
	}
	status := "ok"
	if swap != nil {
		inconsistent, err := rgb11SnapshotInconsistentOutpoints(snapshot)
		if err != nil {
			return err
		}
		if len(inconsistent) != 0 || len(swap.to.faults) != 0 {
			status = "broken"
		}
	}
	if err := p.importRGB11ReservationSnapshotWithMutation(context.Background(), snapshot, tickerInfos, nil, &lockPlan, nil, current, localMutation, nil, swap); err != nil {
		return err
	}
	p.rgbManager.consistencyStatus = status
	if swap != nil {
		p.publishRGB11ConsistencyStatus()
	}
	return nil
}

func (p *rgb11Manager) importRGB11AccountRecoverySnapshot(snapshot *RGB11WalletSnapshot) error {
	if p == nil || snapshot == nil {
		return ErrRGB11Inconsistent
	}
	return p.importRGB11AccountRecoverySnapshotResolved(snapshot, false)
}

// importSelectedRGB11AccountRecoverySnapshot imports a recovery closure whose
// selection was already authenticated by account-managed state. Remote evidence
// is resolved without a lock and the local baseline is checked at commit.
func (p *rgb11Manager) importSelectedRGB11AccountRecoverySnapshot(snapshot *RGB11WalletSnapshot) error {
	if p == nil || snapshot == nil {
		return ErrRGB11Inconsistent
	}
	return p.importRGB11AccountRecoverySnapshotResolved(snapshot, true)
}

func (p *rgb11Manager) importRGB11AccountRecoverySnapshotResolved(snapshot *RGB11WalletSnapshot,
	selected bool) error {
	_, err := p.importRGB11AccountRecoverySnapshotResult(snapshot, selected)
	return err
}

func (p *rgb11Manager) importRGB11AccountRecoverySnapshotResult(snapshot *RGB11WalletSnapshot,
	selected bool) (*RGB11WalletSnapshot, error) {
	if p == nil || snapshot == nil {
		return nil, ErrRGB11Inconsistent
	}
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	if selected {
		base, err := p.readRGB11SelectedBase(walletID)
		if err != nil {
			return nil, err
		}
		return p.applyRGB11Recovery(base.snapshot, snapshot, true, &rgb11ImportSwap{from: base, to: &rgb11ImportState{}})
	}
	current, _, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		return nil, err
	}
	return p.applyRGB11Recovery(current, snapshot, true, nil)
}

func (p *rgb11Manager) applyRGB11Recovery(current, snapshot *RGB11WalletSnapshot,
	localMutation bool, swap *rgb11ImportSwap) (*RGB11WalletSnapshot, error) {
	var merged *RGB11WalletSnapshot
	var faults []rgb11wallet.ProofFault
	var err error
	if swap != nil {
		faults = swap.from.faults
		merged, err = rgb11wallet.MergeSelectedRecoverySnapshot(current, snapshot)
	} else {
		merged, err = rgb11wallet.MergeRecoverySnapshot(current, snapshot)
	}
	if err != nil {
		return nil, err
	}
	var expandedReceipts []string
	if swap != nil {
		local, loadErr := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(current.ProjectionRecords)
		if loadErr != nil {
			return nil, loadErr
		}
		incoming, loadErr := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(merged.ProjectionRecords)
		if loadErr != nil {
			return nil, loadErr
		}
		for hash, receipt := range incoming {
			if previous := local[hash]; previous != nil && len(receipt.Allocations) > len(previous.Allocations) {
				expandedReceipts = append(expandedReceipts, hash)
			}
		}
	}
	resolved, err := p.resolveRGB11Recovery(context.Background(), merged, expandedReceipts, faults)
	if err != nil {
		return nil, err
	}
	lockPlan, err := p.rgb11SnapshotLockPlan(current, resolved.snapshot, resolved.lockReasons)
	if err != nil {
		return nil, err
	}
	inconsistent, err := rgb11SnapshotInconsistentOutpoints(resolved.snapshot)
	if err != nil {
		return nil, err
	}
	// No fallible work after publication: the account provider must retain
	// the exact before/after pair for every scope it actually committed.
	if err := p.importRGB11ReservationSnapshotWithMutation(context.Background(), resolved.snapshot,
		resolved.tickerInfos, resolved.lockReasons, &lockPlan, resolved.checkEvidence, current, localMutation, nil, swap); err != nil {
		return nil, err
	}
	p.rgbManager.consistencyStatus = "ok"
	if len(inconsistent) != 0 {
		p.rgbManager.consistencyStatus = "broken"
	}
	if swap != nil {
		p.publishRGB11ConsistencyStatus()
	}
	return resolved.snapshot, nil
}

// applyRGB11CandidateSnapshot resolves one fully constructed local candidate
// against a stable Bitcoin view, derives its carrier lock plan, and publishes
// engine records, projection records, ticker metadata, reservation records,
// lock rows, and owner RAM through the existing atomic wallet batch.
// Network resolution is lock-free. Publication compares the local baseline
// inside the existing reservation commit gate before replacing any records.
func (p *rgb11Manager) applyRGB11CandidateSnapshot(ctx context.Context,
	current, candidate *RGB11WalletSnapshot) (*RGB11WalletSnapshot, error) {
	if p == nil || current == nil || candidate == nil || p.rgbManager == nil {
		return nil, ErrRGB11Inconsistent
	}
	if current.WalletID != candidate.WalletID || current.AccountIndex != candidate.AccountIndex ||
		current.EngineBuildID != candidate.EngineBuildID {
		return nil, ErrRGB11Inconsistent
	}
	resolved, err := p.resolveRGB11Local(ctx, candidate)
	if errors.Is(err, errRGB11TipAdvanced) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("resolve RGB11 candidate projection: %w", err)
	}
	return p.publishRGB11CandidateSnapshot(ctx, current, resolved)
}

// publishRGB11CandidateSnapshot retains the same evidence recheck and atomic
// baseline-checked publication for newly resolved and unchanged candidates.
func (p *rgb11Manager) publishRGB11CandidateSnapshot(ctx context.Context,
	current *RGB11WalletSnapshot, resolved *rgb11ResolvedRecoveryProjection) (*RGB11WalletSnapshot, error) {
	var err error
	resolved.snapshot.ProjectionRecords, err = rgb11wallet.HydrateLocalProjection(resolved.snapshot.ProjectionRecords, current.ProjectionRecords, resolved.faults)
	if err != nil {
		return nil, err
	}
	lockPlan, err := p.rgb11SnapshotLockPlan(current, resolved.snapshot, resolved.lockReasons, resolved.faults...)
	if err != nil {
		return nil, fmt.Errorf("plan RGB11 candidate locks: %w", err)
	}
	if err := p.importRGB11ReservationSnapshotWithMutation(ctx, resolved.snapshot,
		resolved.tickerInfos, resolved.lockReasons, &lockPlan, resolved.checkEvidence, current, true, resolved, nil); err != nil {
		return nil, fmt.Errorf("publish RGB11 candidate projection: %w", err)
	}
	inconsistent, err := rgb11SnapshotInconsistentOutpoints(resolved.snapshot)
	if err != nil {
		return nil, err
	}
	p.rgbManager.consistencyStatus = "ok"
	if len(inconsistent) != 0 || len(resolved.faults) != 0 {
		p.rgbManager.consistencyStatus = "broken"
	}

	return resolved.snapshot, nil
}

func rgb11SnapshotObject(records []rgb11wallet.SnapshotRecord, hash string) []byte {
	if hash == "" {
		return nil
	}
	key := "object-" + hash
	for _, record := range records {
		if record.Key == key {
			return append([]byte(nil), record.Value...)
		}
	}
	return nil
}

func rgb11SnapshotCarrierLockReasons(records []rgb11wallet.SnapshotRecord) (map[string]string, error) {
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(records)
	if err != nil {
		return nil, err
	}
	reasons := make(map[string]string, len(proofs))
	for _, proof := range proofs {
		if previous := reasons[proof.OutPoint]; previous != "" && previous != sdkcommon.UtxoLockRGBPending {
			return nil, ErrRGB11Inconsistent
		}
		// Keep every known carrier unavailable to ordinary BTC selection until
		// a fresh chain frontier classifies its proof and confirmation depth.
		reasons[proof.OutPoint] = sdkcommon.UtxoLockRGBPending
	}
	return reasons, nil
}

func (p *rgb11Manager) tickerInfosFromRGB11Snapshot(snapshot *RGB11WalletSnapshot) ([]*indexer.TickerInfo, error) {
	if p == nil || snapshot == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	refs, err := rgb11wallet.TickerRefsFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	infos := make([]*indexer.TickerInfo, 0, len(refs))
	for _, ref := range refs {
		name := indexer.NewAssetNameFromString(ref.AssetName)
		if name.Protocol != rgb11wallet.Protocol || name.String() != ref.AssetName {
			return nil, ErrRGB11Inconsistent
		}
		if info := p.getTickerInfo(name); info != nil {
			var ext rgb11wallet.TickerExt
			if json.Unmarshal(info.Content, &ext) != nil || ext.ContractID != ref.ContractID {
				return nil, ErrRGB11Inconsistent
			}
			continue
		}
		raw, receipt, err := rgb11wallet.ContractObjectForTickerRef(snapshot.ProjectionRecords, ref)
		if err != nil {
			return nil, err
		}
		container, err := coreconsignment.Decode(raw)
		if err != nil || container.ContractID != ref.ContractID {
			return nil, ErrRGB11Inconsistent
		}
		info, err := rgb11TickerInfoFromValidatedContract(container, receipt)
		if err != nil || info.AssetName.String() != ref.AssetName {
			return nil, ErrRGB11Inconsistent
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// resolveRGB11Local validates current ownership independently of frozen local
// faults. The complete candidate and every damaged byte survive the work view.
func (p *rgb11Manager) resolveRGB11Local(ctx context.Context, full *RGB11WalletSnapshot) (*rgb11ResolvedRecoveryProjection, error) {
	var faults []rgb11wallet.ProofFault
	for {
		view, err := rgb11wallet.SplitLocalProjection(full.ProjectionRecords, faults...)
		if err != nil {
			return nil, err
		}
		work := *full
		work.ProjectionRecords = view.Records
		resolved, err := p.resolveRGB11RecoveryProjection(ctx, &work)
		if err != nil {
			var fault *rgb11CarrierFault
			if !errors.As(err, &fault) || view.Points[fault.fault.OutPoint] || ctx.Err() != nil {
				return nil, err
			}
			// Each pass removes at least one actual owned carrier; a duplicate
			// or an unlocatable failure terminates instead of retrying forever.
			faults = append(faults, fault.fault)
			continue
		}
		resolved.snapshot.ProjectionRecords, err = view.Merge(resolved.snapshot.ProjectionRecords)
		if err != nil {
			return nil, err
		}
		owned, err := rgb11wallet.OutputsFromProjectionSnapshot(full.ProjectionRecords)
		if err != nil {
			return nil, err
		}
		locks := p.utxoLockerL1.GetLockedUtxoList()
		for point := range view.Points {
			// Freeze an entire operation without claiming foreign recipient
			// outputs as new local carriers. Existing fee/change owners survive.
			if owned[point] != nil || locks[point] != nil {
				resolved.lockReasons[point] = sdkcommon.UtxoLockRGBPending
			}
		}
		resolved.faults = view.Faults
		return resolved, nil
	}
}
