package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

// One ordinary asset leaf of an already-reopened channel, not a job queue.
// This is JSON inside the existing recovery owner's string ActionParam.
type closedChannelAssetRestore struct {
	AssetName, ExactUTXO, Step string
	ChildID                    int64
	Request                    *wwire.SplicingInRequest
	RequestSignature           []byte
	AcceptedJSON               string
}

type closedChannelAssetAccept struct {
	NewCapacity, NewLocalPlainBalance, NewRemotePlainBalance          int64
	NewLocalBalance, NewRemoteBalance                                 string
	RemoteRevKey, RemoteNextRevKey, InvoiceSig                        []byte
	ChannelSnapshot, OldChannelSnapshot, InputsSnapshot, FeesSnapshot []byte
}

func (p *Manager) closedAssetReadyOwner(w common.Wallet, channelID string) (*LocalActionPerformData, *closedChannelIntent, *ChannelInDB, error) {
	if w == nil || w.GetPaymentPubKey() == nil {
		return nil, nil, nil, fmt.Errorf("asset restoration requires its captured wallet")
	}
	stored, err := p.LoadChannelInDB(channelID)
	if err != nil {
		return nil, nil, nil, err
	}
	if stored.Status != CS_READY || !stored.IsInitiator || stored.FundingTime <= 0 || stored.LocalWalletId != w.GetId() || stored.LocalChanCfg.WalletId != w.GetSubAccount() || stored.LocalChanCfg.PaymentKey == nil || !stored.LocalChanCfg.PaymentKey.IsEqual(w.GetPaymentPubKey()) {
		return nil, nil, nil, fmt.Errorf("asset restoration has no exact captured durable READY channel")
	}
	for _, item := range p.GetLocalActionReservations() {
		owner, ok := item.(*LocalActionPerformData)
		if !ok || !closedIntentWalletMatches(owner, w) || owner.Action != localActionClosedChannelRecovery {
			continue
		}
		owner.RLock()
		intent, loadErr := loadClosedChannelIntent(owner)
		matched := loadErr == nil && owner.Status == closedIntentReopened && intent.Choice == "reopen" && intent.ChannelID == channelID
		linked := false
		for _, child := range owner.ActionResvs {
			linked = linked || child != nil && child.ActionType == RESV_TYPE_OPEN && child.ResvId == stored.FundingTime
		}
		owner.RUnlock()
		if loadErr != nil {
			return nil, nil, nil, loadErr
		}
		if matched && linked {
			return owner, intent, stored, nil
		}
	}
	return nil, nil, nil, fmt.Errorf("asset restoration has no completed owner for this funding generation")
}

func (p *Manager) prepareClosedChannelAssetRestore(w common.Wallet, channelID, assetName, exactUTXO string) (*LocalActionPerformData, int64, error) {
	if assetName == "" || exactUTXO == "" {
		return nil, 0, fmt.Errorf("asset restoration requires one exact asset output")
	}
	owner, intent, _, err := p.closedAssetReadyOwner(w, channelID)
	if err != nil {
		return nil, 0, err
	}
	if intent.AssetsComplete {
		return owner, 0, fmt.Errorf("this closed-channel asset restoration is already complete")
	}
	if leaf := intent.AssetRestore; leaf != nil && leaf.Step != "completed" {
		if leaf.AssetName != assetName || leaf.ExactUTXO != exactUTXO {
			return owner, leaf.ChildID, closedChannelRecoveryPending(owner, "asset_"+leaf.Step, nil)
		}
		if leaf.ChildID > 0 {
			return owner, leaf.ChildID, nil
		}
		if leaf.Step != "prepared" {
			return owner, 0, closedChannelRecoveryPending(owner, "asset_"+leaf.Step, nil)
		}
		return owner, 0, nil
	}
	intent.AssetRestore = &closedChannelAssetRestore{AssetName: assetName, ExactUTXO: exactUTXO, Step: "prepared"}
	if err := p.persistClosedChannelIntent(owner, intent, closedIntentReopened); err != nil {
		return owner, 0, err
	}
	return owner, 0, nil
}

