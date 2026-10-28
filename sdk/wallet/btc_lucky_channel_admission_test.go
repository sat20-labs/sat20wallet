package wallet

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	btcwire "github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	sch "github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func luckyChannelFixture(t *testing.T, lucky, marked bool) (*Manager, *Channel, *luckyRewardRPC, *luckyRewardRPC, *swire.MsgTx) {
	t.Helper()
	a, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{41}, 32))
	b, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{42}, 32))
	witness, script, err := GetP2WSHscript(a.PubKey().SerializeCompressed(), b.PubKey().SerializeCompressed())
	require.NoError(t, err)
	address, err := GetP2WSHaddressFromScript(script)
	require.NoError(t, err)
	source := btcwire.NewMsgTx(2)
	if lucky {
		source.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Index: 0xffffffff}, []byte("height1-satoshinet"), nil))
	} else {
		source.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Hash: chainhash.Hash{3}}, nil, nil))
	}
	source.AddTxOut(btcwire.NewTxOut(50_000, script))
	raw, err := EncodeMsgTx(source)
	require.NoError(t, err)
	point := source.TxID() + ":0"
	l1 := &luckyRewardRPC{raw: raw, confirmations: 1, output: indexer.GenerateTxOutput(source, 0)}
	invoice, err := sindexer.StandardAnchorScriptWithSig(point, witness, 50_000, nil, nil)
	require.NoError(t, err)
	anchor := swire.NewMsgTx(2)
	anchor.AddTxIn(&swire.TxIn{PreviousOutPoint: swire.OutPoint{Index: swire.AnchorTxOutIndex}, SignatureScript: invoice})
	anchor.AddTxOut(swire.NewTxOut(50_000, nil, script))
	metadata := &sindexer.BTCLuckyMetadata{Version: 1, BlockHeight: 1, BlockHash: sch.Hash{1}}
	if marked {
		marker, err := metadata.Script()
		require.NoError(t, err)
		anchor.AddTxOut(swire.NewTxOut(0, nil, marker))
	}
	anchorRaw, err := EncodeMsgTx_SatsNet(anchor)
	require.NoError(t, err)
	output := l1.output.Clone()
	output.OutPointStr = anchor.TxID() + ":0"
	l2 := &luckyRewardRPC{rawByID: map[string]string{anchor.TxID(): anchorRaw}, outputs: map[string]*TxOutput{output.OutPointStr: output},
		ascend: &sindexer.AscendData{FundingUtxo: point, AnchorTxId: anchor.TxID(), Address: address, Value: 50_000, Height: 1}}
	if marked {
		l2.ascend.BTCLucky = metadata
	}
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.ChannelId, channel.Address, channel.RedeemScript = address, address, witness
	channel.ChanPoint = indexer.NewTxOutput(1_000_000)
	channel.ChanPoint.OutPointStr = fmt.Sprintf("%064x:0", 9)
	channel.ChanPoint.OutValue.PkScript = script
	channel.Capacity, channel.IsInitiator = 1_000_000, true
	channel.LocalCommitment, channel.RemoteCommitment = NewChannelCommitment(), NewChannelCommitment()
	channel.SetCommitLocalValue(&PLAIN_ASSET, indexer.NewDefaultDecimal(100_000))
	channel.SetCommitRemoteValue(&PLAIN_ASSET, indexer.NewDefaultDecimal(900_000))
	channel.LocalChanCfg.InitialBalance, channel.RemoteChanCfg.InitialBalance = 10_000, 40_000
	channel.FeeCfg.MortgageFee = 0
	p := &Manager{channelMap: map[string]*Channel{address: channel},
		l1IndexerClient: &IndexerRPCClientMgr{active: l1}, l2IndexerClient: &IndexerRPCClientMgr{active: l2}}
	return p, channel, l1, l2, anchor
}

