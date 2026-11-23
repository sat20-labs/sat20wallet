package wallet

import (
	"net"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

// A Core may co-sign with a bootstrap peer, which cannot host an account inbox.
// Use the service wallet root for its local mailbox RPC.
func (p *Manager) rootMessageNode(root common.Wallet) *Node {
	node := p.getAccountServiceNode()
	if _, err := accountServiceCoreNodeID(node); err == nil || p == nil || p.cfg == nil || p.cfg.Mode != SERVER_NODE || root == nil {
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
	key := root.GetPaymentPubKey()
	return NewNode(NewNodeClient(rpc.Scheme, host, rpc.Proxy, p.http), host, SERVER_NODE, key, key)
}

// SetAccountServiceNode selects the account mailbox transport without changing
// the channel peer. Core nodes also own accounts, while their channel peer may
// be a bootstrap signer that deliberately does not provide account storage.
// When a root already exists, initialize its binding on the selected route.
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
	if root, err := p.accountManagementRootWallet(); err == nil {
		m := p.ensureDKVSManager()
		if err := m.registerRootService(m.requestContext(), root, &copy); err != nil {
			return err
		}
	}
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
