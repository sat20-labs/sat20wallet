package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

const localActionClosedChannelRecovery = "closedchannelrecovery"

// Terminal intent records remain positive and are never generic local-action
// completions. A later close has a different exact old parent and a new intent.
const (
	closedIntentReopened  ResvStatus = 0x2410
	closedIntentWithdrawn ResvStatus = 0x2411
)

// This is a JSON envelope inside the existing string ActionParam, not a gob
// type or a new database schema. Opaque snapshots use already-supported gob
// types; no runtime signer, mutex or secret is persisted here.
type closedChannelIntent struct {
	Version                                                 int
	TopupJSON                                               string
	ChannelID, OldParentID, Choice, Step                    string
	Request                                                 *wwire.OpenChannelRequest
	RequestSignature                                        []byte
	Accept                                                  *wwire.AcceptChannel
	FundingCreated                                          *wwire.FundingCreated
	FundingSigned                                           *wwire.FundingSigned
	FundingBroadcasted                                      *wwire.FundingBroadcasted
	FundingSnapshot, FundingInputsSnapshot, ChannelSnapshot []byte
	L2DrainTxID                                             string
	DrainRequest                                            *wwire.SignRequest
	DrainRequestSignature, DrainSnapshot                    []byte
	AssetRestore                                            *closedChannelAssetRestore
	AssetExpectations                                       []closedChannelAssetExpectation
	InputFacts                                              *closedWithdrawalInputFacts `json:",omitempty"`
	AssetsComplete                                          bool
	source                                                  string
}

type closedChannelAssetExpectation struct {
	Asset  AssetName
	Amount *Decimal
}

// Retain identities and client quantities, never old funding roots as inputs.
// nil is unknown; a nonnil empty list means the old balance was checked.
func closedChannelAssetExpectations(stored *ChannelInDB) []closedChannelAssetExpectation {
	if stored == nil || stored.LocalCommitment == nil || stored.LocalCommitment.LocalBalance == nil {
		return nil
	}
	expected := make([]closedChannelAssetExpectation, 0)
	for asset, amount := range stored.LocalCommitment.LocalBalance {
		if asset.AssetName != ASSET_PLAIN_SAT && amount != nil && amount.Sign() > 0 {
			expected = append(expected, closedChannelAssetExpectation{Asset: asset, Amount: amount.Clone()})
		}
	}
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].Asset.String() != expected[j].Asset.String() {
			return expected[i].Asset.String() < expected[j].Asset.String()
		}
		return expected[i].Asset.N < expected[j].Asset.N
	})
	return expected
}

func loadClosedChannelIntent(owner *LocalActionPerformData) (*closedChannelIntent, error) {
	if owner == nil || owner.Action != localActionClosedChannelRecovery {
		return nil, fmt.Errorf("not a closed-channel recovery intent")
	}
	var raw string
	switch value := owner.ActionParam.(type) {
	case string:
		raw = value
	case []byte:
		raw = string(value)
	default:
		return nil, fmt.Errorf("invalid closed-channel intent payload")
	}
	var intent closedChannelIntent
	if err := json.Unmarshal([]byte(raw), &intent); err != nil {
		return nil, err
	}
	if intent.Version != 1 || intent.ChannelID == "" || intent.OldParentID == "" ||
		(intent.Choice != "reopen" && intent.Choice != "withdraw") {
		return nil, fmt.Errorf("invalid closed-channel intent identity")
	}
	intent.source = raw
	return &intent, nil
}

func closedIntentWalletMatches(owner *LocalActionPerformData, w common.Wallet) bool {
	return owner != nil && w != nil && w.GetPaymentPubKey() != nil &&
		owner.WalletId == w.GetWalletId() && bytes.Equal(owner.ReqPubKey, w.GetPaymentPubKey().SerializeCompressed())
}

// Initial metadata is copied into the existing ActionParam JSON in the first
// durable owner save. A retry never replaces an existing asset expectation.
type closedChannelIntentInitial struct {
	TopupJSON         string
	AssetExpectations []closedChannelAssetExpectation
}

