package e2e

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// A ready AUTOPAY delegate proves its existing payment, not that its wallet
// can fund another interval. Reuse the real quote and existing L1-anchor path
// before handing the sole writer role to the browser.
func prepareSDKPWARegtestAccountUsageFunding(t *testing.T, manager *wallet.Manager,
	network *realSatoshiNet, mine func(string, int)) {
	t.Helper()
	attachRGB11RegtestWallet(t, manager, network)
	quote, err := manager.GetAccountAutopayFundingStatus()
	require.NoError(t, err)
	require.True(t, quote.Required)
	require.False(t, quote.FundingPending, "preserve an unresolved prior payment instead of funding a new one")
	require.Equal(t, rgb11RegtestAsset, quote.FeeAsset)
	require.Equal(t, manager.GetWallet().GetAddress(), quote.Payer)
	principal, err := strconv.ParseInt(quote.RecommendedFundingAmount, 10, 64)
	require.NoError(t, err)
	require.Positive(t, principal)
	height := rgb11RegtestCoreHeight(t, network)
	invokeFee, err := contractcommon.GasFeeAtHeight(contractcommon.InvokeBaseGas, height)
	require.NoError(t, err)
	resultFee, err := contractcommon.GasFeeAtHeight(contractcommon.ResultBaseGas, height)
	require.NoError(t, err)
	triggerFee, err := contractcommon.GasFeeAtHeight(contractcommon.TriggerBaseGas, height)
	require.NoError(t, err)
	defaults := dkvsindexer.NetworkDefaultsForParams(wallet.GetChainParam_SatsNet())
	require.Equal(t, defaults.AutopayContract, quote.ContractAddress)
	raw, err := manager.QueryContract(&wallet.ContractQueryRequest{
		Query: wallet.ContractQueryState, Contract: quote.ContractAddress,
	})
	require.NoError(t, err)
	state, err := dkvsindexer.DecodeAutopayContractState([]byte(raw), quote.ContractAddress)
	require.NoError(t, err)
	require.NotNil(t, state)
	require.False(t, state.Closed)
	require.Equal(t, contractcommon.TemplateAutopay, state.TemplateName)
	// Eight settlements cover the operator call/result, a remaining asset
	// anchor/unlock and the UI renewal/result. This is a finite test budget.
	const settlements = int64(8)
	gasTarget := settlements * (triggerFee + resultFee)
	gasBalance := int64(0)
	if state.GasBalance != "" {
		gasBalance, err = strconv.ParseInt(state.GasBalance, 10, 64)
		require.NoError(t, err)
	}
	t.Logf("AUTOPAY operator prerequisite: gas_balance=%d target=%d settlements=%d", gasBalance, gasTarget, settlements)
	if gasBalance < gasTarget {
		// Keep enough for even a fully depleted reserve; any L1 preparation
		// may advance payment blocks before the actual funding quote is read.
		operatorWalletTarget := gasTarget + invokeFee + resultFee
		if rgb11RegtestSgasBalance(t, manager) < operatorWalletTarget {
			channel := ensureRGB11RegtestChannel(t, manager, network, mine)
			ensureRGB11RegtestSgas(t, manager, channel, network, mine, operatorWalletTarget)
		}
		ensureRGB11RegtestAutopay(t, manager, network, defaults,
			gasTarget, triggerFee+resultFee, false)
	}
	target := principal + invokeFee + resultFee
	current := rgb11RegtestSgasBalance(t, manager)
	t.Logf("AUTOPAY UI funding prerequisite: wallet_SGAS=%d principal=%d invoke_result_GAS=%d target=%d",
		current, principal, invokeFee+resultFee, target)
	if current < target {
		channel := ensureRGB11RegtestChannel(t, manager, network, mine)
		ensureRGB11RegtestSgas(t, manager, channel, network, mine, target)
	}
	require.GreaterOrEqual(t, rgb11RegtestSgasBalance(t, manager), target)
	assertRGB11RegtestAutopayReady(t, manager)
}

func realRegtestPeersAreLoopback(addrs []string) bool {
	for _, addr := range addrs {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return false
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return false
		}
	}
	return true
}

func TestRealRegtestPeerAddressesRequireLoopback(t *testing.T) {
	tests := []struct {
		name  string
		addrs []string
		want  bool
	}{
		{name: "no peers", want: true},
		{name: "IPv4 loopback", addrs: []string{"127.0.0.1:18444"}, want: true},
		{name: "IPv6 loopback", addrs: []string{"[::1]:18444"}, want: true},
		{name: "public peer", addrs: []string{"203.0.113.7:18444"}},
		{name: "private non-loopback peer", addrs: []string{"10.0.0.7:18444"}},
		{name: "malformed peer", addrs: []string{"not-an-ip:18444"}},
		{name: "mixed peers", addrs: []string{"127.0.0.1:18444", "203.0.113.7:18444"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, realRegtestPeersAreLoopback(test.addrs))
		})
	}
}

func readSDKPWARegtestMnemonics(t *testing.T) map[string]string {
	t.Helper()
	fixturePath := os.Getenv("SAT20_REAL_TEST_WALLETS")
	require.NotEmpty(t, fixturePath, "use the private fixture referenced by knowledge/pwa-test-wallets.md")
	fixture, err := os.ReadFile(fixturePath)
	require.NoError(t, err)
	var identities []struct{ Role, Mnemonic string }
	require.NoError(t, json.Unmarshal(fixture, &identities))
	seeds := make(map[string]string)
	for _, identity := range identities {
		seeds[identity.Role] = identity.Mnemonic
	}
	return seeds
}

func realRegtestBitcoinCLI(t *testing.T, args ...string) []byte {
	t.Helper()
	cli := os.Getenv("SAT20_REAL_BITCOIN_CLI")
	dataDir := os.Getenv("SAT20_REAL_BITCOIN_DATADIR")
	port := os.Getenv("SAT20_REAL_BITCOIN_RPC_PORT")
	require.True(t, filepath.IsAbs(cli) && filepath.IsAbs(dataDir))
	_, err := strconv.Atoi(port)
	require.NoError(t, err)
	command := exec.Command(cli, append([]string{"-datadir=" + dataDir, "-regtest", "-rpcport=" + port}, args...)...)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "Bitcoin RPC %s: %s", args[0], output)
	return output
}

// The real environment owns these persistent ordinary-wallet databases. This
// function adds no simulated balances and does not replace either indexer.
func prepareSDKPWARegtestAssets(t *testing.T, existingOnly ...bool) (*wallet.Manager, *wallet.Manager, func(address string, blocks int)) {
	a, b, mine, _ := prepareSDKPWARegtestAssetsInternal(t, existingOnly...)
	return a, b, mine
}

// The POS continuation must release the ordinary-1 root before opening its
// derived account in the Miner plugin database. This keeps both the Manager
// and the separately-owned KVDB handle available for an ordered close.
func prepareSDKPWARegtestAssetsWithClose(t *testing.T, existingOnly ...bool) (*wallet.Manager, *wallet.Manager,
	func(address string, blocks int), func(*wallet.Manager)) {
	return prepareSDKPWARegtestAssetsInternal(t, existingOnly...)
}

