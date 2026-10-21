package wallet

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Observe actual transport entry, without taking a business lock to manufacture
// contention. The existing signed in-memory service still handles each request.
type accountNetworkLockAuditHTTP struct {
	*rgb11MemoryDKVSHTTP
	observe func(string)
}

func (h *accountNetworkLockAuditHTTP) DKVSClientConfig() (*dkvsindexer.ClientConfig, error) {
	h.observe("config")
	return h.rgb11MemoryDKVSHTTP.DKVSClientConfig()
}

func (h *accountNetworkLockAuditHTTP) SendDKVSGet(path string, query map[string]string) ([]byte, error) {
	h.observe(path)
	return h.rgb11MemoryDKVSHTTP.SendDKVSGet(path, query)
}

func (h *accountNetworkLockAuditHTTP) SendDKVSGetContext(ctx context.Context, path string, query map[string]string) ([]byte, error) {
	h.observe(path)
	return h.rgb11MemoryDKVSHTTP.SendDKVSGetContext(ctx, path, query)
}

func (h *accountNetworkLockAuditHTTP) SendDKVSPost(path string, body []byte) ([]byte, error) {
	h.observe(path)
	return h.rgb11MemoryDKVSHTTP.SendDKVSPost(path, body)
}

func (h *accountNetworkLockAuditHTTP) SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error) {
	h.observe(path)
	return h.rgb11MemoryDKVSHTTP.SendDKVSPostContext(ctx, path, body)
}

type accountNetworkLockAuditNode struct {
	*accountManagedActiveRateLimitClient
	observe func(string)
}

func (n *accountNetworkLockAuditNode) SendMessageServiceReq(req *swire.MessageServiceRequest) (*swire.MessageServiceResponse, error) {
	n.observe("message-service")
	return n.accountManagedActiveRateLimitClient.SendMessageServiceReq(req)
}

func TestAccountNetworkBoundariesHoldNoStateLocks(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	for _, operation := range []string{"active-data", "managed-state-publication", "recovery-read", "account-direct-message"} {
		t.Run(operation, func(t *testing.T) {
			var manager *Manager
			var run func() error
			var messageClient *accountManagedActiveRateLimitClient
			if operation == "active-data" || operation == "account-direct-message" {
				m, provider, client, _ := newManagedActiveFixture(t)
				manager = m
				messageClient = client
				if operation == "active-data" {
					run = func() error { return manager.syncAccountManagedActiveData(provider.id) }
				} else {
					run = func() error {
						_, err := manager.SendAccountDirectMessage("account-network-audit", "audit",
							manager.accountProfile.AccountID, []byte("audit"))
						return err
					}
				}
			} else {
				remote := newRGB11MemoryDKVSHTTP()
				client := newRGB11MessageNodeClient(remote)
				manager = newRGB11ActivatedFlowAccount(t, accountRootWrapperTestMnemonic,
					remote, client, rgb11ManagedRecoveryEvidence{}, &rgb11FlowIndexer{})
				if operation == "managed-state-publication" {
					if err := manager.EnsureAccount(manager.status.CurrentWallet, 1, "Audit", ""); err != nil {
						t.Fatal(err)
					}
					run = func() error { return manager.SyncAccountManagementState(context.Background()) }
				} else {
					location, err := manager.AccountIndexerLocation()
					if err != nil {
						t.Fatal(err)
					}
					run = func() error {
						_, err := manager.LoadAccountManagementStateForRecovery(location,
							account.Locator{AccountID: manager.accountProfile.AccountID},
							manager.accountSecret, accountRootWrapperTestMnemonic)
						return err
					}
				}
			}
			remote, ok := manager.http.(*rgb11MemoryDKVSHTTP)
			if !ok || manager.dkvs == nil {
				t.Fatal("fixture lacks the existing signed DKVS service")
			}
			var observedMu sync.Mutex
			hits := 0
			held := make(map[string]bool)
			probe := &accountNetworkLockAuditHTTP{rgb11MemoryDKVSHTTP: remote}
			probe.observe = func(path string) {
				if operation == "managed-state-publication" && path != "/v3/dkvs/records/batch-cas" {
					return
				}
				observedMu.Lock()
				defer observedMu.Unlock()
				hits++
				locks := []struct {
					name string
					lock interface {
						TryLock() bool
						Unlock()
					}
				}{
					{"identity", &manager.channelIdentityMu}, {"rgb-scope", &manager.rgbOperationMu},
					{"manager-data", &manager.mutex}, {"account-coordinator", &manager.accountSyncMu},
					{"account-storage", &manager.accountStorageMu}, {"managed-active-sync", &manager.managedActiveSyncMu},
					{"message-send", &manager.messageSendMu},
					{"dkvs-data", &manager.dkvs.mu}, {"dkvs-coordinator", &manager.dkvs.runMu},
					{"verification-height", &manager.dkvs.verifyMu},
				}
				for _, item := range locks {
					if item.lock.TryLock() {
						item.lock.Unlock()
					} else {
						held[item.name] = true
					}
				}
				runtime := runtimeForDKVSManager(manager.dkvs)
				runtime.mu.Lock()
				var keys []*sync.Mutex
				for _, lock := range runtime.keyLocks {
					keys = append(keys, lock)
				}
				runtime.mu.Unlock()
				for _, lock := range keys {
					if lock.TryLock() {
						lock.Unlock()
					} else {
						held["dkvs-record-key"] = true
					}
				}
			}
			manager.http = probe
			if messageClient != nil {
				client := &accountNetworkLockAuditNode{accountManagedActiveRateLimitClient: messageClient, observe: probe.observe}
				manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
			}
			manager.dkvs.mu.Lock()
			if manager.dkvs.stop != nil {
				manager.dkvs.mu.Unlock()
				t.Fatal("network-lock audit requires the existing worker-free fixture")
			}
			for _, client := range manager.dkvs.clients {
				client.Http = probe
			}
			manager.dkvs.mu.Unlock()
			if err := run(); err != nil {
				t.Fatalf("operation did not complete against the real test transport: %v", err)
			}
			observedMu.Lock()
			defer observedMu.Unlock()
			if hits == 0 {
				t.Fatal("operation never reached the required transport boundary")
			}
			// These locks serialize network protocol operations: message sequence
			// allocation/replay, remote CAS updates, and active-sync ordering. They
			// do not guard the wallet selection or local UI state, and are allowed.
			coordination := map[string]bool{
				"message-send": true, "dkvs-record-key": true, "managed-active-sync": true,
			}
			var names, networkLocks []string
			for name := range held {
				if coordination[name] {
					networkLocks = append(networkLocks, name)
				} else {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			sort.Strings(networkLocks)
			t.Logf("network entries=%d; allowed protocol locks=%v", hits, networkLocks)
			if len(names) != 0 {
				t.Fatalf("%d network entries held local state locks: %s", hits, strings.Join(names, ", "))
			}
		})
	}
}
