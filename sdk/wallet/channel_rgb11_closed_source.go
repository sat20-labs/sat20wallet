package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

// This is part of the explicit reopen transaction, not a read-only evidence
// preparation. The existing backup retains the complete old signed obligation,
// including the dormant recovery branch, before c- can name a fresh channel.
func (p *Manager) preserveSTPRGBClosedChannelSource(channelID string) error {
	return p.preserveSTPRGBClosedChannelSourceForWallet(channelID, p.GetWallet())
}

func (p *Manager) preserveSTPRGBClosedChannelSourceForWallet(channelID string, owner common.Wallet) error {
	raw, err := p.GetDB().Read([]byte(GetChannelKey(channelID)))
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var stored ChannelInDB
	if err := DecodeFromBytes(raw, &stored); err != nil {
		return err
	}
	remote := stored.RemoteCommitment
	if remote == nil || len(remote.AssetProofs[rgb11wallet.Protocol]) == 0 || stored.Status != CS_CLOSED_FORCELY {
		return nil
	}
	if owner == nil || !stored.IsInitiator || stored.ChannelId != channelID ||
		stored.LocalWalletId != owner.GetId() || stored.LocalChanCfg.WalletId != owner.GetSubAccount() ||
		stored.LocalChanCfg.PaymentKey == nil || !stored.LocalChanCfg.PaymentKey.IsEqual(owner.GetPaymentPubKey()) ||
		remote.CommitTx == nil || stored.ClosingTx == nil || remote.CommitTx.TxID() != stored.ClosingTx.TxID() {
		return fmt.Errorf("closed RGB source does not match the reopening account and peer commitment")
	}
	// No PrepareForSave or recovery loader: preserve the old DB bytes exactly.
	return p.GetDB().Write([]byte(GetChannelBackupKey(channelID)), append([]byte(nil), raw...))
}

