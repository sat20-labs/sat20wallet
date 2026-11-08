package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Isolated tests use their own signing authority; no real network signing key
// is embedded in the repository. Keep the override on a single test manager.
func useRGB11TestCoreAuthority(t *testing.T, manager *Manager) {
	t.Helper()
	core := NewInternalWalletWithMnemonic(rgb11RegistryTestMnemonic, "", &chaincfg.TestNet4Params)
	manager.rgb11RegistryVerifier = dkvsindexer.StaticSystemVerifier{Keys: [][]byte{core.GetPubKey().SerializeCompressed()}}
}

func installRGB11PublicContractFixture(t *testing.T, source *Manager,
	remote *rgb11MemoryDKVSHTTP, contractID string) *swire.DKVSRecord {
	t.Helper()
	useRGB11TestCoreAuthority(t, source)
	exported, err := source.ExportPublicRGB11Contract(contractID)
	require.NoError(t, err)
	public, err := base64.StdEncoding.Strict().DecodeString(exported.ContractConsignmentBase64)
	require.NoError(t, err)
	value, registration, err := rgb11wallet.NewRGB11ContractValue(public)
	require.NoError(t, err)
	encoded, err := rgb11wallet.EncodeRGB11RegistryValue(value)
	require.NoError(t, err)
	id, err := consensus.ParseContractID(contractID)
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(id[:]), registration.ContractID)
	key, err := rgb11wallet.RGB11RegistryKey(registration.ContractID)
	require.NoError(t, err)
	core := NewInternalWalletWithMnemonic(rgb11RegistryTestMnemonic, "", &chaincfg.TestNet4Params)
	record, err := NewDKVSSignedRecord(core, key, encoded, dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 1})
	require.NoError(t, err)
	remote.mu.Lock()
	remote.records[key] = record
	remote.mu.Unlock()
	return record
}

// This is a client-side preflight, not a substitute for the Core route. Its
// registry-not-found result must be distinct from absent or forged local
// ContractID metadata before deployment reserves a fee.
func TestRGB11PublicRegistrationPreflightIdentityAndMissingRecord(t *testing.T) {
	remote, makeRecord := newRGB11RegistryE2ESource(t)
	record := makeRecord("unowned-submitter", "USD", "f", 0, 1501)
	value, err := rgb11wallet.DecodeRGB11RegistryValue(record.Value)
	require.NoError(t, err)
	contract, err := consignment.DecodeFile(value.ContractContent)
	require.NoError(t, err)
	asset, err := rgb11wallet.NewContractAssetKey(contract.ContractID, indexer.ASSET_TYPE_FT)
	require.NoError(t, err)
	ext := rgb11wallet.TickerExt{ContractID: contract.ContractID, SchemaID: contract.SchemaID, Ticker: "USD"}
	metadata, err := json.Marshal(ext)
	require.NoError(t, err)
	manager := &Manager{
		cfg: &common.Config{IndexerL2: &common.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}},
		db:  newMemoryKVDB(), http: remote, tickerInfoMap: map[string]*indexer.TickerInfo{
			asset.String(): {AssetName: asset, Content: metadata},
		},
	}
	useRGB11TestCoreAuthority(t, manager)
	require.ErrorIs(t, manager.requireRGB11PublicRegistration(&asset), ErrRGB11PublicRegistrationRequired)
	wrong := asset
	wrong.Ticker = "another-asset"
	require.ErrorIs(t, manager.requireRGB11PublicRegistration(&wrong), ErrRGB11PublicIdentityMismatch)
	manager.cfg.IndexerL2.Host = ""
	require.ErrorIs(t, manager.requireRGB11PublicRegistration(&asset), ErrRGB11PublicDataUnavailable)
	// The same public object is readable and authenticated with a configured
	// authority through the production client entry, independent of DID.
	client, _ := newRGB11RegistryE2EHTTPClient(t, remote, nil)
	_, err = remote.indexer.PutInternalContract(record)
	require.NoError(t, err)
	id := rgb11RegistryRecordID(record)
	registration, err := client.GetRGB11Registration(id)
	require.NoError(t, err)
	require.Equal(t, id, registration.ContractID)
	manager.cfg.IndexerL2.Host = "dkvs.test"
	readback, err := manager.VerifyRGB11PublicContract(contract.ContractID,
		base64.StdEncoding.EncodeToString(value.ContractContent))
	require.NoError(t, err, "deployment readback must work before L2 ticker metadata is populated")
	require.Equal(t, id, readback.ContractID)
	delete(manager.tickerInfoMap, asset.String())
	require.NoError(t, manager.requireRGB11PublicRegistration(&asset),
		"first transcend deployment must not require L2 ticker metadata")
	_, err = manager.VerifyRGB11PublicContract(contract.ContractID,
		base64.StdEncoding.EncodeToString([]byte("not a public contract")))
	require.ErrorIs(t, err, ErrRGB11PublicIdentityMismatch)
}

