package wallet

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

var errRGBNetworkAuditBoundary = errors.New("test reached network transport boundary")

type rgbNetworkAuditProbe struct {
	owner *Manager
	calls int
	held  []string
}

func (a *rgbNetworkAuditProbe) record() {
	a.calls++
	if a.calls != 1 {
		return
	}
	p := a.owner
	checks := []struct {
		name string
		lock sync.Locker
		try  func() bool
	}{
		{"manager-data", &p.mutex, p.mutex.TryLock},
		{"wallet-identity", &p.channelIdentityMu, p.channelIdentityMu.TryLock},
		{"rgb-scope", &p.rgbOperationMu, p.rgbOperationMu.TryLock},
		{"rgb-execution", &p.rgbManagedExecutionMu, p.rgbManagedExecutionMu.TryLock},
		{"rgb-chain-refresh", &p.rgbManager.scopeStates.chainRefresh, p.rgbManager.scopeStates.chainRefresh.TryLock},
		{"rgb-reservations", &p.rgbReservationMu, p.rgbReservationMu.TryLock},
	}
	for _, c := range checks {
		if c.try() {
			c.lock.Unlock()
		} else {
			a.held = append(a.held, c.name)
		}
	}
}
func (a *rgbNetworkAuditProbe) assertUnlocked(t *testing.T) {
	t.Helper()
	if a.calls == 0 {
		t.Fatal("setup: actual operation never reached transport boundary")
	}
	t.Logf("network boundary calls=%d; held=%s", a.calls, strings.Join(a.held, ","))
	if len(a.held) != 0 {
		t.Errorf("network I/O entered with locks held: %s", strings.Join(a.held, ","))
	}
}

type rgbNetworkAuditHTTP struct {
	*rgb11MemoryDKVSHTTP
	probe *rgbNetworkAuditProbe
}

func (h *rgbNetworkAuditHTTP) DKVSClientConfig() (*dkvsindexer.ClientConfig, error) {
	h.probe.record()
	return nil, errRGBNetworkAuditBoundary
}

type rgbNetworkAuditEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	probe *rgbNetworkAuditProbe
}

func (e *rgbNetworkAuditEvidence) GetUTXO(string) (*rgb11wallet.BitcoinUTXO, error) {
	e.probe.record()
	return nil, errRGBNetworkAuditBoundary
}
func (e *rgbNetworkAuditEvidence) GetRawTx(string) ([]byte, error) {
	e.probe.record()
	return nil, errRGBNetworkAuditBoundary
}
func (e *rgbNetworkAuditEvidence) GetTxStatus(string) (*rgb11wallet.BitcoinTxStatus, error) {
	e.probe.record()
	return nil, errRGBNetworkAuditBoundary
}
func (e *rgbNetworkAuditEvidence) GetOutspend(string) (*rgb11wallet.BitcoinOutspend, error) {
	e.probe.record()
	return nil, errRGBNetworkAuditBoundary
}
func (e *rgbNetworkAuditEvidence) GetTip() (*rgb11wallet.BitcoinTip, error) {
	e.probe.record()
	return nil, errRGBNetworkAuditBoundary
}
func (e *rgbNetworkAuditEvidence) Broadcast([]byte) (string, error) {
	e.probe.record()
	return "", errRGBNetworkAuditBoundary
}

