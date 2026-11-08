package wallet

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestAccountPasswordAuthenticatesPersistedCredentialsAcrossManagers(t *testing.T) {
	manager, remote, service, closeManager := newAccountGroup(t)
	id, _, err := manager.CreateWallet("original-password")
	require.NoError(t, err)
	discoverInactiveAccount(t, manager)
	runtime := manager.GetWallet()
	require.NoError(t, manager.ChangePassword("original-password", "replacement-password"))
	_, err = manager.UnlockWallet("original-password")
	require.Error(t, err)
	require.Empty(t, manager.GetMnemonic(id, "original-password"))
	require.Error(t, manager.ChangePassword("original-password", "forbidden-password"))
	_, err = manager.UnlockWallet("replacement-password")
	require.NoError(t, err)
	require.Same(t, runtime, manager.GetWallet(), "password reauthentication must preserve the runtime wallet")
	require.Equal(t, "replacement-password", manager.accountPassword)

	// A later PWA runtime opens the committed database, with no old cache copied.
	cfg := manager.cfg
	closeManager()
	restarted, _ := openAccountGroup(t, cfg, remote, service)
	_, err = restarted.UnlockWallet("original-password")
	require.Error(t, err)
	require.Empty(t, restarted.GetMnemonic(id, "original-password"))
	unlocked, err := restarted.UnlockWallet("replacement-password")
	require.NoError(t, err, "cold Manager must use the current profile ciphertext")
	require.Equal(t, id, unlocked)
	require.Equal(t, "replacement-password", restarted.accountPassword)
}

func TestAccountColdUnlockUsesCommittedSelectionAcrossManagers(t *testing.T) {
	manager, remote, service, closeManager := newAccountGroup(t)
	first, _, err := manager.CreateWallet("selection-password")
	require.NoError(t, err)
	discoverInactiveAccount(t, manager)
	second, _, err := manager.CreateWallet("selection-password")
	require.NoError(t, err)
	require.NoError(t, manager.EnsureAccount(second, 1, "Savings", ""))
	require.NoError(t, manager.SwitchWallet(first, ""))
	require.NoError(t, manager.SwitchWallet(second, ""))
	require.NoError(t, manager.SwitchAccount(1))
	committed, err := manager.db.Read([]byte(DB_KEY_STATUS))
	require.NoError(t, err)

	cfg := manager.cfg
	closeManager()
	restarted, _ := openAccountGroup(t, cfg, remote, service)
	id, err := restarted.UnlockWallet("selection-password")
	require.NoError(t, err)
	require.Equal(t, second, id, "cold unlock must read the committed selection")
	require.Equal(t, second, restarted.wallet.GetId())
	require.Equal(t, uint32(1), restarted.wallet.GetSubAccount())
	require.Equal(t, second, restarted.status.CurrentWallet)
	require.Equal(t, uint32(1), restarted.status.CurrentAccount)
	after, err := restarted.db.Read([]byte(DB_KEY_STATUS))
	require.NoError(t, err)
	require.Equal(t, committed, after, "authentication must not rewrite the committed selection")
}

type accountColdUnlockStatusDB struct {
	indexer.KVDB
	status []byte
	err    error
}

func (database *accountColdUnlockStatusDB) Read(key []byte) ([]byte, error) {
	if bytes.Equal(key, []byte(DB_KEY_STATUS)) {
		return database.status, database.err
	}
	return database.KVDB.Read(key)
}

