package wallet

import (
	"encoding/json"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestRGB11TickerCacheUsesShortAssetName(t *testing.T) {
	db := NewKVDB(t.TempDir())
	t.Cleanup(func() { db.Close() })
	makeTicker := func(id string) *indexer.TickerInfo {
		name := indexer.AssetName{Protocol: rgb11wallet.Protocol, Type: "f", Ticker: "trgb241@0e8a"}
		content, err := json.Marshal(rgb11wallet.TickerExt{ContractID: id, Ticker: "TRGB241"})
		require.NoError(t, err)
		return &indexer.TickerInfo{AssetName: name, Content: content, Divisibility: 2, ContractID: id}
	}
	info := makeTicker("8650c89a895329747b8140166be3296432e8c0205a2ac838e097d154c53f0e8a")
	require.NoError(t, saveTickerInfo(db, info))
	old := info.AssetName
	old.Ticker = info.ContractID
	_, err := db.Read([]byte(GetTickerInfoKey(old.String())))
	require.ErrorIs(t, err, indexer.ErrKeyNotFound)
	legacy := *info
	legacy.AssetName = old
	require.ErrorIs(t, saveTickerInfo(db, &legacy), ErrRGB11PublicIdentityMismatch)
	_, err = loadTickerInfo(db, &old)
	require.ErrorIs(t, err, ErrRGB11PublicIdentityMismatch)
	got, err := loadTickerInfo(db, &info.AssetName)
	require.NoError(t, err)
	require.Equal(t, info.AssetName, got.AssetName)
	require.Equal(t, info.Content, got.Content)
	require.Equal(t, 2, got.Divisibility)
	collision := makeTicker("6650c89a895329747b8140166be3296432e8c0205a2ac838e097d154c53f0e8a")
	require.ErrorIs(t, saveTickerInfo(db, collision), dkvs.ErrWriteConflict)
	got, err = loadTickerInfo(db, &info.AssetName)
	require.NoError(t, err)
	require.Equal(t, info.ContractID, got.ContractID)
	require.NoError(t, deleteAllTickerInfoFromDB(db))
	require.ErrorIs(t, saveTickerInfo(db, collision), dkvs.ErrWriteConflict)
	_, err = loadTickerInfo(db, &info.AssetName)
	require.ErrorIs(t, err, indexer.ErrKeyNotFound)
}

func TestRGB11TickerCacheRebuildsFromLocalContract(t *testing.T) {
	manager, _, imported, _, _ := newRGB11GenericSendFixture(t)
	manager.rgbManager.scopeStates.stopReconciliations()
	manager.stopAccountBackgroundTasks()
	manager.RemoveAllTickerInfo()
	state, err := manager.GetRGB11State()
	require.NoError(t, err)
	require.NotEmpty(t, state.TickerInfos)
	require.Equal(t, imported.AssetName, state.TickerInfos[0].AssetName)
	info := manager.GetTickerInfo(&imported.AssetName)
	require.NotNil(t, info)
	require.Equal(t, imported.AssetName, info.AssetName)
	key, err := tickerInfoStorageKey(info)
	require.NoError(t, err)
	require.Equal(t, GetTickerInfoKey(imported.AssetName.String()), key)
	id, err := rgb11wallet.RGB11ContractIDHex(imported.ContractID)
	require.NoError(t, err)
	require.Equal(t, id, info.ContractID)
	_, err = manager.db.Read([]byte(key))
	require.NoError(t, err)
}

// A market request is allowed to be the first reader after a cache rewind.
// This fixture has local genesis data but no activated public name record.
func TestMarketRGB11TickerRebuildsFromLocalContract(t *testing.T) {
	manager, _, imported, _, _ := newRGB11GenericSendFixture(t)
	manager.rgbManager.scopeStates.stopReconciliations()
	manager.stopAccountBackgroundTasks()
	before := manager.marketTicker(&imported.AssetName)
	require.NotNil(t, before)

	manager.RemoveAllTickerInfo()
	_, err := loadTickerInfo(manager.db, &imported.AssetName)
	require.ErrorIs(t, err, indexer.ErrKeyNotFound)
	info := manager.marketTicker(&imported.AssetName)
	require.NotNil(t, info)
	require.Equal(t, before.ContractID, info.ContractID)
	require.Equal(t, before.DisplayName, info.DisplayName)
	require.Equal(t, before.MaxSupply, info.MaxSupply)
	require.Equal(t, before.Divisibility, info.Divisibility)
	stored, err := loadTickerInfo(manager.db, &imported.AssetName)
	require.NoError(t, err)
	require.Equal(t, info.ContractID, stored.ContractID)
	require.Same(t, info, manager.GetTickerInfo(&imported.AssetName))
}
