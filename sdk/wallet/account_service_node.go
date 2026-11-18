package wallet

import "net"

// A Core may co-sign with a bootstrap peer, which cannot host an account inbox.
// Select the local mailbox RPC only for a public RGB payout.
func (p *Manager) payoutMessageNode() *Node {
	node := p.getAccountServiceNode()
	if _, err := accountServiceCoreNodeID(node); err == nil || p == nil || p.cfg == nil || p.cfg.Mode != SERVER_NODE || p.wallet == nil {
		return node
	}
	rpc := p.cfg.RPC
	if rpc.Host == "" {
		return node
	}
	host := rpc.Host
	if ip, port, err := net.SplitHostPort(host); err == nil {
		if addr := net.ParseIP(ip); ip == "" || addr != nil && addr.IsUnspecified() {
			loop := "127.0.0.1"
			if ip != "" && addr.To4() == nil {
				loop = "::1"
			}
			host = net.JoinHostPort(loop, port)
		}
	}
	key := p.wallet.GetPaymentPubKey()
	return NewNode(NewNodeClient(rpc.Scheme, host, rpc.Proxy, p.http), host, SERVER_NODE, key, key)
}

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