func TestRGB11BackupPreflightRejectsOmittedPublicContractReference(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	chain := newCoreE2EChain()
	rpc := &coreE2EL1Indexer{chain: chain}
	wallet := NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", &chaincfg.TestNet4Params)
	chain.fund(t, wallet.GetAddress(), 6)
	source := newRGB11FlowManager(t, wallet, rpc, chain, 1510, accountGroupUnprepared)
	prepareRGBEndpoint(t, source, newRGB11MemoryDKVSHTTP())
	issued, err := source.rgbManager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "REF", Name: "Public reference preflight fixture", Amounts: []uint64{100},
	})
	require.NoError(t, err)
	exported, err := source.ExportPublicRGB11Contract(issued.ContractID)
	require.NoError(t, err, "wallet must export public contract data using the RGB ContractID spelling")
	require.NotEmpty(t, exported.ContractConsignmentBase64)
	catalog, err := source.accountManagedDataCatalog()
	require.NoError(t, err)
	payloads, err := (&rgb11AccountManagedDataProvider{owner: source}).Export(catalog)
	require.NoError(t, err)
	require.NotEmpty(t, payloads)
	packageValue, err := rgb11wallet.DecodeRecoveryPackage(payloads[len(payloads)-1].Payload)
	require.NoError(t, err)
	complete := account.ManagedDataBundle{Items: []account.ManagedDataItem{{
		Provider: rgb11AccountManagedProviderID, Scope: payloads[len(payloads)-1].Scope,
		Payload: payloads[len(payloads)-1].Payload,
	}}}
	// Restore this case's unavailable-indexer precondition after guarded material preparation.
	source.cfg.IndexerL2.Host = ""
	require.ErrorIs(t, source.verifyRGB11PublicRecoveryReferences(complete),
		ErrRGB11PublicDataUnavailable,
		"a private bundle with intact references is not independently restorable without readable public material")
	broken := *packageValue
	broken.ProjectionRecords = make([]rgb11wallet.SnapshotRecord, 0, len(packageValue.ProjectionRecords))
	removed := false
	for _, record := range packageValue.ProjectionRecords {
		if strings.HasPrefix(record.Key, "contract-object-") {
			removed = true
			continue
		}
		broken.ProjectionRecords = append(broken.ProjectionRecords, record)
	}
	require.True(t, removed, "fixture must contain a full public-contract reference")
	encoded, err := rgb11wallet.EncodeRecoveryPackage(&broken)
	require.NoError(t, err, "the public preflight must catch a package that is otherwise well formed")
	bundle := account.ManagedDataBundle{Items: []account.ManagedDataItem{{
		Provider: rgb11AccountManagedProviderID, Scope: payloads[len(payloads)-1].Scope, Payload: encoded,
	}}}
	require.ErrorIs(t, source.verifyRGB11PublicRecoveryReferences(bundle), ErrRGB11PublicIdentityMismatch)
}

// The test Core validates the raw public contract and signs as its own
// authority. It never accepts client-supplied authority records or private
// consigment fields. The client under test is the real NodeClient raw route.
type rgb11RegistrationHTTP struct {
	*rgb11MemoryDKVSHTTP
	calls int
	fail  bool
}

