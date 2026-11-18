package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
		var confirmed *wallet.ConfirmedRGB11ContractWithdrawal
		require.Eventually(t, func() bool {
			confirmed, err = manager.LoadConfirmedRGB11Withdrawal(ctx, owner, withdrawal)
			return err == nil
		}, time.Minute, time.Second, "the original owner must validate both confirmed chains: %v", err)
		require.Equal(t, contract, confirmed.ContractURL)
		require.Equal(t, "100", confirmed.Amount.String())
		require.Empty(t, confirmed.PoolSources, "full withdrawal must leave no RGB pool change")
		sources, err := manager.ListRGB11ChannelSources(ctx, owner, pool, indexer.NewAssetNameFromString(asset))
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
