package wallet

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// cancelUnpublishedRGB11Transfer is used only by an explicit OOB cancel after
// the ordinary account merge reports a conflict. It never publishes a local
// loser over the selected account state. The complete local recovery delta must
// consist solely of this prepared batch, absent from both confirmed baselines.
func (p *Manager) cancelUnpublishedRGB11Transfer(transferID string) error {
	ctx := context.Background()
	return p.runAccountOperation(ctx, func() error {
		if !p.accountManagedRecoveryConfigured() {
			return ErrDKVSPathNotSynced
		}
		manager, err := p.captureRGB11NetworkScope(false)
		if err != nil {
			return err
		}
		if err := manager.checkRGB11OperationSource(); err != nil {
			return err
		}
		p.mutex.Lock()
		snapshot, _, err := p.captureAccountManagementSyncSnapshotBaseLocked()
		p.mutex.Unlock()
		if err != nil {
			return err
		}
		if snapshot == nil {
			return ErrDKVSPathNotSynced
		}
		defer zeroBytes(snapshot.secret)
		base, err := openProfileManagedDataBundle(snapshot.profile, snapshot.secret)
		if err != nil {
			return err
		}
		scopeID, err := manager.rgb11AccountManagedScopeID()
		if err != nil {
			return err
		}
		baseRecovery, err := rgb11RecoveryForManagedScope(base, scopeID)
		if err != nil {
			return err
		}
		walletID, err := manager.RGB11WalletID()
		if err != nil {
			return err
		}
		current, _, err := manager.exportRGB11WalletSnapshot(walletID)
		if err != nil {
			return err
		}
		localRecovery, err := rgb11wallet.RecoveryPackageFromSnapshot(current, 0)
		if err != nil {
			return err
		}
		batch, err := unpublishedRGB11Batch(localRecovery, baseRecovery, transferID)
		if err != nil {
			return err
		}
		if err := manager.verifyExpiredRGB11Reservation(batch); err != nil {
			return err
		}
		store, err := p.accountDKVSStore()
		if err != nil {
			return err
		}
		stateKey, err := p.accountManagedStateKey(snapshot.root)
		if err != nil {
			return err
		}
		dataKey, err := p.accountManagedDataBlobKey(snapshot.root)
		if err != nil {
			return err
		}
		// A response loss may leave a publication in flight. Even an unrelated
		// generation cannot be ignored here: its signed payload may contain this batch.
		entries, err := newDKVSReplicaStore(p.db).LoadOutbox(store.client.replicaNamespace)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if _, matches := accountManagedEntryRecords(entry, stateKey, dataKey); matches {
				return ErrDKVSPathNotSynced
			}
		}
		stateValue, err := store.GetAuthoritative(stateKey)
		if err != nil {
			return err
		}
		remoteState, err := account.OpenManagedState(snapshot.secret, snapshot.profile.AccountID, stateValue.Value)
		if err != nil {
			return err
		}
		if remoteState.Revision < snapshot.profile.StateSeq || remoteState.DataRevision < snapshot.profile.ManagedDataRevision {
			return ErrRGB11Inconsistent
		}
		dataValue, err := store.GetAuthoritative(dataKey)
		if err != nil {
			return err
		}
		remote, err := openAccountManagedDataValue(snapshot.secret, snapshot.profile.AccountID, dataValue, remoteState)
		if err != nil {
			return err
		}
		selected, err := rgb11RecoveryForManagedScope(remote.Bundle, scopeID)
		if err != nil {
			return err
		}
		selectedTransfers, err := rgb11wallet.TransfersFromProjectionSnapshot(selected.ProjectionRecords)
		if err != nil {
			return err
		}
		for _, published := range selectedTransfers {
			for _, private := range batch {
				if published.TransferID == private.State.TransferID || published.WitnessTxID == private.State.WitnessTxID ||
					published.ConsignmentHash == private.State.ConsignmentHash {
					return ErrRGB11OutOfBandRequired
				}
				if rgb11TransferKeepsInputsLocked(published) && slices.ContainsFunc(published.InputOutPoints, func(point string) bool {
					return slices.Contains(private.State.InputOutPoints, point)
				}) {
					return ErrRGB11ManagedOperationActive
				}
			}
		}
		// A coherent authenticated state/blob pair, followed by another state
		// read, excludes a concurrent account CAS during the observation.
		latest, err := store.GetAuthoritative(stateKey)
		if err != nil {
			return err
		}
		if latest.Hash != stateValue.Hash {
			return errAccountSnapshotChanged
		}
		baseSnapshot, err := baseRecovery.WalletSnapshot()
		if err != nil {
			return err
		}
		selectedSnapshot, err := selected.WalletSnapshot()
		if err != nil {
			return err
		}
		// Preserve all previously shared responsibilities and immutable identities.
		if _, err := rgb11wallet.MergeSelectedRecoverySnapshot(baseSnapshot, selectedSnapshot); err != nil {
			return err
		}
		p.mutex.RLock()
		unchanged := p.accountSyncSnapshotCurrentLocked(snapshot)
		p.mutex.RUnlock()
		if !unchanged {
			return errAccountSnapshotChanged
		}
		// Drop only the proven private delta, atomically restoring its confirmed
		// baseline and locks. Normal sync can then select even a newer remote CAS:
		// local == base, so a concurrent remote revision cannot strand this scope
		// in a second opaque three-way conflict after the private ID is gone.
		if _, err := manager.applyRGB11CandidateSnapshot(ctx, current, baseSnapshot); err != nil {
			return err
		}
		return p.syncAccountManagementStateMode(ctx, 0, true, false)
	})
}