// Only the existing single Manager is serialized here. This is not a cross-tab
// database lease. No manager/intent lock is retained across an RPC.
func (p *Manager) ensureClosedChannelIntent(w common.Wallet, channelID, oldParentID, choice string, initialMetadata ...closedChannelIntentInitial) (*LocalActionPerformData, *closedChannelIntent, error) {
	if len(initialMetadata) > 1 || len(initialMetadata) != 0 && choice != "reopen" {
		return nil, nil, fmt.Errorf("invalid initial closed-channel recovery metadata")
	}
	var initial closedChannelIntentInitial
	if len(initialMetadata) == 1 {
		initial = initialMetadata[0]
	}
	if w == nil || w.GetPaymentPubKey() == nil || channelID == "" || oldParentID == "" || (choice != "reopen" && choice != "withdraw") {
		return nil, nil, fmt.Errorf("invalid closed-channel recovery request")
	}
	id := p.GenerateNewResvId()
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, reservation := range p.localActionPerformMap {
		owner, ok := reservation.(*LocalActionPerformData)
		if !ok || owner.Action != localActionClosedChannelRecovery || !closedIntentWalletMatches(owner, w) {
			continue
		}
		owner.RLock()
		intent, err := loadClosedChannelIntent(owner)
		owner.RUnlock()
		if err != nil {
			return nil, nil, err
		}
		if intent.ChannelID != channelID || intent.OldParentID != oldParentID {
			continue
		}
		if intent.Choice != choice {
			return nil, nil, fmt.Errorf("closed-channel recovery already chose %s", intent.Choice)
		}
		if initial.TopupJSON != "" && intent.TopupJSON == "" {
			if !closedSingleUTXOPreparedOwnerMayAdopt(owner, intent) {
				return nil, nil, closedChannelRecoveryPending(owner, intent.Step, nil)
			}
			intent.TopupJSON = initial.TopupJSON
			if err := p.persistClosedChannelIntent(owner, intent, owner.Status); err != nil {
				return nil, nil, err
			}
		}
		return owner, intent, nil
	}
	if err := p.rejectUnfinishedChannelLifecycleLocked(channelID, w.GetWalletId(), w.GetPaymentPubKey().SerializeCompressed()); err != nil {
		return nil, nil, err
	}
	for p.resvMap[id] != nil {
		id++
	}
	intent := &closedChannelIntent{Version: 1, ChannelID: channelID, OldParentID: oldParentID, Choice: choice, Step: "prepared", TopupJSON: initial.TopupJSON, AssetExpectations: initial.AssetExpectations}
	raw, err := json.Marshal(intent)
	if err != nil {
		return nil, nil, err
	}
	owner := &LocalActionPerformData{ReservationBase: NewReservationBase(id, true, RS_PERFORM_ACTION_STARTED, w),
		Action: localActionClosedChannelRecovery, ActionParam: string(raw), ReqPubKey: append([]byte(nil), w.GetPaymentPubKey().SerializeCompressed()...)}
	if err := p.SaveWalletReservation(owner); err != nil {
		return nil, nil, err
	}
	p.addResvLocked(owner)
	intent.source = string(raw)
	return owner, intent, nil
}

func (p *Manager) persistClosedChannelIntent(owner *LocalActionPerformData, intent *closedChannelIntent, status ResvStatus) error {
	return p.persistClosedChannelIntentFunding(owner, intent, status, nil, false)
}

// Caller-owned intent.source is an exact local snapshot comparison, not a new
// persisted generation. Publish in-memory state only after the durable write.
func (p *Manager) persistClosedChannelIntentFunding(owner *LocalActionPerformData, intent *closedChannelIntent, status ResvStatus, funding *FundingReservation, saveChannel bool) error {
	if owner == nil || intent == nil {
		return fmt.Errorf("missing closed-channel intent")
	}
	owner.Lock()
	current, err := loadClosedChannelIntent(owner)
	if err != nil {
		owner.Unlock()
		return err
	}
	if current.source != intent.source || current.ChannelID != intent.ChannelID || current.OldParentID != intent.OldParentID ||
		current.Choice != intent.Choice {
		owner.Unlock()
		return fmt.Errorf("closed-channel recovery changed; retry its current state")
	}
	next := *owner
	next.Status = status
	next.ActionResvs = append([]*SubActionInfo(nil), owner.ActionResvs...)
	if funding != nil {
		if funding.Id == 0 || funding.ChannelId != intent.ChannelID || funding.WalletId != owner.WalletId {
			owner.Unlock()
			return fmt.Errorf("invalid closed-channel funding child")
		}
		found := false
		for _, child := range next.ActionResvs {
			if child.ActionType == RESV_TYPE_OPEN && child.ResvId == funding.Id {
				found = true
			}
		}
		if !found {
			next.ActionResvs = append(next.ActionResvs, &SubActionInfo{ActionType: RESV_TYPE_OPEN, ResvId: funding.Id})
		}
		if saveChannel {
			if err = funding.Channel.ChannelInDB.PrepareForSave(); err != nil {
				owner.Unlock()
				return err
			}
		}
		if err = snapshotClosedChannelFunding(intent, funding); err != nil {
			owner.Unlock()
			return err
		}
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		owner.Unlock()
		return err
	}
	next.ActionParam = string(raw)
	if funding == nil {
		err = p.SaveWalletReservation(&next)
	} else {
		var ownerRaw, fundingRaw, channelRaw []byte
		ownerRaw, err = EncodeToBytes(&next)
		if err == nil {
			fundingRaw, err = EncodeToBytes(&funding.FundingDataInDB)
		}
		if err == nil && saveChannel {
			channelRaw, err = EncodeToBytes(&funding.Channel.ChannelInDB)
		}
		if err == nil {
			batch, batchErr := NewAtomicWriteBatch(p.db)
			if batchErr != nil {
				err = batchErr
			} else {
				if err = batch.Put([]byte(GetResvKey(RESV_TYPE_LOCALACTION, owner.Id)), ownerRaw); err == nil {
					err = batch.Put([]byte(GetResvKey(RESV_TYPE_OPEN, funding.Id)), fundingRaw)
				}
				if err == nil && saveChannel {
					err = batch.Put([]byte(GetChannelKey(intent.ChannelID)), channelRaw)
				}
				if err == nil {
					err = batch.Flush()
				}
				batch.Close()
			}
		}
	}
	if err != nil {
		owner.Unlock()
		return err
	}
	owner.ActionParam, owner.Status, owner.ActionResvs = next.ActionParam, next.Status, next.ActionResvs
	intent.source = string(raw)
	owner.Unlock()
	if funding != nil {
		p.AddResv(funding)
	}
	return nil
}

