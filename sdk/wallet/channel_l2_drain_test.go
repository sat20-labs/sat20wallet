package wallet

import (
	"fmt"
	"testing"

	btcchainhash "github.com/btcsuite/btcd/chaincfg/chainhash"
	btcwire "github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	dbcodec "github.com/sat20-labs/indexer/indexer/db"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	sch "github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type drainRPC struct {
	*channelHistoryRPC
	events     []*sindexer.ChannelStateEvent
	eventsErr  error
	broadcasts int
}

func (r *drainRPC) BroadCastTx_SatsNet(tx *swire.MsgTx) (string, error) {
	r.broadcasts++
	return tx.TxID(), nil
}
func (r *drainRPC) IsCoreNode([]byte) (bool, error) { return false, nil }
func (r *drainRPC) Ping() error                     { return nil }
func (r *drainRPC) GetChannelStateEvents(string) ([]*sindexer.ChannelStateEvent, error) {
	return r.events, r.eventsErr
}
func (r *drainRPC) GetAllUtxosWithAddress(string) []*indexerwire.TxOutputInfo {
	var out []*indexerwire.TxOutputInfo
	for point, u := range r.outputs {
		out = append(out, &indexerwire.TxOutputInfo{OutPoint: point, Value: u.Value(), PkScript: u.OutValue.PkScript})
	}
	return out
}
func drainFixture(t *testing.T) (*Manager, *drainRPC, string) {
	p, _, l2, channel, _ := channelHistoryFixture(t)
	p.db = newMemoryKVDB()
	l2.ledger = nil
	l2.summary = &indexerwire.AssetSummary{Data: []*indexer.AssetInfo{{Name: indexer.ASSET_PLAIN_SAT, Amount: *indexer.NewDefaultDecimal(30)}}}
	script, err := GetPkScriptFromAddress(channel)
	require.NoError(t, err)
	u := indexer.NewTxOutput(30)
	u.OutPointStr = fmt.Sprintf("%064x:0", 42)
	u.OutValue.PkScript = script
	l2.outputs = map[string]*TxOutput{u.OutPointStr: u}
	rpc := &drainRPC{channelHistoryRPC: l2}
	p.l2IndexerClient = &IndexerRPCClientMgr{active: rpc}
	p.utxoLockerL2 = NewUtxoLocker(p.db, rpc, L2_NETWORK_SATOSHI)
	p.utxoLockerL2.Init()
	return p, rpc, channel
}
func TestChannelL2DrainFirstOpenPreservesAssets(t *testing.T) {
	p, rpc, channel := drainFixture(t)
	require.NoError(t, p.EnsureChannelL2DrainedBeforeOpen(channel, ""))
	id, err := p.DrainChannelL2BeforeOpenIfNeeded(channel)
	require.NoError(t, err)
	require.Empty(t, id)
	require.Len(t, rpc.outputs, 1)
}
func TestChannelL2DrainNoCloseEvidence(t *testing.T) {
	p, _, channel := drainFixture(t)
	_, _, _, err := p.buildChannelL2DrainDeAnchorTx(channel)
	require.Error(t, err, "must not burn an address balance without closing evidence")
}

func TestChannelL2FirstOpeningIgnoresLuckyStandaloneAscend(t *testing.T) {
	p, channel, _, l2, luckyAnchor := luckyChannelFixture(t, true, true)
	database := dbcodec.NewKVDB(t.TempDir())
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	p.db = database
	l2.ledger = []*sindexer.ChannelLedgerEntry{{ChannelId: channel.ChannelId, Direction: "ascending", L2TxId: luckyAnchor.TxID(), L1Outpoints: []string{l2.ascend.FundingUtxo}, L2Height: 1, Value: 50_000}}

	first, err := p.channelL2FirstOpening(channel.ChannelId)
	require.NoError(t, err)
	require.True(t, first)
	require.NoError(t, p.EnsureChannelL2DrainedBeforeOpen(channel.ChannelId, ""))
	txid, err := p.DrainChannelL2BeforeOpenIfNeeded(channel.ChannelId)
	require.NoError(t, err)
	require.Empty(t, txid)
	require.Contains(t, l2.outputs, luckyAnchor.TxID()+":0", "first opening must leave the independently ascended reward untouched")

	// Lucky history alone may be ignored; an ordinary unowned anchor still
	// blocks opening without local recovery material.
	ordinary := swire.NewMsgTx(2)
	ordinary.AddTxIn(swire.NewTxIn(&swire.OutPoint{Hash: sch.Hash{8}}, nil, nil))
	ordinary.AddTxOut(swire.NewTxOut(1_000, nil, channel.GetChannelPkScript()))
	raw, err := EncodeMsgTx_SatsNet(ordinary)
	require.NoError(t, err)
	l2.rawByID[ordinary.TxID()] = raw
	l2.ledger = append(l2.ledger, &sindexer.ChannelLedgerEntry{ChannelId: channel.ChannelId, Direction: "ascending", L2TxId: ordinary.TxID(), L2Height: 2, Value: 1_000})
	first, err = p.channelL2FirstOpening(channel.ChannelId)
	require.ErrorContains(t, err, "channel history exists without local recovery material")
	require.False(t, first)
}
func TestChannelL2DrainFirstOpenReadFailure(t *testing.T) {
	p, rpc, channel := drainFixture(t)
	rpc.eventsErr = fmt.Errorf("event service unavailable")
	require.Error(t, p.EnsureChannelL2DrainedBeforeOpen(channel, ""))
}

func closedDrainFixture(t *testing.T) (*Manager, *Channel, *channelHistoryRPC, *drainRPC) {
	t.Helper()
	p, c, _, _ := closedOpenDispatchFixture(t)
	script, err := GetPkScriptFromAddress(c.ChannelId)
	require.NoError(t, err)
	old := indexer.NewTxOutput(30)
	old.OutPointStr = fmt.Sprintf("%064x:0", 42)
	old.OutValue.PkScript = script
	extra := indexer.NewTxOutput(70)
	extra.OutPointStr = fmt.Sprintf("%064x:0", 43)
	extra.OutValue.PkScript = script
	parent := c.RemoteCommitment.CommitTx
	id := parent.TxID()
	raw, err := EncodeMsgTx(parent)
	require.NoError(t, err)
	l1 := &channelHistoryRPC{syncHeight: 10, raw: map[string]string{id: raw}, infos: map[string]*indexerwire.TxSimpleInfo{id: {Confirmations: 2, BlockHeight: 9}}}
	p.l1IndexerClient = &IndexerRPCClientMgr{active: l1}
	l2 := &drainRPC{channelHistoryRPC: &channelHistoryRPC{syncHeight: 10, outputs: map[string]*TxOutput{old.OutPointStr: old, extra.OutPointStr: extra}, summary: &indexerwire.AssetSummary{Data: []*indexer.AssetInfo{{Name: indexer.ASSET_PLAIN_SAT, Amount: *indexer.NewDefaultDecimal(100)}}}}}
	p.l2IndexerClient = &IndexerRPCClientMgr{active: l2}
	p.utxoLockerL2 = NewUtxoLocker(p.db, l2, L2_NETWORK_SATOSHI)
	p.utxoLockerL2.Init()
	c.UtxosL2[old.OutPointStr] = OutputToSatsNet(old)
	burn, _, err := CreateClosingDeAnchorTx(c, id, nil)
	require.NoError(t, err)
	forceClosePayload, err := sindexer.EncodeDescendPayloadV2(id, sindexer.DESCEND_OP_FORCE_CLOSE, nil)
	require.NoError(t, err)
	forceCloseScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DESCENDING, forceClosePayload)
	require.NoError(t, err)
	burn.TxOut[0].PkScript = forceCloseScript
	c.RemoteCommitment.DeAnchorTx = burn.Copy()
	c.ClosingTx = parent.Copy()
	c.DeAnchorTx = burn
	require.NoError(t, p.SaveChannelToDB(c))
	return p, c, l1, l2
}