func prepareSDKPWARegtestAssetsInternal(t *testing.T, existingOnly ...bool) (*wallet.Manager, *wallet.Manager,
	func(address string, blocks int), func(*wallet.Manager)) {
	t.Helper()
	require.LessOrEqual(t, len(existingOnly), 1)
	require.True(t, len(existingOnly) == 0 || existingOnly[0], "optional existingOnly argument can only require existing wallet state")
	reuseExistingWallets := len(existingOnly) == 1 && existingOnly[0]
	endpoint := os.Getenv("SAT20_REAL_L1_INDEXER")
	if endpoint == "" {
		t.Skip("requires the existing real regtest L1 environment")
	}
	l1, err := url.Parse(endpoint)
	require.NoError(t, err)
	ip := net.ParseIP(l1.Hostname())
	require.True(t, l1.Scheme == "http" && ip != nil && ip.IsLoopback() && l1.Port() != "" && l1.User == nil)
	require.Equal(t, "/btc/regtest", strings.TrimRight(l1.Path, "/"))
	runtimeDir := os.Getenv("SAT20_REAL_REGTEST_RUNTIME")
	require.True(t, filepath.IsAbs(runtimeDir))
	seeds := readSDKPWARegtestMnemonics(t)
	require.NotEmpty(t, seeds["bootstrap"], "missing fixed bootstrap role")
	require.NotEmpty(t, seeds["core"], "missing fixed core role")
	bootstrapKey := keyFromMnemonic(t, seeds["bootstrap"], 0)
	coreKey := keyFromMnemonic(t, seeds["core"], 0)
	peers := []string{
		"b@" + hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()) + "@http://127.0.0.1:30253/regtest",
		"s@" + hex.EncodeToString(coreKey.PubKey().SerializeCompressed()) + "@http://127.0.0.1:30263/regtest",
	}
	const password = "regtest-acceptance-only"
	closeWallets := make(map[*wallet.Manager]func())
	open := func(role string) *wallet.Manager {
		dir := filepath.Join(runtimeDir, "wallets", role)
		recoveryRoot := os.Getenv("SAT20_REAL_SDK_WALLET_DIR")
		if recoveryRoot != "" {
			require.NotEmpty(t, os.Getenv("SAT20_REAL_SATOSHINET_ATTACH"), "fixture recovery must reuse the existing regtest services")
			require.True(t, filepath.IsAbs(recoveryRoot))
			require.NotEqual(t, filepath.Clean(filepath.Join(runtimeDir, "wallets")), filepath.Clean(recoveryRoot),
				"preserve the incomplete original DB; recovery needs a separate client-data directory")
			dir = filepath.Join(recoveryRoot, role)
		}
		if reuseExistingWallets && recoveryRoot == "" {
			require.DirExists(t, dir, "existing-wallet regtest fixture requires the existing %s wallet database", role)
		} else {
			require.NoError(t, os.MkdirAll(dir, 0o700))
		}
		cfg := &sdkcommon.Config{Env: "dev", Chain: "regtest", Mode: "client", Peers: peers,
			IndexerL1: &sdkcommon.Indexer{Scheme: l1.Scheme, Host: l1.Host, Proxy: "btc/regtest"},
			IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "127.0.0.1:30262", Proxy: "regtest"}}
		database := indexerdb.NewKVDB(dir)
		require.NotNil(t, database)
		manager := wallet.NewManager(cfg, database)
		require.NotNil(t, manager)
		closed := false
		closeHandle := func() {
			if closed {
				return
			}
			manager.Close()
			require.NoError(t, database.Close())
			closed = true
		}
		closeWallets[manager] = closeHandle
		t.Cleanup(func() {
			closeHandle()
		})
		require.NotEmpty(t, seeds[role])
		if len(manager.GetWalletCatalog()) == 0 {
			if recoveryRoot != "" {
				require.NoError(t, manager.StartDKVSSync())
				requireAccountPWARootRecovery(t, manager, seeds[role], password)
				t.Logf("recovered existing %s SDK client data from DKVS; original DB preserved", role)
			} else {
				require.False(t, reuseExistingWallets, "existing-wallet regtest fixture requires an already-imported %s wallet", role)
				_, err := manager.ImportWallet(seeds[role], password)
				require.NoError(t, err, "import fixed %s", role)
			}
		} else {
			_, err := manager.UnlockWallet(password)
			require.NoError(t, err, "unlock existing %s", role)
		}
		if recoveryRoot != "" {
			status := manager.GetAccountManagementStatus()
			require.True(t, status.Active, "recovered fixture must retain the existing account profile")
			require.NoError(t, manager.SwitchWallet(status.RootWalletID, password))
			require.NoError(t, manager.SwitchAccount(0))
		}
		// Check the fixed identity without logging the mnemonic or private key.
		expected := wallet.NewInternalWalletWithMnemonic(seeds[role], "", wallet.GetChainParam())
		require.NotNil(t, expected)
		require.Equal(t, expected.GetAddress(), manager.GetWallet().GetAddress())
		return manager
	}
	closeManager := func(manager *wallet.Manager) {
		t.Helper()
		closeHandle, ok := closeWallets[manager]
		require.True(t, ok, "manager is not owned by this regtest asset fixture")
		closeHandle()
	}
	a, b := open("ordinary-1"), open("ordinary-2")
	rpc := func(args ...string) []byte {
		t.Helper()
		return realRegtestBitcoinCLI(t, args...)
	}
	var info struct {
		Version int `json:"version"`
	}
	require.NoError(t, json.Unmarshal(rpc("getnetworkinfo"), &info))
	require.Equal(t, 310000, info.Version)
	var peerInfo []struct {
		Addr string `json:"addr"`
	}
	require.NoError(t, json.Unmarshal(rpc("getpeerinfo"), &peerInfo))
	peerAddrs := make([]string, len(peerInfo))
	for i, peer := range peerInfo {
		peerAddrs[i] = peer.Addr
	}
	require.True(t, realRegtestPeersAreLoopback(peerAddrs), "BTC environment must have no non-loopback peers")
	var chain struct {
		Chain string `json:"chain"`
	}
	require.NoError(t, json.Unmarshal(rpc("getblockchaininfo"), &chain))
	require.Equal(t, "regtest", chain.Chain)
	esploraURL := os.Getenv("SAT20_REAL_ESPLORA")
	esplora, err := url.Parse(esploraURL)
	require.NoError(t, err)
	esploraIP := net.ParseIP(esplora.Hostname())
	require.True(t, esplora.Scheme == "http" && esploraIP != nil && esploraIP.IsLoopback() && esplora.Port() != "" && esplora.User == nil)
	httpClient := &http.Client{Timeout: 10 * time.Second}
	waitIndexed := func() {
		var height int
		require.NoError(t, json.Unmarshal(rpc("getblockcount"), &height))
		require.Eventually(t, func() bool { return a.GetSyncHeightL1() == height }, rpcReadyTimeout, time.Second, "real L1 indexer did not catch up")
		// bitcoin-cli prints string RPC results as plain text, unlike objects
		// and numeric results which can be decoded directly as JSON.
		wantHash := strings.TrimSpace(string(rpc("getblockhash", strconv.Itoa(height))))
		require.Len(t, wantHash, 64)
		require.Eventually(t, func() bool {
			resp, err := httpClient.Get(strings.TrimRight(esploraURL, "/") + "/blocks/tip/hash")
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			return err == nil && resp.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == wantHash
		}, rpcReadyTimeout, time.Second, "Electrs tip must match the actual Core tip")
	}
	mine := func(address string, blocks int) {
		rpc("generatetoaddress", strconv.Itoa(blocks), address)
		waitIndexed()
	}
	waitIndexed()
	if reuseExistingWallets {
		return a, b, mine, closeManager
	}
	// Non-mainnet indexer policy marks every sat of rewards at H0..H1000
	// as Vintage. Splitting those rewards cannot create plain funding sats.
	// Advance this group's real chain before mining the ordinary root funds;
	// leave the early special rewards on the separate account-1 mining sink.
	const firstNonVintageFundingHeight = 1001
	var fundingHeight int
	require.NoError(t, json.Unmarshal(rpc("getblockcount"), &fundingHeight))
	if fundingHeight < firstNonVintageFundingHeight {
		mine(a.GetWallet().GetAddressByIndex(1), firstNonVintageFundingHeight-fundingHeight)
	}
	matureCoinbases := make(map[string][]string)
	matureFunds := func(address string) int64 {
		matureCoinbases[address] = nil
		descriptors, err := json.Marshal([]string{"addr(" + address + ")"})
		require.NoError(t, err)
		var scan struct {
			Success  bool `json:"success"`
			Unspents []struct {
				TxID string `json:"txid"`
				Vout int    `json:"vout"`
			} `json:"unspents"`
		}
		require.NoError(t, json.Unmarshal(rpc("scantxoutset", "start", string(descriptors)), &scan))
		require.True(t, scan.Success)
		var total int64
		for _, point := range scan.Unspents {
			var output struct {
				Coinbase      bool    `json:"coinbase"`
				Confirmations int     `json:"confirmations"`
				Value         float64 `json:"value"`
			}
			require.NoError(t, json.Unmarshal(rpc("gettxout", point.TxID, strconv.Itoa(point.Vout), "true"), &output))
			if !output.Coinbase || output.Confirmations > 100 {
				total += int64(output.Value*100000000 + 0.5)
				if output.Coinbase {
					matureCoinbases[address] = append(matureCoinbases[address], point.TxID+":"+strconv.Itoa(point.Vout))
				}
			}
		}
		return total
	}
	// Coinbase funds are real BTC. Mine the maturity window to account 1 so
	// account 0 never selects the many immature outputs used only for warming.
	needsFunding := false
	for _, manager := range []*wallet.Manager{a, b} {
		if matureFunds(manager.GetWallet().GetAddress()) < 100000 {
			mine(manager.GetWallet().GetAddress(), 1)
			needsFunding = true
		}
	}
	if needsFunding {
		mine(a.GetWallet().GetAddressByIndex(1), 100)
	}
	for _, manager := range []*wallet.Manager{a, b} {
		require.GreaterOrEqual(t, matureFunds(manager.GetWallet().GetAddress()), int64(100000))
		balance := manager.GetAssetBalance("", &indexercommon.ASSET_PLAIN_SAT)
		if balance == nil || balance.Cmp(indexercommon.NewDecimal(100000, 0)) < 0 {
			// Real coinbases contain uncommon/black sats and are correctly
			// excluded from PLAIN_SAT. Split only a mature test coinbase using
			// SDK signing/broadcast. Keep its first sat in the small output;
			// fee sats are mined to the fixed account-1 sink below.
			points := matureCoinbases[manager.GetWallet().GetAddress()]
			require.NotEmpty(t, points)
			require.False(t, manager.IsLocked(manager.GetWallet().GetAddress(), points[0]), "test funding coinbase must not be reserved")
			input, err := manager.GetIndexerClient().GetTxOutput(points[0])
			require.NoError(t, err)
			require.NotNil(t, input)
			require.Greater(t, input.OutValue.Value, int64(101330))
			split := wire.NewMsgTx(2)
			split.AddTxIn(wire.NewTxIn(input.OutPoint(), nil, nil))
			split.AddTxOut(wire.NewTxOut(330, input.OutValue.PkScript))
			split.AddTxOut(wire.NewTxOut(input.OutValue.Value-330-1000, input.OutValue.PkScript))
			previous := txscript.NewCannedPrevOutputFetcher(input.OutValue.PkScript, input.OutValue.Value)
			split, err = manager.SignTx(split, previous)
			require.NoError(t, err, "SDK sign test coinbase split")
			_, err = manager.BroadcastTx(split)
			require.NoError(t, err, "actual Core acceptance of SDK-signed funding split")
			mine(a.GetWallet().GetAddressByIndex(1), 1)
			balance = manager.GetAssetBalance("", &indexercommon.ASSET_PLAIN_SAT)
		}
		require.NotNil(t, balance)
		require.GreaterOrEqual(t, balance.Cmp(indexercommon.NewDecimal(100000, 0)), 0)
	}
	asset := &indexercommon.AssetName{Protocol: "brc20", Type: "f", Ticker: "sgas"}
	if a.GetTickerInfoV2(asset.String()) == nil {
		issued, err := a.DeployTicker_brc20("sgas", 100000000, 1000000, 0, 2)
		require.NoError(t, err, "SDK SGAS deploy")
		require.NotNil(t, issued)
		require.NotNil(t, issued.CommitTx)
		require.NotNil(t, issued.RevealTx)
		mine(a.GetWallet().GetAddressByIndex(1), 1)
	}
	ticker := a.GetTickerInfoV2(asset.String())
	require.NotNil(t, ticker, "actual indexer must find SDK-deployed SGAS")
	require.Equal(t, 0, ticker.Divisibility)
	require.Equal(t, "1000000", ticker.Limit)
	if balance := a.GetAssetBalance("", asset); balance == nil || balance.Sign() == 0 {
		minted, err := a.MintAsset_brc20(a.GetWallet().GetAddress(), asset, indexercommon.NewDecimal(1000000, 0), nil, 2)
		require.NoError(t, err, "SDK SGAS mint")
		require.NotNil(t, minted)
		require.NotNil(t, minted.RevealTx)
		mine(a.GetWallet().GetAddressByIndex(1), 1)
	}
	balance := a.GetAssetBalance("", asset)
	require.NotNil(t, balance, "real indexer must return the minted asset")
	require.Greater(t, balance.Sign(), 0)
	return a, b, mine, closeManager
}

