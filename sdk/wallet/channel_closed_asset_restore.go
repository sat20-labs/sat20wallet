package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

const closedChannelAssetRestoreWait = 30 * time.Second

// A policy exists only for the private automatic path. It is not funding
// authorization and never changes the behavior of explicit Expand callers.
type closedChannelAssetRestorePolicy struct {
	ctx        context.Context
	wallet     common.Wallet
	owner      *LocalActionPerformData
	projection *rgb11Manager
}

func closedChannelAssetWalletMatches(channel *Channel, w common.Wallet) bool {
	return channel != nil && w != nil && w.GetPaymentPubKey() != nil && channel.LocalWallet() != nil &&
		channel.LocalWallet().GetPaymentPubKey() != nil && channel.LocalChanCfg.PaymentKey != nil &&
		channel.LocalWallet().GetWalletId() == w.GetWalletId() &&
		channel.LocalChanCfg.WalletId == w.GetSubAccount() &&
		channel.LocalWallet().GetPaymentPubKey().IsEqual(w.GetPaymentPubKey()) &&
		channel.LocalChanCfg.PaymentKey.IsEqual(w.GetPaymentPubKey())
}

func (policy *closedChannelAssetRestorePolicy) beforeRequest(p *Manager, resv *SplicingReservation) error {
	if err := policy.ctx.Err(); err != nil {
		return err
	}
	if !closedChannelAssetWalletMatches(resv.Channel, policy.wallet) || resv.WalletId != policy.wallet.GetWalletId() {
		return fmt.Errorf("closed-channel asset request wallet mismatch")
	}
	// This durable request association precedes even the first peer obligation.
	return p.persistClosedChannelAssetRestore(policy.owner, resv, "request_prepared")
}

func (policy *closedChannelAssetRestorePolicy) accepted(p *Manager, resv *SplicingReservation) error {
	// Even a cancellation after the RPC must first retain its accepted child.
	if err := p.persistClosedChannelAssetRestore(policy.owner, resv, "child_accepted"); err != nil {
		return err
	}
	return policy.ctx.Err()
}

// Use a separate captured account projection, never the live selection's RGB
// overlay when deciding whether an apparently plain stub can be consumed.
func (policy *closedChannelAssetRestorePolicy) output(p *Manager, point string) (*TxOutput, error) {
	if policy.projection == nil {
		scoped, err := p.newScopedRGB11Manager(localRGB11Account{WalletID: policy.wallet.GetId(), AccountIndex: policy.wallet.GetSubAccount(), Address: policy.wallet.GetAddress(), Wallet: policy.wallet})
		if err != nil {
			return nil, err
		}
		policy.projection = scoped
	}
	return policy.projection.getL1TxOutput(point)
}

func (policy *closedChannelAssetRestorePolicy) validateSource(p *Manager, resv *SplicingReservation, point string) error {
	if err := policy.ctx.Err(); err != nil {
		return err
	}
	existing, err := p.l1IndexerClient.GetExistingUtxos([]string{point})
	if err != nil {
		return fmt.Errorf("cannot revalidate exact asset output: %w", err)
	}
	if len(existing) != 1 || existing[0] != point {
		return fmt.Errorf("exact asset output is no longer unspent")
	}
	actual, err := policy.output(p, point)
	if err != nil {
		return err
	}
	if actual == nil || actual.OutPointStr != point || actual.Value() != resv.SplicingValue || !bytes.Equal(actual.OutValue.PkScript, resv.Channel.GetChannelPkScript()) {
		return fmt.Errorf("exact asset output no longer matches its channel")
	}
	protocol, err := STPAssetProtocolFor(&resv.AssetName.AssetName, STPOpExpand)
	if err != nil {
		return err
	}
	if err := protocol.VerifyInput(resv.AssetName, actual); err != nil {
		return err
	}
	amount, _ := actual.GetAssetV2(&resv.AssetName.AssetName)
	if resv.SplicingAmt == nil || amount.Cmp(resv.SplicingAmt) != 0 {
		return fmt.Errorf("exact asset output amount changed")
	}
	return policy.ctx.Err()
}

