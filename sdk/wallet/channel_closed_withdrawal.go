package wallet

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// WithdrawClosedChannel is an explicit client choice, not the default reaction
// to a missing peer or an uncertain reopen. It needs the client's own complete
// signed package and the exact confirmed server commitment. No peer signing or
// server-side closed snapshot is requested.
func (p *Manager) WithdrawClosedChannel(channelID string) (int64, error) {
	return p.withdrawClosedChannelExactParent(channelID, "")
}

// The PWA confirms the exact parent it previewed, never a newer close under
// the same channel address. All ordinary C2 checks remain in the shared body.
func (p *Manager) WithdrawClosedChannelExact(channelID, expectedParentID string) (int64, error) {
	if expectedParentID == "" {
		return 0, fmt.Errorf("the reviewed server closing parent is required")
	}
	return p.withdrawClosedChannelExactParent(channelID, expectedParentID)
}

func (p *Manager) withdrawClosedChannelExactParent(channelID, expectedParentID string) (int64, error) {
	p.channelIdentityMu.RLock()
	p.mutex.RLock()
	if p.wallet == nil {
		p.mutex.RUnlock()
		p.channelIdentityMu.RUnlock()
		return 0, fmt.Errorf("wallet is not created/unlocked")
	}
	w := p.wallet.Clone()
	p.mutex.RUnlock()
	p.channelIdentityMu.RUnlock()
	if channelID == "" {
		return 0, fmt.Errorf("closed channel id is required")
	}
	raw, err := p.db.Read([]byte(GetChannelKey(channelID)))
	if err != nil {
		return 0, err
	}
	var stored ChannelInDB
	if err := DecodeFromBytes(raw, &stored); err != nil {
		return 0, err
	}
	remote := stored.RemoteCommitment
	if !stored.IsInitiator || stored.ChannelId != channelID || stored.LocalWalletId != w.GetId() || stored.LocalChanCfg.WalletId != w.GetSubAccount() || stored.LocalChanCfg.PaymentKey == nil || !stored.LocalChanCfg.PaymentKey.IsEqual(w.GetPaymentPubKey()) || remote == nil || remote.CommitTx == nil || len(remote.Others) == 0 && !closedWithdrawalHasRGB(&stored) {
		return 0, fmt.Errorf("this account has no stored server Other recovery package")
	}
	parentID := remote.CommitTx.TxID()
	if expectedParentID != "" && parentID != expectedParentID {
		return 0, fmt.Errorf("the reviewed closing parent or closed-channel state changed; review again")
	}
	// Look up an existing choice before any chain query; failed preparation
	// must not create a new permanent choice by itself.
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
			return 0, parseErr
		}
		if intent.ChannelID != channelID || intent.OldParentID != parentID {
			continue
		}
		if intent.Choice != "withdraw" {
			return 0, fmt.Errorf("closed-channel recovery already chose %s", intent.Choice)
		}
		id := closedWithdrawalChildID(owner)
		if status == closedIntentWithdrawn {
			return id, nil
		}
		if intent.Step == "withdraw_pending" {
			return id, p.retryClosedChannelWithdrawal(owner)
		}
		if intent.Step != "prepared" {
			return id, closedChannelRecoveryPending(owner, intent.Step, nil)
		}
	}
	if p.GetIndexerRPCClient() == nil || !p.GetIndexerRPCClient().IsTxConfirmed(parentID) {
		return 0, fmt.Errorf("stored server main commitment is awaiting confirmation")
	}
	canonicalRaw, err := p.GetIndexerRPCClient().GetRawTx(parentID)
	if err != nil {
		return 0, err
	}
	actual, err := DecodeMsgTx(canonicalRaw)
	if err != nil || actual.TxID() != parentID {
		return 0, fmt.Errorf("confirmed server main transaction is unavailable")
	}
	channel := &Channel{ChannelInDB: stored, manager: p, localWallet: w.Clone()}
	preparation := &closedChannelIntent{}
	material, err := p.prepareClosedWithdrawal(channel, actual, preparation)
	if err != nil {
		return 0, err
	}
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	AddTxOutputsToPrevFetcher(fetcher, actual)
	if preparation.InputFacts != nil {
		for _, fact := range preparation.InputFacts.Inputs {
			output := closedWithdrawalFactOutput(fact)
			fetcher.AddPrevOut(*output.OutPoint(), output.TxOut())
		}
	}
	ordered := make([]*wire.MsgTx, 0, len(material.PreTxs))
	for i, tx := range material.PreTxs {
		if i != 0 {
			for _, input := range tx.TxIn {
				if input == nil || fetcher.FetchPrevOutput(input.PreviousOutPoint) == nil {
					return 0, fmt.Errorf("closed withdrawal is missing an input dependency")
				}
			}
			if err := VerifySignedTx(tx, fetcher); err != nil {
				return 0, fmt.Errorf("stored Other recovery is not completely authorized: %w", err)
			}
		}
		ordered = append(ordered, tx.Copy())
		AddTxOutputsToPrevFetcher(fetcher, tx)
	}
	height, err := p.GetIndexerRPCClient().GetTxHeight(parentID)
	if err != nil || height <= 0 {
		return 0, fmt.Errorf("confirmed server main height is unavailable: %v", err)
	}
	owner, intent, err := p.ensureClosedChannelIntent(w, channelID, parentID, "withdraw")
	if err != nil {
		return 0, err
	}
	if intent.Step != "prepared" {
		return closedWithdrawalChildID(owner), closedChannelRecoveryPending(owner, intent.Step, nil)
	}
	intent.InputFacts = preparation.InputFacts
	id := p.GenerateNewResvId()
	material.ReservationBase = NewReservationBase(id, false, ResvStatus(CS_CLOSE_FORCELY_SWEEP_BROADCASTED), w)
	material.ChannelId, material.CloseHeight, material.PreTxs = channelID, height, ordered
	closing := &ClosingReservation{ClosingDataInDB: *material, Channel: channel}
	if err := p.persistClosedWithdrawal(owner, intent, closing, raw, false); err != nil {
		return 0, err
	}
	return id, p.retryClosedChannelWithdrawal(owner)
}