func closedAssetAcceptedJSON(resv *SplicingReservation) (string, error) {
	// Existing Decimal semantics use nil for zero (including the ordinary peer).
	// Snapshot that zero before signing; the original Funder still checks both
	// returned balances against its independently computed expected quantities.
	if resv.Channel == nil || resv.OldChannel == nil {
		return "", fmt.Errorf("accepted asset runtime material is incomplete")
	}
	snapshot := closedChannelAssetAccept{NewCapacity: resv.NewCapacity, NewLocalBalance: resv.NewLocalBalance.String(), NewRemoteBalance: resv.NewRemoteBalance.String(), NewLocalPlainBalance: resv.NewLocalPlainBalance, NewRemotePlainBalance: resv.NewRemotePlainBalance, RemoteRevKey: resv.RemoteRevKey, RemoteNextRevKey: resv.RemoteNextRevKey, InvoiceSig: resv.InvoiceSig}
	var a, b, c, d error
	snapshot.ChannelSnapshot, a = EncodeToBytes(&resv.Channel.ChannelInDB)
	snapshot.OldChannelSnapshot, b = EncodeToBytes(&resv.OldChannel.ChannelInDB)
	snapshot.InputsSnapshot, c = EncodeToBytes(resv.SplicingInputs)
	snapshot.FeesSnapshot, d = EncodeToBytes(resv.Fees)
	if err := errors.Join(a, b, c, d); err != nil {
		return "", err
	}
	raw, err := json.Marshal(snapshot)
	return string(raw), err
}

func (p *Manager) persistClosedChannelAssetRestore(owner *LocalActionPerformData, resv *SplicingReservation, step string) error {
	owner.RLock()
	intent, err := loadClosedChannelIntent(owner)
	done := owner.Status == closedIntentReopened
	owner.RUnlock()
	if err != nil {
		return err
	}
	if !done || intent.AssetsComplete || intent.Choice != "reopen" || intent.AssetRestore == nil || resv == nil || resv.InReq == nil || resv.AssetName == nil || resv.NeedSendSplicingTx || resv.InReq.NeedSendSplicingTx || !closedIntentWalletMatches(owner, resv.LocalWallet()) || resv.ChannelId != intent.ChannelID || resv.InReq.ChannelId != intent.ChannelID {
		return fmt.Errorf("asset request does not belong to the exact reopened owner")
	}
	leaf := intent.AssetRestore
	if resv.AssetName.String() != leaf.AssetName || resv.InReq.AssetName != leaf.AssetName || len(resv.InReq.Utxos) != 1 || resv.InReq.Utxos[0] != leaf.ExactUTXO || len(resv.ReqSig) == 0 {
		return fmt.Errorf("asset request changed its prepared exact input")
	}
	raw, err := json.Marshal(resv.InReq)
	if err != nil {
		return err
	}
	if err := VerifySignOfMessage(raw, resv.ReqSig, owner.ReqPubKey); err != nil {
		return err
	}
	if step == "request_prepared" {
		if leaf.Step != "prepared" || leaf.Request != nil {
			return closedChannelRecoveryPending(owner, "asset_"+leaf.Step, nil)
		}
		leaf.Step, leaf.Request, leaf.RequestSignature = step, resv.InReq, append([]byte(nil), resv.ReqSig...)
		return p.persistClosedChannelIntent(owner, intent, closedIntentReopened)
	}
	original, err := json.Marshal(leaf.Request)
	if err != nil {
		return err
	}
	if step != "child_accepted" || leaf.Step != "request_prepared" || resv.Id <= 0 || !bytes.Equal(original, raw) || !bytes.Equal(leaf.RequestSignature, resv.ReqSig) {
		return fmt.Errorf("asset acceptance cannot replace its signed request or pending stage")
	}
	leaf.AcceptedJSON, err = closedAssetAcceptedJSON(resv)
	if err != nil {
		return err
	}
	leaf.Step, leaf.ChildID = step, resv.Id
	return p.persistClosedAssetChild(owner, intent, resv)
}

