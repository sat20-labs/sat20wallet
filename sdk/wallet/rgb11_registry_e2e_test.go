package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const rgb11RegistryTestMnemonic = "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"

type rgb11RegistryE2ERecordFactory func(provider, ticker, kind string, ordinal uint64, seed int) *swire.DKVSRecord

func newRGB11RegistryE2ESource(t *testing.T) (*satoshinetDKVSTestTransport, rgb11RegistryE2ERecordFactory) {
	t.Helper()
	core := NewInternalWalletWithMnemonic(rgb11RegistryTestMnemonic, "", &chaincfg.TestNet4Params)
	require.NotNil(t, core)
	transport := newSatoshiNetDKVSTestTransport()
	transport.height = 100
	transport.indexer.SetSystemVerifier(dkvsindexer.StaticSystemVerifier{Keys: [][]byte{core.GetPubKey().SerializeCompressed()}})
	makeRecord := func(provider, ticker, kind string, ordinal uint64, seed int) *swire.DKVSRecord {
		t.Helper()
		_ = provider // technical registration does not bind a DID
		_ = ordinal  // ticker ordinal belongs to a separate naming registry
		schema := schemas.NIA
		if kind == indexer.ASSET_TYPE_NFT {
			schema = schemas.UDA
		}
		seal, err := seals.NewGraphBlindSeal(bytes.Repeat([]byte{0x12}, 32), 0, uint64(seed))
		require.NoError(t, err)
		issued, err := issuance.Issue(issuance.Spec{Kind: schema, Network: issuance.BitcoinTestnet4, Ticker: ticker, Name: "SDK registry fixture", Timestamp: 1700000000 + int64(seed), Allocations: []issuance.Allocation{{Seal: seal, Amount: 1}}})
		require.NoError(t, err)
		raw, err := consignment.EncodeFile(issued.Container)
		require.NoError(t, err)
		id, err := consensus.ParseContractID(issued.ContractID)
		require.NoError(t, err)
		path, err := rgb11wallet.RGB11RegistryKey(hex.EncodeToString(id[:]))
		require.NoError(t, err)
		value, err := rgb11wallet.EncodeRGB11RegistryValue(rgb11wallet.RGB11ContractValue{ContractContent: raw})
		require.NoError(t, err)
		record, err := NewDKVSSignedRecord(core, path, value, dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 100})
		require.NoError(t, err)
		return record
	}
	return transport, makeRecord
}
func rgb11RegistryRecordID(record *swire.DKVSRecord) string {
	return strings.TrimPrefix(record.Key, rgb11wallet.RGB11RegistryPath+"/")
}

type rgb11RegistryE2EClient struct {
	*SatsNetDKVSClient
	verifier dkvsindexer.SystemVerifier
}

func (c *rgb11RegistryE2EClient) GetRGB11Registration(id string) (*rgb11wallet.RGB11Registration, error) {
	return c.GetRGB11RegistrationWithVerifier(id, c.verifier)
}

// Serve the current single-record application HTTP API with a genuine signed
// DKVS record and its ETag. Only response transport is injectable.
func newRGB11RegistryE2EHTTPClient(t *testing.T, transport *satoshinetDKVSTestTransport, mutate func(*swire.DKVSRecord)) (*rgb11RegistryE2EClient, *atomic.Int64) {
	t.Helper()
	core := NewInternalWalletWithMnemonic(rgb11RegistryTestMnemonic, "", &chaincfg.TestNet4Params)
	verifier := dkvsindexer.StaticSystemVerifier{Keys: [][]byte{core.GetPubKey().SerializeCompressed()}}
	reads := new(atomic.Int64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/testnet/v3/dkvs/record" {
			http.NotFound(w, r)
			return
		}
		reads.Add(1)
		record, err := transport.indexer.Get(r.URL.Query().Get("key"))
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": -1, "msg": err.Error(), "error_code": dkvsindexer.ErrorCodeOf(err)})
			return
		}
		data, _ := json.Marshal(record)
		var copied swire.DKVSRecord
		require.NoError(t, json.Unmarshal(data, &copied))
		if mutate != nil {
			mutate(&copied)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": &copied, "etag": dkvsindexer.RecordHash(&copied).String()})
	}))
	t.Cleanup(server.Close)
	httpClient := server.Client()
	httpClient.Timeout = 10 * time.Second
	client := NewSatsNetDKVSClient("http", strings.TrimPrefix(server.URL, "http://"), "testnet", &NetClient{Client: httpClient})
	return &rgb11RegistryE2EClient{SatsNetDKVSClient: client, verifier: verifier}, reads
}