func (policy *closedChannelAssetRestorePolicy) validateExistingStubs(p *Manager, resv *SplicingReservation) error {
	if err := policy.ctx.Err(); err != nil {
		return err
	}
	_, value := NeedStubUtxoForChannel(&resv.AssetName.AssetName)
	stubs := append([]*TxOutput(nil), resv.Channel.GetStubUtxoForAsset(resv.AssetName)...)
	stubs = append(stubs, resv.Fees...)
	if len(stubs) == 0 {
		return nil
	}
	points := make([]string, 0, len(stubs))
	seen := make(map[string]bool)
	for _, stub := range stubs {
		if stub == nil || stub.OutPointStr == "" || seen[stub.OutPointStr] {
			return fmt.Errorf("invalid or duplicated existing channel stub")
		}
		seen[stub.OutPointStr] = true
		points = append(points, stub.OutPointStr)
	}
	existing, err := p.l1IndexerClient.GetExistingUtxos(points)
	if err != nil {
		return fmt.Errorf("cannot revalidate existing channel stubs: %w", err)
	}
	if len(existing) != len(points) {
		return fmt.Errorf("existing channel stubs are no longer unspent")
	}
	for _, point := range existing {
		if !seen[point] {
			return fmt.Errorf("existing channel stub membership changed")
		}
		delete(seen, point)
	}
	if len(seen) != 0 {
		return fmt.Errorf("existing channel stub membership is incomplete")
	}
	for _, point := range points {
		actual, err := policy.output(p, point)
		if err != nil {
			return fmt.Errorf("cannot inspect existing channel stub %s: %w", point, err)
		}
		if actual == nil || actual.OutPointStr != point || actual.Value() != value || actual.HasAsset() ||
			!bytes.Equal(actual.OutValue.PkScript, resv.Channel.GetChannelPkScript()) {
			return fmt.Errorf("existing channel stub %s no longer matches its plain channel output", point)
		}
	}
	return policy.ctx.Err()
}

// Start precisely one old private channel output. The caller may hand its child
// to the existing monitor; this function never bulk-enumerates assets or waits.
func (p *Manager) startClosedChannelAssetRestoreForWallet(ctx context.Context, w common.Wallet, channelID, assetName, exactUTXO string) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	channel := p.GetChannel(channelID)
	if channel == nil {
		return 0, fmt.Errorf("closed-channel asset restoration requires a READY channel")
	}
	channel.Mutex.RLock()
	matched := closedChannelAssetWalletMatches(channel, w)
	channel.Mutex.RUnlock()
	if !matched {
		return 0, fmt.Errorf("closed-channel asset restoration wallet mismatch")
	}
	if exactUTXO == "" {
		return 0, fmt.Errorf("missing exact closed-channel asset output")
	}
	asset := ParseAssetString(assetName)
	if asset == nil {
		return 0, fmt.Errorf("invalid asset name %s", assetName)
	}
	if err := ValidateSTPAsset(asset, STPOpExpand); err != nil {
		return 0, err
	}
	captured := w.Clone()
	owner, childID, err := p.prepareClosedChannelAssetRestore(captured, channelID, assetName, exactUTXO)
	if err != nil || childID > 0 {
		return childID, err
	}
	if channel.PeerRPC == nil {
		return 0, fmt.Errorf("closed-channel asset restoration peer is unavailable")
	}
	contracts, err := channel.PeerRPC.GetDeployedContractsReq()
	if err != nil {
		return 0, fmt.Errorf("cannot verify restored asset contract control: %w", err)
	}
	for _, url := range contracts {
		if ExtractChannelId(url) == channelID && strings.Contains(url, assetName) {
			return 0, fmt.Errorf("asset %s is controlled by contract", assetName)
		}
	}
	if indexer.GetStakeAssetName(p.GetSyncHeightL1()) == assetName {
		return 0, fmt.Errorf("asset %s is controlled by contract", assetName)
	}
	request, err := json.Marshal(struct {
		Channel, Asset, Input, Reason string
		Memo                          []byte
	}{channelID, assetName, exactUTXO, "", nil})
	if err != nil {
		return 0, err
	}
	evidence, err := p.prepareSTPRGBExpansionEvidence(channel, asset, exactUTXO, request)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	if !closedChannelAssetWalletMatches(channel, captured) || !channel.IsReady() || !channel.IsInitiator || channel.IsBusy() {
		return 0, fmt.Errorf("closed-channel asset restoration no longer matches its READY account")
	}
	if err := evidence.ValidateForChannel(channel, request); err != nil {
		return 0, err
	}
	policy := &closedChannelAssetRestorePolicy{ctx: ctx, wallet: captured, owner: owner}
	_, _, childID, err = p.funderInitExpandingProcessWithRGBEvidenceAndPolicy(channel, asset, exactUTXO, "", nil, evidence, policy)
	return childID, err
}

