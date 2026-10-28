package wallet

import (
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	sch "github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

const (
	unstakeLuckyValue   = int64(50_000)
	unstakeClaimValue   = int64(12_000)
	unstakeOrdinaryL1   = int64(7_000)
	unstakeStakeBalance = int64(100)
)

type unstakeLuckyFixture struct {
	manager      *Manager
	channel      *Channel
	l1, l2       *channelHistoryRPC
	stakeAsset   *AssetName
	luckyL1Point string
	luckyL1TxID  string
	luckyL2Point string
	claimPoint   string
	claimTxID    string
}

func newUnstakeLuckyFixture(t *testing.T, ascended bool) *unstakeLuckyFixture {
	t.Helper()
	manager, channel, luckyL1RPC, luckyL2RPC, anchor := luckyChannelFixture(t, true, true)
	channelScript := channel.GetChannelPkScript()
	brc20 := indexer.AssetName{Protocol: "brc20", Type: "f", Ticker: "ordi"}
	fixture := &unstakeLuckyFixture{
		manager:    manager,
		channel:    channel,
		stakeAsset: &AssetName{AssetName: brc20, N: 1},
	}

	fixture.l1 = &channelHistoryRPC{
		syncHeight: 10,
		outputs:    make(map[string]*TxOutput),
		raw:        make(map[string]string),
		infos:      make(map[string]*indexerwire.TxSimpleInfo),
	}
	fixture.luckyL1Point = luckyL1RPC.output.OutPointStr
	fixture.luckyL1TxID = strings.SplitN(fixture.luckyL1Point, ":", 2)[0]
	luckySource := luckyL1RPC.output.Clone()
	luckySource.OutPointStr = fixture.luckyL1Point
	fixture.l1.outputs[fixture.luckyL1Point] = luckySource
	fixture.l1.raw[fixture.luckyL1TxID] = luckyL1RPC.raw
	fixture.l1.infos[fixture.luckyL1TxID] = &indexerwire.TxSimpleInfo{Confirmations: 100, BlockHeight: 1}
	addExpansionTestOutput(t, fixture.l1, 71, channelScript, unstakeOrdinaryL1, nil, 0, false, false)
	fixture.l1.summary = &indexerwire.AssetSummary{Data: []*indexer.AssetInfo{
		unstakeAssetInfo(indexer.ASSET_PLAIN_SAT, unstakeLuckyValue+unstakeOrdinaryL1),
		unstakeAssetInfo(brc20, unstakeStakeBalance), // Account-based balance without a BRC20 UTXO.
	}}

	fixture.l2 = &channelHistoryRPC{
		syncHeight: 10,
		outputs:    make(map[string]*TxOutput),
		raw:        make(map[string]string),
	}
	fixture.luckyL2Point = anchor.TxID() + ":0"
	fixture.l2.outputs[fixture.luckyL2Point] = luckyL2RPC.outputs[fixture.luckyL2Point].Clone()
	fixture.l2.raw[anchor.TxID()] = luckyL2RPC.rawByID[anchor.TxID()]

	claim := swire.NewMsgTx(2)
	claim.AddTxIn(swire.NewTxIn(&swire.OutPoint{Hash: sch.Hash{72}}, nil, nil))
	claim.AddTxOut(swire.NewTxOut(unstakeClaimValue, nil, channelScript))
	claimRaw, err := EncodeMsgTx_SatsNet(claim)
	require.NoError(t, err)
	fixture.claimTxID = claim.TxID()
	fixture.claimPoint = fixture.claimTxID + ":0"
	claimOutput := indexer.NewTxOutput(unstakeClaimValue)
	claimOutput.OutPointStr = fixture.claimPoint
	claimOutput.OutValue.PkScript = channelScript
	fixture.l2.outputs[fixture.claimPoint] = claimOutput
	fixture.l2.raw[fixture.claimTxID] = claimRaw
	fixture.l2.summary = &indexerwire.AssetSummary{Data: []*indexer.AssetInfo{
		unstakeAssetInfo(indexer.ASSET_PLAIN_SAT, unstakeLuckyValue+unstakeClaimValue),
		unstakeAssetInfo(brc20, unstakeStakeBalance),
	}}
	if ascended {
		fixture.l2.ledger = []*sindexer.ChannelLedgerEntry{{
			ChannelId:   channel.ChannelId,
			Direction:   "ascending",
			L2TxId:      anchor.TxID(),
			L1Outpoints: []string{fixture.luckyL1Point},
			L2Height:    1,
			Value:       unstakeLuckyValue,
		}}
	}

	manager.l1IndexerClient = &IndexerRPCClientMgr{active: fixture.l1}
	manager.l2IndexerClient = &IndexerRPCClientMgr{active: fixture.l2}
	return fixture
}

func unstakeAssetInfo(name indexer.AssetName, amount int64) *indexer.AssetInfo {
	return &indexer.AssetInfo{Name: name, Amount: *indexer.NewDefaultDecimal(amount)}
}

func TestCheckUnstakeChannelAssetsExcludesLuckyL2Profit(t *testing.T) {
	fixture := newUnstakeLuckyFixture(t, true)
	value, err := fixture.manager.checkUnstakeChannelAssets(fixture.channel.ChannelId,
		fixture.stakeAsset, indexer.NewDefaultDecimal(50))
	require.NoError(t, err)
	require.EqualValues(t, unstakeClaimValue, value,
		"unstake may receive the ordinary post-claim BTC output, but not the unclaimed Lucky anchor")
}

func TestCheckUnstakeChannelAssetsRequiresOrdinaryL1Stake(t *testing.T) {
	fixture := newUnstakeLuckyFixture(t, true)
	_, err := fixture.manager.checkUnstakeChannelAssets(fixture.channel.ChannelId,
		&PLAIN_ASSET, indexer.NewDefaultDecimal(unstakeOrdinaryL1+1))
	require.ErrorContains(t, err, "insufficient on L1")
}

func TestCheckUnstakeChannelAssetsRequiresL2Stake(t *testing.T) {
	fixture := newUnstakeLuckyFixture(t, true)
	fixture.l2.summary.Data[1].Amount = *indexer.NewDefaultDecimal(unstakeStakeBalance - 1)
	_, err := fixture.manager.checkUnstakeChannelAssets(fixture.channel.ChannelId,
		fixture.stakeAsset, indexer.NewDefaultDecimal(unstakeStakeBalance))
	require.ErrorContains(t, err, "insufficient on L2")
}

func TestChannelAssetSummaryWithoutLuckyExcludesL1BackingAndKeepsAccountBalance(t *testing.T) {
	for _, test := range []struct {
		name     string
		ascended bool
	}{{name: "unascended"}, {name: "ascended", ascended: true}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newUnstakeLuckyFixture(t, test.ascended)
			l1SummaryBefore := fixture.l1.summary.Data[0].Amount.Clone()
			l2SummaryBefore := fixture.l2.summary.Data[0].Amount.Clone()
			assets, err := fixture.manager.channelAssetSummaryWithoutLucky(fixture.channel.ChannelId, false)
			require.NoError(t, err)
			require.EqualValues(t, unstakeOrdinaryL1, summaryAssetAmount(t, assets, indexer.ASSET_PLAIN_SAT))
			require.EqualValues(t, unstakeStakeBalance, summaryAssetAmount(t, assets, fixture.stakeAsset.AssetName),
				"the account-based BRC20 summary must remain even without a BRC20 UTXO")
			l2Assets, err := fixture.manager.channelAssetSummaryWithoutLucky(fixture.channel.ChannelId, true)
			require.NoError(t, err)
			require.EqualValues(t, unstakeClaimValue, summaryAssetAmount(t, l2Assets, indexer.ASSET_PLAIN_SAT))
			require.Equal(t, l1SummaryBefore, &fixture.l1.summary.Data[0].Amount,
				"L1 filtering must not mutate the indexer's shared summary object")
			require.Equal(t, l2SummaryBefore, &fixture.l2.summary.Data[0].Amount,
				"L2 filtering must not mutate the indexer's shared summary object")
			require.Len(t, fixture.l1.outputs, 2, "fixture has Lucky and ordinary plain UTXOs, but no BRC20 UTXO")
		})
	}
}