func TestRGB11RegistrySDKDKVSE2E(t *testing.T) {
	t.Run("HTTPRoundTripAndSnapshotRecovery", rgb11RegistryHTTPRoundTripE2E)
	t.Run("SDKAssetTypes", rgb11RegistrySDKAssetTypesE2E)
	t.Run("RejectUntrustedHTTPResponses", rgb11RegistryHTTPValidationE2E)
	t.Run("AuthorityPolicyFailsClosed", rgb11RegistryAuthorityPolicyE2E)
}

func rgb11RegistryHTTPRoundTripE2E(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	client, reads := newRGB11RegistryE2EHTTPClient(t, source, nil)
	// The public contract path is never an account-managed subscription. A
	// coordinated client must still read it directly and verify the signature.
	client.SatsNetDKVSClient.manager = &dkvsManager{}
	records := make([]*swire.DKVSRecord, 0, 12)
	for n := 1; n <= 12; n++ {
		record := makeRecord("", "USD", "f", 0, 400+n)
		changed, err := source.indexer.PutInternalContract(record)
		require.NoError(t, err)
		require.True(t, changed)
		records = append(records, record)
	}
	changed, err := source.indexer.PutInternalContract(records[11])
	require.NoError(t, err)
	require.False(t, changed)
	for _, record := range records {
		id := rgb11RegistryRecordID(record)
		got, err := client.GetRGB11Registration(id)
		require.NoError(t, err)
		require.Equal(t, id, got.ContractID)
		require.Equal(t, "usd", got.BaseTicker)
		require.Equal(t, "f", got.AssetType)
		require.NotEmpty(t, got.SchemaID)
	}
	_, err = client.GetRGB11Registration(strings.Repeat("f", 64))
	require.ErrorIs(t, err, dkvsindexer.ErrRecordNotFound)
	require.EqualValues(t, 13, reads.Load())
	snapshot, err := source.indexer.GetPathSnapshot(rgb11wallet.RGB11RegistryPath)
	require.NoError(t, err)
	target, _ := newRGB11RegistryE2ESource(t)
	applied, err := target.indexer.ApplyPathSnapshot(snapshot)
	require.NoError(t, err)
	require.Equal(t, 12, applied)
	recovered, _ := newRGB11RegistryE2EHTTPClient(t, target, nil)
	got, err := recovered.GetRGB11Registration(rgb11RegistryRecordID(records[11]))
	require.NoError(t, err)
	require.Equal(t, rgb11RegistryRecordID(records[11]), got.ContractID)
	next := makeRecord("", "USD", "f", 0, 413)
	_, err = source.indexer.PutInternalContract(next)
	require.NoError(t, err)
	appended, err := source.indexer.GetPathSnapshot(rgb11wallet.RGB11RegistryPath)
	require.NoError(t, err)
	baseline, err := target.indexer.NetworkSyncBaseline(rgb11wallet.RGB11RegistryPath)
	require.NoError(t, err)
	_, err = target.indexer.ApplyPathSnapshotFrom(appended, baseline)
	require.NoError(t, err)
	baseline, err = target.indexer.NetworkSyncBaseline(rgb11wallet.RGB11RegistryPath)
	require.NoError(t, err)
	_, err = target.indexer.ApplyPathSnapshotFrom(snapshot, baseline)
	require.ErrorIs(t, err, dkvsindexer.ErrWriteConflict)
	got, err = recovered.GetRGB11Registration(rgb11RegistryRecordID(next))
	require.NoError(t, err)
	require.Equal(t, rgb11RegistryRecordID(next), got.ContractID)
}

