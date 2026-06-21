package wallet

import (
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

func (p *Manager) SignAndVerifyCommitTx(channel *Channel, prevSignedTx []*wire.MsgTx,
	tx *wire.MsgTx, theirSig [][]byte,
	inscribes []*InscribeResv, prevTxSigs [][][]byte,
	next []*InscribeResv, nextTxSigs [][][]byte, checkAcceptance bool) error {

	return p.signAndVerifyCommitTx(channel, prevSignedTx, tx, theirSig,
		inscribes, prevTxSigs, next, nextTxSigs, nil, nil, nil, checkAcceptance)
}

func (p *Manager) SignAndVerifyCommitTxWithOthers(channel *Channel, prevSignedTx []*wire.MsgTx,
	tx *wire.MsgTx, theirSig [][]byte,
	inscribes []*InscribeResv, prevTxSigs [][][]byte,
	next []*InscribeResv, nextTxSigs [][][]byte,
	others []*CommitmentTx, otherPrevTxSigs [][][][]byte, otherTxSigs [][][]byte,
	checkAcceptance bool) error {

	return p.signAndVerifyCommitTx(channel, prevSignedTx, tx, theirSig,
		inscribes, prevTxSigs, next, nextTxSigs, others, otherPrevTxSigs, otherTxSigs, checkAcceptance)
}

func (p *Manager) signAndVerifyCommitTx(channel *Channel, prevSignedTx []*wire.MsgTx,
	tx *wire.MsgTx, theirSig [][]byte,
	inscribes []*InscribeResv, prevTxSigs [][][]byte,
	next []*InscribeResv, nextTxSigs [][][]byte,
	others []*CommitmentTx, otherPrevTxSigs [][][][]byte, otherTxSigs [][][]byte,
	checkAcceptance bool) error {

	prevFetcher := channel.GetCommitmentPrefetchor()
	var txs []*wire.MsgTx
	if prevSignedTx != nil {
		txs = append(txs, prevSignedTx...)
	}

	if len(inscribes) != 0 {
		for i, insc := range inscribes {
			txs = append(txs, insc.CommitTx)
			txs = append(txs, insc.RevealTx)

			_, err := FinalSignTxWithWallet(channel.LocalWallet(), insc.CommitTx, insc.GetCommitPrevOutputFetcher(), channel.RedeemScript, false,
				channel.GetRemotePubKey().SerializeCompressed(), prevTxSigs[2*i])
			if err != nil {
				Log.Infof("SignAndVerifyCommitTx %d %s FinalSignTx failed, %v", i, insc.CommitTx.TxID(), err)
				return err
			}

			err = VerifySignedTx(insc.CommitTx, insc.GetCommitPrevOutputFetcher())
			if err != nil {
				Log.Infof("SignAndVerifyCommitTx %d %s VerifySignedTx failed, %v", i, insc.CommitTx.TxID(), err)
				return err
			}

			output := indexer.GenerateTxOutput(insc.RevealTx, 0)
			prevFetcher.AddPrevOut(*output.OutPoint(), output.TxOut())
			change := insc.GetChangeOutput()
			if change != nil {
				prevFetcher.AddPrevOut(*change.OutPoint(), change.TxOut())
			}
		}
	}

	txs = append(txs, tx)

	if len(next) != 0 {
		for _, insc := range next {
			txs = append(txs, insc.CommitTx)
			txs = append(txs, insc.RevealTx)
		}
	}

	var err error
	txs, err = p.signAndVerifyCommitmentOthers(channel, tx, txs, others, otherPrevTxSigs, otherTxSigs)
	if err != nil {
		return err
	}

	_, err = FinalSignTxWithWallet(channel.LocalWallet(), tx, prevFetcher, channel.RedeemScript, false,
		channel.GetRemotePubKey().SerializeCompressed(), theirSig)
	if err != nil {
		Log.Infof("SignAndVerifyCommitTx commit TX %s FinalSignTx failed, %v", tx.TxID(), err)
		return err
	}

	err = VerifySignedTx(tx, prevFetcher)
	if err != nil {
		Log.Infof("SignAndVerifyCommitTx commit TX %s VerifySignedTx failed, %v", tx.TxID(), err)
		return err
	}

	if checkAcceptance {
		// commitment tx 不是马上广播的交易；签名验证通过还不够，需要提前做
		// mempool/脚本验收，避免将来 force close 时才发现交易无效。
		if err = p.TestAcceptance(txs); err != nil {
			Log.Errorf("SignAndVerifyCommitTx TestAcceptance failed. %v", err)
			return err
		}
	}

	// 验证完成后移除对端签名，避免本地持久化或导出完整 commitment tx 后被
	// 误广播，也避免把对端签名暴露给不该拥有完整交易的一侧。
	CleanPeerSig(channel.RedeemScript, channel.GetLocalPubKey().SerializeCompressed(),
		channel.GetRemotePubKey().SerializeCompressed(), tx, prevFetcher)

	return nil
}

func (p *Manager) signAndVerifyCommitmentOthers(channel *Channel, parentTx *wire.MsgTx,
	txs []*wire.MsgTx, others []*CommitmentTx, otherPrevTxSigs [][][][]byte,
	otherTxSigs [][][]byte) ([]*wire.MsgTx, error) {

	if len(others) == 0 {
		return txs, nil
	}
	if len(otherTxSigs) != len(others) {
		return nil, fmt.Errorf("invalid other commitment sigs, got %d expected %d", len(otherTxSigs), len(others))
	}
	for i, other := range others {
		if other == nil || other.CommitTx == nil {
			return nil, fmt.Errorf("invalid other commitment %d", i)
		}
		prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
		AddTxOutputsToPrevFetcher(prevFetcher, parentTx)

		var prevSigs [][][]byte
		if len(otherPrevTxSigs) > i {
			prevSigs = otherPrevTxSigs[i]
		}
		if len(prevSigs) != len(other.PrevTxs) {
			return nil, fmt.Errorf("invalid other commitment %d prev tx sigs, got %d expected %d", i, len(prevSigs), len(other.PrevTxs))
		}
		for j, prevTx := range other.PrevTxs {
			if HasChannelInput(prevTx, prevFetcher, channel.GetChannelPkScript()) {
				_, err := FinalSignTxWithWallet(channel.LocalWallet(), prevTx, prevFetcher, channel.RedeemScript, false,
					channel.GetRemotePubKey().SerializeCompressed(), prevSigs[j])
				if err != nil {
					return nil, fmt.Errorf("other commitment %d prev tx %d FinalSignTx failed: %w", i, j, err)
				}
			}
			if err := VerifySignedTx(prevTx, prevFetcher); err != nil {
				return nil, fmt.Errorf("other commitment %d prev tx %d VerifySignedTx failed: %w", i, j, err)
			}
			txs = append(txs, prevTx)
			AddTxOutputsToPrevFetcher(prevFetcher, prevTx)
		}

		_, err := FinalSignTxWithWallet(channel.LocalWallet(), other.CommitTx, prevFetcher, channel.RedeemScript, false,
			channel.GetRemotePubKey().SerializeCompressed(), otherTxSigs[i])
		if err != nil {
			return nil, fmt.Errorf("other commitment %d FinalSignTx failed: %w", i, err)
		}
		if err := VerifySignedTx(other.CommitTx, prevFetcher); err != nil {
			return nil, fmt.Errorf("other commitment %d VerifySignedTx failed: %w", i, err)
		}
		txs = append(txs, other.CommitTx)
	}
	return txs, nil
}

func (p *Manager) SignAndVerifyCommitTxV2(channel *Channel, checkAcceptance bool) error {
	tx := channel.LocalCommitment.CommitTx
	theirSig := channel.LocalCommitment.CommitSig
	prevSignedTx := channel.LocalCommitment.PrevTxs
	var txs []*wire.MsgTx
	prevFetcher := channel.GetCommitmentPrefetchor()
	if len(prevSignedTx) != 0 {
		for i, tx := range prevSignedTx {
			if err := p.fillMissingCommitPrevOutputs(prevFetcher, tx); err != nil {
				return err
			}
			output := indexer.GenerateTxOutput(tx, 0)
			prevFetcher.AddPrevOut(*output.OutPoint(), output.TxOut())
			if len(tx.TxOut) == 2 {
				output := indexer.GenerateTxOutput(tx, 1)
				prevFetcher.AddPrevOut(*output.OutPoint(), output.TxOut())
			}
			if i%2 == 0 {
				_, err := PartialSignTxWithWallet(channel.LocalWallet(), tx, prevFetcher, channel.RedeemScript, false,
					channel.GetRemotePubKey().SerializeCompressed())
				if err != nil {
					Log.Infof("SignAndVerifyCommitTxV2 %d PartialSignTx failed, %v", i, err)
					return err
				}
			}

			err := VerifySignedTx(tx, prevFetcher)
			if err != nil {
				Log.Errorf("SignAndVerifyCommitTxV2 VerifySignedTx previous tx %d failed, %v", i, err)
				return err
			}
			txs = append(txs, tx)
		}
	}
	txs = append(txs, tx)
	_, err := FinalSignTxWithWallet(channel.LocalWallet(), tx, prevFetcher, channel.RedeemScript, false,
		channel.GetRemotePubKey().SerializeCompressed(), theirSig)
	if err != nil {
		Log.Infof("SignAndVerifyCommitTx commit TX %s FinalSignTx failed, %v", tx.TxID(), err)
		return err
	}

	err = VerifySignedTx(tx, prevFetcher)
	if err != nil {
		Log.Infof("SignAndVerifyCommitTx commit TX %s VerifySignedTx failed, %v", tx.TxID(), err)
		return err
	}

	if checkAcceptance {
		// 历史 commitment 重新加载后也要重新做验收检查，确保 prev/reveal/commit
		// 链在当前节点规则下仍然可接受。
		err = p.TestAcceptance(txs)
		if err != nil {
			Log.Errorf("SignAndVerifyCommitTx TestAcceptance failed. %v", err)
			return err
		}
	}

	// V2 路径同样只保留本地可控的半签名状态，避免完整 commitment tx 在本地
	// 数据库中长期存在。
	CleanPeerSig(channel.RedeemScript, channel.GetLocalPubKey().SerializeCompressed(),
		channel.GetRemotePubKey().SerializeCompressed(), tx, prevFetcher)

	return nil
}

func (p *Manager) fillMissingCommitPrevOutputs(prevFetcher *txscript.MultiPrevOutFetcher, tx *wire.MsgTx) error {
	for _, in := range tx.TxIn {
		if prevFetcher.FetchPrevOutput(in.PreviousOutPoint) != nil {
			continue
		}
		output, err := p.GetIndexerClient().GetTxOutput(in.PreviousOutPoint.String())
		if err != nil {
			return fmt.Errorf("GetTxOutput %s failed when verifying commit prev tx: %v", in.PreviousOutPoint.String(), err)
		}
		prevFetcher.AddPrevOut(in.PreviousOutPoint, &output.OutValue)
		Log.Warnf("filled historical commit prevout %s from L1 indexer for tx %s", in.PreviousOutPoint.String(), tx.TxID())
	}
	return nil
}

func (p *Manager) SignAndVerifyClosingTx(channel *Channel, tx *wire.MsgTx,
	theirSig [][]byte, inscribes []*InscribeResv, prevTxSigs [][][]byte,
	checkAcceptance bool) ([][]byte, []*wire.MsgTx, [][][]byte, error) {

	prevFetcher := channel.GetCommitmentPrefetchor()
	preLocalTxSigs := make([][][]byte, 0)
	var txs []*wire.MsgTx
	if len(inscribes) != 0 {
		for i, insc := range inscribes {
			txs = append(txs, insc.CommitTx)
			txs = append(txs, insc.RevealTx)

			sig, err := FinalSignTxWithWallet(channel.LocalWallet(), insc.CommitTx, insc.GetCommitPrevOutputFetcher(), channel.RedeemScript, false,
				channel.GetRemotePubKey().SerializeCompressed(), prevTxSigs[2*i])
			if err != nil {
				Log.Infof("SignAndVerifyClosingTx %d %s FinalSignTx failed, %v", i, insc.CommitTx.TxID(), err)
				return nil, nil, nil, err
			}

			err = VerifySignedTx(insc.CommitTx, insc.GetCommitPrevOutputFetcher())
			if err != nil {
				Log.Infof("SignAndVerifyClosingTx %d %s VerifySignedTx failed, %v", i, insc.CommitTx.TxID(), err)
				return nil, nil, nil, err
			}
			preLocalTxSigs = append(preLocalTxSigs, sig)
			preLocalTxSigs = append(preLocalTxSigs, nil)

			output := indexer.GenerateTxOutput(insc.RevealTx, 0)
			prevFetcher.AddPrevOut(*output.OutPoint(), output.TxOut())
			change := insc.GetChangeOutput()
			if change != nil {
				prevFetcher.AddPrevOut(*change.OutPoint(), change.TxOut())
			}
		}
	}

	result, err := FinalSignTxWithWallet(channel.LocalWallet(), tx, prevFetcher, channel.RedeemScript, false,
		channel.GetRemotePubKey().SerializeCompressed(), theirSig)
	if err != nil {
		Log.Infof("SignAndVerifyClosingTx commit TX %s FinalSignTx failed, %v", tx.TxID(), err)
		return nil, nil, nil, err
	}

	err = VerifySignedTx(tx, prevFetcher)
	if err != nil {
		Log.Infof("SignAndVerifyClosingTx commit TX %s VerifySignedTx failed, %v", tx.TxID(), err)
		return nil, nil, nil, err
	}

	if checkAcceptance {
		// closing tx 也可能带 inscription prev/reveal 链；广播前先整体验收，
		// 可以提前暴露脚本或 fee 问题。
		err = p.TestAcceptance(append(txs, tx))
		if err != nil {
			Log.Errorf("SignAndVerifyClosingTx TestAcceptance failed. %v", err)
			return nil, nil, nil, err
		}
	}

	return result, txs, preLocalTxSigs, nil
}

func (p *Manager) VerifyClosingTx(channel *Channel, tx *wire.MsgTx,
	theirSig [][]byte, inscribes []*InscribeResv, prevTxSigs [][][]byte,
	checkAcceptance bool) ([]*wire.MsgTx, [][][]byte, error) {

	prevFetcher := channel.GetCommitmentPrefetchor()
	preLocalTxSigs := make([][][]byte, 0)
	var txs []*wire.MsgTx
	if len(inscribes) != 0 {
		for i, insc := range inscribes {
			txs = append(txs, insc.CommitTx)
			txs = append(txs, insc.RevealTx)

			sig, err := FinalSignTxWithWallet(channel.LocalWallet(), insc.CommitTx, insc.GetCommitPrevOutputFetcher(), channel.RedeemScript, false,
				channel.GetRemotePubKey().SerializeCompressed(), prevTxSigs[2*i])
			if err != nil {
				Log.Infof("VerifyClosingTx %d %s FinalSignTx failed, %v", i, insc.CommitTx.TxID(), err)
				return nil, nil, err
			}

			err = VerifySignedTx(insc.CommitTx, insc.GetCommitPrevOutputFetcher())
			if err != nil {
				Log.Infof("VerifyClosingTx %d %s VerifySignedTx failed, %v", i, insc.CommitTx.TxID(), err)
				return nil, nil, err
			}
			preLocalTxSigs = append(preLocalTxSigs, sig)
			preLocalTxSigs = append(preLocalTxSigs, nil)

			output := indexer.GenerateTxOutput(insc.RevealTx, 0)
			prevFetcher.AddPrevOut(*output.OutPoint(), output.TxOut())
			change := insc.GetChangeOutput()
			if change != nil {
				prevFetcher.AddPrevOut(*change.OutPoint(), change.TxOut())
			}
		}
	}

	_, err := FinalSignTxWithWallet(channel.LocalWallet(), tx, prevFetcher, channel.RedeemScript, false,
		channel.GetRemotePubKey().SerializeCompressed(), theirSig)
	if err != nil {
		Log.Infof("VerifyClosingTx commit TX %s FinalSignTx failed, %v", tx.TxID(), err)
		return nil, nil, err
	}

	err = VerifySignedTx(tx, prevFetcher)
	if err != nil {
		Log.Infof("VerifyClosingTx commit TX %s VerifySignedTx failed, %v", tx.TxID(), err)
		return nil, nil, err
	}

	if checkAcceptance {
		// 只验证对端 closing 签名时也执行同样的交易链验收，避免接受一个
		// 看似签名正确但最终无法进入 mempool 的关闭方案。
		err = p.TestAcceptance(append(txs, tx))
		if err != nil {
			Log.Errorf("VerifyClosingTx TestAcceptance failed. %v", err)
			return nil, nil, err
		}
	}

	return txs, preLocalTxSigs, nil
}