func closedDrainSweep(t *testing.T, parent *btcwire.MsgTx, l1 *channelHistoryRPC) *btcwire.MsgTx {
	t.Helper()
	parentHash, err := btcchainhash.NewHashFromStr(parent.TxID())
	require.NoError(t, err)
	sweep := btcwire.NewMsgTx(2)
	sweep.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Hash: *parentHash, Index: 0}, nil, nil))
	sweep.AddTxOut(btcwire.NewTxOut(parent.TxOut[0].Value, []byte{0x51}))
	raw, err := EncodeMsgTx(sweep)
	require.NoError(t, err)
	if l1.raw == nil {
		l1.raw = make(map[string]string)
	}
	if l1.infos == nil {
		l1.infos = make(map[string]*indexerwire.TxSimpleInfo)
	}
	l1.raw[sweep.TxID()] = raw
	l1.infos[sweep.TxID()] = &indexerwire.TxSimpleInfo{Confirmations: 2, BlockHeight: 9}
	return sweep
}

func TestChannelL2DrainConfirmedClosePreservesNewAssets(t *testing.T) {
	p, c, _, rpc := closedDrainFixture(t)
	tx, _, points, err := p.buildChannelL2DrainDeAnchorTx(c.ChannelId)
	require.NoError(t, err)
	require.NotNil(t, tx)
	require.Equal(t, []string{fmt.Sprintf("%064x:0", 42)}, points)
	require.EqualValues(t, 30, tx.TxOut[0].Value)
	require.NoError(t, p.ValidateChannelL2DrainEvidence(c.ChannelId, tx))
	require.NoError(t, p.EnsureChannelL2DrainedBeforeOpen(c.ChannelId, tx.TxID()))
	// Simulate the old input being spent: the later transfer must not trigger another drain.
	delete(rpc.outputs, points[0])
	tx, _, _, err = p.buildChannelL2DrainDeAnchorTx(c.ChannelId)
	require.NoError(t, err)
	require.Nil(t, tx)
	require.NoError(t, p.EnsureChannelL2DrainedBeforeOpen(c.ChannelId, ""))
	require.Len(t, rpc.outputs, 1)
}