// A reserved slot is not an empty recovery. Reuse the existing strict Other
// package decoder for every slot, including on restart, before any completion.
func closedWithdrawalPackage(parent *wire.MsgTx, commitment *ChannelCommitment) ([]*wire.MsgTx, error) {
	if parent == nil || commitment == nil || commitment.CommitTx == nil || parent.TxID() != commitment.CommitTx.TxID() || len(commitment.Others) == 0 {
		return nil, fmt.Errorf("missing complete closed withdrawal package")
	}
	txs := []*wire.MsgTx{parent}
	txs = append(txs, commitment.NextTxs...)
	for _, other := range commitment.Others {
		chain, err := stpOtherTransactions(other)
		if err != nil {
			return nil, err
		}
		txs = append(txs, chain...)
	}
	seen := map[string]string{}
	result := make([]*wire.MsgTx, 0, len(txs))
	for _, tx := range txs {
		if tx == nil {
			return nil, fmt.Errorf("nil closed withdrawal dependency")
		}
		raw, err := EncodeMsgTx(tx)
		if err != nil {
			return nil, err
		}
		if previous, ok := seen[tx.TxID()]; ok {
			if previous != raw {
				return nil, fmt.Errorf("conflicting closed withdrawal signed dependency")
			}
			continue
		}
		seen[tx.TxID()] = raw
		result = append(result, tx)
	}
	if len(result) < 2 {
		return nil, fmt.Errorf("closed withdrawal has no signed Other successor")
	}
	return result, nil
}

func closedWithdrawalChildID(owner *LocalActionPerformData) int64 {
	if owner == nil {
		return 0
	}
	owner.RLock()
	defer owner.RUnlock()
	for _, child := range owner.ActionResvs {
		if child != nil && child.ActionType == RESV_TYPE_CLOSE {
			return child.ResvId
		}
	}
	return 0
}

