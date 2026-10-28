package wallet

import (
	"fmt"
	"testing"

	sch "github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestChannelDeanchorInputsRequiresIndexedReceiptAndVerifiedHistory(t *testing.T) {
	p, _, l2, channel, _ := channelHistoryFixture(t)
	parent := swire.NewMsgTx(2)
	parent.AddTxIn(swire.NewTxIn(&swire.OutPoint{Index: 0}, nil, nil))
	parent.AddTxOut(swire.NewTxOut(100, nil, []byte{0x51}))
	hash, err := sch.NewHashFromStr(parent.TxID())
	require.NoError(t, err)
	tx := swire.NewMsgTx(2)
	point := swire.NewOutPoint(hash, 0)
	tx.AddTxIn(swire.NewTxIn(point, nil, nil))
	marker, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DESCENDING, []byte("withdrawal"))
	require.NoError(t, err)
	tx.AddTxOut(swire.NewTxOut(30, nil, marker))
	raw, err := EncodeMsgTx_SatsNet(parent)
	require.NoError(t, err)
	l2.raw = map[string]string{parent.TxID(): raw}
	_, _, err = p.ChannelDeanchorInputs(channel, "withdrawal", tx)
	require.ErrorContains(t, err, "missing UTXO")

	l2.ledger = append(l2.ledger, &sindexer.ChannelLedgerEntry{ChannelId: channel, Direction: "descending", L1TxId: "withdrawal", L2TxId: tx.TxID(), L2Height: 10})
	previous, indexed, err := p.ChannelDeanchorInputs(channel, "withdrawal", tx)
	require.NoError(t, err)
	require.True(t, indexed)
	require.EqualValues(t, 100, previous.FetchPrevOutput(*point).Value)

	// Historical lookups must remain cryptographically tied to each prevout txid.
	parent.TxOut[0].Value = 101
	l2.raw[point.Hash.String()], err = EncodeMsgTx_SatsNet(parent)
	require.NoError(t, err)
	_, _, err = p.ChannelDeanchorInputs(channel, "withdrawal", tx)
	require.ErrorContains(t, err, "historical")
}

func TestRecoveredAnchorRequiresExistingIndexedSource(t *testing.T) {
	p, _, l2, channel, point := channelHistoryFixture(t)
	anchor := channelHistoryAnchor(t, point, 90)
	signCalls := 0
	sign := func([]byte) ([]byte, error) {
		signCalls++
		return nil, fmt.Errorf("sign reached")
	}
	_, err := p.SignRecoveredChannelAnchorTx(anchor, sign)
	require.ErrorContains(t, err, "recovery anchor source is not indexed")
	require.Zero(t, signCalls)

	l2.ledger[0].L1Outpoints = []string{point}
	_, err = p.SignRecoveredChannelAnchorTx(anchor, sign)
	require.ErrorContains(t, err, "sign reached")
	require.Equal(t, 1, signCalls)

	// Recovery only authenticates an existing mapping and does not create one.
	require.Equal(t, []string{point}, l2.ledger[0].L1Outpoints)
	require.Equal(t, channel, l2.ledger[0].ChannelId)
}

func TestRecoveredAnchorRejectsIndexerTipChange(t *testing.T) {
	p, _, l2, _, point := channelHistoryFixture(t)
	l2.ledger[0].L1Outpoints = []string{point}
	p.l2IndexerClient.active = &channelHistoryTipChangeRPC{channelHistoryRPC: l2}
	anchor := channelHistoryAnchor(t, point, 90)
	called := false
	_, err := p.SignRecoveredChannelAnchorTx(anchor, func([]byte) ([]byte, error) {
		called = true
		return nil, nil
	})
	require.ErrorContains(t, err, "L2 changed during anchor recovery")
	require.False(t, called)
}

type channelHistoryTipChangeRPC struct {
	*channelHistoryRPC
	reads int
}

func (r *channelHistoryTipChangeRPC) GetBlockHash(int) (string, error) {
	r.reads++
	return fmt.Sprintf("%064x", r.reads), nil
}

func TestChannelDeanchorInputsRejectsWrongWithdrawalPair(t *testing.T) {
	p, _, _, channel, _ := channelHistoryFixture(t)
	parent := swire.NewMsgTx(2)
	parent.AddTxIn(swire.NewTxIn(&swire.OutPoint{Index: 0}, nil, nil))
	parent.AddTxOut(swire.NewTxOut(100, nil, []byte{0x51}))
	hash, err := sch.NewHashFromStr(parent.TxID())
	require.NoError(t, err)
	tx := swire.NewMsgTx(2)
	tx.AddTxIn(swire.NewTxIn(swire.NewOutPoint(hash, 0), nil, nil))
	marker, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DESCENDING, []byte("wrong-withdrawal"))
	require.NoError(t, err)
	tx.AddTxOut(swire.NewTxOut(30, nil, marker))
	_, _, err = p.ChannelDeanchorInputs(channel, "withdrawal", tx)
	require.ErrorContains(t, err, "does not match")
}

func TestChannelDeanchorAmountsRejectsDuplicateDescendingOutputs(t *testing.T) {
	tx := swire.NewMsgTx(2)
	for i := 0; i < 2; i++ {
		marker, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DESCENDING, []byte("withdrawal"))
		require.NoError(t, err)
		tx.AddTxOut(swire.NewTxOut(30, nil, marker))
	}
	_, err := ChannelDeanchorAmounts(tx, "withdrawal")
	require.ErrorContains(t, err, "duplicate descending")
}