func TestChannelL2DrainUsesStoredCommitmentAfterClosingSweep(t *testing.T) {
	t.Run("confirmed original commitment", func(t *testing.T) {
		p, c, l1, rpc := closedDrainFixture(t)
		parentID := c.RemoteCommitment.CommitTx.TxID()
		c.ClosingTx = closedDrainSweep(t, c.RemoteCommitment.CommitTx, l1)
		c.DeAnchorTx = c.RemoteCommitment.DeAnchorTx.Copy()
		require.NoError(t, p.SaveChannelToDB(c))

		tx, _, points, err := p.buildChannelL2DrainDeAnchorTx(c.ChannelId)
		require.NoError(t, err)
		require.NotNil(t, tx)
		require.Equal(t, []string{fmt.Sprintf("%064x:0", 42)}, points)
		require.EqualValues(t, 30, tx.TxOut[0].Value)
		require.NoError(t, p.ValidateChannelL2DrainEvidence(c.ChannelId, tx))
		kind, data, err := sindexer.ReadDataFromNullDataScript(tx.TxOut[0].PkScript)
		require.NoError(t, err)
		require.EqualValues(t, sindexer.CONTENT_TYPE_DESCENDING, kind)
		payload, err := sindexer.ParseDescendPayload(data)
		require.NoError(t, err)
		require.Equal(t, parentID, payload.L1TxId)
		require.Equal(t, sindexer.DESCEND_OP_FORCE_CLOSE, payload.Operation)
		require.Contains(t, rpc.outputs, fmt.Sprintf("%064x:0", 43), "the bystander L2 asset must remain untouched")
	})

	t.Run("original commitment is unconfirmed", func(t *testing.T) {
		p, c, l1, _ := closedDrainFixture(t)
		parentID := c.RemoteCommitment.CommitTx.TxID()
		c.ClosingTx = closedDrainSweep(t, c.RemoteCommitment.CommitTx, l1)
		c.DeAnchorTx = c.RemoteCommitment.DeAnchorTx.Copy()
		l1.infos[parentID].Confirmations = 0
		require.NoError(t, p.SaveChannelToDB(c))

		_, _, _, err := p.buildChannelL2DrainDeAnchorTx(c.ChannelId)
		require.ErrorContains(t, err, "confirmed closing transaction and matching L2 inputs required")
	})

	t.Run("original burn is bound to another transaction", func(t *testing.T) {
		p, c, l1, _ := closedDrainFixture(t)
		c.ClosingTx = closedDrainSweep(t, c.RemoteCommitment.CommitTx, l1)
		wrongID := fmt.Sprintf("%064x", 999)
		wrongBurn, _, err := CreateClosingDeAnchorTx(c, wrongID, nil)
		require.NoError(t, err)
		wrongPayload, err := sindexer.EncodeDescendPayloadV2(wrongID, sindexer.DESCEND_OP_FORCE_CLOSE, nil)
		require.NoError(t, err)
		wrongScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DESCENDING, wrongPayload)
		require.NoError(t, err)
		wrongBurn.TxOut[0].PkScript = wrongScript
		c.RemoteCommitment.DeAnchorTx = wrongBurn.Copy()
		c.DeAnchorTx = wrongBurn
		require.NoError(t, p.SaveChannelToDB(c))

		_, _, _, err = p.buildChannelL2DrainDeAnchorTx(c.ChannelId)
		require.ErrorContains(t, err, "confirmed closing transaction and matching L2 inputs required")
	})
}