func snapshotClosedChannelFunding(intent *closedChannelIntent, funding *FundingReservation) error {
	var err error
	intent.Request, intent.RequestSignature, intent.Accept = funding.Req, append([]byte(nil), funding.ReqSig...), funding.Accept
	intent.FundingCreated, intent.FundingSigned, intent.FundingBroadcasted = funding.FundingCreated, funding.FundingSigned, funding.FundingBroadcasted
	intent.FundingSnapshot, err = EncodeToBytes(&funding.FundingDataInDB)
	if err != nil {
		return err
	}
	intent.FundingInputsSnapshot, err = EncodeToBytes(funding.FundingUtxos)
	if err != nil {
		return err
	}
	intent.ChannelSnapshot, err = EncodeToBytes(&funding.Channel.ChannelInDB)
	return err
}

func restoreClosedChannelFunding(w common.Wallet, intent *closedChannelIntent, p *Manager) (*FundingReservation, error) {
	if intent.Request == nil || len(intent.RequestSignature) == 0 || len(intent.FundingSnapshot) == 0 || len(intent.ChannelSnapshot) == 0 {
		return nil, fmt.Errorf("closed-channel request snapshot is incomplete")
	}
	funding := &FundingReservation{}
	if err := DecodeFromBytes(intent.FundingSnapshot, &funding.FundingDataInDB); err != nil {
		return nil, err
	}
	var stored ChannelInDB
	if err := DecodeFromBytes(intent.ChannelSnapshot, &stored); err != nil {
		return nil, err
	}
	if err := DecodeFromBytes(intent.FundingInputsSnapshot, &funding.FundingUtxos); err != nil {
		return nil, err
	}
	if funding.WalletId != w.GetWalletId() || !bytes.Equal(intent.Request.FundingKey, w.GetPaymentPubKey().SerializeCompressed()) || funding.ChannelId != intent.ChannelID {
		return nil, fmt.Errorf("closed-channel funding snapshot account mismatch")
	}
	funding.InitRuntime()
	funding.SetLocalWallet(w.Clone())
	funding.Channel = &Channel{ChannelInDB: stored, manager: p, localWallet: w.Clone(), PeerRPC: p.GetPeerNodeClient(&stored)}
	funding.Req, funding.ReqSig, funding.Accept = intent.Request, append([]byte(nil), intent.RequestSignature...), intent.Accept
	funding.FundingCreated, funding.FundingSigned, funding.FundingBroadcasted = intent.FundingCreated, intent.FundingSigned, intent.FundingBroadcasted
	return funding, nil
}

func (p *Manager) closedRecoveryWalletSnapshot() (common.Wallet, NodeRPCClient, error) {
	p.channelIdentityMu.RLock()
	defer p.channelIdentityMu.RUnlock()
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.wallet == nil || p.serverNode == nil || p.serverNode.client == nil {
		return nil, nil, fmt.Errorf("wallet or server is unavailable")
	}
	return p.wallet.Clone(), p.serverNode.client, nil
}

func (p *Manager) closedReopenIntentForWallet(w common.Wallet, channelID string) (*LocalActionPerformData, *closedChannelIntent, error) {
	return p.closedReopenIntentForWalletWithInitialTopup(w, channelID, "", "")
}