// The fresh local runner calls this after NodeReadiness. Account profiles are
// created and published through the SDK in this group's empty databases, never
// restored from a previous acceptance session. Temporary storage is sufficient
// here; the paid-storage journey prepares its own AUTOPAY funding separately.
func TestSDKPWARealRegtestFreshAccountPreparation(t *testing.T) {
	network := prepareSDKPWARegtestNodes(t)
	a, b, _ := prepareSDKPWARegtestAssets(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, manager := range []*wallet.Manager{a, b} {
		attachRGB11RegtestWallet(t, manager, network)
		ensureRGB11RegtestAccountManagement(t, manager)
		authorization, err := manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 100)
		require.NoError(t, err)
		ensureRGB11RegtestRecoveryPackage(t, manager, authorization)
		require.NoError(t, manager.SyncAccountManagementState(ctx))
		require.NoError(t, manager.SyncAccountRootWrapper(ctx))
	}
}

func TestSDKPWARealRegtestL1AssetPreparation(t *testing.T) {
	network := prepareSDKPWARegtestNodes(t)
	var existingOnly []bool
	if os.Getenv("SAT20_REAL_SATOSHINET_ATTACH") != "" {
		existingOnly = []bool{true}
	}
	a, b, mine := prepareSDKPWARegtestAssets(t, existingOnly...)
	seeds := readSDKPWARegtestMnemonics(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for i, manager := range []*wallet.Manager{a, b} {
		role := []string{"ordinary-1", "ordinary-2"}[i]
		_, err := manager.RecoverAccountManagementFromRootMnemonic(ctx, seeds[role], "regtest-acceptance-only")
		require.NoError(t, err, "synchronize existing RGB ownership before selecting preparation inputs")
		_, err = manager.RefreshRGB11State(ctx)
		require.NoError(t, err)
	}
	// Fresh account-1 mining rewards still carry rare sats. Reuse the real
	// coinbase split to prepare plain outputs and plain auxiliary change before
	// channel funding, instead of assuming a previous browser run made them.
	prepareSDKPWARegtestRGBBrowserCoins(t, a, b, mine)
	for _, openMint := range []bool{false, true} {
		asset := prepareSDKPWARegtestRuneAsset(t, a, mine, openMint)
		t.Logf("unified preparation has indexed pure Rune asset %s", asset)
	}
	mixed := prepareSDKPWARegtestMixedRuneCarrier(t, a, mine)
	require.NotNil(t, mixed)
	_, state := prepareSDKPWARegtestChannelPrerequisites(t, "l2-channel-open", a, b, mine)
	active, err := b.AnchorOutputsActive()
	require.NoError(t, err)
	if active {
		prepareSDKPWARegtestChannelV2Asset(t, a, b, mine)
	}
	waitSDKPWARegtestIndexes(t, network)
	t.Logf("unified real-chain preparation complete; mixed protection outpoint=%s channel=%s", mixed.OutPointStr, state)
}

// Add only the independent plain outputs missing from the browser business
// fixture. Existing RGB carriers are never selected or unlocked. Funding is an
// already-mature L1 coinbase at ordinary-1 account 1, signed by the existing SDK.
func prepareSDKPWARegtestRGBBrowserCoins(t *testing.T, a, b *wallet.Manager, mine func(string, int)) {
	t.Helper()
	free := func(manager *wallet.Manager) int {
		count := 0
		client, ok := manager.GetIndexerClient().(interface {
			GetBitcoinUTXOStatus(string) (*indexerwire.BitcoinUTXOStatus, error)
		})
		require.True(t, ok, "existing real indexer client must expose Bitcoin UTXO evidence")
		address := manager.GetWallet().GetAddress()
		for _, output := range manager.GetIndexerClient().GetUtxoListWithTicker(address, &indexercommon.ASSET_PLAIN_SAT) {
			if output == nil || output.Value < 100000 || manager.IsLocked(address, output.OutPoint) {
				continue
			}
			status, err := client.GetBitcoinUTXOStatus(output.OutPoint)
			if err == nil && status != nil && status.Unspent && status.Confirmations >= 1 {
				count++
			}
		}
		return count
	}
	neededA, neededB := 7-free(a), 1-free(b)
	if neededA <= 0 && neededB <= 0 {
		return
	}
	seeds := readSDKPWARegtestMnemonics(t)
	signer := wallet.NewInternalWalletWithMnemonic(seeds["ordinary-1"], "", wallet.GetChainParam())
	require.NotNil(t, signer)
	signer.SetSubAccount(1)
	sourceAddress := signer.GetAddress()
	require.Equal(t, a.GetWallet().GetAddressByIndex(1), sourceAddress)
	descriptors, err := json.Marshal([]string{"addr(" + sourceAddress + ")"})
	require.NoError(t, err)
	var scan struct {
		Success  bool `json:"success"`
		Unspents []struct {
			TxID          string `json:"txid"`
			Vout          uint32 `json:"vout"`
			Coinbase      bool   `json:"coinbase"`
			Confirmations int64  `json:"confirmations"`
		} `json:"unspents"`
	}
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "scantxoutset", "start", string(descriptors)), &scan))
	require.True(t, scan.Success)
	var input *indexercommon.TxOutput
	browserValue := int64(max(neededA, 0)+max(neededB, 0)) * 100000
	for _, point := range scan.Unspents {
		if !point.Coinbase || point.Confirmations < 101 {
			continue
		}
		candidate, err := a.GetIndexerClient().GetTxOutput(fmt.Sprintf("%s:%d", point.TxID, point.Vout))
		require.NoError(t, err)
		if candidate == nil || candidate.OutValue.Value <= browserValue+2330 {
			continue
		}
		// A mature coinbase can be entirely vintage. Check the exact sat
		// range for browser outputs rather than assuming only its first sat
		// has an asset; retain the original rare prefix and source change.
		_, tail, err := candidate.Cut(330)
		require.NoError(t, err)
		browserOutputs, _, err := tail.Cut(browserValue)
		require.NoError(t, err)
		if len(browserOutputs.Assets) != 0 {
			continue
		}
		input = candidate
		break
	}
	require.NotNil(t, input, "reuse a mature existing account-1 coinbase; do not mine a new maturity window")
	sourceScript, err := wallet.GetPkScriptFromAddress(sourceAddress)
	require.NoError(t, err)
	require.Equal(t, sourceScript, input.OutValue.PkScript)
	require.False(t, a.IsLocked(sourceAddress, input.OutPointStr))
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(input.OutPoint(), nil, nil))
	// The source output keeps the coinbase's rare first sat, as in the existing
	// unified preparation helper. All browser outputs are checked as plain.
	tx.AddTxOut(wire.NewTxOut(330, sourceScript))
	remaining := input.OutValue.Value - 330 - 1000
	for _, target := range []struct {
		Manager *wallet.Manager
		Count   int
	}{{a, neededA}, {b, neededB}} {
		script, err := wallet.GetPkScriptFromAddress(target.Manager.GetWallet().GetAddress())
		require.NoError(t, err)
		for i := 0; i < target.Count; i++ {
			tx.AddTxOut(wire.NewTxOut(100000, script))
			remaining -= 100000
		}
	}
	require.Greater(t, remaining, int64(330))
	tx.AddTxOut(wire.NewTxOut(remaining, sourceScript))
	previous := txscript.NewCannedPrevOutputFetcher(sourceScript, input.OutValue.Value)
	tx, err = wallet.SignTxWithWallet(signer, tx, previous)
	require.NoError(t, err)
	_, err = a.BroadcastTx(tx)
	require.NoError(t, err, "accept the existing SDK-signed L1 preparation transaction")
	mine(sourceAddress, 1)
	require.GreaterOrEqual(t, free(a), 7)
	require.GreaterOrEqual(t, free(b), 1)
	t.Logf("prepared only missing RGB browser plain outputs via existing L1 account-1 funds: txid=%s A_added=%d B_added=%d fee=1000", tx.TxHash(), max(neededA, 0), max(neededB, 0))
}

