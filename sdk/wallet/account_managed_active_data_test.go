package wallet

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type accountManagedActiveDataProviderStub struct {
	accountManagedDataProviderStub
}

func (p *accountManagedActiveDataProviderStub) ExportActive(
	_ AccountManagedDataCatalog) ([]AccountManagedDataPayload, error) {

	result := make([]AccountManagedDataPayload, len(p.payloads))
	for index, payload := range p.payloads {
		result[index] = AccountManagedDataPayload{
			Scope: payload.Scope, Payload: append([]byte(nil), payload.Payload...),
		}
	}
	return result, nil
}

func (p *accountManagedActiveDataProviderStub) ValidateActive(
	_ AccountManagedDataCatalog, _ []AccountManagedDataPayload) error {
	return nil
}

func (p *accountManagedActiveDataProviderStub) ImportActive(
	_ AccountManagedDataCatalog, _ []AccountManagedDataPayload) error {
	return nil
}

type accountManagedActiveRateLimitClient struct {
	*rgb11MessageNodeClient
	wallet *InternalWallet

	mu              sync.Mutex
	acceptedPerCall int
	directAttempts  map[string]int
	responseLoss    string
}

func (c *accountManagedActiveRateLimitClient) resetCallBudget(accepted int) {
	c.mu.Lock()
	c.acceptedPerCall = accepted
	c.mu.Unlock()
}

func (c *accountManagedActiveRateLimitClient) attempts(applicationID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.directAttempts[applicationID]
}

func (c *accountManagedActiveRateLimitClient) SendMessageServiceReq(
	req *swire.MessageServiceRequest) (*swire.MessageServiceResponse, error) {

	applicationID := ""
	if req != nil && req.Action == swire.MessageServiceActionSendDirect && req.Direct != nil {
		applicationID = "<invalid>"
		if c.wallet != nil {
			plain, err := c.wallet.DecryptFromAccount(req.Direct.SenderAccount, req.Direct.Ciphertext)
			if err == nil {
				if payload, decodeErr := decodeAccountMessagePayload(plain); decodeErr == nil {
					applicationID = payload.ApplicationID
				}
			}
		}
		c.mu.Lock()
		c.directAttempts[applicationID]++
		attempt := 0
		for _, count := range c.directAttempts {
			attempt += count
		}
		budget := c.acceptedPerCall
		c.mu.Unlock()
		if budget >= 0 && attempt > budget {
			return nil, &MessageServiceRateLimitError{RetryAfter: time.Second}
		}
	}
	response, err := c.rgb11MessageNodeClient.SendMessageServiceReq(req)
	if err == nil && applicationID != "" {
		c.mu.Lock()
		lost := c.responseLoss == applicationID
		if lost {
			c.responseLoss = ""
		}
		c.mu.Unlock()
		if lost {
			return nil, errors.New("accepted response lost")
		}
	}
	return response, err
}

func newManagedActiveFixture(t *testing.T) (
	*Manager, *accountManagedActiveDataProviderStub, *accountManagedActiveRateLimitClient,
	[]string) {

	t.Helper()
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })

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
				Id: rootID, Accounts: 3, Type: WALLET_TYPE_MNEMONIC,
				Name: "Root", AccountNames: map[uint32]string{
					0: "Account 1", 1: "Account 2", 2: "Account 3",
				},
				AccountDIDs: map[uint32]string{},
			},
			Wallet: root,
		}},
		accountProfile: &accountManagementProfile{
			AccountID: accountID, RootFingerprint: walletFingerprint(root), RecoveryConfigured: true,
		},
	}
	configureRGB11DKVSTestManager(manager, remote)
	baseClient := newRGB11MessageNodeClient(remote)
	client := &accountManagedActiveRateLimitClient{
		rgb11MessageNodeClient: baseClient, wallet: root,
		acceptedPerCall: -1, directAttempts: make(map[string]int),
	}
	manager.serverNode = NewNode(client, "message.test", SERVER_NODE,
		client.CoreNodePubKey(), client.CoreNodePubKey())
	if err := manager.bindAccountToCurrentCoreNode(root); err != nil {
		t.Fatal(err)
	}

	catalog, err := manager.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Scopes) != 3 {
		t.Fatalf("catalog scopes=%d want=3", len(catalog.Scopes))
	}
	provider := &accountManagedActiveDataProviderStub{
		accountManagedDataProviderStub: accountManagedDataProviderStub{id: "active-test"},
	}
	applicationIDs := make([]string, 0, len(catalog.Scopes))
	for index, scope := range catalog.Scopes {
		payload := AccountManagedDataPayload{
			Scope: scope.ID(), Payload: []byte(fmt.Sprintf("active-transition-%d", index)),
		}
		provider.payloads = append(provider.payloads, payload)
		_, hash, encodeErr := encodeAccountManagedActiveEnvelope(provider.id, payload)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		applicationIDs = append(applicationIDs,
			accountManagedActiveApplicationID(provider.id, payload.Scope, hash))
	}
	manager.managedDataProviders = map[string]AccountManagedDataProvider{provider.id: provider}
	return manager, provider, client, applicationIDs
}

