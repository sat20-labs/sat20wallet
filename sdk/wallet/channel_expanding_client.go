package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func isIndexerNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "can't find") || strings.Contains(msg, "cannot find")
}

func (p *Manager) markRecoverAscended(resv *SplicingReservation, ascendData *sindexer.AscendData, utxo string) error {
	if ascendData == nil {
		return nil
	}
	if ascendData.BTCLucky != nil {
		return fmt.Errorf("BTC lucky reward requires btc-lucky-claim before channel admission")
	}
	if ascendData.FundingUtxo != "" && ascendData.FundingUtxo != utxo {
		return fmt.Errorf("ascend funding utxo mismatch %s != %s", ascendData.FundingUtxo, utxo)
	}
	if ascendData.Address != "" && ascendData.Address != resv.Channel.ChannelId {
		return fmt.Errorf("ascend channel mismatch %s != %s", ascendData.Address, resv.Channel.ChannelId)
	}
	if ascendData.AnchorTxId == "" {
		return fmt.Errorf("ascend anchor tx is empty for %s", utxo)
	}

	resv.RecoverAscended = true
	resv.RecoveredAnchorTxId = ascendData.AnchorTxId
	resv.RecoveredAnchorOutpoint = fmt.Sprintf("%s:0", ascendData.AnchorTxId)
	if _, err := p.getRecoveredAscendedOutput(resv); err != nil {
		resv.RecoverAscended = false
		resv.RecoveredAnchorTxId = ""
		resv.RecoveredAnchorOutpoint = ""
		return err
	}
	Log.Infof("recover ascended funding utxo %s with anchor %s", utxo, resv.RecoveredAnchorOutpoint)
	return nil
}

func (p *Manager) AllowExpand(resv *SplicingReservation, utxo string) error {
	if resv == nil || resv.AssetName == nil {
		return fmt.Errorf("missing channel expansion context")
	}
	channel := resv.Channel
	assetName := resv.AssetName
	if err := ValidateSTPAssetForChannel(channel, &assetName.AssetName, STPOpExpand); err != nil {
		return err
	}
	protocol, err := STPAssetProtocolFor(&assetName.AssetName, STPOpExpand)
	if err != nil {
		return err
	}
	view, err := rgbReservationView(&resv.RevocationInfo)
	if err != nil {
		return err
	}
	protocol = stpProtocolWithRGBView(protocol, view)
	if len(channel.InControl([]string{utxo})) > 0 {
		return fmt.Errorf("utxo has been in channel")
	}

	ascendData, err := p.GetIndexerRPCClient_SatsNet().GetAscendData(utxo)
	if err != nil && !isIndexerNotFoundErr(err) {
		return err
	}
	if err := p.rejectBTCLuckyChannelFunding(utxo, channel.RedeemScript); err != nil {
		return err
	}

	info, err := p.GetIndexerClient().GetTxOutput(utxo)
	if err != nil {
		return err
	}
	if _, native := protocol.(STPAssetFundingProtocol); native {
		if err := p.acceptSTPChannelFundingWithRGBView(channel, assetName, []*TxOutput{info}, resv.AssetFunding, resv.AssetEntropy, view); err != nil {
			return err
		}
		builder, ok := protocol.(STPAssetExpansionFunding)
		if !ok {
			return fmt.Errorf("%s does not support proof-backed expansion", protocol.Protocol())
		}
		resv.NativeAnchorProof, err = builder.BuildExpansionAnchorProof(p, channel, assetName, info)
		if err != nil {
			return err
		}
		if len(resv.NativeAnchorProof) == 0 {
			return fmt.Errorf("missing native expansion anchor proof")
		}
	}
	amt, _ := info.GetAssetV2(&assetName.AssetName)
	if amt.Sign() == 0 {
		return fmt.Errorf("utxo %s has not specific asset", utxo)
	}
	if !bytes.Equal(channel.ChanPoint.OutValue.PkScript, info.OutValue.PkScript) {
		return fmt.Errorf("utxo %s is not in channel address", utxo)
	}
	if err := protocol.VerifyInput(assetName, info); err != nil {
		return err
	}
	if err := AlignAsset(info, &assetName.AssetName); err != nil {
		return err
	}

	resv.SplicingValue = info.Value()
	resv.SplicingInputs = []*TxOutput{info}
	resv.Amt = amt.Clone()
	resv.SplicingAmt = amt.Clone()
	if ascendData != nil {
		if err := p.markRecoverAscended(resv, ascendData, utxo); err != nil {
			return err
		}
	}
	return nil
}

