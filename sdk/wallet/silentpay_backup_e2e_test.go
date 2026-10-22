package wallet

import (
	"encoding/json"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/stretchr/testify/require"
)

func TestSilentPayStandaloneBackupRestoresEnabledAccountsE2E(t *testing.T) {
	oldEnv, oldChain, oldMode, oldIndexerChain := _env, _chain, _mode, indexer.CHAIN
	t.Cleanup(func() { _env, _chain, _mode, indexer.CHAIN = oldEnv, oldChain, oldMode, oldIndexerChain })
	indexer.CHAIN = "testnet"
	endpoint := &common.Indexer{Scheme: "http", Host: "127.0.0.1:1", Proxy: "testnet"}
	cfg := &common.Config{Env: "test", Chain: "testnet", Mode: "client", IndexerL1: endpoint, IndexerL2: endpoint,
		Peers: []string{"b@" + indexer.GetBootstrapPubKey() + "@http://127.0.0.1:1/testnet"}}
	newDevice := func() (*Manager, func()) {
		database := NewKVDB(t.TempDir())
		require.NotNil(t, database)
		manager := NewManager(cfg, database)
		require.NotNil(t, manager)
		closed := false
		close := func() {
			if !closed {
				manager.Close()
				require.NoError(t, database.Close())
				closed = true
			}
		}
		t.Cleanup(close)
		return manager, close
	}
	const oldPassword = "sp-backup-source-only"
	const newPassword = "sp-backup-restored-only"
	source, closeSource := newDevice()
	id, err := source.ImportWallet(silentPayE2EMnemonic, oldPassword)
	require.NoError(t, err)
	require.NoError(t, source.SwitchAccount(2))
	wantAddresses := make(map[uint32]string)
	for _, index := range []uint32{0, 2} {
		wantAddresses[index], err = source.EnableSilentPay(id, index)
		require.NoError(t, err)
	}
	backup, err := source.ExportAccountBackupForPWA(oldPassword, nil)
	require.NoError(t, err)
	// JSON is the existing standalone backup transport. No source DB or
	// managed-data provider payload is handed to the new devices.
	encoded, err := json.Marshal(backup)
	require.NoError(t, err)
	closeSource()

	for _, api := range []string{"RestoreAccountBackup", "RestoreAccountBackupWithResult"} {
		t.Run(api, func(t *testing.T) {
			var restoredBackup account.Backup
			require.NoError(t, json.Unmarshal(encoded, &restoredBackup))
			target, closeTarget := newDevice()
			defer closeTarget()
			if api == "RestoreAccountBackup" {
				require.NoError(t, target.RestoreAccountBackup(restoredBackup, newPassword))
			} else {
				_, err := target.RestoreAccountBackupWithResult(restoredBackup, newPassword)
				require.NoError(t, err)
			}
			catalog := target.GetWalletCatalog()
			require.Len(t, catalog, 1)
			require.Len(t, catalog[0].Accounts, 3)
			for index := uint32(0); index < 3; index++ {
				enabled, address, err := target.SilentPayAccount(catalog[0].ID, index)
				require.NoError(t, err)
				wantAddress, wantEnabled := wantAddresses[index]
				require.Equal(t, wantEnabled, enabled, "standalone backup lost SP opt-in for account %d", index)
				require.Equal(t, wantAddress, address)
			}
			persisted, err := loadWallet(target.db, catalog[0].ID)
			require.NoError(t, err)
			require.Equal(t, map[uint32]bool{0: true, 2: true}, persisted.SPEnabled)
			_, err = target.UnlockWallet(newPassword)
			require.NoError(t, err)
			_, err = target.LastSyncedSilentPay(catalog[0].ID, 0)
			require.ErrorContains(t, err, "no complete balance scan", "restoring opt-in must not invent a scanned balance")
		})
	}
}