// Persist the decision, exact authorized dependency package and channel phase
// atomically before any broadcast. Only local short locks are held here.
func (p *Manager) persistClosedWithdrawal(owner *LocalActionPerformData, intent *closedChannelIntent, resv *ClosingReservation, expectedChannel []byte, complete bool) error {
	if owner == nil || intent == nil || resv == nil || resv.Id == 0 || resv.Channel == nil || intent.Choice != "withdraw" {
		return fmt.Errorf("invalid closed-channel withdrawal context")
	}
	current := p.GetChannel(intent.ChannelID)
	if current != nil {
		current.Mutex.Lock()
		defer current.Mutex.Unlock()
		if p.GetChannel(intent.ChannelID) != current {
			return fmt.Errorf("closed channel changed during withdrawal preparation")
		}
	}
	owner.Lock()
	snapshot, err := loadClosedChannelIntent(owner)
	if err != nil {
		owner.Unlock()
		return err
	}
	if complete && owner.Status == closedIntentWithdrawn && snapshot.ChannelID == intent.ChannelID && snapshot.OldParentID == intent.OldParentID && snapshot.Choice == "withdraw" {
		owner.Unlock()
		return nil
	}
	if snapshot.source != intent.source || snapshot.ChannelID != intent.ChannelID || snapshot.OldParentID != intent.OldParentID || snapshot.Choice != "withdraw" {
		owner.Unlock()
		return fmt.Errorf("closed recovery choice changed; retry its current state")
	}
	latest, err := p.db.Read([]byte(GetChannelKey(intent.ChannelID)))
	if err != nil {
		owner.Unlock()
		return err
	}
	if !bytes.Equal(latest, expectedChannel) {
		owner.Unlock()
		return fmt.Errorf("closed channel changed before withdrawal persistence")
	}
	nextOwner := *owner
	nextOwner.ActionResvs = append([]*SubActionInfo(nil), owner.ActionResvs...)
	linked := false
	for _, child := range nextOwner.ActionResvs {
		if child != nil && child.ActionType == RESV_TYPE_CLOSE && child.ResvId == resv.Id {
			linked = true
		}
	}
	if !linked {
		nextOwner.ActionResvs = append(nextOwner.ActionResvs, &SubActionInfo{ActionType: RESV_TYPE_CLOSE, ResvId: resv.Id})
	}
	nextOwner.Status = RS_PERFORM_ACTION_RUN_STARTED
	intent.Step = "withdraw_pending"
	nextChannel := resv.Channel.ChannelInDB
	nextChannel.Status, nextChannel.UpdateTime = CS_CLOSE_FORCELY_SWEEP_BROADCASTED, resv.Id
	nextChannel.ClosingTx = resv.CloseTx.Copy()
	nextClosing := resv.ClosingDataInDB
	nextClosing.Status = ResvStatus(CS_CLOSE_FORCELY_SWEEP_BROADCASTED)
	if complete {
		nextOwner.Status = closedIntentWithdrawn
		intent.Step = "withdrawn"
		nextChannel.Status = CS_CLOSED_FORCELY
		nextClosing.Status = RS_CLOSED
	}
	encodedIntent, err := json.Marshal(intent)
	if err != nil {
		owner.Unlock()
		return err
	}
	nextOwner.ActionParam = string(encodedIntent)
	if err := nextChannel.PrepareForSave(); err != nil {
		owner.Unlock()
		return err
	}
	ownerRaw, err := EncodeToBytes(&nextOwner)
	if err != nil {
		owner.Unlock()
		return err
	}
	closingRaw, err := EncodeToBytes(&nextClosing)
	if err != nil {
		owner.Unlock()
		return err
	}
	channelRaw, err := EncodeToBytes(&nextChannel)
	if err != nil {
		owner.Unlock()
		return err
	}
	batch, err := NewAtomicWriteBatch(p.db)
	if err != nil {
		owner.Unlock()
		return err
	}
	defer batch.Close()
	if err = batch.Put([]byte(GetResvKey(RESV_TYPE_LOCALACTION, owner.Id)), ownerRaw); err == nil {
		err = batch.Put([]byte(GetResvKey(RESV_TYPE_CLOSE, resv.Id)), closingRaw)
	}
	if err == nil {
		err = batch.Put([]byte(GetChannelKey(intent.ChannelID)), channelRaw)
	}
	if err == nil {
		err = batch.Flush()
	}
	if err != nil {
		owner.Unlock()
		return err
	}
	owner.Status, owner.ActionParam, owner.ActionResvs = nextOwner.Status, nextOwner.ActionParam, nextOwner.ActionResvs
	intent.source = string(encodedIntent)
	owner.Unlock()
	resv.ClosingDataInDB = nextClosing
	resv.Channel.ChannelInDB = nextChannel
	if current != nil {
		current.ChannelInDB = nextChannel
	}
	disableTarget := resv.Channel
	if current != nil {
		disableTarget = current
	}
	p.DisableChannel(disableTarget)
	if complete {
		p.DelResvWithId(resv.Id)
	} else {
		p.AddResv(resv)
	}
	return nil
}

