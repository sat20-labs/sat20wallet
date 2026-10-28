package wallet

import (
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestLockWithExpandLocalStageGobRoundTrip(t *testing.T) {
	database := newMemoryKVDB()
	asset := &AssetName{AssetName: wire.AssetName{Protocol: "ordx", Type: "f", Ticker: "pizza"}, N: 1}
	reservation := &LocalActionPerformData{
		ReservationBase: newReservationBase(901, RS_PERFORM_ACTION_TX_BROADCASTED, nil),
		Action:          LOCAL_ACTION_LOCK_WITH_EXPAND,
		ActionParam: &LocalActionParam_Expand{
			ChannelId: "channel",
			AssetName: asset,
			Amt:       indexer.NewDefaultDecimal(100),
		},
		TxId: "expand-local-tx",
		ActionResvs: []*SubActionInfo{{
			ActionType: "expand_local",
			ResvId:     902,
			TxId:       "expand-local-tx",
			MoreData:   indexer.NewDefaultDecimal(60),
		}},
	}

	require.NoError(t, SaveReservation(database, reservation))
	loaded, err := LoadReservation(database, nil, RESV_TYPE_LOCALACTION, reservation.Id)
	require.NoError(t, err)
	restored, ok := loaded.(*LocalActionPerformData)
	require.True(t, ok)
	require.Equal(t, "expand_local", restored.ActionResvs[0].ActionType)
	require.Equal(t, reservation.ActionResvs[0].TxId, restored.ActionResvs[0].TxId)

	remaining, ok := restored.ActionResvs[0].MoreData.(*Decimal)
	require.True(t, ok, "remaining amount must be restored as a Decimal")
	require.Equal(t, "60", remaining.String())
	param, ok := restored.ActionParam.(*LocalActionParam_Expand)
	require.True(t, ok)
	require.Equal(t, "100", param.Amt.String())
	require.Equal(t, "ordx:f:pizza", param.AssetName.String())
}
