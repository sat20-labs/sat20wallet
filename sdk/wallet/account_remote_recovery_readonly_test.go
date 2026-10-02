package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/sirupsen/logrus"
	"github.com/tyler-smith/go-bip39"
)

// This diagnostic has no Manager, local database, browser, import or storage
// path. Its transport accepts only GETs for the three exact public record keys
// and chain height. Remote ciphertext/plaintext and root material stay in RAM.
type recoveryReadOnlyTransport struct {
	keys     map[string]bool
	requests int
}

// An in-memory read adapter over the already downloaded remote record slice,
// not a database or an import. All unsupported methods have no implementation.
// ListProofs uses only BatchRead, so the production strict codec can decode
// WitnessTxID without duplicating codecs or confusing a blind seal's carrier
// outpoint with its spending witness transaction.
type recoveryRecordReadView struct {
	indexer.KVDB
	records []rgb11wallet.SnapshotRecord
}

func (v *recoveryRecordReadView) BatchRead(prefix []byte, _ bool, fn func([]byte, []byte) error) error {
	for _, record := range v.records {
		key := []byte("rgb11-diagnostic-" + record.Key)
		if bytes.HasPrefix(key, prefix) {
			if err := fn(key, record.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *recoveryReadOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "apiprd.ordx.market" {
		return nil, errors.New("read-only diagnostic transport rejected request")
	}
	path := req.URL.Path
	allowed := path == "/satsnet/testnet/btc/block/bestblockheight"
	if path == "/satsnet/testnet/v3/dkvs/record" || path == "/satsnet/testnet/v3/dkvs/key-state" {
		allowed = r.keys[req.URL.Query().Get("key")]
	}
	if !allowed {
		return nil, errors.New("read-only diagnostic path rejected")
	}
	r.requests++
	return http.DefaultTransport.RoundTrip(req)
}

func TestDiagnosticRemoteAccountRecoveryReadOnly(t *testing.T) {
	if os.Getenv("SAT20_REMOTE_RECOVERY_READONLY") != "1" {
		t.Skip("opt-in remote read-only diagnostic")
	}
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	oldOutput, oldHooks := Log.Out, Log.Hooks
	Log.SetOutput(io.Discard)
	Log.ReplaceHooks(make(logrus.LevelHooks))
	defer func() { Log.SetOutput(oldOutput); Log.ReplaceHooks(oldHooks) }()
	// Read the already-authorized test fixture in memory; never print, export,
	// place in an environment variable, or create another seed-bearing file.
	file, err := parser.ParseFile(token.NewFileSet(), "../../../transcend/stp/manager_testnet_test.go", nil, 0)
	if err != nil {
		t.Fatal("fixture_parse_failed")
	}
	var root *InternalWallet
	ast.Inspect(file, func(node ast.Node) bool {
		if root != nil {
			return false
		}
		lit, ok := node.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil || len(strings.Fields(value)) != 12 || !bip39.IsMnemonicValid(value) {
			return true
		}
		candidate := NewInternalWalletWithMnemonic(value, "", GetChainParam())
		if candidate != nil && PublicKeyToP2TRAddress_SatsNet(candidate.GetPubKey()) == "tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g" {
			root = candidate
		}
		value = ""
		return true
	})
	file = nil
	if root == nil {
		t.Fatal("root_identity_match=false")
	}
	t.Log("root_identity_match=true")
	accountID, err := dkvsAccountID(root)
	if err != nil {
		t.Fatal("root_account_derivation_failed")
	}
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		t.Fatal("wrapper_key_derivation_failed")
	}
	stateKey := "/personal/" + accountID + "/account/state"
	dataKey := "/blob/" + accountID + "/account-managed-data"
	transport := &recoveryReadOnlyTransport{keys: map[string]bool{wrapperKey: true, stateKey: true, dataKey: true}}
	httpClient := &NetClient{Client: &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	client := NewSatsNetDKVSClient("https", "apiprd.ordx.market", "satsnet/testnet", httpClient)
	if client.manager != nil {
		t.Fatal("unexpected_local_manager")
	}
	height := NewIndexerClient("https", "apiprd.ordx.market", "satsnet/testnet", httpClient).GetBestHeight()
	if height <= 0 {
		t.Fatal("remote_height_unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	read := func(label, key string) *swire.DKVSRecord {
		record, err := client.GetRecordDirectContext(ctx, key)
		if err != nil {
			t.Fatalf("%s_record_read_failed", label)
		}
		if err := dkvsindexer.VerifyRecordForClient(record, dkvsindexer.RecordVerificationOptions{ExpectedKey: key, Height: uint64(height)}); err != nil {
			t.Fatalf("%s_record_auth_or_expiry_invalid", label)
		}
		state, err := client.GetKeyState(key)
		if err != nil || state.Status != dkvsindexer.KeyStateActive || state.Seq != record.Seq || state.ETag != dkvsindexer.RecordHash(record).String() {
			t.Fatalf("%s_key_state_match=false", label)
		}
		t.Logf("%s_auth_and_key_state_match=true seq=%d ttl_zero=%t", label, record.Seq, record.TTL == 0)
		return record
	}
	wrapperRecord := read("wrapper", wrapperKey)
	payload, err := openAccountRootWrapper(root, _chain, accountID, wrapperRecord.Value)
	if err != nil {
		t.Fatal("wrapper_open_same_root=false")
	}
	defer zeroBytes(payload.Secret)
	t.Logf("wrapper_open_same_root=true recovery_configured=%t paid=%t", payload.RecoveryConfigured, payload.StorageMode == AccountStoragePaid)
	stateRecord := read("state", stateKey)
	state, err := account.OpenManagedState(payload.Secret, accountID, stateRecord.Value)
	if err != nil || state.RootFingerprint != walletFingerprint(root) {
		t.Fatal("state_open_same_root=false")
	}
	defer func() {
		for i := range state.Wallets {
			state.Wallets[i].Mnemonic = ""
		}
	}()
	blobRecord := read("blob", dataKey)
	managed, err := openAccountManagedDataValue(payload.Secret, accountID, cloneDKVSValue(blobRecord), state)
	if err != nil {
		t.Fatal("state_blob_reference_match=false")
	}
	defer func() {
		for i := range managed.Bundle.Items {
			zeroBytes(managed.Bundle.Items[i].Payload)
		}
	}()
	t.Logf("state_open_same_root=true state_blob_reference_match=true state_revision=%d data_revision=%d wallet_count=%d provider_item_count=%d", state.Revision, state.DataRevision, len(state.Wallets), len(managed.Bundle.Items))
	rootScope := (AccountManagedDataScope{WalletFingerprint: walletFingerprint(root), AccountIndex: 0, Network: _chain}).ID()
	packages, proofs, outputs, lunarRefs := 0, 0, 0, 0
	returnProof, outboundChangeProof := false, false
	remoteBalanceMatches84 := false
	for _, item := range managed.Bundle.Items {
		if item.Provider != rgb11AccountManagedProviderID || item.Scope != rootScope {
			continue
		}
		pkg, err := rgb11wallet.DecodeRecoveryPackage(item.Payload)
		if err != nil || rgb11wallet.ValidateRecoveryPackage(pkg) != nil {
			t.Fatal("root_rgb_recovery_closure_valid=false")
		}
		if pkg.AccountIndex != 0 || pkg.WalletID != rgb11WalletStorageNamespace+hex.EncodeToString(root.GetPubKey().SerializeCompressed()) {
			t.Fatal("root_rgb_scope_match=false")
		}
		packages++
		projection := rgb11wallet.NewProjectionStore(&recoveryRecordReadView{records: pkg.ProjectionRecords}, nil)
		if err := projection.SetScope("diagnostic"); err != nil {
			t.Fatal("diagnostic_decode_scope_failed")
		}
		decodedProofs, err := projection.ListProofs()
		if err != nil {
			t.Fatal("proof_decode_failed")
		}
		refs, err := rgb11wallet.TickerRefsFromProjectionSnapshot(pkg.ProjectionRecords)
		if err != nil {
			t.Fatal("rgb_contract_refs_valid=false")
		}
		for _, ref := range refs {
			if !strings.Contains(strings.ToLower(ref.AssetName), ":luna927@") {
				continue
			}
			_, _, err := rgb11wallet.ContractObjectForTickerRef(pkg.ProjectionRecords, ref)
			if err != nil {
				t.Fatal("luna_contract_object_hash_match=false")
			}
			lunarRefs++
			for _, proof := range decodedProofs {
				if proof.AssetName.String() != ref.AssetName {
					continue
				}
				balance, balanceErr := projection.Balance(proof.AssetName)
				if balanceErr != nil {
					t.Fatal("remote_luna_balance_decode_failed")
				}
				remoteBalanceMatches84 = balance != nil && balance.Cmp(indexer.NewDecimal(84, 0)) == 0
				returnProof = returnProof || proof.WitnessTxID == "541a976c2cf00ccb5801f9d3e16ad14617eb16d6e669007b01b60b8a579c82d4"
				outboundChangeProof = outboundChangeProof || proof.WitnessTxID == "27c415e22bd69c7f6b1c5311f08ac210ffa21be8f30f09ef49ea5ccd7c2e5cd8"
			}
		}
		for _, record := range pkg.ProjectionRecords {
			if strings.HasPrefix(record.Key, "proof-") {
				proofs++
			}
			if strings.HasPrefix(record.Key, "output-") {
				outputs++
			}
		}
	}
	// A second exact metadata read excludes a concurrently changed remote pair.
	for _, record := range []*swire.DKVSRecord{wrapperRecord, stateRecord, blobRecord} {
		keyState, err := client.GetKeyState(record.Key)
		if err != nil || keyState.ETag != dkvsindexer.RecordHash(record).String() {
			t.Fatal("remote_records_stable=false")
		}
	}
	t.Logf("remote_records_stable=true root_rgb_packages=%d settled_proofs=%d output_count=%d luna_contract_refs=%d latest_return_proof_present=%t latest_outbound_change_present=%t remote_luna_balance_matches_local_reported_84=%t readonly_requests=%d", packages, proofs, outputs, lunarRefs, returnProof, outboundChangeProof, remoteBalanceMatches84, transport.requests)
	if packages != 1 || lunarRefs == 0 || !returnProof || !outboundChangeProof {
		t.Error("required_settled_LUNA927_recovery_evidence_complete=false")
	}
}