func (p *Manager) closedReopenIntentForWalletWithInitialTopup(w common.Wallet, channelID, expectedParent, initialTopupJSON string) (*LocalActionPerformData, *closedChannelIntent, error) {
	stored, err := p.LoadChannelInDB(channelID)
	if err != nil {
		return nil, nil, err
	}
	if stored.Status > CS_CLOSED {
		for _, item := range p.GetLocalActionReservations() {
			owner, ok := item.(*LocalActionPerformData)
			if !ok || !closedIntentWalletMatches(owner, w) || owner.Action != localActionClosedChannelRecovery {
				continue
			}
			owner.RLock()
			intent, parseErr := loadClosedChannelIntent(owner)
			owner.RUnlock()
			if parseErr != nil {
				return nil, nil, parseErr
			}
			if intent.ChannelID != channelID || intent.Choice != "reopen" {
				continue
			}
			for _, child := range owner.ActionResvs {
				if child.ActionType == RESV_TYPE_OPEN && child.ResvId == stored.FundingTime {
					return owner, intent, nil
				}
			}
		}
		return nil, nil, fmt.Errorf("channel %s is not closed; resume its existing lifecycle", channelID)
	}
	if stored.LocalChanCfg.PaymentKey == nil || !stored.LocalChanCfg.PaymentKey.IsEqual(w.GetPaymentPubKey()) ||
		stored.RemoteCommitment == nil || stored.RemoteCommitment.CommitTx == nil {
		return nil, nil, fmt.Errorf("closed channel signer or exact old parent is unavailable")
	}
	if expectedParent != "" && stored.RemoteCommitment.CommitTx.TxID() != expectedParent {
		return nil, nil, fmt.Errorf("reviewed closing parent changed before top-up approval")
	}
	// Do not replace a prepared or revoked native obligation with a reopen.
	if recovered, err := p.loadSTPNativeRevocationRecovery(stored); err != nil {
		return nil, nil, err
	} else if recovered != nil {
		return nil, nil, fmt.Errorf("closed channel has an unresolved revoked commitment")
	}
	if prepared, err := p.readSTPNativePreparedRecovery(&Channel{ChannelInDB: *stored}); err != nil {
		return nil, nil, err
	} else if prepared != nil {
		return nil, nil, fmt.Errorf("closed channel has an unresolved prepared commitment")
	}
	return p.ensureClosedChannelIntent(w, channelID, stored.RemoteCommitment.CommitTx.TxID(), "reopen", closedChannelIntentInitial{
		TopupJSON: initialTopupJSON, AssetExpectations: closedChannelAssetExpectations(stored),
	})
}

func closedChannelRecoveryPending(owner *LocalActionPerformData, step string, cause error) error {
	if cause != nil {
		return fmt.Errorf("closed-channel recovery %d pending at %s: %w", owner.Id, step, cause)
	}
	return fmt.Errorf("closed-channel recovery %d pending at %s", owner.Id, step)
}

// Only call synchronously after this Open RPC returned and its following write
// failed. The failed write never released dispatching, so no later dispatcher
// can own this source. Do not defer this after successful phase writes: exact
// replays can have the same source bytes. This releases local liveness only;
// disk still has the exact pre-send request, never an unpersisted Accept/child.
func releaseClosedOpenDispatchAfterWriteFailure(owner *LocalActionPerformData, dispatchSource, retryStep string) {
	owner.Lock()
	defer owner.Unlock()
	current, err := loadClosedChannelIntent(owner)
	if err != nil || current.source != dispatchSource || current.Step != "open_dispatching" ||
		current.Accept != nil || current.FundingCreated != nil || current.FundingSigned != nil || current.FundingBroadcasted != nil ||
		(retryStep != "open_inflight" && retryStep != "open_rejected") {
		return
	}
	current.Step = retryStep
	if raw, err := json.Marshal(current); err == nil {
		owner.ActionParam = string(raw)
	}
}

// A returned result is runtime-only, never a claim about an unknown cold
// dispatch. Store independent JSON bytes rather than the mutable RPC/monitor
// funding pointer. The owner object and exact pre-send source define its scope.
type closedFundingCreatedReturn struct {
	source, result string
	childID        int64
	created        []byte
	busy           atomic.Bool
}

func captureClosedFundingCreatedReturn(source string, funding *FundingReservation, step string) (*closedFundingCreatedReturn, error) {
	var returned closedChannelIntent
	if err := json.Unmarshal([]byte(source), &returned); err != nil {
		return nil, err
	}
	if returned.Step != "created_inflight" || returned.Accept == nil || returned.FundingCreated == nil ||
		funding == nil || funding.Id != returned.Accept.Id || funding.FundingCreated == nil ||
		funding.FundingCreated.Id != funding.Id || returned.FundingCreated.Id != funding.Id ||
		(step != "created_retry" && step != "funding_signed_received") {
		return nil, fmt.Errorf("invalid returned FundingCreated identity")
	}
	created, err := json.Marshal(returned.FundingCreated)
	if err != nil {
		return nil, err
	}
	actual, err := json.Marshal(funding.FundingCreated)
	if err != nil || !bytes.Equal(created, actual) {
		return nil, fmt.Errorf("returned FundingCreated changed exact request")
	}
	returned.Step = step
	if step == "funding_signed_received" {
		if funding.FundingSigned == nil || funding.FundingSigned.Id != funding.Id {
			return nil, fmt.Errorf("returned FundingSigned has a different funding child")
		}
		returned.FundingSigned = funding.FundingSigned
	}
	raw, err := json.Marshal(&returned)
	if err != nil {
		return nil, err
	}
	return &closedFundingCreatedReturn{source: source, result: string(raw), childID: funding.Id, created: created}, nil
}

