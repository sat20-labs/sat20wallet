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

func TestManagedDataCapacityFailureKeepsRGBProofAndRemoteRevision(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, _, store, keys := accountTTLNoopFixture(t)
	catalog, err := manager.accountManagedDataCatalog()
	if err != nil || len(catalog.Scopes) != 1 {
		t.Fatalf("account catalog scopes=%d err=%v", len(catalog.Scopes), err)
	}
	provider := &rgb11AccountManagedDataProvider{owner: manager}
	accounts, err := provider.accountsByScope()
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := manager.newScopedRGB11Manager(accounts[catalog.Scopes[0].ID()])
	if err != nil {
		t.Fatal(err)
	}
	outpoint, assetName, _ := seedRGB11DurableAccountRecoveryState(t, scoped)
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatalf("publish nonempty RGB11 recovery baseline before capacity failure: %v", err)
	}
	base, _, err := manager.exportAccountManagedData(catalog, manager.accountProfile.ManagedDataRevision)
	if err != nil {
		t.Fatal(err)
	}
	manager.mutex.RLock()
	accountID := manager.accountProfile.AccountID
	secret := append([]byte(nil), manager.accountSecret...)
	beforeRevision := manager.accountProfile.ManagedDataRevision
	beforeHash := manager.accountProfile.ManagedDataHash
	beforeEnvelope := append([]byte(nil), manager.accountProfile.ManagedDataEnvelope...)
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	defer zeroBytes(beforeEnvelope)
	baselineBlob, err := store.GetAuthoritative(keys[1])
	if err != nil || baselineBlob == nil || len(baselineBlob.Value) == 0 {
		t.Fatalf("successful RGB11 baseline did not leave a nonempty remote bundle: value=%v err=%v",
			baselineBlob != nil, err)
	}
	baselinePackageCount := 0
	for _, item := range base.Items {
		if item.Provider == rgb11AccountManagedProviderID && item.Scope == catalog.Scopes[0].ID() {
			packageValue, decodeErr := rgb11wallet.DecodeRecoveryPackage(item.Payload)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			for _, record := range packageValue.ProjectionRecords {
				if strings.HasPrefix(record.Key, "proof-") {
					baselinePackageCount++
				}
			}
		}
	}
	if baselinePackageCount == 0 {
		t.Fatal("successful remote baseline did not contain the locally seeded RGB proof")
	}
	oversizedPayload := accountManagedPayloadForEnvelopeSize(t, secret, accountID, base,
		"zz-capacity", catalog.Scopes[0].ID(), swire.MaxDKVSBlobValueSize)
	if err := manager.RegisterAccountManagedDataProvider(&accountManagedCapacityProvider{
		scope: catalog.Scopes[0].ID(), payload: oversizedPayload,
	}); err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(oversizedPayload)

	beforeRemote := make(map[string]*dkvsValue, len(keys))
	for _, key := range keys {
		value, err := store.GetAuthoritative(key)
		if err != nil {
			t.Fatal(err)
		}
		beforeRemote[key] = value
	}
	if err := manager.markAccountManagedDataDirtyDeferred("zz-capacity"); err != nil {
		t.Fatal(err)
	}
	err = manager.SyncAccountManagementState(context.Background())
	if !errors.Is(err, dkvsindexer.ErrRecordTooLarge) {
		t.Fatalf("oversized final DKB1 sync error=%v, want ErrRecordTooLarge", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("envelope=%d bytes", swire.MaxDKVSBlobValueSize)) ||
		!strings.Contains(err.Error(), fmt.Sprintf("payload limit=%d bytes", swire.MaxDKVSBlobValueSize-8)) {
		t.Fatalf("capacity error lacks exact final envelope/payload limit: %v", err)
	}
	manager.mutex.RLock()
	profile := manager.accountProfile
	profilePreserved := profile != nil && profile.ManagedDataDirty &&
		profile.ManagedDataRevision == beforeRevision && profile.ManagedDataHash == beforeHash &&
		bytes.Equal(profile.ManagedDataEnvelope, beforeEnvelope)
	gotDirty, gotRevision := false, uint64(0)
	gotHash, gotEnvelope := false, false
	if profile != nil {
		gotDirty = profile.ManagedDataDirty
		gotRevision = profile.ManagedDataRevision
		gotHash = profile.ManagedDataHash == beforeHash
		gotEnvelope = bytes.Equal(profile.ManagedDataEnvelope, beforeEnvelope)
	}
	manager.mutex.RUnlock()
	if !profilePreserved {
		t.Fatalf("capacity failure changed durable state: dirty=%t revision=%d hash_unchanged=%t envelope_unchanged=%t",
			gotDirty, gotRevision, gotHash, gotEnvelope)
	}
	for _, key := range keys {
		value, err := store.GetAuthoritative(key)
		if err != nil {
			t.Fatal(err)
		}
		before := beforeRemote[key]
		if before.Hash != value.Hash || before.Seq != value.Seq || !bytes.Equal(before.Value, value.Value) {
			t.Fatalf("capacity failure changed authoritative remote account record %q", key)
		}
	}
	localPayloads, err := provider.Export(catalog)
	if err != nil || len(localPayloads) != 1 {
		t.Fatalf("local RGB recovery payloads=%d err=%v", len(localPayloads), err)
	}
	localPackage, err := rgb11wallet.DecodeRecoveryPackage(localPayloads[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	foundProof := false
	for _, record := range localPackage.ProjectionRecords {
		if strings.HasPrefix(record.Key, "proof-"+outpoint+"-") {
			foundProof = true
		}
	}
	if !foundProof {
		t.Fatalf("capacity failure lost local RGB proof for %s", assetName.String())
	}
	prospective, _, err := manager.exportAccountManagedData(catalog, beforeRevision)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := account.EncodeManagedDataBundle(prospective)
	if err != nil {
		t.Fatal(err)
	}
	envelope, _, err := account.SealManagedDataBundleWithInfo(secret, accountID, prospective, nil)
	if err != nil {
		t.Fatalf("expected the exact ADE boundary to seal before DKB1 rejection: %v", err)
	}
	blob, blobErr := EncodeDKVSBlobValue(envelope, nil)
	if !errors.Is(blobErr, dkvsindexer.ErrRecordTooLarge) {
		t.Fatalf("prospective DKB1 error=%v, want ErrRecordTooLarge", blobErr)
	}
	if len(envelope)+8 <= swire.MaxDKVSBlobValueSize {
		t.Fatalf("prospective DKB1 bytes=%d unexpectedly fit limit=%d", len(envelope)+8, swire.MaxDKVSBlobValueSize)
	}
	localRecovery, err := rgb11wallet.DecodeRecoveryPackage(localPayloads[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	_, rpkRawBytes, err := rgb11wallet.EncodeRecoveryPackageWithSize(localRecovery)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("capacity failure preserved local proof and prior nonempty remote RGB bundle: RPK_raw=%d RPK_deflate=%d AMD=%d ADE=%d DKB1_would_be=%d DKB1_limit=%d rejected_blob_bytes=%d old_blob_bytes=%d baseline_revision=%d baseline_hash_present=%t",
		rpkRawBytes, len(localPayloads[0].Payload), len(plain), len(envelope), len(envelope)+8,
		swire.MaxDKVSBlobValueSize, len(blob), len(baselineBlob.Value), beforeRevision,
		beforeHash != "")
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
