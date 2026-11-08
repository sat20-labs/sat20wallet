package wallet

import (
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/seals"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/stretchr/testify/require"
)

func TestRGB11LocalChangeAllocationsAllowsDistinctSealsOnMixedCarrier(t *testing.T) {
	const txid = "mixed-change-tx"
	control, err := seals.RandomWitnessBlindSeal(1)
	require.NoError(t, err)
	fungible, err := seals.RandomWitnessBlindSeal(1)
	require.NoError(t, err)
	controlStrict, err := control.StrictBytes()
	require.NoError(t, err)
	fungibleStrict, err := fungible.StrictBytes()
	require.NoError(t, err)
	pending := &rgb11wallet.PendingTransfer{
		State:       rgb11wallet.TransferState{WitnessTxID: txid},
		ChangeSeals: []seals.GraphBlindSeal{control, fungible},
	}
	allocations := []rgb11wallet.ValidatedAllocation{
		{OutPoint: txid + ":1", AssetName: indexer.AssetName{Protocol: "rgb11", Type: "control", Ticker: "contract"},
			WitnessTxPtr: true, WitnessTxID: txid, SealBlinding: control.Blinding, SealDisclosure: controlStrict},
		{OutPoint: txid + ":1", AssetName: indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: "contract"},
			WitnessTxPtr: true, WitnessTxID: txid, SealBlinding: fungible.Blinding, SealDisclosure: fungibleStrict},
	}
	matched, err := rgb11LocalChangeAllocations(allocations, pending)
	require.NoError(t, err)
	require.Len(t, matched, 2)
	unknown, err := seals.RandomWitnessBlindSeal(1)
	require.NoError(t, err)
	unknownStrict, err := unknown.StrictBytes()
	require.NoError(t, err)
	extra := rgb11wallet.ValidatedAllocation{
		OutPoint: txid + ":1", AssetName: indexer.AssetName{Protocol: "rgb11", Type: "f", Ticker: "other"},
		WitnessTxPtr: true, WitnessTxID: txid, SealBlinding: unknown.Blinding, SealDisclosure: unknownStrict,
	}
	_, err = rgb11LocalChangeAllocations(append(append([]rgb11wallet.ValidatedAllocation(nil), allocations...), extra), pending)
	require.ErrorIs(t, err, rgb11wallet.ErrInvalidProof, "an unknown seal on a declared change carrier cannot be ignored")
	extra.OutPoint = txid + ":2"
	otherCarrier, err := rgb11LocalChangeAllocations(append(append([]rgb11wallet.ValidatedAllocation(nil), allocations...), extra), pending)
	require.NoError(t, err, "unrelated carriers are outside this transfer's local change")
	require.Len(t, otherCarrier, 2)
	allocations[1].AssetName = allocations[0].AssetName
	_, err = rgb11LocalChangeAllocations(allocations, pending)
	require.Error(t, err, "same-asset Opouts cannot overwrite one projection key")
	allocations[1].AssetName.Type = indexer.ASSET_TYPE_FT
	allocations[1].SealDisclosure = controlStrict
	_, err = rgb11LocalChangeAllocations(allocations, pending)
	require.Error(t, err, "one reveal must not satisfy two allocations")
}
