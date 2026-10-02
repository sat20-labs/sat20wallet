package wallet

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
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
	case "/v3/dkvs/prefixes/snapshot":
		h.gate.wait("WaitReady")
	case "/v3/dkvs/records/batch-cas":
		h.gate.wait("CAS")
	}
	return h.rgb11MemoryDKVSHTTP.SendDKVSPost(path, body)
}

func (h *accountActivationGateHTTP) SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return h.SendDKVSPost(path, body)
}

type accountActivationGateNode struct {
	*rgb11MessageNodeClient
	gate *accountActivationNetworkGate
}

func (n *accountActivationGateNode) SendMessageServiceReq(req *swire.MessageServiceRequest) (*swire.MessageServiceResponse, error) {
	if req.Action == swire.MessageServiceActionBindAccount {
		n.gate.wait("RPC")
	}
	return n.rgb11MessageNodeClient.SendMessageServiceReq(req)
}

func TestAccountPaidActivationNetworkWaitDoesNotBlockLocalState(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	for _, phase := range []string{"WaitReady", "CAS", "RPC"} {
		t.Run(phase, func(t *testing.T) {
			manager, _, secret := buildRootWrapperSource(t)
			defer zeroBytes(secret)
			gate := &accountActivationNetworkGate{phase: phase, started: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(gate.release) }) }
			defer release()
			remote := &accountActivationGateHTTP{rgb11MemoryDKVSHTTP: newRGB11MemoryDKVSHTTP(), gate: gate}
			manager.http = remote
			manager.dkvs.mu.Lock()
			manager.dkvs.clients = make(map[string]*SatsNetDKVSClient)
			manager.dkvs.mu.Unlock()
			client := &accountActivationGateNode{rgb11MessageNodeClient: newRGB11MessageNodeClient(remote.rgb11MemoryDKVSHTTP), gate: gate}
			manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
			defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
			authorization := AccountStorageAuthorization{Mode: AccountStoragePaid,
				Autopay: &DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet(), PoolContract: defaults.AutopayContract}}
			locator := account.Locator{AccountID: manager.accountProfile.AccountID}
			activationDone := make(chan error, 1)
			go func() {
				activationDone <- manager.ActivateAccountManagement(secret, "password", authorization, locator, "")
			}()
			select {
			case <-gate.started:
			case err := <-activationDone:
				t.Fatalf("activation ended before %s transport: %v", phase, err)
			case <-time.After(10 * time.Second):
				t.Fatalf("activation did not reach %s transport", phase)
			}
			if !strings.Contains(gate.stack, "activateAccountManagement") ||
				(phase == "WaitReady" && !strings.Contains(gate.stack, "WaitReady")) {
				t.Error("transport gate is not on the expected activation call stack")
			}
			assertAccountNetworkReleasedScopeLocks(t, manager)
			stateDone := make(chan error, 1)
			go func() { _, err := manager.GetRGB11State(); stateDone <- err }()
			stateCompleted := false
			select {
			case err := <-stateDone:
				stateCompleted = true
				if err != nil {
					t.Errorf("local RGB state: %v", err)
				}
			case <-time.After(time.Second):
				t.Errorf("GetRGB11State blocked behind paid activation %s network wait", phase)
			}
			release()
			select {
			case err := <-activationDone:
				if err != nil {
					t.Errorf("activation after releasing transport: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("activation did not finish after transport release")
			}
			if !stateCompleted {
				select {
				case err := <-stateDone:
					if err != nil {
						t.Errorf("RGB state after transport release: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("RGB state did not finish after transport release")
				}
			}
			status := manager.GetAccountManagementStatus()
			if !status.RecoveryConfigured || status.StorageMode != AccountStoragePaid {
				t.Error("fixture did not complete paid activation")
			}
		})
	}
}

// No live wallet or network: use the same signed DKVS transport as activation.
func startGatedPaidActivation(t *testing.T, manager *Manager, secret []byte, phase string) (*accountActivationGateHTTP, <-chan error, func()) {
	t.Helper()
	gate := &accountActivationNetworkGate{phase: phase, started: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(gate.release) }) }
	t.Cleanup(release)
	remote := &accountActivationGateHTTP{rgb11MemoryDKVSHTTP: newRGB11MemoryDKVSHTTP(), gate: gate}
	manager.http = remote
	manager.dkvs.mu.Lock()
	manager.dkvs.clients = make(map[string]*SatsNetDKVSClient)
	manager.dkvs.mu.Unlock()
	client := &accountActivationGateNode{rgb11MessageNodeClient: newRGB11MessageNodeClient(remote.rgb11MemoryDKVSHTTP), gate: gate}
	manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	authorization := AccountStorageAuthorization{Mode: AccountStoragePaid,
		Autopay: &DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet(), PoolContract: defaults.AutopayContract}}
	locator := account.Locator{AccountID: manager.accountProfile.AccountID}
	done := make(chan error, 1)
	go func() { done <- manager.ActivateAccountManagement(secret, "password", authorization, locator, "") }()
	select {
	case <-gate.started:
	case err := <-done:
		t.Fatalf("activation ended before gate: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("activation did not reach gate")
	}
	return remote, done, release
}

func TestAccountPaidActivationPreservesConcurrentLocalChanges(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	for _, phase := range []string{"WaitReady", "CAS", "RPC"} {
		t.Run(phase, func(t *testing.T) {
			manager, _, secret := buildRootWrapperSource(t)
			defer zeroBytes(secret)
			if err := manager.EnsureAccount(manager.status.CurrentWallet, 1, "Before activation", ""); err != nil {
				t.Fatal(err)
			}
			_, done, release := startGatedPaidActivation(t, manager, secret, phase)
			oldGeneration := manager.accountProfile.ManagedDataGeneration
			if err := manager.EnsureAccount(manager.status.CurrentWallet, 1, "During activation", ""); err != nil {
				t.Fatal(err)
			}
			manager.markAccountManagedDataDirtyDeferred(rgb11AccountManagedProviderID)
			mutationID := manager.accountProfile.Pending[0].ID
			release()
			err := <-done
			if phase == "WaitReady" {
				if !errors.Is(err, errAccountSnapshotChanged) {
					t.Fatalf("stale prepublication snapshot: %v", err)
				}
			} else if err != nil {
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
			manager, _, secret := buildRootWrapperSource(t)
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

			_, done, release := startGatedPaidActivation(t, manager, secret, "RPC")
			switch change {
			case "account":
				manager.SwitchAccount(1)
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

func TestAccountPaidActivationRejectsChangedRootConfiguration(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	_, done, release := startGatedPaidActivation(t, manager, secret, "RPC")
	manager.mutex.Lock()
	manager.bumpAccountGenerationLocked()
	manager.mutex.Unlock()
	release()
	if err := <-done; !errors.Is(err, errAccountSnapshotChanged) {
		t.Fatalf("changed root configuration accepted: %v", err)
	}
	if manager.accountProfile.RecoveryConfigured || manager.accountProfile.StorageMode != AccountStorageTemporary {
		t.Fatal("stale activation applied after root configuration changed")
	}
}
