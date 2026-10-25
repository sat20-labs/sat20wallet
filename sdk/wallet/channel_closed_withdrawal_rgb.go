package wallet

import (
	"bytes"
	"context"
	"fmt"
	"reflect"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/schemas"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Missing native material must fail in the native branch, not fall through to
// an unrelated legacy Other package. This predicate grants no authorization.
func closedWithdrawalHasRGB(channel *ChannelInDB) bool {
	if channel == nil {
		return false
	}
	return len(channel.AssetFunding[rgb11wallet.Protocol]) != 0 ||
		channel.RemoteCommitment != nil && (channel.RemoteCommitment.RGBClientRecovery != nil ||
			len(channel.RemoteCommitment.AssetProofs[rgb11wallet.Protocol]) != 0)
}

// This initial explicit exit supports only the independently prepared RGB
// child and plain fee carriers. A mixed package is rejected in its entirety.
func rgbClosedWithdrawalPackage(channel *Channel, parent *wire.MsgTx) (*rgb11wallet.ChannelPackage, *CommitmentTx, error) {
	if channel == nil || channel.LocalWallet() == nil || !channel.IsInitiator || parent == nil || channel.RemoteCommitment == nil {
		return nil, nil, fmt.Errorf("missing RGB closed withdrawal context")
	}
	remote := channel.RemoteCommitment
	w := channel.LocalWallet()
	if channel.LocalWalletId != w.GetId() || channel.LocalChanCfg.WalletId != w.GetSubAccount() ||
		channel.LocalChanCfg.PaymentKey == nil || w.GetPaymentPubKey() == nil || !channel.LocalChanCfg.PaymentKey.IsEqual(w.GetPaymentPubKey()) {
		return nil, nil, fmt.Errorf("RGB withdrawal belongs to another wallet account")
	}
	var recoveryTx *wire.MsgTx
	if remote.RGBClientRecovery != nil {
		recoveryTx = remote.RGBClientRecovery.CommitTx
	}
	for _, tx := range []*wire.MsgTx{parent, remote.CommitTx, recoveryTx} {
		if tx == nil || len(tx.TxIn) == 0 || len(tx.TxOut) == 0 {
			return nil, nil, fmt.Errorf("incomplete RGB withdrawal transaction")
		}
		for _, input := range tx.TxIn {
			if input == nil {
				return nil, nil, fmt.Errorf("nil RGB withdrawal input")
			}
		}
		for _, output := range tx.TxOut {
			if output == nil || output.Value < 0 {
				return nil, nil, fmt.Errorf("invalid RGB withdrawal output")
			}
		}
	}
	if remote.CommitTx == nil || remote.CommitTx.TxID() != parent.TxID() || remote.RGBClientRecovery == nil ||
		remote.DeAnchorTx == nil || len(remote.Others) != 0 || len(remote.NextTxs) != 0 ||
		len(remote.AssetProofs) != 1 || len(remote.AssetProofs[rgb11wallet.Protocol]) == 0 {
		return nil, nil, fmt.Errorf("RGB withdrawal requires its complete separate server-main recovery package")
	}
	for _, proofs := range []map[string][]byte{channel.AssetFunding, channel.ClosingAssetProofs, remote.RecoveryProofs} {
		for protocol := range proofs {
			if protocol != rgb11wallet.Protocol {
				return nil, nil, fmt.Errorf("mixed-protocol closed withdrawal is not supported")
			}
		}
	}
	for name := range channel.FundingUtxos {
		if name.Protocol != rgb11wallet.Protocol && !indexer.IsPlainAsset(&name.AssetName) {
			return nil, nil, fmt.Errorf("mixed-protocol closed withdrawal is not supported")
		}
	}
	for _, commitment := range []*ChannelCommitment{channel.LocalCommitment, remote} {
		if commitment == nil {
			continue
		}
		for _, proofs := range []map[string][]byte{commitment.AssetProofs, commitment.RecoveryProofs} {
			for protocol := range proofs {
				if protocol != rgb11wallet.Protocol {
					return nil, nil, fmt.Errorf("mixed-protocol closed withdrawal is not supported")
				}
			}
		}
		for _, balances := range []map[AssetName]*Decimal{commitment.LocalBalance, commitment.RemoteBalance} {
			for name := range balances {
				if name.Protocol != rgb11wallet.Protocol && !indexer.IsPlainAsset(&name.AssetName) {
					return nil, nil, fmt.Errorf("mixed-protocol closed withdrawal is not supported")
				}
			}
		}
	}
	recovery := remote.RGBClientRecovery
	if recovery.DeAnchorTx != nil || len(recovery.RecoveryProofs) != 0 {
		return nil, nil, fmt.Errorf("unexpected RGB withdrawal dependencies")
	}
	if _, err := rgbClientRecoveryFetcher(channel, parent, recovery); err != nil {
		return nil, nil, err
	}
	pkg, err := rgb11wallet.DecodeChannelPackage(remote.AssetProofs[rgb11wallet.Protocol])
	if err != nil {
		return nil, nil, err
	}
	child, err := rgb11wallet.DecodeChannelPackage(recovery.AssetProofs[rgb11wallet.Protocol])
	if err != nil {
		return nil, nil, err
	}
	if err := verifySTPRGB11PackageContext(channel, pkg); err != nil {
		return nil, nil, err
	}
	if err := verifySTPRGB11PackageContext(channel, child); err != nil {
		return nil, nil, err
	}
	if len(pkg.Steps) == 0 || len(child.Steps) != len(pkg.Steps)+1 || pkg.Version != child.Version ||
		!reflect.DeepEqual(pkg.Funding, child.Funding) || !reflect.DeepEqual(pkg.Steps, child.Steps[:len(pkg.Steps)]) {
		return nil, nil, fmt.Errorf("RGB withdrawal changed its exact server-main proof")
	}
	last := pkg.Steps[len(pkg.Steps)-1]
	end := child.Steps[len(child.Steps)-1]
	if last.Context.Branch != "server" || end.Context.Branch != rgbClientRecoveryBranch || end.Context.Operation != string(STPOpForceClose) {
		return nil, nil, fmt.Errorf("RGB withdrawal changed its authorized branch")
	}
	return pkg, recovery, nil
}

// Read-only preflight. Historical validation establishes the exact old main
// and its paired burn; the separate outspend checks below establish current
// ownership or consumption by this exact already authorized child. In
// particular, a confirmed retry never requires the spent parent to be unspent.
func (p *Manager) prepareRGBClosedWithdrawal(channel *Channel, parent *wire.MsgTx) ([]*wire.MsgTx, *swire.MsgTx, int64, error) {
	pkg, recovery, err := rgbClosedWithdrawalPackage(channel, parent)
	if err != nil {
		return nil, nil, 0, err
	}
	ctx := context.Background()
	w := channel.LocalWallet()
	witness, _, err := p.nativeChannelWitness(w, channel.ChannelId)
	if err != nil || !bytes.Equal(witness, channel.RedeemScript) {
		return nil, nil, 0, fmt.Errorf("RGB withdrawal changed the fixed channel owner pair")
	}
	chains, err := p.newNativeSettlementChains(ctx, w)
	if err != nil {
		return nil, nil, 0, err
	}
	actual, err := chains.bitcoinTx(parent.TxID(), true)
	if err != nil || !equalSTPBitcoinTx(actual, parent) {
		return nil, nil, 0, fmt.Errorf("RGB withdrawal canonical server main changed: %w", err)
	}
	if err := chains.verifyBitcoinSpend(actual); err != nil {
		return nil, nil, 0, err
	}
	protocol := rgb11STPProtocol{}
	if _, err := protocol.VerifyChannelProof(p, channel, actual, channel.RemoteCommitment.AssetProofs[rgb11wallet.Protocol], STPOpForceClose); err != nil {
		return nil, nil, 0, err
	}
	fetcher, err := rgbClientRecoveryFetcher(channel, actual, recovery)
	if err != nil {
		return nil, nil, 0, err
	}
	fee, err := VerifySignedTxV2(recovery.CommitTx, fetcher)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("RGB withdrawal child is not completely authorized: %w", err)
	}
	if _, err := protocol.VerifyChannelProof(p, channel, recovery.CommitTx, recovery.AssetProofs[rgb11wallet.Protocol], STPOpForceClose); err != nil {
		return nil, nil, 0, err
	}
	descends, err := p.collectRGB11PrivateSettlementDescends(w, channel.ChannelId, pkg)
	if err != nil {
		return nil, nil, 0, err
	}
	// The stored peer locator may have only one signature. Retrieve its actual
	// canonical witness; graph validation below checks the exact asset burn.
	deanchor, err := chains.satoshiTx(channel.RemoteCommitment.DeAnchorTx.TxID())
	if err != nil {
		return nil, nil, 0, err
	}
	knownAssets := make(map[indexer.AssetName]bool)
	for _, source := range pkg.Funding {
		knownAssets[source.AssetName] = true
	}
	l2Previous, err := chains.satoshiPrevious(deanchor)
	if err != nil {
		return nil, nil, 0, err
	}
	for _, output := range deanchor.TxOut {
		for _, asset := range output.Assets {
			if !indexer.IsPlainAsset(&asset.Name) && (asset.Name.Protocol != rgb11wallet.Protocol || !knownAssets[asset.Name]) {
				return nil, nil, 0, fmt.Errorf("RGB withdrawal burn contains an unknown co-asset")
			}
		}
	}
	for _, input := range deanchor.TxIn {
		if input == nil || l2Previous.FetchPrevOutput(input.PreviousOutPoint) == nil {
			return nil, nil, 0, fmt.Errorf("RGB withdrawal burn is missing an input dependency")
		}
		for _, asset := range l2Previous.FetchPrevOutput(input.PreviousOutPoint).Assets {
			if !indexer.IsPlainAsset(&asset.Name) && (asset.Name.Protocol != rgb11wallet.Protocol || !knownAssets[asset.Name]) {
				return nil, nil, 0, fmt.Errorf("RGB withdrawal burn consumes an unknown co-asset")
			}
		}
	}
	found := false
	for _, id := range descends {
		found = found || id == deanchor.TxID()
	}
	if !found {
		descends = append(descends, deanchor.TxID())
	}
	inputs := make(map[string]bool, len(recovery.CommitTx.TxIn))
	inputAssets := make(map[string]indexer.AssetName, len(recovery.CommitTx.TxIn))
	for _, input := range recovery.CommitTx.TxIn {
		inputs[input.PreviousOutPoint.String()] = true // Fetcher checked nonnil and exact parent.
		inputAssets[input.PreviousOutPoint.String()] = indexer.AssetName{}
	}
	clientScript, err := GetP2TRpkScript(w.GetPaymentPubKey())
	if err != nil {
		return nil, nil, 0, err
	}
	childPackage, err := rgb11wallet.DecodeChannelPackage(recovery.AssetProofs[rgb11wallet.Protocol])
	if err != nil {
		return nil, nil, 0, err
	}
	for _, assignment := range childPackage.Steps[len(childPackage.Steps)-1].Assignments {
		if uint64(assignment.OutputIndex) >= uint64(len(recovery.CommitTx.TxOut)) || recovery.CommitTx.TxOut[assignment.OutputIndex] == nil ||
			!bytes.Equal(recovery.CommitTx.TxOut[assignment.OutputIndex].PkScript, clientScript) {
			return nil, nil, 0, fmt.Errorf("RGB withdrawal does not pay the current client wallet")
		}
	}
	selected := 0
	for _, assignment := range pkg.Steps[len(pkg.Steps)-1].Assignments {
		source, err := rgb11wallet.ChannelFundingFromStep(pkg.Steps[len(pkg.Steps)-1], assignment.OutputIndex, assignment.Amount)
		if err != nil {
			return nil, nil, 0, err
		}
		if !bytes.Equal(source.PkScript, channel.GetChannelPkScript()) {
			continue // The server CSV sibling is not claimed or released here.
		}
		if !inputs[source.OutPoint] {
			return nil, nil, 0, fmt.Errorf("RGB withdrawal omitted a fixed-client allocation")
		}
		inputAssets[source.OutPoint] = source.AssetName
		proof, err := source.NativeProof()
		if err != nil {
			return nil, nil, 0, err
		}
		output := indexer.NewTxOutput(source.Value)
		output.OutPointStr, output.OutValue.PkScript = source.OutPoint, append([]byte(nil), source.PkScript...)
		output.Assets = indexer.TxAssets{{Name: source.AssetName, Amount: *source.Amount.Clone(), BindingSat: 0}}
		validated, err := p.validateNativeChannelSource(ctx, w, channel.ChannelId,
			&NativeChannelSource{Version: 1, ChannelID: channel.ChannelId, Output: output, PublicProof: proof}, nativeproof.Historical)
		if err != nil {
			return nil, nil, 0, err
		}
		if err := p.validateRGBWithdrawalPrivateRelease(chains, validated, source.OutPoint, source.PkScript, actual.TxID(), deanchor.TxID(), descends); err != nil {
			return nil, nil, 0, err
		}
		selected++
	}
	if selected == 0 {
		return nil, nil, 0, fmt.Errorf("RGB withdrawal has no fixed-client allocation")
	}
	if err := verifySTPRGB11KnownCoassets(p, inputAssets); err != nil {
		return nil, nil, 0, err
	}
	// All inputs, including the plain fee reserve, must still belong to this
	// package. A foreign spend is never permission to import or complete it.
	for _, input := range recovery.CommitTx.TxIn {
		point := input.PreviousOutPoint.String()
		spent, err := chains.bitcoin.GetOutspend(point)
		if err != nil || spent == nil {
			return nil, nil, 0, fmt.Errorf("RGB withdrawal current spend evidence is unavailable: %w", err)
		}
		if !spent.Spent && spent.SpendingTx != "" {
			return nil, nil, 0, fmt.Errorf("RGB withdrawal has contradictory current spend evidence")
		}
		indexed, indexErr := p.l1IndexerClient.GetTxOutput(point)
		if indexErr == nil && indexed != nil && len(indexed.Assets) != 0 {
			return nil, nil, 0, fmt.Errorf("RGB withdrawal input contains an indexed co-asset")
		}
		if spent.Spent {
			if spent.SpendingTx != recovery.CommitTx.TxID() {
				return nil, nil, 0, fmt.Errorf("RGB withdrawal parent was spent by another transaction")
			}
			// The authoritative input list comes from exact canonical bytes,
			// not optional/indexer-specific Vin metadata on an outspend row.
			observed, err := chains.bitcoinTx(spent.SpendingTx, false)
			if err != nil || !equalSTPBitcoinTx(observed, recovery.CommitTx) {
				return nil, nil, 0, fmt.Errorf("RGB withdrawal observed child differs from its durable signed bytes: %w", err)
			}
			continue
		}
		if indexErr != nil || indexed == nil {
			return nil, nil, 0, fmt.Errorf("RGB withdrawal current co-asset evidence is unavailable: %w", indexErr)
		}
		utxo, err := chains.bitcoin.GetUTXO(point)
		previous := fetcher.FetchPrevOutput(input.PreviousOutPoint)
		if err != nil || utxo == nil || utxo.OutPoint != point || utxo.Value != previous.Value || !bytes.Equal(utxo.PkScript, previous.PkScript) {
			return nil, nil, 0, fmt.Errorf("RGB withdrawal parent is not the exact current carrier: %w", err)
		}
	}
	if err := chains.stable(); err != nil {
		return nil, nil, 0, err
	}
	return []*wire.MsgTx{actual.Copy(), recovery.CommitTx.Copy()}, deanchor.Copy(), fee, nil
}

