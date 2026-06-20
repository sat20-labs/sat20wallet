package wallet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	ChannelL2DrainReason = "channel_l2_drain"
	zeroL1TxId           = "0000000000000000000000000000000000000000000000000000000000000000"
)

type channelL2Residual struct {
	Assets  []string
	Utxos   []string
	Outputs []*sindexer.TxOutput
}

func (r *channelL2Residual) Empty() bool {
	return r == nil || (len(r.Assets) == 0 && len(r.Utxos) == 0)
}

func (p *Manager) EnsureChannelL2DrainedBeforeOpen(channelAddr, pendingDrainTxId string) error {
	if channelAddr == "" {
		return fmt.Errorf("channel address is required")
	}
	if p.getChannel(channelAddr) != nil {
		return nil
	}
	hasResidual, residualAssets, err := p.hasChannelL2Residual(channelAddr)
	if err != nil {
		return err
	}
	if !hasResidual {
		return nil
	}
	if pendingDrainTxId != "" {
		expectedTxId, err := p.expectedChannelL2DrainTxId(channelAddr)
		if err != nil {
			return err
		}
		if expectedTxId == "" {
			return nil
		}
		if expectedTxId != pendingDrainTxId {
			return fmt.Errorf("CHANNEL_L2_DRAIN_REQUIRED channel %s pending drain tx %s mismatch expected %s",
				channelAddr, pendingDrainTxId, expectedTxId)
		}
		Log.Warnf("channel %s still reports L2 residual assets after verified drain tx %s broadcasted; allow open to continue",
			channelAddr, pendingDrainTxId)
		return nil
	}
	return fmt.Errorf("CHANNEL_L2_DRAIN_REQUIRED channel %s has L2 residual assets (%s); call DrainChannelL2BeforeReopen before open/reopen",
		channelAddr, strings.Join(residualAssets, ","))
}

func (p *Manager) expectedChannelL2DrainTxId(channelAddr string) (string, error) {
	tx, _, _, err := p.buildChannelL2DrainDeAnchorTx(channelAddr)
	if err != nil {
		return "", err
	}
	if tx == nil {
		return "", nil
	}
	return tx.TxID(), nil
}

func (p *Manager) DrainChannelL2BeforeOpenIfNeeded(channelAddr string) (string, error) {
	hasResidual, residualAssets, err := p.hasChannelL2Residual(channelAddr)
	if err != nil {
		return "", err
	}
	if !hasResidual {
		return "", nil
	}
	txId, drainedUtxos, err := p.DrainChannelL2BeforeReopen(channelAddr)
	if err != nil {
		return "", err
	}
	if txId == "" {
		return "", fmt.Errorf("CHANNEL_L2_DRAIN_REQUIRED channel %s has L2 residual assets (%s) but no drain tx was broadcasted",
			channelAddr, strings.Join(residualAssets, ","))
	}
	Log.Infof("channel %s L2 residual assets drained by tx %s before open/reopen, utxos %v",
		channelAddr, txId, drainedUtxos)
	return txId, nil
}

func (p *Manager) hasChannelL2Residual(channelAddr string) (bool, []string, error) {
	if p.l2IndexerClient == nil {
		return false, nil, fmt.Errorf("L2 indexer is not configured")
	}
	if err := p.l2IndexerClient.Ping(); err != nil {
		return false, nil, fmt.Errorf("verify channel %s L2 residual assets failed: %v", channelAddr, err)
	}
	summary := p.l2IndexerClient.GetAssetSummaryWithAddress(channelAddr)
	if summary == nil || len(summary.Data) == 0 {
		return false, nil, nil
	}
	assets := channelL2ResidualAssetNames(summary)
	return len(assets) != 0, assets, nil
}

