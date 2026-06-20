package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

type SignedSweepTxPackage struct {
	SweepTx       *wire.MsgTx
	Txs           []*wire.MsgTx
	PrevFetcher   txscript.PrevOutputFetcher
	Fee           int64
	CommitTxId    string
	SweepTxId     string
	Signed        bool
	Verified      bool
	Broadcastable bool
}

func FindOutputIndexes(commitTx *wire.MsgTx, ourPkScript, theirPkScript []byte) ([]int, []int, error) {
	var ourIndex []int
	var theirIndex []int
	for i, txOut := range commitTx.TxOut {
		switch {
		case bytes.Equal(txOut.PkScript, ourPkScript):
			ourIndex = append(ourIndex, i)
		case bytes.Equal(txOut.PkScript, theirPkScript):
			theirIndex = append(theirIndex, i)
		}
	}

	Log.Infof("findOutputIndexes %d %d", ourIndex, theirIndex)
	if len(ourIndex) == 0 && len(theirIndex) == 0 {
		return nil, nil, fmt.Errorf("can't find any output index")
	}

	return ourIndex, theirIndex, nil
}

func (p *Manager) CreateAndVerifyPunishTx(channel *Channel, rev []byte, feeRate int64) (*wire.MsgTx, error) {
	// punish tx 花费的是对端已经撤销的 remote commitment 的 delayed output。
	// rev 是对端刚释放的旧 commitment secret，本地用它派生 revocation 私钥。
	bootstrapKey := p.GetBootstrapNodePaymentPubKey()
	commitsecret, commitpoint := btcec.PrivKeyFromBytes(rev)
	keyRing := DeriveCommitmentKeys(commitpoint, 1, bootstrapKey, nil, channel)

	revPrivKey := channel.LocalWallet().DeriveRevocationPrivKey(commitsecret)
	commitTx := channel.RemoteCommitment.CommitTx
	serverCommit := channel.IsInitiator

	toLocalScript, toRemoteScript, err := GenerateChannelScript3(serverCommit, channel, keyRing, bootstrapKey)
	if err != nil {
		Log.Errorf("GenerateChannelScript2 failed. %v", err)
		return nil, err
	}
	remoteIndex, _, err := FindOutputIndexes(commitTx, toLocalScript.PkScript(), toRemoteScript.PkScript())
	if err != nil {
		Log.Errorf("findOutputIndexes failed. %v", err)
		return nil, err
	}
	if len(remoteIndex) == 0 {
		Log.Warning("no remote output")
		return nil, nil
	}

	punishTx, prevFetcher, err := CreatePunishmentTx(channel.RemoteCommitment, revPrivKey, channel.LocalChanCfg.PaymentKey,
		remoteIndex, toLocalScript.WitnessScriptToSign(), feeRate)
	if err != nil {
		Log.Errorf("Failed to create punish tx: %v", err)
		return nil, err
	}
	if punishTx == nil {
		return nil, nil
	}

	hexTx, _ := EncodeMsgTx(punishTx)
	Log.Infof("channel %s commit height %d punishTx: \n %s", channel.ChannelId, channel.CommitHeight, hexTx)

	err = VerifySignedTx(punishTx, prevFetcher)
	if err != nil {
		return nil, err
	}
	return punishTx, nil
}

func (p *Manager) BuildSignedSweepTxForClient(channel *Channel, height int, feeRate int64) (*SignedSweepTxPackage, error) {
	// client sweep 花费的是本地 commitment tx 中属于自己的 delayed output。
	// 如果本地 commitment 没有 local output，说明没有需要等待 CSV 后清扫的资产。
	commitSecret := channel.LocalWallet().GetCommitSecret(channel.PeerNodeId, uint32(channel.CommitHeight))
	if commitSecret == nil {
		return nil, fmt.Errorf("GetCommitSecret failed")
	}
	bootstrapKey := p.GetBootstrapNodePaymentPubKey()
	commitPoint := commitSecret.PubKey()
	keyRing := DeriveCommitmentKeys(commitPoint, 0, bootstrapKey, nil, channel)

	toLocalScript, toRemoteScript, err := GenerateChannelScript3(false, channel, keyRing, bootstrapKey)
	if err != nil {
		Log.Errorf("GenerateChannelScript2 failed. %v", err)
		return nil, err
	}

	commitTx := channel.LocalCommitment.CommitTx
	localOutput, _, err := FindOutputIndexes(commitTx, toLocalScript.PkScript(), toRemoteScript.PkScript())
	if err != nil {
		Log.Errorf("findOutputIndexes failed. %v", err)
		return nil, err
	}
	if len(localOutput) == 0 {
		Log.Warning("no local output")
		return nil, nil
	}
	Log.Infof("commit TxId: %s", commitTx.TxID())
	PrintJsonTx(commitTx, "commitTx")
	Log.Infof("CreateAndSignSweepTxForClient pkscript: %s", hex.EncodeToString(toRemoteScript.PkScript()))

	commitmentScript := toLocalScript.WitnessScriptToSign()
	sweepTx, prevFetcher, fee, err := p.CreateSweepTxForClient(channel.LocalCommitment, localOutput,
		channel.LocalChanCfg.PaymentKey, uint32(channel.CsvDelay), uint32(height), commitmentScript, feeRate)
	if err != nil {
		Log.Errorf("Failed to create sweep tx: %v", err)
		return nil, err
	}
	if sweepTx == nil {
		return nil, nil
	}

	result := &SignedSweepTxPackage{
		SweepTx:     sweepTx,
		Txs:         []*wire.MsgTx{sweepTx},
		PrevFetcher: prevFetcher,
		Fee:         fee,
		CommitTxId:  commitTx.TxID(),
		SweepTxId:   sweepTx.TxID(),
	}

	_, err = PartialSignTxWithWallet(channel.LocalWallet(), sweepTx, prevFetcher, commitmentScript, true, nil)
	if err != nil {
		return nil, err
	}
	result.Signed = true

	err = VerifySignedTx(sweepTx, prevFetcher)
	if err != nil {
		Log.Errorf("VerifySignedTx failed, %v", err)
		return nil, err
	}
	result.Verified = true
	result.Broadcastable = true
	return result, nil
}

