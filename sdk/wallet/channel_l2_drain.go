package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
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

// An address balance is not evidence of a closed channel. Only an absent
// local channel AND absent indexed history permit a first opening.
func (p *Manager) channelL2FirstOpening(channel string) (bool, error) {
	if p.db == nil || p.l2IndexerClient == nil {
		return false, fmt.Errorf("channel history unavailable")
	}
	old, err := p.LoadChannelInDB(channel)
	if err != nil && !errors.Is(err, indexer.ErrKeyNotFound) {
		return false, err
	}
	ledger, err := p.l2IndexerClient.GetChannelLedger(channel)
	if err != nil {
		return false, err
	}
	events, err := p.l2IndexerClient.GetChannelStateEvents(channel)
	if err != nil {
		return false, err
	}
	if old == nil {
		if len(events) != 0 {
			return false, fmt.Errorf("channel history exists without local recovery material")
		}
		for _, entry := range ledger {
			if entry == nil || entry.Direction != "ascending" || entry.L2TxId == "" {
				return false, fmt.Errorf("channel history exists without local recovery material")
			}
			err := p.rejectBTCLuckyOutput(entry.L2TxId+":0", nil)
			if errors.Is(err, errBTCLuckyChannelInput) {
				continue
			}
			if err != nil {
				return false, fmt.Errorf("channel history cannot be classified: %w", err)
			}
			return false, fmt.Errorf("channel history exists without local recovery material")
		}
	}
	return old == nil, nil
}

func (p *Manager) EnsureChannelL2DrainedBeforeOpen(channelAddr, pendingDrainTxId string) error {
	first, err := p.channelL2FirstOpening(channelAddr)
	if err != nil {
		return err
	}
	if first {
		if pendingDrainTxId != "" {
			return fmt.Errorf("first opening must not declare a drain")
		}
		return nil
	}
	tx, _, _, err := p.buildChannelL2DrainDeAnchorTx(channelAddr)
	if err != nil {
		return err
	}
	if tx == nil {
		return nil
	}
	if pendingDrainTxId != "" && pendingDrainTxId == tx.TxID() {
		return nil
	}
	return fmt.Errorf("CHANNEL_L2_DRAIN_REQUIRED channel %s has verified closing inputs to drain", channelAddr)
}

