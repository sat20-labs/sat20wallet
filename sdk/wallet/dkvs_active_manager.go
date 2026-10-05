package wallet

// GetDKVSClient returns the configured endpoint client coordinated by this
// Manager. Explicit subscriptions are served from the confirmed local replica;
// writes use the same durable client outbox as account-management domains.
func (p *Manager) GetDKVSClient() (*SatsNetDKVSClient, error) {
	if p == nil || p.db == nil { return nil, ErrDKVSPathNotSynced }
	return p.ensureDKVSManager().primaryClient()
}

// StartDKVSSync resumes startup reconciliation followed by one aggregated
// long poll. It is idempotent and can be used when a suspended PWA resumes.
func (p *Manager) StartDKVSSync() error {
	if _, err := p.GetDKVSClient(); err != nil { return err }
	p.ensureDKVSManager().start()
	return nil
}

// StopDKVSSync cancels the current request and waits for the worker to stop.
// It preserves the confirmed replica, subscription scopes and pending outbox.
func (p *Manager) StopDKVSSync() {
	if p == nil { return }
	p.dkvsInitMu.Lock()
	manager := p.dkvs
	p.dkvsInitMu.Unlock()
	if manager != nil { manager.stopAndWait() }
}
