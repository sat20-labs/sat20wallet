package wallet

import (
	"testing"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestImportAccountManagedActiveDataIgnoresAndPrunesDeletedScope(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	remote := newRGB11MemoryDKVSHTTP()
	root := NewInternalWalletWithMnemonic(accountRootWrapperTestMnemonic, "", GetChainParam())
	if root == nil {
		t.Fatal("create root wallet")
	}
	root.SetSubAccount(0)
	rootID := root.GetId()
	accountID, err := dkvsAccountID(root)
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		db: newMemoryKVDB(), wallet: root,
		status: &Status{CurrentWallet: rootID, CurrentAccount: 0, TotalWallet: 1},
		walletInfoMap: map[int64]*WalletInfo{rootID: {
			WalletInDB: WalletInDB{
				Id: rootID, Accounts: 1, Type: WALLET_TYPE_MNEMONIC,
				Name: "Root", AccountNames: map[uint32]string{0: "Account 1"},
				AccountDIDs: map[uint32]string{},
			},
			Wallet: root,
		}},
		accountProfile: &accountManagementProfile{
			AccountID: accountID, RootFingerprint: walletFingerprint(root), RecoveryConfigured: true,
		},
	}
	configureRGB11DKVSTestManager(manager, remote)
	messageClient := newRGB11MessageNodeClient(remote)
	manager.serverNode = NewNode(messageClient, "message.test", SERVER_NODE,
		messageClient.CoreNodePubKey(), messageClient.CoreNodePubKey())
	if err := manager.bindAccountToCurrentCoreNode(root); err != nil {
		t.Fatal(err)
	}

	staleScope := AccountManagedDataScope{
		WalletID: rootID + 1, WalletFingerprint: "deleted-wallet",
		AccountIndex: 0, Network: _chain,
	}.ID()
	body, contentHash, err := encodeAccountManagedActiveEnvelope(
		rgb11AccountManagedProviderID,
		AccountManagedDataPayload{Scope: staleScope, Payload: []byte("stale active transition")},
	)
	if err != nil {
		t.Fatal(err)
	}
	applicationID := accountManagedActiveApplicationID(
		rgb11AccountManagedProviderID, staleScope, contentHash)
	if _, err := manager.sendWalletDirectMessage(root, applicationID,
		AccountMessageKindManagedActive, accountID, body); err != nil {
		t.Fatal(err)
	}

	if err := manager.runAccountApplicationSync(nil, func() error {
		return manager.importAccountManagedActiveData()
	}); err != nil {
		t.Fatalf("stale deleted scope blocked recovery: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		remote.mu.Lock()
		remaining := len(remote.records)
		mailboxPresent := false
		for key := range remote.records {
			parsed, parseErr := dkvsindexer.ParseKey(key)
			if parseErr == nil && parsed.Namespace == "mail" {
				mailboxPresent = true
				break
			}
		}
		remote.mu.Unlock()
		if !mailboxPresent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stale active mailbox entry was not pruned from service node: records=%d", remaining)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The local one-minute read cache may still contain the deleted FREE_LOCAL
	// entry until the next prefix refresh. It must remain harmless meanwhile.
	if err := manager.runAccountApplicationSync(nil, func() error {
		return manager.importAccountManagedActiveData()
	}); err != nil {
		t.Fatalf("cached stale scope blocked a later recovery: %v", err)
	}
}
