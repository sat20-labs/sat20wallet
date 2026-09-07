package wallet

import (
	"context"
	"errors"
	"strings"
	"testing"

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
	remote.generations[mailboxPrefix]++
	initialSnapshots := remote.snapshotCalls
	initialStatus := remote.statusCalls
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
	if remote.statusCalls <= initialStatus || remote.snapshotCalls != initialSnapshots+1 {
		remote.mu.Unlock()
		t.Fatalf("changed mailbox status=%d snapshot=%d initial_status=%d initial_snapshot=%d",
			remote.statusCalls, remote.snapshotCalls, initialStatus, initialSnapshots)
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
