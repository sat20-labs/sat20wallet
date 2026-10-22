package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/anchors"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
)

// RGB quantities are client validated. A Bitcoin/indexer output without an
// RGB entry is not a proof that the carrier has no RGB state.
type rgb11STPProtocol struct{ stpUTXORecovery }

func (rgb11STPProtocol) Protocol() string             { return rgb11wallet.Protocol }
func (rgb11STPProtocol) BindingSat(*AssetName) uint32 { return 0 }

func (rgb11STPProtocol) AssetNameN(precision, _ int) int { return precision }

func (rgb11STPProtocol) ValidateAsset(name *indexer.AssetName, operation STPAssetOperation) error {
	if err := validateSTPFungibleAsset(name, rgb11wallet.Protocol, operation); err != nil {
		return err
	}
	key, err := hex.DecodeString(name.Ticker)
	if err != nil || len(key) != 32 || name.Ticker != strings.ToLower(name.Ticker) {
		return fmt.Errorf("RGB11 STP requires the canonical full contract ID")
	}
	return nil
}

func (protocol rgb11STPProtocol) NormalizeContract(name *indexer.AssetName) error {
	return protocol.ValidateAsset(name, STPOpContract)
}

func (rgb11STPProtocol) Policy(*AssetName) STPAssetPolicy {
	return STPAssetPolicy{PartialTransfer: true, FixedOutputIndexes: true,
		SplicePayloadSize: 34, AlwaysSplicePayload: true, NoOrdinalInputStub: true, ExchangeRecoveryPackages: true}
}

func (rgb11STPProtocol) EstimateSplicePayload(*AssetName, int) ([]byte, error) {
	return anchors.OpretScript([32]byte{}), nil
}

func (rgb11STPProtocol) BuildTransfer(_ *Manager, _ STPAssetTransferRequest) (*STPAssetPlan, error) {
	// Public send already has a managed prepare/receiver-ACK/authorization
	// route. A generic unsigned builder cannot replace that durability gate.
	return nil, fmt.Errorf("RGB11 transfers require the managed RGB11 transfer planner")
}

func (p *Manager) stpRGB11Scope(owner common.Wallet) (*rgb11Manager, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.evidence == nil {
		return nil, ErrRGB11Inconsistent
	}
	if owner == nil {
		return p.captureRGB11NetworkScope(false)
	}
	for _, account := range p.localRGB11Accounts() {
		if account.WalletID == owner.GetId() && account.AccountIndex == owner.GetSubAccount() &&
			account.Wallet.GetPaymentPubKey().IsEqual(owner.GetPaymentPubKey()) {
			return p.newScopedRGB11Manager(account)
		}
	}
	return nil, fmt.Errorf("RGB11 channel wallet is not a known local account")
}

func stpRGB11Evidence(p *Manager) (rgb11wallet.BitcoinEvidenceProvider, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.evidence == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgbManager.evidence, nil
}

func stpRGB11Inventory(channel *Channel) (*rgb11wallet.ChannelInventory, error) {
	if channel == nil {
		return nil, fmt.Errorf("missing RGB11 channel")
	}
	raw := channel.AssetFunding[rgb11wallet.Protocol]
	if len(raw) == 0 {
		return &rgb11wallet.ChannelInventory{Version: 1}, nil
	}
	return rgb11wallet.DecodeChannelInventory(raw)
}

