//go:build pwa

package e2e

import (
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/btcsuite/btcd/txscript"
	btcwire "github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	rgb11 "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/stretchr/testify/require"
)

func TestSDKPWARealRegtestRGBExpand(t *testing.T) {
	configureSDKPWARegtestPOSV2(t)
	network := prepareSDKPWARegtestNodes(t)
	// The browser advances ordinary-2's channel in its own wallet store.
	manager, _, mineL1 := prepareSDKPWARegtestAssets(t)
	attachRGB11RegtestWallet(t, manager, network)
	channel := ensureRGB11RegtestChannel(t, manager, network, mineL1)
	ensureRGB11RegtestAccountManagement(t, manager)
	if !manager.GetAccountManagementStatus().RecoveryConfigured {
		auth, err := manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		require.NoError(t, err)
		ensureRGB11RegtestRecoveryPackage(t, manager, auth)
		requireE2EFirstBackup(t, manager)
	}

	issued, err := manager.IssueRGB11Asset(t.Context(), wallet.RGB11IssueRequest{
		Schema: "NIA", Ticker: "XPND", Name: "Regtest channel expansion", Amounts: []uint64{10},
	})
	require.NoError(t, err)
	require.Len(t, issued.OutPoints, 1)
	name, err := manager.RGB11ChainAsset(&issued.AssetName, true)
	require.NoError(t, err)
	require.Equal(t, "rgb11:f:xpnd@"+issued.AssetName.Ticker[60:], name.String())
	contract, err := manager.ExportRGB11Contract(issued.ContractID)
	require.NoError(t, err)
	client, ok := manager.GetIndexerRPCClient().(*wallet.IndexerRPCClientMgr)
	require.True(t, ok)
	input, err := client.GetTxOutput(issued.OutPoints[0])
	require.NoError(t, err)
	require.GreaterOrEqual(t, input.Value(), int64(1660))
	amount := indexer.NewDefaultDecimal(10)

	// Fund the channel address on Bitcoin before adopting that allocation.
	tx := btcwire.NewMsgTx(2)
	tx.AddTxIn(input.TxIn())
	_, err = rgb11.ReserveChannelDBC(tx, 0)
	require.NoError(t, err)
	tx.AddTxOut(btcwire.NewTxOut(330, channel.GetChannelPkScript()))
	tx.AddTxOut(btcwire.NewTxOut(input.Value()-1330, input.OutValue.PkScript))
	source := rgb11.ChannelFunding{OutPoint: input.OutPointStr, Value: input.Value(),
		PkScript: input.OutValue.PkScript, AssetName: issued.AssetName, Amount: *amount.Clone(),
		Consignment: []byte(contract.Armor)}
	channel.Mutex.RLock()
	entropy, height := channel.AssetEntropy, channel.CommitHeight
	channel.Mutex.RUnlock()
	if entropy == ([32]byte{}) {
		_, err := rand.Read(entropy[:])
		require.NoError(t, err)
	}
	step, err := rgb11.BuildChannelStep(t.Context(), []rgb11.ChannelFunding{source}, tx, 0,
		[]rgb11.ChannelAssignment{{OutputIndex: 1, Amount: *amount.Clone()}}, rgb11.ChannelContext{
			ChannelID: channel.ChannelId, Operation: "expand-source", Branch: "funding",
			Height: uint64(height), Entropy: entropy, BitcoinNetwork: "regtest",
		}, regtestRGBEvidence{client})
	require.NoError(t, err)
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	fetcher.AddPrevOut(*input.OutPoint(), input.TxOut())
	tx, err = wallet.SignTxWithWallet(manager.GetWallet(), tx, fetcher)
	require.NoError(t, err)
	txid, err := client.BroadCastTx(tx)
	require.NoError(t, err)
	confirmRGB11RegtestL1Tx(t, manager, txid, manager.GetWallet().GetAddressByIndex(1), mineL1)
	funding, err := rgb11.ChannelFundingFromStep(*step, 1, *amount)
	require.NoError(t, err)
	carrier, err := client.GetTxOutput(funding.OutPoint)
	require.NoError(t, err)
	inventory, err := rgb11.EncodeChannelInventory([]rgb11.ChannelFunding{funding})
	require.NoError(t, err)
	asset := &wallet.AssetName{AssetName: *name}
	channel.Mutex.Lock()
	err = manager.AcceptSTPChannelFunding(channel, asset, []*wallet.TxOutput{carrier},
		map[string][]byte{rgb11.Protocol: inventory}, entropy)
	channel.Mutex.Unlock()
	require.NoError(t, err)
	proof, err := rgb11.EncodeChannelPackage(&rgb11.ChannelPackage{Version: 1,
		Funding: []rgb11.ChannelFunding{source}, Steps: []rgb11.ChannelStep{*step}})
	require.NoError(t, err)
	require.NoError(t, manager.ImportSTPChannelSettlement(tx, map[string][]byte{rgb11.Protocol: proof}))
	anchorID, expanded, _, err := manager.ExpandChannel(channel.ChannelId, name.String(), funding.OutPoint, "", nil)
	require.NoError(t, err)
	require.Equal(t, "10", expanded)
	hash, err := chainhash.NewHashFromStr(anchorID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		info, err := network.Core.Client.GetRawTransactionVerbose(hash)
		return err == nil && info.Confirmations > 0
	}, rpcReadyTimeout, 250*time.Millisecond, "the expanded RGB Anchor must confirm on the real SatoshiNet chain")
	confirmed, err := network.Core.Client.GetRawTransaction(hash)
	require.NoError(t, err)
	anchor := confirmed.MsgTx()
	info, _, err := wallet.CheckAnchorTxSig(anchor, false)
	require.NoError(t, err)
	require.Zero(t, info.Value)
	require.Zero(t, anchor.TxOut[0].Value)
	require.Len(t, info.Assets, 1)
	require.Equal(t, *name, info.Assets[0].Name)
	require.Zero(t, info.Assets[0].BindingSat)
	require.Equal(t, info.Assets, anchor.TxOut[0].Assets)
	_, present, err := sindexer.NativeAssetProof(anchor)
	require.NoError(t, err)
	require.True(t, present)
	require.Eventually(t, func() bool {
		current := manager.GetChannel(channel.ChannelId)
		if current == nil {
			return false
		}
		current.Mutex.RLock()
		defer current.Mutex.RUnlock()
		return current.Status == wallet.CS_READY && current.GetCommitLocalValue(asset).Cmp(amount) == 0
	}, rpcReadyTimeout, 250*time.Millisecond, "the channel must account for the expanded RGB by its SatoshiNet name")
	t.Logf("confirmed RGB expansion funding=%s anchor=%s asset=%s amount=%s value=%d", txid, anchorID, name, expanded, info.Value)
}