func (p *Manager) AllowExpandSatsNet(payment *PaymentReservation, utxos []string) error {
	channel := payment.Channel
	utxos = channel.NotInControl_SatsNet(utxos)
	if len(utxos) == 0 {
		return fmt.Errorf("no new utxo")
	}

	var amt *indexer.Decimal
	result := make([]*TxOutput_SatsNet, 0, len(utxos))
	for _, utxo := range utxos {
		info, err := p.GetIndexerRPCClient_SatsNet().GetTxOutput(utxo)
		if err != nil {
			return err
		}
		v := info.GetAsset(&payment.AssetName.AssetName)
		if v.Sign() == 0 {
			return fmt.Errorf("utxo %s has not specific asset", utxo)
		}
		if !bytes.Equal(channel.ChanPoint.OutValue.PkScript, info.OutValue.PkScript) {
			return fmt.Errorf("utxo %s is not in channel address", utxo)
		}
		if err := p.rejectBTCLuckyOutput(utxo, nil); err != nil {
			return err
		}
		result = append(result, OutputToSatsNet(info))
		amt = indexer.DecimalAdd(amt, v)
	}

	if err := channel.HasEnoughCapacityForLock(payment.AssetName, amt, true, payment.Channel.IsInitiator == payment.IsInitiator); err != nil {
		return err
	}
	payment.Amt = amt
	payment.Utxos = result
	return nil
}

func isPeerChannelPendingError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "can't find channel") || strings.Contains(msg, "is not ready")
}

// Read current UTXOs, not an address total: channel commitments already own
// their funding, pending and stub inputs. Lucky backing is never surplus.
func (p *Manager) unmanagedChannelExpansionInputs(channel *Channel, assetName *swire.AssetName) ([]string, error) {
	rpc := p.GetIndexerRPCClient()
	outputs, err := rpc.GetUtxosWithAddress(channel.Address)
	if err != nil {
		return nil, err
	}
	controlled := channel.UtxosInControl()
	result := make([]string, 0)
	for point := range outputs {
		if controlled[point] || (p.utxoLockerL1 != nil && p.utxoLockerL1.IsLocked(point)) {
			continue
		}
		output, err := rpc.GetTxOutput(point)
		if err != nil {
			return nil, err
		}
		if output == nil {
			return nil, fmt.Errorf("missing expansion output %s", point)
		}
		amount, invalid := output.GetAssetV2(assetName)
		if (invalid && !STPPolicyFor(&AssetName{AssetName: *assetName}).AccountBased) || amount == nil || amount.Sign() <= 0 || (indexer.IsPlainAsset(assetName) && len(output.Assets) != 0) {
			continue
		}
		lucky, err := p.isBTCLuckyL1Output(point, nil)
		if err != nil {
			return nil, err
		}
		if lucky {
			continue
		}
		result = append(result, point)
	}
	sort.Strings(result)
	return result, nil
}

func (p *Manager) getAllUnmanagedUtxos(channel *Channel, assetName *swire.AssetName) ([]string, error) {
	utxos, err := p.unmanagedChannelExpansionInputs(channel, assetName)
	if err != nil {
		return nil, err
	}
	if len(utxos) == 0 {
		return nil, fmt.Errorf("no new utxo")
	}
	return utxos, nil
}