func TestAccountColdUnlockRejectsInvalidDurableSelectionWithoutChanges(t *testing.T) {
	for _, scenario := range []string{"read failure", "missing", "corrupt", "wrong chain", "unknown wallet", "invalid account"} {
		t.Run(scenario, func(t *testing.T) {
			manager, remote, service, closeManager := newAccountGroup(t)
			_, _, err := manager.CreateWallet("selection-password")
			require.NoError(t, err)
			cfg := manager.cfg
			closeManager()
			stale, _ := openAccountGroup(t, cfg, remote, service)
			beforeStatus := cloneStatusForAccountRestore(stale.status)
			beforeProfile := *stale.accountProfile
			beforeGeneration := stale.accountGeneration
			committed, err := stale.db.Read([]byte(DB_KEY_STATUS))
			require.NoError(t, err)
			fault := &accountColdUnlockStatusDB{KVDB: stale.db, status: committed}
			switch scenario {
			case "read failure":
				fault.err = errors.New("selection storage read failed")
			case "missing":
				fault.err = indexer.ErrKeyNotFound
			case "corrupt":
				fault.status = []byte("invalid status")
			default:
				invalid := cloneStatusForAccountRestore(stale.status)
				switch scenario {
				case "wrong chain":
					invalid.CurrentChain = "different-chain"
				case "unknown wallet":
					invalid.CurrentWallet = -1
				case "invalid account":
					invalid.CurrentAccount = uint32(stale.walletInfoMap[invalid.CurrentWallet].Accounts)
				}
				fault.status, err = encodeStatusToBytes(invalid)
				require.NoError(t, err)
			}
			stale.db = fault
			_, err = stale.UnlockWallet("selection-password")
			require.Error(t, err)
			require.Nil(t, stale.wallet)
			for _, info := range stale.walletInfoMap {
				require.Nil(t, info.Wallet)
			}
			require.Equal(t, beforeStatus, stale.status)
			require.Equal(t, beforeProfile, *stale.accountProfile)
			require.Equal(t, beforeGeneration, stale.accountGeneration)
			require.Empty(t, stale.accountSecret)
			require.Empty(t, stale.accountPassword)
			after, err := fault.KVDB.Read([]byte(DB_KEY_STATUS))
			require.NoError(t, err)
			require.Equal(t, committed, after)
		})
	}
}

type accountGuardianReadErrorDB struct{ indexer.KVDB }

func (db *accountGuardianReadErrorDB) Read(key []byte) ([]byte, error) {
	if bytes.HasPrefix(key, []byte(DB_KEY_WALLET)) {
		return nil, errors.New("guardian storage read failed")
	}
	return db.KVDB.Read(key)
}

func TestAccountGuardianPasswordAuthenticationAndRotation(t *testing.T) {
	manager, _, _ := newInactiveAccountGroup(t, "", "original-password")
	_, err := manager.GetOrCreateAccountGuardianIdentity("incorrect-password")
	require.Error(t, err, "a first guardian identity must authenticate the wallet password")
	identity, err := manager.GetOrCreateAccountGuardianIdentity("original-password")
	require.NoError(t, err)
	original, err := manager.LoadAccountGuardianPrivateKey("original-password")
	require.NoError(t, err)
	defer zeroBytes(original)
	database := manager.db
	manager.db = &accountGuardianReadErrorDB{KVDB: database}
	_, err = manager.GetOrCreateAccountGuardianIdentity("original-password")
	require.ErrorContains(t, err, "guardian storage read failed")
	manager.db = &passwordChangeFailFlushDB{KVDB: database}
	require.Error(t, manager.ChangePassword("original-password", "replacement-password"))
	manager.db = database
	unchanged, err := manager.LoadAccountGuardianPrivateKey("original-password")
	require.NoError(t, err)
	require.Equal(t, original, unchanged)
	zeroBytes(unchanged)
	require.NoError(t, manager.ChangePassword("original-password", "replacement-password"))
	_, err = manager.LoadAccountGuardianPrivateKey("original-password")
	require.Error(t, err)
	restored, err := manager.LoadAccountGuardianPrivateKey("replacement-password")
	require.NoError(t, err)
	require.Equal(t, original, restored)
	zeroBytes(restored)
	next, err := manager.GetOrCreateAccountGuardianIdentity("replacement-password")
	require.NoError(t, err)
	require.Equal(t, identity, next)
}