func (p *Manager) DrainChannelL2BeforeOpenIfNeeded(channelAddr string) (string, error) {
	first, err := p.channelL2FirstOpening(channelAddr)
	if err != nil || first {
		return "", err
	}
	txId, _, err := p.DrainChannelL2BeforeReopen(channelAddr)
	return txId, err
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
	l1TxId, allowed, err := p.channelL2DrainEvidence(channelAddr)
	if err != nil {
		return nil, nil, nil, err
	}

	tx := swire.NewMsgTx(swire.TxVersion)
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	total := sindexer.NewTxOutput(0)
	var drained []string

	for _, output := range residual.Outputs {
		if !allowed[output.OutPointStr] {
			continue
		}
		outpoint := output.OutPointStr
		if p.GetUtxoLocker_SatsNet().IsLocked(outpoint) {
			return nil, nil, nil, fmt.Errorf("L2 channel residual utxo %s is locked", outpoint)
		}
		drained = append(drained, outpoint)
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
	return tx, prevFetcher, drained, nil
}

// Use an exact locally retained settlement, never an address-wide balance or
// an unverified event. Missing material requires recovery, not a synthetic burn.
func (p *Manager) channelL2DrainEvidence(channel string) (string, map[string]bool, error) {
	if p.db == nil || p.l1IndexerClient == nil {
		return "", nil, fmt.Errorf("closing evidence unavailable")
	}
	// This validator also runs inside an existing recovery owner. Do not
	// invoke the entry-point lifecycle gate, which would reject that owner.
	stored, err := p.LoadChannelInDB(channel)
	if err != nil {
		return "", nil, err
	}
	if stored == nil || stored.ChannelId != channel || stored.Status > CS_CLOSED {
		return "", nil, fmt.Errorf("closed channel evidence unavailable for %s", channel)
	}
	if recovered, err := p.loadSTPNativeRevocationRecovery(stored); err != nil {
		return "", nil, err
	} else if recovered != nil {
		return "", nil, fmt.Errorf("unresolved revoked commitment")
	}
	if prepared, err := p.readSTPNativePreparedRecovery(&Channel{ChannelInDB: *stored}); err != nil {
		return "", nil, err
	} else if prepared != nil {
		return "", nil, fmt.Errorf("unresolved prepared commitment")
	}
	type settlement struct {
		id   string
		burn *swire.MsgTx
	}
	var candidates []settlement
	if stored.ClosingTx != nil {
		candidates = append(candidates, settlement{stored.ClosingTx.TxID(), stored.DeAnchorTx})
	}
	// ClosingTx advances to the sweep transaction after force-close. The
	// original confirmed commitment and its paired burn remain authoritative.
	for _, c := range []*ChannelCommitment{stored.LocalCommitment, stored.RemoteCommitment} {
		if c != nil && c.CommitTx != nil {
			candidates = append(candidates, settlement{c.CommitTx.TxID(), c.DeAnchorTx})
		}
	}
	rpc := p.l1IndexerClient.getActiveIndexer()
	if rpc == nil {
		return "", nil, fmt.Errorf("closing indexer unavailable")
	}
	tip, err := readChannelChainTip(rpc)
	if err != nil {
		return "", nil, err
	}
	for _, c := range candidates {
		if c.burn == nil || len(c.burn.TxIn) == 0 || c.id == zeroL1TxId {
			continue
		}
		bound := false
		for _, out := range c.burn.TxOut {
			kind, data, err := sindexer.ReadDataFromNullDataScript(out.PkScript)
			if err != nil || kind != sindexer.CONTENT_TYPE_DESCENDING {
				continue
			}
			payload, err := sindexer.ParseDescendPayload(data)
			if err == nil && payload.L1TxId == c.id {
				bound = true
			}
		}
		if !bound {
			continue
		}
		info, err := rpc.GetTxInfo(c.id)
		if err != nil || info == nil || info.Confirmations <= 0 || info.BlockHeight < 0 || info.BlockHeight > int64(tip.height) {
			continue
		}
		raw, err := rpc.GetRawTx(c.id)
		if err != nil {
			return "", nil, err
		}
		actual, err := DecodeMsgTx(raw)
		if err != nil || actual.TxID() != c.id {
			return "", nil, fmt.Errorf("closing transaction mismatch")
		}
		end, err := readChannelChainTip(rpc)
		if err != nil || end != tip {
			return "", nil, fmt.Errorf("closing chain changed during validation")
		}
		inputs := make(map[string]bool)
		for _, in := range c.burn.TxIn {
			inputs[in.PreviousOutPoint.String()] = true
		}
		return c.id, inputs, nil
	}
	return "", nil, fmt.Errorf("confirmed closing transaction and matching L2 inputs required for %s", channel)
}

// ValidateChannelL2DrainEvidence is shared by the signer and every broadcast
// path, including recovery of an already signed transaction.
func (p *Manager) ValidateChannelL2DrainEvidence(channel string, tx *swire.MsgTx) error {
	id, allowed, err := p.channelL2DrainEvidence(channel)
	if err != nil {
		return err
	}
	if tx == nil || len(tx.TxIn) == 0 || len(tx.TxOut) != 2 {
		return fmt.Errorf("invalid closing drain")
	}
	seen := make(map[string]bool)
	for _, in := range tx.TxIn {
		point := in.PreviousOutPoint.String()
		if !allowed[point] || seen[point] {
			return fmt.Errorf("drain input %s is outside closing material", point)
		}
		seen[point] = true
	}
	kind, data, err := sindexer.ReadDataFromNullDataScript(tx.TxOut[0].PkScript)
	if err != nil || kind != sindexer.CONTENT_TYPE_DESCENDING {
		return fmt.Errorf("invalid closing drain output")
	}
	payload, err := sindexer.ParseDescendPayload(data)
	if err != nil || payload.L1TxId != id || payload.Operation != sindexer.DESCEND_OP_FORCE_CLOSE || len(payload.ReturnedOutputVouts) != 0 {
		return fmt.Errorf("drain does not match confirmed closing transaction")
	}
	kind, data, err = sindexer.ReadDataFromNullDataScript(tx.TxOut[1].PkScript)
	if err != nil || kind != sindexer.CONTENT_TYPE_CHANNELID || string(data) != channel || tx.TxOut[1].Value != 0 || len(tx.TxOut[1].Assets) != 0 {
		return fmt.Errorf("invalid closing drain channel marker")
	}
	return nil
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
	if err := p.ValidateChannelL2DrainEvidence(channelAddr, tx); err != nil {
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
	var w common.Wallet
	if p.wallet != nil {
		w = p.wallet.Clone()
	}
	p.recordChannelL2DrainedForWallet(w, channelAddr, txId)
}

func (p *Manager) recordChannelL2DrainedForWallet(w common.Wallet, channelAddr, txId string) {
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
	if err := p.recordChannelStateEventForWallet(w, event); err != nil {
		Log.Warnf("RecordChannelStateEvent L2 drained %s failed: %v", channelAddr, err)
	}
}

func (p *Manager) RecordChannelStateEvent(event *sindexer.ChannelStateEvent) error {
	var w common.Wallet
	if p.wallet != nil {
		w = p.wallet.Clone()
	}
	return p.recordChannelStateEventForWallet(w, event)
}

func (p *Manager) recordChannelStateEventForWallet(w common.Wallet, event *sindexer.ChannelStateEvent) error {
	if event == nil || p.l2IndexerClient == nil {
		return nil
	}
	if !p.IsCoreNode() {
		Log.Warnf("skip channel state event %s for %s: only core node can record events", event.EventType, event.ChannelId)
		return nil
	}
	if w == nil {
		return fmt.Errorf("wallet is not created/unlocked")
	}
	pubkey := w.GetNodePubKey().SerializeCompressed()
	msg, err := json.Marshal(event)
	if err != nil {
		return err
	}
	sig, err := w.SignMessageWithIndex(msg, 0)
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