func ChannelL2ResidualAssetNames(summary *indexerwire.AssetSummary) []string {
	if summary == nil || len(summary.Data) == 0 {
		return nil
	}
	assets := make([]string, 0, len(summary.Data))
	for _, asset := range summary.Data {
		if asset == nil || asset.Amount.IsZero() {
			continue
		}
		assets = append(assets, asset.Name.String())
	}
	sort.Strings(assets)
	return assets
}

func channelL2ResidualAssetNames(summary *indexerwire.AssetSummary) []string {
	return ChannelL2ResidualAssetNames(summary)
}

func (p *Manager) getChannelL2Residual(channelAddr string) (*channelL2Residual, error) {
	if p.l2IndexerClient == nil {
		return nil, fmt.Errorf("L2 indexer is not configured")
	}
	if err := p.l2IndexerClient.Ping(); err != nil {
		return nil, fmt.Errorf("verify channel %s L2 residual assets failed: %v", channelAddr, err)
	}
	outpoints := make(map[string]bool)
	assetNames := make(map[string]bool)
	residual := &channelL2Residual{}
	for _, utxo := range p.l2IndexerClient.GetAllUtxosWithAddress(channelAddr) {
		if utxo == nil || utxo.OutPoint == "" || outpoints[utxo.OutPoint] {
			continue
		}
		output := OutputInfoToOutput_SatsNet(utxo)
		if output == nil || output.Zero() {
			continue
		}
		outpoints[utxo.OutPoint] = true
		residual.Utxos = append(residual.Utxos, utxo.OutPoint)
		residual.Outputs = append(residual.Outputs, output)

		if utxo.Value > 0 {
			assetNames[ASSET_PLAIN_SAT.String()] = true
		}
		for _, asset := range utxo.Assets {
			if asset == nil {
				continue
			}
			assetNames[asset.AssetName.String()] = true
		}
	}
	for name := range assetNames {
		residual.Assets = append(residual.Assets, name)
	}
	sort.Strings(residual.Assets)
	sort.Slice(residual.Outputs, func(i, j int) bool {
		return residual.Outputs[i].OutPointStr < residual.Outputs[j].OutPointStr
	})
	residual.Utxos = residual.Utxos[:0]
	for _, output := range residual.Outputs {
		residual.Utxos = append(residual.Utxos, output.OutPointStr)
	}
	return residual, nil
}

func (p *Manager) buildChannelL2DrainDeAnchorTx(channelAddr string) (*swire.MsgTx, stxscript.PrevOutputFetcher, []string, error) {
	residual, err := p.getChannelL2Residual(channelAddr)
	if err != nil {
		return nil, nil, nil, err
	}
	if residual.Empty() {
		return nil, nil, nil, nil
	}

	tx := swire.NewMsgTx(swire.TxVersion)
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	total := sindexer.NewTxOutput(0)

	for _, output := range residual.Outputs {
		outpoint := output.OutPointStr
		if p.GetUtxoLocker_SatsNet().IsLocked(outpoint) {
			return nil, nil, nil, fmt.Errorf("L2 channel residual utxo %s is locked", outpoint)
		}
		out := output.OutPoint()
		tx.AddTxIn(swire.NewTxIn(out, nil, nil))
		prevFetcher.AddPrevOut(*out, &output.OutValue)
		if err := total.Merge(output); err != nil {
			return nil, nil, nil, err
		}
	}
	if len(tx.TxIn) == 0 || total.Zero() {
		return nil, nil, nil, nil
	}

	l1TxId := p.latestObservedRevokedCommitmentTxId(channelAddr)
	if l1TxId == "" {
		l1TxId = zeroL1TxId
	}
	payload, err := sindexer.EncodeDescendPayloadV2(l1TxId, sindexer.DESCEND_OP_FORCE_CLOSE, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("EncodeDescendPayloadV2 failed: %v", err)
	}
	nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DESCENDING, payload)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("NullDataScript descending failed: %v", err)
	}
	tx.AddTxOut(swire.NewTxOut(total.Value(), GenTxAssetsFromAssets(total.OutValue.Assets), nullDataScript))

	channelScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_CHANNELID, []byte(channelAddr))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("NullDataScript channel failed: %v", err)
	}
	tx.AddTxOut(swire.NewTxOut(0, nil, channelScript))

	PrintJsonTx_SatsNet(tx, "channel l2 drain deAnchor")
	return tx, prevFetcher, residual.Utxos, nil
}