// Prepare only the real unused supply required by the Mint partial/remainder review.
// Reuse its confirmed deployment; never inject ticker/permission responses or reset a wallet.
func prepareSDKPWARegtestMintReviewAssets(t *testing.T, manager *wallet.Manager, mine func(string, int)) {
	t.Helper()
	const key = "ordx:f:PWAMINT"
	if manager.GetTickerInfoV2(key) == nil {
		deployed, err := manager.DeployTicker_ordx("PWAMINT", 200, 200, 1, 2)
		require.NoError(t, err, "SDK deploy real partial/remainder ticker")
		require.NotNil(t, deployed)
		require.NotNil(t, deployed.CommitTx)
		require.NotNil(t, deployed.RevealTx)
		mine(manager.GetWallet().GetAddressByIndex(1), 1)
	}
	ticker := manager.GetTickerInfoV2(key)
	require.NotNil(t, ticker, "existing real indexer must observe SDK deployment")
	require.Equal(t, "200", ticker.MaxSupply)
	require.Equal(t, "200", ticker.Limit)
	require.Equal(t, "0", ticker.TotalMinted, "review ticker must retain its original unminted supply")
	require.Equal(t, 1, ticker.N)
}

// Create the first BRC20 send case's existing transferable carrier through SDK issuance.
// Every object is observed from the real indexer; an existing confirmed fixture is reused.
func prepareSDKPWARegtestBRC20TransferAsset(t *testing.T, manager *wallet.Manager, mine func(string, int)) string {
	t.Helper()
	asset := &indexercommon.AssetName{Protocol: "brc20", Type: "f", Ticker: "pwbt"}
	address := manager.GetWallet().GetAddress()
	sink := manager.GetWallet().GetAddressByIndex(1)
	if manager.GetTickerInfoV2(asset.String()) == nil {
		deployed, err := manager.DeployTicker_brc20("pwbt", 250, 250, 0, 2)
		require.NoError(t, err)
		require.NotNil(t, deployed)
		require.NotNil(t, deployed.RevealTx)
		mine(sink, 1)
	}
	ticker := manager.GetTickerInfoV2(asset.String())
	require.NotNil(t, ticker)
	require.Equal(t, "250", ticker.MaxSupply)
	require.Equal(t, "250", ticker.Limit)
	require.Equal(t, 0, ticker.Divisibility)
	balance := manager.GetAssetBalance("", asset)
	if balance == nil || balance.Sign() == 0 {
		require.Equal(t, "0", ticker.TotalMinted, "an existing fixed supply must be reused from its current holder, not minted again")
		minted, err := manager.MintAsset_brc20(address, asset, indexercommon.NewDecimal(250, 0), nil, 2)
		require.NoError(t, err)
		require.NotNil(t, minted)
		require.NotNil(t, minted.RevealTx)
		mine(sink, 1)
	}
	balance = manager.GetAssetBalance("", asset)
	require.NotNil(t, balance)
	require.Equal(t, "250", balance.String(), "reuse only the original unspent 250-token fixture")
	outputs := manager.GetIndexerClient().GetUtxoListWithTicker(address, asset)
	if len(outputs) == 0 {
		transfer, err := manager.MintTransfer_brc20(address, address, nil, asset, indexercommon.NewDecimal(250, 0), 2, nil, false, nil, false, true, true)
		require.NoError(t, err)
		require.NotNil(t, transfer)
		require.NotNil(t, transfer.RevealTx)
		mine(sink, 1)
		outputs = manager.GetIndexerClient().GetUtxoListWithTicker(address, asset)
	}
	require.Len(t, outputs, 1, "real confirmed transferable inscription")
	require.NotEmpty(t, outputs[0].OutPoint)
	return outputs[0].OutPoint
}

