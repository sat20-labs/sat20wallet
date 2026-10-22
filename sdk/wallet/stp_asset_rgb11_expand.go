package wallet

import (
	"bytes"
	"context"
	"fmt"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
)

// Expanding a channel consumes no Bitcoin transaction. Its source must already
// be the exact confirmed channel carrier and have independently validated
// funding material; an indexer's empty RGB balance cannot establish ownership.
func (protocol rgb11STPProtocol) BuildExpansionAnchorProof(p *Manager, channel *Channel,
	asset *AssetName, output *TxOutput) ([]byte, error) {
	if channel == nil || asset == nil || output == nil || !bytes.Equal(output.OutValue.PkScript, channel.GetChannelPkScript()) {
		return nil, fmt.Errorf("RGB11 expansion requires its exact private channel carrier")
	}
	if err := protocol.ValidateAsset(&asset.AssetName, STPOpExpand); err != nil {
		return nil, err
	}
	inventory, err := stpRGB11Inventory(channel)
	if err != nil {
		return nil, err
	}
	evidence, err := stpRGB11Evidence(p)
	if err != nil {
		return nil, err
	}
	for _, source := range inventory.Sources {
		if source.OutPoint != output.OutPointStr {
			continue
		}
		if source.AssetName != asset.AssetName || source.Amount.Precision != asset.N || source.Value != output.Value() ||
			!bytes.Equal(source.PkScript, output.OutValue.PkScript) || len(output.Assets) != 1 ||
			output.Assets[0].BindingSat != 0 || source.Amount.Cmp(output.GetAsset(&asset.AssetName)) != 0 {
			return nil, fmt.Errorf("RGB11 expansion changed its full native source")
		}
		validated, err := rgb11wallet.ValidateChannelFunding(context.Background(), source, evidence, nativeproof.Current, GetChainParam().Name)
		if err != nil {
			return nil, err
		}
		if validated.Value != output.Value() || validated.Asset.Name != asset.AssetName ||
			validated.Asset.Amount.Cmp(&source.Amount) != 0 || validated.Asset.Amount.Precision != asset.N {
			return nil, fmt.Errorf("RGB11 expansion source differs from its validated allocation")
		}
		return source.NativeProof()
	}
	return nil, fmt.Errorf("RGB11 expansion has no authenticated source consignment")
}
