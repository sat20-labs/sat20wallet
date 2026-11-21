package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/rgb11/invoicing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Independent module capacity failures must not block real RGB invoice, ACK,
// broadcast, or terminal recovery. RGB sizing still includes its own closure.
func coreRGBRecoveryCapacityBoundaryE2E(t *testing.T, cfg coreE2EConfig,
	chain *coreE2EChain, sender, receiver *Manager,
	senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	t.Helper()
	t.Run("R21_independent_module_failure_preserves_cross_scope_RGB", func(t *testing.T) {
		proxy := &rgb11ProxyTestServer{}
		proxyHTTP := httptest.NewServer(http.HandlerFunc(proxy.serveHTTP))
		defer proxyHTTP.Close()
		proxyEndpoint := "rpc://" + strings.TrimPrefix(proxyHTTP.URL, "http://")

		issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
			Schema: "NIA", Ticker: "E2EC", Name: "E2E capacity proof asset", Amounts: []uint64{777},
		})
		coreRequire(t, "issue capacity-boundary RGB11 asset", err)
		coreCheckpoint(t, "R21_nonempty_RGB11_baseline", sender, cfg, chain, senderMaterial)
		coreLogRGB11AccountPackageSize(t, "R21_real_nonempty_baseline", sender)

		exported, err := sender.ExportRGB11Contract(issued.ContractID)
		coreRequire(t, "export capacity-boundary contract", err)
		contract, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
		coreRequire(t, "decode capacity-boundary contract", err)
		_, err = receiver.ImportRGB11ContractFile(context.Background(), contract)
		coreRequire(t, "import contract into receiver root account", err)

		rootID := receiver.GetAccountManagementStatus().RootWalletID
		coreRequire(t, "create receiver account scope 1", receiver.EnsureAccount(rootID, 1,
			"Capacity receive scope", "did:capacity:receive"))
		coreRequire(t, "publish receiver's expanded account catalog",
			receiver.SyncAccountManagementState(context.Background()))
		coreRequire(t, "select receiver account scope 1", receiver.SwitchAccount(1))
		_, err = receiver.ImportRGB11ContractFile(context.Background(), contract)
		coreRequire(t, "import contract into receiver account scope 1", err)
		coreRequire(t, "select receiver root account", receiver.SwitchAccount(0))
		cleanupSenderCapacity := coreInstallUnrelatedCapacityFailure(t, sender, cfg)
		cleanupReceiverCapacity := coreInstallUnrelatedCapacityFailure(t, receiver, cfg)

		coreRequire(t, "select receiver account scope 1 for standard receive", receiver.SwitchAccount(1))
		receiveInvoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: RGB11ProxyTransport,
			TransportEndpoints: []string{proxyEndpoint}, ContractID: issued.ContractID,
			AmountRaw: "100", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		coreRequire(t, "create Standard witness invoice in receiver scope 1", err)
		coreAssert(t, receiveInvoice != nil && receiveInvoice.RequestID != "" && receiveInvoice.Invoice != "",
			"receiver scope 1 did not create a durable Standard witness invoice")

		beforeBroadcast := chain.broadcastCount()
		prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
			Invoice: receiveInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
		})
		coreRequire(t, "prepare cross-scope Standard witness transfer", err)
		coreAssert(t, prepared != nil && prepared.State != nil && len(prepared.State.InputOutPoints) != 0,
			"cross-scope transfer omitted its real input/operation identity")
		coreAssert(t, chain.broadcastCount() == beforeBroadcast,
			"cross-scope prepare broadcast before receiver accepted the consignment")
		delivery, err := sender.DeliverAndBroadcastRGB11ProxyTransfer(
			context.Background(), []string{prepared.State.TransferID})
		coreRequire(t, "deliver cross-scope Standard witness consignment", err)
		coreAssert(t, delivery.AwaitingACK && !delivery.Broadcast,
			"cross-scope delivery crossed the positive ACK barrier")
		received, err := receiver.ReceiveRGB11ProxyConsignment(context.Background(), receiveInvoice.RequestID)
		coreRequire(t, "receiver accepts cross-scope Standard consignment and publishes ACK", err)
		coreAssert(t, received.AckPosted && received.AwaitingBroadcast && received.TxID == prepared.TxID,
			"receiver did not durably ACK exactly one cross-scope consignment before broadcast")
		coreAssertRGB11ACKRecoveryPublished(t, receiver, prepared)
		coreAssert(t, received.Receipt != nil, "Proxy ACK omitted the exact received consignment receipt")
		receiveHash := received.Receipt.ConsignmentHash
		coreAssert(t, chain.broadcastCount() == beforeBroadcast,
			"receiver ACK caused an early transaction broadcast")
		delivery, err = sender.DeliverAndBroadcastRGB11ProxyTransfer(
			context.Background(), []string{prepared.State.TransferID})
		coreRequire(t, "sender adopts the positive ACK and broadcasts once", err)
		coreAssert(t, delivery.Broadcast && delivery.TxID == prepared.TxID,
			"sender did not broadcast the acknowledged original transaction")
		coreAssert(t, chain.broadcastCount() == beforeBroadcast+1,
			"positive ACK did not authorize exactly one original transaction")
		chain.confirm(prepared.TxID)
		_, err = sender.RefreshRGB11State(context.Background())
		coreRequire(t, "settle sender side before prepare capacity case", err)
		coreAssert(t, chain.broadcastCount() == beforeBroadcast+1,
			"sender refresh unexpectedly rebroadcast a transaction")

		// The unrelated failed module cannot prevent the accepted receive from
		// materializing and backing up its real proof and validation receipt.
		_, err = receiver.RefreshRGB11State(context.Background())
		coreRequire(t, "settle receiver despite unrelated capacity failure", err)
		lateState, err := receiver.GetRGB11State()
		coreRequire(t, "read settled receive proof", err)
		foundLocalProof, foundReceipt := false, false
		for _, proof := range lateState.Proofs {
			if proof != nil && proof.ConsignmentHash == receiveHash && rgb11ProofIsCurrent(proof) {
				foundLocalProof = true
			}
		}
		catalog, err := receiver.accountManagedDataCatalog()
		coreRequire(t, "enumerate receive recovery scopes", err)
		payloads, err := (&rgb11AccountManagedDataProvider{owner: receiver}).Export(catalog)
		coreRequire(t, "export settled local receive recovery", err)
		for _, payload := range payloads {
			value, decodeErr := rgb11wallet.DecodeRecoveryPackage(payload.Payload)
			coreRequire(t, "decode settled receive recovery", decodeErr)
			for _, record := range value.ProjectionRecords {
				foundReceipt = foundReceipt || record.Key == "validation-"+receiveHash
			}
		}
		coreAssert(t, foundLocalProof && foundReceipt, "unrelated failure lost receive proof or receipt")
		coreRequire(t, "confirm selected receive recovery", receiver.WaitAccountBackup(
			context.Background(), rgb11AccountManagedProviderID, coreSelectedRGBScope(receiver)))
		confirmed, err := accountTestPayloads(receiver, rgb11AccountManagedProviderID)
		coreRequire(t, "read confirmed terminal recovery", err)
		confirmedPackage, err := rgb11wallet.DecodeRecoveryPackage(confirmed[coreSelectedRGBScope(receiver)].Payload)
		coreRequire(t, "decode confirmed terminal recovery", err)
		confirmedReceipt := false
		for _, record := range confirmedPackage.ProjectionRecords {
			confirmedReceipt = confirmedReceipt || record.Key == "validation-"+receiveHash
		}
		coreAssert(t, confirmedReceipt, "confirmed RGB module omitted terminal validation receipt")
		coreAssert(t, chain.broadcastCount() == beforeBroadcast+1,
			"terminal reconciliation repeated the original broadcast")
		coreRequire(t, "remove sender capacity fault", cleanupSenderCapacity())
		coreRequire(t, "remove receiver capacity fault", cleanupReceiverCapacity())
		coreVerifyPaidRecords(t, receiver, cfg)
	})
	t.Run("R21_blind_terminal_preview_and_independent_capacity_failure", func(t *testing.T) {
		// This case hands the same accounts to independent devices; it is not
		// a concurrent-writer test. Resume originals only after those devices close.
		coreTransferDeviceCleanup(t, sender, receiver)
		coreSuspendRGBDevice(t, sender)
		coreSuspendRGBDevice(t, receiver)
		coreRGB11BlindCapacityBoundaryE2E(t, cfg, chain, senderMaterial, receiverMaterial)
	})
}

