package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
	p.markRGB11ProjectionUnverified()
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
	if p == nil || snapshot == nil || p.rgbManager == nil ||
		p.rgbManager.engineStore == nil || p.rgbManager.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	p.markRGB11ProjectionUnverified()
	if len(snapshot.ProjectionRecords) != 0 || len(snapshot.EngineRecords) != 0 {
		if err := rgb11wallet.ValidateWalletSnapshot(snapshot); err != nil {
			p.markRGB11ProjectionUnavailable()
			return err
		}
	}
	walletID, err := p.RGB11WalletID()
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return err
	}
	current, _, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return err
	}
	if expected != nil && rgb11SnapshotChanged(expected, current) {
		return errAccountSnapshotChanged
	}
	tickerInfos, err := p.tickerInfosFromRGB11Snapshot(snapshot)
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return err
	}
	for index, info := range tickerInfos {
		var ext rgb11wallet.TickerExt
		if err := json.Unmarshal(info.Content, &ext); err != nil {
			p.markRGB11ProjectionUnavailable()
			return err
		}
		info, err = p.withRGB11NamingOriginFromRaw(info, rgb11SnapshotObject(snapshot.ProjectionRecords, ext.ContractHash))
		if err != nil {
			p.markRGB11ProjectionUnavailable()
			return err
		}
		tickerInfos[index] = info
	}
	lockReasons, err := rgb11SnapshotCarrierLockReasons(snapshot.ProjectionRecords)
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return err
	}
	lockPlan, err := p.rgb11SnapshotLockPlan(current, snapshot, lockReasons)
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return err
	}
	if err := p.importRGB11ReservationSnapshotWithBaseline(context.Background(), snapshot, tickerInfos, lockReasons, &lockPlan, nil, current); err != nil {
		p.markRGB11ProjectionUnavailable()
		return err
	}
	p.markRGB11ProjectionUnavailable()
	p.rgbManager.consistencyStatus = "ok"
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
	p.markRGB11ProjectionUnverified()
	walletID, err := p.RGB11WalletID()
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return nil, err
	}
	current, _, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return nil, err
	}
	var merged *RGB11WalletSnapshot
	if selected {
		merged, err = rgb11wallet.MergeSelectedRecoverySnapshot(current, snapshot)
	} else {
		merged, err = rgb11wallet.MergeRecoverySnapshot(current, snapshot)
	}
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return nil, err
	}
	var expandedReceipts []string
	if selected {
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
	resolved, err := p.resolveRGB11RecoveryProjection(context.Background(), merged, expandedReceipts...)
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return nil, err
	}
	lockPlan, err := p.rgb11SnapshotLockPlan(current, resolved.snapshot, resolved.lockReasons)
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return nil, err
	}
	if err := p.importRGB11ReservationSnapshotWithBaseline(context.Background(), resolved.snapshot,
		resolved.tickerInfos, resolved.lockReasons, &lockPlan, resolved.checkEvidence, current); err != nil {
		p.markRGB11ProjectionUnavailable()
		return nil, err
	}
	inconsistent, err := rgb11SnapshotInconsistentOutpoints(resolved.snapshot)
	if err != nil {
		return nil, err
	}
	p.rgbManager.consistencyStatus = "ok"
	if len(inconsistent) != 0 {
		p.rgbManager.consistencyStatus = "broken"
	}
	if resolved.complete {
		p.markRGB11ProjectionVerified()
	} else {
		p.markRGB11ProjectionUnavailable()
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
	if err := rgb11wallet.ValidateWalletSnapshot(candidate); err != nil {
		return nil, fmt.Errorf("validate RGB11 candidate snapshot: %w", err)
	}
	resolved, err := p.resolveRGB11RecoveryProjection(ctx, candidate)
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return nil, fmt.Errorf("resolve RGB11 candidate projection: %w", err)
	}
	lockPlan, err := p.rgb11SnapshotLockPlan(current, resolved.snapshot, resolved.lockReasons)
	if err != nil {
		p.markRGB11ProjectionUnavailable()
		return nil, fmt.Errorf("plan RGB11 candidate locks: %w", err)
	}
	if err := p.importRGB11ReservationSnapshotWithBaseline(ctx, resolved.snapshot,
		resolved.tickerInfos, resolved.lockReasons, &lockPlan, resolved.checkEvidence, current); err != nil {
		p.markRGB11ProjectionUnavailable()
		return nil, fmt.Errorf("publish RGB11 candidate projection: %w", err)
	}
	inconsistent, err := rgb11SnapshotInconsistentOutpoints(resolved.snapshot)
	if err != nil {
		return nil, err
	}
	p.rgbManager.consistencyStatus = "ok"
	if len(inconsistent) != 0 {
		p.rgbManager.consistencyStatus = "broken"
	}
	if resolved.complete {
		p.markRGB11ProjectionVerified()
	} else {
		p.markRGB11ProjectionUnavailable()
	}
	return resolved.snapshot, nil
}

func (p *rgb11Manager) importRGB11ActiveRecoverySnapshot(snapshot *RGB11WalletSnapshot) error {
	if p == nil || snapshot == nil {
		return ErrRGB11Inconsistent
	}
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return err
	}
	current, _, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		return err
	}
	merged, err := rgb11wallet.MergeActiveRecoverySnapshot(current, snapshot)
	if err != nil {
		return err
	}
	return p.importRGB11WalletSnapshot(merged)
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
		if previous := reasons[proof.OutPoint]; previous != "" && previous != rgb11wallet.LockReasonPending {
			return nil, ErrRGB11Inconsistent
		}
		// Keep every known carrier unavailable to ordinary BTC selection until
		// a fresh chain frontier classifies its proof and confirmation depth.
		reasons[proof.OutPoint] = rgb11wallet.LockReasonPending
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

func (p *Manager) registerDKVSDomainObservers() {
	if p == nil || p.dkvs == nil {
		return
	}
	p.dkvs.addObserver(func(paths []string) {
		p.scheduleMailboxRefreshForChanges(paths)
		if err := p.SyncAccountManagementState(nil); err != nil &&
			!errors.Is(err, ErrDKVSPathNotSynced) &&
			!errors.Is(err, ErrDKVSRecordNotFound) {
			Log.Warningf("apply account management replica failed: %v", err)
		}
	})
}
