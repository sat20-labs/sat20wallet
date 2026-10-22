package wallet

// SetAccountServiceNode selects the account mailbox transport without changing
// the channel peer. Core nodes also own accounts, while their channel peer may
// be a bootstrap signer that deliberately does not provide account storage.
// This does not bind or migrate an account: the existing signed binding and
// DKVS endpoint identity checks still run before account writes.
func (p *Manager) SetAccountServiceNode(node *Node) error {
	if p == nil {
		return ErrMessageServiceUnavailable
	}
	if _, err := accountServiceCoreNodeID(node); err != nil {
		return err
	}
	if _, ok := node.client.(MessageServiceRPCClient); !ok {
		return ErrMessageServiceUnavailable
	}
	copy := *node
	p.accountServiceMu.Lock()
	p.accountServiceNode = &copy
	p.accountServiceMu.Unlock()
	return nil
}

func (p *Manager) getAccountServiceNode() *Node {
	if p == nil {
		return nil
	}
	p.accountServiceMu.RLock()
	node := p.accountServiceNode
	p.accountServiceMu.RUnlock()
	if node != nil {
		return node
	}
	return p.serverNode
}