// Historical evidence alone is never a spendable source. This private graph
// is used only with the exact parent/child/current-outspend gate above, never
// exposed as a public-pool receipt or registered as funding inventory.
func (p *Manager) validateRGBWithdrawalPrivateRelease(chains *nativeSettlementChains, validated *nativeproof.Result,
	point string, script []byte, producer, deanchorID string, descends []string) error {
	bootstrap := p.GetBootstrapNodePaymentPubKey()
	if bootstrap == nil || chains == nil || validated == nil || validated.Mode != nativeproof.Historical ||
		validated.Outpoint != point || !bytes.Equal(validated.PkScript, script) || len(script) == 0 || !canonicalNativeTxID(producer) {
		return fmt.Errorf("RGB withdrawal has no exact historical private release context")
	}
	graph := &nativeReceiptGraph{manager: p, chains: chains, asset: validated.Asset,
		bootstrap: bootstrap.SerializeCompressed(), states: make(map[schemas.InputRef]*nativeReceiptAllocation),
		steps: make(map[string]*nativeReceiptStep), points: make(map[string][]*nativeReceiptAllocation),
		anchorChecked: make(map[string]bool), descends: make(map[string]*nativeReceiptDescend),
		usedDescends: make(map[string]bool), delayedPools: make(map[string]bool), requiredPrivate: string(script),
		closedPrivateProducer: producer}
	if err := graph.walkValidatedHistory(validated, descends); err != nil {
		return err
	}
	if graph.descends[producer] == nil || graph.descends[producer].id != deanchorID {
		return fmt.Errorf("RGB withdrawal burn is not the exact stored server-main pair")
	}
	return graph.validateClosedPrivateTerminal(point, script, producer)
}