func (p *Manager) getAllUnmanagedUtxosSatsNet(channel *Channel) ([]string, error) {
	utxomap, err := p.GetIndexerRPCClient_SatsNet().GetUtxosWithAddress(channel.Address)
	if err != nil {
		Log.Errorf("GetUtxosWithAddress %s failed. %v", channel.Address, err)
		return nil, err
	}
	utxos := make([]string, 0, len(utxomap))
	for k := range utxomap {
		if err := p.rejectBTCLuckyOutput(k, nil); err != nil {
			if errors.Is(err, errBTCLuckyChannelInput) {
				continue
			}
			return nil, err
		}
		utxos = append(utxos, k)
	}
	utxos = channel.NotInControl_SatsNet(utxos)
	if len(utxos) == 0 {
		return nil, fmt.Errorf("no new utxo")
	}
	return utxos, nil
}

// channel mutex must already be held by caller.
func (p *Manager) getUtxosWithChannelAddress(channel *Channel, value int64, num int) ([]string, error) {
	address := channel.Address
	utxos := p.GetIndexerClient().GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)

	utxosInControl := channel.UtxosInControl()
	p.GetUtxoLocker().Reload(address)

	result := make([]string, 0, num)
	for _, u := range utxos {
		utxo := u.OutPoint
		if _, ok := utxosInControl[utxo]; ok {
			continue
		}
		if p.GetUtxoLocker().IsLocked(utxo) {
			continue
		}
		lucky, err := p.isBTCLuckyL1Output(utxo, nil)
		if err != nil {
			return nil, err
		}
		if lucky {
			continue
		}
		if u.Value == value {
			result = append(result, utxo)
			if len(result) == num {
				break
			}
		}
	}

	if len(result) != num {
		return nil, fmt.Errorf("can't find %d utxos with value %d in channel address", num, value)
	}
	return result, nil
}

// ExpandAssetInChannel owns its lock. Each accepted expansion advances the
// commitment, so the next source must collect a new request-bound RGB window.
func (p *Manager) ExpandAssetInChannel(channel *Channel, assetName *swire.AssetName) ([]string, *Decimal, error) {
	if channel == nil {
		return nil, nil, fmt.Errorf("missing expansion channel")
	}
	channel.Mutex.RLock()
	w := channel.LocalWallet()
	if w != nil {
		w = w.Clone()
	}
	channel.Mutex.RUnlock()
	return p.expandAssetInChannelForWallet(channel, assetName, w)
}

func (p *Manager) expandAssetInChannelForWallet(channel *Channel, assetName *swire.AssetName, w common.Wallet) ([]string, *Decimal, error) {
	if channel == nil || w == nil {
		return nil, nil, fmt.Errorf("missing expansion channel or captured wallet")
	}
	channel.Mutex.Lock()
	utxos, err := p.getAllUnmanagedUtxos(channel, assetName)
	channel.Mutex.Unlock()
	if err != nil {
		return nil, nil, err
	}
	var value *Decimal
	var itemErrors []error
	anchorIDs := make([]string, 0)
	for _, output := range utxos {
		request, err := json.Marshal(struct{ Channel, Asset, Input string }{channel.ChannelId, assetName.String(), output})
		if err != nil {
			itemErrors = append(itemErrors, err)
			continue
		}
		evidence, err := p.prepareSTPRGBExpansionEvidence(channel, assetName, output, request)
		if err != nil {
			itemErrors = append(itemErrors, fmt.Errorf("input %s: %w", output, err))
			continue
		}
		channel.Mutex.Lock()
		err = evidence.ValidateForChannel(channel, request)
		if err == nil && (p.GetChannel(channel.ChannelId) != channel || channel.LocalWallet() == nil ||
			channel.LocalWallet().GetWalletId() != w.GetWalletId() || !channel.LocalWallet().GetPaymentPubKey().IsEqual(w.GetPaymentPubKey())) {
			err = fmt.Errorf("channel signer does not match captured recovery account")
		}
		var txid string
		var amt *Decimal
		var id int64
		if err == nil {
			txid, amt, id, err = p.funderInitExpandingProcessWithRGBEvidence(channel, assetName, output, "", nil, evidence)
		}
		channel.Mutex.Unlock()
		if err != nil {
			itemErrors = append(itemErrors, fmt.Errorf("input %s: %w", output, err))
			continue
		}
		value = value.Add(amt)
		anchorIDs = append(anchorIDs, txid)
		for {
			time.Sleep(100 * time.Millisecond)
			resv, ok := p.GetSplicingReservation(id)
			if !ok || resv.Status == RS_CLOSED || resv.Status == RS_SPLICINGIN_ANCHOR_CONFIRMED {
				break
			}
		}
	}
	return anchorIDs, value, errors.Join(itemErrors...)
}

