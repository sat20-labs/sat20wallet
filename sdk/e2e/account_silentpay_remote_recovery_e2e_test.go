package e2e

import (
	"bytes"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// This proves the SDK managed-data backup and independent recovery path. It
// exercises no browser UI and does not configure paid storage or AUTOPAY.
func TestSDKAccountSilentPayRemoteBackupIndependentRecovery_NotUI(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
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
	dataKey, err := dkvs.BlobKey(accountID, "account-managed-data")
	require.NoError(t, err)
	client, err := f.manager.GetDKVSClient()
	require.NoError(t, err)

	// Keep both remote records and the local status from one stable revision.
	// The worker may finish an earlier upload between individual HTTP reads.
	cleanSnapshot := func() (wallet.AccountManagementStatus, *wire.DKVSRecord, *wire.DKVSRecord, uint64, bool) {
		start := f.manager.GetAccountManagementStatus()
		if start.PendingChanges != 0 || start.ManagedDataDirty || start.ManagedDataRevision == 0 {
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
		dataBlob, readErr := wallet.DecodeDKVSBlobValue(dataRecord.Value)
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
		end := f.manager.GetAccountManagementStatus()
		stable := end.PendingChanges == 0 && !end.ManagedDataDirty &&
			start.StateSeq == end.StateSeq && start.ManagedDataRevision == end.ManagedDataRevision &&
			decoded.Seq == end.StateSeq && decoded.State.Revision == end.StateSeq &&
			decoded.ManagedData.Revision == end.ManagedDataRevision &&
			bytes.Equal(decoded.Envelope, stateRecord.Value) && bytes.Equal(decoded.ManagedDataEnvelope, dataBlob.Data)
		return end, stateRecord, dataRecord, decoded.Seq, stable
	}
	var baseline wallet.AccountManagementStatus
	var baselineLogicalRevision uint64
	var beforeState, beforeData *wire.DKVSRecord
	require.Eventually(t, func() bool {
		current, stateRecord, dataRecord, logicalRevision, stable := cleanSnapshot()
		if !stable {
			return false
		}
		baseline, beforeState, beforeData, baselineLogicalRevision = current, stateRecord, dataRecord, logicalRevision
		return true
	}, 30*time.Second, 100*time.Millisecond, "initial temporary account backup did not converge to one stable revision")
	baselineRevision := baseline.ManagedDataRevision
	require.Equal(t, baselineLogicalRevision, baseline.StateSeq)

	spAddress, err := f.manager.EnableSilentPay(root.ID, 0)
	require.NoError(t, err)
	require.NotEmpty(t, spAddress)
	require.NotEqual(t, ordinaryAddress, spAddress)

	var after wallet.AccountManagementStatus
	var afterLogicalRevision uint64
	var afterState, afterData *wire.DKVSRecord
	require.Eventually(t, func() bool {
		current, stateRecord, dataRecord, logicalRevision, stable := cleanSnapshot()
		if !stable || current.ManagedDataRevision <= baselineRevision ||
			stateRecord.Seq <= beforeState.Seq || dataRecord.Seq <= beforeData.Seq {
			return false
		}
		after, afterState, afterData, afterLogicalRevision = current, stateRecord, dataRecord, logicalRevision
		return true
	}, 30*time.Second, 100*time.Millisecond, "background managed-data CAS did not converge to one stable revision")
	require.Greater(t, after.ManagedDataRevision, baselineRevision)
	require.Equal(t, afterLogicalRevision, after.StateSeq)
	require.Greater(t, after.StateSeq, baseline.StateSeq)
	require.Zero(t, after.PendingChanges)
	require.False(t, after.ManagedDataDirty)
	updatedRoot := accountReviewFind(t, f.manager, rootFingerprint)
	require.True(t, updatedRoot.Accounts[0].SPEnabled)
	require.False(t, updatedRoot.Accounts[1].SPEnabled)
	require.False(t, updatedRoot.Accounts[2].SPEnabled)
	for _, record := range []*wire.DKVSRecord{afterState, afterData} {
		require.NoError(t, dkvs.VerifySignature(record))
		proof, proofErr := dkvs.ParseFeeProof(record.FeeProof)
		require.NoError(t, proofErr)
		require.Equal(t, dkvs.FeeModeFreeLocal, proof.Mode)
		require.Positive(t, record.TTL)
		require.Equal(t, f.authorization.RecordOptions.TTL, record.TTL)
	}

	// Stop the source device before opening a separate temporary database; the
	// recovery path receives only the root mnemonic and a new local password.
	f.manager.Close()
	fresh, _ := accountReviewDevice(t, network, "")
	require.NoError(t, fresh.StartDKVSSync())
	const recoveryPassword = "silentpay-new-device-password"
	requireAccountPWARootRecovery(t, fresh, f.rootMnemonic, recoveryPassword)

	recoveredStatus := fresh.GetAccountManagementStatus()
	require.True(t, recoveredStatus.RecoveryConfigured)
	require.Equal(t, rootFingerprint, recoveredStatus.RootFingerprint)
	require.Equal(t, accountID, recoveredStatus.AccountID)
	require.Equal(t, after.ManagedDataRevision, recoveredStatus.ManagedDataRevision)
	recoveredRoot := accountReviewFind(t, fresh, rootFingerprint)
	require.Len(t, recoveredRoot.Accounts, 3)
	require.Equal(t, ordinaryAddress, recoveredRoot.Accounts[0].Address)
	require.True(t, recoveredRoot.Accounts[0].SPEnabled)
	require.False(t, recoveredRoot.Accounts[1].SPEnabled)
	require.False(t, recoveredRoot.Accounts[2].SPEnabled)
	enabled, recoveredSPAddress, err := fresh.SilentPayAccount(recoveredRoot.ID, 0)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, spAddress, recoveredSPAddress)
}