// Recover only the selected carrier from the last closed server commitment.
// This function is called after releasing the capture read lock and before the
// original expansion business lock. It never writes a source, projection,
// backup, channel balance, or inventory.
func (p *Manager) prepareSTPRGBClosedChannelSource(f *STPRGBReservationEvidence, owner common.Wallet,
	asset *indexer.AssetName, point string) ([]byte, error) {
	if f == nil || owner == nil || asset == nil || asset.Protocol != rgb11wallet.Protocol {
		return nil, nil
	}
	if len(f.capturedInventory) != 0 {
		inventory, err := rgb11wallet.DecodeChannelInventory(f.capturedInventory)
		if err != nil {
			return nil, err
		}
		for _, source := range inventory.Sources {
			if source.OutPoint == point {
				return nil, nil // Existing current funding remains authoritative.
			}
		}
	}
	raw, err := p.GetDB().Read([]byte(GetChannelBackupKey(f.channelID)))
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stored ChannelInDB
	if err := DecodeFromBytes(raw, &stored); err != nil {
		return nil, err
	}
	remote := stored.RemoteCommitment
	if remote == nil || len(remote.AssetProofs[rgb11wallet.Protocol]) == 0 || stored.Status != CS_CLOSED_FORCELY {
		return nil, nil
	}
	if !stored.IsInitiator || stored.ChannelId != f.channelID || stored.CommitHeight < 0 || stored.CommitHeight >= f.capturedHeight ||
		stored.LocalWalletId != f.walletID || stored.LocalChanCfg.WalletId != f.account ||
		stored.LocalChanCfg.PaymentKey == nil || !bytes.Equal(stored.LocalChanCfg.PaymentKey.SerializeCompressed(), f.paymentKey) ||
		remote.CommitTx == nil || stored.ClosingTx == nil || remote.CommitTx.TxID() != stored.ClosingTx.TxID() {
		return nil, fmt.Errorf("RGB closed source backup has a foreign account, generation, or commitment")
	}
	witness, _, err := p.nativeChannelWitness(owner, f.channelID)
	if err != nil || !bytes.Equal(witness, stored.RedeemScript) {
		return nil, fmt.Errorf("RGB closed source changed its fixed private owner pair")
	}
	pkg, err := rgb11wallet.DecodeChannelPackage(remote.AssetProofs[rgb11wallet.Protocol])
	if err != nil {
		return nil, err
	}
	old := &Channel{ChannelInDB: stored, manager: p, localWallet: owner}
	if err := verifySTPRGB11PackageContext(old, pkg); err != nil {
		return nil, err
	}
	last := pkg.Steps[len(pkg.Steps)-1]
	if last.Context.Branch != "server" {
		return nil, fmt.Errorf("RGB closed source is not the stored server-main branch")
	}
	var selected *rgb11wallet.ChannelFunding
	for _, assignment := range last.Assignments {
		source, err := rgb11wallet.ChannelFundingFromStep(last, assignment.OutputIndex, assignment.Amount)
		if err != nil {
			return nil, err
		}
		if source.OutPoint != point {
			continue
		}
		if selected != nil || source.AssetName != *asset || !bytes.Equal(source.PkScript, stored.GetChannelPkScript()) {
			return nil, fmt.Errorf("RGB closed source is not one complete selected private carrier")
		}
		selected = &source
	}
	if selected == nil {
		return nil, nil
	}
	ctx := context.Background()
	chains, err := p.newNativeSettlementChains(ctx, owner)
	if err != nil {
		return nil, err
	}
	actual, err := chains.bitcoinTx(remote.CommitTx.TxID(), true)
	if err != nil {
		return nil, err
	}
	// Reuse the existing exact channel-root, deterministic package and known
	// co-asset checks, but require the actual canonical confirmed witness.
	if _, err := (rgb11STPProtocol{}).VerifyChannelProof(p, old, actual, remote.AssetProofs[rgb11wallet.Protocol], STPOpForceClose); err != nil {
		return nil, err
	}
	proof, err := selected.NativeProof()
	if err != nil {
		return nil, err
	}
	output := indexer.NewTxOutput(selected.Value)
	output.OutPointStr, output.OutValue.PkScript = selected.OutPoint, append([]byte(nil), selected.PkScript...)
	output.Assets = indexer.TxAssets{{Name: selected.AssetName, Amount: *selected.Amount.Clone(), BindingSat: 0}}
	// This ephemeral source is not registered in the public-pool store. Its
	// current proof also checks the complete carrier and known historical
	// co-assets against independent Bitcoin/indexer facts.
	validated, err := p.validateNativeChannelSource(ctx, owner, f.channelID,
		&NativeChannelSource{Version: 1, ChannelID: f.channelID, Output: output, PublicProof: proof}, nativeproof.Current)
	if err != nil {
		return nil, err
	}
	descends, err := p.collectRGB11PrivateSettlementDescends(owner, f.channelID, pkg)
	if err != nil {
		return nil, err
	}
	if err := p.validateSTPRGBClosedPrivateTerminal(chains, validated, selected.OutPoint, selected.PkScript, actual.TxID(), descends); err != nil {
		return nil, err
	}
	funding, err := rgb11wallet.EncodeChannelInventory([]rgb11wallet.ChannelFunding{*selected})
	if err != nil {
		return nil, err
	}
	if err := chains.stable(); err != nil {
		return nil, err
	}
	f.closedSourceFunding = append([]byte(nil), funding...)
	f.closedSourceBackup = append([]byte(nil), raw...)
	return funding, nil
}