func (p *Manager) persistClosedAssetChild(owner *LocalActionPerformData, intent *closedChannelIntent, resv *SplicingReservation) error {
	owner.Lock()
	defer owner.Unlock()
	current, err := loadClosedChannelIntent(owner)
	if err != nil {
		return err
	}
	if owner.Status != closedIntentReopened || current.source != intent.source || current.Choice != "reopen" || current.ChannelID != intent.ChannelID || current.OldParentID != intent.OldParentID {
		return fmt.Errorf("asset recovery owner changed before acceptance persistence")
	}
	next := *owner
	next.ActionResvs = append([]*SubActionInfo(nil), owner.ActionResvs...)
	linked := false
	for _, child := range next.ActionResvs {
		linked = linked || child != nil && child.ActionType == RESV_TYPE_SPLICING && child.ResvId == resv.Id
	}
	if !linked {
		next.ActionResvs = append(next.ActionResvs, &SubActionInfo{ActionType: RESV_TYPE_SPLICING, ResvId: resv.Id})
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	next.ActionParam = string(raw)
	ownerRaw, err := EncodeToBytes(&next)
	if err != nil {
		return err
	}
	childRaw, err := EncodeToBytes(&resv.SplicingDataInDB)
	if err != nil {
		return err
	}
	batch, err := NewAtomicWriteBatch(p.db)
	if err != nil {
		return err
	}
	defer batch.Close()
	if err = batch.Put([]byte(GetResvKey(RESV_TYPE_LOCALACTION, owner.Id)), ownerRaw); err == nil {
		err = batch.Put([]byte(GetResvKey(RESV_TYPE_SPLICING, resv.Id)), childRaw)
	}
	if err == nil {
		err = batch.Flush()
	}
	if err != nil {
		return err
	}
	owner.ActionParam, owner.ActionResvs = next.ActionParam, next.ActionResvs
	// Accepted is not monitor-ready. Only the original signed pipeline adds
	// this child to the normal monitor after it reaches STARTED.
	return nil
}

func (p *Manager) completeClosedChannelAssetRestore(owner *LocalActionPerformData, childID int64) error {
	owner.RLock()
	intent, err := loadClosedChannelIntent(owner)
	owner.RUnlock()
	if err != nil {
		return err
	}
	leaf := intent.AssetRestore
	if leaf == nil || leaf.ChildID != childID || childID <= 0 || (leaf.Step != "child_accepted" && leaf.Step != "completed") {
		return fmt.Errorf("asset completion does not match its accepted child")
	}
	w := owner.LocalWallet()
	current, _, channel, err := p.closedAssetReadyOwner(w, intent.ChannelID)
	if err != nil {
		return err
	}
	if current != owner {
		return fmt.Errorf("asset completion lost its exact READY owner")
	}
	if leaf.Step == "completed" {
		return nil
	}
	record, err := LoadReservation(p.db, nil, RESV_TYPE_SPLICING, childID)
	if err != nil {
		return err
	}
	child, ok := record.(*SplicingReservation)
	if !ok || child.Status != RS_CLOSED || child.WalletId != owner.WalletId || child.ChannelId != intent.ChannelID || child.AssetName == nil || child.AssetName.String() != leaf.AssetName || child.NeedSendSplicingTx || channel.UpdateTime != childID || child.SplicingAmt == nil {
		return fmt.Errorf("asset child is not durably completed in its exact channel")
	}
	anchorID := child.AnchorTxId()
	if anchorID == "" || p.l2IndexerClient == nil || !p.l2IndexerClient.IsTxConfirmed(anchorID) {
		return closedChannelRecoveryPending(owner, "asset_anchor_confirmation", nil)
	}
	matched := false
	script := channel.GetChannelPkScript()
	for _, output := range channel.FundingUtxos[*child.AssetName] {
		if output == nil || output.OutPointStr != leaf.ExactUTXO {
			continue
		}
		amount, _ := output.GetAssetV2(&child.AssetName.AssetName)
		matched = output.Value() == child.SplicingValue && bytes.Equal(output.OutValue.PkScript, script) && amount.Cmp(child.SplicingAmt) == 0
	}
	if !matched {
		return fmt.Errorf("completed asset is absent from its exact durable asset funding bucket")
	}
	leaf.Step = "completed"
	return p.persistClosedChannelIntent(owner, intent, closedIntentReopened)
}

// The captured driver must first make a fresh inventory observation containing
// no unmanaged assets, including unsupported/native leftovers. This only marks
// the one restoration terminal; it never authorizes later deposits to expand.
func (p *Manager) completeClosedChannelAssets(owner *LocalActionPerformData) error {
	owner.RLock()
	intent, err := loadClosedChannelIntent(owner)
	owner.RUnlock()
	if err != nil {
		return err
	}
	if intent.AssetExpectations == nil {
		return closedChannelRecoveryPending(owner, "asset_expectations_unknown", nil)
	}
	if intent.AssetRestore != nil && intent.AssetRestore.Step != "completed" {
		return closedChannelRecoveryPending(owner, "asset_"+intent.AssetRestore.Step, nil)
	}
	current, _, _, err := p.closedAssetReadyOwner(owner.LocalWallet(), intent.ChannelID)
	if err != nil {
		return err
	}
	if current != owner {
		return fmt.Errorf("asset terminal lost its exact READY owner")
	}
	if intent.AssetsComplete {
		return nil
	}
	intent.AssetsComplete = true
	return p.persistClosedChannelIntent(owner, intent, closedIntentReopened)
}