// Absence from the runtime map is not completion. The normal monitor writes a
// durable closed child; its exact READY channel and funding bucket must agree.
func (p *Manager) waitClosedChannelAssetRestoreForWallet(ctx context.Context, w common.Wallet, channelID, assetName, exactUTXO string, childID int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	bounded, cancel := context.WithTimeout(ctx, closedChannelAssetRestoreWait)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := bounded.Err(); err != nil {
			return err
		}
		finished, err := p.closedChannelAssetRestoreConfirmed(w, channelID, assetName, exactUTXO, childID)
		if err != nil || finished {
			return err
		}
		select {
		case <-bounded.Done():
			return bounded.Err()
		case <-ticker.C:
		}
	}
}

func (p *Manager) closedChannelAssetRestoreConfirmed(w common.Wallet, channelID, assetName, exactUTXO string, childID int64) (bool, error) {
	if w == nil || w.GetPaymentPubKey() == nil || childID <= 0 || exactUTXO == "" {
		return false, fmt.Errorf("invalid closed-channel asset child identity")
	}
	if live, ok := p.GetSplicingReservation(childID); ok {
		live.RLock()
		matched := live.WalletId == w.GetWalletId() && live.ChannelId == channelID && live.AssetName != nil && live.AssetName.String() == assetName && !live.NeedSendSplicingTx
		live.RUnlock()
		if !matched {
			return false, fmt.Errorf("closed-channel asset child identity mismatch")
		}
	}
	stored, err := LoadReservation(p.db, nil, RESV_TYPE_SPLICING, childID)
	if err != nil {
		return false, fmt.Errorf("closed-channel asset child is unavailable, not restored: %w", err)
	}
	child, ok := stored.(*SplicingReservation)
	if !ok || child.WalletId != w.GetWalletId() || child.ChannelId != channelID || child.AssetName == nil || child.AssetName.String() != assetName || child.NeedSendSplicingTx {
		return false, fmt.Errorf("durable closed-channel asset child identity mismatch")
	}
	if child.Status != RS_CLOSED {
		return false, nil
	}
	channel, err := p.LoadChannelInDB(channelID)
	if err != nil {
		return false, err
	}
	if channel.Status != CS_READY || channel.UpdateTime != childID || channel.LocalWalletId != w.GetId() || channel.LocalChanCfg.WalletId != w.GetSubAccount() ||
		channel.LocalChanCfg.PaymentKey == nil || !channel.LocalChanCfg.PaymentKey.IsEqual(w.GetPaymentPubKey()) {
		return false, fmt.Errorf("closed asset child lacks its exact durable READY channel")
	}
	for _, output := range channel.FundingUtxos[*child.AssetName] {
		if output == nil || output.OutPointStr != exactUTXO {
			continue
		}
		amt, _ := output.GetAssetV2(&child.AssetName.AssetName)
		if output.Value() == child.SplicingValue && child.SplicingAmt != nil && amt.Cmp(child.SplicingAmt) == 0 &&
			bytes.Equal(output.OutValue.PkScript, channel.GetChannelPkScript()) {
			return true, nil
		}
	}
	return false, fmt.Errorf("closed asset child lacks its exact durable funding output")
}