// Issue a real decimal Rune, resuming the same SDK reservation if its commit
// already exists. The Rune commitment must mature before broadcasting reveal.
func prepareSDKPWARegtestRuneAsset(t *testing.T, manager *wallet.Manager, mine func(string, int), ordinaryMint ...bool) string {
	t.Helper()
	name := "SAT•ACCEPTANCE•PURE"
	selfMint, limit := true, int64(0)
	if len(ordinaryMint) > 0 && ordinaryMint[0] {
		name, selfMint, limit = "SAT•ACCEPTANCE•MINT", false, 10000
	}
	key := "runes:f:" + name
	address := manager.GetWallet().GetAddress()
	sink := manager.GetWallet().GetAddressByIndex(1)
	if manager.GetTickerInfoV2(key) == nil {
		etching, err := wallet.GenEtchingWithTerms(name, 'T', 10000, limit, selfMint, 2)
		require.NoError(t, err)
		stone := runestone.Runestone{Etching: etching}
		payload, err := stone.Encipher()
		require.NoError(t, err)
		var reservation *wallet.InscribeResv
		for _, candidate := range manager.GetAllResv() {
			inscription, ok := candidate.(*wallet.InscribeResv)
			if !ok || inscription.RevealTx == nil {
				continue
			}
			for _, output := range inscription.RevealTx.TxOut {
				if bytes.Equal(output.PkScript, payload) {
					require.Nil(t, reservation, "only one original Rune deployment reservation is permitted")
					reservation = inscription
				}
			}
		}
		if reservation == nil {
			reservation, err = manager.DeployTicker_runesWithTerms(address, name, 'T', 10000, limit, selfMint, 2, 2)
			require.NoError(t, err, "SDK real Rune commitment")
		}
		require.NotNil(t, reservation)
		require.NotNil(t, reservation.CommitTx)
		require.NotNil(t, reservation.RevealTx)
		tapscript := reservation.RevealTx.TxIn[0].Witness[len(reservation.RevealTx.TxIn[0].Witness)-2]
		tokenizer := txscript.MakeScriptTokenizer(0, tapscript)
		for tokenizer.Next() {
			require.NotEqual(t, byte(txscript.OP_IF), tokenizer.Opcode(), "new pure Rune deployment must not produce an inscription envelope")
		}
		require.NoError(t, tokenizer.Err())
		_, err = manager.BroadcastRunesDeployCommit(reservation)
		require.NoError(t, err)
		commit, err := manager.GetIndexerClient().GetTxInfo(reservation.CommitTx.TxID())
		require.NoError(t, err)
		require.NotNil(t, commit)
		if commit.Confirmations < 6 {
			mine(sink, int(6-commit.Confirmations))
		}
		_, err = manager.BroadcastRunesDeployReveal(reservation)
		require.NoError(t, err, "SDK real Rune reveal after commitment maturity")
		reveal, err := manager.GetIndexerClient().GetTxInfo(reservation.RevealTx.TxID())
		require.NoError(t, err)
		require.NotNil(t, reveal)
		if reveal.Confirmations == 0 {
			mine(sink, 1)
		}
	}
	ticker := manager.GetTickerInfoV2(key)
	require.NotNil(t, ticker, "real indexer must recognize the original confirmed etching")
	require.Equal(t, 2, ticker.Divisibility)
	require.Equal(t, "100", ticker.MaxSupply)
	if !selfMint && ticker.TotalMinted == "0" {
		runeID, err := runestone.RuneIdFromString(ticker.DisplayName)
		require.NoError(t, err)
		payload, err := wallet.EncipherRuneMintPayload(runeID, nil)
		require.NoError(t, err)
		var pending []string
		require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawmempool"), &pending))
		mintID := ""
		for _, id := range pending {
			var observed struct {
				Vout []struct {
					ScriptPubKey struct {
						Hex string `json:"hex"`
					} `json:"scriptPubKey"`
				} `json:"vout"`
			}
			require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawtransaction", id, "true"), &observed))
			for _, output := range observed.Vout {
				if output.ScriptPubKey.Hex == hex.EncodeToString(payload) {
					require.Empty(t, mintID, "only one original normal Rune mint may be pending")
					mintID = id
				}
			}
		}
		if mintID == "" {
			mintID, err = manager.MintAsset_runes(address, ticker, 2)
			require.NoError(t, err, "SDK normal Rune mint after open-terms deployment")
		}
		mine(sink, 1)
		t.Logf("confirmed normal SDK Rune mint: asset=%s txid=%s", key, mintID)
		ticker = manager.GetTickerInfoV2(key)
		require.NotNil(t, ticker)
	}
	require.Equal(t, "100", ticker.TotalMinted)
	balance := manager.GetAssetBalance("", &ticker.AssetName)
	require.NotNil(t, balance)
	minimumTestAmount, err := indexercommon.NewDecimalFromString("12.34", 2)
	require.NoError(t, err)
	require.GreaterOrEqual(t, balance.Cmp(minimumTestAmount), 0,
		"the current wallet must hold enough confirmed Rune for the decimal-send case")
	carriers := manager.GetIndexerClient().GetUtxoListWithTicker(address, &ticker.AssetName)
	require.NotEmpty(t, carriers, "the current wallet must retain at least one unspent Rune carrier")
	carrierTotal := indexercommon.NewDefaultDecimal(0)
	for _, carrier := range carriers {
		require.Len(t, carrier.Assets, 1, "pure Rune send inputs must not contain another indexed asset")
		asset := carrier.Assets[0]
		require.Equal(t, ticker.AssetName, asset.AssetName)
		require.Equal(t, 2, asset.Precision)
		amount, err := indexercommon.NewDecimalFromString(asset.Amount, asset.Precision)
		require.NoError(t, err)
		carrierTotal = carrierTotal.AddAlignPrecision(amount)
	}
	require.Equal(t, balance.String(), carrierTotal.String(), "pure Rune carriers must account for the current available balance")
	t.Logf("real Rune ready: asset=%s rune_id=%s etching_txid=%s carriers=%d balance=%s premine=%t",
		key, ticker.DisplayName, ticker.DeployTx, len(carriers), balance.String(), selfMint)
	return ticker.AssetName.String()
}