func CreatePunishmentTx(remoteCommit *ChannelCommitment, revocationPrivKey *btcec.PrivateKey,
	recvPubKey *btcec.PublicKey, outputIndex []int,
	commitmentScript []byte, feeRate int64) (*wire.MsgTx, txscript.PrevOutputFetcher, error) {
	// 构建并签名惩罚交易。这里不依赖引导节点签名，只使用已经收到的
	// revocation 私钥花费对端旧 commitment 的可惩罚输出。
	oldCommitTx := remoteCommit.CommitTx
	remoteBalance := remoteCommit.LocalBalance
	punishTx := wire.NewMsgTx(2)
	var weightEstimate utils.TxWeightEstimator
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)

	recvPkScript, err := GetP2TRpkScript(recvPubKey)
	if err != nil {
		return nil, nil, fmt.Errorf("GetP2TRpkScript failed: %v", err)
	}

	brc20OutputCount := 0
	for k, v := range remoteBalance {
		if k.Protocol == indexer.PROTOCOL_NAME_BRC20 && v.Sign() != 0 {
			brc20OutputCount++
		}
	}

	var plainSats []*TxOutput
	txId := oldCommitTx.TxID()
	hash := oldCommitTx.TxHash()
	for i, index := range outputIndex {
		txOut := oldCommitTx.TxOut[index]
		outPoint := wire.NewOutPoint(&hash, uint32(index))
		if i+1 <= brc20OutputCount {
			// BRC20 对应的 commitment output 本身仍是白聪，不能直接作为资产
			// 输出转走；真正的 transfer reveal 输出在 NextTxs 中处理。
			plainSats = append(plainSats, &indexer.TxOutput{OutPointStr: fmt.Sprintf("%s:%d", txId, index), OutValue: *txOut})
			continue
		}

		if i < len(outputIndex)-1 {
			txIn := wire.NewTxIn(outPoint, nil, nil)
			punishTx.AddTxIn(txIn)
			weightEstimate.AddNestedP2WSHInput(int64(len(commitmentScript)))
			prevFetcher.AddPrevOut(*outPoint, txOut)

			out := wire.NewTxOut(txOut.Value, recvPkScript)
			punishTx.AddTxOut(out)
			weightEstimate.AddP2TROutput()
		} else {
			plainSats = append(plainSats, &indexer.TxOutput{OutPointStr: fmt.Sprintf("%s:%d", txId, index), OutValue: *txOut})
		}
	}

	if brc20OutputCount != 0 {
		// 当前实现要求构造 commitment 时已经准备好 BRC20 transfer inscription。
		// punish 时先广播/花费 reveal tx 的输出，而不是在惩罚阶段临时 mint。
		if len(remoteCommit.NextTxs) == 0 {
			return nil, nil, fmt.Errorf("should construct brc20 transfer inscription before")
		}
		for i, tx := range remoteCommit.NextTxs {
			if i%2 == 0 {
				continue
			}
			hash := tx.TxHash()
			outPoint := wire.NewOutPoint(&hash, 0)
			txOut := tx.TxOut[0]
			txIn := wire.NewTxIn(outPoint, nil, nil)
			punishTx.AddTxIn(txIn)
			weightEstimate.AddNestedP2WSHInput(int64(len(commitmentScript)))
			prevFetcher.AddPrevOut(*outPoint, txOut)

			out := wire.NewTxOut(txOut.Value, recvPkScript)
			punishTx.AddTxOut(out)
			weightEstimate.AddP2TROutput()
		}

		if len(plainSats) == 0 {
			return nil, nil, fmt.Errorf("no enough plain sats to pay network fee")
		}
	}

	var feeValue int64
	for _, plain := range plainSats {
		// 最后归集的 plain sats 用作网络费来源；如果扣 fee 后仍大于 dust，
		// 剩余部分返还给惩罚方。
		feeValue += plain.Value()
		outPoint := plain.OutPoint()
		txIn := wire.NewTxIn(outPoint, nil, nil)
		punishTx.AddTxIn(txIn)
		weightEstimate.AddNestedP2WSHInput(int64(len(commitmentScript)))
		prevFetcher.AddPrevOut(*outPoint, plain.TxOut())
	}
	weightEstimate.AddP2TROutput()
	requiredFee1 := weightEstimate.Fee(feeRate)
	if feeValue >= requiredFee1+330 {
		out := wire.NewTxOut(feeValue-requiredFee1, recvPkScript)
		punishTx.AddTxOut(out)
	}

	if len(punishTx.TxOut) == 0 {
		Log.Errorf("%s output too small to punish", oldCommitTx.TxID())
		return nil, nil, nil
	}

	sigHashes := txscript.NewTxSigHashes(punishTx, prevFetcher)
	for i, txIn := range punishTx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(txIn.PreviousOutPoint)
		sigScript, err := txscript.RawTxInWitnessSignature(punishTx, sigHashes, i,
			preOut.Value, commitmentScript, txscript.SigHashAll, revocationPrivKey)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to sign transaction: %v", err)
		}
		// commitment script 的第二个 witness 元素选择 revocation 分支。
		txIn.Witness = wire.TxWitness{sigScript, []byte{1}, commitmentScript}
	}

	PrintJsonTx(punishTx, "punish TX")
	return punishTx, prevFetcher, nil
}

