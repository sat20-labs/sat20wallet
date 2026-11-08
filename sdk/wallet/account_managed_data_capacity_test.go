package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// This test exercises the independent 8-byte DKB1 envelope at its exact wire
// boundary. Its deterministic high-entropy opaque payload is only a codec
// boundary fixture; it is not used to claim a real RGB history size.
func TestManagedDataBlobCapacityIncludesDKB1Header(t *testing.T) {
	if account.ManagedDataMaxPayload != swire.MaxDKVSBlobValueSize {
		t.Fatalf("managed-data limit=%d DKVS blob limit=%d",
			account.ManagedDataMaxPayload, swire.MaxDKVSBlobValueSize)
	}
	secret := make([]byte, 32)
	secret[0] = 1
	accountID := strings.Repeat("11", 32)

	for _, test := range []struct {
		name              string
		wantEnvelopeBytes int
		wantWrapperError  bool
		wantSealError     bool
	}{
		{name: "exact wire limit", wantEnvelopeBytes: swire.MaxDKVSBlobValueSize - 8},
		{name: "DKB1 overflow by one", wantEnvelopeBytes: swire.MaxDKVSBlobValueSize - 7, wantWrapperError: true},
		{name: "cipher limit leaves no room for wrapper", wantEnvelopeBytes: swire.MaxDKVSBlobValueSize, wantWrapperError: true},
		{name: "encrypted envelope exceeds limit", wantEnvelopeBytes: swire.MaxDKVSBlobValueSize + 1, wantSealError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle := managedDataBundleWithExactEnvelopeSize(t, secret, accountID,
				test.wantEnvelopeBytes)
			plain, err := account.EncodeManagedDataBundle(bundle)
			if err != nil {
				t.Fatal(err)
			}
			envelope, info, err := account.SealManagedDataBundleWithInfo(secret, accountID, bundle, nil)
			if test.wantSealError {
				if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%d bytes", test.wantEnvelopeBytes)) ||
					!strings.Contains(err.Error(), fmt.Sprintf("limit is %d bytes", account.ManagedDataMaxPayload)) {
					t.Fatalf("encrypted envelope error=%v; want actual=%d limit=%d",
						err, test.wantEnvelopeBytes, account.ManagedDataMaxPayload)
				}
				t.Logf("encrypted envelope rejected: AMD=%d desired_ADE=%d ADE_limit=%d", len(plain), test.wantEnvelopeBytes, account.ManagedDataMaxPayload)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if info.Compressed {
				t.Fatal("high-entropy boundary fixture was unexpectedly compressed")
			}
			if len(envelope) != test.wantEnvelopeBytes {
				t.Fatalf("ADE bytes=%d want=%d", len(envelope), test.wantEnvelopeBytes)
			}
			if len(plain)+managedDataEnvelopeOverhead(t, secret, accountID) != len(envelope) {
				t.Fatalf("uncompressed AMD/ADE size delta=%d want measured overhead", len(envelope)-len(plain))
			}

			blob, err := EncodeDKVSBlobValue(envelope, nil)
			if test.wantWrapperError {
				if !errors.Is(err, dkvsindexer.ErrRecordTooLarge) {
					t.Fatalf("DKB1 wrapper error=%v, want ErrRecordTooLarge", err)
				}
				_, mutationErr := accountManagedDataMutation(nil, nil, "", envelope)
				if !errors.Is(mutationErr, dkvsindexer.ErrRecordTooLarge) {
					t.Fatalf("account-managed mutation error=%v, want ErrRecordTooLarge", mutationErr)
				}
				if !strings.Contains(mutationErr.Error(), fmt.Sprintf("envelope=%d bytes", len(envelope))) ||
					!strings.Contains(mutationErr.Error(), fmt.Sprintf("payload limit=%d bytes", swire.MaxDKVSBlobValueSize-8)) {
					t.Fatalf("account-managed mutation error lacks actual/limit: %v", mutationErr)
				}
				if len(blob) != 0 {
					t.Fatalf("overflow produced a blob of %d bytes", len(blob))
				}
				t.Logf("capacity boundary overflow: AMD=%d ADE=%d DKB1=%d (limit=%d)",
					len(plain), len(envelope), len(envelope)+8, swire.MaxDKVSBlobValueSize)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(blob) != swire.MaxDKVSBlobValueSize {
				t.Fatalf("DKB1 bytes=%d want=%d", len(blob), swire.MaxDKVSBlobValueSize)
			}
			t.Logf("capacity boundary accepted: AMD=%d ADE=%d DKB1=%d (limit=%d)",
				len(plain), len(envelope), len(blob), swire.MaxDKVSBlobValueSize)
		})
	}
}

type accountManagedCapacityProvider struct {
	scope   string
	payload []byte
}

func (p *accountManagedCapacityProvider) ID() string { return "zz-capacity" }
func (p *accountManagedCapacityProvider) Export(AccountManagedDataCatalog) ([]AccountManagedDataPayload, error) {
	return []AccountManagedDataPayload{{Scope: p.scope, Payload: append([]byte(nil), p.payload...)}}, nil
}
func (p *accountManagedCapacityProvider) Validate(AccountManagedDataCatalog, []AccountManagedDataPayload) error {
	return nil
}
func (p *accountManagedCapacityProvider) Import(AccountManagedDataCatalog, []AccountManagedDataPayload) error {
	return nil
}

func TestManagedDataBundleCapacityReportsPlaintextActualAndLimit(t *testing.T) {
	const provider, scope = "rgb11", "testnet/capacityfixture/0"
	base := account.ManagedDataBundle{Version: account.ManagedDataBundleVersion, Revision: 1,
		Items: []account.ManagedDataItem{{Provider: provider, Scope: scope, Payload: []byte{0x42}}}}
	baseRaw, err := account.EncodeManagedDataBundle(base)
	if err != nil {
		t.Fatal(err)
	}
	bundle := base
	bundle.Items = []account.ManagedDataItem{{Provider: provider, Scope: scope,
		Payload: make([]byte, account.ManagedDataMaxPayload)}}
	_, err = account.EncodeManagedDataBundle(bundle)
	var encodedLength [binary.MaxVarintLen64]byte
	wantActual := len(baseRaw) - 1 + account.ManagedDataMaxPayload +
		binary.PutUvarint(encodedLength[:], uint64(account.ManagedDataMaxPayload)) - 1
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%d bytes", wantActual)) ||
		!strings.Contains(err.Error(), fmt.Sprintf("limit is %d bytes", account.ManagedDataMaxPayload)) {
		t.Fatalf("plaintext capacity error=%v; want actual=%d limit=%d",
			err, wantActual, account.ManagedDataMaxPayload)
	}
	t.Logf("plaintext bundle rejected: AMD=%d limit=%d", wantActual, account.ManagedDataMaxPayload)
}

func TestModuleCapacityKeepsRGBProof(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	chain := newCoreE2EChain()
	rpc := &coreE2EL1Indexer{chain: chain}
	var source *Manager
	var issued *RGB11IssueResult
	manager, remote, store, _ := accountTTLWithSeed(t, func(owner *Manager, remote *rgb11MemoryDKVSHTTP) {
		wallet := cloneWalletAtAccountZero(owner.wallet)
		chain.fund(t, wallet.GetAddress(), 6)
		source = newRGB11FlowManager(t, wallet, rpc, chain, 1660, accountGroupUnprepared)
		prepareRGBEndpoint(t, source, remote)
		var issueErr error
		issued, issueErr = source.rgbManager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
			Schema: "NIA", Ticker: "CAP", Name: "Capacity recovery fixture", Amounts: []uint64{25},
		})
		if issueErr != nil {
			t.Fatal(issueErr)
		}
	})
	catalog, err := manager.accountManagedDataCatalog()
	if err != nil || len(catalog.Scopes) != 1 {
		t.Fatalf("account catalog scopes=%d err=%v", len(catalog.Scopes), err)
	}
	scope := catalog.Scopes[0].ID()
	provider := &rgb11AccountManagedDataProvider{owner: manager}
	// Seed a real issued contract and proof; synthetic proof bytes do not
	// establish an independently restorable RGB baseline.
	manager.accountBackgroundWG.Wait()
	manager.l1IndexerClient = NewIndexerRPCClientMgr()
	manager.l1IndexerClient.Set(rpc)
	manager.rgbManager.evidence = chain
	defer manager.rgbManager.scopeStates.stopReconciliations()
	installRGB11PublicContractFixture(t, source, remote, issued.ContractID)
	useRGB11TestCoreAuthority(t, manager)
	sourceCatalog, err := source.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := (&rgb11AccountManagedDataProvider{owner: source}).Export(sourceCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Import(catalog, payloads); err != nil {
		t.Fatal(err)
	}
	// Import installs the real RGB projection. Record that public module change
	// in the same local durability model used by normal provider writes before
	// taking its first backup snapshot.
	batch, err := NewAtomicWriteBatch(manager.db)
	if err != nil {
		t.Fatal(err)
	}
	finish, err := manager.StageAccountChange(batch, rgb11AccountManagedProviderID, scope)
	if err != nil {
		batch.Close()
		t.Fatal(err)
	}
	defer finish(false)
	if err := batch.Flush(); err != nil {
		batch.Close()
		t.Fatal(err)
	}
	finish(true)
	batch.Close()
	proofs, err := source.rgbManager.projectionStore.ListProofs()
	if err != nil || len(proofs) == 0 {
		t.Fatalf("issued proof unavailable: %v", err)
	}
	outpoint := proofs[0].OutPoint
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatalf("publish nonempty RGB11 module baseline before capacity failure: %v", err)
	}

	manager.mutex.RLock()
	accountID := manager.accountProfile.AccountID
	secret := append([]byte(nil), manager.accountSecret...)
	network := manager.cfg.Chain
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	indexKey, err := account.ManagedIndexKey(accountID)
	if err != nil {
		t.Fatal(err)
	}
	remoteIndex, err := store.GetAuthoritative(indexKey)
	if err != nil || remoteIndex == nil {
		t.Fatalf("read confirmed account index: value=%v err=%v", remoteIndex != nil, err)
	}
	if err := dkvsindexer.VerifySignature(remoteIndex.record); err != nil {
		t.Fatalf("verify account index signature: %v", err)
	}
	index, err := account.OpenManagedIndex(secret, accountID, network, remoteIndex.Value)
	if err != nil {
		t.Fatal(err)
	}
	var rgbEntry account.ManagedEntry
	for _, entry := range index.Entries {
		if !entry.Deleted && entry.Provider == rgb11AccountManagedProviderID && entry.Scope == scope {
			rgbEntry = entry
		}
	}
	if rgbEntry.Key == "" {
		t.Fatal("confirmed account index does not contain the RGB11 module")
	}
	rgbRecord, err := store.GetAuthoritative(rgbEntry.Key)
	if err != nil || rgbRecord == nil {
		t.Fatalf("read confirmed RGB11 module: value=%v err=%v", rgbRecord != nil, err)
	}
	if err := dkvsindexer.VerifySignature(rgbRecord.record); err != nil {
		t.Fatalf("verify RGB11 module signature: %v", err)
	}
	blob, err := DecodeDKVSBlobValue(rgbRecord.Value)
	if err != nil {
		t.Fatal(err)
	}
	if len(blob.Metadata) != 0 {
		t.Fatal("confirmed RGB11 module unexpectedly contains plaintext metadata")
	}
	managedModule, err := account.OpenManagedModule(secret, accountID, network, rgbEntry.Key, blob.Data)
	if err != nil {
		t.Fatal(err)
	}
	if managedModule.Provider != rgbEntry.Provider || managedModule.Scope != rgbEntry.Scope || managedModule.Codec != rgbEntry.Codec {
		t.Fatal("confirmed module does not match its authenticated index entry")
	}
	remotePackage, err := rgb11wallet.DecodeRecoveryPackage(managedModule.Payload)
	if err != nil {
		t.Fatal(err)
	}
	remoteHasProof := false
	for _, record := range remotePackage.ProjectionRecords {
		if strings.HasPrefix(record.Key, "proof-"+outpoint+"-") {
			remoteHasProof = true
		}
	}
	if !remoteHasProof {
		t.Fatal("confirmed remote RGB11 module does not contain the issued proof")
	}

	walletModule, err := accountEntry(accountID, "wallets", AccountManagedDataGlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	walletModuleKey := walletModule.Key
	if walletModuleKey == "" {
		t.Fatal("wallet module key is empty")
	}
	root, err := manager.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		t.Fatal(err)
	}
	beforeRemote := make(map[string]*dkvsValue, 5)
	for _, key := range []string{indexKey, walletModuleKey, wrapperKey, rgbEntry.Key} {
		value, err := store.GetAuthoritative(key)
		if err != nil || value == nil {
			t.Fatalf("read confirmed record %q: value=%v err=%v", key, value != nil, err)
		}
		beforeRemote[key] = value
	}

	capacityEntry, err := accountEntry(accountID, "zz-capacity", scope)
	if err != nil {
		t.Fatal(err)
	}
	// ManagedObjectMaxBytes and MaxDKVSBlobValueSize are both 1 MiB. Sealing
	// adds 33 bytes (AMOE magic, nonce, and GCM tag), while DKB1 adds 8 bytes;
	// therefore there is an 8-byte interval of valid sealed modules that DKB1
	// cannot carry. Find the first such payload while keeping the module valid.
	low, high := 1, account.ManagedDataMaxPayload
	for low < high {
		middle := low + (high-low)/2
		candidate := capacityTestNoise(middle)
		encoded, sealErr := account.SealManagedModule(secret, accountID, network,
			account.ManagedModule{Provider: capacityEntry.Provider, Scope: capacityEntry.Scope,
				Codec: capacityEntry.Codec, Payload: candidate}, nil)
		zeroBytes(candidate)
		if sealErr != nil {
			high = middle
			continue
		}
		_, wrapErr := EncodeDKVSBlobValue(encoded, nil)
		zeroBytes(encoded)
		if errors.Is(wrapErr, dkvsindexer.ErrRecordTooLarge) {
			high = middle
		} else if wrapErr == nil {
			low = middle + 1
		} else {
			t.Fatalf("encode DKB1 capacity candidate: %v", wrapErr)
		}
	}
	oversizedPayload := capacityTestNoise(low)
	defer zeroBytes(oversizedPayload)
	encodedModule, err := account.SealManagedModule(secret, accountID, network,
		account.ManagedModule{Provider: capacityEntry.Provider, Scope: capacityEntry.Scope,
			Codec: capacityEntry.Codec, Payload: oversizedPayload}, nil)
	if err != nil {
		t.Fatalf("seal valid account module just beyond DKB1 limit: %v", err)
	}
	if len(encodedModule) > account.ManagedDataMaxPayload {
		t.Fatalf("account module envelope=%d exceeds account module limit=%d", len(encodedModule), account.ManagedDataMaxPayload)
	}
	if len(encodedModule)+8 <= swire.MaxDKVSBlobValueSize {
		t.Fatalf("module bytes=%d plus DKB1 header fit wire limit=%d", len(encodedModule), swire.MaxDKVSBlobValueSize)
	}
	_, capacityErr := EncodeDKVSBlobValue(encodedModule, nil)
	defer zeroBytes(encodedModule)
	if !errors.Is(capacityErr, dkvsindexer.ErrRecordTooLarge) {
		t.Fatalf("single module DKB1 error=%v, want ErrRecordTooLarge", capacityErr)
	}
	if err := manager.RegisterAccountManagedDataProvider(&accountManagedCapacityProvider{
		scope: scope, payload: oversizedPayload,
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.markAccountManagedDataDirtyDeferred("zz-capacity"); err != nil {
		t.Fatal(err)
	}
	err = manager.SyncAccountManagementState(context.Background())
	if !errors.Is(err, dkvsindexer.ErrRecordTooLarge) {
		t.Fatalf("oversized per-module sync error=%v, want ErrRecordTooLarge", err)
	}

	status := manager.GetAccountManagementStatus()
	if !status.DKVSWriteAllowed {
		t.Fatalf("unrelated module capacity failure revoked a healthy account baseline: %+v", status)
	}
	if err := manager.CheckAccountWrite(rgb11AccountManagedProviderID, scope); err != nil {
		t.Fatalf("capacity failure blocked the healthy RGB11 module: %v", err)
	}
	if err := manager.CheckAccountWrite("zz-capacity", scope); err == nil {
		t.Fatal("oversized module remained eligible for a new module write")
	}
	var capacityStatus, rgbStatus *AccountModuleStatus
	for index := range status.Modules {
		module := &status.Modules[index]
		if module.Provider == "zz-capacity" && module.Scope == scope {
			capacityStatus = module
		}
		if module.Provider == rgb11AccountManagedProviderID && module.Scope == scope {
			rgbStatus = module
		}
	}
	if capacityStatus == nil || capacityStatus.WriteAllowed || capacityStatus.BackupState != "error" || capacityStatus.LastError == "" {
		t.Fatalf("failed module status does not expose its capacity failure: %+v", capacityStatus)
	}
	if rgbStatus == nil || !rgbStatus.WriteAllowed {
		t.Fatalf("healthy RGB11 module lost write eligibility: %+v", rgbStatus)
	}
	localCapacity, err := manager.readAccountLocal(accountID, "zz-capacity", scope)
	if err != nil || localCapacity == nil {
		t.Fatalf("read failed per-module receipt: value=%v err=%v", localCapacity != nil, err)
	}
	if localCapacity.AccountID != accountID || localCapacity.Network != network ||
		localCapacity.Provider != capacityEntry.Provider || localCapacity.Scope != capacityEntry.Scope {
		t.Fatal("capacity failure changed the durable module identity")
	}
	pendingEntry, err := accountEntry(localCapacity.AccountID, localCapacity.Provider, localCapacity.Scope)
	if err != nil || pendingEntry != capacityEntry ||
		localCapacity.Key != "" && localCapacity.Key != capacityEntry.Key ||
		localCapacity.Codec != "" && localCapacity.Codec != capacityEntry.Codec {
		t.Fatalf("capacity failure retained an inconsistent module destination: %v", err)
	}
	// Encoding failed before capture: retain the durable change marker, not
	// a fabricated frozen snapshot or confirmation of the oversized value.
	if localCapacity.Generation != 1 || localCapacity.Captured != 0 || localCapacity.Confirmed != 0 ||
		len(localCapacity.Value) != 0 || localCapacity.Baseline != nil ||
		localCapacity.LastError != capacityErr.Error() {
		t.Fatalf("capacity failure lost the pre-capture marker: generation=%d captured=%d confirmed=%d value_bytes=%d baseline=%t error_matches=%t",
			localCapacity.Generation, localCapacity.Captured, localCapacity.Confirmed,
			len(localCapacity.Value), localCapacity.Baseline != nil, localCapacity.LastError == capacityErr.Error())
	}
	if _, err := store.GetAuthoritative(capacityEntry.Key); !errors.Is(err, ErrDKVSRecordNotFound) {
		t.Fatalf("oversized module was published remotely: %v", err)
	}

	for key, before := range beforeRemote {
		after, err := store.GetAuthoritative(key)
		if err != nil || after == nil {
			t.Fatalf("read prior healthy record %q after capacity failure: value=%v err=%v", key, after != nil, err)
		}
		if before.Hash != after.Hash || before.Seq != after.Seq || !bytes.Equal(before.Value, after.Value) {
			t.Fatalf("capacity failure changed healthy remote record %q", key)
		}
	}
	afterIndex, err := store.GetAuthoritative(indexKey)
	if err != nil || afterIndex == nil {
		t.Fatalf("read account index after capacity failure: value=%v err=%v", afterIndex != nil, err)
	}
	decodedIndex, err := account.OpenManagedIndex(secret, accountID, network, afterIndex.Value)
	if err != nil {
		t.Fatal(err)
	}
	keptRGB := false
	for _, entry := range decodedIndex.Entries {
		if !entry.Deleted && entry.Provider == rgbEntry.Provider && entry.Scope == rgbEntry.Scope && entry.Key == rgbEntry.Key {
			keptRGB = true
		}
		if !entry.Deleted && entry.Provider == "zz-capacity" && entry.Scope == scope {
			t.Fatal("directory advertised an oversized module without a published record")
		}
	}
	if !keptRGB {
		t.Fatal("capacity failure removed the valid RGB11 module from the directory")
	}

	localPayloads, err := provider.Export(catalog)
	if err != nil || len(localPayloads) != len(catalog.Scopes) {
		t.Fatalf("local RGB recovery payloads=%d err=%v", len(localPayloads), err)
	}
	var localProofPayload []byte
	for _, payload := range localPayloads {
		if payload.Scope == scope {
			localProofPayload = payload.Payload
			break
		}
	}
	if len(localProofPayload) == 0 {
		t.Fatalf("local RGB recovery payload for proof scope %q is missing", scope)
	}
	localPackage, err := rgb11wallet.DecodeRecoveryPackage(localProofPayload)
	if err != nil {
		t.Fatal(err)
	}
	localHasProof := false
	for _, record := range localPackage.ProjectionRecords {
		if strings.HasPrefix(record.Key, "proof-"+outpoint+"-") {
			localHasProof = true
		}
	}
	if !localHasProof {
		t.Fatal("capacity failure lost the original local RGB proof")
	}
}
func accountManagedPayloadForEnvelopeSize(t *testing.T, secret []byte, accountID string,
	base account.ManagedDataBundle, provider, scope string, wantEnvelopeBytes int) []byte {
	t.Helper()
	payload := capacityTestNoise(1)
	overhead := managedDataEnvelopeOverhead(t, secret, accountID)
	wantPlainBytes := wantEnvelopeBytes - overhead
	for attempt := 0; attempt < 8; attempt++ {
		items := append([]account.ManagedDataItem(nil), base.Items...)
		items = append(items, account.ManagedDataItem{Provider: provider, Scope: scope, Payload: payload})
		bundle := base
		bundle.Items = items
		plain, err := account.EncodeManagedDataBundle(bundle)
		if err != nil {
			t.Fatal(err)
		}
		delta := wantPlainBytes - len(plain)
		if delta == 0 {
			compressed, err := account.ManagedDataBundleCompressionBeneficial(bundle)
			if err != nil {
				t.Fatal(err)
			}
			if compressed {
				t.Fatal("high-entropy account capacity payload compressed under bundle policy")
			}
			zeroBytes(plain)
			return payload
		}
		zeroBytes(plain)
		if len(payload)+delta <= 0 {
			t.Fatalf("capacity payload adjustment underflow: payload=%d delta=%d", len(payload), delta)
		}
		payload = capacityTestNoise(len(payload) + delta)
	}
	t.Fatalf("cannot construct account plaintext size %d for ADE target %d", wantPlainBytes, wantEnvelopeBytes)
	return nil
}

func managedDataEnvelopeOverhead(t *testing.T, secret []byte, accountID string) int {
	t.Helper()
	bundle := account.ManagedDataBundle{Version: account.ManagedDataBundleVersion, Revision: 1,
		Items: []account.ManagedDataItem{{Provider: "rgb11", Scope: "testnet/capacityfixture/0", Payload: []byte{0x42}}}}
	plain, err := account.EncodeManagedDataBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	envelope, info, err := account.SealManagedDataBundleWithInfo(secret, accountID, bundle, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(envelope)
	if info.Compressed {
		t.Fatal("small envelope-overhead fixture unexpectedly compressed")
	}
	return len(envelope) - len(plain)
}

func managedDataBundleWithExactEnvelopeSize(t *testing.T, secret []byte, accountID string,
	wantEnvelopeBytes int) account.ManagedDataBundle {
	t.Helper()
	const provider, scope = "rgb11", "testnet/capacityfixture/0"
	base := account.ManagedDataBundle{
		Version: account.ManagedDataBundleVersion, Revision: 1,
		Items: []account.ManagedDataItem{{Provider: provider, Scope: scope, Payload: []byte{0x42}}},
	}
	baseRaw, err := account.EncodeManagedDataBundle(base)
	if err != nil {
		t.Fatal(err)
	}
	wantPlainBytes := wantEnvelopeBytes - managedDataEnvelopeOverhead(t, secret, accountID)
	payloadSize := wantPlainBytes - len(baseRaw) + len(base.Items[0].Payload)
	if payloadSize <= 0 {
		t.Fatalf("invalid boundary payload size %d", payloadSize)
	}
	payload := capacityTestNoise(payloadSize)
	bundle := base
	bundle.Items = []account.ManagedDataItem{{Provider: provider, Scope: scope, Payload: payload}}
	for attempt := 0; attempt < 8; attempt++ {
		plain, err := account.EncodeManagedDataBundle(bundle)
		if err != nil {
			t.Fatal(err)
		}
		delta := wantPlainBytes - len(plain)
		if delta == 0 {
			compressed, err := account.ManagedDataBundleCompressionBeneficial(bundle)
			if err != nil {
				t.Fatal(err)
			}
			if compressed {
				t.Fatal("boundary fixture is compressible under managed-data policy")
			}
			return bundle
		}
		payloadSize += delta
		if payloadSize <= 0 {
			t.Fatalf("invalid adjusted boundary payload size %d", payloadSize)
		}
		payload = capacityTestNoise(payloadSize)
		bundle.Items[0].Payload = payload
	}
	t.Fatalf("could not make uncompressed ADE exactly %d bytes", wantEnvelopeBytes)
	return account.ManagedDataBundle{}
}

func capacityTestNoise(size int) []byte {
	result := make([]byte, size)
	for offset := 0; offset < len(result); {
		var counter [8]byte
		counter[0] = byte(offset)
		counter[1] = byte(offset >> 8)
		counter[2] = byte(offset >> 16)
		counter[3] = byte(offset >> 24)
		digest := sha256.Sum256(counter[:])
		offset += copy(result[offset:], digest[:])
	}
	return result
}