// FunderInitExpandingProcess is the lock-held non-RGB wrapper. RGB callers
// must pass evidence captured outside the business lock.
func (p *Manager) FunderInitExpandingProcess(channel *Channel, assetName *swire.AssetName, utxo string,
	reason string, memo []byte) (string, *Decimal, int64, error) {
	return p.funderInitExpandingProcessWithRGBEvidence(channel, assetName, utxo, reason, memo, nil)
}

func (p *Manager) funderInitExpandingProcessWithRGBEvidence(channel *Channel, assetName *swire.AssetName, utxo string,
	reason string, memo []byte, evidence *STPRGBReservationEvidence) (string, *Decimal, int64, error) {
	return p.funderInitExpandingProcessWithRGBEvidenceAndPolicy(channel, assetName, utxo, reason, memo, evidence, nil)
}

// A non-nil policy is private to closed-channel restoration. Explicit expansion
// keeps its former funding, locking, RPC order and cleanup behavior.
func (p *Manager) funderInitExpandingProcessWithRGBEvidenceAndPolicy(channel *Channel, assetName *swire.AssetName, utxo string,
	reason string, memo []byte, evidence *STPRGBReservationEvidence, policy *closedChannelAssetRestorePolicy) (string, *Decimal, int64, error) {
	if err := ValidateSTPAssetForChannel(channel, assetName, STPOpExpand); err != nil {
		return "", nil, 0, err
	}
	if err := p.rejectClosedChannelAssetRestoreOverlap(channel, assetName.String(), utxo, policy); err != nil {
		return "", nil, 0, err
	}

	oldChannel, err := p.loadChannel(channel.ChannelId)
	if err != nil {
		return "", nil, 0, err
	}
	tickerInfo := p.getTickerInfo(assetName)
	if tickerInfo == nil {
		return "", nil, 0, fmt.Errorf("can't get ticker %s info", assetName.String())
	}
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		return "", nil, 0, err
	}

	resv := SplicingReservation{}
	resv.RGBEvidence = evidence
	resv.InitRuntime()
	resv.Status = RS_INIT
	resv.IsInitiator = reason != SPLICING_REASON_REMOTE
	resv.SetLocalWallet(channel.LocalWallet().Clone())
	resv.WalletId = resv.LocalWallet().GetWalletId()
	resv.ChannelId = channel.ChannelId
	resv.OldChanPoint = channel.ChanPoint.OutPointStr
	resv.OldChannel = oldChannel
	resv.Channel = channel
	resv.AssetName = GetAssetName(tickerInfo)
	resv.TickerInfo = tickerInfo
	resv.NeedSendSplicingTx = false
	resv.FeeRate = p.GetFeeRate()
	resv.RevealPrivKey = priv.Serialize()
	resv.Memo = memo
	protocol, err := STPAssetProtocolFor(assetName, STPOpExpand)
	if err != nil {
		return "", nil, 0, err
	}
	view, viewErr := rgbReservationView(&resv.RevocationInfo)
	if viewErr != nil {
		return "", nil, 0, viewErr
	}
	if _, native := protocol.(STPAssetFundingProtocol); native {
		input, err := p.GetIndexerClient().GetTxOutput(utxo)
		if err != nil {
			return "", nil, 0, err
		}
		// A client-validated carrier needs its previously supplied consignment.
		// Preparing it does not insert a P2WSH balance into the ordinary wallet.
		resv.AssetFunding, err = p.prepareSTPChannelFundingWithRGBView(channel, resv.AssetName, []*TxOutput{input}, view)
		if err != nil {
			return "", nil, 0, err
		}
		resv.AssetEntropy = channel.AssetEntropy
	}

	if err := p.AllowExpand(&resv, utxo); err != nil {
		Log.Errorf("AllowExpand failed. %v", err)
		return "", nil, 0, err
	}
	if policy != nil {
		if err := policy.validateSource(p, &resv, utxo); err != nil {
			return "", nil, 0, err
		}
	}
	// This handoff's existing bc- snapshot is the sole exact old signed
	// obligation. The fresh pre-expansion channel must not overwrite it.
	if view == nil || len(view.facts.closedSourceFunding) == 0 {
		if err := p.SaveBackupChannelToDB(&resv.OldChannel.ChannelInDB); err != nil {
			Log.Errorf("SaveBackupChannelToDB failed. %v", err)
			return "", nil, 0, err
		}
	}

	var stubUtxos []string
	if c, v := resv.Channel.NeedStubUtxo(resv.AssetName); c > 0 {
		stubUtxos, err = p.getUtxosWithChannelAddress(resv.Channel, v, c)
		if err != nil {
			if policy != nil {
				return "", nil, 0, fmt.Errorf("%w: channel READY; asset restoration paused; existing channel stubs are insufficient; additional stub funding requires approval: %v", errClosedChannelFundingApprovalRequired, err)
			}
			txid, _, err := p.BatchSendPlainSats(resv.Channel.Address, v, c, resv.FeeRate, nil)
			if err != nil {
				return "", nil, 0, err
			}
			for i := range c {
				stubUtxos = append(stubUtxos, fmt.Sprintf("%s:%d", txid, i))
			}
		}
		stubs := make([]*TxOutput, 0, c)
		for _, outpoint := range stubUtxos {
			info, err := p.GetIndexerClient().GetTxOutput(outpoint)
			if err != nil {
				if policy != nil {
					return "", nil, 0, fmt.Errorf("cannot revalidate existing channel stub %s: %w", outpoint, err)
				}
				info = indexer.NewTxOutput(v)
				info.OutPointStr = outpoint
				info.OutValue.PkScript = resv.Channel.GetChannelPkScript()
			}
			stubs = append(stubs, info)
		}
		resv.Fees = stubs
	}
	if policy != nil {
		if err := policy.validateExistingStubs(p, &resv); err != nil {
			return "", nil, 0, err
		}
	}

	resv.InReq = &wwire.SplicingInRequest{
		AssetFunding:       cloneSTPAssetProofs(resv.AssetFunding),
		AssetEntropy:       resv.AssetEntropy,
		MsgHeader:          wwire.NewMsgHeader(),
		ChannelId:          channel.ChannelId,
		CommitHeight:       channel.CommitHeight,
		AssetName:          resv.AssetName.String(),
		Amt:                resv.Amt.String(),
		Utxos:              []string{utxo},
		Fees:               stubUtxos,
		FeeRate:            resv.FeeRate,
		RevealKey:          resv.RevealPrivKey,
		NeedSendSplicingTx: resv.NeedSendSplicingTx,
		Reason:             reason,
		Memo:               memo,
	}
	msg, err := json.Marshal(resv.InReq)
	if err != nil {
		return "", nil, 0, err
	}
	resv.ReqSig, err = resv.LocalWallet().SignMessage(msg)
	if err != nil {
		return "", nil, 0, err
	}

	logID := ""
	if !resv.IsInitiator {
		logID = p.beginOperationLogBestEffort(remoteExpandOperationLogCreate(&resv, utxo))
	}

	for {
		channelID := resv.Channel.ChannelId
		if policy != nil {
			if err = policy.beforeRequest(p, &resv); err != nil {
				break
			}
		}
		if channel.PeerRPC == nil {
			err = fmt.Errorf("peer rpc is not initialized")
			break
		}
		err = channel.PeerRPC.SendSplicingInReq(&resv)
		if err != nil {
			Log.Errorf("SendSplicingInReq failed. %v", err)
			break
		}
		if policy != nil {
			if err = policy.accepted(p, &resv); err != nil {
				break
			}
		}
		channel.ResvId = resv.Id
		p.bindOperationLogReservationBestEffort(logID, RESV_TYPE_SPLICING, resv.Id)
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{
			Status:  OperationLogRunning,
			Message: peerAcceptedSplicingMessage(&resv, "channel expansion"),
			Details: map[string]string{"reservation_id": fmt.Sprintf("%d", resv.Id)},
		})

		err = p.FunderProcessAcceptSplicingIn(&resv)
		if err != nil {
			Log.Errorf("funderProcessAcceptSplicingIn failed. %v", err)
			break
		}

		if policy != nil && policy.ctx.Err() != nil {
			err = policy.ctx.Err()
			break
		}
		err = channel.PeerRPC.SendSplicingInCommitSigReq(&resv)
		if err != nil {
			Log.Errorf("SendSplicingInCommitSigReq failed. %v", err)
			break
		}

		if policy != nil && policy.ctx.Err() != nil {
			err = policy.ctx.Err()
			break
		}
		err = p.ReceiveRevocation(&resv.RevocationInfo, resv.RemoteRev)
		if err != nil {
			Log.Errorf("ReceiveRevocation %s failed. %v", channelID, err)
			break
		}

		if policy == nil {
			err = p.ReceiveNewCommitment(&resv.RevocationInfo, &resv.LocalCommitInfo.CommitSigInfo, nil)
		} else {
			err = p.receiveNewCommitmentForWallet(&resv.RevocationInfo, &resv.LocalCommitInfo.CommitSigInfo, nil, policy.wallet)
		}
		if err != nil {
			Log.Errorf("ReceiveNewCommitment %s failed. %v", channelID, err)
			break
		}

		if policy == nil {
			resv.LocalRev, err = p.RevokeCurrentCommitment(&resv.RevocationInfo)
		} else {
			resv.LocalRev, err = p.revokeCurrentCommitmentForWallet(&resv.RevocationInfo, policy.wallet)
		}
		if err != nil {
			Log.Errorf("RevokeCurrentCommitment %s failed. %v", channelID, err)
			break
		}

		if policy != nil && policy.ctx.Err() != nil {
			err = policy.ctx.Err()
			break
		}
		err = channel.PeerRPC.SendSplicingInRevokeAndAckReq(&resv)
		if err != nil {
			if !isBroadcastResultUnknown(err) {
				Log.Errorf("SendSplicingInRevokeAndAckReq %s failed. %v", channelID, err)
			} else {
				Log.Warnf("SendSplicingInRevokeAndAckReq %s result is unknown. Abort local expand state advance and wait for peer sync. %v", channelID, err)
			}
			break
		}

		resv.Status = RS_SPLICINGIN_STARTED
		resv.Channel.CommitHeight += 1
		resv.Channel.UpdateTime = resv.Id
		if err = SaveReservation(p.db, &resv.SplicingDataInDB); err != nil {
			Log.Errorf("SaveReservation failed. %v", err)
			break
		}
		if err = p.saveChannelToDB(resv.Channel); err != nil {
			Log.Errorf("saveChannelToDB failed. %v", err)
			break
		}
		p.addResv(&resv)
		_ = p.HandleSplicingInStarted(&resv)
		break
	}

	// A private error can still own an accepted durable child. Preserve that
	// busy marker; explicit callers retain their original unconditional cleanup.
	if policy == nil || err == nil {
		channel.ResvId = 0
	}
	anchorTxID := ""
	if err == nil {
		anchorTxID = resv.AnchorTxId()
		Log.Infof("expand anchor TxId: %s", anchorTxID)
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{
			Status:  OperationLogRunning,
			Message: "Channel expansion submitted; waiting for confirmation",
			TxID:    anchorTxID,
			Details: map[string]string{"txid": anchorTxID},
		})
	} else if policy != nil {
		// An automatic caller cannot erase a possibly accepted peer obligation
		// or roll a revoked commitment back. The durable owner retains the exact
		// request/child for reconciliation, including an unknown RPC result.
		return "", resv.Amt.Clone(), resv.Id, err
	} else {
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{
			Status: OperationLogFailed, Message: err.Error(), Details: map[string]string{"error": err.Error()},
		})
		_ = p.saveChannelToDB(resv.OldChannel)
		p.enableChannel(resv.OldChannel)
		p.DelResvWithId(resv.Id)
		if resv.Id != 0 && channel.PeerRPC != nil {
			_ = channel.PeerRPC.SendActionResultNfty(resv.LocalWallet(), resv.Id, RESV_TYPE_SPLICING, -1, err.Error())
		}
	}
	return anchorTxID, resv.Amt.Clone(), resv.Id, err
}

