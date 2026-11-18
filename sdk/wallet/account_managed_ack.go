package wallet

// Finish only per-key receipts. This path does not receive remote state or
// grant business eligibility; its cleanup may safely be retried locally.
func (p *Manager) finishAccountManagedACK(client *SatsNetDKVSClient, entry *DKVSBatchOutboxEntry) error {
	return p.finishAccountKVACK(client, entry)
}

func (p *Manager) finishAccountManagedACKLocked(client *SatsNetDKVSClient, entry *DKVSBatchOutboxEntry) error {
	return p.finishAccountKVACKLocked(client, entry)
}

func (p *Manager) finishReadyAccountACKsLocked() error {
	if p.accountProfile == nil || !p.accountProfile.RecoveryConfigured || p.dkvs == nil {
		return nil
	}
	client, err := p.dkvs.primaryClient()
	if err != nil {
		return err
	}
	store := newDKVSReplicaStore(p.db)
	entries, err := store.LoadOutbox(client.replicaNamespace)
	if err != nil {
		return err
	}
	var cleanup error
	for _, entry := range entries {
		if entry.OriginDomain != accountKVOutboxDomain || entry.State != DKVSOutboxAcknowledged {
			continue
		}
		if err := p.finishAccountKVACKLocked(client, entry); err != nil {
			return accountACKCleanup(entry, err)
		}
		if err := store.DiscardOutbox(entry); err != nil {
			cleanup = accountACKCleanup(entry, err)
		}
	}
	return cleanup
}
