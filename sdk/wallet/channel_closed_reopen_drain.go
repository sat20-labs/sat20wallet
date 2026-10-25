package wallet

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Only the existing reopen drain is journaled here. This does not introduce a
// new remote reason or permit a quote to sign/broadcast anything.
func (p *Manager) drainClosedChannelBeforeReopen(w common.Wallet, client NodeRPCClient, owner *LocalActionPerformData, intent *closedChannelIntent) (string, error) {
	if !closedIntentWalletMatches(owner, w) || intent.Choice != "reopen" {
		return "", fmt.Errorf("closed-channel drain account mismatch")
	}
	if intent.Step == "drain_done" {
		return intent.L2DrainTxID, nil
	}
	if intent.Step == "drain_inflight" {
		return "", closedChannelRecoveryPending(owner, intent.Step, nil)
	}
	if intent.Step == "drain_signed" || intent.Step == "drain_broadcast_inflight" {
		tx, err := DecodeMsgTx_SatsNet(string(intent.DrainSnapshot))
		if err != nil {
			return "", err
		}
		intent.Step = "drain_broadcast_inflight"
		if err := p.persistClosedChannelIntent(owner, intent, RS_PERFORM_ACTION_RUN_STARTED); err != nil {
			return "", err
		}
		id, err := p.BroadcastTx_SatsNet(tx)
		if err != nil {
			return "", closedChannelRecoveryPending(owner, intent.Step, err)
		}
		intent.L2DrainTxID, intent.Step = id, "drain_done"
		if err := p.persistClosedChannelIntent(owner, intent, RS_PERFORM_ACTION_STARTED); err != nil {
			return "", err
		}
		p.recordChannelL2DrainedForWallet(w, intent.ChannelID, id)
		return id, nil
	}
	if intent.Step != "prepared" && intent.Step != "open_rejected" {
		return "", closedChannelRecoveryPending(owner, intent.Step, nil)
	}
	hasResidual, residualAssets, err := p.hasChannelL2Residual(intent.ChannelID)
	if err != nil {
		return "", err
	}
	if !hasResidual {
		return "", nil
	}
	tx, fetcher, _, err := p.buildChannelL2DrainDeAnchorTx(intent.ChannelID)
	if err != nil {
		return "", err
	}
	if tx == nil {
		return "", fmt.Errorf("CHANNEL_L2_DRAIN_REQUIRED channel %s has L2 residual assets (%s) but no drain tx was broadcasted",
			intent.ChannelID, strings.Join(residualAssets, ","))
	}
	peerKey := append([]byte(nil), p.serverNode.Pubkey.SerializeCompressed()...)
	localKey := w.GetPaymentPubKey().SerializeCompressed()
	witness, script, err := GetP2WSHscript(localKey, peerKey)
	if err != nil {
		return "", err
	}
	derived, err := GetP2WSHaddressFromScript(script)
	if err != nil || derived != intent.ChannelID {
		return "", fmt.Errorf("closed-channel drain payment keys mismatch")
	}
	sigs, err := PartialSignTxWithWallet_SatsNet(w, tx, fetcher, witness, peerKey)
	if err != nil {
		return "", err
	}
	hexTx, err := EncodeMsgTx_SatsNet(tx)
	if err != nil {
		return "", err
	}
	more, err := json.Marshal(wwire.RemoteSignMoreData{Tx: []*wwire.TxSignInfo{{Tx: hexTx, L1Tx: false, LocalSigs: sigs, Reason: ChannelL2DrainReason}}, Witness: witness, Action: ChannelL2DrainReason})
	if err != nil {
		return "", err
	}
	req := &wwire.SignRequest{MsgHeader: wwire.NewMsgHeader(), ChannelId: intent.ChannelID, CommitHeight: -1,
		Reason: ChannelL2DrainReason, MoreData: more, PubKey: localKey, NodeId: w.GetNodePubKey().SerializeCompressed()}
	raw, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sig, err := w.SignMessageWithIndex(raw, 0)
	if err != nil {
		return "", err
	}
	intent.DrainRequest, intent.DrainRequestSignature, intent.DrainSnapshot = req, sig, []byte(hexTx)
	intent.Step = "drain_inflight"
	if err := p.persistClosedChannelIntent(owner, intent, RS_PERFORM_ACTION_RUN_STARTED); err != nil {
		return "", err
	}
	peerSigs, err := client.SendSigReq(req, sig)
	if err != nil {
		return "", closedChannelRecoveryPending(owner, intent.Step, err)
	}
	if len(peerSigs) != 1 {
		return "", closedChannelRecoveryPending(owner, intent.Step, fmt.Errorf("invalid drain signature count"))
	}
	if _, err := FinalSignTxWithWallet_SatsNet(w, tx, fetcher, witness, peerKey, peerSigs[0]); err != nil {
		return "", err
	}
	if err := VerifySignedTx_SatsNet(tx, fetcher); err != nil {
		return "", err
	}
	hexTx, err = EncodeMsgTx_SatsNet(tx)
	if err != nil {
		return "", err
	}
	intent.DrainSnapshot, intent.Step = []byte(hexTx), "drain_signed"
	if err := p.persistClosedChannelIntent(owner, intent, RS_PERFORM_ACTION_RUN_STARTED); err != nil {
		return "", err
	}
	if err := p.TestAcceptance_SatsNet([]*swire.MsgTx{tx}); err != nil {
		return "", err
	}
	return p.drainClosedChannelBeforeReopen(w, client, owner, intent)
}