// ExpandAssetInChannelSatsNet, like the public SDK entry, owns its lock.
func (p *Manager) ExpandAssetInChannelSatsNet(channel *Channel, assetName *swire.AssetName) (*Decimal, error) {
	channel.Mutex.Lock()
	utxos, err := p.getAllUnmanagedUtxosSatsNet(channel)
	channel.Mutex.Unlock()
	if err != nil {
		Log.Errorf("getAllUnmanagedUtxosSatsNet failed. %v", err)
		return nil, err
	}
	return p.expandChannelSatsNet(channel.ChannelId, assetName.String(), utxos)
}

// The low-level wrapper is lock-held and non-RGB only. RGB callers must
// prepare outside that lock and pass the reservation-local evidence explicitly.
func (p *Manager) FunderInitExpandingProcessSatsNet(channel *Channel, assetName *swire.AssetName, utxos []string) (*Decimal, error) {
	return p.funderInitExpandingProcessSatsNetWithRGBEvidence(channel, assetName, utxos, nil)
}

func (p *Manager) funderInitExpandingProcessSatsNetWithRGBEvidence(channel *Channel, assetName *swire.AssetName, utxos []string, evidence *STPRGBReservationEvidence) (*Decimal, error) {
	if _, err := rgbReservationView(&RevocationInfo{Channel: channel, RGBEvidence: evidence}); err != nil {
		return nil, err
	}
	if err := ValidateSTPAsset(assetName, STPOpExpand); err != nil {
		return nil, err
	}

	oldChannel, err := p.loadChannel(channel.ChannelId)
	if err != nil {
		return nil, err
	}
	tickerInfo := p.getTickerInfo(assetName)
	if tickerInfo == nil {
		return nil, fmt.Errorf("can't get ticker %s info", assetName)
	}

	var revealPrivKey []byte
	if channel.NeedsSTPRevealKey() {
		priv, err := btcec.NewPrivateKey()
		if err != nil {
			return nil, err
		}
		revealPrivKey = priv.Serialize()
	}

	resv := &PaymentReservation{
		PaymentDataInDB: PaymentDataInDB{
			ReservationBase: NewReservationBase(0, true, RS_INIT, channel.LocalWallet()),
			ChannelId:       channel.ChannelId,
			IsUnlock:        false,
			NeedSendLockTx:  false,
			AssetName:       GetAssetName(tickerInfo),
		},
		RevocationInfo: RevocationInfo{
			RGBEvidence:   evidence,
			OldChannel:    oldChannel,
			Channel:       channel,
			FeeRate:       p.GetFeeRate(),
			RevealPrivKey: revealPrivKey,
		},
	}

	if err := p.AllowExpandSatsNet(resv, utxos); err != nil {
		return nil, fmt.Errorf("not allow lock, %v", err)
	}
	_ = p.SaveBackupChannelToDB(&resv.OldChannel.ChannelInDB)
	channelID := resv.ChannelId

	for {
		err = CreatePaymentTx(resv)
		if err != nil {
			Log.Errorf("UpdateUtxoL2 failed. %v", err)
			break
		}
		err = UpdateCommitWithPaymentResv(resv)
		if err != nil {
			Log.Errorf("UpdateCommitValue failed. %v", err)
			break
		}
		resv.LocalRevKey, err = p.GetCurrRevocationKey(channel)
		if err != nil {
			break
		}
		resv.LocalNextRevKey, err = p.GetNextRevocationKey(channel)
		if err != nil {
			break
		}

		resv.LockReq = &wwire.LockRequest{
			MsgHeader:      wwire.NewMsgHeader(),
			ChannelId:      channelID,
			CommitHeight:   channel.CommitHeight,
			AssetName:      resv.AssetName.String(),
			Amt:            resv.Amt.String(),
			FeeRate:        resv.FeeRate,
			LockUtxos:      utxos,
			FeeUtxos:       nil,
			RevealKey:      resv.RevealPrivKey,
			RevKey:         resv.LocalRevKey,
			NextRevKey:     resv.LocalNextRevKey,
			NeedSendLockTx: resv.NeedSendLockTx,
			Memo:           resv.Memo,
		}
		msg, err := json.Marshal(resv.LockReq)
		if err != nil {
			break
		}
		resv.ReqSig, err = resv.LocalWallet().SignMessage(msg)
		if err != nil {
			break
		}

		if channel.PeerRPC == nil {
			err = fmt.Errorf("peer rpc is not initialized")
			break
		}
		err = channel.PeerRPC.SendLockReq(resv)
		if err != nil {
			Log.Errorf("SendLockReq %s failed. %v", channelID, err)
			break
		}
		channel.ResvId = resv.Id

		err = p.ReceiveNewCommitment(&resv.RevocationInfo, &resv.LocalCommitInfo.CommitSigInfo, nil)
		if err != nil {
			Log.Errorf("ReceiveNewCommitment %s failed. %v", channelID, err)
			break
		}
		resv.LocalRev, err = p.RevokeCurrentCommitment(&resv.RevocationInfo)
		if err != nil {
			Log.Errorf("RevokeCurrentCommitment %s failed. %v", channelID, err)
			break
		}
		err = p.SignNextCommitment(&resv.RevocationInfo)
		if err != nil {
			Log.Errorf("SignNextCommitment %s failed. %v", channelID, err)
			break
		}

		err = channel.PeerRPC.SendLockCommitSigAndRevokeReq(resv)
		if err != nil {
			Log.Errorf("SendLockCommitSigAndRevokeReq %s failed. %v", channelID, err)
			break
		}

		err = p.ReceiveRevocation(&resv.RevocationInfo, resv.RemoteRev)
		if err != nil {
			Log.Errorf("ReceiveRevocation %s failed. %v", channelID, err)
			break
		}

		err = p.TestAcceptance_SatsNet([]*swire.MsgTx{resv.Channel.LocalCommitment.DeAnchorTx})
		if err != nil {
			Log.Errorf("TestAcceptance_SatsNet failed. %v", err)
			break
		}

		err = channel.PeerRPC.SendLockAckReq(resv)
		if err != nil {
			if !isBroadcastResultUnknown(err) {
				Log.Errorf("SendRevokeAndAckReq %s failed. %v", channelID, err)
			} else {
				Log.Warnf("SendRevokeAndAckReq %s result is unknown. Abort local lock-with-expand state advance and wait for peer sync. %v", channelID, err)
			}
			break
		}

		channel.CommitHeight += 1
		channel.UpdateTime = resv.Id
		_ = p.saveChannelToDB(channel)
		_ = p.HandlePaymentFinished(resv)
		break
	}

	channel.ResvId = 0
	if err != nil {
		p.enableChannel(resv.OldChannel)
		if resv.Id != 0 && channel.PeerRPC != nil {
			_ = channel.PeerRPC.SendActionResultNfty(resv.LocalWallet(), resv.Id, RESV_TYPE_PAYMENT, -1, err.Error())
		}
		return nil, err
	}
	return resv.Amt, nil
}