func (protocol rgb11STPProtocol) PrepareInputs(p *Manager, req STPAssetInputRequest) (*STPAssetInputs, error) {
	if err := protocol.ValidateAsset(req.Asset, req.Operation); err != nil {
		return nil, err
	}
	if req.Operation == STPOpSpliceOut {
		return &STPAssetInputs{}, nil
	}
	if req.Amount == nil || req.Amount.Validate() != nil || req.Amount.Sign() <= 0 {
		return nil, fmt.Errorf("invalid RGB11 funding amount")
	}
	manager, err := p.stpRGB11Scope(nil)
	if err != nil {
		return nil, err
	}
	if req.AssetAddress != "" && req.AssetAddress != manager.wallet.GetAddress() {
		return nil, fmt.Errorf("RGB11 private funding must use its captured wallet")
	}
	proofs, err := manager.projectionStore.ListProofs()
	if err != nil {
		return nil, err
	}
	sort.Slice(proofs, func(i, j int) bool { return proofs[i].OutPoint < proofs[j].OutPoint })
	seen := make(map[string]bool)
	total := indexer.NewDecimal(0, req.Amount.Precision)
	var inputs []*TxOutput
	for _, proof := range proofs {
		if proof == nil || proof.AssetName != *req.Asset || seen[proof.OutPoint] || req.ExcludedUtxos[proof.OutPoint] ||
			(proof.Status != "valid" && proof.Status != "settled") {
			continue
		}
		seen[proof.OutPoint] = true
		if !rgb11CarrierLockAllowsSpend(manager.utxoLockerL1.GetLockedUtxoList()[proof.OutPoint]) {
			continue
		}
		output, err := manager.projectionStore.LoadOutput(proof.OutPoint)
		if err != nil {
			return nil, err
		}
		walletScript, err := AddrToPkScript(manager.wallet.GetAddress(), GetChainParam())
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(output.OutValue.PkScript, walletScript) {
			continue
		}
		source, err := manager.stpRGB11Funding(output, req.Asset)
		if err != nil {
			return nil, err
		}
		validated, err := rgb11wallet.ValidateChannelFunding(context.Background(), source, manager.evidence, nativeproof.Current, GetChainParam().Name)
		if err != nil {
			return nil, err
		}
		if validated.Value != source.Value || validated.Asset.Amount.Precision != req.Amount.Precision {
			return nil, fmt.Errorf("RGB11 funding carrier or precision changed")
		}
		input := output.Clone()
		input.Assets = indexer.TxAssets{validated.Asset}
		input.SatBindingMap = nil
		inputs = append(inputs, input)
		total = total.Add(&validated.Asset.Amount)
		if total.Cmp(req.Amount) >= 0 {
			break
		}
	}
	if total.Cmp(req.Amount) < 0 {
		return nil, ErrRGB11InsufficientBalance
	}
	return &STPAssetInputs{Inputs: inputs}, nil
}

// Export only actual validated projection objects. The peer independently
// verifies these material claims against its own Bitcoin facts before signing.
func (p *rgb11Manager) stpRGB11Funding(output *TxOutput, asset *indexer.AssetName) (rgb11wallet.ChannelFunding, error) {
	if output == nil || asset == nil || HasMultiAsset(output, asset) {
		return rgb11wallet.ChannelFunding{}, ErrRGB11AssetPreservation
	}
	if err := p.projectionStore.AssertConsistent(output.OutPointStr, *asset); err != nil {
		return rgb11wallet.ChannelFunding{}, err
	}
	proofs, err := p.projectionStore.ListProofs()
	if err != nil {
		return rgb11wallet.ChannelFunding{}, err
	}
	for _, proof := range proofs {
		if proof.OutPoint == output.OutPointStr && proof.AssetName != *asset {
			return rgb11wallet.ChannelFunding{}, ErrRGB11AssetPreservation
		}
	}
	proof, err := p.projectionStore.LoadProof(output.OutPointStr, *asset)
	if err != nil || proof == nil {
		return rgb11wallet.ChannelFunding{}, fmt.Errorf("RGB11 funding proof is unavailable: %w", errRGB11ProjectionEvidence(err))
	}
	raw, err := p.projectionStore.LoadObject(proof.ConsignmentHash)
	if err != nil {
		return rgb11wallet.ChannelFunding{}, err
	}
	receipt, err := p.projectionStore.LoadValidationReceipt(proof.ConsignmentHash)
	if err != nil {
		return rgb11wallet.ChannelFunding{}, err
	}
	var reveals [][]byte
	for _, allocation := range receipt.Allocations {
		if len(allocation.SealDisclosure) != 0 {
			reveals = append(reveals, allocation.SealDisclosure)
		}
	}
	// BuildRGB11 normalizes and deduplicates disclosures; its canonical
	// representation is shared by both peers and by the SatoshiNet verifier.
	encoded, err := nativeproof.BuildRGB11(raw, reveals)
	if err != nil {
		return rgb11wallet.ChannelFunding{}, err
	}
	canonical, err := nativeproof.Decode(encoded)
	if err != nil {
		return rgb11wallet.ChannelFunding{}, err
	}
	amount := output.GetAsset(asset)
	if amount == nil || amount.Sign() <= 0 {
		return rgb11wallet.ChannelFunding{}, rgb11wallet.ErrInvalidProof
	}
	return rgb11wallet.ChannelFunding{OutPoint: output.OutPointStr, Value: output.Value(),
		PkScript: append([]byte(nil), output.OutValue.PkScript...), AssetName: *asset, Amount: *amount.Clone(),
		Consignment: canonical.Consignment, Reveals: canonical.Reveals}, nil
}

