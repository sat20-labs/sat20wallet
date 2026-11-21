package e2e

import (
	"bytes"
	"reflect"
	"slices"
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestSDKSilentPayColdRootRestore(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)
	mnemonic := accountSyncMnemonic(t, 253)
	config, _ := accountReviewConfig(t, network)
	db := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, db)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	local := wallet.NewManager(config, db)
	require.NotNil(t, local)
	// A standalone wallet backup restores the same catalog as a device
	// that already shared its address before account management was set up.
	restored, err := local.RestoreAccountBackupWithResult(account.Backup{
		Version: account.BackupVersion,
		Wallets: []account.WalletBackup{{
			Name: "Standalone Root", Type: account.WalletMnemonic, Secret: []byte(mnemonic), AccountCount: 1,
			SubAccounts: []account.SubAccount{{Index: 0}}, SPEnabled: []uint32{0},
		}},
	}, accountReviewPassword)
	require.NoError(t, err)
	require.Len(t, restored, 1)
	id := restored[0].ID
	require.False(t, local.GetAccountManagementStatus().RecoveryConfigured)
	enabled, address, err := local.SilentPayAccount(id, 0)
	require.NoError(t, err)
	require.True(t, enabled)
	require.NotEmpty(t, address)
	local.Close()

	source := prepareAccountReviewWithMnemonic(t, network, mnemonic, true)
	source.activate(t)
	root := source.manager.GetAccountManagementStatus().RootFingerprint
	sourceRoot := accountReviewFind(t, source.manager, root)
	require.False(t, sourceRoot.Accounts[0].SPEnabled)
	sourceWallet := source.manager.FindWalletById(sourceRoot.ID)
	require.NotNil(t, sourceWallet)
	key, err := dkvs.PersonalKey(sourceWallet.GetPubKey().SerializeCompressed(), "account/state")
	require.NoError(t, err)
	client := dkvsClientForNode(t, network.Core)
	before, err := client.GetRecordDirect(key)
	require.NoError(t, err)
	require.NotNil(t, before)
	source.manager.Close()

	reopened := wallet.NewManager(config, db)
	require.NotNil(t, reopened)
	t.Cleanup(reopened.Close)
	require.False(t, reopened.GetAccountManagementStatus().RecoveryConfigured)
	reopened.Start()
	_, err = reopened.UnlockWallet(accountReviewPassword)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return reopened.GetAccountManagementStatus().RecoveryConfigured
	}, 30*time.Second, 100*time.Millisecond, "cold unlock must discover and restore the remote account")
	enabled, restoredAddress, err := reopened.SilentPayAccount(id, 0)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, address, restoredAddress)
	require.Eventually(t, func() bool {
		record, err := client.GetRecordDirect(key)
		if err != nil || record == nil || record.Seq <= before.Seq {
			return false
		}
		state, err := account.OpenManagedState(source.secret, source.pkg.Envelope.Locator.AccountID, record.Value)
		return err == nil && len(state.Wallets) == 1 && slices.Equal(state.Wallets[0].SPEnabled, []uint32{0})
	}, 30*time.Second, 100*time.Millisecond, "the cold-restored wallet must write back its opt-in")

	peer, _ := accountReviewDevice(t, network, "")
	requireAccountPWARootRecovery(t, peer, mnemonic, accountReviewPassword)
	peerRoot := accountReviewFind(t, peer, root)
	enabled, restoredAddress, err = peer.SilentPayAccount(peerRoot.ID, 0)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, address, restoredAddress)
}

