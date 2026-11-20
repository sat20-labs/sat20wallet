package e2e

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// All confirmations use this group's owned Core RPC. No bitcoin-cli environment
// or persistent PWA attachment can redirect the native asset lifecycle.
func (f *nativeIfaceChain) confirmL1(t *testing.T, manager *wallet.Manager, txid string) {
	t.Helper()
	var pending []string
	require.NoError(t, f.core.call("getrawmempool", nil, &pending))
	require.Contains(t, pending, txid)
	nativeIfaceL1Fee(t, f, nativeIfaceRawL1(t, f, txid))
	f.mine(t, 1)
	require.Eventually(t, func() bool {
		info, err := manager.GetIndexerClient().GetTxInfo(txid)
		return err == nil && info != nil && info.Confirmations > 0
	}, rpcReadyTimeout, 200*time.Millisecond)
	var confirmed struct {
		BlockHash     string `json:"blockhash"`
		Confirmations int    `json:"confirmations"`
	}
	require.NoError(t, f.core.call("getrawtransaction", []any{txid, true}, &confirmed))
	require.Positive(t, confirmed.Confirmations)
	require.NotEmpty(t, confirmed.BlockHash)
	var block struct {
		Tx []string `json:"tx"`
	}
	require.NoError(t, f.core.call("getblock", []any{confirmed.BlockHash, 1}, &block))
	require.Contains(t, block.Tx, txid)
}

func nativeIfaceVerifyL2(t *testing.T, network *realSatoshiNet, txid string) *wire.MsgTx {
	t.Helper()
	hash, err := chainhash.NewHashFromStr(txid)
	require.NoError(t, err)
	raw, err := network.Core.Client.GetRawTransaction(hash)
	require.NoError(t, err)
	tx := raw.MsgTx()
	require.Equal(t, txid, tx.TxID())
	previous := txscript.NewMultiPrevOutFetcher(nil)
	seen := map[wire.OutPoint]bool{}
	for _, input := range tx.TxIn {
		require.False(t, seen[input.PreviousOutPoint])
		seen[input.PreviousOutPoint] = true
		parent, err := network.Core.Client.GetRawTransaction(&input.PreviousOutPoint.Hash)
		require.NoError(t, err)
		require.Less(t, int(input.PreviousOutPoint.Index), len(parent.MsgTx().TxOut))
		previous.AddPrevOut(input.PreviousOutPoint, parent.MsgTx().TxOut[input.PreviousOutPoint.Index])
		require.NotEmpty(t, input.Witness)
	}
	hashes := txscript.NewTxSigHashes(tx, previous)
	for i, input := range tx.TxIn {
		prev := previous.FetchPrevOutput(input.PreviousOutPoint)
		vm, err := txscript.NewEngine(prev.PkScript, tx, i, txscript.StandardVerifyFlags, nil, hashes, prev.Value, prev.Assets, previous)
		require.NoError(t, err)
		require.NoError(t, vm.Execute())
	}
	return tx
}