func coreRGB11BlindCapacityBoundaryE2E(t *testing.T, cfg coreE2EConfig,
	chain *coreE2EChain, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	t.Helper()
	proxy := &rgb11ProxyTestServer{}
	proxyHTTP := httptest.NewServer(http.HandlerFunc(proxy.serveHTTP))
	defer proxyHTTP.Close()
	proxyEndpoint := "rpc://" + strings.TrimPrefix(proxyHTTP.URL, "http://")

	sender := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreSynchronizeRestoredAccount(t, sender)
	receiver := coreRestoreE2E(t, cfg, chain, receiverMaterial)
	coreSynchronizeRestoredAccount(t, receiver)
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2EBL", Name: "E2E blind capacity asset", Amounts: []uint64{777},
	})
	coreRequire(t, "issue blind-capacity RGB11 asset", err)
	coreCheckpoint(t, "R21_blind_capacity_nonempty_sender", sender, cfg, chain, senderMaterial)
	exported, err := sender.ExportRGB11Contract(issued.ContractID)
	coreRequire(t, "export blind-capacity contract", err)
	contract, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
	coreRequire(t, "decode blind-capacity contract", err)
	_, err = receiver.ImportRGB11ContractFile(context.Background(), contract)
	coreRequire(t, "import blind-capacity contract", err)

	// This blind receive needs its own confirmed, unallocated carrier; earlier
	// journeys may have spent or bound the shared wallet's initial funding.
	chain.fund(t, receiver.GetWallet().GetAddress(), 1)

	invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "blind", TransportMode: RGB11ProxyTransport,
		TransportEndpoints: []string{proxyEndpoint}, ContractID: issued.ContractID,
		AmountRaw: "100", Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "create Standard blind proxy invoice", err)
	coreAssert(t, invoice != nil && invoice.RequestID != "" && invoice.Invoice != "",
		"Standard blind invoice did not become available after stable publication")
	invoiceValue, err := invoicing.Parse(invoice.Invoice)
	coreRequire(t, "parse Standard blind proxy invoice", err)
	coreAssert(t, invoiceValue.Beneficiary.Kind == invoicing.BeneficiaryBlindedSeal,
		"capacity fixture did not create a blinded-seal beneficiary")

	beforeBroadcast := chain.broadcastCount()
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	coreRequire(t, "prepare sender transfer to blind invoice", err)
	coreAssert(t, prepared != nil && prepared.State != nil && prepared.State.TransferID != "" &&
		prepared.TxID != "", "blind proxy prepare omitted the real operation identity")
	delivery, err := sender.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{prepared.State.TransferID})
	coreRequire(t, "deliver blind consignment before broadcast", err)
	coreAssert(t, delivery.AwaitingACK && !delivery.Broadcast && chain.broadcastCount() == beforeBroadcast,
		"blind transaction broadcast before the receiver ACK")

	cleanup := coreInstallUnrelatedCapacityFailure(t, receiver, cfg)
	beforeAttemptBroadcasts := chain.broadcastCount()
	received, err := receiver.ReceiveRGB11ProxyConsignment(context.Background(), invoice.RequestID)
	coreRequire(t, "accept blind receive despite unrelated capacity failure", err)
	coreAssert(t, received.AckPosted && received.AwaitingBroadcast && received.TxID == prepared.TxID,
		"unrelated module failure blocked the exact blind ACK")
	coreAssertRGB11ACKRecoveryPublished(t, receiver, prepared)
	coreAssert(t, chain.broadcastCount() == beforeAttemptBroadcasts,
		"blind receive ACK broadcast the transaction")
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "read sender pending operation before blind broadcast", err)
	coreAssert(t, pending != nil && pending.State.WitnessTxID == prepared.TxID &&
		len(pending.RecipientConsignment) != 0 && len(pending.LocalConsignment) != 0 &&
		len(pending.SignedTx) != 0, "unrelated failure lost sender recovery materials")
	delivery, err = sender.DeliverAndBroadcastRGB11ProxyTransfer(context.Background(),
		[]string{prepared.State.TransferID})
	coreRequire(t, "broadcast blind transfer after positive ACK", err)
	coreAssert(t, delivery.Broadcast && delivery.TxID == prepared.TxID &&
		chain.broadcastCount() == beforeAttemptBroadcasts+1, "blind ACK did not authorize exactly one broadcast")
	chain.confirm(prepared.TxID)
	_, err = sender.RefreshRGB11State(context.Background())
	coreRequire(t, "settle blind sender before device handoff", err)
	_, err = receiver.RefreshRGB11State(context.Background())
	coreRequire(t, "settle blind receiver despite unrelated capacity failure", err)
	coreRequire(t, "confirm blind terminal recovery", receiver.WaitAccountBackup(context.Background(),
		rgb11AccountManagedProviderID, coreSelectedRGBScope(receiver)))
	coreRequire(t, "remove blind capacity fault", cleanup())
	// A local broadcast is not a completed asynchronous backup. Finish both
	// devices' actual recovery closures before cleanup returns to the originals.
	coreSuspendRGBDevice(t, sender)
	coreSuspendRGBDevice(t, receiver)
	coreAssert(t, chain.broadcastCount() == beforeAttemptBroadcasts+1,
		"blind device handoff repeated the original broadcast")
	// Correlate a later source-refresh failure with this independent device's
	// confirmed spend without logging transaction or recovery payloads.
	t.Logf("core-e2e: blind capacity handoff: actor=%d scope_tag=%s witness_tag=%s input_count=%d",
		sender.debugID, coreCASTag(coreSelectedRGBScope(sender)), coreCASTag(prepared.TxID), len(prepared.State.InputOutPoints))
	for _, point := range prepared.State.InputOutPoints {
		t.Logf("core-e2e: blind capacity spent input: point_tag=%s witness_tag=%s", coreCASTag(point), coreCASTag(prepared.TxID))
	}
}