// Only the reopened intent for this captured account and this current funding
// generation can authorize recovery. Older terminal owners are not new jobs.
func (p *Manager) closedChannelAssetRestoreOwnerForWallet(w common.Wallet, channelID string) (*LocalActionPerformData, *closedChannelIntent, error) {
	channel := p.GetActiveChannelWithId(channelID)
	if channel == nil {
		return nil, nil, nil
	}
	channel.Mutex.RLock()
	matched, fundingID := closedChannelAssetWalletMatches(channel, w), channel.FundingTime
	channel.Mutex.RUnlock()
	if !matched || fundingID <= 0 {
		return nil, nil, nil
	}
	var found *LocalActionPerformData
	var snapshot *closedChannelIntent
	for _, reservation := range p.GetLocalActionReservations() {
		owner, ok := reservation.(*LocalActionPerformData)
		if !ok || owner.Action != localActionClosedChannelRecovery || !closedIntentWalletMatches(owner, w) {
			continue
		}
		owner.RLock()
		intent, err := loadClosedChannelIntent(owner)
		status := owner.Status
		linked := false
		for _, child := range owner.ActionResvs {
			if child != nil && child.ActionType == RESV_TYPE_OPEN && child.ResvId == fundingID {
				linked = true
			}
		}
		owner.RUnlock()
		if err != nil {
			return nil, nil, err
		}
		if status != closedIntentReopened || intent.Choice != "reopen" || intent.ChannelID != channelID || !linked {
			continue
		}
		if found != nil {
			return nil, nil, fmt.Errorf("multiple closed-channel asset owners match current funding")
		}
		found, snapshot = owner, intent
	}
	return found, snapshot, nil
}