func (h *rgb11RegistrationHTTP) SendPostRequest(u *URL, body []byte) ([]byte, error) {
	if !strings.HasSuffix(u.Path, "/v3/rgb11/contracts/register") {
		return h.rgb11MemoryDKVSHTTP.SendPostRequest(u, body)
	}
	h.calls++
	if h.fail {
		return nil, errors.New("controlled Core registration unavailable")
	}
	var request struct {
		Content string `json:"contract_consignment_base64"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, err
	}
	content, err := base64.StdEncoding.Strict().DecodeString(request.Content)
	if err != nil {
		return nil, err
	}
	canonical, err := rgb11wallet.PublicRGB11Contract(content)
	if err != nil || !bytes.Equal(canonical, content) {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	value, registration, err := rgb11wallet.NewRGB11ContractValue(content)
	if err != nil {
		return nil, err
	}
	encoded, err := rgb11wallet.EncodeRGB11RegistryValue(value)
	if err != nil {
		return nil, err
	}
	key, err := rgb11wallet.RGB11RegistryKey(registration.ContractID)
	if err != nil {
		return nil, err
	}
	authority := NewInternalWalletWithMnemonic(rgb11RegistryTestMnemonic, "", &chaincfg.TestNet4Params)
	record, err := NewDKVSSignedRecord(authority, key, encoded, dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 1})
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	if existing := h.records[key]; existing != nil && !bytes.Equal(existing.Value, encoded) {
		h.mu.Unlock()
		return nil, ErrRGB11PublicIdentityMismatch
	}
	h.records[key] = record
	h.mu.Unlock()
	return json.Marshal(struct {
		Code         int                            `json:"code"`
		Registration *rgb11wallet.RGB11Registration `json:"registration"`
	}{Registration: registration})
}

type rgb11RegistrationNode struct {
	NodeRPCClient
	raw *NodeClient
}

func (n *rgb11RegistrationNode) RegisterPublicRGB11(content []byte) (*rgb11wallet.RGB11Registration, error) {
	return n.raw.RegisterPublicRGB11(content)
}

func TestRGBPublicBackupRegisters(t *testing.T) {
	manager, receiver, asset, _, _ := newRGB11GenericSendFixture(t)
	manager.rgbManager.scopeStates.stopReconciliations()
	receiver.rgbManager.scopeStates.stopReconciliations()
	remote, ok := manager.http.(*rgb11MemoryDKVSHTTP)
	require.True(t, ok)
	useRGB11TestCoreAuthority(t, manager)
	transport := &rgb11RegistrationHTTP{rgb11MemoryDKVSHTTP: remote, fail: true}
	original := manager.serverNode
	manager.serverNode = NewNode(&rgb11RegistrationNode{NodeRPCClient: original.RPCClient(), raw: NewNodeClient("http", "core.test", "testnet", transport)}, original.Host, SERVER_NODE, original.NodeId, original.Pubkey)
	key, err := rgb11wallet.RGB11RegistryKey(asset.AssetName.Ticker)
	require.NoError(t, err)
	remote.mu.Lock()
	delete(remote.records, key)
	remote.mu.Unlock()
	issued, err := manager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "AUTO", Name: "Local issue before public backup", Amounts: []uint64{77},
	})
	require.NoError(t, err, "unavailable public registration must not block local issuance")
	require.NotNil(t, issued)
	require.Zero(t, transport.calls, "issuance itself must not wait for Core registration")
	id := manager.GetCurrentWalletId()
	require.NoError(t, manager.UpdateWalletName(id, "local remains usable"))
	require.Error(t, manager.SyncAccountManagementState(context.Background()))
	require.True(t, manager.GetAccountManagementStatus().ManagedDataDirty)
	require.Equal(t, "local remains usable", manager.GetWalletCatalog()[0].Name)
	require.Equal(t, 1, transport.calls)
	transport.fail = false
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	require.False(t, manager.GetAccountManagementStatus().ManagedDataDirty)
	require.Equal(t, 3, transport.calls, "both missing public contracts must be registered independently")
	require.NoError(t, manager.verifyRGB11PublicContract(asset.AssetName.Ticker, mustRGBPublicRaw(t, manager, asset.ContractID)))
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	require.Equal(t, 3, transport.calls, "verified registered contracts must not be resubmitted")
}
func mustRGBPublicRaw(t *testing.T, manager *Manager, contractID string) []byte {
	t.Helper()
	public, err := manager.ExportPublicRGB11Contract(contractID)
	require.NoError(t, err)
	raw, err := base64.StdEncoding.Strict().DecodeString(public.ContractConsignmentBase64)
	require.NoError(t, err)
	return raw
}