type coreManagedRecordFingerprint struct {
	seq    uint64
	hash   string
	digest string
	bytes  int
}

type coreManagedProfileFingerprint struct {
	dirty         bool
	revision      uint64
	hash          string
	envelope      []byte
	envelopeBytes int
}

func coreManagedRemoteRecords(t *testing.T, manager *Manager,
	cfg coreE2EConfig) map[string]coreManagedRecordFingerprint {
	t.Helper()
	manager.mutex.RLock()
	accountID := manager.accountProfile.AccountID
	secret := bytes.Clone(manager.accountSecret)
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	key, err := account.ManagedModuleKey(secret, accountID, rgb11AccountManagedProviderID, coreSelectedRGBScope(manager))
	coreRequire(t, "derive selected RGB module key", err)
	client := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	record, err := client.GetRecordDirect(key)
	coreRequire(t, "read authoritative RGB module", err)
	return map[string]coreManagedRecordFingerprint{key: {
		seq: record.Seq, hash: dkvsindexer.RecordHash(record).String(),
		digest: coreDigest(record.Value), bytes: len(record.Value),
	}}
}

func coreAssertManagedRemoteEqual(t *testing.T, label string,
	want, got map[string]coreManagedRecordFingerprint) {
	t.Helper()
	if !coreManagedRemoteRecordsEqual(want, got) {
		t.Fatalf("core-e2e: %s changed an authoritative managed record", label)
	}
}

