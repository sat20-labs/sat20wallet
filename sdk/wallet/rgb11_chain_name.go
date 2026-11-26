package wallet

import (
	"encoding/base64"
	"errors"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func rgb11DisplayName(asset swire.AssetName, genesisTicker string) string {
	ticker, err := dkvs.RGB11NameTicker(genesisTicker, asset.Ticker)
	if err != nil {
		return ""
	}
	asset.Ticker = ticker
	return asset.String()
}

// RGB11ChainAsset resolves a local RGB contract key to its registered chain
// asset. Deployment can register the short name from the public contract.
func (p *Manager) RGB11ChainAsset(asset *swire.AssetName, register bool) (*swire.AssetName, error) {
	if asset == nil || asset.Protocol != rgb11wallet.Protocol || asset.Type != "f" {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	if _, err := dkvs.RGB11NameKey(asset.Ticker); err == nil {
		_, _, err := p.readRGB11Name(asset.Ticker)
		return asset, err
	}
	id, err := consensus.IDFromHex(asset.Ticker)
	if err != nil {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	contract, err := p.ExportPublicRGB11Contract(consensus.ContractID(id).String())
	if err != nil {
		return nil, err
	}
	public, err := base64.StdEncoding.Strict().DecodeString(contract.ContractConsignmentBase64)
	if err != nil {
		return nil, err
	}
	_, registration, err := rgb11wallet.NewRGB11ContractValue(public)
	if err != nil || registration.ContractID != asset.Ticker || registration.AssetType != asset.Type {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	ticker, err := dkvs.RGB11NameTicker(registration.BaseTicker, registration.ContractID)
	if err != nil {
		return nil, err
	}
	contractID := registration.ContractID
	name := &swire.AssetName{Protocol: asset.Protocol, Type: asset.Type, Ticker: ticker}
	_, registeredID, err := p.readRGB11Name(ticker)
	if err == nil {
		if registeredID != contractID {
			return nil, dkvs.ErrWriteConflict
		}
		return name, nil
	}
	if !register || !errors.Is(err, dkvs.ErrRecordNotFound) {
		return nil, err
	}
	p.mutex.RLock()
	node := p.getAccountServiceNode()
	p.mutex.RUnlock()
	if node == nil {
		return nil, ErrRGB11PublicDataUnavailable
	}
	registrar, ok := node.RPCClient().(interface {
		RegisterRGB11Name([]byte) error
	})
	if !ok {
		return nil, ErrRGB11PublicDataUnavailable
	}
	submitErr := registrar.RegisterRGB11Name(public)
	_, registeredID, err = p.readRGB11Name(ticker)
	if err == nil && registeredID != contractID {
		return nil, dkvs.ErrWriteConflict
	}
	if err != nil {
		return nil, errors.Join(submitErr, err)
	}
	return name, nil
}

// RGB11LocalAsset maps a registered chain name to the Bitcoin RGB inventory key.
func (p *Manager) RGB11LocalAsset(asset *swire.AssetName) (*swire.AssetName, error) {
	if asset == nil || asset.Protocol != rgb11wallet.Protocol {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	if _, err := rgb11ContractIDFromAssetName(*asset); err == nil {
		return asset, nil
	}
	if asset.Type != "f" {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	if _, err := dkvs.RGB11NameKey(asset.Ticker); err != nil {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	_, id, err := p.readRGB11Name(asset.Ticker)
	if err != nil {
		return nil, err
	}
	return &swire.AssetName{Protocol: asset.Protocol, Type: asset.Type, Ticker: id}, nil
}

func (v *stpRGBEvidenceView) localFundingAsset(p *Manager, asset *swire.AssetName) (*swire.AssetName, error) {
	if v == nil {
		return p.RGB11LocalAsset(asset)
	}
	if asset != nil {
		if _, err := rgb11ContractIDFromAssetName(*asset); err == nil {
			return asset, nil
		}
		if v.facts != nil && v.facts.fundingName != nil && v.facts.fundingName.chain == *asset {
			local := v.facts.fundingName.local
			return &local, nil
		}
	}
	return nil, v.unavailable("registered funding asset", "")
}

func (v *stpRGBEvidenceView) namedFundingProof(p *Manager, name indexer.AssetName, id string, proof []byte) ([]byte, error) {
	if v == nil {
		return p.rgb11NamedProof(name, id, proof)
	}
	if v.facts == nil || v.facts.fundingName == nil || v.facts.fundingName.chain != name {
		return nil, v.unavailable("registered funding proof", name.String())
	}
	fact := v.facts.fundingName
	if fact.local.Ticker != id {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	return nativeproof.WithRGB11Name(proof, fact.record)
}

func (p *Manager) rgb11NamedProof(name indexer.AssetName, id string, proof []byte) ([]byte, error) {
	record, registered, err := p.readRGB11Name(name.Ticker)
	if err != nil {
		return nil, err
	}
	if registered != id {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	return nativeproof.WithRGB11Name(proof, record)
}

// A Bitcoin-funded entry needs genesis precision before it has a SatoshiNet UTXO.
func (p *Manager) fundingTicker(name *swire.AssetName) *indexer.TickerInfo {
	if name.Protocol != rgb11wallet.Protocol {
		return p.getTickerInfo(name)
	}
	local, err := p.RGB11LocalAsset(name)
	if err != nil {
		return nil
	}
	info := p.getTickerInfo(local)
	if info == nil {
		return nil
	}
	copy := *info
	copy.AssetName = *name
	return &copy
}
