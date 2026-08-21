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
	if len(punishTx.TxOut) == 0 {
		withChange := weightEstimate
		withChange.AddTxOutput(wire.NewTxOut(0, recvPkScript))
		if feeValue < withChange.Fee(feeRate)+330 {
			Log.Errorf("%s output too small to punish", oldCommitTx.TxID())
			return nil, nil, nil
		}
	}
	if _, err := addSweepFeeChange(punishTx, &weightEstimate, feeValue, feeRate, recvPkScript); err != nil {
		return nil, nil, err
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

func addSweepFeeChange(tx *wire.MsgTx, weightEstimate *utils.TxWeightEstimator,
	feeValue, feeRate int64, changePkScript []byte) (int64, error) {

	if tx == nil || weightEstimate == nil || feeRate <= 0 || feeValue < 0 || len(changePkScript) == 0 {
		return 0, fmt.Errorf("invalid sweep fee parameters")
	}
	feeNoChange := weightEstimate.Fee(feeRate)
	if feeValue < feeNoChange {
		return 0, fmt.Errorf("no enough plain sats for sweep fee: require %d but %d", feeNoChange, feeValue)
	}

	withChange := *weightEstimate
	changeOutput := wire.NewTxOut(0, changePkScript)
	withChange.AddTxOutput(changeOutput)
	feeWithChange := withChange.Fee(feeRate)
	change := feeValue - feeWithChange
	if change >= 330 {
		changeOutput.Value = change
		tx.AddTxOut(changeOutput)
		*weightEstimate = withChange
		return feeWithChange, nil
	}

	// Without a non-dust change output, every remaining plain sat is the
	// transaction fee. This value is the actual input/output delta.
	return feeValue, nil
}

func (p *Manager) CreateSweepTx(commit *ChannelCommitment, outputIndex []int,
	recvPkScript []byte, scriptType int, csvDelay, currHeight uint32,
	commitmentScript []byte, feeRate int64) (*wire.MsgTx, txscript.PrevOutputFetcher, int64, error) {
	// 构建清扫交易。sweep 花费的是自己 commitment 的 delayed output，需要满足
	// CSV，并用当前高度作为 locktime。
	if commit == nil || commit.CommitTx == nil || len(outputIndex) == 0 || len(recvPkScript) == 0 {
		return nil, nil, 0, fmt.Errorf("invalid sweep transaction parameters")
	}
	if feeRate <= 0 {
		return nil, nil, 0, fmt.Errorf("invalid sweep fee rate %d", feeRate)
	}
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

	sweepTx.LockTime = currHeight
	txID := commitTx.TxID()
	hash := commitTx.TxHash()
	var plainSats []*TxOutput
	for i, index := range outputIndex {
		if index < 0 || index >= len(commitTx.TxOut) {
			return nil, nil, 0, fmt.Errorf("invalid sweep output index %d", index)
		}
		txOut := commitTx.TxOut[index]
		outPoint := wire.NewOutPoint(&hash, uint32(index))
		if i+1 <= brc20OutputCount {
			// BRC20 的 commitment output 先按白聪保留，真正的资产转移输出来自
			// 已经预构造的 reveal tx。
			plainSats = append(plainSats, &indexer.TxOutput{
				OutPointStr: fmt.Sprintf("%s:%d", txID, index), OutValue: *txOut,
			})
			continue
		}

		if i < len(outputIndex)-1 {
			txIn := &wire.TxIn{PreviousOutPoint: *outPoint, Sequence: csvDelay}
			sweepTx.AddTxIn(txIn)
			weightEstimate.AddNestedP2WSHInput(int64(len(commitmentScript)))
			prevFetcher.AddPrevOut(*outPoint, txOut)

			out := wire.NewTxOut(txOut.Value, recvPkScript)
			sweepTx.AddTxOut(out)
			weightEstimate.AddTxOutput(out)
		} else {
			plainSats = append(plainSats, &indexer.TxOutput{
				OutPointStr: fmt.Sprintf("%s:%d", txID, index), OutValue: *txOut,
			})
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
			if tx == nil || len(tx.TxOut) == 0 {
				return nil, nil, 0, fmt.Errorf("invalid brc20 sweep predecessor")
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
			weightEstimate.AddTxOutput(out)
		}
		if len(plainSats) == 0 {
			return nil, nil, 0, fmt.Errorf("no enough plain sats to pay network fee")
		}
	}

	var feeValue int64
	for _, plain := range plainSats {
		feeValue += plain.Value()
		outPoint := plain.OutPoint()
		txIn := &wire.TxIn{PreviousOutPoint: *outPoint, Sequence: csvDelay}
		sweepTx.AddTxIn(txIn)
		weightEstimate.AddNestedP2WSHInput(int64(len(commitmentScript)))
		prevFetcher.AddPrevOut(*outPoint, plain.TxOut())
	}

	// Preserve the historical behavior for a plain-only commitment output:
	// do not pull in an unrelated wallet UTXO merely to recover an output that
	// cannot pay for its own transaction and a non-dust change output.
	if len(sweepTx.TxOut) == 0 {
		withChange := weightEstimate
		withChange.AddTxOutput(wire.NewTxOut(0, recvPkScript))
		if feeValue < withChange.Fee(feeRate)+330 {
			Log.Warningf("%s output too small to sweep", commitTx.TxID())
			return nil, nil, 0, nil
		}
	}

	feeNoChange := weightEstimate.Fee(feeRate)
	var selected []*TxOutput
	if feeValue < feeNoChange {
		inChannel := scriptType == SCRIPT_TYPE_CHANNEL || scriptType == SCRIPT_TYPE_SWEEP
		selected, feeValue, err = p.SelectUtxosForFee(
			recvAddr, nil, feeValue, feeRate, &weightEstimate, false, inChannel,
		)
		if err != nil {
			return nil, nil, 0, err
		}
		for _, output := range selected {
			sweepTx.AddTxIn(output.TxIn())
			prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
		}
	}

	changePkScript := recvPkScript
	if len(selected) > 0 {
		changePkScript = selected[0].OutValue.PkScript
	}
	actualFee, err := addSweepFeeChange(sweepTx, &weightEstimate, feeValue, feeRate, changePkScript)
	if err != nil {
		return nil, nil, 0, err
	}
	if len(sweepTx.TxOut) == 0 {
		Log.Warningf("%s output too small to sweep", commitTx.TxID())
		return nil, nil, 0, nil
	}

	PrintJsonTx(sweepTx, "sweepTx for "+strconv.Itoa(scriptType))
	return sweepTx, prevFetcher, actualFee, nil
}

func calculateSweepActualFee(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher) (int64, error) {
	if tx == nil || prevFetcher == nil {
		return 0, fmt.Errorf("invalid sweep fee calculation")
	}
	var inputValue int64
	for _, input := range tx.TxIn {
		previous := prevFetcher.FetchPrevOutput(input.PreviousOutPoint)
		if previous == nil {
			return 0, fmt.Errorf("missing sweep previous output %s", input.PreviousOutPoint.String())
		}
		inputValue += previous.Value
	}
	var outputValue int64
	for _, output := range tx.TxOut {
		if output == nil || output.Value < 0 {
			return 0, fmt.Errorf("invalid sweep output")
		}
		outputValue += output.Value
	}
	if inputValue < outputValue {
		return 0, fmt.Errorf("sweep outputs exceed inputs")
	}
	return inputValue - outputValue, nil
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