func coreManagedRemoteRecordsEqual(want, got map[string]coreManagedRecordFingerprint) bool {
	return maps.Equal(want, got)
}

func coreAssertMapEqual(t *testing.T, label string, want, got map[string]string) {
	t.Helper()
	if coreDigest(want) != coreDigest(got) {
		t.Fatalf("core-e2e: %s changed", label)
	}
}

func coreSelectedRGBScope(manager *Manager) string {
	scope, err := manager.rgbManager.rgb11AccountManagedScopeID()
	if err != nil {
		panic(err)
	}
	return scope
}

func coreCaptureManagedProfile(manager *Manager) coreManagedProfileFingerprint {
	scope := coreSelectedRGBScope(manager)
	manager.mutex.RLock()
	defer manager.mutex.RUnlock()
	if manager.accountProfile == nil {
		return coreManagedProfileFingerprint{}
	}
	value, err := manager.readAccountLocal(manager.accountProfile.AccountID, rgb11AccountManagedProviderID, scope)
	if err != nil {
		panic(err)
	}
	if value == nil {
		return coreManagedProfileFingerprint{}
	}
	result := coreManagedProfileFingerprint{dirty: value.Generation > value.Confirmed}
	if value.Baseline != nil {
		blob, err := DecodeDKVSBlobValue(value.Baseline.Value)
		if err != nil {
			panic(err)
		}
		result.revision = value.Baseline.Seq
		result.hash = dkvsindexer.RecordHash(value.Baseline).String()
		result.envelope = bytes.Clone(blob.Data)
		result.envelopeBytes = len(blob.Data)
	}
	return result
}

