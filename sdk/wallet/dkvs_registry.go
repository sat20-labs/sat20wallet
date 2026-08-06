package wallet

// refreshDKVSRegistrations collects account-management-owned paths. Module
// providers contribute recovery payloads through account management and never
// register independent permanent DKVS records.
func (p *Manager) refreshDKVSRegistrations() error {
	if p == nil || p.dkvs == nil {
		return nil
	}
	keys := make([]string, 0, 2)

	p.mutex.RLock()
	accountActive := p.accountProfile != nil && len(p.accountSecret) == 32
	p.mutex.RUnlock()
	if accountActive {
		root, err := p.accountManagementRootWallet()
		if err != nil {
			return err
		}
		stateKey, err := p.accountManagedStateKey(root)
		if err != nil {
			return err
		}
		dataKey, err := p.accountManagedDataBlobKey(root)
		if err != nil {
			return err
		}
		keys = append(keys, stateKey, dataKey)
	}
	p.dkvs.rememberPaths(keys)
	p.dkvs.rememberDirectories(nil)
	p.dkvs.wakeSync()
	return nil
}
