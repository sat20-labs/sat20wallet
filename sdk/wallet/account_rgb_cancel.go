package wallet

import (
	"context"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"slices"
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
		scopeID, err := manager.rgb11AccountManagedScopeID()
		if err != nil {
			return err
		}
		if err := p.CheckAccountRestore(rgb11AccountManagedProviderID, scopeID); err != nil {
			return err
		}
		module, err := accountEntry(snapshot.secret, snapshot.profile.AccountID, rgb11AccountManagedProviderID, scopeID)
		if err != nil {
			return err
		}
		p.mutex.RLock()
		local, err := p.readAccountLocal(snapshot.profile.AccountID, module.Provider, module.Scope)
		p.mutex.RUnlock()
		if err != nil {
			return err
		}
		if local == nil || local.Deleted || local.Restoring || local.Baseline == nil {
			return ErrDKVSPathNotSynced
		}
		basePayload, err := openAccountPayload(snapshot.secret, snapshot.profile.AccountID, snapshot.network, module, local.Baseline.Value)
		if err != nil {
			return err
		}
		defer zeroBytes(basePayload)
		baseRecovery, err := rgb11wallet.DecodeRecoveryPackage(basePayload)
		if err != nil {
			return err
		}
		walletID, err := manager.RGB11WalletID()
		if err != nil {
			return err
		}
		current, backup, err := manager.readRGB11StrictViews(walletID, true)
		if err != nil {
			return err
		}
		// Match the confirmed backup's representation when proving the private
		// delta. Keep current's exact raw bytes for the final atomic comparison.
		localRecovery, err := rgb11wallet.RecoveryPackageFromSnapshot(backup, 0)
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
		// Only this independent module can contain the private batch. An
		// unrelated account/module publication cannot block cancellation.
		entries, err := newDKVSReplicaStore(p.db).LoadOutbox(store.client.replicaNamespace)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			mutations, err := entry.DecodeMutations()
			if err != nil {
				return err
			}
			for _, mutation := range mutations {
				if mutation.Record != nil && mutation.Record.Key == module.Key {
					return ErrDKVSPathNotSynced
				}
			}
		}
		indexKey, err := account.ManagedIndexKey(snapshot.profile.AccountID)
		if err != nil {
			return err
		}
		indexValue, err := store.GetAuthoritative(indexKey)
		if err != nil {
			return err
		}
		index, err := account.OpenManagedIndex(snapshot.secret, snapshot.profile.AccountID, snapshot.network, indexValue.Value)
		if err != nil {
			return err
		}
		if selected, ok := accountEntryMap(index)[module.Provider+"\x00"+module.Scope]; !ok || selected != module {
			return ErrRGB11OperationScopeChanged
		}
		dataValue, err := store.GetAuthoritative(module.Key)
		if err != nil {
			return err
		}
		if dataValue.record == nil || dataValue.record.Seq < local.Baseline.Seq {
			return ErrRGB11Inconsistent
		}
		payload, err := openAccountPayload(snapshot.secret, snapshot.profile.AccountID, snapshot.network, module, dataValue.Value)
		if err != nil {
			return err
		}
		defer zeroBytes(payload)
		selected, err := rgb11wallet.DecodeRecoveryPackage(payload)
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
		// Re-read both the directory and this module after validation.
		latest, err := store.GetAuthoritative(module.Key)
		if err != nil {
			return err
		}
		if latest.Hash != dataValue.Hash {
			return errAccountSnapshotChanged
		}
		latestIndex, err := store.GetAuthoritative(indexKey)
		if err != nil {
			return err
		}
		if latestIndex.Hash != indexValue.Hash {
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
		live, readErr := p.readAccountLocal(snapshot.profile.AccountID, module.Provider, module.Scope)
		unchanged := readErr == nil && live != nil && live.Generation == local.Generation &&
			!live.Restoring && !live.Deleted && p.accountSyncSnapshotCurrentLocked(snapshot)
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
		return nil
	})
}
