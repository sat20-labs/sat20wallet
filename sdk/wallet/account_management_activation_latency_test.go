package wallet

import (
	"context"
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// The gate pauses real activation at its transport boundary; it does not replace
// activation, account publication, signature validation, or local-state reads.
type accountActivationNetworkGate struct {
	phase            string
	started, release chan struct{}
	once             sync.Once
	stack            string
}

func (g *accountActivationNetworkGate) wait(phase string) {
	if g.phase != phase {
		return
	}
	g.once.Do(func() {
		stack := make([]byte, 16<<10)
		g.stack = string(stack[:runtime.Stack(stack, false)])
		close(g.started)
	})
	<-g.release
}

type accountActivationGateHTTP struct {
	*rgb11MemoryDKVSHTTP
	gate *accountActivationNetworkGate
}

func (h *accountActivationGateHTTP) SendDKVSPost(path string, body []byte) ([]byte, error) {
	switch path {
	case "/v3/dkvs/active/sync":
		// WaitReady now uses the shared active-state protocol. Binding and
		// explicit refresh use that protocol too; pause only readiness here.
		stack := make([]byte, 16<<10)
		if strings.Contains(string(stack[:runtime.Stack(stack, false)]), ".WaitReady(") {
			h.gate.wait("WaitReady")
		}
	case "/v3/dkvs/records/batch-cas":
		var req DKVSBatchCASRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		if len(req.Mutations) == 1 && req.Mutations[0].Record != nil && dkvsindexer.IsAccountMappingBindingKey(req.Mutations[0].Record.Key) {
			h.gate.wait("BindingCAS")
		} else {
			h.gate.wait("CAS")
		}
	}
	return h.rgb11MemoryDKVSHTTP.SendDKVSPost(path, body)
}

func (h *accountActivationGateHTTP) SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return h.SendDKVSPost(path, body)
}

func TestAccountPaidActivationNetworkWaitDoesNotBlockLocalState(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	for _, phase := range []string{"CAS", "BindingCAS"} {
		t.Run(phase, func(t *testing.T) {
			manager, endpoint, _, _, secret := newLocalActivationFixture(t)
			defer zeroBytes(secret)
			_, done, release := startGatedPaidActivation(t, manager, secret, phase, endpoint)
			status := accountStatusWithin(t, manager)
			if status.AccountID == "" {
				t.Error("account status became unavailable during activation network wait")
			}
			stateDone := make(chan error, 1)
			go func() { _, err := manager.GetRGB11State(); stateDone <- err }()
			select {
			case err := <-stateDone:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(time.Second):
				t.Error("local RGB read blocked behind activation network")
			}
			release()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Minute):
				t.Fatal("activation did not finish")
			}
			if !manager.GetAccountManagementStatus().RecoveryConfigured {
				t.Fatal("service activation did not finish")
			}
		})
	}
}

// Pause real service reconfiguration at its binding or wrapper request.
func startGatedPaidActivation(t *testing.T, manager *Manager, secret []byte, phase string, existing ...*rgb11MemoryDKVSHTTP) (*accountActivationGateHTTP, <-chan error, func()) {
	t.Helper()
	gate := &accountActivationNetworkGate{phase: phase, started: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(gate.release) }) }
	t.Cleanup(release)
	endpoint := newRGB11MemoryDKVSHTTP()
	if len(existing) != 0 {
		endpoint = existing[0]
	}
	remote := &accountActivationGateHTTP{rgb11MemoryDKVSHTTP: endpoint, gate: gate}
	setActivationTestHTTP(t, manager, remote)
	client := newRGB11MessageNodeClient(endpoint)
	manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	location, err := manager.AccountIndexerLocation()
	if err != nil {
		t.Fatal(err)
	}
	pkg, _, packageSecret := rereviewRecoveryPackage(t, manager)
	zeroBytes(packageSecret)
	authorization := AccountStorageAuthorization{Mode: AccountStoragePaid, Location: location,
		Autopay: &DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet(), PoolContract: defaults.AutopayContract}}
	locator := pkg.Envelope.Locator
	done := make(chan error, 1)
	go func() { done <- manager.ActivateAccountManagement(secret, "password", authorization, locator, "") }()
	select {
	case <-gate.started:
	case err := <-done:
		t.Fatalf("activation ended before gate: %v", err)
	case <-time.After(time.Minute):
		t.Fatal("activation did not reach gate")
	}
	return remote, done, release
}