// No test acquires the mutexes being audited. Production entrypoints own any
// observed lock; the existing transport fixtures stop before external I/O.
func TestRGBNetworkBoundariesHoldNoWalletLocks(t *testing.T) {
	for _, entry := range []string{"public-refresh-dkvs", "background-mailbox-dkvs", "background-enable-dkvs", "read-only-resolve-dkvs"} {
		t.Run(entry, func(t *testing.T) {
			old := _chain
			_chain = "testnet"
			defer func() { _chain = old }()
			manager, _, secret := buildRootWrapperSource(t)
			defer zeroBytes(secret)
			probe := &rgbNetworkAuditProbe{owner: manager}
			remote := newRGB11MemoryDKVSHTTP()
			manager.http = &rgbNetworkAuditHTTP{rgb11MemoryDKVSHTTP: remote, probe: probe}
			manager.dkvs.mu.Lock()
			manager.dkvs.clients = make(map[string]*SatsNetDKVSClient)
			manager.dkvs.mu.Unlock()
			manager.accountProfile.RecoveryConfigured = true
			manager.accountProfile.ManagedDataDirty = true
			switch entry {
			case "public-refresh-dkvs":
				_, _ = manager.RefreshRGB11State(context.Background())
			case "background-mailbox-dkvs":
				_, _ = manager.syncRootRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
			case "background-enable-dkvs":
				client := newRGB11MessageNodeClient(remote)
				manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
				_, _ = manager.enableRootRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
			case "read-only-resolve-dkvs":
				_, _ = manager.ResolveConfiguredRGB11AddressEndpoint(manager.wallet.GetAddress(), dkvsindexer.RecordVerificationOptions{})
			}
			probe.assertUnlocked(t)
		})
	}
	for _, entry := range []string{"scoped-refresh-bitcoin", "read-validation-bitcoin", "project-allocation-bitcoin"} {
		t.Run(entry, func(t *testing.T) {
			manager, _, imported, evidence, _ := newRGB11GenericSendFixture(t)
			manager.rgbManager.scopeStates.stopReconciliations()
			proofs, err := manager.rgbManager.projectionStore.ListProofs()
			if err != nil || len(proofs) == 0 {
				t.Fatalf("setup: proof: %v", err)
			}
			proof := proofs[0]
			raw, err := manager.rgbManager.projectionStore.LoadObject(proof.ConsignmentHash)
			if err != nil || len(raw) == 0 {
				t.Fatalf("setup: raw: %v", err)
			}
			probe := &rgbNetworkAuditProbe{owner: manager}
			manager.rgbManager.evidence = &rgbNetworkAuditEvidence{BitcoinEvidenceProvider: evidence, probe: probe}
			switch entry {
			case "scoped-refresh-bitcoin":
				_, _ = manager.rgbManager.RefreshRGB11State(context.Background())
			case "read-validation-bitcoin":
				_, _ = manager.ValidateRGB11Consignment(context.Background(), raw)
			case "project-allocation-bitcoin":
				output, err := manager.rgbManager.projectionStore.LoadOutput(proof.OutPoint)
				if err != nil {
					t.Fatal(err)
				}
				var assetIndex = -1
				for i := range output.Assets {
					if output.Assets[i].Name == imported.AssetName {
						assetIndex = i
						break
					}
				}
				if assetIndex < 0 {
					t.Fatal("setup: proof asset missing")
				}
				_ = manager.rgbManager.ProjectRGB11Allocation(proof.OutPoint, &output.Assets[assetIndex], proof)
			}
			probe.assertUnlocked(t)
		})
	}
	t.Run("probe-unlocked-control", func(t *testing.T) {
		manager, _, secret := buildRootWrapperSource(t)
		defer zeroBytes(secret)
		probe := &rgbNetworkAuditProbe{owner: manager}
		probe.record()
		probe.assertUnlocked(t)
	})
}

func TestRGB11ProxyReceiveTransportHoldsNoWalletLocks(t *testing.T) {
	manager, _, imported, _, _ := newRGB11GenericSendFixture(t)
	manager.rgbManager.scopeStates.stopReconciliations()
	probe := &rgbNetworkAuditProbe{owner: manager}
	proxy := &rgb11ProxyTestServer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { probe.record(); proxy.serveHTTP(w, r) }))
	defer server.Close()
	invoice, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", ContractID: imported.ContractID, AmountRaw: "10", TransportMode: RGB11ProxyTransport, TransportEndpoints: []string{"rpc://" + strings.TrimPrefix(server.URL, "http://")}, Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("setup invoice: %v", err)
	}
	_, _ = manager.ReceiveRGB11ProxyConsignment(context.Background(), invoice.RequestID)
	probe.assertUnlocked(t)
}

func TestRGB11AccountRecoveryTransportHoldsNoWalletLocks(t *testing.T) {
	manager, _, _, evidence, _ := newRGB11GenericSendFixture(t)
	manager.rgbManager.scopeStates.stopReconciliations()
	catalog, err := manager.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	provider := &rgb11AccountManagedDataProvider{owner: manager}
	payloads, err := provider.Export(catalog)
	if err != nil {
		t.Fatalf("setup export: %v", err)
	}
	probe := &rgbNetworkAuditProbe{owner: manager}
	manager.rgbManager.evidence = &rgbNetworkAuditEvidence{BitcoinEvidenceProvider: evidence, probe: probe}
	_ = manager.runAccountApplicationSync(context.Background(), func() error { return provider.Import(catalog, payloads) })
	probe.assertUnlocked(t)
}

func TestRGB11ExactRestoreNamingTransportHoldsNoWalletLocks(t *testing.T) {
	manager, _, imported, evidence, _ := newRGB11GenericSendFixture(t)
	manager.rgbManager.scopeStates.stopReconciliations()
	walletID, err := manager.rgbManager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	// Exact restore only derives naming metadata on a cold ticker-cache miss.
	// Remove this derived cache in the private in-memory fixture, keeping the
	// complete authenticated RGB snapshot and all real environment data intact.
	manager.mutex.Lock()
	delete(manager.tickerInfoMap, imported.AssetName.String())
	manager.mutex.Unlock()
	if err := manager.db.Delete([]byte(GetTickerInfoKey(imported.AssetName.String()))); err != nil {
		t.Fatal(err)
	}
	probe := &rgbNetworkAuditProbe{owner: manager}
	manager.rgbManager.evidence = &rgbNetworkAuditEvidence{BitcoinEvidenceProvider: evidence, probe: probe}
	if err := manager.rgbManager.importRGB11WalletSnapshot(snapshot); err != nil {
		t.Fatalf("restore: %v", err)
	}
	probe.assertUnlocked(t)
}
