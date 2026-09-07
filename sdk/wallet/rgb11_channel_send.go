package wallet

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

type rgb11ChannelSendContext struct {
	data                    rgb11wallet.ChannelSendData
	wallet                  common.Wallet
	pkScript                []byte
	excluded                map[string]bool
	maxConfirmedInputHeight int
	excludeRecentBlock      bool
}

// SendRGB11FromChannel uses the existing STP authorization request. It does not
// enable RGB ascend/descend or grant a peer permission to bypass its contract.
func (p *Manager) SendRGB11FromChannel(local common.Wallet, dest []*SendAssetInfo,
	asset string, feeRate int64, channelID, reason string, moreData []byte, payFeeByLocal, excludeRecentBlock bool) (string, int64, error) {
	return p.sendRGB11FromChannelAtHeight(local, dest, asset, feeRate, channelID, reason, moreData, payFeeByLocal, excludeRecentBlock, 0)
}

func (p *Manager) sendRGB11FromChannelAtHeight(local common.Wallet, dest []*SendAssetInfo,
	asset string, feeRate int64, channelID, reason string, moreData []byte, payFeeByLocal, excludeRecentBlock bool, maxConfirmedInputHeight int) (string, int64, error) {
	if local == nil {
		local = p.wallet
	}
	name := ParseAssetString(asset)
	if name == nil || name.Protocol != rgb11wallet.Protocol {
		return "", 0, rgb11wallet.ErrInvalidRGB11Asset
	}
	ctx, err := p.newRGB11ChannelSendContext(local, channelID, reason, moreData, payFeeByLocal)
	if err != nil {
		return "", 0, err
	}
	ctx.maxConfirmedInputHeight = maxConfirmedInputHeight
	ctx.excludeRecentBlock = excludeRecentBlock
	tx, fee, err := p.sendRGB11AssetsWithChannel(local, dest, name, feeRate, nil, ctx)
	if tx == nil {
		return "", fee, err
	}
	return tx.TxID(), fee, err
}

func (p *rgb11Manager) rgb11ChannelInputWithinHeight(outpoint string) bool {
	if p.channelSend == nil {
		return true
	}
	if p.channelSend.excludeRecentBlock {
		output, err := p.l1IndexerClient.GetTxOutput(outpoint)
		if err != nil || output == nil || p.IsRecentBlockUtxo(output.UtxoId) {
			return false
		}
	}
	if p.channelSend.maxConfirmedInputHeight <= 0 {
		return true
	}
	point, err := wire.NewOutPointFromString(outpoint)
	if err != nil {
		return false
	}
	status, err := p.evidence.GetTxStatus(point.Hash.String())
	return err == nil && status != nil && status.Confirmed && status.BlockHeight > 0 &&
		status.BlockHeight <= int64(p.channelSend.maxConfirmedInputHeight)
}

func (p *Manager) newRGB11ChannelSendContext(local common.Wallet, channelID, reason string,
	moreData []byte, payFeeByLocal bool) (*rgb11ChannelSendContext, error) {
	if local == nil || p.wallet == nil || local.GetAddress() != p.wallet.GetAddress() {
		return nil, ErrRGB11DirectRootRequired
	}
	var fields map[string]json.RawMessage
	if reason == "" || json.Unmarshal(moreData, &fields) != nil || fields == nil {
		return nil, fmt.Errorf("invalid RGB11 channel signing context")
	}
	witness, peer, err := p.channelWitness(local, channelID)
	if err != nil {
		return nil, err
	}
	script, err := AddrToPkScript(channelID, GetChainParam())
	if err != nil {
		return nil, err
	}
	excluded := make(map[string]bool)
	if channel := p.GetChannel(channelID); channel != nil {
		channel.Mutex.RLock()
		excluded = channel.UtxosInControl()
		channel.Mutex.RUnlock()
	}
	return &rgb11ChannelSendContext{wallet: local, pkScript: script, excluded: excluded,
		data: rgb11wallet.ChannelSendData{ChannelID: channelID, WitnessScript: witness,
			PeerPubKey: peer, Reason: reason, MoreData: append([]byte(nil), moreData...), PayFeeByLocal: payFeeByLocal}}, nil
}

