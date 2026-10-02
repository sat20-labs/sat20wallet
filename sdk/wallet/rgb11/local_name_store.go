package rgb11wallet

import (
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
)

func (s *ProjectionStore) localAssetNameKey(contractID string) ([]byte, error) {
	if s == nil || s.db == nil {
		return nil, ErrWalletScope
	}
	identity, err := NewContractAssetKey(contractID, indexer.ASSET_TYPE_FT)
	if err != nil {
		return nil, err
	}
	prefix, err := s.scopedPrefix("local-name-")
	if err != nil {
		return nil, err
	}
	return append(prefix, identity.Ticker...), nil
}

// SaveLocalAssetName writes local SDK metadata only. It never rewrites a
// projection, receipt, transfer, contract object, or registered AssetName.
// The public wallet method separately prevents changes to registered names.
func (s *ProjectionStore) SaveLocalAssetName(contractID, name string) error {
	if _, err := (LocalNameMetadata{ContractID: contractID}).Rename(name); err != nil {
		return err
	}
	key, err := s.localAssetNameKey(contractID)
	if err != nil {
		return err
	}
	return s.db.Write(key, []byte(name))
}

// LoadLocalAssetName returns ErrKeyNotFound when the SDK-generated default
// should be used. Database corruption is not silently treated as no alias.
func (s *ProjectionStore) LoadLocalAssetName(contractID string) (string, error) {
	key, err := s.localAssetNameKey(contractID)
	if err != nil {
		return "", err
	}
	raw, err := s.db.Read(key)
	if err != nil {
		return "", err
	}
	name := string(raw)
	if _, err := (LocalNameMetadata{ContractID: contractID}).Rename(name); err != nil {
		return "", fmt.Errorf("%w: invalid local RGB11 name: %v", ErrRGB11Inconsistent, err)
	}
	return name, nil
}