func (protocol rgb11STPProtocol) PrepareChannelFunding(p *Manager, channel *Channel, asset *AssetName, inputs []*TxOutput) ([]byte, error) {
	if asset == nil {
		return nil, fmt.Errorf("missing RGB11 funding asset")
	}
	if err := protocol.ValidateAsset(&asset.AssetName, STPOpSpliceIn); err != nil {
		return nil, err
	}
	inventory, err := stpRGB11Inventory(channel)
	if err != nil {
		return nil, err
	}
	for _, source := range inventory.Sources {
		if source.AssetName != asset.AssetName {
			return nil, fmt.Errorf("this channel already carries another RGB11 contract")
		}
	}
	manager, err := p.stpRGB11Scope(channel.LocalWallet())
	if err != nil {
		return nil, err
	}
	byPoint := make(map[string]rgb11wallet.ChannelFunding)
	for _, source := range inventory.Sources {
		byPoint[source.OutPoint] = source
	}
	for _, input := range inputs {
		if input == nil {
			return nil, fmt.Errorf("nil RGB11 funding input")
		}
		if _, exists := byPoint[input.OutPointStr]; exists {
			continue
		}
		source, err := manager.stpRGB11Funding(input, &asset.AssetName)
		if err != nil {
			return nil, err
		}
		byPoint[source.OutPoint] = source
	}
	var sources []rgb11wallet.ChannelFunding
	for _, source := range byPoint {
		sources = append(sources, source)
	}
	return rgb11wallet.EncodeChannelInventoryWithLast(sources, inventory.LastPackage)
}

func (protocol rgb11STPProtocol) ValidateChannelFunding(p *Manager, channel *Channel, asset *AssetName, inputs []*TxOutput, raw []byte) ([]byte, error) {
	if asset == nil {
		return nil, fmt.Errorf("missing RGB11 funding asset")
	}
	if err := protocol.ValidateAsset(&asset.AssetName, STPOpSpliceIn); err != nil {
		return nil, err
	}
	old, err := stpRGB11Inventory(channel)
	if err != nil {
		return nil, err
	}
	incoming, err := rgb11wallet.DecodeChannelInventory(raw)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(old.LastPackage, incoming.LastPackage) {
		return nil, fmt.Errorf("RGB11 funding changed the previous lifecycle obligation")
	}
	evidence, err := stpRGB11Evidence(p)
	if err != nil {
		return nil, err
	}
	oldSources := make(map[string]rgb11wallet.ChannelFunding)
	for _, source := range old.Sources {
		if source.AssetName != asset.AssetName {
			return nil, fmt.Errorf("this channel already carries another RGB11 contract")
		}
		oldSources[source.OutPoint] = source
	}
	requested := make(map[string]*TxOutput)
	for _, input := range inputs {
		if input == nil || requested[input.OutPointStr] != nil {
			return nil, fmt.Errorf("duplicate or nil RGB11 funding input")
		}
		if HasMultiAsset(input, &asset.AssetName) {
			return nil, ErrRGB11AssetPreservation
		}
		requested[input.OutPointStr] = input
	}
	knownSources := make(map[string]indexer.AssetName, len(incoming.Sources))
	for _, source := range incoming.Sources {
		knownSources[source.OutPoint] = source.AssetName
	}
	if err := verifySTPRGB11KnownCoassets(p, knownSources); err != nil {
		return nil, err
	}
	for _, source := range incoming.Sources {
		if source.AssetName != asset.AssetName {
			return nil, fmt.Errorf("RGB11 funding changed the requested contract")
		}
		input := requested[source.OutPoint]
		previous, known := oldSources[source.OutPoint]
		if known && !sameSTPRGB11Funding(previous, source) {
			return nil, fmt.Errorf("RGB11 funding replaced existing native material")
		}
		if input == nil && !known {
			return nil, fmt.Errorf("unsolicited RGB11 funding source")
		}
		if input != nil {
			if source.Value != input.Value() || !bytes.Equal(source.PkScript, input.OutValue.PkScript) {
				return nil, fmt.Errorf("RGB11 funding changed its Bitcoin carrier")
			}
			validated, err := rgb11wallet.ValidateChannelFunding(context.Background(), source, evidence, nativeproof.Current, GetChainParam().Name)
			if err != nil {
				return nil, err
			}
			if validated.Value != source.Value || validated.Asset.Amount.Precision != asset.N {
				return nil, fmt.Errorf("RGB11 funding value or precision mismatch")
			}
			input.Assets = indexer.TxAssets{validated.Asset}
			input.SatBindingMap = nil
			delete(requested, source.OutPoint)
		}
		delete(oldSources, source.OutPoint)
	}
	if len(requested) != 0 || len(oldSources) != 0 {
		return nil, fmt.Errorf("RGB11 funding omitted an input or existing source")
	}
	return rgb11wallet.EncodeChannelInventoryWithLast(incoming.Sources, incoming.LastPackage)
}

