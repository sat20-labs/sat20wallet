package fakeindexer

import (
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestChannelLedgerProjectsConfirmedHistory(t *testing.T) {
	n := NewNetwork("satoshinet", 10)
	c := NewClient(n)
	key, _ := btcec.PrivKeyFromBytes([]byte{21})
	peer, _ := btcec.PrivKeyFromBytes([]byte{22})
	witness, script, err := wallet.GetP2WSHscript(key.PubKey().SerializeCompressed(), peer.PubKey().SerializeCompressed())
	require.NoError(t, err)
	channel, err := wallet.GetP2WSHaddressFromScript(script)
	require.NoError(t, err)
	source := fmt.Sprintf("%064x:0", 1)
	assets := swire.TxAssets{{Name: indexer.AssetName{Protocol: "ordx", Type: "f", Ticker: "test"}, Amount: *indexer.NewDefaultDecimal(25), BindingSat: 1}}
	invoice, err := sindexer.StandardAnchorInvoice(source, witness, 1000, assets, nil, false)
	require.NoError(t, err)
	sig := ecdsa.Sign(key, chainhash.HashB(invoice)).Serialize()
	anchorScript, err := sindexer.StandardAnchorScriptWithSig(source, witness, 1000, assets, sig)
	require.NoError(t, err)
	anchor := swire.NewMsgTx(swire.TxVersion)
	anchor.AddTxIn(&swire.TxIn{PreviousOutPoint: swire.OutPoint{Index: swire.AnchorTxOutIndex}, Sequence: swire.AnchorTxOutIndex, SignatureScript: anchorScript})
	anchor.AddTxOut(swire.NewTxOut(1000, assets, script))
	_, err = c.BroadCastTx_SatsNet(anchor)
	require.NoError(t, err)
	ledger, err := c.GetChannelLedger(channel)
	require.NoError(t, err)
	require.Empty(t, ledger, "unconfirmed ascends must not release quota locks")
	require.Equal(t, 10, c.GetSyncHeight())
	ledger, err = c.GetChannelLedger(channel)
	require.NoError(t, err)
	require.Len(t, ledger, 1)
	require.Equal(t, "ascending", ledger[0].Direction)
	require.Equal(t, channel, ledger[0].ChannelId)
	require.Equal(t, anchor.TxID(), ledger[0].L2TxId)
	require.Equal(t, 10, ledger[0].L2Height)
	require.Equal(t, []string{source}, ledger[0].L1Outpoints)
	require.EqualValues(t, 1000, ledger[0].Value)
	require.Equal(t, assets, ledger[0].Assets)
	outputs, err := c.GetUtxosWithAddress(channel)
	require.NoError(t, err)
	require.Contains(t, outputs, anchor.TxID()+":0", "address inventory must include asset-bearing collateral")
	other, err := c.GetChannelLedger("another-channel")
	require.NoError(t, err)
	require.Empty(t, other)

	l1id := fmt.Sprintf("%064x", 2)
	payload, err := sindexer.EncodeDescendPayloadV2(l1id, sindexer.DESCEND_OP_SPLICING_OUT, []uint32{1, 3})
	require.NoError(t, err)
	marker, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DESCENDING, payload)
	require.NoError(t, err)
	burnAssets := assets.Clone()
	burnAssets[0].Amount = *indexer.NewDefaultDecimal(10)
	changeAssets := assets.Clone()
	changeAssets[0].Amount = *indexer.NewDefaultDecimal(15)
	desc := swire.NewMsgTx(swire.TxVersion)
	point, err := swire.NewOutPointFromString(anchor.TxID() + ":0")
	require.NoError(t, err)
	desc.AddTxIn(swire.NewTxIn(point, nil, nil))
	desc.AddTxOut(swire.NewTxOut(600, burnAssets, marker))
	desc.AddTxOut(swire.NewTxOut(400, changeAssets, script))
	_, err = c.BroadCastTx_SatsNet(desc)
	require.NoError(t, err)
	ledger, err = c.GetChannelLedger(channel)
	require.NoError(t, err)
	require.Len(t, ledger, 1)
	require.Equal(t, 11, c.GetSyncHeight())
	ledger, err = c.GetChannelLedger(channel)
	require.NoError(t, err)
	require.Len(t, ledger, 2, "spent ascends must stay in the history")
	require.Equal(t, "descending", ledger[1].Direction)
	require.Equal(t, "splicing_out", ledger[1].Operation)
	require.Equal(t, l1id, ledger[1].L1TxId)
	require.Equal(t, desc.TxID()+":0", ledger[1].NullDataUtxo)
	require.Equal(t, []string{l1id + ":1", l1id + ":3"}, ledger[1].ReturnedChannelOutputs)
	require.EqualValues(t, 600, ledger[1].Value)
	require.Equal(t, burnAssets, ledger[1].Assets)
	require.False(t, ledger[1].Legacy)
	// No persisted ledger: rolling the block snapshot back removes its entry.
	delete(n.Blocks, 11)
	n.Height = 11
	ledger, err = c.GetChannelLedger(channel)
	require.NoError(t, err)
	require.Len(t, ledger, 1)
	// Decode failures cannot masquerade as an empty or partial history.
	n.TxBroadcasted[anchor.TxID()] = "invalid"
	_, err = c.GetChannelLedger(channel)
	require.Error(t, err)
}

func TestTransactionInfoReportsExplicitAbsence(t *testing.T) {
	c := NewClient(NewNetwork("bitcoin", 10))
	info, err := c.GetTxInfo(fmt.Sprintf("%064x", 999))
	require.Nil(t, info)
	require.ErrorContains(t, err, "No such mempool or blockchain transaction")
}