// Retries reuse the exact durable bytes. Visibility is only a broadcast retry
// aid; completion requires every dependency to be confirmed on Bitcoin.
func (p *Manager) retryClosedChannelWithdrawal(owner *LocalActionPerformData) error {
	owner.RLock()
	intent, err := loadClosedChannelIntent(owner)
	status := owner.Status
	w := owner.LocalWallet()
	owner.RUnlock()
	if err != nil {
		return err
	}
	if status == closedIntentWithdrawn {
		return nil
	}
	if intent.Choice != "withdraw" || intent.Step != "withdraw_pending" || w == nil || !closedIntentWalletMatches(owner, w) {
		return fmt.Errorf("closed-channel withdrawal identity is unavailable")
	}
	id := closedWithdrawalChildID(owner)
	if id == 0 {
		return fmt.Errorf("closed-channel withdrawal has no durable closing child")
	}
	raw, err := p.db.Read([]byte(GetResvKey(RESV_TYPE_CLOSE, id)))
	if err != nil {
		return err
	}
	var stored ClosingDataInDB
	if err := DecodeFromBytes(raw, &stored); err != nil {
		return err
	}
	if stored.Id != id || stored.ChannelId != intent.ChannelID || stored.WalletId != w.GetWalletId() || stored.CloseTx == nil || stored.CloseTx.TxID() != intent.OldParentID || len(stored.PreTxs) == 0 {
		return fmt.Errorf("closed withdrawal child changed identity")
	}
	channelRaw, err := p.db.Read([]byte(GetChannelKey(intent.ChannelID)))
	if err != nil {
		return err
	}
	var channel ChannelInDB
	if err := DecodeFromBytes(channelRaw, &channel); err != nil {
		return err
	}
	if channel.UpdateTime != id || channel.LocalWalletId != w.GetId() || channel.LocalChanCfg.WalletId != w.GetSubAccount() {
		return fmt.Errorf("closed withdrawal no longer owns this channel")
	}
	if p.GetIndexerRPCClient() == nil || !p.GetIndexerRPCClient().IsTxConfirmed(intent.OldParentID) {
		return fmt.Errorf("withdrawal server parent is awaiting confirmation")
	}
	canonicalRaw, err := p.GetIndexerRPCClient().GetRawTx(intent.OldParentID)
	if err != nil {
		return err
	}
	canonical, err := DecodeMsgTx(canonicalRaw)
	if err != nil || canonical.TxID() != intent.OldParentID {
		return fmt.Errorf("closed withdrawal canonical server parent is unavailable")
	}
	if channel.RemoteCommitment == nil || channel.RemoteCommitment.CommitTx == nil || channel.RemoteCommitment.CommitTx.TxID() != intent.OldParentID {
		return fmt.Errorf("closed withdrawal stored parent changed")
	}
	resv := &ClosingReservation{ClosingDataInDB: stored, Channel: &Channel{ChannelInDB: channel, manager: p, localWallet: w.Clone()}}
	if intent.InputFacts != nil {
		approved, err := closedWithdrawalPackage(canonical, channel.RemoteCommitment)
		if err != nil || !equalSTPBitcoinChain(approved, stored.PreTxs) || !equalSTPBitcoinTx(canonical, stored.CloseTx) || len(stored.AssetProofs) != 0 {
			return fmt.Errorf("indexed withdrawal no longer matches its owner's exact approved package: %v", err)
		}
	}
	expected, err := p.prepareClosedWithdrawal(resv.Channel, canonical, intent)
	if err != nil {
		return err
	}
	if !equalSTPBitcoinChain(expected.PreTxs, stored.PreTxs) || !equalSTPBitcoinTx(expected.CloseTx, stored.CloseTx) || !equalSTPAssetProofs(expected.AssetProofs, stored.AssetProofs) {
		return fmt.Errorf("closed withdrawal durable bytes differ from the stored authorized package")
	}
	if expected.DeAnchorTx == nil || stored.DeAnchorTx == nil {
		if expected.DeAnchorTx != nil || stored.DeAnchorTx != nil {
			return fmt.Errorf("closed withdrawal durable paired burn changed")
		}
	} else {
		actualL2, encodeErr := EncodeMsgTx_SatsNet(expected.DeAnchorTx)
		durableL2, durableErr := EncodeMsgTx_SatsNet(stored.DeAnchorTx)
		if encodeErr != nil || durableErr != nil || actualL2 != durableL2 {
			return fmt.Errorf("closed withdrawal durable paired burn changed")
		}
	}
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	AddTxOutputsToPrevFetcher(fetcher, canonical)
	if intent.InputFacts != nil {
		for _, fact := range intent.InputFacts.Inputs {
			output := closedWithdrawalFactOutput(fact)
			fetcher.AddPrevOut(*output.OutPoint(), output.TxOut())
		}
	}
	for _, tx := range stored.PreTxs[1:] {
		for _, input := range tx.TxIn {
			if input == nil || fetcher.FetchPrevOutput(input.PreviousOutPoint) == nil {
				return fmt.Errorf("closed withdrawal durable package is missing an input dependency")
			}
		}
		if err := VerifySignedTx(tx, fetcher); err != nil {
			return fmt.Errorf("closed withdrawal durable signature: %w", err)
		}
		AddTxOutputsToPrevFetcher(fetcher, tx)
	}
	if _, err := p.broadcastPunishPackage(stored.PreTxs, "client-selected closed-channel Other recovery"); err != nil {
		return err
	}
	for _, tx := range stored.PreTxs {
		if tx == nil || !p.GetIndexerRPCClient().IsTxConfirmed(tx.TxID()) {
			return nil
		}
	}
	if err := p.completeClosedWithdrawal(resv, intent); err != nil {
		return err
	}
	return p.persistClosedWithdrawal(owner, intent, resv, channelRaw, true)
}

