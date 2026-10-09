package rgb11wallet

import (
	"encoding/hex"
	"errors"
	"fmt"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"

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

func (s *ProjectionStore) registeredAssetNameKey(contractID string) ([]byte, error) {
	if s == nil || s.db == nil {
		return nil, ErrWalletScope
	}
	identity, err := NewContractAssetKey(contractID, indexer.ASSET_TYPE_FT)
	if err != nil {
		return nil, err
	}
	prefix, err := s.scopedPrefix("registered-name-")
	if err != nil {
		return nil, err
	}
	return append(prefix, identity.Ticker...), nil
}

// Registration caches retain the signed record, so restore cannot turn an
// arbitrary display string into an authenticated canonical name.
func rgb11RegistryAuthority() dkvsindexer.SystemVerifier {
	var keys [][]byte
	for _, encoded := range []string{indexer.GetCoreNodePubKey()} {
		key, err := hex.DecodeString(encoded)
		if err == nil {
			keys = append(keys, key)
		}
	}
	return dkvsindexer.StaticSystemVerifier{Keys: keys}
}

func (s *ProjectionStore) SaveRegisteredAssetRecord(contractID string, record *wire.DKVSRecord) error {
	identity, err := NewContractAssetKey(contractID, indexer.ASSET_TYPE_FT)
	if err != nil {
		return err
	}
	if _, err := VerifyRGB11RegistrationForClient(record, identity.Ticker, rgb11RegistryAuthority()); err != nil {
		return err
	}
	key, err := s.registeredAssetNameKey(contractID)
	if err != nil {
		return err
	}
	raw, err := dkvsindexer.MarshalRecord(record)
	if err != nil {
		return err
	}
	if previous, err := s.db.Read(key); err == nil {
		oldRecord, err := dkvsindexer.UnmarshalRecord(previous)
		if err != nil || string(oldRecord.Value) != string(record.Value) {
			return ErrRGB11Inconsistent
		}
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	return s.db.Write(key, raw)
}

func (s *ProjectionStore) LoadRegisteredAssetName(contractID string) (string, error) {
	key, err := s.registeredAssetNameKey(contractID)
	if err != nil {
		return "", err
	}
	raw, err := s.db.Read(key)
	if err != nil {
		return "", err
	}
	identity, err := NewContractAssetKey(contractID, indexer.ASSET_TYPE_FT)
	if err != nil {
		return "", err
	}
	record, err := dkvsindexer.UnmarshalRecord(raw)
	if err != nil {
		return "", ErrRGB11Inconsistent
	}
	registration, err := VerifyRGB11RegistrationForClient(record, identity.Ticker, rgb11RegistryAuthority())
	if err != nil {
		return "", ErrRGB11Inconsistent
	}
	return registration.AssetName, nil
}