// A channel is owned only when the current account's key and a configured
// peer derive exactly its P2WSH script. An arbitrary witness supplied with an
// imported consignment never establishes ownership.
func (p *rgb11Manager) ownsRGB11ChannelScript(script []byte) bool {
	if !txscript.IsPayToWitnessScriptHash(script) || p.wallet == nil {
		return false
	}
	if p.channelSend != nil && bytes.Equal(script, p.channelSend.pkScript) {
		return true
	}
	owner := p.accountManagementOwner()
	if owner == nil {
		return false
	}
	localKey := p.wallet.GetPaymentPubKey().SerializeCompressed()
	if node := owner.GetServerNode(); node != nil && node.Pubkey != nil {
		_, expected, err := GetP2WSHscript(localKey, node.Pubkey.SerializeCompressed())
		if err == nil && bytes.Equal(expected, script) {
			return true
		}
	}
	for _, channel := range owner.GetAllChannels() {
		channel.Mutex.RLock()
		local, remote := channel.LocalWallet(), channel.GetRemotePubKey()
		matches := local != nil && remote != nil && bytes.Equal(local.GetPaymentPubKey().SerializeCompressed(), localKey)
		if matches {
			_, expected, err := GetP2WSHscript(localKey, remote.SerializeCompressed())
			matches = err == nil && bytes.Equal(expected, script)
		}
		channel.Mutex.RUnlock()
		if matches {
			return true
		}
	}
	return false
}

// signRGB11ChannelBatch is called inside the persisted broadcast-intent
// boundary. The peer may broadcast as part of its existing signing handler,
// therefore all receiver ACKs must already be durable before this call.
func (p *rgb11Manager) signRGB11ChannelBatch(batch []*rgb11wallet.PendingTransfer) error {
	first := batch[0]
	data := first.ChannelSend
	if data == nil || data.Signed {
		return nil
	}
	for _, item := range batch {
		if item == nil || !sameRGB11ChannelSend(data, item.ChannelSend) || !item.State.AddressMode ||
			!item.State.DeliveryAcknowledged || item.State.AckStatus != "accepted" || item.State.DeliveryRecordHash == "" {
			return ErrRGB11AddressDeliveryRequired
		}
	}
	owner := p.accountManagementOwner()
	if owner == nil {
		return ErrRGB11Inconsistent
	}
	context, err := owner.newRGB11ChannelSendContext(p.wallet, data.ChannelID, data.Reason, data.MoreData, data.PayFeeByLocal)
	if err != nil {
		return err
	}
	if !bytes.Equal(context.data.WitnessScript, data.WitnessScript) || !bytes.Equal(context.data.PeerPubKey, data.PeerPubKey) {
		return fmt.Errorf("RGB11 channel signing peer changed")
	}
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(first.SignedPSBT), false)
	if err != nil {
		return err
	}
	if packet.UnsignedTx.TxID() != first.State.WitnessTxID || len(packet.Inputs) != len(packet.UnsignedTx.TxIn) {
		return ErrRGB11Inconsistent
	}
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	for i, input := range packet.UnsignedTx.TxIn {
		if packet.Inputs[i].WitnessUtxo == nil || context.excluded[input.PreviousOutPoint.String()] {
			return ErrRGB11Inconsistent
		}
		prevFetcher.AddPrevOut(input.PreviousOutPoint, packet.Inputs[i].WitnessUtxo)
	}
	tx := packet.UnsignedTx.Copy()
	sigs, err := PartialSignTxWithWallet(context.wallet, tx, prevFetcher, data.WitnessScript, false, data.PeerPubKey)
	if err != nil {
		return err
	}
	txHex, err := EncodeMsgTx(tx)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data.MoreData, &fields); err != nil {
		return err
	}
	fields["tx1"], err = json.Marshal([]*wwire.TxSignInfo{{Tx: txHex, L1Tx: true, LocalSigs: sigs}})
	if err != nil {
		return err
	}
	fields["witness"], err = json.Marshal(data.WitnessScript)
	if err != nil {
		return err
	}
	proof := &wwire.RGB11SigningProof{Consignment: first.LocalConsignment}
	for _, seal := range first.ChangeSeals {
		raw, err := seal.StrictBytes()
		if err != nil {
			return err
		}
		proof.ChangeSeals = append(proof.ChangeSeals, raw)
	}
	fields["rgb11"], err = json.Marshal(proof)
	if err != nil {
		return err
	}
	more, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	req := wwire.SignRequest{MsgHeader: wwire.NewMsgHeader(), ChannelId: data.ChannelID,
		CommitHeight: -1, Reason: data.Reason, MoreData: more,
		PubKey: context.wallet.GetPaymentPubKey().SerializeCompressed(), NodeId: context.wallet.GetNodePubKey().SerializeCompressed()}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	sig, err := context.wallet.SignMessageWithIndex(raw, 0)
	if err != nil {
		return err
	}
	peerSigs, err := owner.serverNode.client.SendSigReq(&req, sig)
	if err != nil {
		return err
	}
	if len(peerSigs) != 1 {
		return fmt.Errorf("invalid RGB11 channel signature response")
	}
	if _, err := FinalSignTxWithWallet(context.wallet, tx, prevFetcher, data.WitnessScript, false, data.PeerPubKey, peerSigs[0]); err != nil {
		return err
	}
	if tx.TxID() != first.State.WitnessTxID {
		return ErrRGB11Inconsistent
	}
	if err := VerifySignedTx(tx, prevFetcher); err != nil {
		return err
	}
	var signed bytes.Buffer
	if err := tx.Serialize(&signed); err != nil {
		return err
	}
	for _, item := range batch {
		item.SignedTx = append([]byte(nil), signed.Bytes()...)
		item.ChannelSend.Signed = true
	}
	return p.projectionStore.SavePendingTransferStates(batch)
}