func (p *Manager) retryClosedWithdrawalChild(resv *ClosingReservation) (bool, error) {
	if resv == nil {
		return false, nil
	}
	for _, reservation := range p.GetLocalActionReservations() {
		owner, ok := reservation.(*LocalActionPerformData)
		if !ok || owner.Action != localActionClosedChannelRecovery {
			continue
		}
		if closedWithdrawalChildID(owner) != resv.Id {
			continue
		}
		owner.RLock()
		intent, err := loadClosedChannelIntent(owner)
		owner.RUnlock()
		if err != nil {
			return true, err
		}
		if intent.Choice != "withdraw" || intent.ChannelID != resv.ChannelId || owner.WalletId != resv.WalletId {
			return true, fmt.Errorf("closed withdrawal child does not match its owning intent")
		}
		return true, p.retryClosedChannelWithdrawal(owner)
	}
	return false, nil
}

// Protocol-specific material never owns the withdrawal lifecycle. The caller
// alone chooses, persists, broadcasts, retries and completes the common C2.
func (p *Manager) prepareClosedWithdrawal(channel *Channel, parent *wire.MsgTx, contexts ...*closedChannelIntent) (*ClosingDataInDB, error) {
	if len(contexts) > 1 {
		return nil, fmt.Errorf("invalid closed withdrawal material context")
	}
	context := &closedChannelIntent{}
	if len(contexts) == 1 && contexts[0] != nil {
		context = contexts[0]
	}
	if channel == nil || parent == nil {
		return nil, fmt.Errorf("missing closed withdrawal context")
	}
	if closedWithdrawalHasRGB(&channel.ChannelInDB) {
		txs, deanchor, _, err := p.prepareRGBClosedWithdrawal(channel, parent)
		if err != nil {
			return nil, err
		}
		return &ClosingDataInDB{CloseTx: parent.Copy(), PreTxs: txs,
			AssetProofs: cloneSTPAssetProofs(channel.RemoteCommitment.RGBClientRecovery.AssetProofs), DeAnchorTx: deanchor}, nil
	}
	if closedWithdrawalUsesInputFacts(&channel.ChannelInDB) || context.InputFacts != nil {
		return p.prepareIndexedClosedWithdrawal(channel, parent, context, false)
	}
	// A native adapter needs its actual validation/completion path. An empty
	// sidecar or a fully signed Bitcoin transaction cannot bypass this guard.
	if nativeForceClose(&ClosingReservation{Channel: channel}) {
		return nil, fmt.Errorf("native Other recovery requires its existing native receipt completion path")
	}
	txs, err := closedWithdrawalPackage(parent, channel.RemoteCommitment)
	if err != nil {
		return nil, err
	}
	return &ClosingDataInDB{CloseTx: parent.Copy(), PreTxs: txs}, nil
}

// All dependencies are already confirmed before this hook. Legacy C2 has no
// additional import; protocols with native receipts must finish those first.
func (p *Manager) completeClosedWithdrawal(resv *ClosingReservation, contexts ...*closedChannelIntent) error {
	if len(contexts) > 1 {
		return fmt.Errorf("invalid closed withdrawal completion context")
	}
	var context *closedChannelIntent
	if len(contexts) == 1 {
		context = contexts[0]
	}
	if resv == nil || resv.Channel == nil {
		return fmt.Errorf("missing closed withdrawal completion context")
	}
	if closedWithdrawalHasRGB(&resv.Channel.ChannelInDB) {
		return p.completeRGBClosedWithdrawal(resv)
	}
	if closedWithdrawalUsesInputFacts(&resv.Channel.ChannelInDB) || context != nil && context.InputFacts != nil {
		if context == nil || context.InputFacts == nil {
			return fmt.Errorf("closed withdrawal has no validated input facts")
		}
		_, err := p.prepareIndexedClosedWithdrawal(resv.Channel, resv.CloseTx, context, true)
		return err
	}
	if len(resv.AssetProofs) != 0 || resv.DeAnchorTx != nil {
		return fmt.Errorf("closed withdrawal has unsupported native completion material")
	}
	return nil
}
