package wallet

import (
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

func (p *Manager) ForcelyCloseChannel(channel *Channel, feeRate int64) (string, string, error) {
	if channel == nil {
		return "", "", fmt.Errorf("channel is nil")
	}

	commitTx := channel.LocalCommitment.CommitTx
	if commitTx == nil {
		return "", "", fmt.Errorf("can't find lastest commitment TX")
	}

	prevFetcher := channel.GetCommitmentPrefetchor()
	var txs []*wire.MsgTx
	if len(channel.LocalCommitment.PrevTxs) != 0 {
		for _, tx := range channel.LocalCommitment.PrevTxs {
			output := indexer.GenerateTxOutput(tx, 0)
			prevFetcher.AddPrevOut(*output.OutPoint(), output.TxOut())
			if len(tx.TxOut) == 2 {
				output := indexer.GenerateTxOutput(tx, 1)
				prevFetcher.AddPrevOut(*output.OutPoint(), output.TxOut())
			}

			if err := VerifySignedTx(tx, prevFetcher); err != nil {
				Log.Errorf("ForcelyCloseChannel VerifySignedTx previous tx failed, %v", err)
				return "", "", err
			}
			txs = append(txs, tx)
		}
	}
	if err := VerifyTxWithChannel(channel, commitTx, prevFetcher, channel.LocalCommitment.CommitSig); err != nil {
		return "", "", err
	}

	deAnchorTx := channel.LocalCommitment.DeAnchorTx
	if deAnchorTx == nil {
		return "", "", fmt.Errorf("can't find lastest commitment deAnchor TX")
	}

	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	resv := &ClosingReservation{
		ClosingDataInDB: ClosingDataInDB{
			ReservationBase: NewReservationBase(p.GenerateNewResvId(), true, 0, p.wallet),
			ChannelId:       channel.ChannelId,
			FeeRate:         feeRate,
			CloseTx:         commitTx,
			DeAnchorTx:      deAnchorTx,
		},
		Channel: channel,
	}
	resv.InitRuntime()

	PrintJsonTx_SatsNet(resv.DeAnchorTx, "forcely close deAnchorTx")
	deAnchorTxId, err := p.BroadcastTx_SatsNet(resv.DeAnchorTx)
	if err != nil {
		Log.Errorf("BroadCastTx_SatsNet %s failed. %v", resv.DeAnchorTx.TxID(), err)
	} else {
		Log.Infof("deanchor Tx broadcasted: %s", deAnchorTxId)
	}

	PrintJsonTx(commitTx, "forcely closeTx")
	txs = append(txs, commitTx)
	txs = append(txs, channel.LocalCommitment.NextTxs...)
	broadcasted, err := p.BroadcastTxsIrreversibleL1(txs, "force close")
	if err != nil {
		Log.Errorf("BroadcastTxs %s failed, %v, try with other btc node", commitTx.TxID(), err)
		for _, preTx := range channel.LocalCommitment.PrevTxs {
			if err = BroadcastTxByOtherProvider(preTx); err != nil {
				Log.Errorf("BroadcastTxByOtherProvider %s failed. %v", preTx.TxID(), err)
				return "", "", err
			}
			Log.Infof("pre Tx broadcasted: %s", preTx.TxID())
		}
		if err = BroadcastTxByOtherProvider(commitTx); err != nil {
			Log.Errorf("BroadCastTx commitTx %s failed. %v", commitTx.TxID(), err)
			return "", "", err
		}
		for _, nextTx := range channel.LocalCommitment.NextTxs {
			if err = BroadcastTxByOtherProvider(nextTx); err != nil {
				Log.Errorf("BroadcastTxByOtherProvider %s failed. %v", nextTx.TxID(), err)
				return "", "", err
			}
			Log.Infof("pre Tx broadcasted: %s", nextTx.TxID())
		}
		broadcasted = true
	}

	if !broadcasted {
		Log.Warnf("force close %s broadcast result is unknown, keep pending for retry", commitTx.TxID())
	}
	Log.Infof("commitTx Tx broadcasted or pending: %s", commitTx.TxID())

	p.AddResv(resv)
	p.DisableChannel(channel)
	channel.Status = CS_CLOSE_FORCELY_BROADCASTED
	channel.ClosingTx = commitTx
	if err := p.SaveWalletReservation(resv); err != nil {
		return "", "", err
	}
	if err := p.SaveChannelToDB(channel); err != nil {
		return "", "", err
	}

	if channel.IsInitiator && channel.PeerRPC != nil {
		_ = channel.PeerRPC.SendActionResultNfty(0, RESV_TYPE_CLOSE, 1, channel.ChannelId)
	}

	return commitTx.TxID(), deAnchorTxId, err
}

func (p *Manager) CloserForcelyClose(channelID string, feeRate int64) (string, string, error) {
	channel := p.GetChannel(channelID)
	if channel == nil {
		return "", "", fmt.Errorf("can't find channel %s", channelID)
	}
	if channel.Status < CS_READY {
		return "", "", fmt.Errorf("channel %s invalid status %d", channelID, channel.Status)
	}

	if err := p.SaveBackupChannelToDB(&channel.ChannelInDB); err != nil {
		return "", "", err
	}

	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	return p.ForcelyCloseChannel(channel, feeRate)
}

func BroadcastTxByOtherProvider(tx *wire.MsgTx) error {
	txHex, err := EncodeMsgTx(tx)
	if err != nil {
		return err
	}

	var mempoolRPC *RESTClient
	var blockstreamRPC *RESTClient

	http := NewHTTPClient()
	if IsTestNet() {
		blockstreamRPC = NewRESTClient("https", "blockstream.info", "testnet4/api", http)
	} else {
		blockstreamRPC = NewRESTClient("https", "blockstream.info", "api", http)
	}

	if blockstreamRPC != nil {
		url := blockstreamRPC.GetUrl("/tx")
		txId, err := blockstreamRPC.Http.SendPostRequest(url, []byte(txHex))
		if err != nil {
			Log.Errorf("broadcast by blockstream failed, %v", err)
		} else {
			Log.Infof("broadcast by blockstream succed, %s", txId)
			return nil
		}
	}

	if mempoolRPC != nil {
		url := mempoolRPC.GetUrl("/tx")
		txId, err := mempoolRPC.Http.SendPostRequest(url, []byte(txHex))
		if err != nil {
			Log.Errorf("broadcast by mempool failed, %v", err)
		} else {
			Log.Infof("broadcast by mempool succed, %s", txId)
			return nil
		}
	}

	return fmt.Errorf("broadcast tx %s failed", tx.TxID())
}

func (p *Manager) HandleChannelForceCloseConfirmed(resv *ClosingReservation) error {
	if resv == nil || resv.Channel == nil || resv.Channel.ClosingTx == nil {
		return fmt.Errorf("invalid force close reservation")
	}

	Log.Warnf("channel %s forcely close confirmed", resv.Channel.ChannelId)
	p.SendMessageToUpper(MSG_CHANNEL_FORCELY_CLOSED, resv.Channel.ClosingTx.TxID())

	resv.Channel.Status = CS_CLOSE_FORCELY_CONFIRMED
	if err := p.SaveChannelToDB(resv.Channel); err != nil {
		return err
	}

	height, err := p.GetIndexerRPCClient().GetTxHeight(resv.Channel.ClosingTx.TxID())
	if err != nil {
		return err
	}
	resv.CloseHeight = height
	return p.SaveWalletReservation(resv)
}

func (p *Manager) HandleChannelForceCloseWaitToSweep(resv *ClosingReservation, height int) error {
	if resv == nil || resv.Channel == nil {
		return fmt.Errorf("invalid force close reservation")
	}
	if height-resv.CloseHeight <= int(resv.Channel.CsvDelay) {
		return nil
	}

	Log.Warnf("channel %s forcely close start to sweep output", resv.Channel.ChannelId)
	if !resv.Channel.IsInitiator {
		return nil
	}

	sweepTxPackage, err := p.BuildSignedSweepTxForClient(resv.Channel, height, p.GetFeeRate())
	if err != nil {
		Log.Errorf("BuildSignedSweepTxForClient failed. %v", err)
		return err
	}
	if sweepTxPackage == nil || sweepTxPackage.SweepTx == nil {
		Log.Warningf("output too small to sweep")
		return p.HandleChannelForceCloseSweepConfirmed(resv)
	}

	broadcasted, err := p.BroadcastTxsIrreversibleL1(sweepTxPackage.Txs, "force close sweep")
	if err != nil {
		Log.Errorf("BroadCastTx sweep Tx failed. %v", err)
		return err
	}
	if !broadcasted {
		return nil
	}

	resv.Channel.ClosingTx = sweepTxPackage.SweepTx
	resv.Channel.Status = CS_CLOSE_FORCELY_SWEEP_BROADCASTED
	return p.SaveChannelToDB(resv.Channel)
}

func (p *Manager) HandleChannelForceCloseSweepConfirmed(resv *ClosingReservation) error {
	if resv == nil || resv.Channel == nil || resv.Channel.ClosingTx == nil {
		return fmt.Errorf("invalid force close reservation")
	}

	Log.Warnf("channel %s forcely close sweep confirmed", resv.Channel.ChannelId)
	channel := resv.Channel
	p.DelResvWithId(resv.Id)

	channel.Status = CS_CLOSED_FORCELY
	if err := p.SaveChannelToDB(channel); err != nil {
		return err
	}
	p.SendMessageToUpper(MSG_CHANNEL_SWEPT, channel.ClosingTx.TxID())
	return nil
}
