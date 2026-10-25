package wallet

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/btcsuite/btcd/txscript"
)

// A transient UI preview only. Nothing here creates a recovery choice,
// reserves coins, signs, or broadcasts. C2 revalidates again on explicit action.
type closedChannelRecoveryOptions struct {
	WalletID            int64    `json:"walletId"`
	AccountIndex        uint32   `json:"accountIndex"`
	ChannelID           string   `json:"channelId"`
	OldParentID         string   `json:"oldParentId"`
	HasLocalClosedState bool     `json:"hasLocalClosedState"`
	CanWithdraw         bool     `json:"canWithdraw"`
	WithdrawReason      string   `json:"withdrawReason"`
	WithdrawFeeSats     int64    `json:"withdrawFeeSats"`
	WithdrawTxCount     int      `json:"withdrawTxCount"`
	WithdrawTxIDs       []string `json:"withdrawTxIds"`
	WithdrawPending     bool     `json:"withdrawPending"`
	WithdrawComplete    bool     `json:"withdrawComplete"`
}

func (p *Manager) PreviewClosedChannelRecovery(channelID string) (string, error) {
	w, err := p.captureWalletIdentity()
	if err != nil {
		return "", err
	}
	id := w.GetWalletId()
	if channelID == "" {
		channelID, err = p.channelIDForWallet(w)
		if err != nil {
			return "", err
		}
	}
	options := closedChannelRecoveryOptions{WalletID: id.Id, AccountIndex: id.SubAccountId, ChannelID: channelID,
		WithdrawReason: "No complete verified local withdrawal package is available; online rebuilding or the required local recovery material is needed."}
	result := func() (string, error) { raw, err := json.Marshal(options); return string(raw), err }
	if channelID == "" || p.db == nil {
		return result()
	}
	active := p.GetActiveChannelWithId(channelID)
	stored, err := p.LoadChannelInDB(channelID)
	if err != nil {
		options.WithdrawReason = "The local closed-channel state is unavailable; connect online to rebuild or restore the required local recovery material."
		return result()
	}
	remote := stored.RemoteCommitment
	if !stored.IsInitiator || stored.ChannelId != channelID || stored.LocalWalletId != w.GetId() ||
		stored.LocalChanCfg.WalletId != w.GetSubAccount() || stored.LocalChanCfg.PaymentKey == nil || !stored.LocalChanCfg.PaymentKey.IsEqual(w.GetPaymentPubKey()) || remote == nil || remote.CommitTx == nil {
		options.WithdrawReason = "This account has no matching local closed-channel recovery state."
		return result()
	}
	options.OldParentID = remote.CommitTx.TxID()
	options.HasLocalClosedState = stored.Status <= CS_CLOSED && active == nil
	var recoveryContext *closedChannelIntent
	for _, item := range p.GetLocalActionReservations() {
		owner, ok := item.(*LocalActionPerformData)
		if !ok || owner.Action != localActionClosedChannelRecovery || !closedIntentWalletMatches(owner, w) {
			continue
		}
		owner.RLock()
		intent, parseErr := loadClosedChannelIntent(owner)
		status := owner.Status
		owner.RUnlock()
		if parseErr != nil {
			return "", parseErr
		}
		if intent.ChannelID != channelID {
			continue
		}
		terminal := status == closedIntentWithdrawn || status == closedIntentReopened
		if terminal && intent.OldParentID != options.OldParentID {
			continue
		}
		if status == closedIntentWithdrawn && intent.OldParentID == options.OldParentID {
			options.WithdrawComplete = true
			options.WithdrawReason = "The original withdrawal is already complete."
			return result()
		}
		if !terminal && (intent.OldParentID != options.OldParentID || intent.Choice != "withdraw") {
			options.WithdrawReason = "The original rebuild or another recovery is pending; its new funds are not protected by old Others."
			return result()
		}
		if !terminal && intent.Choice == "withdraw" {
			options.WithdrawPending = true
			recoveryContext = intent
		}
	}
	if (stored.Status > CS_CLOSED || active != nil) && !options.WithdrawPending {
		options.WithdrawReason = "The channel is still active; an offline peer alone does not permit withdrawal."
		return result()
	}
	options.HasLocalClosedState = true
	rgb := closedWithdrawalHasRGB(stored)
	if !rgb && !closedWithdrawalUsesInputFacts(stored) && nativeForceClose(&ClosingReservation{Channel: &Channel{ChannelInDB: *stored}}) {
		options.WithdrawReason = "This native or mixed-protocol withdrawal package is not supported by this action."
		return result()
	}
	p.mutex.RLock()
	manager := p.l1IndexerClient
	p.mutex.RUnlock()
	if manager == nil {
		options.WithdrawReason = "Bitcoin evidence is unavailable."
		return result()
	}
	manager.mutex.RLock()
	rpc := manager.active
	if rpc == nil && len(manager.indexers) > 0 {
		rpc = manager.indexers[0]
	}
	manager.mutex.RUnlock()
	if rpc == nil || !rpc.IsTxConfirmed(options.OldParentID) {
		options.WithdrawReason = "The exact server closing parent is not confirmed."
		return result()
	}
	height, err := rpc.GetTxHeight(options.OldParentID)
	if err != nil || height <= 0 {
		options.WithdrawReason = "The confirmed server closing height is unavailable."
		return result()
	}
	canonicalRaw, err := rpc.GetRawTx(options.OldParentID)
	if err != nil {
		options.WithdrawReason = "The canonical server closing parent is unavailable."
		return result()
	}
	parent, err := DecodeMsgTx(canonicalRaw)
	if err != nil || parent.TxID() != options.OldParentID {
		options.WithdrawReason = "The canonical closing parent does not match local state."
		return result()
	}
	material, err := p.prepareClosedWithdrawal(&Channel{ChannelInDB: *stored, manager: p, localWallet: w.Clone()}, parent, recoveryContext)
	if err != nil {
		if rgb {
			options.WithdrawReason = err.Error()
		}
		return result()
	}
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	AddTxOutputsToPrevFetcher(fetcher, parent)
	for i, tx := range material.PreTxs {
		if i != 0 {
			for _, input := range tx.TxIn {
				if input == nil || fetcher.FetchPrevOutput(input.PreviousOutPoint) == nil {
					options.WithdrawReason = "The local withdrawal package is missing an input dependency."
					return result()
				}
			}
			fee, err := VerifySignedTxV2(tx, fetcher)
			if err != nil {
				options.WithdrawReason = "The local withdrawal package is not completely signed or has invalid Bitcoin values."
				return result()
			}
			options.WithdrawFeeSats += fee
			options.WithdrawTxIDs = append(options.WithdrawTxIDs, tx.TxID())
		}
		AddTxOutputsToPrevFetcher(fetcher, tx)
	}
	p.channelIdentityMu.RLock()
	p.mutex.RLock()
	same := p.wallet != nil && p.wallet.GetWalletId() == id && p.wallet.GetPaymentPubKey() != nil && bytes.Equal(p.wallet.GetPaymentPubKey().SerializeCompressed(), w.GetPaymentPubKey().SerializeCompressed())
	p.mutex.RUnlock()
	p.channelIdentityMu.RUnlock()
	if !same {
		return "", fmt.Errorf("wallet account changed; discard this recovery preview")
	}
	options.WithdrawTxCount = len(options.WithdrawTxIDs)
	options.CanWithdraw = true
	options.WithdrawReason = ""
	return result()
}