func rgb11RegistrySDKAssetTypesE2E(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	client, _ := newRGB11RegistryE2EHTTPClient(t, source, nil)
	for i, kind := range []string{indexer.ASSET_TYPE_FT, indexer.ASSET_TYPE_NFT} {
		record := makeRecord("", "USD", kind, 0, 800+i)
		_, err := source.indexer.PutInternalContract(record)
		require.NoError(t, err)
		got, err := client.GetRGB11Registration(rgb11RegistryRecordID(record))
		require.NoError(t, err)
		require.Equal(t, kind, got.AssetType)
	}
}

func rgb11RegistryHTTPValidationE2E(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	original := makeRecord("", "USD", "f", 0, 901)
	_, err := source.indexer.PutInternalContract(original)
	require.NoError(t, err)
	core := NewInternalWalletWithMnemonic(rgb11RegistryTestMnemonic, "", &chaincfg.TestNet4Params)
	attacker := NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
	for _, attack := range []string{"unsigned", "untrusted_signer", "tampered_value", "truncated", "old_codec", "wrong_contract_key", "mutable_sequence", "TTL"} {
		t.Run(attack, func(t *testing.T) {
			client, reads := newRGB11RegistryE2EHTTPClient(t, source, func(record *swire.DKVSRecord) {
				switch attack {
				case "unsigned":
					record.Signature = nil
				case "untrusted_signer":
					record.PubKey = attacker.GetPubKey().SerializeCompressed()
					require.NoError(t, SignDKVSRecord(attacker, record))
				case "tampered_value":
					record.Value[len(record.Value)-1] ^= 1
				case "truncated":
					record.Value = record.Value[:12]
					require.NoError(t, SignDKVSRecord(core, record))
				case "old_codec":
					record.Value[0] = 0
					require.NoError(t, SignDKVSRecord(core, record))
				case "wrong_contract_key":
					record.Key = rgb11wallet.RGB11RegistryPath + "/" + strings.Repeat("f", 64)
					require.NoError(t, SignDKVSRecord(core, record))
				case "mutable_sequence":
					record.Seq = 2
					require.NoError(t, SignDKVSRecord(core, record))
				case "TTL":
					record.TTL = 10
					require.NoError(t, SignDKVSRecord(core, record))
				}
			})
			got, err := client.GetRGB11Registration(rgb11RegistryRecordID(original))
			require.Error(t, err)
			require.Nil(t, got)
			require.EqualValues(t, 1, reads.Load())
		})
	}
	clean, _ := newRGB11RegistryE2EHTTPClient(t, source, nil)
	got, err := clean.GetRGB11Registration(rgb11RegistryRecordID(original))
	require.NoError(t, err)
	require.Equal(t, rgb11RegistryRecordID(original), got.ContractID)
	stored, err := source.indexer.Get(original.Key)
	require.NoError(t, err)
	require.Equal(t, dkvsindexer.RecordHash(original), dkvsindexer.RecordHash(stored))
}

func rgb11RegistryAuthorityPolicyE2E(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	record := makeRecord("", "USD", "f", 0, 950)
	_, err := source.indexer.PutInternalContract(record)
	require.NoError(t, err)
	client, reads := newRGB11RegistryE2EHTTPClient(t, source, nil)
	id := rgb11RegistryRecordID(record)
	got, err := client.GetRGB11Registration(id)
	require.NoError(t, err)
	require.NotNil(t, got)
	got, err = client.SatsNetDKVSClient.GetRGB11Registration(id)
	require.ErrorIs(t, err, dkvsindexer.ErrPermissionDenied)
	require.Nil(t, got)
	require.EqualValues(t, 2, reads.Load())
	got, err = client.GetRGB11RegistrationWithVerifier(id, nil)
	require.ErrorIs(t, err, dkvsindexer.ErrPermissionDenied)
	require.Nil(t, got)
	require.EqualValues(t, 2, reads.Load())
}