func coreLocalRGBBundleDigests(t *testing.T, manager *Manager) map[string]string {
	t.Helper()
	catalog, err := manager.accountManagedDataCatalog()
	coreRequire(t, "enumerate account scopes for local capacity snapshot", err)
	provider := &rgb11AccountManagedDataProvider{owner: manager}
	payloads, err := provider.Export(catalog)
	coreRequire(t, "export local RGB recovery material for capacity snapshot", err)
	result := make(map[string]string, len(payloads))
	for _, payload := range payloads {
		result[payload.Scope] = coreDigest(payload.Payload)
	}
	return result
}

func coreCurrentManagedBundle(manager *Manager,
	catalog AccountManagedDataCatalog) (account.ManagedDataBundle, error) {
	selected, err := accountScopeCatalog(catalog, coreSelectedRGBScope(manager))
	if err != nil {
		return account.ManagedDataBundle{}, err
	}
	payloads, err := (&rgb11AccountManagedDataProvider{owner: manager}).Export(selected)
	if err != nil {
		return account.ManagedDataBundle{}, err
	}
	// This container holds payloads only; each module has its own DKVS record.
	bundle := account.ManagedDataBundle{}
	for _, payload := range payloads {
		bundle.Items = append(bundle.Items, account.ManagedDataItem{
			Provider: rgb11AccountManagedProviderID, Scope: payload.Scope, Payload: payload.Payload,
		})
	}
	return bundle, nil
}