func (p *Manager) latestObservedRevokedCommitmentTxId(channelAddr string) string {
	events, err := p.l2IndexerClient.GetChannelStateEvents(channelAddr)
	if err != nil {
		Log.Warnf("GetChannelStateEvents %s failed: %v", channelAddr, err)
		return ""
	}
	var latest *sindexer.ChannelStateEvent
	for _, event := range events {
		if event == nil || event.EventType != sindexer.CHANNEL_EVENT_REVOKED_COMMITMENT_BROADCASTED {
			continue
		}
		if latest == nil || event.CreatedAt > latest.CreatedAt {
			latest = event
		}
	}
	if latest == nil {
		return ""
	}
	return latest.ObservedL1TxId
}

func (p *Manager) DrainChannelL2BeforeReopen(channelAddr string) (string, []string, error) {
	if p.wallet == nil {
		return "", nil, fmt.Errorf("wallet is not created/unlocked")
	}
	if !p.IsReady() {
		return "", nil, fmt.Errorf("not ready")
	}
	if channelAddr == "" {
		var err error
		channelAddr, err = p.GetChannelAddress()
		if err != nil {
			return "", nil, err
		}
	}
	if p.getChannel(channelAddr) != nil {
		return "", nil, fmt.Errorf("channel %s is active, not allow to drain L2 assets", channelAddr)
	}
	tx, prevFetcher, utxos, err := p.buildChannelL2DrainDeAnchorTx(channelAddr)
	if err != nil {
		return "", nil, err
	}
	if tx == nil {
		Log.Infof("channel %s has no L2 residual assets to drain", channelAddr)
		return "", nil, nil
	}

	localWallet := p.wallet
	peerPubKey := p.GetServerNode().Pubkey.SerializeCompressed()
	localKey := localWallet.GetPaymentPubKey().SerializeCompressed()
	witness, pkScript, err := GetP2WSHscript(localKey, peerPubKey)
	if err != nil {
		return "", nil, err
	}
	derivedAddr, err := GetP2WSHaddressFromScript(pkScript)
	if err != nil {
		return "", nil, err
	}
	if derivedAddr != channelAddr {
		return "", nil, fmt.Errorf("invalid channel address %s, derived %s", channelAddr, derivedAddr)
	}

	sigs, err := PartialSignTxWithWallet_SatsNet(localWallet, tx, prevFetcher, witness, peerPubKey)
	if err != nil {
		return "", nil, fmt.Errorf("PartialSignTxWithWallet_SatsNet failed: %v", err)
	}
	txHex, err := EncodeMsgTx_SatsNet(tx)
	if err != nil {
		return "", nil, err
	}
	txsSignInfo := []*wwire.TxSignInfo{{
		Tx:        txHex,
		L1Tx:      false,
		LocalSigs: sigs,
		Reason:    ChannelL2DrainReason,
	}}
	moredata := wwire.RemoteSignMoreData{
		Tx:      txsSignInfo,
		Witness: witness,
		Action:  ChannelL2DrainReason,
	}
	md, err := json.Marshal(moredata)
	if err != nil {
		return "", nil, err
	}
	req := wwire.SignRequest{
		MsgHeader:    wwire.NewMsgHeader(),
		ChannelId:    channelAddr,
		CommitHeight: -1,
		Reason:       ChannelL2DrainReason,
		MoreData:     md,
		PubKey:       localKey,
		NodeId:       localWallet.GetNodePubKey().SerializeCompressed(),
	}
	msg, err := json.Marshal(req)
	if err != nil {
		return "", nil, err
	}
	msgSig, err := localWallet.SignMessageWithIndex(msg, 0)
	if err != nil {
		return "", nil, err
	}
	peerSig, err := p.serverNode.client.SendSigReq(&req, msgSig)
	if err != nil {
		return "", nil, fmt.Errorf("SendSigReq failed: %v", err)
	}
	if len(peerSig) != 1 {
		return "", nil, fmt.Errorf("invalid peer signature count %d", len(peerSig))
	}
	_, err = FinalSignTxWithWallet_SatsNet(localWallet, tx, prevFetcher, witness, peerPubKey, peerSig[0])
	if err != nil {
		return "", nil, err
	}
	if err := p.TestAcceptance_SatsNet([]*swire.MsgTx{tx}); err != nil {
		return "", nil, err
	}
	txId, err := p.BroadcastTx_SatsNet(tx)
	if err != nil {
		return "", nil, err
	}
	p.RecordChannelL2Drained(channelAddr, txId)
	return txId, utxos, nil
}

