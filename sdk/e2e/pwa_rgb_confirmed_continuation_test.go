package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

// Continue the retained TT3bWK deposit, never the issue/deploy/deposit journey.
// The caller restores a private clone of that run's real chains and state.
func TestSDKPWARealRegtestRGBConfirmedDepositContinuationBrowser(t *testing.T) {
	root := os.Getenv("SAT20_REAL_RGB_CONTINUATION_ROOT")
	if root == "" {
		t.Skip("requires the retained TT3bWK confirmed-deposit state")
	}
	const funding = "f3663ace0a3554218645b61960de05699a639c303c0ea8660eceff184226954e"
	const blockHash = "688647f4fdc5e90da564da124916703347d0fd0569c27104b3a9c860ed652e82"
	const asset = "rgb11:f:fad9e3fc541ebf72268e1ce7eef7f27303ae4065ecbfa5fad96a4a045ac40184"
	const pool = "bcrt1qw86hsm7etf4jcqqg556x94s6ska9z0239ahl0tslsuvr5t5kd0nqn4an6p"
	const contract = pool + "_" + asset + "_transcend.tc"
	const journalKey = "dev-regtest-native-contract-deposit/regtest/send/e4559aa572470ec03c58a650c55ba7872b079580c8477e4565a2629026b0cdd3"
	require.True(t, filepath.IsAbs(root))
	canonicalRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(canonicalRoot, "/private/tmp/pwa-rgb-spent-fix-20261110/"), "operate only on the private continuation clone")
	marker, err := os.ReadFile(filepath.Join(root, "continuation-source.json"))
	require.NoError(t, err)
	var provenance struct {
		Funding string `json:"funding"`
		Source  string `json:"source"`
	}
	require.NoError(t, json.Unmarshal(marker, &provenance))
	require.Equal(t, funding, provenance.Funding)
	require.True(t, strings.HasSuffix(provenance.Source, "/sat20-workspace-e2e-TT3bWK/RGBCrossLayerBrowser"))
	require.Empty(t, os.Getenv("SAT20_REAL_SATOSHINET_ATTACH"))
	require.Equal(t, filepath.Join(root, "bitcoin"), os.Getenv("SAT20_REAL_BITCOIN_DATADIR"))
	require.Equal(t, filepath.Join(root, "wallets.json"), os.Getenv("SAT20_REAL_TEST_WALLETS"))
	runtimeDir := filepath.Join(root, "runtime")
	require.Equal(t, runtimeDir, os.Getenv("SAT20_REAL_REGTEST_RUNTIME"))
	require.Equal(t, "1", os.Getenv("SAT20_REAL_RGB_CROSS_LAYER"))

	statePath := filepath.Join(runtimeDir, "browser-failure-state", "context-85186-1-1.json")
	var state struct {
		Origins []struct {
			Origin    string `json:"origin"`
			Databases []struct {
				Name   string `json:"name"`
				Stores []struct {
					Name    string `json:"name"`
					Records []struct {
						Key   string          `json:"key"`
						Value json.RawMessage `json:"value"`
					} `json:"records"`
				} `json:"stores"`
			} `json:"indexedDB"`
		} `json:"origins"`
	}
	stateBytes, err := os.ReadFile(statePath)
	require.NoError(t, err)
	stateInfo, err := os.Stat(statePath)
	require.NoError(t, err)
	require.Zero(t, stateInfo.Mode().Perm()&0o077)
	require.NoError(t, json.Unmarshal(stateBytes, &state))
	var journal struct {
		Deposit      wallet.NativeContractDeposit `json:"deposit"`
		Acknowledged bool                         `json:"peer_acknowledged"`
		Broadcast    bool                         `json:"broadcast"`
	}
	matches := 0
	for _, origin := range state.Origins {
		for _, db := range origin.Databases {
			for _, store := range db.Stores {
				for _, record := range store.Records {
					if record.Key != journalKey {
						continue
					}
					require.Equal(t, "http://127.0.0.1:5189", origin.Origin)
					require.Equal(t, "sat20-wallet-sdk", db.Name)
					require.Equal(t, "kv", store.Name)
					var encoded string
					require.NoError(t, json.Unmarshal(record.Value, &encoded))
					raw, err := base64.StdEncoding.DecodeString(encoded)
					require.NoError(t, err)
					require.NoError(t, json.Unmarshal(raw, &journal))
					matches++
				}
			}
		}
	}
	require.Equal(t, 1, matches, "restore exactly the original native deposit journal")
	require.True(t, journal.Acknowledged)
	require.True(t, journal.Broadcast)
	require.Equal(t, contract, journal.Deposit.Intent.ContractURL)
	require.Equal(t, asset, journal.Deposit.Intent.AssetName)
	require.Equal(t, "100", journal.Deposit.Intent.Amount)
	require.Equal(t, funding+":1", journal.Deposit.Source.Output.OutPointStr)
	require.Equal(t, int64(330), journal.Deposit.Source.Output.Value())
	require.Equal(t, pool, journal.Deposit.Source.ChannelID)
	require.NotEmpty(t, journal.Deposit.Source.PublicProof)
	require.Len(t, journal.Deposit.Source.Output.Assets, 1)
	require.Equal(t, asset, journal.Deposit.Source.Output.Assets[0].Name.String())
	require.Equal(t, "100", journal.Deposit.Source.Output.Assets[0].Amount.String())
	require.Zero(t, journal.Deposit.Source.Output.Assets[0].BindingSat)

	var confirmed struct {
		TxID          string `json:"txid"`
		BlockHash     string `json:"blockhash"`
		Confirmations int64  `json:"confirmations"`
		Hex           string `json:"hex"`
	}
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawtransaction", funding, "true"), &confirmed))
	require.Equal(t, funding, confirmed.TxID)
	require.Equal(t, blockHash, confirmed.BlockHash)
	require.Positive(t, confirmed.Confirmations)
	require.Equal(t, blockHash, strings.TrimSpace(string(realRegtestBitcoinCLI(t, "getblockhash", "1139"))))
	tx, err := wallet.DecodeMsgTx(confirmed.Hex)
	require.NoError(t, err)
	require.Equal(t, funding, tx.TxID())
	require.True(t, tx.HasWitness())
	require.Greater(t, len(tx.TxOut), 1)
	require.Equal(t, int64(330), tx.TxOut[1].Value)
	require.Equal(t, journal.Deposit.Source.Output.OutValue.PkScript, tx.TxOut[1].PkScript)
	var pending []string
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawmempool"), &pending))
	require.Empty(t, pending, "never resume over an unreviewed pending Bitcoin transaction")

	before, err := capturePWABuildInputs()
	require.NoError(t, err)
	require.True(t, t.Run("artifact_guard", TestSDKPWARealRegtestArtifactFingerprintGuard))
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	sdk := filepath.Dir(filepath.Dir(source))
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	wasmPath, wasmRuntimePath := buildPWAWalletRuntime(t, ctx, sdk)
	after, err := capturePWABuildInputs()
	require.NoError(t, err)
	require.Empty(t, changedPWABuildInputs(before, after), "Core/plugin/WASM must use the same stable source inputs")
	wasmBytes, err := os.ReadFile(wasmPath)
	require.NoError(t, err)
	t.Logf("continuation build_input_sha256=%s wasm_sha256=%x", fingerprintPWABuildInputs(after), sha256.Sum256(wasmBytes))

	network := prepareSDKPWARegtestNodes(t)
	var anchor *posPWAAnchorEvidence
	require.Eventually(t, func() bool {
		snapshot, err := readSDKPWARegtestSatoshiNetSnapshot(network, 0)
		if err != nil {
			return false
		}
		for _, node := range snapshot.Nodes {
			if node.Height != snapshot.Height || node.Hash != snapshot.Hash || node.APIHeight != snapshot.Height {
				return false
			}
		}
		var matching []posPWAAnchorEvidence
		for _, candidate := range snapshot.Anchors {
			if candidate.FundingOutpoint == funding+":1" {
				matching = append(matching, candidate)
			}
		}
		require.LessOrEqual(t, len(matching), 1, "the original funding must have exactly one Anchor")
		if len(matching) == 1 && len(snapshot.MempoolTxIDs) == 0 {
			anchor = &matching[0]
			return true
		}
		return false
	}, 3*time.Minute, time.Second, "the existing confirmed deposit must produce its canonical Anchor without another deposit")
	t.Logf("continued original funding=%s L1_height=1139 Anchor=%s L2_height=%d", funding, anchor.TxID, anchor.Height)

	// Reject an already-submitted withdrawal before opening the restored browser.
	for _, node := range network.Nodes {
		items, err := rgbContinuationHistory(node, contract)
		require.NoError(t, err)
		for _, item := range items {
			require.NotEqual(t, 7, item.OrderType, "never resubmit an existing withdrawal")
		}
	}

	var fixture map[string]any
	fixtureBytes, err := os.ReadFile(filepath.Join(runtimeDir, "pwa-case-2869066549", "fixture.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(fixtureBytes, &fixture))
	prepared := fixture["walletFixture"].(map[string]any)
	require.Equal(t, journal.Deposit.Intent.Recipient, prepared["address"])
	prepared["asset"] = map[string]string{"key": asset, "ticker": strings.TrimPrefix(asset, "rgb11:f:"), "type": "RGB"}
	prepared["wasmPath"], prepared["wasmRuntimePath"] = wasmPath, wasmRuntimePath
	prepared["resumeConfirmedRGBDeposit"] = map[string]any{
		"storage_state": statePath, "journal_key": journalKey, "funding_txid": funding,
		"funding_height": 1139, "funding_block_hash": blockHash, "contract_url": contract,
		"asset_key": asset, "amount": "100", "carrier_value": 330, "pool_address": pool, "anchor": anchor,
	}
	seeds := readSDKPWARegtestMnemonics(t)
	confirmationWallet := wallet.NewInternalWalletWithMnemonic(seeds["ordinary-1"], "", wallet.GetChainParam())
	require.NotNil(t, confirmationWallet)
	mine := func(address string, blocks int) { mineSDKPWARegtestL1WithoutWalletManager(t, address, blocks) }
	prepared["control_url"] = startSDKPWARegtestBitcoinController(t, confirmationWallet.GetAddressByIndex(1), mine, network)
	artifactDir, err := os.MkdirTemp(runtimeDir, "rgb-confirmed-continuation-")
	require.NoError(t, err)
	fixturePath := writeSDKPWARegtestPrivateJSON(t, artifactDir, "fixture.json", fixture)
	evidenceDir := filepath.Join(runtimeDir, "pwa-browser-evidence")
	require.NoError(t, os.MkdirAll(evidenceDir, 0o700))
	runSDKPWARegtestBrowserGroup(t, filepath.Join(sdk, "..", "pwa"),
		filepath.Join(runtimeDir, "pwa-case-2869066549", "config.json"), fixturePath, evidenceDir,
		"rgb-confirmed-continuation", []string{"RGB PWA: continue the confirmed deposit Anchor and withdraw without another deposit"})

	var withdrawal, invokePoint string
	for i, node := range network.Nodes {
		var settled wallet.InvokeItem
		require.Eventually(t, func() bool {
			items, err := rgbContinuationHistory(node, contract)
			if err != nil {
				return false
			}
			var found []wallet.InvokeItem
			for _, item := range items {
				if item.OrderType == 7 {
					found = append(found, item)
				}
			}
			require.LessOrEqual(t, len(found), 1, "one browser withdrawal must create one ledger entry")
			if len(found) != 1 || !found[0].Finished() {
				return false
			}
			// Read the exact durable item, not just an aggregate balance.
			raw, err := rgbContinuationContractRead(node, "/info/contract/item/inutxo/"+contract+"/"+found[0].InUtxo)
			if err != nil || json.Unmarshal(raw, &settled) != nil {
				return false
			}
			return settled.Finished()
		}, 3*time.Minute, time.Second, "both pool owners must settle the exact withdrawal ledger")
		require.Equal(t, wallet.ITEM_STATUS_DEALT, settled.Done)
		require.Equal(t, asset, settled.AssetName)
		require.True(t, settled.ToL1)
		require.Equal(t, "100", settled.OutAmt.String())
		require.Equal(t, journal.Deposit.Intent.Recipient, settled.Address)
		if withdrawal == "" {
			withdrawal, invokePoint = settled.OutTxId, settled.InUtxo
		} else {
			require.Equal(t, withdrawal, settled.OutTxId)
			require.Equal(t, invokePoint, settled.InUtxo)
		}
		other := network.Nodes[1-i]
		verifyRGBContinuationPool(t, ctx, node, other, contract, pool, asset, funding, withdrawal)
		t.Logf("continuation owner=%s ledger=%s withdrawal=%s RGB_remaining=0 returned_carrier=330", node.role, settled.InUtxo, withdrawal)
	}
	finalInputs, err := capturePWABuildInputs()
	require.NoError(t, err)
	require.Empty(t, changedPWABuildInputs(after, finalInputs), "source inputs changed during continuation")
}

func rgbContinuationContractRead(node *testHarness, path string) ([]byte, error) {
	var response wallet.ContractStatusResp
	if err := posPWAReadJSON("http://"+node.stpAddr+"/regtest"+path, &response); err != nil {
		return nil, err
	}
	if response.Code != 0 {
		return nil, fmt.Errorf("%s contract read: %s", node.role, response.Msg)
	}
	return []byte(response.Status), nil
}

func rgbContinuationHistory(node *testHarness, contract string) ([]wallet.InvokeItem, error) {
	raw, err := rgbContinuationContractRead(node, "/info/contract/history/"+contract+"?start=0&limit=100")
	if err != nil {
		return nil, err
	}
	var history struct {
		Data []wallet.InvokeItem `json:"data"`
	}
	if err := json.Unmarshal(raw, &history); err != nil {
		return nil, err
	}
	return history.Data, nil
}

// Reuse the existing exclusive-node-DB inspection pattern. Never Start the
// inspection Manager: it only unlocks the retained identity and validates reads.
func verifyRGBContinuationPool(t *testing.T, ctx context.Context, node, other *testHarness,
	contract, pool, asset, funding, withdrawal string) {
	t.Helper()
	raw, err := rgbContinuationContractRead(node, "/info/contract/"+contract)
	require.NoError(t, err)
	var runtime wallet.SwapContractRuntimeInDB
	require.NoError(t, json.Unmarshal(raw, &runtime))
	require.Equal(t, pool, runtime.ChannelAddr)
	require.NoError(t, posPWAStopNode(ctx, node), "stop the cloned node normally before exclusive DB inspection")
	dbDir := filepath.Join(node.nodeDir, "data", "db", "stp", "regtest")
	holders, lsofErr := exec.Command("lsof", "-t", "+D", dbDir).CombinedOutput()
	require.Empty(t, holders, "a process still holds the cloned SDK DB")
	var exit *exec.ExitError
	require.ErrorAs(t, lsofErr, &exit)
	require.Equal(t, 1, exit.ExitCode(), "lsof must report no open files, not an inspection error")
	l1, err := url.Parse(os.Getenv("SAT20_REAL_L1_INDEXER"))
	require.NoError(t, err)
	l2Text, err := other.IndexerURL("regtest")
	require.NoError(t, err)
	l2, err := url.Parse(l2Text)
	require.NoError(t, err)
	database := indexerdb.NewKVDB(dbDir)
	require.NotNil(t, database)
	func() {
		defer func() { require.NoError(t, database.Close()) }()
		manager := wallet.NewManager(&sdkcommon.Config{Env: "dev", Chain: "regtest", Mode: "client",
			Peers:     []string{fmt.Sprintf("b@%s@http://%s/regtest", other.nodePubKey, other.stpAddr)},
			IndexerL1: &sdkcommon.Indexer{Scheme: l1.Scheme, Host: l1.Host, Proxy: "btc/regtest"},
			IndexerL2: &sdkcommon.Indexer{Scheme: l2.Scheme, Host: l2.Host, Proxy: "regtest"}}, database)
		require.NotNil(t, manager)
		defer manager.Close()
		require.NotEmpty(t, manager.GetWalletCatalog(), "never import or replace a pool identity")
		_, err = manager.UnlockWallet("rpctest")
		require.NoError(t, err)
		owner := manager.GetWallet().CloneByPubKey(runtime.LocalPubKey)
		require.NotNil(t, owner)
		require.Equal(t, runtime.LocalPubKey, owner.GetPaymentPubKey().SerializeCompressed())
		var confirmed *wallet.ConfirmedNativeContractWithdrawal
		require.Eventually(t, func() bool {
			confirmed, err = manager.LoadConfirmedNativeContractWithdrawal(ctx, owner, withdrawal)
			return err == nil
		}, time.Minute, time.Second, "the original owner must validate both confirmed chains: %v", err)
		require.Equal(t, contract, confirmed.ContractURL)
		require.Equal(t, "100", confirmed.Amount.String())
		require.Empty(t, confirmed.PoolSources, "full withdrawal must leave no RGB pool change")
		sources, err := manager.ListNativeChannelSources(ctx, owner, pool, indexer.NewAssetNameFromString(asset))
		require.NoError(t, err)
		require.Empty(t, sources, "both owners must independently recover zero remaining RGB")
		script, err := wallet.GetPkScriptFromAddress(pool)
		require.NoError(t, err)
		consumed := false
		for _, input := range confirmed.Bitcoin.TxIn {
			if input.PreviousOutPoint.String() == funding+":1" {
				consumed = true
			}
		}
		require.True(t, consumed, "withdrawal must spend the original deposit carrier")
		var returned int64
		for n, output := range confirmed.Bitcoin.TxOut {
			if !bytes.Equal(output.PkScript, script) {
				continue
			}
			returned += output.Value
			var unspent map[string]any
			require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "gettxout", withdrawal, fmt.Sprint(n), "true"), &unspent))
			require.NotEmpty(t, unspent, "returned pool carrier must still be unspent")
		}
		require.Equal(t, int64(330), returned, "zero RGB remaining must not lose the Bitcoin pool carrier")
	}()
	require.NoError(t, posPWARestartNode(ctx, node))
	waitSDKPWARegtestManagerHealth(t, node)
	require.NoError(t, connectNode(node, other))
	require.NoError(t, joinBlocks([]*testHarness{node, other}))
}
