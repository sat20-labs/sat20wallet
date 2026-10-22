package wallet

import (
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/txscript"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
)

// STPAssetOperation identifies the capability required before STP accepts an
// asset. Supporting ordinary transfers alone does not enable channel custody.
type STPAssetOperation string

const (
	STPOpTransfer   STPAssetOperation = "transfer"
	STPOpLock       STPAssetOperation = "lock"
	STPOpUnlock     STPAssetOperation = "unlock"
	STPOpSpliceIn   STPAssetOperation = "splice-in"
	STPOpSpliceOut  STPAssetOperation = "splice-out"
	STPOpExpand     STPAssetOperation = "expand"
	STPOpCommit     STPAssetOperation = "commit"
	STPOpCoopClose  STPAssetOperation = "cooperative-close"
	STPOpForceClose STPAssetOperation = "force-close"
	STPOpPunish     STPAssetOperation = "punish"
	STPOpContract   STPAssetOperation = "contract"
)

// STPAssetRules contains only deterministic asset rules. Implementations must
// not read the network, wallet secrets, or mutable channel state.
type STPAssetRules interface {
	Protocol() string
	ValidateAsset(*indexer.AssetName, STPAssetOperation) error
	NormalizeContract(*indexer.AssetName) error
	Policy(*AssetName) STPAssetPolicy
}

// STPAssetBindingRules separates native quantity precision from sat binding.
// Legacy asset metadata continues to use N as its sat binding ratio.
type STPAssetBindingRules interface {
	BindingSat(*AssetName) uint32
}

func STPAssetBindingSat(asset *AssetName) uint32 {
	if asset == nil {
		return 1
	}
	if rules, ok := stpAssetProtocols.byName[asset.Protocol].(STPAssetBindingRules); ok {
		return rules.BindingSat(asset)
	}
	if asset.N <= 0 {
		return 0
	}
	return uint32(asset.N)
}

// STPAssetPolicy describes how an asset uses the existing STP UTXO layout.
// Protocol-specific transaction construction remains in the asset planner.
type STPAssetPolicy struct {
	StubCount          int
	StubValue          int64
	SharedStubs        bool
	AccountBased       bool
	NeedsRevealKey     bool
	PartialTransfer    bool
	FixedOutputIndexes bool
	SpliceFeeReserve   int64
	SplicePayloadSize  int
	// AlwaysSplicePayload reserves a native commitment even for an exact
	// amount splice without asset change (for example an RGB DBC output).
	AlwaysSplicePayload bool
	// Some protocols use stubs as fee/carrier reserves, without assigning
	// ordinal offsets. They must not prepend an ordinal input stub.
	NoOrdinalInputStub bool
	// ExchangeRecoveryPackages requires complete peer recovery transactions
	// in the normal revocation exchange, before a channel update is accepted.
	ExchangeRecoveryPackages bool
}

type btcSTPRules struct{}
type ordxSTPRules struct{}
type runesSTPRules struct{}
type brc20STPRules struct{}

func (btcSTPRules) Protocol() string   { return "" }
func (ordxSTPRules) Protocol() string  { return indexer.PROTOCOL_NAME_ORDX }
func (runesSTPRules) Protocol() string { return indexer.PROTOCOL_NAME_RUNES }
func (brc20STPRules) Protocol() string { return indexer.PROTOCOL_NAME_BRC20 }

func validateSTPOperation(operation STPAssetOperation) error {
	switch operation {
	case STPOpTransfer, STPOpLock, STPOpUnlock, STPOpSpliceIn, STPOpSpliceOut,
		STPOpExpand, STPOpCommit, STPOpCoopClose, STPOpForceClose, STPOpPunish,
		STPOpContract:
		return nil
	default:
		return fmt.Errorf("unsupported STP operation %q", operation)
	}
}

func (btcSTPRules) ValidateAsset(name *indexer.AssetName, operation STPAssetOperation) error {
	if err := validateSTPOperation(operation); err != nil {
		return err
	}
	if name == nil || *name != indexer.ASSET_PLAIN_SAT {
		return fmt.Errorf("invalid plain bitcoin asset")
	}
	return nil
}

func validateSTPProtocolAsset(name *indexer.AssetName, protocol string, operation STPAssetOperation) error {
	if err := validateSTPOperation(operation); err != nil {
		return err
	}
	if name == nil {
		return fmt.Errorf("missing STP asset")
	}
	if name.Protocol != protocol {
		return fmt.Errorf("invalid protocol %s, expected %s", name.Protocol, protocol)
	}
	return nil
}