func TestManagedActiveRateResume(t *testing.T) {
	manager, provider, client, applicationIDs := newManagedActiveFixture(t)
	client.resetCallBudget(2)

	err := manager.syncAccountManagedActiveData(provider.id)
	var rateLimited *MessageServiceRateLimitError
	if !errors.As(err, &rateLimited) {
		t.Fatalf("first sync error=%v want MessageServiceRateLimitError", err)
	}
	for index, applicationID := range applicationIDs {
		want := 1
		if index == 2 {
			want = 1 // The rate-limited durable outbox was attempted once.
		}
		if got := client.attempts(applicationID); got != want {
			t.Fatalf("first sync attempts[%s]=%d want=%d", applicationID, got, want)
		}
	}

	// A new retry window may accept the remaining payload. The already accepted
	// exact self-directed messages must be recognized from the CoreNode mailbox,
	// otherwise replaying the prefix starves the tail and invalidates its durable
	// sender sequence.
	client.resetCallBudget(-1)
	if err := manager.syncAccountManagedActiveData(provider.id); err != nil {
		t.Fatalf("second sync did not resume after the accepted prefix: %v", err)
	}
	for index, applicationID := range applicationIDs {
		want := 1
		if index == 2 {
			want = 2 // One rejected attempt plus the resumed durable outbox.
		}
		if got := client.attempts(applicationID); got != want {
			t.Fatalf("active payload %s was attempted %d times, want %d", applicationID, got, want)
		}
	}
	root, err := manager.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	messages, err := manager.freshDirectMessages(root)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(map[string]int)
	for _, item := range messages {
		if item != nil && item.Payload != nil && item.Payload.Kind == AccountMessageKindManagedActive {
			accepted[item.Payload.ApplicationID]++
		}
	}
	for _, applicationID := range applicationIDs {
		if accepted[applicationID] != 1 {
			t.Fatalf("accepted mailbox count[%s]=%d want=1", applicationID, accepted[applicationID])
		}
	}

	t.Run("mismatch-fails-closed", func(t *testing.T) {
		manager, provider, client, applicationIDs := newManagedActiveFixture(t)
		root, err := manager.accountManagementRootWallet()
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := manager.accountManagedDataCatalog()
		if err != nil {
			t.Fatal(err)
		}
		body, _, err := encodeAccountManagedActiveEnvelope(provider.id,
			AccountManagedDataPayload{Scope: provider.payloads[0].Scope, Payload: []byte("mismatched-content")})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.sendWalletDirectMessage(root, applicationIDs[0],
			AccountMessageKindManagedActive, catalog.AccountID, body); err != nil {
			t.Fatal(err)
		}
		if err := manager.syncAccountManagedActiveData(provider.id); err == nil {
			t.Fatal("mismatched accepted envelope was trusted")
		}
		if got := client.attempts(applicationIDs[0]); got != 1 {
			t.Fatalf("mismatch triggered an active payload send: attempts=%d", got)
		}
	})

	t.Run("accepted-response-loss", func(t *testing.T) {
		manager, provider, client, applicationIDs := newManagedActiveFixture(t)
		client.mu.Lock()
		client.responseLoss = applicationIDs[0]
		client.mu.Unlock()
		if err := manager.syncAccountManagedActiveData(provider.id); err == nil {
			t.Fatal("accepted response loss was not surfaced")
		}
		if err := manager.syncAccountManagedActiveData(provider.id); err != nil {
			t.Fatalf("accepted mailbox did not close response-loss outbox: %v", err)
		}
		if got := client.attempts(applicationIDs[0]); got != 1 {
			t.Fatalf("accepted response-loss payload was replayed %d times", got)
		}
		root, err := manager.accountManagementRootWallet()
		if err != nil {
			t.Fatal(err)
		}
		accountID, err := dkvsAccountID(root)
		if err != nil {
			t.Fatal(err)
		}
		outbox, err := manager.loadWalletMessageOutbox(accountID, applicationIDs[0])
		if err != nil || outbox != nil {
			t.Fatalf("accepted response-loss outbox remained: outbox=%+v err=%v", outbox, err)
		}
	})
}

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
