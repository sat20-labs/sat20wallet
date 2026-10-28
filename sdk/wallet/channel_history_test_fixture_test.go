package wallet

import (
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// channelHistoryRPC provides the small, explicit L1/L2 indexer view shared by
// channel history, recovery, and drain tests. It contains no quota behavior.
type channelHistoryRPC struct {
	IndexerRPCClient
	outputs    map[string]*TxOutput
	ledger     []*sindexer.ChannelLedgerEntry
	infos      map[string]*indexerwire.TxSimpleInfo
	ledgerErr  error
	syncHeight int
	raw        map[string]string
	summary    *indexerwire.AssetSummary
}

func (r *channelHistoryRPC) GetAssetSummaryWithAddress(string) *indexerwire.AssetSummary {
	return r.summary
}

func (r *channelHistoryRPC) GetRawTx(id string) (string, error) {
	if raw := r.raw[id]; raw != "" {
		return raw, nil
	}
	return "", fmt.Errorf("unknown raw transaction")
}

func (r *channelHistoryRPC) GetSyncHeight() int               { return r.syncHeight }
func (r *channelHistoryRPC) GetBestHeight() int64             { return 10 }
func (r *channelHistoryRPC) GetBlockHash(int) (string, error) { return fmt.Sprintf("%064x", 10), nil }
func (r *channelHistoryRPC) GetChannelLedger(string) ([]*sindexer.ChannelLedgerEntry, error) {
	return r.ledger, r.ledgerErr
}

func (r *channelHistoryRPC) GetChannelStateEvents(string) ([]*sindexer.ChannelStateEvent, error) {
	return nil, nil
}

func (r *channelHistoryRPC) GetUtxosWithAddress(string) (map[string]*wire.TxOut, error) {
	out := make(map[string]*wire.TxOut, len(r.outputs))
	for key, item := range r.outputs {
		out[key] = &item.OutValue
	}
	return out, nil
}

func (r *channelHistoryRPC) GetTxOutput(point string) (*TxOutput, error) {
	if out := r.outputs[point]; out != nil {
		return out, nil
	}
	return nil, fmt.Errorf("missing UTXO")
}

func (r *channelHistoryRPC) GetTxInfo(id string) (*indexerwire.TxSimpleInfo, error) {
	if info := r.infos[id]; info != nil {
		return info, nil
	}
	return nil, fmt.Errorf("-5: No such mempool or blockchain transaction. Use gettransaction for wallet transactions.")
}

func (r *channelHistoryRPC) GetExistingUtxos([]string) ([]string, error) { return nil, nil }

func channelHistoryFixture(t *testing.T) (*Manager, *channelHistoryRPC, *channelHistoryRPC, string, string) {
	t.Helper()
	// Use fixed, nonzero keys so invoices and channel IDs are deterministic.
	a, _ := btcec.PrivKeyFromBytes(bytesRepeat(21, 32))
	b, _ := btcec.PrivKeyFromBytes(bytesRepeat(22, 32))
	_, script, err := GetP2WSHscript(a.PubKey().SerializeCompressed(), b.PubKey().SerializeCompressed())
	require.NoError(t, err)
	channel, err := GetP2WSHaddressFromScript(script)
	require.NoError(t, err)
	root := wire.NewMsgTx(2)
	root.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{3}}, nil, nil))
	root.AddTxOut(wire.NewTxOut(90, script))
	raw, err := EncodeMsgTx(root)
	require.NoError(t, err)
	id := root.TxID()
	point := id + ":0"
	out := indexer.NewTxOutput(90)
	out.OutPointStr = point
	out.OutValue.PkScript = script
	l1 := &channelHistoryRPC{syncHeight: 10, raw: map[string]string{id: raw}, outputs: map[string]*TxOutput{point: out}, infos: map[string]*indexerwire.TxSimpleInfo{id: {Confirmations: 2, BlockHeight: 9}, "withdrawn": {Confirmations: 2, BlockHeight: 9}}}
	l2 := &channelHistoryRPC{syncHeight: 10, ledger: []*sindexer.ChannelLedgerEntry{
		{ChannelId: channel, Direction: "ascending", L2TxId: "anchor", L1Outpoints: []string{"old:0"}, L2Height: 1, Value: 100},
		{ChannelId: channel, Direction: "descending", L2TxId: "burn", L1TxId: "withdrawn", NullDataUtxo: "burn:0", L2Height: 2, Value: 30, ReturnedChannelOutputs: []string{point}},
	}}
	manager := &Manager{l1IndexerClient: &IndexerRPCClientMgr{active: l1}, l2IndexerClient: &IndexerRPCClientMgr{active: l2}}
	return manager, l1, l2, channel, point
}

func channelHistoryAnchor(t *testing.T, source string, value int64) *swire.MsgTx {
	t.Helper()
	a, _ := btcec.PrivKeyFromBytes(bytesRepeat(21, 32))
	b, _ := btcec.PrivKeyFromBytes(bytesRepeat(22, 32))
	witness, script, err := GetP2WSHscript(a.PubKey().SerializeCompressed(), b.PubKey().SerializeCompressed())
	require.NoError(t, err)
	invoice, err := sindexer.StandardAnchorScriptWithSig(source, witness, value, nil, nil)
	require.NoError(t, err)
	tx := swire.NewMsgTx(2)
	tx.AddTxIn(&swire.TxIn{PreviousOutPoint: swire.OutPoint{Index: swire.AnchorTxOutIndex}, SignatureScript: invoice})
	tx.AddTxOut(swire.NewTxOut(value, nil, script))
	return tx
}

func bytesRepeat(value byte, n int) []byte {
	result := make([]byte, n)
	for i := range result {
		result[i] = value
	}
	return result
}
