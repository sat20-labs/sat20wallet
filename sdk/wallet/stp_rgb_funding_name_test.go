package wallet

import (
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/stretchr/testify/require"
)

func TestSTPRGBFundingNameUsesCapturedIdentity(t *testing.T) {
	name := indexer.AssetName{Protocol: "rgb11", Type: "f", Ticker: "name"}
	local := indexer.AssetName{Protocol: "rgb11", Type: "f", Ticker: "local-contract"}
	facts := newSTPRGBReservationEvidence()
	facts.fundingName = &stpRGBFundingName{chain: name, local: local}
	view := facts.useView()
	got, err := view.localFundingAsset(nil, &name)
	require.NoError(t, err)
	require.Equal(t, local, *got)
	got.Ticker = "not the captured name"
	got, err = view.localFundingAsset(nil, &name)
	require.NoError(t, err)
	require.Equal(t, local, *got)

	different := name
	different.Ticker = "other"
	_, err = view.localFundingAsset(nil, &different)
	require.ErrorIs(t, err, errSTPRGBEvidenceMissing)
	require.ErrorIs(t, view.complete(), errSTPRGBEvidenceMissing)
}
