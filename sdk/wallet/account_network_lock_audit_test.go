package wallet

import (
	"context"
	"runtime"
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

func logAccountNetworkLockCallers(t *testing.T, operation, path string) {
	t.Helper()
	pcs := make([]uintptr, 32)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for frameCount := 0; frameCount < 32; frameCount++ {
		frame, more := frames.Next()
		t.Logf("network audit call frame operation=%s path=%s frame=%d function=%s file=%s line=%d",
			operation, path, frameCount, frame.Function, frame.File, frame.Line)
		if !more {
			return
		}
	}
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
				// The successful initial sync also schedules active-data cleanup.
				// Drain that fixture work before creating a new dirty generation or
				// auditing the foreground operation's network boundaries.
				manager.accountBackgroundWG.Wait()
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
					status := manager.GetAccountManagementStatus()
					locator := account.Locator{Version: account.Version, AccountID: status.AccountID,
						PackageID: status.PackageID, RecoveryMode: account.RecoveryMode(status.RecoveryMode)}
					run = func() error {
						_, err := manager.LoadAccountManagementStateForRecovery(location, locator,
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
			loggedManagerLockFrame := make(map[string]bool)
			probe := &accountNetworkLockAuditHTTP{rgb11MemoryDKVSHTTP: remote}
			probe.observe = func(path string) {
				if operation == "managed-state-publication" && path != "/v3/dkvs/records/batch-cas" {
					return
				}
				observedMu.Lock()
				defer observedMu.Unlock()
				hits++
				pathWithoutQuery := path
				if separator := strings.IndexByte(pathWithoutQuery, '?'); separator >= 0 {
					pathWithoutQuery = pathWithoutQuery[:separator]
				}
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
						continue
					}
					held[item.name] = true
					if item.name != "manager-data" {
						continue
					}
					key := operation + "\x00" + pathWithoutQuery
					if loggedManagerLockFrame[key] {
						continue
					}
					loggedManagerLockFrame[key] = true
					t.Logf("network audit manager lock busy operation=%s path=%s stage=transport", operation, pathWithoutQuery)
					logAccountNetworkLockCallers(t, operation, pathWithoutQuery)
				}
				dkvsRuntime := runtimeForDKVSManager(manager.dkvs)
				dkvsRuntime.mu.Lock()
				var keys []*sync.Mutex
				for _, lock := range dkvsRuntime.keyLocks {
					keys = append(keys, lock)
				}
				dkvsRuntime.mu.Unlock()
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
			t.Logf("network audit operation=%s stage=start", operation)
			if err := run(); err != nil {
				t.Logf("network audit operation=%s stage=end result=error", operation)
				t.Fatalf("operation did not complete against the real test transport: %v", err)
			}
			t.Logf("network audit operation=%s stage=end result=success", operation)
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