func (p *Manager) RecordChannelL2Drained(channelAddr, txId string) {
	if channelAddr == "" || txId == "" || p.l2IndexerClient == nil {
		return
	}
	event := &sindexer.ChannelStateEvent{
		ChannelId:      channelAddr,
		EventType:      sindexer.CHANNEL_EVENT_L2_DRAINED,
		Status:         sindexer.CHANNEL_EVENT_STATUS_DRAINED,
		ObservedL1TxId: zeroL1TxId,
		Source:         "stp_reopen_recovery",
		Message:        fmt.Sprintf("channel L2 residual assets drained by deAnchor tx %s before open/reopen", txId),
		CreatedAt:      time.Now().Unix(),
	}
	if err := p.RecordChannelStateEvent(event); err != nil {
		Log.Warnf("RecordChannelStateEvent L2 drained %s failed: %v", channelAddr, err)
	}
}

func (p *Manager) RecordChannelStateEvent(event *sindexer.ChannelStateEvent) error {
	if event == nil || p.l2IndexerClient == nil {
		return nil
	}
	if !p.IsCoreNode() {
		Log.Warnf("skip channel state event %s for %s: only core node can record events", event.EventType, event.ChannelId)
		return nil
	}
	if p.wallet == nil {
		return fmt.Errorf("wallet is not created/unlocked")
	}
	pubkey := p.wallet.GetNodePubKey().SerializeCompressed()
	msg, err := json.Marshal(event)
	if err != nil {
		return err
	}
	sig, err := p.wallet.SignMessageWithIndex(msg, 0)
	if err != nil {
		return err
	}
	return p.l2IndexerClient.RecordChannelStateEvent(event, pubkey, sig)
}

func (p *Manager) validateChannelL2DrainTxInputs(tx *swire.MsgTx, channelPkScript []byte) (stxscript.PrevOutputFetcher, *sindexer.TxOutput, error) {
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	total := sindexer.NewTxOutput(0)
	for _, txIn := range tx.TxIn {
		txOut, err := p.l2IndexerClient.GetTxOutput(txIn.PreviousOutPoint.String())
		if err != nil {
			return nil, nil, fmt.Errorf("GetTxOutput %s failed: %v", txIn.PreviousOutPoint.String(), err)
		}
		output := OutputToSatsNet(txOut)
		if output == nil || output.Zero() {
			return nil, nil, fmt.Errorf("channel l2 drain input %s is empty", txIn.PreviousOutPoint.String())
		}
		if !bytes.Equal(output.OutValue.PkScript, channelPkScript) {
			return nil, nil, fmt.Errorf("channel l2 drain input %s is not from channel address", txIn.PreviousOutPoint.String())
		}
		prevFetcher.AddPrevOut(txIn.PreviousOutPoint, &output.OutValue)
		if err := total.Merge(output); err != nil {
			return nil, nil, err
		}
	}
	return prevFetcher, total, nil
}
