package wallet

import (
	"encoding/hex"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec"
)

const testRGB11FreeLocalTTL = uint64(144)

func configureRGB11DKVSTestManager(manager *Manager, remote HttpClient) {
	manager.cfg = &sdkcommon.Config{
		Env: "test", Chain: "testnet",
		IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"},
	}
	manager.http = remote
}

func TestRGB11SnapshotDoesNotCopyGlobalTickerCatalog(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 42)
	globalName := indexer.AssetName{Protocol: rgb11wallet.Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: "global_test"}
	manager.tickerInfoMap[globalName.String()] = &indexer.TickerInfo{
		AssetName: globalName, DisplayName: "Global RGB Test",
	}
	walletID, err := manager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	wantWalletID := "rgb11-" + hex.EncodeToString(manager.wallet.GetPubKey().SerializeCompressed())
	if walletID != wantWalletID {
		t.Fatalf("RGB11 wallet key contains format version: got=%s want=%s", walletID, wantWalletID)
	}
	snapshot, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	if rgb11SnapshotHasState(snapshot) {
		t.Fatal("empty wallet inherited global RGB ticker metadata")
	}
}

func TestRGB11SnapshotPreflightDoesNotPartiallyImportEngineState(t *testing.T) {
	sourcePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	source := newRGB11MultiDeviceManager(t, sourcePriv, 42)
	createRGB11MultiDeviceInvoice(t, source, "recipient-preflight")
	engineRecords, err := source.rgbManager.engineStore.ExportSnapshot()
	if err != nil || len(engineRecords) != 1 {
		t.Fatalf("source engine records=%d err=%v", len(engineRecords), err)
	}

	targetPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	target := newRGB11MultiDeviceManager(t, targetPriv, 43)
	snapshot := &RGB11WalletSnapshot{
		Version: rgb11WalletSnapshotVersion, EngineRecords: engineRecords,
		ProjectionRecords: []rgb11wallet.SnapshotRecord{{Key: "invalid-record", Value: []byte{1}}},
	}
	if err := target.rgbManager.importRGB11WalletSnapshot(snapshot); err == nil {
		t.Fatal("invalid projection snapshot was accepted")
	}
	restoredEngine, err := target.rgbManager.engineStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(restoredEngine) != 0 {
		t.Fatalf("engine state was imported before projection preflight: %+v", restoredEngine)
	}
}

func newRGB11MultiDeviceManager(t *testing.T, priv *btcec.PrivateKey, localWalletID int64) *Manager {
	t.Helper()
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { database.Close() })
	wallet := dkvsTestWalletFromPriv(t, priv)
	manager := &Manager{
		db: database, wallet: wallet,
		status: &Status{CurrentWallet: localWalletID, CurrentAccount: 0, SyncHeightL2: 1},
		walletInfoMap: map[int64]*WalletInfo{localWalletID: {
			WalletInDB: WalletInDB{Id: localWalletID, Accounts: 1, Type: WALLET_TYPE_MNEMONIC}, Wallet: wallet,
		}},
		tickerInfoMap: make(map[string]*indexer.TickerInfo),
		utxoLockerL1:  NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN),
	}
	rgbManager, err := newRGB11Manager(manager, database, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rgbManager.consistencyStatus = "ok"
	manager.rgbManager = rgbManager
	if err := manager.rgbManager.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.rgbManager.scopeStates.stopReconciliations() })
	return manager
}

func flushRGB11Background(t *testing.T, manager *Manager) {
	t.Helper()
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
}

func createRGB11MultiDeviceInvoice(t *testing.T, manager *Manager, recipient string) string {
	t.Helper()
	request, err := manager.rgbManager.engine.CreateReceive(corewallet.ReceiveParams{
		Network: invoicing.BitcoinTestnet4, RecipientID: recipient,
		WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return request.RequestID
}