func sameRGB11ChannelSend(a, b *rgb11wallet.ChannelSendData) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.ChannelID == b.ChannelID && a.Reason == b.Reason && a.PayFeeByLocal == b.PayFeeByLocal && a.Signed == b.Signed &&
		bytes.Equal(a.WitnessScript, b.WitnessScript) && bytes.Equal(a.PeerPubKey, b.PeerPubKey) && bytes.Equal(a.MoreData, b.MoreData)
}

// A peer may have broadcast successfully and lost its response. Recover the
// complete witness from Bitcoin using the already committed txid; never ask
// for a different transaction or release the shared RGB carrier.
func (p *rgb11Manager) recoverRGB11ChannelWitness(batch []*rgb11wallet.PendingTransfer) error {
	first := batch[0]
	if first.ChannelSend == nil || first.ChannelSend.Signed {
		return nil
	}
	raw, err := p.evidence.GetRawTx(first.State.WitnessTxID)
	if err != nil {
		return err
	}
	tx := wire.NewMsgTx(2)
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		return err
	}
	if tx.TxID() != first.State.WitnessTxID {
		return ErrRGB11Inconsistent
	}
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(first.SignedPSBT), false)
	if err != nil {
		return err
	}
	if packet.UnsignedTx.TxID() != tx.TxID() || len(packet.Inputs) != len(tx.TxIn) {
		return ErrRGB11Inconsistent
	}
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	for i, input := range tx.TxIn {
		if packet.Inputs[i].WitnessUtxo == nil {
			return ErrRGB11Inconsistent
		}
		prevFetcher.AddPrevOut(input.PreviousOutPoint, packet.Inputs[i].WitnessUtxo)
	}
	if err := VerifySignedTx(tx, prevFetcher); err != nil {
		return err
	}
	for _, item := range batch {
		if !sameRGB11ChannelSend(first.ChannelSend, item.ChannelSend) {
			return ErrRGB11Inconsistent
		}
	}
	for _, item := range batch {
		item.SignedTx = append([]byte(nil), raw...)
		item.ChannelSend.Signed = true
	}
	return p.projectionStore.SavePendingTransferStates(batch)
}