func TestAccountStorageFirstPublicationBindsCurrentCore(t *testing.T) {
	for _, guardian := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovery", true: "guardian"}[guardian], func(t *testing.T) {
			manager, _, client := newInactiveAccountGroup(t, "", "password")
			root, err := manager.accountManagementRootWallet()
			require.NoError(t, err)
			location, err := manager.AccountIndexerLocation()
			require.NoError(t, err)
			auth := AccountStorageAuthorization{Mode: AccountStorageTemporary, Location: location,
				RecordOptions: dkvsindexer.RecordOptions{TTL: testRGB11FreeLocalTTL}}
			bindingKey, err := dkvsindexer.AccountMappingKey(GetChainParam().Name, root.GetAddress())
			require.NoError(t, err)
			store, err := manager.accountDKVSStore()
			require.NoError(t, err)
			_, err = store.client.GetRecordDirect(bindingKey)
			require.ErrorIs(t, err, dkvsindexer.ErrRecordNotFound)
			err = nil
			if guardian {
				// This manager is the Guardian receiving an independent friend's
				// capsule, rather than the owner using itself as its Guardian.
				friend, _, _ := newInactiveAccountGroup(t, "", "password")
				backup, err := friend.ExportAccountBackup("password", nil)
				require.NoError(t, err)
				defer clearAccountBackup(&backup)
				private, public, err := account.GenerateGuardianKey(nil)
				require.NoError(t, err)
				defer zeroBytes(private)
				pkg, err := friend.CreateAccountRecoveryPackage(account.CreateOptions{
					AccountID: friend.accountProfile.AccountID, Backup: backup,
					RecoveryMode: account.RecoveryMode2Of3, Questions: coreQuestions(),
					GuardianPublicKey: public, GuardianMailboxID: manager.accountProfile.AccountID})
				require.NoError(t, err)
				require.NoError(t, manager.PutGuardianCapsuleForStorage(auth, manager.accountProfile.AccountID, *pkg.GuardianCapsule))
			} else {
				_, err = manager.NewAccountRepositoryForStorage(auth)
			}
			require.NoError(t, err)
			binding, err := store.client.GetRecordDirect(bindingKey)
			require.NoError(t, err)
			_, _, descriptor, err := dkvsindexer.ValidateAccountMappingBindingRecord(binding)
			require.NoError(t, err)
			require.Equal(t, client.CoreNodeID(), descriptor.CoreNodeID)
			auth.Location.Host = "foreign.test"
			_, err = manager.NewAccountRepositoryForStorage(auth)
			require.Error(t, err)
		})
	}
}

func TestAccountStorageBindingDoesNotHoldLocalScope(t *testing.T) {
	oldChain, oldIndexerChain := _chain, indexer.CHAIN
	_chain, indexer.CHAIN = "testnet", "testnet"
	t.Cleanup(func() { _chain, indexer.CHAIN = oldChain, oldIndexerChain })
	manager := newAccountManagementAutoTestManager(t)
	gate := &accountActivationNetworkGate{phase: "BindingCAS", started: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(gate.release) }) }
	defer release()
	remote := &accountActivationGateHTTP{rgb11MemoryDKVSHTTP: newRGB11MemoryDKVSHTTP(), gate: gate}
	manager.http = remote
	client := newRGB11MessageNodeClient(remote.rgb11MemoryDKVSHTTP)
	manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	id, _, err := manager.CreateWallet("password")
	require.NoError(t, err)
	discoverInactiveAccount(t, manager)
	location, err := manager.AccountIndexerLocation()
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, err := manager.NewAccountRepositoryForStorage(AccountStorageAuthorization{
			Mode: AccountStorageTemporary, Location: location, RecordOptions: dkvsindexer.RecordOptions{TTL: testRGB11FreeLocalTTL}})
		done <- err
	}()
	select {
	case <-gate.started:
	case err := <-done:
		t.Fatalf("binding ended before its transport gate: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("binding transport was not reached")
	}
	localDone := make(chan error, 1)
	go func() { localDone <- manager.EnsureAccount(id, 1, "Savings", "") }()
	select {
	case err := <-localDone:
		require.NoError(t, err, "unactivated local edits must finish while service binding waits")
	case <-time.After(time.Second):
		t.Error("binding transport holds the local account scope")
	}
	release()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("binding did not complete")
	}
}
