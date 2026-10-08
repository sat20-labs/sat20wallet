package wallet

import (
	"errors"
	"fmt"
	"time"
)

type AccountStoragePurpose string

const (
	AccountStoragePurposeRecovery AccountStoragePurpose = "recovery-package"
	AccountStoragePurposeGuardian AccountStoragePurpose = "guardian-capsule"
)

var (
	ErrAccountStorageAuthorizationBusy = errors.New("account storage authorization is already in use")
	ErrAccountStoragePurposeMismatch   = errors.New("account storage authorization belongs to another operation purpose")
	ErrAccountStorageRuntimeStopped    = errors.New("account storage runtime is stopped")
)

type accountStorageBinding struct {
	accountID string
	network   string
	location  AccountIndexerLocation
}

// Always acquire the manager lock before the storage lock. Cancellation needs
// only the storage lock and may also be called by manager lifecycle code. These
// locks protect identity checks and local state transitions, never callbacks or
// network requests. The selected UI wallet/account is deliberately irrelevant.
func (p *Manager) lockAccountStorageState() (accountStorageBinding, func(), error) {
	if p == nil {
		return accountStorageBinding{}, nil, ErrAccountStorageAuthorizationMissing
	}
	p.mutex.RLock()
	var rootInfo *WalletInfo
	var err error
	if p.accountProfile == nil {
		rootInfo, err = p.accountManagementCandidateRootLocked()
	} else {
		rootInfo, err = p.accountManagementRootWalletLocked()
	}
	if err != nil {
		p.mutex.RUnlock()
		return accountStorageBinding{}, nil, err
	}
	accountID, err := dkvsAccountID(cloneWalletAtAccountZero(rootInfo.Wallet))
	if err != nil {
		p.mutex.RUnlock()
		return accountStorageBinding{}, nil, err
	}
	location, err := p.AccountIndexerLocation()
	if err != nil {
		p.mutex.RUnlock()
		return accountStorageBinding{}, nil, err
	}
	binding := accountStorageBinding{accountID: accountID, network: _chain, location: location}
	p.accountStorageMu.Lock()
	if p.accountStorageStopped {
		p.accountStorageMu.Unlock()
		p.mutex.RUnlock()
		return accountStorageBinding{}, nil, ErrAccountStorageRuntimeStopped
	}
	return binding, func() {
		p.accountStorageMu.Unlock()
		p.mutex.RUnlock()
	}, nil
}

func (s *accountStorageAuthorizationSession) matches(binding accountStorageBinding) bool {
	return s != nil && !time.Now().After(s.ExpiresAt) &&
		s.AccountID == binding.accountID && s.Network == binding.network &&
		s.Authorization.Location == binding.location
}

func (p *Manager) stopAccountStorageRuntime() {
	if p == nil {
		return
	}
	p.accountStorageMu.Lock()
	p.accountStorageStopped = true
	p.accountStorageAuthorization = nil
	p.accountStorageMu.Unlock()
}

func (p *Manager) resumeAccountStorageRuntime() {
	if p == nil {
		return
	}
	p.accountStorageMu.Lock()
	p.accountStorageStopped = false
	p.accountStorageMu.Unlock()
}

// Paid setup and recharge share the storage coordinator without replacing a
// consumable grant. Cancellation/expiry of that grant cannot release live
// funding. These locks protect only the flag, never network work.
func (p *Manager) beginAccountAutopayFunding() (func(), error) {
	_, release, err := p.lockAccountStorageState()
	if err != nil {
		return nil, err
	}
	if p.accountAutopayFundingActive {
		release()
		return nil, ErrAccountStorageAuthorizationBusy
	}
	p.accountAutopayFundingActive = true
	release()
	return func() {
		p.accountStorageMu.Lock()
		p.accountAutopayFundingActive = false
		p.accountStorageMu.Unlock()
	}, nil
}

// Reserve the slot before any asynchronous policy/funding work. A late result
// can fill only this exact session; Cancel removes it and cannot be undone by
// that result. An in-use authorization cannot be silently replaced by Confirm
// or ReusePaidStorage, including a second request which might fund AUTOPAY.
func (p *Manager) beginAccountStoragePreparation() (*accountStorageAuthorizationSession, error) {
	id, err := newAccountStorageAuthorizationID()
	if err != nil {
		return nil, err
	}
	binding, release, err := p.lockAccountStorageState()
	if err != nil {
		return nil, err
	}
	defer release()
	if current := p.accountStorageAuthorization; current != nil &&
		!time.Now().After(current.ExpiresAt) && (current.Preparing || current.InUse) {
		return nil, ErrAccountStorageAuthorizationBusy
	}
	session := &accountStorageAuthorizationSession{
		Authorization: AccountStorageAuthorization{ID: id, Location: binding.location},
		AccountID:     binding.accountID, Network: binding.network,
		ExpiresAt: time.Now().Add(accountStorageAuthorizationTTL), Preparing: true,
	}
	p.accountStorageAuthorization = session
	return session, nil
}