// Only native completion lives here; common C2 owns persistence and retries.
func (p *Manager) completeRGBClosedWithdrawal(resv *ClosingReservation) error {
	if resv == nil || resv.Channel == nil || resv.Channel.LocalWallet() == nil || len(resv.PreTxs) != 2 || resv.PreTxs[1] == nil {
		return fmt.Errorf("missing RGB withdrawal completion material")
	}
	w := resv.Channel.LocalWallet()
	child := resv.PreTxs[1]
	chains, err := p.newNativeSettlementChains(context.Background(), w)
	if err != nil {
		return err
	}
	confirmedChild, err := chains.bitcoinTx(child.TxID(), true)
	if err != nil || !equalSTPBitcoinTx(confirmedChild, child) {
		return fmt.Errorf("RGB withdrawal confirmed child differs from its durable signed bytes: %w", err)
	}
	pkg, err := rgb11wallet.DecodeChannelPackage(resv.AssetProofs[rgb11wallet.Protocol])
	if err != nil {
		return err
	}
	last := pkg.Steps[len(pkg.Steps)-1]
	for _, assignment := range last.Assignments {
		funding, err := rgb11wallet.ChannelFundingFromStep(last, assignment.OutputIndex, assignment.Amount)
		if err != nil {
			return err
		}
		if _, err := rgb11wallet.ValidateChannelFunding(context.Background(), funding, chains.bitcoin, nativeproof.Current, GetChainParam().Name); err != nil {
			return err
		}
	}
	if err := chains.stable(); err != nil {
		return err
	}
	// Import performs full package/historical and actual mined validation,
	// then projects only current unspent outputs owned by this wallet.
	if err := (rgb11STPProtocol{}).ImportChannelSettlement(p, child, resv.AssetProofs[rgb11wallet.Protocol]); err != nil {
		return err
	}
	return nil
}
