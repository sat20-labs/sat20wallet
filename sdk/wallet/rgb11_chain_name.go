package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

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
		return p.verifyRGB11ChainAsset(asset.Ticker, "")
	}
	if _, err := rgb11ContractIDFromAssetName(*asset); err != nil {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	// Issuance and import already persisted the validated genesis ticker.
	// A chain operation only needs to verify its official name binding.
	if !register {
		ticker, err := p.rgb11ChainTicker(asset)
		if err == nil {
			return p.verifyRGB11ChainAsset(ticker, asset.Ticker)
		}
		if !errors.Is(err, indexer.ErrKeyNotFound) {
			return nil, err
		}
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
	name, err := p.verifyRGB11ChainAsset(ticker, registration.ContractID)
	if err == nil {
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
	name, err = p.verifyRGB11ChainAsset(ticker, registration.ContractID)
	if err != nil {
		return nil, errors.Join(submitErr, err)
	}
	return name, nil
}

func (p *Manager) rgb11ChainTicker(asset *swire.AssetName) (string, error) {
	info := p.getTickerInfo(asset)
	if info == nil {
		return "", indexer.ErrKeyNotFound
	}
	var ext rgb11wallet.TickerExt
	if info.AssetName != *asset || json.Unmarshal(info.Content, &ext) != nil ||
		!rgb11wallet.ContractAssetKeyMatches(*asset, ext.ContractID) {
		return "", ErrRGB11PublicIdentityMismatch
	}
	return dkvs.RGB11NameTicker(ext.Ticker, asset.Ticker)
}

// An imported genesis is usable as native RGB even when its public name is
// unavailable or belongs to a different contract. Try once after local commit.
func (p *Manager) learnRGB11Name(ctx context.Context, asset *swire.AssetName) {
	if asset.Type != indexer.ASSET_TYPE_FT {
		return
	}
	ticker, err := p.rgb11ChainTicker(asset)
	if err != nil {
		return
	}
	if _, err := p.loadRGB11Name(ticker); !errors.Is(err, indexer.ErrKeyNotFound) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, dkvsUnmanagedReadTimeout)
	defer cancel()
	network := dbNetworkKeyPrefix(_env, _chain)
	record, id, err := p.readRGB11NameCtx(ctx, ticker)
	if err == nil && id == asset.Ticker && network == dbNetworkKeyPrefix(_env, _chain) {
		if err := p.saveRGB11Name(record); err != nil {
			Log.Warnf("cache imported RGB11 name %s: %v", ticker, err)
		}
	}
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

// rgb11WalletAsset accepts a registered name at wallet API boundaries and
// returns the immutable projection key consumed by the native RGB engine.
func (p *Manager) rgb11WalletAsset(asset *swire.AssetName) (*swire.AssetName, error) {
	if asset == nil || asset.Protocol != rgb11wallet.Protocol {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	if _, err := rgb11ContractIDFromAssetName(*asset); err == nil {
		return asset, nil
	}
	if asset.Type != indexer.ASSET_TYPE_FT {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	id, err := p.loadRGB11Name(asset.Ticker)
	if errors.Is(err, indexer.ErrKeyNotFound) {
		var proof *swire.DKVSRecord
		proof, id, err = p.readRGB11Name(asset.Ticker)
		if err == nil {
			err = p.saveRGB11Name(proof)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("resolve RGB11 asset %s: %w", asset, err)
	}
	return &swire.AssetName{Protocol: asset.Protocol, Type: asset.Type, Ticker: id}, nil
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
