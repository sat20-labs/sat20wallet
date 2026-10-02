package rgb11wallet

import (
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
)

const TranscendRegistrationDescriptorMagic = "rgb11-reg-v1"

// TranscendRegistrationDescriptor contains only facts that cannot be recovered
// from the existing transcend contract asset identity or immutable Bitcoin
// history. ContractID/type come from AssetName; GenesisAddress comes from the
// GenesisOutpoint; Primary DID ownership is checked before deployment.
type TranscendRegistrationDescriptor struct {
	BaseTicker      string `json:"baseTicker"`
	GenesisOutpoint string `json:"genesisOutpoint"`
	ProviderDID     string `json:"providerDid"`
}

func ValidateTranscendRegistrationDescriptor(d *TranscendRegistrationDescriptor) error {
	if d == nil {
		return ErrInvalidRGB11Asset
	}
	base, err := namingBaseTicker(d.BaseTicker)
	if err != nil || base != NormalizeTicker(d.BaseTicker) {
		return ErrInvalidRGB11Asset
	}
	outpoint, err := wire.NewOutPointFromString(d.GenesisOutpoint)
	if err != nil || outpoint.String() != d.GenesisOutpoint {
		return ErrInvalidRGB11Asset
	}
	if err := ValidatePrimaryDIDName(d.ProviderDID); err != nil {
		return err
	}
	return nil
}

func EncodeTranscendRegistrationDescriptor(d *TranscendRegistrationDescriptor) ([]byte, error) {
	if err := ValidateTranscendRegistrationDescriptor(d); err != nil {
		return nil, err
	}
	return stxscript.NewScriptBuilder().
		AddData([]byte(TranscendRegistrationDescriptorMagic)).
		AddData([]byte(d.BaseTicker)).
		AddData([]byte(d.GenesisOutpoint)).
		AddData([]byte(d.ProviderDID)).
		Script()
}

// DecodeTranscendRegistrationDescriptor reads the optional descriptor appended
// after the four ContractBase fields. ContractID is validated from the existing
// RGB11 AssetName and is deliberately not duplicated in the descriptor.
func DecodeTranscendRegistrationDescriptor(content []byte) (*TranscendRegistrationDescriptor, error) {
	tokenizer := stxscript.MakeScriptTokenizer(0, content)
	if !tokenizer.Next() || tokenizer.Err() != nil { // template
		return nil, ErrInvalidRGB11Asset
	}
	if !tokenizer.Next() || tokenizer.Err() != nil || tokenizer.Data() == nil { // asset
		return nil, ErrInvalidRGB11Asset
	}
	asset := indexer.NewAssetNameFromString(string(tokenizer.Data()))
	if asset == nil || asset.Protocol != Protocol {
		return nil, ErrInvalidRGB11Asset
	}
	if _, err := consensus.ParseContractID(asset.Ticker); err != nil {
		return nil, ErrInvalidRGB11Asset
	}
	for i := 0; i < 2; i++ { // start/end block
		if !tokenizer.Next() || tokenizer.Err() != nil {
			return nil, ErrInvalidRGB11Asset
		}
	}
	if !tokenizer.Next() {
		if tokenizer.Err() != nil {
			return nil, ErrInvalidRGB11Asset
		}
		return nil, nil
	}
	if string(tokenizer.Data()) != TranscendRegistrationDescriptorMagic {
		return nil, ErrInvalidRGB11Asset
	}
	next := func() (string, error) {
		if !tokenizer.Next() || tokenizer.Err() != nil || tokenizer.Data() == nil {
			return "", ErrInvalidRGB11Asset
		}
		return string(tokenizer.Data()), nil
	}
	ticker, err := next()
	if err != nil {
		return nil, err
	}
	outpoint, err := next()
	if err != nil {
		return nil, err
	}
	provider, err := next()
	if err != nil {
		return nil, err
	}
	if tokenizer.Next() || tokenizer.Err() != nil {
		return nil, ErrInvalidRGB11Asset
	}
	d := &TranscendRegistrationDescriptor{
		BaseTicker: ticker, GenesisOutpoint: outpoint, ProviderDID: provider,
	}
	if err := ValidateTranscendRegistrationDescriptor(d); err != nil {
		return nil, err
	}
	return d, nil
}