func coreLogRGB11AccountPackageSize(t *testing.T, label string, manager *Manager) {
	t.Helper()
	catalog, err := manager.accountManagedDataCatalog()
	coreRequire(t, "enumerate account scopes for RGB11 size sample", err)
	bundle, err := coreCurrentManagedBundle(manager, catalog)
	coreRequire(t, "export selected RGB module for RGB11 size sample", err)
	size, err := coreMeasureManagedBundle(t, manager, bundle)
	coreRequire(t, "measure selected RGB module for RGB11 size sample", err)
	provider := &rgb11AccountManagedDataProvider{owner: manager}
	selected, err := accountScopeCatalog(catalog, coreSelectedRGBScope(manager))
	coreRequire(t, "select RGB11 scope for size sample", err)
	payloads, err := provider.Export(selected)
	coreRequire(t, "export RGB11 packages for account size sample", err)
	rpkRaw, rpkEncoded := 0, 0
	for _, payload := range payloads {
		rpkRaw += coreRecoveryPackageRawSize(t, payload.Payload)
		rpkEncoded += len(payload.Payload)
	}
	t.Logf("core-e2e: %s RPK_raw=%d RPK_deflate=%d AMD=%d ADE=%d DKB1=%d", label,
		rpkRaw, rpkEncoded, size.AMDBytes, size.ADEBytes, size.DKB1Bytes)
}

type coreRGBBackupSize struct{ AMDBytes, ADEBytes, DKB1Bytes int }

func coreMeasureManagedBundle(t *testing.T, manager *Manager,
	bundle account.ManagedDataBundle) (coreRGBBackupSize, error) {
	t.Helper()
	manager.mutex.RLock()
	if manager.accountProfile == nil {
		manager.mutex.RUnlock()
		return coreRGBBackupSize{}, errors.New("account profile unavailable")
	}
	accountID, network := manager.accountProfile.AccountID, manager.cfg.Chain
	secret := bytes.Clone(manager.accountSecret)
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	scope := coreSelectedRGBScope(manager)
	for _, item := range bundle.Items {
		if item.Provider == rgb11AccountManagedProviderID && item.Scope == scope {
			encoded, err := account.SealManagedModule(secret, accountID, network, account.ManagedModule{
				Provider: rgb11AccountManagedProviderID, Scope: scope, Codec: "v1", Payload: item.Payload,
			}, nil)
			if err != nil {
				return coreRGBBackupSize{}, err
			}
			blob, err := EncodeDKVSBlobValue(encoded, nil)
			return coreRGBBackupSize{AMDBytes: len(item.Payload), ADEBytes: len(encoded), DKB1Bytes: len(blob)}, err
		}
	}
	return coreRGBBackupSize{}, errors.New("selected RGB module payload unavailable")
}

// This provider fails only its own module. Export honors the selected scope
// catalog used by production independent-module synchronization.
type coreUnrelatedCapacityProvider struct {
	id      string
	payload []byte
}

func (p *coreUnrelatedCapacityProvider) ID() string { return p.id }
func (p *coreUnrelatedCapacityProvider) Export(catalog AccountManagedDataCatalog) ([]AccountManagedDataPayload, error) {
	var result []AccountManagedDataPayload
	for _, scope := range catalog.Scopes {
		result = append(result, AccountManagedDataPayload{Scope: scope.ID(), Payload: bytes.Clone(p.payload)})
	}
	return result, nil
}
func (*coreUnrelatedCapacityProvider) Validate(AccountManagedDataCatalog, []AccountManagedDataPayload) error {
	return nil
}
func (*coreUnrelatedCapacityProvider) Import(AccountManagedDataCatalog, []AccountManagedDataPayload) error {
	return nil
}

