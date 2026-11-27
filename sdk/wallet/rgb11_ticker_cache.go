package wallet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/consignment"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// The cache's AssetName uses the short ticker. The value still records the
// full contract identity used by the Bitcoin RGB wallet and its proofs.
func tickerInfoStorageKey(info *indexer.TickerInfo) (string, error) {
	name := info.AssetName
	if name.Protocol == rgb11wallet.Protocol {
		var ext rgb11wallet.TickerExt
		if err := json.Unmarshal(info.Content, &ext); err != nil {
			return "", err
		}
		id, err := rgb11TickerContractID(ext.ContractID)
		if err != nil {
			return "", ErrRGB11PublicIdentityMismatch
		}
		idHex := hex.EncodeToString(id[:])
		short, err := dkvs.RGB11NameTicker(ext.Ticker, idHex)
		if err != nil || name.Ticker != short || (info.ContractID != "" && info.ContractID != idHex) ||
			(ext.AssetName != (indexer.AssetName{}) && ext.AssetName != name) {
			return "", ErrRGB11PublicIdentityMismatch
		}
		name.Ticker = short
	}
	return GetTickerInfoKey(name.String()), nil
}

func rgb11TickerContractID(text string) (consensus.ContractID, error) {
	if id, err := consensus.IDFromHex(text); err == nil {
		return consensus.ContractID(id), nil
	}
	return consensus.ParseContractID(text)
}

// Keep the identity binding outside the disposable t- cache. Rewinding
// ticker metadata must not allow another wallet account to claim the name.
var rgb11TickerWriteMu sync.Mutex

func rgb11TickerBindingKey(name indexer.AssetName) []byte {
	name.Type = indexer.ASSET_TYPE_FT // A control right belongs to the same contract.
	return []byte(GetDBKeyPrefix() + "rgb11-short-binding-" + name.String())
}

func stageRGB11TickerInfo(db indexer.KVDB, batch indexer.WriteBatch, info *indexer.TickerInfo) error {
	key, err := tickerInfoStorageKey(info)
	if err != nil {
		return err
	}
	var ext rgb11wallet.TickerExt
	if err := json.Unmarshal(info.Content, &ext); err != nil {
		return err
	}
	id, err := rgb11wallet.RGB11ContractIDHex(ext.ContractID)
	if err != nil {
		return err
	}
	binding := rgb11TickerBindingKey(info.AssetName)
	current, err := db.Read(binding)
	switch {
	case errors.Is(err, indexer.ErrKeyNotFound):
		if err := batch.Put(binding, []byte(id)); err != nil {
			return err
		}
	case err != nil:
		return err
	case string(current) != id:
		return dkvs.ErrWriteConflict
	}
	data, err := EncodeToBytes(info)
	if err != nil {
		return err
	}
	return batch.Put([]byte(key), data)
}

func loadRGB11TickerInfo(db indexer.KVDB, name *indexer.AssetName) (*indexer.TickerInfo, error) {
	if _, err := dkvs.RGB11NameKey(name.Ticker); err != nil {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	key := GetTickerInfoKey(name.String())
	raw, err := db.Read([]byte(key))
	if err != nil {
		return nil, err
	}
	var info indexer.TickerInfo
	if err := DecodeFromBytes(raw, &info); err != nil {
		return nil, err
	}
	if actual, err := tickerInfoStorageKey(&info); err != nil || actual != key || info.AssetName != *name {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	if info.ContractID == "" {
		var ext rgb11wallet.TickerExt
		if err := json.Unmarshal(info.Content, &ext); err != nil {
			return nil, err
		}
		info.ContractID, err = rgb11wallet.RGB11ContractIDHex(ext.ContractID)
		if err != nil {
			return nil, err
		}
	}
	return &info, nil
}

func rgb11TickerInfoFromPublicContract(expected *indexer.AssetName, contractID string, raw []byte) (*indexer.TickerInfo, error) {
	_, registration, err := rgb11wallet.NewRGB11ContractValue(raw)
	if err != nil || registration.AssetType != expected.Type || registration.ContractID != contractID {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	container, err := consignment.DecodeFile(raw)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	info, err := rgb11TickerInfoFromValidatedContract(container, &rgb11wallet.ValidationReceipt{
		ContractID: container.ContractID, SchemaID: container.SchemaID,
		ConsignmentHash: hex.EncodeToString(digest[:]),
	})
	if err != nil {
		return nil, err
	}
	key, err := tickerInfoStorageKey(info)
	if err != nil || *expected != info.AssetName || key != GetTickerInfoKey(expected.String()) {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	return info, nil
}

func (p *Manager) saveRebuiltRGB11Ticker(info *indexer.TickerInfo) error {
	if err := saveTickerInfo(p.db, info); err != nil {
		return err
	}
	p.cacheTickerInfo(info)
	return nil
}

// localRGB11TickerInfo is safe for callers that cannot do network I/O.
func (p *Manager) localRGB11TickerInfo(name *indexer.AssetName) (*indexer.TickerInfo, error) {
	if info, err := loadTickerInfo(p.db, name); err == nil {
		return info, nil
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, err
	}
	return p.rgb11TickerInfoFromLocalContract(name)
}

func (p *Manager) rgb11TickerInfoFromLocalContract(name *indexer.AssetName) (*indexer.TickerInfo, error) {
	release := p.beginRGB11ReadObservation()
	defer release()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil || manager.projectionStore == nil {
		return nil, indexer.ErrKeyNotFound
	}
	return rgb11TickerInfoFromProjection(manager.projectionStore, name)
}

// This helper does not take wallet lifecycle locks. The caller owns a stable
// scope or already holds the read gate, and no network call is performed.
func rgb11TickerInfoFromProjection(store *rgb11wallet.ProjectionStore, name *indexer.AssetName) (*indexer.TickerInfo, error) {
	contractID, raw, err := store.FindValidatedContractByAssetName(*name)
	if err != nil {
		return nil, err
	}
	genesis, _, err := rgb11ContractFromValidatedHistory(raw, contractID)
	if err != nil {
		return nil, err
	}
	public, err := rgb11wallet.PublicRGB11Contract(genesis)
	if err != nil {
		return nil, err
	}
	id, err := rgb11wallet.RGB11ContractIDHex(contractID)
	if err != nil {
		return nil, err
	}
	return rgb11TickerInfoFromPublicContract(name, id, public)
}

// fetchRGB11TickerInfo is the RGB source on a cache miss. The common ticker
// lookup owns the cache read and write.
func (p *Manager) fetchRGB11TickerInfo(name *indexer.AssetName) (*indexer.TickerInfo, error) {
	info, err := p.rgb11TickerInfoFromLocalContract(name)
	if !errors.Is(err, indexer.ErrKeyNotFound) {
		return info, err
	}
	id, err := p.loadRGB11Name(name.Ticker)
	if errors.Is(err, indexer.ErrKeyNotFound) {
		_, id, err = p.readRGB11RegistryName(nil, name.Ticker)
	}
	if err != nil {
		return nil, err
	}
	_, value, err := p.readAccountRGBContract(id)
	if err != nil {
		return nil, err
	}
	return rgb11TickerInfoFromPublicContract(name, id, value.ContractContent)
}