func rgb11RecoveryForManagedScope(bundle account.ManagedDataBundle, scope string) (*rgb11wallet.RecoveryPackage, error) {
	for _, item := range bundle.Items {
		if item.Provider == rgb11AccountManagedProviderID && item.Scope == scope {
			return rgb11wallet.DecodeRecoveryPackage(item.Payload)
		}
	}
	return nil, ErrRGB11Inconsistent
}

func unpublishedRGB11Batch(local, base *rgb11wallet.RecoveryPackage, transferID string) ([]*rgb11wallet.PendingTransfer, error) {
	if local == nil || base == nil || local.WalletID != base.WalletID || local.AccountIndex != base.AccountIndex ||
		local.EngineBuildID != base.EngineBuildID || !sameRGB11SnapshotRecords(local.EngineRecords, base.EngineRecords) {
		return nil, ErrRGB11Inconsistent
	}
	pendings, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(local.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*rgb11wallet.PendingTransfer, len(pendings))
	for _, pending := range pendings {
		byID[pending.State.TransferID] = pending
	}
	first := byID[transferID]
	if first == nil {
		return nil, ErrRGB11OutOfBandRequired
	}
	ids := first.State.BatchTransferIDs
	if len(ids) == 0 {
		ids = []string{transferID}
	}
	if len(ids) > 32 || len(ids) != first.State.BatchSize || len(ids) > 1 && first.State.BatchID == "" {
		return nil, ErrRGB11Inconsistent
	}
	allowed := make(map[string]bool, len(ids)*3)
	batch := make([]*rgb11wallet.PendingTransfer, 0, len(ids))
	for index, id := range ids {
		pending := byID[id]
		if pending == nil || pending.State.Direction != "send" || pending.State.Status != "prepared" ||
			pending.State.TransportMode != "out-of-band" || pending.State.AddressMode ||
			pending.State.AckStatus != "awaiting" || pending.State.DeliveryAcknowledged || pending.ChannelSend != nil ||
			pending.State.NetworkBackupRef != "" || pending.State.DKVSOperationID != "" ||
			pending.State.WitnessTxID != first.State.WitnessTxID || pending.State.BatchID != first.State.BatchID ||
			pending.State.BatchSize != len(ids) || !slices.Equal(pending.State.BatchTransferIDs, first.State.BatchTransferIDs) ||
			pending.ReservationID == "" || pending.ReservationID != first.ReservationID ||
			!bytes.Equal(pending.SignedTx, first.SignedTx) || validateRGB11PendingTransaction(pending) != nil {
			return nil, ErrRGB11OutOfBandRequired
		}
		if len(ids) > 1 && (id != rgb11BatchRecipientID(first.State.BatchID, uint32(index)) || pending.State.RecipientVout != uint32(index+1)) {
			return nil, ErrRGB11Inconsistent
		}
		if allowed["pending-"+id] {
			return nil, ErrRGB11Inconsistent
		}
		allowed["pending-"+id] = true
		allowed["object-"+pending.RecipientObjectHash] = true
		allowed["object-"+pending.LocalObjectHash] = true
		batch = append(batch, pending)
	}
	if !allowed["pending-"+transferID] {
		return nil, ErrRGB11Inconsistent
	}
	baseRecords := make(map[string][]byte, len(base.ProjectionRecords))
	for _, record := range base.ProjectionRecords {
		if allowed[record.Key] {
			return nil, ErrRGB11OutOfBandRequired
		}
		baseRecords[record.Key] = record.Value
	}
	remaining := 0
	for _, record := range local.ProjectionRecords {
		if allowed[record.Key] {
			continue
		}
		if expected, ok := baseRecords[record.Key]; !ok || !bytes.Equal(expected, record.Value) {
			return nil, fmt.Errorf("%w: private cancel would discard unrelated RGB11 recovery changes", ErrRGB11Inconsistent)
		}
		remaining++
	}
	if remaining != len(baseRecords) {
		return nil, ErrRGB11Inconsistent
	}
	return batch, nil
}