// Claim one known returned result while retaining it until its corresponding
// durable write succeeds. A concurrent call, a changed owner/source/child or
// a cold Manager with no receipt must not release created_inflight or send RPC.
func (p *Manager) persistClosedFundingCreatedReturn(w common.Wallet, owner *LocalActionPerformData, intent *closedChannelIntent) (*closedChannelIntent, error) {
	value, ok := p.closedFundingCreatedReturns.Load(owner)
	if !ok {
		return nil, nil
	}
	returned := value.(*closedFundingCreatedReturn)
	created, err := json.Marshal(intent.FundingCreated)
	if err != nil || intent.source != returned.source || intent.Accept == nil || intent.Accept.Id != returned.childID ||
		intent.FundingCreated == nil || intent.FundingCreated.Id != returned.childID || !bytes.Equal(created, returned.created) {
		return nil, fmt.Errorf("returned FundingCreated no longer matches exact recovery owner")
	}
	owner.RLock()
	attached := false
	for _, child := range owner.ActionResvs {
		if child.ActionType == RESV_TYPE_OPEN && child.ResvId == returned.childID {
			attached = true
		}
	}
	owner.RUnlock()
	if !attached || !returned.busy.CompareAndSwap(false, true) {
		return nil, nil
	}
	var next closedChannelIntent
	if err := json.Unmarshal([]byte(returned.result), &next); err != nil {
		returned.busy.Store(false)
		return nil, err
	}
	next.source = returned.source
	funding, err := restoreClosedChannelFunding(w, &next, p)
	if err != nil {
		returned.busy.Store(false)
		return nil, err
	}
	if next.Step == "funding_signed_received" {
		err = p.persistClosedChannelIntentFunding(owner, &next, RS_PERFORM_ACTION_RUN_STARTED, funding, false)
	} else {
		err = p.persistClosedChannelIntent(owner, &next, RS_PERFORM_ACTION_RUN_STARTED)
	}
	if err != nil {
		returned.busy.Store(false)
		return nil, err
	}
	// Keep successful receipts permanently claimed: a delayed Load consumer
	// may retain this pointer while an exact retry reuses the same source.
	p.closedFundingCreatedReturns.CompareAndDelete(owner, returned)
	return &next, nil
}

