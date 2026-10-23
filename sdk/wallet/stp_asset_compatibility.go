package wallet

import (
	"errors"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
)

// ErrSTPAssetConflict is an admission failure, before funding, signing or
// revoking a channel state. It is not a Bitcoin consensus activation rule.
var ErrSTPAssetConflict = errors.New("incompatible STP channel assets")

// An adapter owns this requirement: adding another protocol must not require a
// protocol-name switch in the channel state machine. Current Runes and RGB
// planners share an exclusive data-carrier position. Bitcoin Core 30 permits
// multiple OP_RETURN outputs, but that alone does not make the STP commitment,
// sweep and punishment planners compatible with that combined layout.
type stpExclusiveDataCarrier interface {
	RequiresExclusiveDataCarrier() bool
}

func (runesSTPRules) RequiresExclusiveDataCarrier() bool { return true }
func (rgb11STPProtocol) RequiresExclusiveDataCarrier() bool { return true }

func requiresExclusiveSTPDataCarrier(protocol STPAssetProtocol) bool {
	rules, ok := protocol.(stpExclusiveDataCarrier)
	return ok && rules.RequiresExclusiveDataCarrier()
}

// ValidateSTPAssetForChannel checks both active funding and balances. Historical
// proof journals are intentionally not considered active assets: a full exit
// must allow a different protocol to enter the now-empty channel. Callers own
// synchronization of the channel snapshot; this function performs no I/O.
func ValidateSTPAssetForChannel(channel *Channel, asset *indexer.AssetName, operation STPAssetOperation) error {
	protocol, err := STPAssetProtocolFor(asset, operation)
	if err != nil {
		return err
	}
	if channel == nil {
		return fmt.Errorf("missing STP channel for asset admission")
	}
	if !requiresExclusiveSTPDataCarrier(protocol) {
		return nil
	}
	active := make(map[indexer.AssetName]struct{})
	for name, outputs := range channel.FundingUtxos {
		if len(outputs) != 0 {
			active[name.AssetName] = struct{}{}
		}
	}
	for _, commitment := range []*ChannelCommitment{channel.LocalCommitment, channel.RemoteCommitment} {
		if commitment == nil {
			continue
		}
		for _, balances := range []map[AssetName]*Decimal{commitment.LocalBalance, commitment.RemoteBalance} {
			for name, amount := range balances {
				if amount != nil && amount.Sign() != 0 {
					active[name.AssetName] = struct{}{}
				}
			}
		}
	}
	for existing := range active {
		if existing.Protocol == asset.Protocol {
			continue
		}
		other, err := STPAssetProtocolFor(&existing, operation)
		if err != nil {
			return err
		}
		if requiresExclusiveSTPDataCarrier(other) {
			return fmt.Errorf("%w: %s and %s require the exclusive STP data-carrier layout", ErrSTPAssetConflict, existing.Protocol, asset.Protocol)
		}
	}
	return nil
}