func TestBTCLuckyChannelAdmission(t *testing.T) {
	for _, marked := range []bool{true, false} {
		for _, confirmations := range []uint64{1, 100} {
			t.Run(fmt.Sprintf("marked=%t/confirmations=%d", marked, confirmations), func(t *testing.T) {
				p, channel, l1, l2, anchor := luckyChannelFixture(t, true, marked)
				l1.confirmations = confirmations
				point := l1.output.OutPointStr
				newSplicing := func() *SplicingReservation {
					return &SplicingReservation{SplicingDataInDB: SplicingDataInDB{AssetName: &PLAIN_ASSET}, RevocationInfo: RevocationInfo{Channel: channel}}
				}
				t.Run("L1 expansion", func(t *testing.T) {
					resv := newSplicing()
					require.ErrorContains(t, p.AllowExpand(resv, point), "btc-lucky-claim")
					require.False(t, resv.RecoverAscended)
					require.Empty(t, resv.SplicingInputs)
				})
				t.Run("L1 unascended expansion", func(t *testing.T) {
					ascend := l2.ascend
					l2.ascend = nil
					defer func() { l2.ascend = ascend }()
					resv := newSplicing()
					require.ErrorContains(t, p.AllowExpand(resv, point), "btc-lucky-claim")
					require.Empty(t, resv.SplicingInputs)
				})
				t.Run("L2 direct and mixed admission", func(t *testing.T) {
					ordinary := swire.NewMsgTx(2)
					ordinary.AddTxIn(swire.NewTxIn(&swire.OutPoint{Hash: sch.Hash{7}}, nil, nil))
					ordinary.AddTxOut(swire.NewTxOut(1000, nil, channel.GetChannelPkScript()))
					raw, err := EncodeMsgTx_SatsNet(ordinary)
					require.NoError(t, err)
					ordinaryPoint := ordinary.TxID() + ":0"
					l2.rawByID[ordinary.TxID()] = raw
					output := indexer.NewTxOutput(1000)
					output.OutPointStr, output.OutValue.PkScript = ordinaryPoint, channel.GetChannelPkScript()
					l2.outputs[ordinaryPoint] = output
					for _, points := range [][]string{{anchor.TxID() + ":0"}, {ordinaryPoint, anchor.TxID() + ":0"}} {
						payment := &PaymentReservation{PaymentDataInDB: PaymentDataInDB{AssetName: &PLAIN_ASSET}, RevocationInfo: RevocationInfo{Channel: channel}}
						require.ErrorContains(t, p.AllowExpandSatsNet(payment, points), "btc-lucky-claim")
						require.Empty(t, payment.Utxos)
						require.Nil(t, payment.Amt)
					}
				})
				t.Run("recovery invoice signer", func(t *testing.T) {
					candidate := anchor.Copy()
					candidate.TxOut = candidate.TxOut[:1] // Ordinary recovery constructs a new, unmarked invoice.
					called := false
					_, err := p.SignRecoveredChannelAnchorTx(candidate, func([]byte) ([]byte, error) {
						called = true
						return nil, fmt.Errorf("signature escaped validation")
					})
					require.ErrorContains(t, err, "btc-lucky-claim")
					require.False(t, called)
				})
				t.Run("restored splicing output", func(t *testing.T) {
					resv := newSplicing()
					resv.RecoverAscended, resv.RecoveredAnchorTxId = true, anchor.TxID()
					require.ErrorContains(t, p.AddRecoveredAscendedOutput(resv), "btc-lucky-claim")
					require.Empty(t, channel.UtxosL2)
				})
				t.Run("restored opening output", func(t *testing.T) {
					require.ErrorContains(t, p.AddExistingOpeningAnchorOutput(&FundingReservation{Channel: channel}), "can't find existing L2 opening anchor output")
					require.Empty(t, channel.UtxosL2)
				})
			})
		}
	}
}

