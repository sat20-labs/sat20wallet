package wallet

import (
	"context"
	"errors"
)

// Root creation has already committed. A started runtime finishes its entry
// query before returning to the UI. Standalone clients can call the same
// recovery API explicitly. No identity lock is held during remote I/O.
func (p *Manager) discoverNewRoot(password string) {
	p.actionMonitorLock.Lock()
	running := p.actionMonitorRunning
	p.actionMonitorLock.Unlock()
	if !running {
		return
	}
	ctx, cancel := context.WithTimeout(p.ensureDKVSManager().requestContext(), accountManagedDataReadyTimeout)
	defer cancel()
	_, err := p.RecoverAccountManagementFromCurrentWallet(ctx, password)
	if errors.Is(err, ErrRootAccountNotFound) {
		err = nil
	}
	p.dkvs.setLastSyncError(err)
}

// accountDKVSClient returns the endpoint client privately coordinated by this
// Manager. Explicit subscriptions are served from the confirmed local replica;
// writes use the same durable client outbox as account-management domains.
func (p *Manager) accountDKVSClient() (*SatsNetDKVSClient, error) {
	if p == nil || p.db == nil {
		return nil, ErrDKVSPathNotSynced
	}
	return p.ensureDKVSManager().primaryClient()
}

// StartDKVSSync resumes startup reconciliation followed by one aggregated
// long poll. It is idempotent and can be used when a suspended PWA resumes.
func (p *Manager) StartDKVSSync() error {
	if err := p.accountDBFailure(); err != nil {
		return err
	}
	if !p.accountRecoveryConfigured() {
		return ErrDKVSPathNotSynced
	}
	if _, err := p.accountDKVSClient(); err != nil {
		return err
	}
	if p.ensureDKVSManager().start() {
		// A new worker needs its account baseline even when remote data is unchanged.
		p.requestAccountSync()
	}
	return nil
}

func (p *Manager) accountRecoveryConfigured() bool {
	if p == nil {
		return false
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.accountProfile != nil && p.accountProfile.RecoveryConfigured
}

// An unlocked mnemonic wallet can discover its root before creating a profile.
func (p *Manager) accountDiscoveryUnlockedLocked() bool {
	if p.wallet == nil || p.accountPassword == "" {
		return false
	}
	if p.accountProfile != nil {
		return len(p.accountSecret) == 32
	}
	root := p.firstWalletLocked()
	return root != nil && root.Type == WALLET_TYPE_MNEMONIC && root.Wallet != nil
}

// Discovery is an explicit entry/retry operation, not an inactive account's
// ordinary backup worker. It shares the existing cancellable DKVS lifetime.
func (p *Manager) startAccountDiscovery() error {
	if err := p.accountDBFailure(); err != nil {
		return err
	}
	if p == nil {
		return ErrDKVSPathNotSynced
	}
	p.mutex.RLock()
	unlocked := p.accountDiscoveryUnlockedLocked()
	p.mutex.RUnlock()
	if !unlocked {
		return ErrDKVSPathNotSynced
	}
	if _, err := p.accountDKVSClient(); err != nil {
		return err
	}
	p.ensureDKVSManager().startWorker(true)
	return nil
}

// Native callers still own StartDKVSSync explicitly. A started wallet runtime
// resumes its account service only at startup, cold unlock or activation.
func (p *Manager) startAccountRuntime(discover bool) {
	if p == nil {
		return
	}
	p.actionMonitorLock.Lock()
	defer p.actionMonitorLock.Unlock()
	// Only local initialization follows. Serialize this start with runtime
	// shutdown so an activation completion cannot restart a closed wallet.
	if !p.actionMonitorRunning {
		return
	}
	p.mutex.RLock()
	unlocked := p.accountDiscoveryUnlockedLocked()
	p.mutex.RUnlock()
	if !unlocked {
		return
	}
	if p.accountRecoveryConfigured() {
		if err := p.StartDKVSSync(); err != nil {
			Log.Warningf("start activated account synchronization failed: %v", err)
		}
	} else if discover {
		if err := p.startAccountDiscovery(); err != nil {
			Log.Warningf("start account entry discovery failed: %v", err)
		}
	}
}

// NotifyNetworkOnline delivers a host connectivity event to the current SDK runtime.
func (p *Manager) NotifyNetworkOnline() {
	p.dkvsInitMu.Lock()
	m := p.dkvs
	p.dkvsInitMu.Unlock()
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onlineEpoch++
	if m.recheckOnOnline {
		m.startWorkerLocked(true, false)
	}
}

// RetryDKVSSync wakes the existing worker for an immediate synchronization attempt.
func (p *Manager) RetryDKVSSync() error {
	if err := p.accountDBFailure(); err != nil {
		return err
	}
	if err := p.StartDKVSSync(); err != nil {
		return err
	}
	p.dkvsInitMu.Lock()
	manager := p.dkvs
	p.dkvsInitMu.Unlock()
	if manager == nil {
		return ErrDKVSPathNotSynced
	}
	client, err := manager.primaryClient()
	if err != nil {
		return err
	}
	if err := p.retryAccountBackup(client); err != nil {
		return err
	}
	p.requestAccountSync()
	return nil
}

// StopDKVSSync cancels the current request and waits for the worker to stop.
// It preserves the confirmed replica, subscription scopes and pending outbox.
func (p *Manager) StopDKVSSync() {
	if p == nil {
		return
	}
	p.dkvsInitMu.Lock()
	manager := p.dkvs
	p.dkvsInitMu.Unlock()
	if manager != nil {
		manager.stopAndWait()
	}
}