func TestBTCLuckyClaimOutputRemainsOrdinaryForUnstake(t *testing.T) {
	fixture := newUnstakeLuckyFixture(t, true)
	preExcluded := strings.Repeat("f", 64) + ":0"
	excluded := map[string]bool{preExcluded: true}
	require.NoError(t, fixture.manager.excludeBTCLuckyL2Outputs(fixture.channel.ChannelId, excluded))
	require.True(t, excluded[fixture.luckyL2Point], "the unclaimed Lucky anchor must be excluded from ordinary L2 selection")
	require.False(t, excluded[fixture.claimPoint], "the claim output is ordinary and remains spendable")
	require.True(t, excluded[preExcluded], "the caller's existing coin-selection exclusion must remain intact")

	profit, err := fixture.manager.channelAssetBalanceWithoutLucky(fixture.channel.ChannelId,
		&indexer.ASSET_PLAIN_SAT, true)
	require.NoError(t, err)
	require.EqualValues(t, unstakeClaimValue, profit.Int64())

	hash, err := sch.NewHashFromStr(fixture.claimTxID)
	require.NoError(t, err)
	spend := swire.NewMsgTx(2)
	spend.AddTxIn(swire.NewTxIn(&swire.OutPoint{Hash: *hash, Index: 0}, nil, nil))
	require.NoError(t, fixture.manager.RejectBTCLuckyInputs(spend, nil),
		"a markerless output created by claim is ordinary L2 funding")
}

func TestCheckUnstakeChannelAssetsRejectsLuckySourceReadFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		fail func(*unstakeLuckyFixture)
	}{{
		name: "L1 source",
		fail: func(f *unstakeLuckyFixture) { delete(f.l1.raw, f.luckyL1TxID) },
	}, {
		name: "L2 source",
		fail: func(f *unstakeLuckyFixture) { delete(f.l2.raw, strings.SplitN(f.luckyL2Point, ":", 2)[0]) },
	}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newUnstakeLuckyFixture(t, true)
			test.fail(fixture)
			_, err := fixture.manager.checkUnstakeChannelAssets(fixture.channel.ChannelId,
				fixture.stakeAsset, indexer.NewDefaultDecimal(50))
			require.ErrorContains(t, err, "unknown raw transaction")
		})
	}
}

func summaryAssetAmount(t *testing.T, assets []*indexer.AssetInfo, name indexer.AssetName) int64 {
	t.Helper()
	for _, asset := range assets {
		if asset != nil && asset.Name == name {
			return asset.Amount.Int64()
		}
	}
	t.Fatalf("summary missing asset %s", name.String())
	return 0
}
