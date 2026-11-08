package wallet

import (
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

var _ rgb11wallet.ReservationCommitMetadata = (*rgb11Manager)(nil)

// StageReservationMetadata records this scope's change in the same transaction
// as its RGB data. Business qualification belongs to the operation boundary;
// already authorized reconciliation must still be able to commit local facts.
func (p *rgb11Manager) StageReservationMetadata(batch indexer.WriteBatch, scope string) (func(bool), error) {
	owner := p.accountManagementOwner()
	if owner == nil {
		return nil, nil
	}
	if scope != p.rgb11ScopeKey() {
		return nil, ErrRGB11OperationScopeChanged
	}
	accountScope, err := p.rgb11AccountManagedScopeID()
	if err != nil {
		return nil, err
	}
	if err := owner.CheckAccountRestore(rgb11AccountManagedProviderID, accountScope); err != nil {
		return nil, err
	}
	finish, err := owner.StageAccountChange(batch, rgb11AccountManagedProviderID, accountScope)
	if err != nil {
		return nil, err
	}
	// StageAccountChange retains owner.mutex until finish. Do not reacquire it.
	info := owner.walletInfoMap[p.status.CurrentWallet]
	if info == nil || info.Wallet == nil || walletFingerprint(info.Wallet) != walletFingerprint(p.wallet) ||
		max(info.Accounts, 1) <= int(p.status.CurrentAccount) {
		finish(false)
		return nil, ErrRGB11OperationScopeChanged
	}
	if err := owner.checkAccountManagedDataImport(); err != nil {
		finish(false)
		return nil, err
	}
	return finish, nil
}

func (s *rgb11SnapshotReservations) StageReservationMetadata(batch indexer.WriteBatch, scope string) (func(bool), error) {
	if !s.localMutation {
		return nil, nil
	}
	return s.manager.StageReservationMetadata(batch, scope)
}