func (p *Manager) abandonAccountStoragePreparation(session *accountStorageAuthorizationSession) {
	if p == nil || session == nil {
		return
	}
	p.accountStorageMu.Lock()
	defer p.accountStorageMu.Unlock()
	if p.accountStorageAuthorization == session && session.Preparing {
		p.accountStorageAuthorization = nil
	}
}

func (p *Manager) finishAccountStoragePreparation(session *accountStorageAuthorizationSession,
	value *AccountStorageAuthorization) (*AccountStorageAuthorization, error) {
	if session == nil || value == nil {
		return nil, ErrAccountStorageAuthorizationMissing
	}
	binding, release, err := p.lockAccountStorageState()
	if err != nil {
		return nil, errors.Join(ErrAccountStorageAuthorizationMissing, err)
	}
	defer release()
	if p.accountStorageAuthorization != session || !session.Preparing ||
		!session.matches(binding) || value.Location != binding.location {
		return nil, ErrAccountStorageAuthorizationMissing
	}
	authorization := cloneAccountStorageAuthorization(*value)
	authorization.ID = session.Authorization.ID
	session.Authorization = cloneAccountStorageAuthorization(authorization)
	session.Preparing = false
	session.ExpiresAt = time.Now().Add(accountStorageAuthorizationTTL)
	return &authorization, nil
}

func (p *Manager) claimAccountStorageAuthorization(purpose AccountStoragePurpose) (
	*accountStorageAuthorizationSession, *AccountStorageAuthorization, error) {
	binding, release, err := p.lockAccountStorageState()
	if err != nil {
		return nil, nil, err
	}
	defer release()
	session := p.accountStorageAuthorization
	if !session.matches(binding) {
		p.accountStorageAuthorization = nil
		return nil, nil, ErrAccountStorageAuthorizationMissing
	}
	if session.Preparing || session.InUse {
		return nil, nil, ErrAccountStorageAuthorizationBusy
	}
	if session.Purpose != "" && session.Purpose != purpose {
		return nil, nil, ErrAccountStoragePurposeMismatch
	}
	session.Purpose = purpose
	session.InUse = true
	value := cloneAccountStorageAuthorization(session.Authorization)
	return session, &value, nil
}

func (p *Manager) finishAccountStorageUse(session *accountStorageAuthorizationSession, consume bool) error {
	binding, release, err := p.lockAccountStorageState()
	if err != nil {
		// A lock/root/network lifecycle change invalidates the captured claim.
		// Delete only that claim, never a newer authorization.
		if p != nil {
			p.accountStorageMu.Lock()
			if p.accountStorageAuthorization == session {
				p.accountStorageAuthorization = nil
			}
			p.accountStorageMu.Unlock()
		}
		return errors.Join(ErrAccountStorageAuthorizationMissing, err)
	}
	defer release()
	if p.accountStorageAuthorization != session {
		return ErrAccountStorageAuthorizationMissing
	}
	if !session.matches(binding) || !session.InUse {
		p.accountStorageAuthorization = nil
		return ErrAccountStorageAuthorizationMissing
	}
	if consume {
		p.accountStorageAuthorization = nil
	} else {
		// The same purpose may retry a transient failure, but another workflow
		// needs explicit cancellation or a new confirmed authorization.
		session.InUse = false
	}
	return nil
}

// UseAccountStorageAuthorization atomically claims and conditionally consumes
// the SDK-owned authorization. A duplicate claimant fails before invoking any
// business work. Cancel/expiry/root changes invalidate an in-flight operation's
// result even when its network write has already reached the remote service;
// this API does not claim to roll back a remotely accepted write.
func (p *Manager) UseAccountStorageAuthorization(purpose AccountStoragePurpose,
	operation func(*AccountStorageAuthorization) error) (err error) {
	if operation == nil || (purpose != AccountStoragePurposeRecovery && purpose != AccountStoragePurposeGuardian) {
		return fmt.Errorf("invalid account storage operation")
	}
	session, value, err := p.claimAccountStorageAuthorization(purpose)
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if finishErr := p.finishAccountStorageUse(session, completed && err == nil); finishErr != nil {
			err = errors.Join(err, finishErr)
		}
	}()
	err = operation(value)
	completed = true
	return err
}