// prepareSDKPWARegtestMixedRuneCarrier reuses an exact live 660-sat Rune/NFT
// carrier with a non-empty inscription body, or creates one through the SDK's
// real inscription, reservation, acceptance, and broadcast paths.
func prepareSDKPWARegtestMixedRuneCarrier(t *testing.T, manager *wallet.Manager, mine func(string, int)) *indexercommon.TxOutput {
	t.Helper()
	address := manager.GetWallet().GetAddress()
	if output := findSDKPWARegtestMixedRuneCarrier(t, manager, address); output != nil {
		return output
	}

	reservation := findSDKPWARegtestMixedRuneReservation(t, manager)
	if reservation == nil {
		name := ""
		for suffix := byte('A'); suffix <= 'Z'; suffix++ {
			candidate := "SAT•ACCEPTANCE•MIXED•" + string(suffix)
			asset := "runes:f:" + candidate
			if manager.GetTickerInfoV2(asset) == nil {
				name = candidate
				break
			}
		}
		require.NotEmpty(t, name, "no unused mixed Rune fixture name is available")
		etching, err := wallet.GenEtchingWithTerms(name, 'T', 10000, 0, true, 2)
		require.NoError(t, err)
		pointer := uint32(0)
		runestonePayload, err := (&runestone.Runestone{Etching: etching, Pointer: &pointer}).Encipher()
		require.NoError(t, err)
		feeOutputs, err := selectSDKPWARegtestPlainFeeOutputs(t, manager, 5000)
		require.NoError(t, err, "select fee inputs through the existing SDK safe selector")
		request := &wallet.InscriptionRequest{
			CommitTxPrevOutputList: feeOutputs,
			CommitFeeRate:          2,
			RevealFeeRate:          2,
			RevealOutValue:         660,
			MinChangeValue:         330,
			InscriptionData: wallet.InscriptionData{
				ContentType:      "application/octet-stream",
				Body:             etching.Rune.Commitment(),
				RevealTxNullData: runestonePayload,
			},
			DestAddress:   address,
			ChangeAddress: address,
			Signer:        manager.SignTxV2,
		}
		reservation, err = wallet.Inscribe(wallet.GetChainParam(), request, manager.GenerateNewResvId())
		require.NoError(t, err, "build mixed Rune/Ordinal inscription through SDK")
		require.NotNil(t, reservation)
		reservation.ReservationBase = wallet.NewReservationBase(reservation.GetId(), true,
			wallet.RS_INSCRIBING_START, manager.GetWallet())
		manager.AddResv(reservation)
		require.NoError(t, manager.TestAcceptance([]*wire.MsgTx{reservation.CommitTx, reservation.RevealTx}),
			"Bitcoin Core must accept the unsigned-to-chain commit/reveal package")
		artifact, err := (&runestone.Runestone{}).DecipherFromTx(reservation.RevealTx)
		require.NoError(t, err)
		require.NotNil(t, artifact)
		require.Nil(t, artifact.Cenotaph)
		require.NotNil(t, artifact.Runestone)
		require.NotNil(t, artifact.Runestone.Etching)
		require.NotNil(t, artifact.Runestone.Etching.Rune)
		require.Equal(t, etching.Rune.Value, artifact.Runestone.Etching.Rune.Value)
		require.NotNil(t, artifact.Runestone.Pointer)
		require.Equal(t, uint32(0), *artifact.Runestone.Pointer)
		require.NotEmpty(t, reservation.RevealTx.TxIn[0].Witness)
		fields, _, err := indexercommon.ParseInscription(reservation.RevealTx.TxIn[0].Witness)
		require.NoError(t, err)
		require.Len(t, fields, 1)
		require.Equal(t, etching.Rune.Commitment(), fields[0][indexercommon.FIELD_CONTENT])
		require.Nil(t, fields[0][indexercommon.FIELD_RUNE_NAME])
		require.Equal(t, int64(660), reservation.RevealTx.TxOut[0].Value)
	}
	require.NotNil(t, reservation)
	require.GreaterOrEqual(t, reservation.Status, wallet.RS_INSCRIBING_START)

	commitTxID, err := manager.BroadcastRunesDeployCommit(reservation)
	require.NoError(t, err, "broadcast or resume the existing commit reservation")
	require.NotEmpty(t, commitTxID)
	commit, err := manager.GetIndexerClient().GetTxInfo(commitTxID)
	require.NoError(t, err)
	require.NotNil(t, commit)
	if commit.Confirmations < runestone.COMMIT_CONFIRMATIONS {
		require.NotNil(t, mine, "Rune commitment confirmation requires the existing regtest miner")
		mine(manager.GetWallet().GetAddressByIndex(1), int(runestone.COMMIT_CONFIRMATIONS-commit.Confirmations))
	}
	_, err = manager.BroadcastRunesDeployReveal(reservation)
	require.NoError(t, err, "broadcast or resume reveal after the Rune commitment matures")
	revealTxID := reservation.RevealTx.TxID()
	reveal, err := manager.GetIndexerClient().GetTxInfo(revealTxID)
	if err != nil || reveal == nil || reveal.Confirmations == 0 {
		require.NotNil(t, mine, "Rune reveal confirmation requires the existing regtest miner")
		mine(manager.GetWallet().GetAddressByIndex(1), 1)
	}
	require.Eventually(t, func() bool {
		info, err := manager.GetIndexerClient().GetTxInfo(revealTxID)
		return err == nil && info != nil && info.Confirmations > 0
	}, rpcReadyTimeout, time.Second, "mixed Rune inscription reveal was not confirmed")

	output := findSDKPWARegtestMixedRuneCarrier(t, manager, address)
	require.NotNil(t, output, "real indexer must expose the confirmed mixed carrier")
	t.Logf("real mixed Rune/NFT carrier ready: outpoint=%s value=%d assets=%v", output.OutPointStr, output.OutValue.Value, output.Assets)
	return output
}