func (p *Manager) CreateSweepTx(commit *ChannelCommitment, outputIndex []int,
	recvPkScript []byte, scriptType int, csvDelay, currHeight uint32,
	commitmentScript []byte, feeRate int64) (*wire.MsgTx, txscript.PrevOutputFetcher, int64, error) {
	// 构建清扫交易。sweep 花费的是自己 commitment 的 delayed output，需要满足
	// CSV，并用当前高度作为 locktime。
	commitTx := commit.CommitTx
	localBalance := commit.LocalBalance
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	sweepTx := wire.NewMsgTx(2)
	var weightEstimate utils.TxWeightEstimator

	recvAddr, err := AddrFromPkScript(recvPkScript)
	if err != nil {
		return nil, nil, 0, err
	}

	brc20OutputCount := 0
	for k, v := range localBalance {
		if k.Protocol == indexer.PROTOCOL_NAME_BRC20 && v.Sign() != 0 {
			brc20OutputCount++
		}
	}

	sweepTx.LockTime = uint32(currHeight)
	txId := commitTx.TxID()
	hash := commitTx.TxHash()
	var plainSats []*TxOutput
	for i, index := range outputIndex {
		txOut := commitTx.TxOut[index]
		outPoint := wire.NewOutPoint(&hash, uint32(index))
		if i+1 <= brc20OutputCount {
			// BRC20 的 commitment output 先按白聪保留，真正的资产转移输出来自
			// 已经预构造的 reveal tx。
			plainSats = append(plainSats, &indexer.TxOutput{OutPointStr: fmt.Sprintf("%s:%d", txId, index), OutValue: *txOut})
			continue
		}

		if i < len(outputIndex)-1 {
			txIn := &wire.TxIn{PreviousOutPoint: *outPoint, Sequence: csvDelay}
			sweepTx.AddTxIn(txIn)
			weightEstimate.AddNestedP2WSHInput(int64(len(commitmentScript)))
			prevFetcher.AddPrevOut(*outPoint, txOut)

			out := wire.NewTxOut(txOut.Value, recvPkScript)
			sweepTx.AddTxOut(out)
			weightEstimate.AddP2TROutput()
		} else {
			plainSats = append(plainSats, &indexer.TxOutput{OutPointStr: fmt.Sprintf("%s:%d", txId, index), OutValue: *txOut})
		}
	}

	if brc20OutputCount != 0 {
		// 与 punish 一样，BRC20 transfer inscription 必须在 commitment 构造时
		// 已经准备好；sweep 阶段只消费 reveal tx 输出。
		if len(commit.NextTxs) == 0 {
			return nil, nil, 0, fmt.Errorf("should construct brc20 transfer inscription before")
		}
		for i, tx := range commit.NextTxs {
			if i%2 == 0 {
				continue
			}
			hash := tx.TxHash()
			outPoint := wire.NewOutPoint(&hash, 0)
			txOut := tx.TxOut[0]
			txIn := &wire.TxIn{PreviousOutPoint: *outPoint, Sequence: csvDelay}
			sweepTx.AddTxIn(txIn)
			weightEstimate.AddNestedP2WSHInput(int64(len(commitmentScript)))
			prevFetcher.AddPrevOut(*outPoint, txOut)

			out := wire.NewTxOut(txOut.Value, recvPkScript)
			sweepTx.AddTxOut(out)
			weightEstimate.AddP2TROutput()
		}
		if len(plainSats) == 0 {
			return nil, nil, 0, fmt.Errorf("no enough plain sats to pay network fee")
		}
	}

	var feeValue int64
	for _, plain := range plainSats {
		// plain sats 负责支付清扫交易网络费，扣除 fee 后的可用余额再返还。
		feeValue += plain.Value()
		outPoint := plain.OutPoint()
		txIn := &wire.TxIn{PreviousOutPoint: *outPoint, Sequence: csvDelay}
		sweepTx.AddTxIn(txIn)
		weightEstimate.AddNestedP2WSHInput(int64(len(commitmentScript)))
		prevFetcher.AddPrevOut(*outPoint, plain.TxOut())
	}
	weightEstimate.AddP2TROutput()
	requiredFee1 := weightEstimate.Fee(feeRate)
	if feeValue >= requiredFee1+330 {
		out := wire.NewTxOut(feeValue-requiredFee1, recvPkScript)
		sweepTx.AddTxOut(out)
	}

	if len(sweepTx.TxOut) == 0 {
		Log.Warningf("%s output too small to sweep", commitTx.TxID())
		return nil, nil, 0, nil
	}

	fee := weightEstimate.Fee(feeRate)
	if feeValue < fee {
		// 通常 channel 内 plain sats 应足够支付清扫 fee；不足时允许额外选择
		// 钱包白聪补 fee，避免有效资产因为手续费不足而无法 sweep。
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		inChannel := scriptType == SCRIPT_TYPE_CHANNEL || scriptType == SCRIPT_TYPE_SWEEP
		selected, feeValue, err := p.SelectUtxosForFee(recvAddr, nil, feeValue, feeRate, &weightEstimate, false, inChannel)
		if err == nil {
			for _, output := range selected {
				sweepTx.AddTxIn(output.TxIn())
				prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
			}
			fee = weightEstimate.Fee(feeRate)
			weightEstimate.AddP2TROutput()
			fee1 := weightEstimate.Fee(feeRate)
			feeChange := feeValue - fee
			if feeChange >= 330 {
				fee = fee1
				txOut := &wire.TxOut{PkScript: selected[0].OutValue.PkScript, Value: feeChange}
				sweepTx.AddTxOut(txOut)
			}
		}
	}

	PrintJsonTx(sweepTx, "sweepTx for "+strconv.Itoa(scriptType))
	return sweepTx, prevFetcher, fee, nil
}