// Bootstrap the existing private regtest with a real Bitcoin output. H1 uses
// the SDK's Anchor builder/signature; H2 spends that exact anchor with both
// operator signatures; H3 is an ordinary public SDK transfer. No indexer or
// channel state is inserted by the test.
func nativeIfaceBootstrap(t *testing.T, f *nativeIfaceChain, ordinary *wallet.Manager) {
	t.Helper()
	for _, node := range f.network.Nodes {
		require.NoError(t, node.Client.SetGenerate(false, 1))
		_, height, err := node.Client.GetBestBlock()
		require.NoError(t, err)
		require.Zero(t, height)
	}
	bootstrapKey := keyFromMnemonic(t, f.seeds["bootstrap"], 0)
	coreKey := keyFromMnemonic(t, f.seeds["core"], 0)
	witness, script, err := wallet.GetP2WSHscript(bootstrapKey.PubKey().SerializeCompressed(), coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	address, err := wallet.GetP2WSHaddressFromScript(script)
	require.NoError(t, err)
	funding, _, err := ordinary.BatchSendPlainSats(address, 1_000_000, 1, 2, nil)
	require.NoError(t, err)
	f.confirmL1(t, ordinary, funding)
	point := funding + ":0"
	output, err := ordinary.GetIndexerClient().GetTxOutput(point)
	require.NoError(t, err)
	require.NotNil(t, output)
	require.Equal(t, point, output.OutPointStr)
	require.Equal(t, int64(1_000_000), output.OutValue.Value)
	require.Equal(t, script, output.OutValue.PkScript)
	parent := nativeIfaceRawL1(t, f, funding)
	require.Equal(t, output.OutValue.Value, parent.TxOut[0].Value)
	require.Equal(t, script, parent.TxOut[0].PkScript)
	var unspent any
	require.NoError(t, f.core.call("gettxout", []any{funding, 0, true}, &unspent))
	require.NotNil(t, unspent, "the actual Bitcoin funding output must still be unspent")
	ticker := ordinary.GetTickerInfoV2(wallet.PLAIN_ASSET.String())
	require.NotNil(t, ticker)
	active, err := ordinary.AnchorOutputsActive()
	require.NoError(t, err)
	require.False(t, active)
	anchor, err := wallet.CreateAnchorTx(output, &wallet.PLAIN_ASSET, ticker, witness, nil, nil, active)
	require.NoError(t, err)
	bootstrap := wallet.NewInternalWalletWithMnemonic(f.seeds["bootstrap"], "", wallet.GetChainParam())
	require.NotNil(t, bootstrap)
	_, err = ordinary.SignAnchorTx(anchor, bootstrap.SignMessage)
	require.NoError(t, err)
	id, err := ordinary.BroadcastTx_SatsNet(anchor)
	require.NoError(t, err)
	require.Equal(t, anchor.TxID(), id)
	mineExpectedSatsNetTx(t, f.network, id, 1)
	recipient, err := wallet.GetPkScriptFromAddress(ordinary.GetWallet().GetAddress())
	require.NoError(t, err)
	spend := buildRealAnchorSpend(t, anchor, witness, recipient, bootstrapKey, coreKey)
	sent, err := f.network.Bootstrap.Client.SendRawTransaction(spend, true)
	require.NoError(t, err)
	require.Equal(t, spend.TxHash(), *sent)
	mineExpectedSatsNetTx(t, f.network, spend.TxID(), 2)
	nativeIfaceVerifyL2(t, f.network, spend.TxID())
	self, err := ordinary.SendAssets_SatsNet(ordinary.GetWallet().GetAddress(), "::", "10000", nil)
	require.NoError(t, err)
	mineExpectedSatsNetTx(t, f.network, self.TxID(), 3)
	nativeIfaceVerifyL2(t, f.network, self.TxID())
	verifyAnchorHistory(t, f.network, ordinary, witness, point, output)
	requireBIP9Active(t, f.network)
	for _, node := range f.network.Nodes {
		require.NoError(t, node.Client.SetGenerate(true, 1))
	}
}

func nativeIfaceOpenChannel(t *testing.T, f *nativeIfaceChain, manager *wallet.Manager) {
	t.Helper()
	id, err := manager.GetChannelAddress()
	require.NoError(t, err)
	require.Nil(t, manager.GetChannel(id), "fresh native group must really open, not reuse a previous channel")
	preview, err := manager.PreviewOpenChannel(2, 1_000_000)
	require.NoError(t, err)
	require.NotNil(t, preview)
	require.True(t, preview.Valid, preview.ValidationError)
	opened, err := manager.OpenChannel(2, 1_000_000, nil, "native real BRC20 lifecycle")
	require.NoError(t, err)
	require.Equal(t, id, opened)
	// The opening reservation owns the broadcast until Bitcoin confirms it.
	// Use it only to locate the transaction, never as proof the channel is active.
	pending := manager.GetAllChannels()[opened]
	require.NotNil(t, pending)
	require.Equal(t, opened, pending.ChannelId)
	require.Equal(t, wallet.CS_FUNDING_BROADCASTED, pending.Status)
	require.NotNil(t, pending.ChanPoint)
	point := pending.ChanPoint.OutPointStr
	funding := pending.ChanPoint.TxID()
	require.NotEmpty(t, funding)
	f.confirmL1(t, manager, funding)
	require.Eventually(t, func() bool {
		channel := manager.GetActiveChannelWithId(id)
		return channel != nil && channel.ChanPoint != nil && channel.ChanPoint.OutPointStr == point
	}, rpcReadyTimeout, 200*time.Millisecond)
	waitRGB11RegtestPeerChannelReady(t, manager, f.network, id)
	require.Equal(t, wallet.CS_READY, manager.GetChannel(id).Status)
}

// Native interface wallets have verified an absent root; they do not configure recovery.
func requireNativeIfaceLocalSender(t *testing.T, sender *wallet.Manager) {
	t.Helper()
	require.Eventually(t, func() bool {
		return sender.GetAccountManagementStatus().DiscoveryStatus == "inactive"
	}, rpcReadyTimeout, 20*time.Millisecond, "local account discovery after wallet start")
	status := sender.GetAccountManagementStatus()
	require.Equal(t, "inactive", status.DiscoveryStatus)
	require.True(t, status.LocalOnly)
	require.False(t, status.RecoveryConfigured)
	require.False(t, status.ActivationPending)
	require.Equal(t, "local", status.AccountState)
	require.True(t, status.WriteAllowed)
}

// Start begins a fresh discovery even when the wallet was previously writable.
func attachNativeIfaceWallet(t *testing.T, manager *wallet.Manager, network *realSatoshiNet) {
	t.Helper()
	attachRGB11RegtestWallet(t, manager, network)
	requireNativeIfaceLocalSender(t, manager)
}

func runNativeIfaceRealL2(t *testing.T, f *nativeIfaceChain) {
	t.Helper()
	a := f.device(t, f.seeds["ordinary-1"]).mgr
	b := f.device(t, f.seeds["ordinary-2"]).mgr
	f.fund(t, a.GetWallet().GetAddress(), 25_000_000)
	f.fund(t, b.GetWallet().GetAddress(), 10_000_000)
	core := wallet.NewInternalWalletWithMnemonic(f.seeds["core"], "", wallet.GetChainParam())
	require.NotNil(t, core)
	for i := 0; i < 8; i++ {
		f.fund(t, core.GetAddress(), 1_000_000)
	}
	t.Log("real L2 prerequisite: Bitcoin-backed H1/H2/H3 bootstrap")
	nativeIfaceBootstrap(t, f, a)
	attachNativeIfaceWallet(t, a, f.network)
	attachNativeIfaceWallet(t, b, f.network)
	t.Log("real L2 prerequisite: exact POS31/32/33 activation and same-DB restart")
	_, err := activateNativeIfacePOS(t, t.Context(), f.network, a, sdkPWARegtestPOSV2ActivationHeight, func() ([]string, error) {
		var pool []string
		err := f.core.call("getrawmempool", nil, &pool)
		return pool, err
	})
	require.NoError(t, err)
	// The activation driver restarts Core at H31 with the same database.
	attachNativeIfaceWallet(t, b, f.network)
	t.Log("real L2 prerequisite: actual SDK open and both-peer READY")
	nativeIfaceOpenChannel(t, f, b)
	t.Log("real L2 prerequisite: BRC20 deploy/mint/transfer-inscription/splice/unlock")
	require.Nil(t, b.GetTickerInfoV2(sdkPWARegtestWASMAsset), "the fresh chain must exercise actual BRC20 deployment")
	owner, buyer, sample := prepareNativeIfaceAsset(t, f.network, a, b, requireNativeIfaceLocalSender,
		func(_ string, blocks int) { f.mine(t, blocks) },
		func(manager *wallet.Manager, txid string) { f.confirmL1(t, manager, txid) })
	asset := indexercommon.NewAssetNameFromString(sdkPWARegtestWASMAsset)
	for _, actor := range []map[string]string{owner, buyer} {
		balance := b.GetAssetBalance_SatsNet(actor["address"], asset)
		require.NotNil(t, balance)
		require.Equal(t, int64(25_000_000), balance.Int64())
	}
	summary := b.GetIndexerRPCClient_SatsNet().GetAssetSummaryWithAddress(b.GetWallet().GetAddress())
	require.NotNil(t, summary, "an unavailable indexer cannot count as a zero BRC20 balance")
	for _, held := range summary.Data {
		if held.Name == *asset {
			require.Zero(t, held.Amount.Sign())
		}
	}
	point := sample["orderOutpoints"].([]string)[0]
	distribution, _, ok := strings.Cut(point, ":")
	require.True(t, ok)
	tx := nativeIfaceVerifyL2(t, f.network, distribution)
	var total int64
	for _, output := range tx.TxOut {
		for _, held := range output.Assets {
			if held.Name == *asset {
				total += held.Amount.Int64()
			}
		}
	}
	require.Equal(t, sdkPWARegtestWASMAmount, total)
	t.Logf("real BRC20 lifecycle: %s distribution=%s owner=%s buyer=%s", sdkPWARegtestWASMAsset, distribution, owner["address"], buyer["address"])
	nativeIfaceFundL2(t, a, f.network, owner, buyer)
	runNativeIfaceL2(t, f.network, f.cfg, owner, buyer, sample)
}

// Keep the two explicit 50k plain outputs of the original native API fixture,
// now funded by the real Bitcoin-backed H2 balance rather than a synthetic
// setup transaction. Asset-order outpoints remain unspent and unchanged.
func nativeIfaceFundL2(t *testing.T, sender *wallet.Manager, network *realSatoshiNet,
	owner, buyer map[string]string) {
	t.Helper()
	baseline, height, err := network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	block := blockAtHeight(t, network.Bootstrap, height)
	submitted := time.Now()
	id, err := sender.BatchSendAssetsV2_SatsNet([]string{owner["address"], buyer["address"]}, "::", []string{"50000", "50000"}, nil)
	require.NoError(t, err)
	waitForScheduledSatsNetTx(t, network, id, submitted, height, baseline, block.Header.Timestamp)
	tx := nativeIfaceVerifyL2(t, network, id)
	for _, actor := range []map[string]string{owner, buyer} {
		script, err := hex.DecodeString(actor["pk_script"])
		require.NoError(t, err)
		count := 0
		for i, out := range tx.TxOut {
			if hex.EncodeToString(out.PkScript) != actor["pk_script"] {
				continue
			}
			count++
			require.Equal(t, int64(50_000), out.Value)
			require.Empty(t, out.Assets)
			point := fmt.Sprintf("%s:%d", id, i)
			indexed, err := sender.GetIndexerRPCClient_SatsNet().GetTxOutput(point)
			require.NoError(t, err)
			require.NotNil(t, indexed)
			require.Equal(t, point, indexed.OutPointStr)
			require.Equal(t, int64(50_000), indexed.OutValue.Value)
			require.Equal(t, script, indexed.OutValue.PkScript)
			require.Empty(t, indexed.Assets)
		}
		require.Equal(t, 1, count, "each actor must receive one actual indexed plain output")
	}
}