// A valid proof for one contract cannot authorize burning another known
// contract carried by the same Bitcoin output. Check every local account;
// peers' public indexers cannot establish the absence of native allocations.
func verifySTPRGB11KnownCoassets(p *Manager, sources map[string]indexer.AssetName) error {
	for _, account := range p.localRGB11Accounts() {
		manager, err := p.newScopedRGB11Manager(account)
		if err != nil {
			return err
		}
		proofs, err := manager.projectionStore.ListProofs()
		if err != nil {
			return err
		}
		for _, proof := range proofs {
			if proof == nil {
				continue
			}
			if expected, exists := sources[proof.OutPoint]; exists && expected != proof.AssetName {
				return ErrRGB11AssetPreservation
			}
		}
	}
	return nil
}

func (rgb11STPProtocol) TickerFromFunding(p *Manager, name *indexer.AssetName, raw []byte) (*indexer.TickerInfo, error) {
	inventory, err := rgb11wallet.DecodeChannelInventory(raw)
	if err != nil {
		return nil, err
	}
	evidence, err := stpRGB11Evidence(p)
	if err != nil {
		return nil, err
	}
	for _, source := range inventory.Sources {
		if name == nil || source.AssetName != *name {
			continue
		}
		validated, err := rgb11wallet.ValidateChannelFunding(context.Background(), source, evidence, nativeproof.Prepared, GetChainParam().Name)
		if err != nil {
			return nil, err
		}
		container, err := coreconsignment.Decode(source.Consignment)
		if err != nil {
			return nil, err
		}
		var reveals []seals.GraphBlindSeal
		for _, raw := range source.Reveals {
			seal, err := seals.DecodeGraphBlindSeal(raw)
			if err != nil {
				return nil, err
			}
			reveals = append(reveals, seal)
		}
		history, err := rgb11wallet.NewNativeConsensusValidatorWithReveals(reveals...).ValidateHistoricalConsignmentReport(context.Background(), source.Consignment, evidence)
		if err != nil {
			return nil, err
		}
		info, err := rgb11TickerInfoFromValidatedContract(container, history.MaterialReceipt)
		if err != nil {
			return nil, err
		}
		if info.AssetName != validated.Asset.Name || info.Divisibility != validated.Asset.Amount.Precision {
			return nil, rgb11wallet.ErrValidationReceipt
		}
		return info, nil
	}
	return nil, fmt.Errorf("RGB11 funding has no authenticated metadata for the requested asset")
}

func sameSTPRGB11Funding(a, b rgb11wallet.ChannelFunding) bool {
	left, le := rgb11wallet.EncodeChannelInventory([]rgb11wallet.ChannelFunding{a})
	right, re := rgb11wallet.EncodeChannelInventory([]rgb11wallet.ChannelFunding{b})
	return le == nil && re == nil && bytes.Equal(left, right)
}

func (rgb11STPProtocol) VerifyInput(asset *AssetName, input *TxOutput) error {
	if err := verifySTPInput(asset, input); err != nil {
		return err
	}
	if asset == nil || input.GetAsset(&asset.AssetName).Sign() <= 0 {
		return fmt.Errorf("RGB11 input has no proven positive allocation")
	}
	for _, allocation := range input.Assets {
		if allocation.Name != asset.AssetName || allocation.BindingSat != 0 || allocation.Amount.Precision != asset.N {
			return fmt.Errorf("invalid RGB11 input asset view")
		}
	}
	return nil
}

func (rgb11STPProtocol) SplitOutputs(inputs []*TxOutput, asset *AssetName, value int64, amount *Decimal) (*TxOutput, *TxOutput, error) {
	if asset == nil || amount == nil || amount.Validate() != nil || amount.Sign() <= 0 || amount.Precision != asset.N {
		return nil, nil, fmt.Errorf("invalid RGB11 split amount")
	}
	total := indexer.NewDecimal(0, amount.Precision)
	var sats int64
	for _, input := range inputs {
		if input == nil || HasMultiAsset(input, &asset.AssetName) {
			return nil, nil, ErrRGB11AssetPreservation
		}
		sats += input.Value()
		total = total.Add(input.GetAsset(&asset.AssetName))
	}
	if total.Cmp(amount) < 0 || value < 330 || sats < value {
		return nil, nil, fmt.Errorf("insufficient RGB11 split inputs")
	}
	output := indexer.NewTxOutput(value)
	output.Assets = indexer.TxAssets{{Name: asset.AssetName, Amount: *amount.Clone(), BindingSat: 0}}
	changeAmount := total.Sub(amount)
	var change *TxOutput
	if sats > value || changeAmount.Sign() > 0 {
		change = indexer.NewTxOutput(sats - value)
		if changeAmount.Sign() > 0 {
			change.Assets = indexer.TxAssets{{Name: asset.AssetName, Amount: *changeAmount, BindingSat: 0}}
		}
	}
	return output, change, nil
}