func TestAccountPaidActivationPreservesConcurrentLocalChanges(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	for _, phase := range []string{"CAS"} {
		t.Run(phase, func(t *testing.T) {
			manager, endpoint, _, _ := accountTTLNoopFixture(t)
			secret := append([]byte(nil), manager.accountSecret...)
			defer zeroBytes(secret)
			_, done, release := startGatedPaidActivation(t, manager, secret, phase, endpoint)
			oldGeneration := manager.accountProfile.ManagedDataGeneration

			if err := manager.EnsureAccount(manager.status.CurrentWallet, 1, "During activation", ""); err != nil {
				t.Fatal(err)
			}
			mutationID := manager.accountProfile.Pending[0].ID
			release()
			err := <-done
			if err != nil {
				t.Fatal(err)
			}
			profile := manager.accountProfile
			if !profile.ManagedDataDirty || profile.ManagedDataGeneration <= oldGeneration || len(profile.Pending) != 1 || profile.Pending[0].ID != mutationID {
				t.Fatal("activation lost a newer pending mutation or dirty generation")
			}
			var persisted accountManagementProfile
			data, err := manager.db.Read(accountManagementProfileKey())
			if err != nil {
				t.Fatal(err)
			}
			if err := DecodeFromBytes(data, &persisted); err != nil {
				t.Fatal(err)
			}
			if persisted.ManagedDataGeneration != profile.ManagedDataGeneration || !persisted.ManagedDataDirty || len(persisted.Pending) != 1 || persisted.Pending[0].ID != mutationID {
				t.Fatal("activation ACK is not durable with concurrent mutation")
			}
		})
	}
}

func TestAccountPaidActivationSelectionChangeDoesNotChangeRootIdentity(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	for _, change := range []string{"account", "wallet"} {
		t.Run(change, func(t *testing.T) {
			manager, endpoint, _, _ := accountTTLNoopFixture(t)
			secret := append([]byte(nil), manager.accountSecret...)
			defer zeroBytes(secret)
			rootID := manager.status.CurrentWallet
			var alternateWallet int64
			if change == "wallet" {
				var err error
				alternateWallet, _, err = manager.CreateWallet("password")
				if err != nil {
					t.Fatal(err)
				}
				if err := manager.SwitchWallet(rootID, "password"); err != nil {
					t.Fatal(err)
				}
			}

			if err := manager.EnsureAccount(rootID, 1, "Selection target", ""); err != nil {
				t.Fatal(err)
			}
			if err := manager.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, done, release := startGatedPaidActivation(t, manager, secret, "CAS", endpoint)
			switch change {
			case "account":
				if err := manager.SwitchAccount(1); err != nil {
					t.Fatal(err)
				}
				if manager.status.CurrentAccount != 1 {
					t.Fatal("account selection did not change")
				}
			case "wallet":
				if err := manager.SwitchWallet(alternateWallet, "password"); err != nil {
					t.Fatal(err)
				}
			}
			selectedWallet, selectedAccount := manager.status.CurrentWallet, manager.status.CurrentAccount
			release()
			if err := <-done; err != nil {
				t.Fatalf("root-account activation was invalidated by %s selection: %v", change, err)
			}
			if !manager.accountProfile.RecoveryConfigured || manager.accountProfile.StorageMode != AccountStoragePaid {
				t.Fatal("paid root-account activation did not commit")
			}
			if manager.status.CurrentWallet != selectedWallet || manager.status.CurrentAccount != selectedAccount {
				t.Fatal("root-account activation changed the user's current wallet/account selection")
			}
		})
	}
}

func TestAccountActivationThenBackup(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	manager, remote, pkg, auth, secret := newLocalActivationFixture(t)
	defer zeroBytes(secret)
	if err := manager.ActivateAccountManagement(secret, "password", *auth, pkg.Envelope.Locator, ""); err != nil {
		t.Fatal(err)
	}
	status := manager.GetAccountManagementStatus()
	if !status.RecoveryConfigured || !status.DKVSWriteAllowed || !status.ManagedDataDirty || !manager.accountProfile.InitialBackupPending {
		t.Fatalf("service activation incorrectly waited for private backup: %+v", status)
	}
	root, err := manager.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	walletModule, err := accountEntry(manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	indexKey, err := account.ManagedIndexKey(manager.accountProfile.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		t.Fatal(err)
	}
	initial := snapshotRGB11MemoryRecords(remote)
	if initial[wrapperKey] == nil || initial[indexKey] != nil || initial[walletModule.Key] != nil {
		t.Fatal("activation did not separate service opening from private backup")
	}
	peer := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(peer, remote)
	client := newRGB11MessageNodeClient(remote)
	peer.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	if _, err := peer.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := peer.StartDKVSSync(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.StopDKVSSync)
	require.Eventually(t, func() bool { return peer.GetAccountManagementStatus().DiscoveryStatus == "active-waiting-backup" }, 30*time.Second, 20*time.Millisecond)
	before := peer.GetWalletCatalog()
	if err := peer.UpdateWalletName(peer.GetCurrentWalletId(), "forbidden replacement"); err == nil {
		t.Fatal("new device overwrote a service whose first private backup is pending")
	}
	if !reflect.DeepEqual(before, peer.GetWalletCatalog()) {
		t.Fatal("waiting discovery changed the new device catalog")
	}
	if err := manager.UpdateWalletName(manager.GetCurrentWalletId(), "B"); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateWalletName(manager.GetCurrentWalletId(), "C"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.accountProfile.InitialBackupPending || manager.GetAccountManagementStatus().ManagedDataDirty {
		t.Fatal("first backup did not finish its local initialization task")
	}
	require.Eventually(t, func() bool {
		return peer.GetAccountManagementStatus().RecoveryConfigured && peer.GetWalletCatalog()[0].Name == "C"
	}, 30*time.Second, 20*time.Millisecond, "new device must restore the completed backup without activation")
}
