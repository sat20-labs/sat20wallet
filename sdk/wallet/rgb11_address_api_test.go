package wallet

import (
	"context"
	"errors"
	"strings"
	"testing"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestRGB11AddressMessageIDIsBoundedAndDomainSeparated(t *testing.T) {
	canonicalID := testRGB11ConsignmentID(t, "canonical")
	first, err := rgb11AddressMessageID(canonicalID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := rgb11AddressMessageID(canonicalID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := rgb11AddressMessageID(testRGB11ConsignmentID(t, "other-canonical"))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || first != strings.ToLower(first) || first != second || first == other {
		t.Fatalf("message IDs first=%q second=%q other=%q", first, second, other)
	}
	if _, err := rgb11AddressMessageID(""); err == nil {
		t.Fatal("empty transfer ID produced a mailbox message ID")
	}
}

func TestConfiguredRGB11AddressTransportRetentionUsesServiceNodePolicy(t *testing.T) {
	remote := newRGB11MemoryDKVSHTTP()
	remote.freeLocal.MaxTTL = 777
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	store := &dkvsStore{client: client}
	manager := &Manager{}
	manager.rgbManager = &rgb11Manager{Manager: manager}

	transient := dkvsindexer.RecordOptions{}
	if err := manager.rgbManager.configureRGB11AddressTransientRetention(store, &transient); err != nil {
		t.Fatal(err)
	}
	if transient.TTL != remote.freeLocal.MaxTTL {
		t.Fatalf("RGB transport retention=%d want=%d", transient.TTL, remote.freeLocal.MaxTTL)
	}

	remote.freeLocal.Enabled = false
	if err := manager.rgbManager.configureRGB11AddressTransientRetention(store,
		&dkvsindexer.RecordOptions{}); !errors.Is(err, dkvsindexer.ErrFreeLocalDisabled) {
		t.Fatalf("disabled FREE_LOCAL err=%v", err)
	}
}

func TestConfiguredRGB11AddressTransferRejectsChildAccount(t *testing.T) {
	privateKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, privateKey, 710)
	manager.SwitchAccount(1)
	if _, _, err := manager.PrepareConfiguredRGB11AddressTransfer(
		context.Background(), RGB11AddressSendRequest{}, dkvsindexer.RecordVerificationOptions{},
	); !errors.Is(err, ErrRGB11TraditionalReceiveRequired) {
		t.Fatalf("child account direct transfer err=%v", err)
	}
}

func TestConfiguredRGB11DirectAPIsRejectNonRootWalletByPublicKey(t *testing.T) {
	rootKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, rootKey, 710)
	if !manager.rgb11ManagerIsRoot(manager.rgbManager) {
		t.Fatal("initial root wallet was not recognized")
	}

	childKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	child := dkvsTestWalletFromPriv(t, childKey)
	manager.walletInfoMap[711] = &WalletInfo{
		WalletInDB: WalletInDB{Id: 711, Accounts: 1, Type: WALLET_TYPE_MNEMONIC},
		Wallet:     child,
	}
	manager.wallet = child
	manager.status.CurrentWallet = 711
	manager.status.CurrentAccount = 0
	if err := manager.rgbManager.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	// Private-key-backed test wallets both have process-local ID zero. The
	// root check must therefore compare their public account identities.
	if manager.rgb11ManagerIsRoot(manager.rgbManager) {
		t.Fatal("non-root wallet with the same local wallet ID was accepted")
	}
	if _, err := manager.EnableConfiguredRGB11AddressReceive(
		RGB11ReceiveCapabilityOptions{}); !errors.Is(err, ErrRGB11DirectRootRequired) {
		t.Fatalf("enable Direct receive err=%v", err)
	}
	if _, err := manager.SyncConfiguredRGB11AddressMailbox(context.Background(),
		dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{}); !errors.Is(err, ErrRGB11DirectRootRequired) {
		t.Fatalf("sync Direct mailbox err=%v", err)
	}
}

func TestConfiguredRGB11MailboxSyncUsesGenerationBeforeReplicaRead(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	accountID, err := dkvsAccountID(manager.wallet)
	if err != nil {
		t.Fatal(err)
	}
	mailboxPrefix, err := mailboxSubscriptionTarget(accountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(mailboxPrefix); err != nil {
		t.Fatal(err)
	}
	client, err := manager.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ensureDKVSManager().ensureCurrentSubscription(client); err != nil {
		t.Fatal(err)
	}

	messageID := strings.Repeat("a", 64)
	key, err := dkvsindexer.MailMsgKey(accountID, accountID, messageID)
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewDKVSAccountSignedRecord(manager.wallet, key, []byte("invalid-rgb11-payload"),
		dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 1, TTL: testRGB11FreeLocalTTL})
	if err != nil {
		t.Fatal(err)
	}
	remote.seedInternalMailboxRecord(record)
	remote.mu.Lock()
	initialSnapshots := remote.snapshotCalls
	initialStatus := remote.statusCalls
	initialDeltas := remote.deltaCalls
	remote.mu.Unlock()

	result, err := manager.SyncConfiguredRGB11AddressMailbox(context.Background(),
		dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 1 || result.Invalid != 1 {
		t.Fatalf("mailbox result=%+v", result)
	}
	remote.mu.Lock()
	if remote.statusCalls <= initialStatus || remote.snapshotCalls != initialSnapshots || remote.deltaCalls != initialDeltas+1 {
		remote.mu.Unlock()
		t.Fatalf("changed mailbox status=%d snapshot=%d delta=%d initial_status=%d initial_snapshot=%d initial_delta=%d",
			remote.statusCalls, remote.snapshotCalls, remote.deltaCalls, initialStatus, initialSnapshots, initialDeltas)
	}
	unchangedSnapshots := remote.snapshotCalls
	remote.mu.Unlock()

	if _, err := manager.SyncConfiguredRGB11AddressMailbox(context.Background(),
		dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{}); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	defer remote.mu.Unlock()
	if remote.snapshotCalls != unchangedSnapshots {
		t.Fatalf("unchanged mailbox downloaded snapshot: before=%d after=%d",
			unchangedSnapshots, remote.snapshotCalls)
	}
}

func TestRGB11DirectRejectsInvalid(t *testing.T) {
	senderKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	recipientKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	sender := newRGB11MultiDeviceManager(t, senderKey, 720)
	recipient := newRGB11MultiDeviceManager(t, recipientKey, 721)
	remote := newRGB11MemoryDKVSHTTP()
	configure := func(manager *Manager) {
		configureRGB11DKVSTestManager(manager, remote)
		client := newRGB11MessageNodeClient(remote)
		manager.serverNode = NewNode(client, "message.test", SERVER_NODE,
			client.CoreNodePubKey(), client.CoreNodePubKey())
	}
	configure(sender)
	configure(recipient)
	endpoint, err := recipient.EnableConfiguredRGB11AddressReceive(
		RGB11ReceiveCapabilityOptions{RecordOptions: dkvsindexer.RecordOptions{TTL: testRGB11FreeLocalTTL}},
	)
	if err != nil {
		t.Fatal(err)
	}
	mailbox, err := mailboxSubscriptionTarget(endpoint.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := recipient.SubscribeDKVSPrefix(mailbox); err != nil {
		t.Fatal(err)
	}
	const applicationID = "invalid-rgb11-direct"
	if _, err := sender.sendWalletDirectMessage(sender.wallet, applicationID,
		AccountMessageKindRGB11Consignment, endpoint.AccountID, []byte("invalid")); err != nil {
		t.Fatal(err)
	}
	result, err := recipient.SyncConfiguredRGB11AddressMailbox(context.Background(),
		dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Invalid != 1 {
		t.Fatalf("invalid Direct result=%+v", result)
	}
	messages, err := sender.readWalletDirectMessages(sender.wallet)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message == nil || message.Payload == nil ||
			message.Payload.Kind != AccountMessageKindRGB11ACK ||
			message.Payload.ApplicationID != applicationID {
			continue
		}
		ack, err := rgb11wallet.DecodeAddressACK(message.Payload.Body)
		if err != nil || ack.Status != RGB11AddressACKRejected {
			t.Fatalf("invalid Direct ACK=%+v err=%v", ack, err)
		}
		return
	}
	t.Fatal("invalid Direct did not emit a rejected ACK")
}