// A retained private terminal is the opposite of a public-pool terminal: its
// exact fixed-client allocations must have been released by the confirmed
// force-close consumer. Sibling allocations are not claimed or settled here.
// Existing pre-signed commitments use v2 CLOSE, whereas
// a later force-close drain can use v2 FORCE_CLOSE. The exact stored forced
// server-main identity, not that operation label alone, authorizes this path.
// This separate predicate never relaxes public receipts.
func (p *Manager) validateSTPRGBClosedPrivateTerminal(chains *nativeSettlementChains, validated *nativeproof.Result,
	point string, script []byte, producer string, descends []string) error {
	bootstrap := p.GetBootstrapNodePaymentPubKey()
	if bootstrap == nil || validated == nil || chains == nil {
		return fmt.Errorf("RGB closed source has no independent settlement context")
	}
	if validated.Mode != nativeproof.Current || validated.Outpoint != point || !bytes.Equal(validated.PkScript, script) ||
		!canonicalNativeTxID(producer) || len(script) == 0 {
		return fmt.Errorf("RGB closed source is not its exact current private carrier")
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
	if err := graph.validateClosedPrivateTerminal(point, script, producer); err != nil {
		return err
	}
	return chains.stable()
}

// A graph-local predicate only: successful private source verification never
// exposes this graph or an unclaimed sibling as a receipt or current inventory.
func (graph *nativeReceiptGraph) validateClosedPrivateTerminal(point string, script []byte, producer string) error {
	step := graph.steps[producer]
	if step == nil || len(step.inputs) == 0 || !step.complete {
		return fmt.Errorf("RGB closed source has no complete force-close consumer")
	}
	for _, input := range step.inputs {
		if input.mapping == nil || input.mapping.public || !bytes.Equal(input.mapping.script, script) {
			return fmt.Errorf("RGB closed source producer did not consume its complete private mapping")
		}
	}
	for _, output := range step.outputs {
		if bytes.Equal(output.output.PkScript, script) {
			if output.mapping != nil {
				return fmt.Errorf("RGB closed source client allocation was not released or was remapped")
			}
		} else if !sameNativeReceiptMapping(output.mapping, step.inputs[0].mapping) {
			return fmt.Errorf("RGB closed source changed an unclaimed sibling's mapping")
		}
	}
	terminal := graph.points[point]
	if len(terminal) == 0 || !graph.seenRequiredPrivate {
		return fmt.Errorf("RGB closed source has no exact prior private mapping")
	}
	for _, allocation := range terminal {
		descend := graph.descends[allocation.producer]
		if allocation.producer != producer || allocation.consumer != "" || allocation.mapping != nil ||
			!bytes.Equal(allocation.output.PkScript, script) || descend == nil || !graph.usedDescends[descend.id] ||
			descend.payload.Version != 2 || (descend.payload.Operation != sindexer.DESCEND_OP_CLOSE &&
			descend.payload.Operation != sindexer.DESCEND_OP_FORCE_CLOSE) {
			return fmt.Errorf("RGB private source was not released by its exact confirmed force-close burn")
		}
	}
	for id, used := range graph.usedDescends {
		if !used {
			return fmt.Errorf("RGB closed source contains an unrelated descending locator %s", id)
		}
	}
	return nil
}

// Selection under the business lock is local-only. The source was validated
// and its full query closure captured before the lock; normal funding validation
// below rechecks it through that request's immutable evidence view.
func (protocol rgb11STPProtocol) closedChannelFunding(input *TxOutput, asset *indexer.AssetName) (rgb11wallet.ChannelFunding, bool, error) {
	if protocol.view == nil || protocol.view.facts == nil || len(protocol.view.facts.closedSourceFunding) == 0 {
		return rgb11wallet.ChannelFunding{}, false, nil
	}
	inventory, err := rgb11wallet.DecodeChannelInventory(protocol.view.facts.closedSourceFunding)
	if err != nil || len(inventory.Sources) != 1 || len(inventory.LastPackage) != 0 {
		return rgb11wallet.ChannelFunding{}, false, fmt.Errorf("invalid captured RGB closed source")
	}
	source := inventory.Sources[0]
	if input == nil || asset == nil || input.OutPointStr != source.OutPoint || source.AssetName != *asset ||
		input.Value() != source.Value || !bytes.Equal(input.OutValue.PkScript, source.PkScript) {
		return rgb11wallet.ChannelFunding{}, false, fmt.Errorf("RGB closed source changed its exact selected carrier")
	}
	return source, true, nil
}
