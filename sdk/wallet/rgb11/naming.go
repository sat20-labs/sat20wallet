package rgb11wallet

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
)

const (
	MaxPrimaryDIDLength      = 10
	LocalAddressSuffixLength = 12
)

var (
	ErrInvalidProviderDID      = errors.New("invalid RGB11 provider DID")
	ErrProviderOwnerMismatch   = errors.New("RGB11 provider owner does not match genesis address")
	ErrReservedTickerSuffix    = errors.New("RGB11 ticker uses a reserved ordinal suffix")
	ErrRegisteredNameFrozen    = errors.New("registered RGB11 asset name is immutable")
	ErrNamingOriginUnavailable = errors.New("RGB11 genesis naming origin is unavailable")
)

// ValidatePrimaryDIDName is the additional SatoshiNet bind restriction, not
// proof of DID existence or ownership. The caller must use the canonical DID
// supplied by the Ordinals resolver; no truncation or silent renaming is done.
// Length means Unicode code points, not UTF-8 bytes.
func ValidatePrimaryDIDName(name string) error {
	if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > MaxPrimaryDIDLength ||
		name != strings.ToLower(name) {
		return ErrInvalidProviderDID
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
			strings.ContainsRune("@:/\\", r) {
			return ErrInvalidProviderDID
		}
	}
	return nil
}

func canonicalNamingAddress(address string) (string, error) {
	if address == "" || address != strings.TrimSpace(address) {
		return "", ErrNamingOriginUnavailable
	}
	decoded, err := btcutil.DecodeAddress(address, &chaincfg.MainNetParams)
	if err != nil {
		return "", ErrNamingOriginUnavailable
	}
	return decoded.EncodeAddress(), nil
}

// ValidateProviderBinding requires both addresses to come from verified chain
// facts: the genesis output script and the DID's current sat owner. Merely
// calling this helper with caller-supplied strings is not an authorization.
func ValidateProviderBinding(genesisAddress, ownerAddress, provider string) error {
	if err := ValidatePrimaryDIDName(provider); err != nil {
		return err
	}
	genesis, err := canonicalNamingAddress(genesisAddress)
	if err != nil {
		return err
	}
	owner, err := canonicalNamingAddress(ownerAddress)
	if err != nil || owner != genesis {
		return ErrProviderOwnerMismatch
	}
	return nil
}

func namingBaseTicker(ticker string) (string, error) {
	if !utf8.ValidString(ticker) || strings.TrimSpace(ticker) == "" || strings.ContainsAny(ticker, "@:") {
		return "", ErrInvalidRGB11Asset
	}
	trimmed := strings.TrimSpace(ticker)
	if i := strings.LastIndexByte(trimmed, '_'); i >= 0 && i+1 < len(trimmed) {
		digits := true
		for _, r := range trimmed[i+1:] {
			if r < '0' || r > '9' {
				digits = false
				break
			}
		}
		if digits {
			return "", ErrReservedTickerSuffix
		}
	}
	return NormalizeTicker(trimmed), nil
}

// BuildRegisteredAssetName formats an already allocated registration. It does
// not allocate ordinals and cannot establish an authoritative registration.
// The caller must first validate the provider binding and genesis metadata.
func BuildRegisteredAssetName(ticker, assetType, provider string, ordinal uint64) (indexer.AssetName, error) {
	base, err := namingBaseTicker(ticker)
	if err != nil {
		return indexer.AssetName{}, err
	}
	if err := ValidatePrimaryDIDName(provider); err != nil {
		return indexer.AssetName{}, err
	}
	if ordinal == 0 {
		return indexer.AssetName{}, ErrInvalidRGB11Asset
	}
	if assetType == "" {
		assetType = indexer.ASSET_TYPE_FT
	}
	if assetType != indexer.ASSET_TYPE_FT && assetType != indexer.ASSET_TYPE_NFT {
		return indexer.AssetName{}, ErrInvalidRGB11Asset
	}
	if ordinal > 1 {
		base += "_" + strconv.FormatUint(ordinal, 10)
	}
	return indexer.AssetName{Protocol: Protocol, Type: assetType, Ticker: base + "@" + provider}, nil
}

// BuildLocalDisplayName is a mutable SDK alias, never a balance/database key.
// A nonempty provider must already have passed current primary-bind and owner
// verification. With no qualified provider, use the genesis address suffix.
// No local ordinal is allocated: duplicates must be resolved by full ContractID.
func BuildLocalDisplayName(ticker, genesisAddress, qualifiedProvider string) (string, error) {
	if qualifiedProvider != "" {
		if err := ValidatePrimaryDIDName(qualifiedProvider); err != nil {
			return "", err
		}
		return NormalizeTicker(ticker) + "@" + qualifiedProvider, nil
	}
	address, err := canonicalNamingAddress(genesisAddress)
	if err != nil || len(address) < LocalAddressSuffixLength {
		return "", ErrNamingOriginUnavailable
	}
	return NormalizeTicker(ticker) + "@" + address[len(address)-LocalAddressSuffixLength:], nil
}

// LocalNameMetadata is local SDK state, separate from validated allocations.
// RegisteredName is populated only from an authenticated SatoshiNet registry.
// An ordinary SDK rename cannot turn a local alias into a registered name.
type LocalNameMetadata struct {
	ContractID     string `json:"contract_id"`
	LocalName      string `json:"local_name"`
	RegisteredName string `json:"registered_name,omitempty"`
}

func (m LocalNameMetadata) Rename(name string) (LocalNameMetadata, error) {
	if m.RegisteredName != "" {
		return m, ErrRegisteredNameFrozen
	}
	if !utf8.ValidString(name) || strings.TrimSpace(name) == "" {
		return m, ErrInvalidRGB11Asset
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return m, ErrInvalidRGB11Asset
		}
	}
	m.LocalName = name
	return m, nil
}
