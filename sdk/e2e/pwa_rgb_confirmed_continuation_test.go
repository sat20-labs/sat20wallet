package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
		for i := range snapshot.Anchors {
			if snapshot.Anchors[i].FundingOutpoint == funding+":1" {
				candidate := snapshot.Anchors[i]
				anchor = &candidate
				return true
			}
		}
		return false
	}, 3*time.Minute, time.Second, "the existing confirmed deposit must produce its canonical Anchor without another deposit")
	t.Logf("continued original funding=%s L1_height=1139 Anchor=%s L2_height=%d", funding, anchor.TxID, anchor.Height)

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
		"asset_key": asset, "amount": "100", "carrier_value": 330, "anchor": anchor,
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
}