func selectSDKPWARegtestPlainFeeOutputs(t *testing.T, manager *wallet.Manager, required int64) (wallet.PrevOutputs, error) {
	t.Helper()
	address := manager.GetWallet().GetAddress()
	outputs, err := manager.SelectUtxosForFeeV2(address, nil, required, false, false)
	if err != nil {
		return nil, err
	}
	result := make(wallet.PrevOutputs, len(outputs))
	for i, output := range outputs {
		for _, asset := range output.Assets {
			if asset.Name != indexercommon.ASSET_PLAIN_SAT {
				return nil, fmt.Errorf("SDK fee selector returned a non-plain asset carrier: %s", output.OutPointStr)
			}
		}
		result[i] = output
	}
	return result, nil
}

func findSDKPWARegtestMixedRuneReservation(t *testing.T, manager *wallet.Manager) *wallet.InscribeResv {
	t.Helper()
	var found *wallet.InscribeResv
	for _, candidate := range manager.GetAllResv() {
		reservation, ok := candidate.(*wallet.InscribeResv)
		if !ok || reservation.Status < wallet.RS_INSCRIBING_START || reservation.RevealTx == nil ||
			len(reservation.RevealTx.TxIn) == 0 || len(reservation.RevealTx.TxOut) < 2 || reservation.RevealTx.TxOut[0].Value != 660 {
			continue
		}
		fields, _, err := indexercommon.ParseInscription(reservation.RevealTx.TxIn[0].Witness)
		if err != nil || len(fields) != 1 {
			continue
		}
		artifact, err := (&runestone.Runestone{}).DecipherFromTx(reservation.RevealTx)
		if err != nil || artifact == nil || artifact.Cenotaph != nil || artifact.Runestone == nil ||
			artifact.Runestone.Etching == nil || !sdkPWARegtestIsMixedRuneEtching(artifact.Runestone.Etching) ||
			artifact.Runestone.Pointer == nil || *artifact.Runestone.Pointer != 0 {
			continue
		}
		if !sdkPWARegtestIsMixedRuneEtching(artifact.Runestone.Etching) ||
			!bytes.Equal(fields[0][indexercommon.FIELD_CONTENT], artifact.Runestone.Etching.Rune.Commitment()) {
			continue
		}
		require.Nil(t, found, "only one active mixed Rune carrier reservation is permitted")
		found = reservation
	}
	return found
}

func findSDKPWARegtestMixedRuneCarrier(t *testing.T, manager *wallet.Manager, address string) *indexercommon.TxOutput {
	t.Helper()
	indexer := manager.GetIndexerClient()
	summary := indexer.GetAssetSummaryWithAddress(address)
	require.NotNil(t, summary, "read the actual L1 asset summary before looking for a mixed carrier")
	var candidates []*indexerwire.TxOutputInfo
	for _, asset := range summary.Data {
		if asset != nil && asset.Name.Protocol == "runes" {
			candidates = append(candidates, indexer.GetUtxoListWithTicker(address, &asset.Name)...)
		}
	}
	for _, output := range candidates {
		if !sdkPWARegtestHasExpectedMixedRuneAssets(output) {
			continue
		}
		fields, ok, err := sdkPWARegtestOutputHasMixedRuneCommitment(manager, output.OutPoint)
		require.NoError(t, err, "read the transaction backing a matching Rune/NFT carrier")
		if !ok {
			parts := strings.Split(output.OutPoint, ":")
			require.Len(t, parts, 2)
			vout, err := strconv.ParseUint(parts[1], 10, 32)
			require.NoError(t, err)
			value, _, confirmed := bitcoinUnspentAt(t, parts[0], uint32(vout))
			require.True(t, confirmed, "an ineligible historical mixed carrier must remain confirmed and unspent")
			require.Equal(t, int64(660), value)
			continue
		}
		require.NotEmpty(t, fields[indexercommon.FIELD_CONTENT])
		parts := strings.Split(output.OutPoint, ":")
		require.Len(t, parts, 2)
		vout, err := strconv.ParseUint(parts[1], 10, 32)
		require.NoError(t, err)
		value, script, confirmed := bitcoinUnspentAt(t, parts[0], uint32(vout))
		require.True(t, confirmed, "matching mixed carrier must remain confirmed and unspent")
		require.Equal(t, int64(660), value)
		require.Equal(t, output.PkScript, script)
		return output.ToTxOutput()
	}
	return nil
}

func sdkPWARegtestHasExpectedMixedRuneAssets(output *indexercommon.AssetsInUtxo) bool {
	if output == nil || output.Value != 660 || len(output.Assets) != 2 {
		return false
	}
	var runeAsset, ordinalAsset *indexercommon.DisplayAsset
	for _, asset := range output.Assets {
		switch {
		case asset.AssetName.Protocol == "runes" && asset.AssetName.Type == "f":
			runeAsset = asset
		case asset.AssetName.Protocol == "ordx" && asset.AssetName.Type == "o":
			ordinalAsset = asset
		}
	}
	return runeAsset != nil && runeAsset.Amount == "100" && runeAsset.Precision == 2 &&
		ordinalAsset != nil && ordinalAsset.Amount == "1" && ordinalAsset.Precision == 0
}