func TestChannelL2DrainRejectsUnconfirmedAndUnrelatedInputs(t *testing.T) {
	p, c, l1, _ := closedDrainFixture(t)
	tx, _, _, err := p.buildChannelL2DrainDeAnchorTx(c.ChannelId)
	require.NoError(t, err)
	id := c.ClosingTx.TxID()
	l1.infos[id].Confirmations = 0
	require.Error(t, p.ValidateChannelL2DrainEvidence(c.ChannelId, tx))
	_, _, _, err = p.buildChannelL2DrainDeAnchorTx(c.ChannelId)
	require.Error(t, err)
	l1.infos[id].Confirmations = 2
	tx.TxIn[0].PreviousOutPoint.Index = 1
	require.ErrorContains(t, p.ValidateChannelL2DrainEvidence(c.ChannelId, tx), "outside closing material")
}

func TestChannelL2DrainSignedRecoveryRechecksClosing(t *testing.T) {
	p, c, l1, l2 := closedDrainFixture(t)
	tx, _, _, err := p.buildChannelL2DrainDeAnchorTx(c.ChannelId)
	require.NoError(t, err)
	owner, intent, err := p.closedReopenIntentForWallet(p.wallet, c.ChannelId)
	require.NoError(t, err)
	require.NoError(t, p.ValidateChannelL2DrainEvidence(c.ChannelId, tx), "the recovery owner must not reject its own drain")
	raw, err := EncodeMsgTx_SatsNet(tx)
	require.NoError(t, err)
	intent.Step = "drain_signed"
	intent.DrainSnapshot = []byte(raw)
	// The close was reorged out after signatures were obtained.
	l1.infos[c.ClosingTx.TxID()].Confirmations = 0
	_, err = p.drainClosedChannelBeforeReopen(p.wallet, p.serverNode.client, owner, intent)
	require.Error(t, err)
	require.Equal(t, "drain_signed", intent.Step, "must stop before any broadcast dispatch")
	require.Zero(t, l2.broadcasts)
	l1.infos[c.ClosingTx.TxID()].Confirmations = 2
	id, err := p.drainClosedChannelBeforeReopen(p.wallet, p.serverNode.client, owner, intent)
	require.NoError(t, err)
	require.Equal(t, tx.TxID(), id)
	require.Equal(t, 1, l2.broadcasts)
}

type drainReorgRPC struct {
	*channelHistoryRPC
	reads int
}

func (r *drainReorgRPC) GetBlockHash(int) (string, error) {
	r.reads++
	return fmt.Sprintf("%064x", r.reads), nil
}
func TestChannelL2DrainRejectsTipChange(t *testing.T) {
	p, c, l1, _ := closedDrainFixture(t)
	p.l1IndexerClient = &IndexerRPCClientMgr{active: &drainReorgRPC{channelHistoryRPC: l1}}
	_, _, _, err := p.buildChannelL2DrainDeAnchorTx(c.ChannelId)
	require.ErrorContains(t, err, "chain changed")
}

func TestChannelL2DrainRejectsUnboundOrdinaryHistory(t *testing.T) {
	p, rpc, channel := drainFixture(t)
	script, err := GetPkScriptFromAddress(channel)
	require.NoError(t, err)
	ordinary := swire.NewMsgTx(2)
	ordinary.AddTxIn(swire.NewTxIn(&swire.OutPoint{Hash: sch.Hash{9}}, nil, nil))
	ordinary.AddTxOut(swire.NewTxOut(1_000, nil, script))
	raw, err := EncodeMsgTx_SatsNet(ordinary)
	require.NoError(t, err)
	if rpc.raw == nil {
		rpc.raw = make(map[string]string)
	}
	rpc.raw[ordinary.TxID()] = raw
	rpc.ledger = []*sindexer.ChannelLedgerEntry{{ChannelId: channel, Direction: "ascending", L2TxId: ordinary.TxID()}}
	require.ErrorContains(t, p.EnsureChannelL2DrainedBeforeOpen(channel, ""), "recovery material")
	require.Zero(t, rpc.broadcasts)
	p, c, _, _ := closedDrainFixture(t)
	tx, _, _, err := p.buildChannelL2DrainDeAnchorTx(c.ChannelId)
	require.NoError(t, err)
	payload, err := sindexer.EncodeDescendPayloadV2(zeroL1TxId, sindexer.DESCEND_OP_FORCE_CLOSE, nil)
	require.NoError(t, err)
	tx.TxOut[0].PkScript, err = sindexer.NullDataScript(sindexer.CONTENT_TYPE_DESCENDING, payload)
	require.NoError(t, err)
	require.ErrorContains(t, p.ValidateChannelL2DrainEvidence(c.ChannelId, tx), "confirmed closing")
}
