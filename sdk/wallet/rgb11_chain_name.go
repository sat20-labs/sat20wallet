package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// RGB11ChainAsset verifies the registered name, or registers it using the
// independently validated local genesis. The name is never itself a ContractID.
func (p *Manager) RGB11ChainAsset(asset *swire.AssetName, register bool) (*swire.AssetName, error) {
	if asset == nil || asset.Protocol != rgb11wallet.Protocol || asset.Type != indexer.ASSET_TYPE_FT {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	if _, err := dkvs.RGB11NameKey(asset.Ticker); err != nil {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	var expected string
	if info, err := p.localRGB11TickerInfo(asset); err == nil {
		expected = info.ContractID
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, err
	}
	name, err := p.verifyRGB11ChainAsset(asset.Ticker, expected)
	if err == nil || !register || !errors.Is(err, dkvs.ErrRecordNotFound) {
		return name, err
	}
	id, err := consensus.IDFromHex(expected)
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
	if err != nil || registration.ContractID != expected || registration.AssetType != asset.Type {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	ticker, err := dkvs.RGB11NameTicker(registration.BaseTicker, registration.ContractID)
	if err != nil || ticker != asset.Ticker {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	p.mutex.RLock()
	node := p.getAccountServiceNode()
	p.mutex.RUnlock()
	if node == nil {
		return nil, ErrRGB11PublicDataUnavailable
	}
	registrar, ok := node.RPCClient().(interface{ RegisterRGB11Name([]byte) error })
	if !ok {
		return nil, ErrRGB11PublicDataUnavailable
	}
	submitErr := registrar.RegisterRGB11Name(public)
	name, err = p.verifyRGB11ChainAsset(ticker, registration.ContractID)
	if err != nil {
		return nil, errors.Join(submitErr, err)
	}
	return name, nil
}

func (p *Manager) checkRGB11ImportName(ctx context.Context, info *indexer.TickerInfo) error {
	binding, err := p.db.Read(rgb11TickerBindingKey(info.AssetName))
	if err == nil && string(binding) != info.ContractID {
		return dkvs.ErrWriteConflict
	}
	if err != nil && !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, dkvsUnmanagedReadTimeout)
	defer cancel()
	record, id, err := p.readRGB11RegistryName(ctx, info.AssetName.Ticker)
	if errors.Is(err, dkvs.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if id != info.ContractID {
		return dkvs.ErrWriteConflict
	}
	// Cache only the activated name for local wallet display.
	if record.Seq == dkvs.RGB11NameProofSeq {
		return p.saveRGB11Name(record)
	}
	return nil
}

func (p *Manager) verifyRGB11ChainAsset(ticker, expectedID string) (*swire.AssetName, error) {
	record, id, err := p.readRGB11Name(ticker)
	if err != nil {
		return nil, err
	}
	if expectedID != "" && id != expectedID {
		return nil, dkvs.ErrWriteConflict
	}
	if err := p.saveRGB11Name(record); err != nil {
		return nil, err
	}
	return &swire.AssetName{Protocol: rgb11wallet.Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: ticker}, nil
}

// The signed record is the single reverse lookup for wallet operations. Chain
// admission and Anchor construction still read the authoritative registry.
func rgb11NameDBKey(key string) []byte {
	return []byte(dbNetworkKeyPrefix(_env, _chain) + "rgb11-name" + key)
}

func (p *Manager) saveRGB11Name(record *swire.DKVSRecord) error {
	key := rgb11NameDBKey(record.Key)
	raw, err := swire.SerializeDKVSRecord(record)
	if err != nil {
		return err
	}
	old, err := p.db.Read(key)
	if err == nil && bytes.Equal(old, raw) {
		return nil
	}
	if err != nil && !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	return p.db.Write(key, raw)
}

func (p *Manager) loadRGB11Name(ticker string) (string, error) {
	key, err := dkvs.RGB11NameKey(ticker)
	if err != nil {
		return "", err
	}
	if p.db == nil {
		return "", indexer.ErrKeyNotFound
	}
	raw, err := p.db.Read(rgb11NameDBKey(key))
	if err != nil {
		return "", err
	}
	record, err := swire.DeserializeDKVSRecord(raw)
	if err != nil {
		return "", err
	}
	return dkvs.VerifyRGB11Name(record, ticker)
}

// Wallet-native use does not need a registered SatoshiNet name, but requires
// a structurally canonical name. The scoped RGB engine resolves local identity.
func (p *Manager) rgb11WalletAsset(asset *swire.AssetName) (*swire.AssetName, error) {
	if asset == nil || !rgb11wallet.IsCanonicalAssetName(*asset) {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	return asset, nil
}

// RGB11ContractID resolves the authoritative SatoshiNet name binding.
// Local-only operations use the validated RGB contract from their wallet scope.
func (p *Manager) RGB11ContractID(asset *swire.AssetName) (string, error) {
	if asset == nil || asset.Type != indexer.ASSET_TYPE_FT {
		return "", ErrRGB11PublicIdentityMismatch
	}
	if _, err := p.rgb11WalletAsset(asset); err != nil {
		return "", err
	}
	_, id, err := p.readRGB11Name(asset.Ticker)
	return id, err
}

func (p *Manager) RGB11LocalAsset(asset *swire.AssetName) (*swire.AssetName, error) {
	id, err := p.RGB11ContractID(asset)
	if err != nil {
		return nil, err
	}
	if info, err := p.localRGB11TickerInfo(asset); err == nil {
		if info.ContractID != id {
			return nil, dkvs.ErrWriteConflict
		}
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, err
	}
	copy := *asset
	return &copy, nil
}

func (v *stpRGBEvidenceView) localFundingAsset(p *Manager, asset *swire.AssetName) (*swire.AssetName, error) {
	if v == nil {
		return p.RGB11LocalAsset(asset)
	}
	if asset != nil && v.facts != nil && v.facts.fundingName != nil && v.facts.fundingName.chain == *asset {
		copy := v.facts.fundingName.chain
		return &copy, nil
	}
	return nil, v.unavailable("registered funding asset", "")
}

func (v *stpRGBEvidenceView) namedFundingProof(p *Manager, name indexer.AssetName, contractID string, proof []byte) ([]byte, error) {
	id, err := rgb11wallet.RGB11ContractIDHex(contractID)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return p.rgb11NamedProof(name, id, proof)
	}
	if v.facts == nil || v.facts.fundingName == nil || v.facts.fundingName.chain != name {
		return nil, v.unavailable("registered funding proof", name.String())
	}
	fact := v.facts.fundingName
	if fact.contractID != id {
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
	info := p.GetTickerInfo(local)
	if info == nil {
		return nil
	}
	copy := *info
	copy.AssetName = *name
	return &copy
}
