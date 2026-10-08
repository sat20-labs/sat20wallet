package wallet

import (
	"github.com/sat20-labs/sat20wallet/sdk/account"
)

// applyAccountManagedRebaseForSync applies a three-way merge locally before
// publishing it. The confirmed profile still points to the REMOTE baseline;
// all local pending mutations remain pending until the subsequent PUT is ACKed.
// This establishes the invariant required by the own-ACK path: every remote
// increment in an outgoing request has already been applied to the live catalog
// and providers, while edits made during transport remain independent overlays.
func (p *Manager) applyAccountManagedRebaseForSync(
	remote, merged account.ManagedState, snapshot *accountManagementSyncSnapshot,
	remoteEnvelope []byte, remoteData, mergedData *accountManagedDataSnapshot,
	applicationLocked bool) error {
	if snapshot == nil || remoteData == nil || mergedData == nil || len(remoteEnvelope) == 0 {
		return errAccountSnapshotChanged
	}
	p.mutex.Lock()
	base, err := p.captureAccountManagedCommitBaseLocked(snapshot)
	p.mutex.Unlock()
	if err != nil {
		return err
	}
	commit, err := p.prepareAccountManagedStateCommit(
		merged, snapshot, remoteEnvelope, mergedData, base)
	if err != nil {
		return err
	}
	if !commit.importManagedData {
		return errAccountSnapshotChanged
	}
	// The prepared wallet records contain the field-level merge, not an entire
	// wallet skipped because it has one local edit. Nothing has been published:
	// do not clear those edits or advance the confirmed baseline to the merge.
	commit.profile.Pending = append([]accountManagementMutation(nil), base.profile.Pending...)
	commit.profile.StateSeq = remote.Revision
	commit.profile.StateHash = accountStateDigest(remoteEnvelope)
	commit.profile.StateEnvelope = append([]byte(nil), remoteEnvelope...)
	commit.profile.ManagedDataRevision = remote.DataRevision
	commit.profile.ManagedDataHash = remoteData.Hash
	commit.profile.ManagedDataEnvelope = append([]byte(nil), remoteData.Envelope...)
	commit.profile.ManagedDataDirty = true
	commit.pendingRemains = len(commit.profile.Pending) != 0
	marker := accountManagedImportMarkerForProfile(accountManagedImportOriginRemoteApply,
		accountManagedImportStageLocalCommit, &commit.profile)
	marker.ConfirmedStateHash = commit.profile.StateHash
	marker.ReplayStateEnvelope, err = account.SealManagedState(snapshot.secret,
		snapshot.profile.AccountID, merged, nil)
	if err != nil {
		return err
	}
	marker.ReplayDataEnvelope = append([]byte(nil), mergedData.Envelope...)
	marker.TargetStateRevision, marker.TargetStateHash = merged.Revision, accountStateDigest(marker.ReplayStateEnvelope)
	marker.TargetDataRevision, marker.TargetDataHash = merged.DataRevision, merged.DataHash
	commit.importMarker = &marker
	commit.profileBytes, err = EncodeToBytes(&commit.profile)
	if err != nil {
		return err
	}
	return p.withAccountLocalState(applicationLocked, func() error {
		if err := p.checkAccountManagedDataImport(); err != nil {
			return err
		}
		// A concurrent local edit invalidates preparation before any marker or
		// durable write. Re-capture/re-merge instead of overwriting that edit.
		if _, _, err := p.applyAccountManagedCommitForSync(commit, snapshot); err != nil {
			return err
		}
		if err := p.updateAccountManagedDataImportStage(accountManagedImportStageProviderImport); err != nil {
			return err
		}
		if err := p.importAccountManagedDataSnapshot(mergedData); err != nil {
			return err
		}
		if err := p.finishAccountManagedRemoteImport(); err != nil {
			return err
		}
		p.markDKVSStateDirty()
		return nil
	})
}