func (ordxSTPRules) ValidateAsset(name *indexer.AssetName, operation STPAssetOperation) error {
	if err := validateSTPProtocolAsset(name, indexer.PROTOCOL_NAME_ORDX, operation); err != nil {
		return err
	}
	if operation == STPOpContract && name.Type != indexer.ASSET_TYPE_FT {
		return fmt.Errorf("invalid asset type %s for STP contracts", name.Type)
	}
	switch name.Type {
	case indexer.ASSET_TYPE_NFT:
		// The indexer also represents the generic inscription collection with
		// an empty ticker. Do not apply fungible-token naming rules to it.
		return nil
	case indexer.ASSET_TYPE_FT, indexer.ASSET_TYPE_NS, indexer.ASSET_TYPE_EXOTIC:
		if name.Ticker == "" {
			return fmt.Errorf("invalid asset name %s", name.Ticker)
		}
		return nil
	default:
		return fmt.Errorf("invalid asset type %s", name.Type)
	}
}

func validateSTPFungibleAsset(name *indexer.AssetName, protocol string, operation STPAssetOperation) error {
	if err := validateSTPProtocolAsset(name, protocol, operation); err != nil {
		return err
	}
	if name.Ticker == "" {
		return fmt.Errorf("invalid asset name %s", name.Ticker)
	}
	if name.Type != indexer.ASSET_TYPE_FT {
		return fmt.Errorf("invalid asset type %s", name.Type)
	}
	return nil
}

func (runesSTPRules) ValidateAsset(name *indexer.AssetName, operation STPAssetOperation) error {
	return validateSTPFungibleAsset(name, indexer.PROTOCOL_NAME_RUNES, operation)
}

func (brc20STPRules) ValidateAsset(name *indexer.AssetName, operation STPAssetOperation) error {
	return validateSTPFungibleAsset(name, indexer.PROTOCOL_NAME_BRC20, operation)
}

func (r btcSTPRules) NormalizeContract(name *indexer.AssetName) error {
	return r.ValidateAsset(name, STPOpContract)
}

func (r ordxSTPRules) NormalizeContract(name *indexer.AssetName) error {
	if err := r.ValidateAsset(name, STPOpContract); err != nil {
		return err
	}
	if !indexer.IsValidSat20Name(name.Ticker) {
		return fmt.Errorf("invalid asset name %s", name.Ticker)
	}
	name.Ticker = strings.ToLower(name.Ticker)
	return nil
}

func (r runesSTPRules) NormalizeContract(name *indexer.AssetName) error {
	if err := r.ValidateAsset(name, STPOpContract); err != nil {
		return err
	}
	if strings.Contains(name.Ticker, ".") {
		return fmt.Errorf("\".\" not allowed, use • instead")
	}
	if _, err := runestone.SpacedRuneFromString(name.Ticker); err != nil {
		return fmt.Errorf("invalid asset name %s", name.Ticker)
	}
	return nil
}

func (r brc20STPRules) NormalizeContract(name *indexer.AssetName) error {
	if err := r.ValidateAsset(name, STPOpContract); err != nil {
		return err
	}
	if len(name.Ticker) != 4 && len(name.Ticker) != 5 {
		return fmt.Errorf("invalid ticker length %d", len(name.Ticker))
	}
	name.Ticker = strings.ToLower(name.Ticker)
	return nil
}

func (btcSTPRules) Policy(*AssetName) STPAssetPolicy {
	return STPAssetPolicy{}
}

func (ordxSTPRules) Policy(name *AssetName) STPAssetPolicy {
	if name != nil && (name.Type == indexer.ASSET_TYPE_FT || name.Type == indexer.ASSET_TYPE_EXOTIC) {
		return STPAssetPolicy{StubCount: 2, StubValue: 330}
	}
	return STPAssetPolicy{}
}

func (runesSTPRules) Policy(*AssetName) STPAssetPolicy {
	return STPAssetPolicy{
		StubCount: 1, StubValue: 330, SharedStubs: true,
		PartialTransfer: true, FixedOutputIndexes: true, SpliceFeeReserve: 330,
		SplicePayloadSize: txscript.MaxDataCarrierSize,
	}
}

func (brc20STPRules) Policy(*AssetName) STPAssetPolicy {
	return STPAssetPolicy{
		StubCount: 2, StubValue: STUB_VALUE_BRC20,
		AccountBased: true, NeedsRevealKey: true,
		SpliceFeeReserve: STUB_VALUE_BRC20,
	}
}