// Exercise wallet state backup, cross-device merge and independent recovery
// using temporary storage and the native SDK.
func TestSDKAccountSilentPayRemoteBackupIndependentRecovery_NotUI(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 252), true)
	f.activate(t)
	require.NoError(t, f.manager.StartDKVSSync())

	status := f.manager.GetAccountManagementStatus()
	require.True(t, status.RecoveryConfigured)
	require.Equal(t, wallet.AccountStorageTemporary, status.StorageMode)
	require.Nil(t, f.authorization.Autopay)
	rootFingerprint, accountID := status.RootFingerprint, status.AccountID
	root := accountReviewFind(t, f.manager, rootFingerprint)
	require.Len(t, root.Accounts, 3)
	ordinaryAddress := root.Accounts[0].Address
	require.NotEmpty(t, ordinaryAddress)
	require.False(t, root.Accounts[0].SPEnabled)
	require.False(t, root.Accounts[1].SPEnabled)
	require.False(t, root.Accounts[2].SPEnabled)

	rootWallet := f.manager.FindWalletById(root.ID)
	require.NotNil(t, rootWallet)
	stateKey, err := dkvs.PersonalKey(rootWallet.GetPubKey().SerializeCompressed(), "account/state")
	require.NoError(t, err)
	dataKey, err := account.ManagedIndexKey(accountID)
	require.NoError(t, err)
	client := dkvsClientForNode(t, network.Core)

	// Read the wallet catalog, directory, module copies and local status from
	// one stable set of remote records.
	// The worker may finish an earlier upload between individual HTTP reads.
	cleanSnapshot := func() (wallet.AccountManagementStatus, *wire.DKVSRecord, *wire.DKVSRecord, uint64, bool) {
		start := f.manager.GetAccountManagementStatus()
		if start.PendingChanges != 0 || start.ManagedDataDirty || start.StateSeq == 0 {
			return start, nil, nil, 0, false
		}
		stateRecord, readErr := client.GetRecordDirect(stateKey)
		if readErr != nil || stateRecord == nil {
			return start, nil, nil, 0, false
		}
		dataRecord, readErr := client.GetRecordDirect(dataKey)
		if readErr != nil || dataRecord == nil {
			return start, nil, nil, 0, false
		}
		index, readErr := account.OpenManagedIndex(f.secret, accountID, "testnet", dataRecord.Value)
		if readErr != nil {
			return start, nil, nil, 0, false
		}
		decoded, readErr := f.manager.LoadAccountManagementStateForRecovery(
			f.location, f.pkg.Envelope.Locator, f.secret, f.rootMnemonic)
		if readErr != nil || decoded == nil {
			return start, nil, nil, 0, false
		}
		confirmedState, readErr := client.GetRecordDirect(stateKey)
		if readErr != nil || confirmedState == nil || dkvs.RecordHash(stateRecord) != dkvs.RecordHash(confirmedState) {
			return start, nil, nil, 0, false
		}
		for _, copy := range decoded.Copies {
			actual, err := client.GetRecordDirect(copy.Entry.Key)
			if err != nil || actual == nil || copy.Record == nil || dkvs.RecordHash(actual) != dkvs.RecordHash(copy.Record) {
				return start, nil, nil, 0, false
			}
		}
		end := f.manager.GetAccountManagementStatus()
		stable := end.PendingChanges == 0 && !end.ManagedDataDirty &&
			start.StateSeq == end.StateSeq && decoded.Seq == end.StateSeq &&
			bytes.Equal(decoded.Envelope, stateRecord.Value) && reflect.DeepEqual(decoded.Index, index)
		return end, stateRecord, dataRecord, decoded.Seq, stable
	}
	var baseline wallet.AccountManagementStatus
	var baselineStateSeq uint64
	var beforeState, beforeData *wire.DKVSRecord
	require.Eventually(t, func() bool {
		current, stateRecord, dataRecord, stateSeq, stable := cleanSnapshot()
		if !stable {
			return false
		}
		baseline, beforeState, beforeData, baselineStateSeq = current, stateRecord, dataRecord, stateSeq
		return true
	}, 30*time.Second, 100*time.Millisecond, "initial temporary account backup did not converge to one stable set of remote records")
	require.Equal(t, baselineStateSeq, baseline.StateSeq)

	peer, _ := accountReviewDevice(t, network, "")
	const recoveryPassword = "silentpay-new-device-password"
	requireAccountPWARootRecovery(t, peer, f.rootMnemonic, recoveryPassword)
	require.NoError(t, peer.StartDKVSSync())
	requireE2EFirstBackup(t, peer)
	peerRoot := accountReviewFind(t, peer, rootFingerprint)
	require.Equal(t, baselineStateSeq, peer.GetAccountManagementStatus().StateSeq)

	spAddress, err := f.manager.EnableSilentPay(root.ID, 0)
	require.NoError(t, err)
	require.NotEmpty(t, spAddress)
	require.NotEqual(t, ordinaryAddress, spAddress)
	peerAddress, err := peer.EnableSilentPay(peerRoot.ID, 1)
	require.NoError(t, err)
	require.NotEqual(t, spAddress, peerAddress)

	var after wallet.AccountManagementStatus
	var afterStateSeq uint64
	var afterState, afterData *wire.DKVSRecord
	require.Eventually(t, func() bool {
		current, stateRecord, dataRecord, stateSeq, stable := cleanSnapshot()
		if !stable || stateSeq <= baselineStateSeq {
			return false
		}
		state, err := account.OpenManagedState(f.secret, accountID, stateRecord.Value)
		if err != nil || !slices.Equal(state.Wallets[0].SPEnabled, []uint32{0, 1}) {
			return false
		}
		if status := peer.GetAccountManagementStatus(); status.ManagedDataDirty || status.StateSeq != stateSeq {
			return false
		}
		after, afterState, afterData, afterStateSeq = current, stateRecord, dataRecord, stateSeq
		return true
	}, 30*time.Second, 100*time.Millisecond, "wallet state publication did not converge to one stable set of remote records")
	require.Equal(t, afterStateSeq, after.StateSeq)
	require.Greater(t, after.StateSeq, baseline.StateSeq)
	require.NotEqual(t, dkvs.RecordHash(beforeState), dkvs.RecordHash(afterState))
	require.Equal(t, dkvs.RecordHash(beforeData), dkvs.RecordHash(afterData), "updating wallet state must leave its existing directory entry intact")
	require.Zero(t, after.PendingChanges)
	require.False(t, after.ManagedDataDirty)
	updatedRoot := accountReviewFind(t, f.manager, rootFingerprint)
	require.True(t, updatedRoot.Accounts[0].SPEnabled)
	require.True(t, updatedRoot.Accounts[1].SPEnabled)
	require.False(t, updatedRoot.Accounts[2].SPEnabled)
	index, indexRecord := readAccountE2EIndex(t, network.Core, f.secret, accountID)
	_, moduleRecords := readAccountE2EModules(t, network.Core, f.secret, accountID, index)
	for _, entry := range index.Entries {
		require.NotEqual(t, "silentpay", entry.Provider)
	}
	for _, accountIndex := range []uint32{0, 1} {
		scope := wallet.AccountManagedDataScope{Network: "testnet", WalletFingerprint: rootFingerprint, AccountIndex: accountIndex}.ID()
		key, err := account.ManagedModuleKey(f.secret, accountID, "silentpay", scope)
		require.NoError(t, err)
		_, err = client.GetRecordDirect(key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
	}
	for _, record := range append(moduleRecords, afterState, afterData, indexRecord) {
		require.NoError(t, dkvs.VerifySignature(record))
		proof, proofErr := dkvs.ParseFeeProof(record.FeeProof)
		require.NoError(t, proofErr)
		require.Equal(t, dkvs.FeeModeFreeLocal, proof.Mode)
		require.Positive(t, record.TTL)
		require.Equal(t, f.authorization.RecordOptions.TTL, record.TTL)
	}

	// An older device publishes an authenticated wallet state from before the
	// opt-ins. The already-confirmed device must repair the remote copy as
	// well as keep both addresses locally.
	peer.Close()
	f.manager.StopDKVSSync()
	stale, err := client.PutSignedRecordFreeLocal(rootWallet, stateKey, beforeState.Value,
		dkvs.RecordOptions{Seq: afterState.Seq + 1, TTL: afterState.TTL})
	require.NoError(t, err)
	require.NoError(t, f.manager.StartDKVSSync())
	var repairedSeq uint64
	require.Eventually(t, func() bool {
		_, record, _, seq, stable := cleanSnapshot()
		if !stable || seq <= stale.Seq {
			return false
		}
		state, err := account.OpenManagedState(f.secret, accountID, record.Value)
		if err != nil || !slices.Equal(state.Wallets[0].SPEnabled, []uint32{0, 1}) {
			return false
		}
		repairedSeq = seq
		return true
	}, 30*time.Second, 100*time.Millisecond, "confirmed wallet did not write back both SilentPay opt-ins")

	// Stop the source device before opening a separate temporary database; the
	// recovery path receives only the root mnemonic and a new local password.
	f.manager.Close()
	fresh, _ := accountReviewDevice(t, network, "")
	requireAccountPWARootRecovery(t, fresh, f.rootMnemonic, recoveryPassword)
	require.NoError(t, fresh.StartDKVSSync())

	recoveredStatus := fresh.GetAccountManagementStatus()
	require.True(t, recoveredStatus.RecoveryConfigured)
	require.Equal(t, rootFingerprint, recoveredStatus.RootFingerprint)
	require.Equal(t, accountID, recoveredStatus.AccountID)
	require.Equal(t, repairedSeq, recoveredStatus.StateSeq)
	recoveredRoot := accountReviewFind(t, fresh, rootFingerprint)
	require.Len(t, recoveredRoot.Accounts, 3)
	require.Equal(t, ordinaryAddress, recoveredRoot.Accounts[0].Address)
	require.True(t, recoveredRoot.Accounts[0].SPEnabled)
	require.True(t, recoveredRoot.Accounts[1].SPEnabled)
	require.False(t, recoveredRoot.Accounts[2].SPEnabled)
	enabled, recoveredSPAddress, err := fresh.SilentPayAccount(recoveredRoot.ID, 0)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, spAddress, recoveredSPAddress)
	enabled, recoveredSPAddress, err = fresh.SilentPayAccount(recoveredRoot.ID, 1)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, peerAddress, recoveredSPAddress)
}