func (p *Manager) funderClosedReopenForWallet(w common.Wallet, client NodeRPCClient, owner *LocalActionPerformData, intent *closedChannelIntent, opts FundingInitOptions, feeCfg *ChannelFeeConfig) (string, error) {
	if !closedIntentWalletMatches(owner, w) || intent.Choice != "reopen" {
		return "", fmt.Errorf("closed-channel recovery account or choice mismatch")
	}
	if owner.Status == closedIntentReopened {
		return intent.ChannelID, nil
	}
	if err := p.rejectClosedChannelIntentConflicts(owner, w); err != nil {
		return "", err
	}
	if intent.Step == "created_inflight" {
		returned, err := p.persistClosedFundingCreatedReturn(w, owner, intent)
		if err != nil {
			return "", err
		}
		if returned == nil {
			return "", closedChannelRecoveryPending(owner, intent.Step, nil)
		}
		intent = returned
	}
	if intent.Step == "open_dispatching" || intent.Step == "drain_inflight" {
		return "", closedChannelRecoveryPending(owner, intent.Step, nil)
	}
	var funding *FundingReservation
	var err error
	freshOpen := intent.Request == nil || intent.Step == "open_rejected"
	retryOpen := intent.Step == "open_inflight"
	if freshOpen && intent.TopupJSON != "" {
		fundingOutput, err := p.closedChannelSingleUTXOTopupFunding(w, owner, intent)
		if err != nil {
			return "", err
		}
		approvedRate, err := closedChannelSingleUTXOTopupFeeRate(intent)
		if err != nil {
			return "", err
		}
		if fundingOutput == nil || opts.NeedSendFundingTx || opts.FundingUtxo == nil || opts.Amount != fundingOutput.Value() ||
			opts.FeeRate != approvedRate || opts.FundingUtxo.OutPointStr != fundingOutput.OutPointStr ||
			!bytes.Equal(opts.FundingUtxo.OutValue.PkScript, fundingOutput.OutValue.PkScript) {
			return "", fmt.Errorf("closed-channel reopen must use only its exact confirmed, approved top-up output and fee rate")
		}
	}
	if retryOpen && (intent.Request == nil || intent.Accept != nil || intent.FundingCreated != nil || intent.FundingSigned != nil || intent.FundingBroadcasted != nil) {
		return "", closedChannelRecoveryPending(owner, intent.Step, fmt.Errorf("Open retry cannot replace an advanced funding child"))
	}
	if freshOpen {
		// This captured-wallet path replaces the public reopen branch. Preserve
		// its exact old RGB proof before the fresh c- record can replace it.
		if err := p.preserveSTPRGBClosedChannelSourceForWallet(intent.ChannelID, w); err != nil {
			return "", err
		}
		if opts.NeedSendFundingTx && len(opts.Outpoints) == 0 {
			opts.Outpoints, err = p.selectWalletFundingOutpointsForWallet(w, opts.FeeRate, opts.Amount, feeCfg)
			if err != nil {
				return "", err
			}
		}
		funding, err = p.initInitiatorFundingReservationForWallet(w, opts)
		if err != nil {
			return "", err
		}
		funding.ChannelId = intent.ChannelID
		if err := snapshotClosedChannelFunding(intent, funding); err != nil {
			return "", err
		}
	} else {
		funding, err = restoreClosedChannelFunding(w, intent, p)
		if err != nil {
			return "", err
		}
	}
	if freshOpen || retryOpen {
		// Claim only this exact local owner before dispatch. A concurrent call
		// sees dispatching; a later explicit retry restores the stored request,
		// signature and inputs rather than generating another Open obligation.
		intent.Step = "open_dispatching"
		if err := p.persistClosedChannelIntent(owner, intent, RS_PERFORM_ACTION_RUN_STARTED); err != nil {
			return "", err
		}
		dispatchSource := intent.source
		if err := client.SendOpenChannelReq(funding); err != nil {
			// A rejection of a first dispatch is definite. After an earlier lost
			// reply even a rejected replay cannot erase the possible original
			// acceptance, so keep the original exact request pending.
			if !retryOpen && errors.Is(err, errChannelProtocolRejected) {
				intent.Step = "open_rejected"
				if saveErr := p.persistClosedChannelIntent(owner, intent, RS_PERFORM_ACTION_STARTED); saveErr != nil {
					releaseClosedOpenDispatchAfterWriteFailure(owner, dispatchSource, "open_rejected")
					return "", errors.Join(err, saveErr)
				}
				return "", err
			}
			intent.Step = "open_inflight"
			if saveErr := p.persistClosedChannelIntent(owner, intent, RS_PERFORM_ACTION_RUN_STARTED); saveErr != nil {
				releaseClosedOpenDispatchAfterWriteFailure(owner, dispatchSource, "open_inflight")
				return "", errors.Join(err, saveErr)
			}
			return "", closedChannelRecoveryPending(owner, intent.Step, err)
		}
		if funding.Id == 0 || funding.Accept == nil {
			intent.Step = "open_inflight"
			if err := p.persistClosedChannelIntent(owner, intent, RS_PERFORM_ACTION_RUN_STARTED); err != nil {
				releaseClosedOpenDispatchAfterWriteFailure(owner, dispatchSource, "open_inflight")
				return "", err
			}
			return "", closedChannelRecoveryPending(owner, intent.Step, fmt.Errorf("Open response has no reservation identity"))
		}
		intent.Step = "open_accepted"
		if err := p.persistClosedChannelIntentFunding(owner, intent, RS_PERFORM_ACTION_RUN_STARTED, funding, false); err != nil {
			releaseClosedOpenDispatchAfterWriteFailure(owner, dispatchSource, "open_inflight")
			return "", err
		}
	}
	if intent.Step == "open_accepted" {
		if funding.Accept == nil || funding.Accept.OpenFee == nil {
			return "", fmt.Errorf("accepted funding fee configuration is unavailable")
		}
		funding.Channel.FeeCfg = NewFromOpenChannelFee(funding.Accept.OpenFee)
		if funding.NeedSendFundingTx {
			funding.Channel.Capacity = funding.Req.LocalFundingAmount - funding.Channel.FeeCfg.FeeToDAO()
			funding.FundingUtxos, err = p.AllowOpen(funding.Req.FeeRate, funding.Req.LocalFundingAmount, funding.Req.Outpoints, funding.Channel.FeeCfg)
			if err != nil {
				return "", err
			}
		}
		if err := p.funderProcessAcceptChannel(funding); err != nil {
			return "", err
		}
		intent.Step = "created_prepared"
		if err := p.persistClosedChannelIntentFunding(owner, intent, RS_PERFORM_ACTION_RUN_STARTED, funding, false); err != nil {
			return "", err
		}
	}
	if intent.Step == "created_prepared" || intent.Step == "created_retry" {
		intent.Step = "created_inflight"
		if err := p.persistClosedChannelIntent(owner, intent, RS_PERFORM_ACTION_RUN_STARTED); err != nil {
			return "", err
		}
		dispatchSource := intent.source
		if err := client.SendFundingCreatedReq(funding); err != nil {
			// A known child and the exact signed FundingCreated are retained.
			// A later explicit retry reuses that child, never a second Open.
			intent.Step = "created_retry"
			returned, captureErr := captureClosedFundingCreatedReturn(dispatchSource, funding, intent.Step)
			if saveErr := p.persistClosedChannelIntent(owner, intent, RS_PERFORM_ACTION_RUN_STARTED); saveErr != nil {
				if captureErr == nil {
					p.closedFundingCreatedReturns.Store(owner, returned)
				}
				return "", errors.Join(err, saveErr, captureErr)
			}
			return "", closedChannelRecoveryPending(owner, intent.Step, err)
		}
		intent.Step = "funding_signed_received"
		returned, captureErr := captureClosedFundingCreatedReturn(dispatchSource, funding, intent.Step)
		if err := p.persistClosedChannelIntentFunding(owner, intent, RS_PERFORM_ACTION_RUN_STARTED, funding, false); err != nil {
			if captureErr == nil {
				p.closedFundingCreatedReturns.Store(owner, returned)
			}
			return "", errors.Join(err, captureErr)
		}
	}
	if intent.Step == "funding_signed_received" {
		if err := p.funderProcessFundingSigned(funding); err != nil {
			return "", err
		}
		if funding.NeedSendFundingTx {
			funding.Channel.Status = CS_FUNDING_BROADCASTED
		} else {
			funding.Channel.Status = CS_FUNDING_CONFIRMED
		}
		funding.Status = ResvStatus(funding.Channel.Status)
		funding.Channel.ResvId = 0
		fundingTxID := funding.FundingUtxos[0].TxID()
		if funding.NeedSendFundingTx {
			fundingTxID = funding.FundingTx.TxID()
		}
		funding.FundingBroadcasted = &wwire.FundingBroadcasted{Id: funding.Id, FundingTxId: fundingTxID}
		funding.Channel.StaticMerkleRoot = funding.Channel.CalcStaticMerkleRoot()
		funding.Channel.UpdateTime = funding.Id
		intent.Step = "funding_pending"
		if err := p.persistClosedChannelIntentFunding(owner, intent, RS_PERFORM_ACTION_RUN_STARTED, funding, true); err != nil {
			return "", err
		}
	}
	if intent.Step != "funding_pending" && intent.Step != "funding_monitor" {
		return "", closedChannelRecoveryPending(owner, intent.Step, nil)
	}
	// An existing monitor may already have advanced this exact child. Never
	// replace its current channel with an older intent snapshot on a retry.
	if current, ok := p.GetFundingReservation(funding.Id); ok && current != nil && current.Channel != nil &&
		current.WalletId == owner.WalletId && current.ChannelId == intent.ChannelID && current.Channel.FundingTime == funding.Id {
		funding = current
	}
	if funding.Channel.Status == CS_READY {
		if err := p.completeClosedReopenIntent(funding); err != nil {
			return intent.ChannelID, err
		}
		return intent.ChannelID, nil
	}
	p.AddChannelToNode(funding.Channel)
	if funding.NeedSendFundingTx {
		broadcasted, err := p.BroadcastTxsIrreversibleL1([]*wire.MsgTx{funding.FundingTx}, "closed-channel reopen funding")
		if err != nil || !broadcasted {
			return intent.ChannelID, closedChannelRecoveryPending(owner, intent.Step, err)
		}
	}
	if funding.FundingBroadcasted != nil {
		if err := client.SendFundingBroadcastedReq(funding); err != nil {
			return intent.ChannelID, closedChannelRecoveryPending(owner, intent.Step, err)
		}
	}
	intent.Step = "funding_monitor"
	if err := p.persistClosedChannelIntent(owner, intent, RS_PERFORM_ACTION_RUN_STARTED); err != nil {
		return intent.ChannelID, err
	}
	return intent.ChannelID, nil
}

