package wallet

import (
	"context"
	"testing"
	"time"
)

// Establish the same signed Core binding and verified state/blob baseline that
// an online managed wallet needs before catalog writes. The memory transport
// still checks the real account signature and endpoint generation.
func establishAccountManagedTestWriteSession(t *testing.T, manager *Manager, remote *rgb11MemoryDKVSHTTP, passwords ...string) {
	t.Helper()
	if manager == nil || remote == nil {
		t.Fatal("managed account test needs a wallet and DKVS endpoint")
	}
	password := "password"
	if len(passwords) != 0 {
		password = passwords[0]
	}
	if _chain == "" {
		oldChain := _chain
		_chain = "testnet"
		t.Cleanup(func() { _chain = oldChain })
	}
	if manager.cfg.Chain == "" {
		manager.cfg.Chain = _chain
		manager.cfg.IndexerL2.Proxy = _chain
	}
	if manager.accountProfile == nil {
		if err := manager.InitializeAccountManagement(password); err != nil {
			t.Fatal(err)
		}
	}
	client := newRGB11MessageNodeClient(remote)
	node := NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	if err := manager.SetAccountServiceNode(node); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartDKVSSync(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.StopDKVSSync)
	if err := manager.BindAccountToCurrentCoreNode(); err != nil {
		t.Fatal(err)
	}
	status := manager.GetAccountManagementStatus()
	if !status.RecoveryConfigured {
		if status.ActivationPending {
			t.Fatal("managed account test has an unfinished activation")
		}
		pkg, auth, secret := rereviewRecoveryPackage(t, manager, password)
		if err := manager.ActivateAccountManagement(secret, password, *auth,
			pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := manager.SyncAccountManagementState(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.WaitAccountManagedDataReady(ctx); err != nil {
		t.Fatal(err)
	}
}

// accountGroupState belongs to one outer business group, never to Manager or
// a process-global switch. Negative states retain their dedicated gate tests.
type accountGroupState string

const (
	accountGroupInactive accountGroupState = "KnownInactive"
	accountGroupReady    accountGroupState = "ActiveReady"
)

// prepareAccountGroup attaches a fresh real-signature endpoint before any
// client starts. Both modes retain the fixture's wallet, ID and local secrets;
// only ActiveReady activates account backup. Call only from group constructors.
func prepareAccountGroup(t *testing.T, manager *Manager, state accountGroupState,
	mnemonic, password string) {
	t.Helper()
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(manager, remote)
	t.Cleanup(func() {
		manager.StopDKVSSync()
		manager.stopAccountBackgroundTasks()
		manager.clearAccountManagementSession()
	})
	manager.mutex.Lock()
	info, err := manager.accountManagementCandidateRootLocked()
	if err == nil {
		wallet := NewInternalWalletWithMnemonic(mnemonic, "", GetChainParam())
		if wallet == nil || walletFingerprint(wallet) != walletFingerprint(info.Wallet) {
			manager.mutex.Unlock()
			t.Fatal("group credential does not match the business wallet")
		}
		err = manager.saveWalletSecretWithPassword(mnemonic, password, &info.WalletInDB)
	}
	manager.mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	switch state {
	case accountGroupInactive:
		discoverInactiveAccount(t, manager)
		t.Cleanup(func() {
			remote.mu.Lock()
			count := len(remote.records)
			remote.mu.Unlock()
			if count != 0 {
				t.Errorf("inactive business group uploaded %d records", count)
			}
		})
	case accountGroupReady:
		establishAccountManagedTestWriteSession(t, manager, remote, password)
		status := manager.GetAccountManagementStatus()
		if !status.RecoveryConfigured || status.ActivationPending || !status.DKVSWriteAllowed {
			t.Fatalf("active group unavailable: discovery=%s configured=%t pending=%t writable=%t",
				status.DiscoveryStatus, status.RecoveryConfigured, status.ActivationPending, status.DKVSWriteAllowed)
		}
	default:
		t.Fatalf("unsupported business group state %q", state)
	}
}

// This phase receives authenticated absence only. It never creates or rewrites
// credentials and can therefore run after a real ImportWallet or UnlockWallet.
// It also permits a later explicit activation; lifetime upload assertions belong
// to groups which remain inactive for the entire test.
func discoverInactiveAccount(t *testing.T, manager *Manager) {
	t.Helper()
	if err := manager.refreshDKVSRegistrations(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	status := manager.GetAccountManagementStatus()
	if status.DiscoveryStatus != "inactive" || !status.LocalOnly ||
		status.RecoveryConfigured || status.ActivationPending {
		t.Fatalf("inactive group unavailable: discovery=%s local_only=%t configured=%t pending=%t",
			status.DiscoveryStatus, status.LocalOnly, status.RecoveryConfigured, status.ActivationPending)
	}
}

func prepareLocalAccountGroup(t *testing.T, manager *Manager) {
	t.Helper()
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(manager, remote)
	t.Cleanup(func() {
		manager.StopDKVSSync()
		manager.stopAccountBackgroundTasks()
		manager.clearAccountManagementSession()
	})
	discoverInactiveAccount(t, manager)
	t.Cleanup(func() {
		remote.mu.Lock()
		count := len(remote.records)
		remote.mu.Unlock()
		if count != 0 {
			t.Errorf("inactive business group uploaded %d records", count)
		}
	})
}