func TestBTCLuckyChannelAdmissionOrdinaryOutputs(t *testing.T) {
	p, channel, l1, l2, anchor := luckyChannelFixture(t, false, false)
	ascend := l2.ascend
	l2.ascend = nil
	resv := &SplicingReservation{SplicingDataInDB: SplicingDataInDB{AssetName: &PLAIN_ASSET}, RevocationInfo: RevocationInfo{Channel: channel}}
	require.NoError(t, p.AllowExpand(resv, l1.output.OutPointStr))
	require.False(t, resv.RecoverAscended)
	l2.ascend = ascend
	payment := &PaymentReservation{PaymentDataInDB: PaymentDataInDB{AssetName: &PLAIN_ASSET}, RevocationInfo: RevocationInfo{Channel: channel}}
	require.NoError(t, p.AllowExpandSatsNet(payment, []string{anchor.TxID() + ":0"}))
	require.Equal(t, "50000", payment.Amt.String())
	require.Len(t, payment.Utxos, 1)
	_, err := p.SignRecoveredChannelAnchorTx(anchor.Copy(), func(invoice []byte) ([]byte, error) {
		key, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{41}, 32))
		return ecdsa.Sign(key, chainhash.HashB(invoice)).Serialize(), nil
	})
	require.NoError(t, err)
	require.NoError(t, p.AddExistingOpeningAnchorOutput(&FundingReservation{Channel: channel}))
	require.Contains(t, channel.UtxosL2, anchor.TxID()+":0")
}

func TestOpeningAnchorRebuildSkipsLuckyCandidateForOrdinaryCandidate(t *testing.T) {
	p, channel, _, l2, _ := luckyChannelFixture(t, true, true)
	target := expectedOpeningAnchorOutputValue(channel)

	lucky := swire.NewMsgTx(2)
	lucky.AddTxIn(&swire.TxIn{PreviousOutPoint: swire.OutPoint{Index: swire.AnchorTxOutIndex}})
	lucky.AddTxOut(swire.NewTxOut(target, nil, channel.GetChannelPkScript()))
	marker, err := (sindexer.BTCLuckyMetadata{Version: 1, BlockHeight: 1, BlockHash: sch.Hash{1}}).Script()
	require.NoError(t, err)
	lucky.AddTxOut(swire.NewTxOut(0, nil, marker))
	luckyRaw, err := EncodeMsgTx_SatsNet(lucky)
	require.NoError(t, err)
	l2.rawByID[lucky.TxID()] = luckyRaw
	luckyPoint := lucky.TxID() + ":0"
	luckyOutput := indexer.NewTxOutput(target)
	luckyOutput.OutPointStr, luckyOutput.OutValue.PkScript = luckyPoint, channel.GetChannelPkScript()
	l2.outputs[luckyPoint] = luckyOutput

	ordinary := swire.NewMsgTx(2)
	ordinary.AddTxIn(swire.NewTxIn(&swire.OutPoint{Hash: sch.Hash{8}}, nil, nil))
	ordinary.AddTxOut(swire.NewTxOut(target, nil, channel.GetChannelPkScript()))
	ordinaryRaw, err := EncodeMsgTx_SatsNet(ordinary)
	require.NoError(t, err)
	l2.rawByID[ordinary.TxID()] = ordinaryRaw
	ordinaryPoint := ordinary.TxID() + ":0"
	ordinaryOutput := indexer.NewTxOutput(target)
	ordinaryOutput.OutPointStr, ordinaryOutput.OutValue.PkScript = ordinaryPoint, channel.GetChannelPkScript()
	l2.outputs[ordinaryPoint] = ordinaryOutput
	l2.ledger = []*sindexer.ChannelLedgerEntry{
		{ChannelId: channel.ChannelId, Direction: "ascending", L2TxId: lucky.TxID(), L2Height: 1, Value: target},
		{ChannelId: channel.ChannelId, Direction: "ascending", L2TxId: ordinary.TxID(), L2Height: 2, Value: target},
	}

	require.NoError(t, p.AddExistingOpeningAnchorOutput(&FundingReservation{Channel: channel}))
	require.Contains(t, channel.UtxosL2, ordinaryPoint)
	require.NotContains(t, channel.UtxosL2, luckyPoint)
}