func (p *Manager) CreateSweepTxForClient(commit *ChannelCommitment, outputIndex []int,
	localPubKey *secp256k1.PublicKey, csvDelay, currHeight uint32,
	commitmentScript []byte, feeRate int64) (*wire.MsgTx, txscript.PrevOutputFetcher, int64, error) {
	// client 侧 sweep 直接回到本地 payment key，对应单方签名路径。
	recvPkScript, err := GetP2TRpkScript(localPubKey)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("GetP2TRpkScript failed: %v", err)
	}
	return p.CreateSweepTx(commit, outputIndex, recvPkScript, SCRIPT_TYPE_SWEEP, csvDelay, currHeight, commitmentScript, feeRate)
}

func (p *Manager) CreateSweepTxForServer(commit *ChannelCommitment, outputIndex []int,
	localPubKey, bootstrapKey *secp256k1.PublicKey, csvDelay, currHeight uint32,
	commitmentScript []byte, feeRate int64) (*wire.MsgTx, txscript.PrevOutputFetcher, int64, error) {
	// server 侧 sweep 输出到 server/bootstrap 的 2-of-2 脚本，后续由服务端流程
	// 完成需要的协同签名和广播。
	_, recvPkScript, err := GetP2WSHscript(localPubKey.SerializeCompressed(), bootstrapKey.SerializeCompressed())
	if err != nil {
		Log.Errorf("GetP2WSHScript failed. %v", err)
		return nil, nil, 0, err
	}
	return p.CreateSweepTx(commit, outputIndex, recvPkScript, SCRIPT_TYPE_SWEEP, csvDelay, currHeight, commitmentScript, feeRate)
}
