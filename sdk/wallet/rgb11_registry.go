package wallet

import (
	"errors"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func (p *SatsNetDKVSClient) RegisterRGB11Contract(owner common.Wallet, providerDID, ticker, contractID string) (*dkvsindexer.RGB11Registration, error) {
	if p == nil || owner == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	key, err := dkvsindexer.RGB11RegistryKey(providerDID, ticker)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < 3; attempt++ {
		var ids []string
		existing, err := p.GetRecordDirect(key)
		if err == nil {
			ids, err = dkvsindexer.DecodeRGB11RegistryContracts(existing.Value)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				if id == contractID {
					return dkvsindexer.RGB11RegistrationFromRecord(existing, contractID)
				}
			}
		} else if !errors.Is(err, ErrDKVSRecordNotFound) {
			return nil, err
		}

		ids = append(ids, contractID)
		value, err := dkvsindexer.EncodeRGB11RegistryContracts(ids)
		if err != nil {
			return nil, err
		}
		height, err := p.GetBestHeight()
		if err != nil {
			return nil, err
		}
		record, err := p.PutSignedRecord(owner, key, value, dkvsindexer.RecordOptions{IssueHeight: height})
		if err != nil {
			if errors.Is(err, dkvsindexer.ErrWriteConflict) || errors.Is(err, dkvsindexer.ErrInvalidSequence) {
				continue
			}
			return nil, err
		}
		return dkvsindexer.RGB11RegistrationFromRecord(record, contractID)
	}
	return nil, dkvsindexer.ErrWriteConflict
}

func (p *SatsNetDKVSClient) GetRGB11Registration(providerDID, ticker, contractID string) (*dkvsindexer.RGB11Registration, error) {
	key, err := dkvsindexer.RGB11RegistryKey(providerDID, ticker)
	if err != nil {
		return nil, err
	}
	record, err := p.GetRecord(key)
	if err != nil {
		return nil, err
	}
	return dkvsindexer.RGB11RegistrationFromRecord(record, contractID)
}

func rgb11AssetNameFromRegistration(registration *dkvsindexer.RGB11Registration) (*indexer.AssetName, error) {
	if registration == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	name := indexer.NewAssetNameFromString(registration.AssetName)
	if name == nil || name.Protocol != "rgb11" || name.Type != indexer.ASSET_TYPE_FT {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return name, nil
}
