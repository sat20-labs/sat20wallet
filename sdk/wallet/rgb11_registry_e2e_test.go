package wallet

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Keep this entrypoint: the naming workflow invokes it explicitly. These tests
// run the real SDK HTTP client against real DKVS state in isolated memory. The
// loopback adapter exposes only the read route; it is not a full node/STP test.
func TestRGB11RegistrySDKDKVSE2E(t *testing.T) {
	t.Run("HTTPRoundTripAndSnapshotRecovery", rgb11RegistryHTTPRoundTripE2E)
	t.Run("RejectUntrustedHTTPResponses", rgb11RegistryHTTPValidationE2E)
}

type rgb11RegistryE2ERecordFactory func(provider, ticker, assetType string, ordinal uint64, contractID string) *swire.DKVSRecord

func newRGB11RegistryE2ESource(t *testing.T) (*satoshinetDKVSTestTransport, rgb11RegistryE2ERecordFactory) {
	t.Helper()
	// Existing public CoreNode test mnemonic. No live wallet or network is used.
	coreWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	if coreWallet == nil || coreWallet.GetPubKey() == nil {
		t.Fatal("create CoreNode test wallet")
	}
	transport := newSatoshiNetDKVSTestTransport()
	transport.height = 100
	transport.indexer.SetSystemVerifier(dkvsindexer.StaticSystemVerifier{
		Keys: [][]byte{coreWallet.GetPubKey().SerializeCompressed()},
	})
	makeRecord := func(provider, ticker, assetType string, ordinal uint64, contractID string) *swire.DKVSRecord {
		t.Helper()
		key, err := dkvsindexer.RGB11RegistryKey(provider, ticker, ordinal)
		if err != nil {
			t.Fatal(err)
		}
		value, err := dkvsindexer.EncodeRGB11RegistryValue(assetType, contractID)
		if err != nil {
			t.Fatal(err)
		}
		record, err := NewDKVSSignedRecord(coreWallet, key, value,
			dkvsindexer.RecordOptions{Seq: 1, IssueHeight: transport.height})
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	return transport, makeRecord
}

func newRGB11RegistryE2EHTTPClient(t *testing.T, transport *satoshinetDKVSTestTransport,
	mutate func(*dkvsindexer.PrefixReadResult)) (*SatsNetDKVSClient, *atomic.Int64) {
	t.Helper()
	reads := new(atomic.Int64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/testnet/v3/dkvs/prefixes/read" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			Prefix string `json:"prefix"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := transport.indexer.ReadPrefix(request.Prefix)
		if err == nil && mutate != nil {
			// Mutate only the response, never the real server's stored records.
			var copied dkvsindexer.PrefixReadResult
			var raw []byte
			raw, err = json.Marshal(result)
			if err == nil {
				err = json.Unmarshal(raw, &copied)
			}
			if err == nil {
				mutate(&copied)
				result = &copied
			}
		}
		raw, encodeErr := satoshinetTestResponse(result, err)
		if encodeErr != nil {
			http.Error(w, encodeErr.Error(), http.StatusInternalServerError)
			return
		}
		reads.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(server.Close)
	httpClient := server.Client()
	httpClient.Timeout = 5 * time.Second
	client := NewSatsNetDKVSClient("http", strings.TrimPrefix(server.URL, "http://"), "testnet", &NetClient{Client: httpClient})
	return client, reads
}

func rgb11RegistryHTTPRoundTripE2E(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	client, reads := newRGB11RegistryE2EHTTPClient(t, source, nil)
	var last *swire.DKVSRecord
	for ordinal := uint64(1); ordinal <= 12; ordinal++ {
		assetType := "f"
		if ordinal%2 == 0 {
			assetType = "n"
		}
		contractID := fmt.Sprintf("%064x", 400+ordinal)
		last = makeRecord("alice", "USD", assetType, ordinal, contractID)
		if updated, err := source.indexer.PutInternalRGB11Registry(last); err != nil || !updated {
			t.Fatalf("register ordinal %d: updated=%v err=%v", ordinal, updated, err)
		}
	}
	if updated, err := source.indexer.PutInternalRGB11Registry(last); err != nil || updated {
		t.Fatalf("retry must be idempotent: updated=%v err=%v", updated, err)
	}
	if count, err := source.indexer.RGB11RegistryCount("alice", "USD"); err != nil || count != 12 {
		t.Fatalf("FT/NFT must share one ordinal namespace: count=%d err=%v", count, err)
	}
	for ordinal := uint64(1); ordinal <= 12; ordinal++ {
		contractID := fmt.Sprintf("%064x", 400+ordinal)
		assetType := "f"
		if ordinal%2 == 0 {
			assetType = "n"
		}
		expected, err := rgb11wallet.BuildRegisteredAssetName("USD", assetType, "alice", ordinal)
		if err != nil {
			t.Fatal(err)
		}
		registration, err := client.GetRGB11Registration("alice", " USD ", contractID)
		if err != nil || registration == nil {
			t.Fatalf("HTTP read ordinal %d: registration=%+v err=%v", ordinal, registration, err)
		}
		if registration.ContractID != contractID || registration.AssetName != expected.String() ||
			registration.AssetType != assetType || registration.ProviderDID != "alice" ||
			registration.BaseTicker != "usd" || registration.Ordinal != ordinal {
			t.Fatalf("wrong registration at ordinal %d: %+v", ordinal, registration)
		}
		byName, err := source.indexer.LookupRGB11AssetName(registration.AssetName)
		if err != nil || byName == nil || byName.ContractID != contractID {
			t.Fatalf("reverse lookup: registration=%+v err=%v", byName, err)
		}
	}
	if _, err := client.GetRGB11Registration("alice", "USD", fmt.Sprintf("%064x", 999)); !errors.Is(err, dkvsindexer.ErrRecordNotFound) {
		t.Fatalf("missing ContractID must not resolve to another asset: %v", err)
	}
	if reads.Load() != 13 {
		t.Fatalf("expected 13 real HTTP reads, got %d", reads.Load())
	}

	// A fresh DKVS node must recover names before any SatoshiNet block replay.
	snapshot, err := source.indexer.GetPathSnapshot("/rgb11/alice/usd")
	if err != nil {
		t.Fatal(err)
	}
	target, _ := newRGB11RegistryE2ESource(t)
	if applied, err := target.indexer.ApplyPathSnapshot(snapshot); err != nil || applied != 12 {
		t.Fatalf("initial DKVS recovery: applied=%d err=%v", applied, err)
	}
	recoveredClient, _ := newRGB11RegistryE2EHTTPClient(t, target, nil)
	registration, err := recoveredClient.GetRGB11Registration("alice", "USD", fmt.Sprintf("%064x", 412))
	if err != nil || registration == nil || registration.AssetName != "rgb11:n:usd_12@alice" {
		t.Fatalf("SDK read after DKVS recovery: registration=%+v err=%v", registration, err)
	}
	thirteenth := makeRecord("alice", "USD", "f", 13, fmt.Sprintf("%064x", 413))
	if _, err := source.indexer.PutInternalRGB11Registry(thirteenth); err != nil {
		t.Fatal(err)
	}
	appended, err := source.indexer.GetPathSnapshot("/rgb11/alice/usd")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.indexer.ApplyPathSnapshot(appended); err != nil {
		t.Fatalf("append-only snapshot: %v", err)
	}
	if _, err := target.indexer.ApplyPathSnapshot(snapshot); err == nil {
		t.Fatal("stale snapshot removed an existing ordinal")
	}
	for _, item := range []struct {
		id   int
		name string
	}{{401, "rgb11:f:usd@alice"}, {413, "rgb11:f:usd_13@alice"}} {
		registration, err := recoveredClient.GetRGB11Registration("alice", "USD", fmt.Sprintf("%064x", item.id))
		if err != nil || registration == nil || registration.AssetName != item.name {
			t.Fatalf("immutable mapping after append/stale snapshot: registration=%+v err=%v", registration, err)
		}
	}
}

func rgb11RegistryHTTPValidationE2E(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	contractID := fmt.Sprintf("%064x", 901)
	authorized := makeRecord("alice", "USD", "f", 1, contractID)
	if _, err := source.indexer.PutInternalRGB11Registry(authorized); err != nil {
		t.Fatal(err)
	}
	cleanClient, _ := newRGB11RegistryE2EHTTPClient(t, source, nil)
	if registration, err := cleanClient.GetRGB11Registration("alice", "USD", contractID); err != nil || registration == nil {
		t.Fatalf("valid-response control failed: registration=%+v err=%v", registration, err)
	}
	// Public independent test wallet; signing uses the SDK API, not a helper
	// defined only in the dependency package's own _test.go files.
	attacker := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	if attacker == nil || attacker.GetPubKey() == nil {
		t.Fatal("create untrusted test wallet")
	}
	untrusted, err := NewDKVSSignedRecord(attacker, authorized.Key, authorized.Value,
		dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := dkvsindexer.VerifySignature(untrusted); err != nil {
		t.Fatalf("untrusted-signer fixture must have a valid signature: %v", err)
	}
	if _, err := source.indexer.PutInternalRGB11Registry(untrusted); !errors.Is(err, dkvsindexer.ErrPermissionDenied) {
		t.Fatalf("server must reject the untrusted signer: %v", err)
	}
	wrongProvider := makeRecord("company", "USD", "f", 1, contractID)
	wrongTicker := makeRecord("alice", "EUR", "f", 1, contractID)
	for _, tc := range []struct {
		name   string
		mutate func(*dkvsindexer.PrefixReadResult)
	}{
		{"unsigned_record", func(result *dkvsindexer.PrefixReadResult) {
			result.Records = []*swire.DKVSRecord{{Key: authorized.Key, Value: append([]byte(nil), authorized.Value...)}}
		}},
		{"untrusted_signer", func(result *dkvsindexer.PrefixReadResult) {
			result.Records = []*swire.DKVSRecord{untrusted}
		}},
		{"tampered_type", func(result *dkvsindexer.PrefixReadResult) {
			result.Records[0].Value[0] = 'n'
		}},
		{"wrong_provider", func(result *dkvsindexer.PrefixReadResult) {
			result.Records = []*swire.DKVSRecord{wrongProvider}
		}},
		{"wrong_ticker", func(result *dkvsindexer.PrefixReadResult) {
			result.Records = []*swire.DKVSRecord{wrongTicker}
		}},
		{"truncated_value", func(result *dkvsindexer.PrefixReadResult) {
			result.Records[0].Value = []byte{'f'}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, reads := newRGB11RegistryE2EHTTPClient(t, source, tc.mutate)
			registration, err := client.GetRGB11Registration("alice", "USD", contractID)
			if reads.Load() != 1 {
				t.Fatalf("rejection must exercise the HTTP response: reads=%d err=%v", reads.Load(), err)
			}
			// Never weaken this assertion to accept the current vulnerability.
			if err == nil || registration != nil {
				t.Fatalf("untrusted registry response accepted as canonical: registration=%+v err=%v", registration, err)
			}
		})
	}
	stored, err := source.indexer.Get(authorized.Key)
	if err != nil || stored == nil || dkvsindexer.RecordHash(stored) != dkvsindexer.RecordHash(authorized) {
		t.Fatalf("response injection mutated server state: record=%+v err=%v", stored, err)
	}
}