func TestBTCLuckyChannelAdmissionOrdinaryAscendedRecovery(t *testing.T) {
	for _, test := range []struct {
		name string
		err  string
	}{
		{name: "valid"},
		{name: "funding mismatch", err: "ascend funding utxo mismatch"},
		{name: "channel mismatch", err: "ascend channel mismatch"},
		{name: "output script mismatch", err: "is not in channel address"},
		{name: "amount mismatch", err: "recovered anchor amount mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, channel, l1, l2, anchor := luckyChannelFixture(t, false, false)
			point := anchor.TxID() + ":0"
			switch test.name {
			case "funding mismatch":
				l2.ascend.FundingUtxo = "another:0"
			case "channel mismatch":
				l2.ascend.Address = "another channel"
			case "output script mismatch":
				l2.outputs[point].OutValue.PkScript = []byte{0x51}
			case "amount mismatch":
				l2.outputs[point].OutValue.Value--
			}
			resv := &SplicingReservation{SplicingDataInDB: SplicingDataInDB{AssetName: &PLAIN_ASSET}, RevocationInfo: RevocationInfo{Channel: channel}}
			err := p.AllowExpand(resv, l1.output.OutPointStr)
			if test.err != "" {
				require.ErrorContains(t, err, test.err)
				require.False(t, resv.RecoverAscended)
				require.Empty(t, resv.RecoveredAnchorOutpoint)
				require.Empty(t, channel.UtxosL2)
				return
			}
			require.NoError(t, err)
			require.True(t, resv.RecoverAscended)
			require.Equal(t, anchor.TxID(), resv.RecoveredAnchorTxId)
			require.Equal(t, point, resv.RecoveredAnchorOutpoint)
			require.Equal(t, "50000", resv.SplicingAmt.String())
			require.NoError(t, p.AddRecoveredAscendedOutput(resv))
			require.Len(t, channel.UtxosL2, 1)
			require.Equal(t, int64(50_000), channel.UtxosL2[point].Value())
			require.Equal(t, channel.GetChannelPkScript(), channel.UtxosL2[point].OutValue.PkScript)
			require.ErrorContains(t, p.AddRecoveredAscendedOutput(resv), "has been in channel")
			require.Len(t, channel.UtxosL2, 1)
		})
	}
}

func TestBTCLuckyChannelAdmissionRejectsUnreadableSources(t *testing.T) {
	p, channel, l1, l2, anchor := luckyChannelFixture(t, false, false)
	l1.rawErr = fmt.Errorf("L1 source unavailable")
	resv := &SplicingReservation{SplicingDataInDB: SplicingDataInDB{AssetName: &PLAIN_ASSET}, RevocationInfo: RevocationInfo{Channel: channel}}
	require.ErrorContains(t, p.AllowExpand(resv, l1.output.OutPointStr), "L1 source unavailable")
	l2.rawErr = fmt.Errorf("L2 source unavailable")
	payment := &PaymentReservation{PaymentDataInDB: PaymentDataInDB{AssetName: &PLAIN_ASSET}, RevocationInfo: RevocationInfo{Channel: channel}}
	require.ErrorContains(t, p.AllowExpandSatsNet(payment, []string{anchor.TxID() + ":0"}), "L2 source unavailable")
	require.Empty(t, payment.Utxos)
}

func TestBTCLuckyChannelAdmissionRejectsMarkedRecovery(t *testing.T) {
	p, channel, l1, l2, _ := luckyChannelFixture(t, true, true)
	resv := &SplicingReservation{SplicingDataInDB: SplicingDataInDB{AssetName: &PLAIN_ASSET}, RevocationInfo: RevocationInfo{Channel: channel}}
	require.ErrorContains(t, p.markRecoverAscended(resv, l2.ascend, l1.output.OutPointStr), "btc-lucky-claim")
	require.False(t, resv.RecoverAscended)
	require.Empty(t, resv.RecoveredAnchorTxId)
}

func TestBTCLuckyMissingMarkerValidationDoesNotRelockChannel(t *testing.T) {
	p, channel, _, _, anchor := luckyChannelFixture(t, true, false)
	channel.Mutex.Lock()
	result := make(chan error, 1)
	go func() {
		_, err := p.SignChannelAnchorTxWithFunding(anchor, nil, nil, func([]byte) ([]byte, error) {
			return nil, fmt.Errorf("signer must not be called")
		})
		result <- err
	}()
	select {
	case err := <-result:
		channel.Mutex.Unlock()
		require.ErrorContains(t, err, "missing its marker")
	case <-time.After(time.Second):
		// Release the lock so the failing implementation can exit cleanly.
		channel.Mutex.Unlock()
		select {
		case <-result:
		case <-time.After(time.Second):
			t.Fatal("source validation did not finish after releasing the channel lock")
		}
		t.Fatal("source validation attempted to reacquire the held channel lock")
	}
}