// This narrow exception validates an actual closed-channel owner and only its
// attached funding child. Ordinary open/sync guards have no arbitrary skip ID.
func (p *Manager) rejectClosedChannelIntentConflicts(owner *LocalActionPerformData, w common.Wallet) error {
	if !closedIntentWalletMatches(owner, w) {
		return fmt.Errorf("closed-channel intent account mismatch")
	}
	owner.RLock()
	intent, err := loadClosedChannelIntent(owner)
	children := append([]*SubActionInfo(nil), owner.ActionResvs...)
	owner.RUnlock()
	if err != nil {
		return err
	}
	if intent.Choice != "reopen" {
		return fmt.Errorf("closed-channel intent chose %s", intent.Choice)
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	for id, reservation := range p.resvMap {
		if id == owner.Id {
			continue
		}
		ownFunding := false
		for _, child := range children {
			if child.ActionType == RESV_TYPE_OPEN && child.ResvId == id && reservation.GetType() == RESV_TYPE_OPEN {
				ownFunding = true
			}
		}
		if ownFunding {
			continue
		}
		if reservationMatchesChannelContext(reservation, intent.ChannelID, w.GetWalletId()) {
			return fmt.Errorf("channel %s has another unfinished lifecycle", intent.ChannelID)
		}
		if other, ok := reservation.(*LocalActionPerformData); ok && other.Action == localActionClosedChannelRecovery && closedIntentWalletMatches(other, w) {
			other.RLock()
			record, parseErr := loadClosedChannelIntent(other)
			active := other.Status != closedIntentReopened && other.Status != closedIntentWithdrawn
			other.RUnlock()
			if parseErr != nil {
				return parseErr
			}
			if active && record.ChannelID == intent.ChannelID {
				return fmt.Errorf("another closed-channel recovery is pending")
			}
		}
	}
	return nil
}

func (p *Manager) completeClosedReopenIntent(funding *FundingReservation) error {
	for _, item := range p.GetLocalActionReservations() {
		owner, ok := item.(*LocalActionPerformData)
		if !ok || owner.Action != localActionClosedChannelRecovery || owner.WalletId != funding.WalletId {
			continue
		}
		owner.RLock()
		intent, err := loadClosedChannelIntent(owner)
		children := append([]*SubActionInfo(nil), owner.ActionResvs...)
		owner.RUnlock()
		if err != nil {
			return err
		}
		if intent.ChannelID != funding.ChannelId || intent.Choice != "reopen" {
			continue
		}
		for _, child := range children {
			if child.ActionType == RESV_TYPE_OPEN && child.ResvId == funding.Id {
				intent.Step = "reopened"
				return p.persistClosedChannelIntent(owner, intent, closedIntentReopened)
			}
		}
	}
	return nil
}

// Retain pending owners and positive tombstones without invoking the generic
// action state machine. Runtime signers are always restored from exact account.
func (p *Manager) rehydrateClosedChannelIntents() error {
	for _, item := range p.GetLocalActionReservations() {
		owner, ok := item.(*LocalActionPerformData)
		if !ok || owner.Action != localActionClosedChannelRecovery {
			continue
		}
		owner.RLock()
		intent, err := loadClosedChannelIntent(owner)
		done := owner.Status == closedIntentReopened || owner.Status == closedIntentWithdrawn
		cold := owner.LocalWallet() == nil
		owner.RUnlock()
		if err != nil {
			return err
		}
		if err := owner.InitLocalWallet(p); err != nil {
			return err
		}
		w := owner.LocalWallet()
		if !closedIntentWalletMatches(owner, w) {
			continue
		}
		// Only a cold-restored owner has no live dispatcher. Unlocking an
		// already-running Manager must not release an outstanding Open RPC.
		if cold && intent.Step == "open_dispatching" && intent.Request != nil && intent.Accept == nil && intent.FundingCreated == nil && intent.FundingSigned == nil && intent.FundingBroadcasted == nil {
			intent.Step = "open_inflight"
			if err := p.persistClosedChannelIntent(owner, intent, RS_PERFORM_ACTION_RUN_STARTED); err != nil {
				return err
			}
		}
		if done || intent.Accept == nil {
			continue
		}
		funding, err := restoreClosedChannelFunding(w, intent, p)
		if err != nil {
			return err
		}
		if funding.Id == 0 {
			return fmt.Errorf("accepted closed-channel recovery has no child id")
		}
		if intent.Step == "funding_pending" || intent.Step == "funding_monitor" {
			stored, readErr := p.LoadChannelInDB(intent.ChannelID)
			if readErr != nil && !errors.Is(readErr, indexer.ErrKeyNotFound) {
				return readErr
			}
			if readErr == nil && stored.FundingTime == funding.Id && stored.Status == CS_READY {
				if err := p.completeClosedReopenIntent(funding); err != nil {
					return err
				}
				continue
			}
		}
		if current, ok := p.GetFundingReservation(funding.Id); ok && current != nil && current.Channel != nil &&
			current.WalletId == owner.WalletId && current.ChannelId == intent.ChannelID && current.Channel.FundingTime == funding.Id && current.Channel.Status >= CS_FUNDING_BROADCASTED {
			continue
		}
		p.AddResv(funding)
	}
	return nil
}