func sdkPWARegtestOutputHasMixedRuneCommitment(manager *wallet.Manager, outpoint string) (map[int][]byte, bool, error) {
	parts := strings.Split(outpoint, ":")
	if len(parts) != 2 || parts[0] == "" {
		return nil, false, fmt.Errorf("invalid output point %q", outpoint)
	}
	rawHex, err := manager.GetIndexerClient().GetRawTx(parts[0])
	if err != nil {
		return nil, false, err
	}
	raw, err := hex.DecodeString(rawHex)
	if err != nil {
		return nil, false, err
	}
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		return nil, false, err
	}
	if !strings.EqualFold(tx.TxID(), parts[0]) {
		return nil, false, fmt.Errorf("raw transaction id %s does not match output point %s", tx.TxID(), outpoint)
	}
	vout, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return nil, false, err
	}
	if vout != 0 || len(tx.TxOut) == 0 || tx.TxOut[0].Value != 660 {
		return nil, false, nil
	}
	artifact, err := (&runestone.Runestone{}).DecipherFromTx(&tx)
	if err != nil || artifact == nil || artifact.Cenotaph != nil || artifact.Runestone == nil ||
		artifact.Runestone.Etching == nil || !sdkPWARegtestIsMixedRuneEtching(artifact.Runestone.Etching) ||
		artifact.Runestone.Pointer == nil || *artifact.Runestone.Pointer != 0 {
		return nil, false, nil
	}
	for _, input := range tx.TxIn {
		fields, _, err := indexercommon.ParseInscription(input.Witness)
		if err != nil {
			continue
		}
		for _, field := range fields {
			if bytes.Equal(field[indexercommon.FIELD_CONTENT], artifact.Runestone.Etching.Rune.Commitment()) {
				return field, true, nil
			}
		}
	}
	return nil, false, nil
}

func sdkPWARegtestIsMixedRuneEtching(etching *runestone.Etching) bool {
	if etching == nil || etching.Rune == nil || etching.Spacers == nil {
		return false
	}
	name := (&runestone.SpacedRune{Rune: *etching.Rune, Spacers: *etching.Spacers}).String()
	return strings.HasPrefix(name, "SAT•ACCEPTANCE•MIXED•")
}

// Real ORDX for the ordinary Bitcoin send/split cases (binding ratio one).
func prepareSDKPWARegtestORDXSendAsset(t *testing.T, manager *wallet.Manager, mine func(string, int)) {
	t.Helper()
	const name = "pwapos"
	asset := &indexercommon.AssetName{Protocol: "ordx", Type: "f", Ticker: name}
	sink := manager.GetWallet().GetAddressByIndex(1)
	if manager.GetTickerInfoV2(asset.String()) == nil {
		deployment, err := manager.DeployTicker_ordx(name, 5000, 5000, 1, 2)
		require.NoError(t, err)
		require.NotNil(t, deployment)
		mine(sink, 1)
	}
	ticker := manager.GetTickerInfoV2(asset.String())
	require.NotNil(t, ticker)
	require.Equal(t, 1, ticker.N)
	require.Equal(t, "5000", ticker.MaxSupply)
	if ticker.TotalMinted == "0" {
		minted, err := manager.MintAsset_ordx(manager.GetWallet().GetAddress(), ticker, 5000, nil, 2)
		require.NoError(t, err)
		require.NotNil(t, minted)
		mine(sink, 1)
	}
	balance := manager.GetAssetBalance("", asset)
	require.NotNil(t, balance)
	require.GreaterOrEqual(t, balance.Int64(), int64(1200), "original send plus two split outputs need 1200 real tokens")
}

// The channel counterparty uses its actual fixed Core wallet and L1 coins.
// Fund through the SDK only when its existing plain balance is insufficient.
func prepareSDKPWARegtestCoreChannelCoins(t *testing.T, manager *wallet.Manager, mine func(string, int)) {
	t.Helper()
	seeds := readSDKPWARegtestMnemonics(t)
	coreWallet := wallet.NewInternalWalletWithMnemonic(seeds["core"], "", wallet.GetChainParam())
	require.NotNil(t, coreWallet)
	address := coreWallet.GetAddress()
	balance := manager.GetAssetBalance(address, &indexercommon.ASSET_PLAIN_SAT)
	if balance == nil || balance.Int64() < 4000000 {
		// Reuse the already-funded auxiliary account, keeping protected RGB
		// carriers at account 0 intact and the active wallet identity unchanged.
		signer := manager.GetWallet().Clone()
		signer.SetSubAccount(1)
		require.Equal(t, manager.GetWallet().GetAddressByIndex(1), signer.GetAddress())
		locks, err := manager.GetLockedUtxoList(manager.GetWallet().GetAddress())
		require.NoError(t, err)
		if lock := locks["c2c6f74766ca62fd313eb6ca7d8daff7d4f3dd73f14abf2b091017da02b89cc7:2"]; lock != nil {
			t.Logf("existing standard-RGB carrier remains protected: reason=%s value=%d", lock.Reason, lock.Value)
		}
		tx, fee, err := manager.BatchSendAssetsWithWallet(signer, address, indexercommon.ASSET_PLAIN_SAT.String(), "1000000", 8, 2, nil)
		require.NoError(t, err, "SDK real L1 funding of fixed Core channel counterparty")
		require.NotNil(t, tx)
		mine(manager.GetWallet().GetAddressByIndex(1), 1)
		t.Logf("real Core channel fee coins from existing account 1: txid=%s outputs=8 value_each=1000000 fee=%d", tx.TxID(), fee)
	}
	balance = manager.GetAssetBalance(address, &indexercommon.ASSET_PLAIN_SAT)
	require.NotNil(t, balance)
	require.GreaterOrEqual(t, balance.Int64(), int64(4000000))
}

func prepareSDKPWARegtestChannelOpeningCoins(t *testing.T, source, receiver *wallet.Manager, amount int64, mine func(string, int)) {
	t.Helper()
	preview, err := receiver.PreviewOpenChannel(2, amount)
	require.NoError(t, err)
	require.NotNil(t, preview)
	if !preview.Valid {
		t.Logf("real opening funding preflight: valid=%t reason=%s", preview.Valid, preview.ValidationError)
		require.True(t, strings.HasPrefix(preview.ValidationError, "no enough plain sats") || preview.ValidationError == "no plain sats",
			"only missing spendable fee coins may trigger test funding: %s", preview.ValidationError)
		signer := source.GetWallet().Clone()
		signer.SetSubAccount(1)
		require.Equal(t, source.GetWallet().GetAddressByIndex(1), signer.GetAddress())
		tx, fee, err := source.BatchSendAssetsWithWallet(signer, receiver.GetWallet().GetAddress(), indexercommon.ASSET_PLAIN_SAT.String(),
			strconv.FormatInt(amount*2, 10), 1, 2, nil)
		require.NoError(t, err)
		require.NotNil(t, tx)
		mine(signer.GetAddress(), 1)
		t.Logf("real opening fee coins from existing A1: txid=%s value=%d fee=%d", tx.TxID(), amount*2, fee)
		preview, err = receiver.PreviewOpenChannel(2, amount)
		require.NoError(t, err)
		require.NotNil(t, preview)
	}
	require.True(t, preview.Valid, "real SDK opening funding must be sufficient: %s", preview.ValidationError)
}
