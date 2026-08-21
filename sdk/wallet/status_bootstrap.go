package wallet

// startStatusBootstrap initializes missing chain tips in the background. Local
// wallet initialization must never wait for an indexer or DKVS endpoint.
func (p *Manager) startStatusBootstrap() {
	if p == nil || p.status == nil || ENABLE_TESTING {
		return
	}

	targetChain := _chain
	if p.cfg != nil && p.cfg.Chain != "" {
		targetChain = p.cfg.Chain
	}

	p.status.RLock()
	needsL1 := p.status.SyncHeightL1 < 0
	needsL2 := p.status.SyncHeightL2 < 0
	p.status.RUnlock()
	if !needsL1 && !needsL2 {
		return
	}

	p.statusBootstrapMu.Lock()
	p.statusBootstrapGeneration++
	generation := p.statusBootstrapGeneration
	p.statusBootstrapMu.Unlock()

	if needsL1 && p.l1IndexerClient != nil {
		go p.bootstrapStatusTip(generation, targetChain, true, p.l1IndexerClient)
	}
	if needsL2 && p.l2IndexerClient != nil {
		go p.bootstrapStatusTip(generation, targetChain, false, p.l2IndexerClient)
	}
}

func (p *Manager) invalidateStatusBootstrap() {
	if p == nil {
		return
	}
	p.statusBootstrapMu.Lock()
	p.statusBootstrapGeneration++
	p.statusBootstrapMu.Unlock()
}

func (p *Manager) bootstrapStatusTip(generation uint64, targetChain string, l1 bool,
	client interface {
		GetSyncHeight() int
		GetBlockHash(int) (string, error)
	}) {
	if client == nil {
		return
	}
	height := client.GetSyncHeight()
	if height < 0 {
		return
	}
	hashes := loadBlockHashWindow(height, initialStatusBlockHashWindow(targetChain, l1), client)

	p.statusBootstrapMu.Lock()
	defer p.statusBootstrapMu.Unlock()
	if generation != p.statusBootstrapGeneration || p.status == nil {
		return
	}
	p.status.Lock()
	if p.status.CurrentChain != "" && p.status.CurrentChain != targetChain {
		p.status.Unlock()
		return
	}
	p.status.CurrentChain = targetChain
	if l1 {
		p.status.SyncHeight = height
		p.status.SyncHeightL1 = height
		p.status.BlockHashMapL1 = hashes
	} else {
		p.status.SyncHeightL2 = height
		p.status.BlockHashMapL2 = hashes
	}
	p.status.Unlock()

	if err := p.saveStatus(); err != nil {
		Log.Warningf("save background indexer tip failed: %v", err)
	}
}