type regtestRGBEvidence struct{ *wallet.IndexerRPCClientMgr }

func (e regtestRGBEvidence) GetGenesisHash() (string, error) { return e.GetBlockHash(0) }

func (e regtestRGBEvidence) GetUTXO(point string) (*rgb11.BitcoinUTXO, error) {
	status, err := e.GetBitcoinUTXOStatus(point)
	if err != nil || status == nil || !status.Exists || !status.Unspent {
		return nil, err
	}
	script, err := hex.DecodeString(status.PkScript)
	return &rgb11.BitcoinUTXO{OutPoint: point, Value: status.Value, PkScript: script, Confirmations: status.Confirmations}, err
}

func (e regtestRGBEvidence) GetRawTx(txid string) ([]byte, error) {
	result, err := e.GetBitcoinRawTx(txid)
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(result.RawTx)
}

func (e regtestRGBEvidence) GetTxStatus(txid string) (*rgb11.BitcoinTxStatus, error) {
	result, err := e.GetBitcoinTxStatus(txid)
	if err != nil {
		return nil, err
	}
	return &rgb11.BitcoinTxStatus{TxID: result.TxID, InMempool: result.InMempool, Confirmed: result.Confirmed,
		BlockHeight: result.BlockHeight, BlockHash: result.BlockHash, Confirmations: result.Confirmations}, nil
}

func (e regtestRGBEvidence) GetOutspend(point string) (*rgb11.BitcoinOutspend, error) {
	result, err := e.GetBitcoinOutspend(point)
	if err != nil {
		return nil, err
	}
	return &rgb11.BitcoinOutspend{Spent: result.Spent, SpendingTx: result.SpendingTx}, nil
}

func (e regtestRGBEvidence) GetTip() (*rgb11.BitcoinTip, error) {
	result, err := e.GetBitcoinTip()
	if err != nil {
		return nil, err
	}
	return &rgb11.BitcoinTip{Height: result.Height, BlockHash: result.BlockHash}, nil
}

func (e regtestRGBEvidence) Broadcast(raw []byte) (string, error) { return e.BroadcastBitcoinTx(raw) }