// A signed heartbeat drives one exact source and the existing monitor, not an
// all-account scheduler. Completion queues the existing wake for the next item.
func (p *Manager) driveClosedChannelAssetRestoreForWallet(ctx context.Context, w common.Wallet, client NodeRPCClient, channelID string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if client == nil {
		return false, fmt.Errorf("closed-channel asset peer is unavailable")
	}
	owner, intent, err := p.closedChannelAssetRestoreOwnerForWallet(w, channelID)
	if err != nil || owner == nil {
		return false, err
	}
	if intent.AssetsComplete {
		return false, nil
	}
	pending := intent.AssetRestore
	var assetName, point string
	var childID int64
	if pending != nil && pending.Step != "completed" {
		assetName, point, childID = pending.AssetName, pending.ExactUTXO, pending.ChildID
	} else {
		assetName, point, err = p.nextClosedChannelAssetRestoreSource(ctx, w, channelID)
		if err != nil {
			return false, err
		}
		if point == "" {
			if err := p.completeClosedChannelAssets(owner); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	if childID == 0 {
		childID, err = p.startClosedChannelAssetRestoreForWallet(ctx, w, channelID, assetName, point)
		if err != nil {
			return false, err
		}
	}
	if childID <= 0 {
		return false, fmt.Errorf("closed-channel asset request has no monitorable child")
	}
	if err := p.waitClosedChannelAssetRestoreForWallet(ctx, w, channelID, assetName, point, childID); err != nil {
		return false, err
	}
	if err := p.completeClosedChannelAssetRestore(owner, childID); err != nil {
		return false, err
	}
	p.wakeChannelHeartbeat()
	return false, nil
}

// Keep the established account-based (BRC20) first order. All candidates are
// private outputs at the old/user channel address, never ordinary wallet funds.
// A missing listing is not an empty inventory or proof of all-assets restored.
func (p *Manager) nextClosedChannelAssetRestoreSource(ctx context.Context, w common.Wallet, channelID string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	channel := p.GetChannel(channelID)
	if channel == nil {
		return "", "", fmt.Errorf("asset restoration channel is not READY")
	}
	channel.Mutex.RLock()
	matched := closedChannelAssetWalletMatches(channel, w) && channel.IsReady()
	address := channel.Address
	controlled := channel.UtxosInControlV2()
	known := make(map[string]*TxOutput, len(controlled))
	for point, output := range controlled {
		if output != nil {
			known[point] = output.Clone()
		}
	}
	channel.Mutex.RUnlock()
	if !matched {
		return "", "", fmt.Errorf("asset inventory no longer matches captured READY account")
	}
	owner, intent, err := p.closedChannelAssetRestoreOwnerForWallet(w, channelID)
	if err != nil {
		return "", "", err
	}
	if owner == nil || intent.AssetExpectations == nil {
		return "", "", fmt.Errorf("prior channel asset inventory is unknown; asset restoration remains pending")
	}
	assets, err := p.channelAssetSummaryWithoutLucky(address, false)
	if err != nil {
		return "", "", fmt.Errorf("cannot read closed-channel asset inventory: %w", err)
	}
	// Public summaries may omit already-ascended/invalid BRC20 carriers.
	// Old metadata only forces ticker discovery and an exact quantity floor;
	// every actual source still comes from the current invalid-inclusive list.
	candidates := append([]*indexer.AssetInfo(nil), assets...)
	seen := make(map[string]bool)
	for _, asset := range candidates {
		if asset != nil {
			seen[asset.Name.String()] = true
		}
	}
	expected := make(map[string]*Decimal)
	for _, legacy := range intent.AssetExpectations {
		name := legacy.Asset.String()
		if indexer.IsPlainAsset(&legacy.Asset.AssetName) || legacy.Amount == nil {
			return "", "", fmt.Errorf("prior asset %s has no usable non-BTC quantity expectation", name)
		}
		if err := legacy.Amount.Validate(); err != nil {
			return "", "", err
		}
		if legacy.Amount.Sign() < 0 {
			return "", "", fmt.Errorf("prior asset %s has a negative quantity expectation", name)
		}
		if _, duplicate := expected[name]; duplicate {
			return "", "", fmt.Errorf("prior asset %s has duplicate quantity expectations", name)
		}
		expected[name] = legacy.Amount.Clone()
		if !seen[name] {
			candidates = append(candidates, &indexer.AssetInfo{Name: legacy.Asset.AssetName, Amount: *indexer.NewDefaultDecimal(0)})
			seen[name] = true
		}
	}
	// Existing plain outputs are also the only authorized source of stubs.
	// Recover non-BTC assets before allowing plain expansion to consume them;
	// retain BRC20 priority, and never classify plain BTC by protocol alone.
	priority := func(asset *indexer.AssetInfo) int {
		if asset == nil {
			return 3
		}
		if indexer.IsPlainAsset(&asset.Name) {
			return 2
		}
		if STPPolicyFor(&AssetName{AssetName: asset.Name}).AccountBased {
			return 0
		}
		return 1
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := priority(candidates[i]), priority(candidates[j])
		if left != right {
			return left < right
		}
		if candidates[i] == nil || candidates[j] == nil {
			return false
		}
		return candidates[i].Name.String() < candidates[j].Name.String()
	})
	for _, asset := range candidates {
		if asset == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		var outputs []*indexerwire.TxOutputInfo
		if STPPolicyFor(&AssetName{AssetName: asset.Name}).AccountBased {
			outputs = p.l1IndexerClient.GetUtxoListWithBRC20Ticker(address, &asset.Name, true)
		} else {
			outputs = p.l1IndexerClient.GetUtxoListWithTicker(address, &asset.Name)
		}
		var listedPlain *Decimal
		listedPlainPoints := make(map[string]bool)
		for _, output := range outputs {
			if output == nil || output.OutPoint == "" {
				return "", "", fmt.Errorf("malformed closed-channel asset listing")
			}
			if indexer.IsPlainAsset(&asset.Name) && len(output.Assets) != 0 {
				continue
			}
			if controlledOutput, controlled := known[output.OutPoint]; controlled {
				if indexer.IsPlainAsset(&asset.Name) {
					// Current Bitcoin listings classify ascended/invalid BRC
					// carriers as plain. Their controlled historical metadata
					// still retains the old inscription footprint; do not
					// subtract it again or adopt the same source a second time.
					if listedPlainPoints[output.OutPoint] || output.Value != controlledOutput.Value() ||
						!bytes.Equal(output.PkScript, controlledOutput.OutValue.PkScript) {
						return "", "", fmt.Errorf("current plain listing changed or duplicated a controlled output")
					}
					listedPlainPoints[output.OutPoint] = true
					listedPlain = listedPlain.Add(indexer.NewDefaultDecimal(output.Value))
				}
				continue
			}
			lucky, err := p.isBTCLuckyL1Output(output.OutPoint, nil)
			if err != nil {
				return "", "", err
			}
			if lucky {
				continue
			}
			if err := ValidateSTPAsset(&asset.Name, STPOpExpand); err != nil {
				return "", "", err
			}
			if indexer.GetStakeAssetName(p.GetSyncHeightL1()) == asset.Name.String() {
				return "", "", fmt.Errorf("unrestored channel asset %s is contract controlled", asset.Name.String())
			}
			return asset.Name.String(), output.OutPoint, nil
		}
		var accounted *Decimal
		if indexer.IsPlainAsset(&asset.Name) {
			// An absent or incomplete current listing still cannot satisfy the
			// unchanged public-summary quantity gate below.
			accounted = listedPlain
		} else {
			for _, output := range known {
				amount, _ := output.GetAssetV2(&asset.Name)
				accounted = accounted.Add(amount)
			}
		}
		if accounted.Cmp(&asset.Amount) < 0 {
			return "", "", fmt.Errorf("asset %s still has unmanaged value or its exact listing is unavailable", asset.Name.String())
		}
		if wanted := expected[asset.Name.String()]; wanted != nil && accounted.Cmp(wanted) < 0 {
			return "", "", fmt.Errorf("prior asset %s expected client quantity %s is not restored", asset.Name.String(), wanted.String())
		}
	}
	return "", "", nil
}

// Existing explicit expansion retains its behavior unless this exact account
// already has a signed automatic restoration obligation. Check before loading,
// preparing or signing another request, under the caller's original channel lock.
func (p *Manager) rejectClosedChannelAssetRestoreOverlap(channel *Channel, assetName, point string, policy *closedChannelAssetRestorePolicy) error {
	if channel == nil || channel.LocalWallet() == nil || channel.LocalWallet().GetPaymentPubKey() == nil {
		return nil
	}
	for _, item := range p.GetLocalActionReservations() {
		owner, ok := item.(*LocalActionPerformData)
		if !ok || owner.Action != localActionClosedChannelRecovery || !closedIntentWalletMatches(owner, channel.LocalWallet()) {
			continue
		}
		owner.RLock()
		intent, err := loadClosedChannelIntent(owner)
		owner.RUnlock()
		if err != nil {
			return err
		}
		if intent.ChannelID != channel.ChannelId || intent.AssetRestore == nil {
			continue
		}
		leaf := intent.AssetRestore
		if leaf.Step != "request_prepared" && leaf.Step != "child_accepted" {
			continue
		}
		// The private caller still has to persist the exact signed request via
		// its existing owner CAS. This exemption is not a public retry grant.
		if policy != nil && policy.owner == owner && leaf.AssetName == assetName && leaf.ExactUTXO == point && closedChannelAssetWalletMatches(channel, policy.wallet) {
			continue
		}
		return closedChannelRecoveryPending(owner, "asset_"+leaf.Step, nil)
	}
	return nil
}
