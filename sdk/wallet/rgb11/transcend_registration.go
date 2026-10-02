package rgb11wallet

import (
	"strings"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/rgb11/consensus"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
)

const TranscendRegistrationDescriptorMagic = "rgb11-reg-v1"

type TranscendRegistrationDescriptor struct {
	ContractID      string `json:"contractId"`
	BaseTicker      string `json:"baseTicker"`
	GenesisOutpoint string `json:"genesisOutpoint"`
	GenesisAddress  string `json:"genesisAddress"`
}

func ValidateTranscendRegistrationDescriptor(d *TranscendRegistrationDescriptor, params *chaincfg.Params) error {
	if d == nil || params == nil {
		return ErrInvalidRGB11Asset
	}
	if _, err := consensus.ParseContractID(d.ContractID); err != nil {
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
	address, err := canonicalNamingAddressForParams(d.GenesisAddress, params)
	if err != nil || address != d.GenesisAddress {
		return ErrInvalidRGB11Asset
	}
	return nil
}

func canonicalNamingAddressForParams(address string, params *chaincfg.Params) (string, error) {
	if params == nil || strings.TrimSpace(address) != address || address == "" {
		return "", ErrNamingOriginUnavailable
	}
	decoded, err := btcutil.DecodeAddress(address, params)
	if err != nil || !decoded.IsForNet(params) {
		return "", ErrNamingOriginUnavailable
	}
	return decoded.EncodeAddress(), nil
}

func EncodeTranscendRegistrationDescriptor(d *TranscendRegistrationDescriptor, params *chaincfg.Params) ([]byte, error) {
	if err := ValidateTranscendRegistrationDescriptor(d, params); err != nil {
		return nil, err
	}
	return stxscript.NewScriptBuilder().
		AddData([]byte(TranscendRegistrationDescriptorMagic)).
		AddData([]byte(d.ContractID)).
		AddData([]byte(d.BaseTicker)).
		AddData([]byte(d.GenesisOutpoint)).
		AddData([]byte(d.GenesisAddress)).
		Script()
}


// DecodeTranscendRegistrationDescriptor reads the optional descriptor appended
// after the four ContractBase fields. A nil descriptor means a non-RGB legacy
// transcend contract with no extension.
func DecodeTranscendRegistrationDescriptor(content []byte, params *chaincfg.Params) (*TranscendRegistrationDescriptor, error) {
	tokenizer := stxscript.MakeScriptTokenizer(0, content)
	for i := 0; i < 4; i++ {
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
	contractID, err := next()
	if err != nil { return nil, err }
	ticker, err := next()
	if err != nil { return nil, err }
	outpoint, err := next()
	if err != nil { return nil, err }
	address, err := next()
	if err != nil { return nil, err }
	if tokenizer.Next() || tokenizer.Err() != nil {
		return nil, ErrInvalidRGB11Asset
	}
	d := &TranscendRegistrationDescriptor{
		ContractID: contractID, BaseTicker: ticker,
		GenesisOutpoint: outpoint, GenesisAddress: address,
	}
	if err := ValidateTranscendRegistrationDescriptor(d, params); err != nil {
		return nil, err
	}
	return d, nil
}