func coreInstallUnrelatedCapacityFailure(t *testing.T, manager *Manager, cfg coreE2EConfig) func() error {
	t.Helper()
	coreRequire(t, "publish RGB baseline before independent fault", manager.SyncAccountManagementState(context.Background()))
	beforeLocal := coreLocalRGBBundleDigests(t, manager)
	beforeRemote := coreManagedRemoteRecords(t, manager, cfg)
	beforeProfile := coreCaptureManagedProfile(manager)
	manager.mutex.RLock()
	accountID, network := manager.accountProfile.AccountID, manager.cfg.Chain
	secret := bytes.Clone(manager.accountSecret)
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	scope := coreSelectedRGBScope(manager)
	var err error
	// Match the same real module envelope framing for the independent provider.
	providerID := "zz-capacity-" + coreDigest(t.Name())[:12]
	payload := make([]byte, swire.MaxDKVSBlobValueSize/2)
	for {
		envelope, err := account.SealManagedModule(secret, accountID, network, account.ManagedModule{
			Provider: providerID, Scope: scope, Codec: "v1", Payload: payload,
		}, nil)
		coreRequire(t, "seal unrelated capacity module", err)
		delta := swire.MaxDKVSBlobValueSize + 1 - (len(envelope) + 8)
		if delta == 0 {
			break
		}
		payload = make([]byte, len(payload)+delta)
	}
	provider := &coreUnrelatedCapacityProvider{id: providerID, payload: payload}
	coreRequire(t, "register independent capacity provider", manager.RegisterAccountManagedDataProvider(provider))
	cleaned := false
	cleanup := func() error {
		if cleaned {
			return nil
		}
		// Retire the fixture through explicit module tombstones, never by
		// interpreting a provider's missing Export item as deletion.
		manager.mutex.Lock()
		values, err := manager.listAccountLocal(accountID)
		if err == nil {
			for _, value := range values {
				if value.Provider != provider.ID() || value.Deleted {
					continue
				}
				entry, entryErr := accountEntry(manager.accountSecret, accountID, value.Provider, value.Scope)
				if entryErr != nil {
					err = entryErr
					break
				}
				value.Key, value.Codec = entry.Key, entry.Codec
				value.Deleted = true
				value.Generation++
				value.Captured = value.Generation
				value.LastError, value.Conflict, value.Restoring = "", false, false
				if err = manager.writeAccountLocal(nil, value); err != nil {
					break
				}
			}
		}
		manager.mutex.Unlock()
		if err != nil {
			return err
		}
		if err := manager.SyncAccountManagementState(context.Background()); err != nil {
			return err
		}
		manager.managedDataMu.Lock()
		delete(manager.managedDataProviders, provider.ID())
		manager.managedDataMu.Unlock()
		cleaned = true
		return nil
	}
	t.Cleanup(func() { coreRequire(t, "retire independent capacity fixture", cleanup()) })
	err = manager.SyncAccountManagementState(context.Background())
	coreAssertCapacityRejected(t, err, "independent provider exceeds DKB1 capacity")
	coreAssert(t, accountModuleOnly(err), "independent capacity error escaped its module boundary")
	manager.mutex.RLock()
	failed, readErr := manager.readAccountLocal(accountID, provider.ID(), scope)
	manager.mutex.RUnlock()
	coreRequire(t, "read independently failed module", readErr)
	coreAssert(t, failed != nil && failed.Generation > failed.Confirmed && failed.LastError != "",
		"capacity failure lost the independent module pending/error state")
	coreAssertMapEqual(t, "local RGB recovery after independent failure", beforeLocal, coreLocalRGBBundleDigests(t, manager))
	coreAssertManagedRemoteEqual(t, "remote RGB recovery after independent failure", beforeRemote, coreManagedRemoteRecords(t, manager, cfg))
	coreAssertManagedProfileUnchanged(t, "confirmed RGB receipt after independent failure", beforeProfile, coreCaptureManagedProfile(manager))
	return cleanup
}

func coreAssertCapacityRejected(t *testing.T, err error, action string) {
	t.Helper()
	if err == nil || (!errors.Is(err, dkvsindexer.ErrRecordTooLarge) &&
		!strings.Contains(strings.ToLower(err.Error()), "limit") &&
		!strings.Contains(strings.ToLower(err.Error()), "bytes")) {
		t.Fatalf("core-e2e: %s error=%v; want explicit account-data capacity rejection", action, err)
	}
}

func coreAssertManagedProfileUnchanged(t *testing.T, label string,
	before, after coreManagedProfileFingerprint) {
	t.Helper()
	if before.dirty != after.dirty || before.revision != after.revision ||
		before.hash != after.hash || !bytes.Equal(before.envelope, after.envelope) {
		t.Fatalf("core-e2e: %s changed account profile metadata", label)
	}
}
