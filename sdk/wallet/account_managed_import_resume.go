package wallet

import (
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

// Caller owns the account operation and local application gates. Only a
// committed remote apply can resume here; restore/unknown markers stay closed.
func (p *Manager) resumeAccountManagedRemoteImport() error {
	marker, err := p.readAccountManagedDataImportMarker()
	if err != nil || marker == nil {
		return err
	}
	p.mutex.Lock()
	profile := p.accountProfile
	if marker.Version != accountManagedImportMarkerVersion || marker.Origin != accountManagedImportOriginRemoteApply ||
		profile == nil || len(p.accountSecret) != 32 || p.accountPassword == "" {
		p.mutex.Unlock()
		return ErrAccountManagedDataImportIncomplete
	}
	profileCopy := *profile
	profileCopy.StateEnvelope = append([]byte(nil), profile.StateEnvelope...)
	profileCopy.ManagedDataEnvelope = append([]byte(nil), profile.ManagedDataEnvelope...)
	profile = &profileCopy
	accountID, rootFingerprint, password := profile.AccountID, profile.RootFingerprint, p.accountPassword
	secret := append([]byte(nil), p.accountSecret...)
	stateEnvelope := append([]byte(nil), profile.StateEnvelope...)
	dataEnvelope := append([]byte(nil), profile.ManagedDataEnvelope...)
	if len(marker.ReplayStateEnvelope) != 0 {
		stateEnvelope = append([]byte(nil), marker.ReplayStateEnvelope...)
		dataEnvelope = append([]byte(nil), marker.ReplayDataEnvelope...)
	} else if len(marker.ReplayDataEnvelope) != 0 {
		p.mutex.Unlock()
		zeroBytes(secret)
		return ErrAccountManagedDataImportIncomplete
	}
	wallets := make([]*WalletInfo, 0, len(p.walletInfoMap))
	for _, info := range p.walletInfoMap {
		wallets = append(wallets, cloneWalletInfoForAccountSync(info))
	}
	p.mutex.Unlock()
	defer zeroBytes(secret)
	state, err := account.OpenManagedState(secret, accountID, stateEnvelope)
	if err != nil || state.RootFingerprint != rootFingerprint ||
		state.Revision != marker.TargetStateRevision || accountStateDigest(stateEnvelope) != marker.TargetStateHash ||
		state.DataRevision != marker.TargetDataRevision || state.DataHash != marker.TargetDataHash {
		return ErrAccountManagedDataImportIncomplete
	}
	bundle := emptyAccountManagedDataBundle(1)
	dataHash := ""
	if state.DataRevision != 0 {
		bundle, err = account.OpenManagedDataBundle(secret, accountID, dataEnvelope)
		if err != nil {
			return ErrAccountManagedDataImportIncomplete
		}
		dataHash, err = accountManagedDataContentHash(bundle.Items)
		if err != nil || verifyAccountManagedDataReference(state, bundle, dataHash) != nil {
			return ErrAccountManagedDataImportIncomplete
		}
	} else if state.DataHash != "" || len(dataEnvelope) != 0 {
		return ErrAccountManagedDataImportIncomplete
	}
	// A baseline envelope and its references must still agree even if a merge
	// supplies the actual import target. This also rejects corrupted profile I/O.
	confirmedHash := marker.TargetStateHash
	if len(marker.ReplayStateEnvelope) != 0 {
		confirmedHash = marker.ConfirmedStateHash
		if confirmedHash == "" {
			return ErrAccountManagedDataImportIncomplete
		}
	} else if marker.ConfirmedStateHash != "" {
		return ErrAccountManagedDataImportIncomplete
	}
	baseline, err := account.OpenManagedState(secret, accountID, profile.StateEnvelope)
	if err != nil || baseline.Revision != profile.StateSeq ||
		profile.StateHash != confirmedHash || accountStateDigest(profile.StateEnvelope) != confirmedHash ||
		baseline.DataRevision != profile.ManagedDataRevision || baseline.DataHash != profile.ManagedDataHash ||
		baseline.RootFingerprint != rootFingerprint {
		return ErrAccountManagedDataImportIncomplete
	}
	baseBundle, err := openProfileManagedDataBundle(*profile, secret)
	if err != nil {
		return ErrAccountManagedDataImportIncomplete
	}
	if baseline.DataRevision != 0 {
		baseHash, err := accountManagedDataContentHash(baseBundle.Items)
		if err != nil || verifyAccountManagedDataReference(baseline, baseBundle, baseHash) != nil {
			return ErrAccountManagedDataImportIncomplete
		}
	}
	liveCount := 0
	for _, managed := range state.Wallets {
		if !managed.Deleted {
			liveCount++
		}
	}
	if liveCount != len(wallets) {
		return ErrAccountManagedDataImportIncomplete
	}
	for _, info := range wallets {
		if info == nil || info.Wallet == nil || info.Type != WALLET_TYPE_MNEMONIC {
			return ErrAccountManagedDataImportIncomplete
		}
		managed, err := p.managedWalletFromInfoLocked(info, password, state.Revision)
		if err != nil || !managedWalletContentMatches(managed, findManagedWallet(&state, managed.Fingerprint)) {
			return ErrAccountManagedDataImportIncomplete
		}
	}
	if err := p.updateAccountManagedDataImportStage(accountManagedImportStageProviderImport); err != nil {
		return err
	}
	if err := p.importAccountManagedDataSnapshot(&accountManagedDataSnapshot{
		Bundle: bundle, Hash: dataHash, Envelope: dataEnvelope,
	}); err != nil {
		return err
	}
	return p.finishAccountManagedRemoteImport()
}

func (p *Manager) finishAccountManagedRemoteImport() error {
	if err := p.updateAccountManagedDataImportStage(accountManagedImportStageScopeRebuild); err != nil {
		return err
	}
	if err := p.rgbManager.selectRGB11Scope(); err != nil {
		return fmt.Errorf("select imported RGB11 wallet scope: %w", err)
	}
	if err := p.rgbManager.rebuildRGB11Locks(); err != nil {
		return fmt.Errorf("rebuild imported RGB11 locks: %w", err)
	}
	if err := p.updateAccountManagedDataImportStage(accountManagedImportStageMarkerDelete); err != nil {
		return err
	}
	return p.db.Delete(accountManagedDataImportKey())
}

// Recovery retries finish the already committed target, even if another device
// has advanced the remote state. The profile already stores its authenticated
// encrypted envelopes; no additional snapshot or persistent key is needed.
func (p *Manager) loadCommittedAccountManagementRestore(secret []byte,
	locator account.Locator, rootFingerprint string) (*RecoveredAccountManagementState, error) {
	p.mutex.RLock()
	marker, err := p.readAccountManagedDataImportMarker()
	if err != nil {
		p.mutex.RUnlock()
		return nil, err
	}
	if marker == nil || len(p.walletInfoMap) == 0 {
		p.mutex.RUnlock()
		return nil, nil
	}
	profile := p.accountProfile
	if marker.Origin != accountManagedImportOriginRestore || profile == nil ||
		profile.AccountID != locator.AccountID || profile.PackageID != locator.PackageID ||
		profile.RootFingerprint != rootFingerprint || len(profile.Pending) != 0 ||
		profile.StateSeq != marker.TargetStateRevision || profile.StateHash != marker.TargetStateHash ||
		profile.ManagedDataRevision != marker.TargetDataRevision || profile.ManagedDataHash != marker.TargetDataHash {
		p.mutex.RUnlock()
		return nil, ErrAccountManagedDataImportIncomplete
	}
	copyProfile := *profile
	copyProfile.StateEnvelope = append([]byte(nil), profile.StateEnvelope...)
	copyProfile.ManagedDataEnvelope = append([]byte(nil), profile.ManagedDataEnvelope...)
	p.mutex.RUnlock()
	state, err := account.OpenManagedState(secret, locator.AccountID, copyProfile.StateEnvelope)
	if err != nil || state.RootFingerprint != rootFingerprint {
		return nil, ErrAccountManagedDataImportIncomplete
	}
	bundle, err := openProfileManagedDataBundle(copyProfile, secret)
	if err != nil {
		return nil, err
	}
	value, err := validateRecoveredAccountManagementState(RecoveredAccountManagementState{
		State: state, Seq: copyProfile.StateSeq, Hash: copyProfile.StateHash,
		Envelope: copyProfile.StateEnvelope, ManagedData: bundle,
		ManagedDataHash: copyProfile.ManagedDataHash, ManagedDataEnvelope: copyProfile.ManagedDataEnvelope,
	}, secret, locator)
	if err != nil {
		return nil, err
	}
	return &value, nil
}
