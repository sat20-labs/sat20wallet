package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// A PWA user can edit C while its normal background backup of B is uploading.
// B's exact ACK must preserve C; each real remote submission uses Seq+1.
func TestSDKDKVSConfirmedReplica(t *testing.T) {
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, fixture.Network)
	runSDKDKVSConfirmedReplica(t, fixture, "")
}

func runSDKDKVSConfirmedReplica(t *testing.T, fixture *templateFixture, mnemonic string) {
	t.Helper()
	network := fixture.Network
	f := prepareAccountReviewWithMnemonic(t, network, mnemonic, true)
	f.activate(t)
	manager := f.manager
	root := manager.GetWalletCatalog()[0]
	key, err := dkvs.PersonalKey(manager.GetWallet().GetPubKey().SerializeCompressed(), "account/state")
	require.NoError(t, err)
	a := waitForDKVSRecord(t, network.Core, key)
	bHold, cHold := newAccountStateHold(true), newAccountStateHold(false)
	t.Cleanup(bHold.unblock)
	t.Cleanup(cHold.unblock)
	transport := &accountBackupHTTP{inner: wallet.NewHTTPClient(), holds: []*accountStateHold{bHold, cHold}}
	manager.SetDKVSHttpClient(transport)
	require.NoError(t, manager.StartDKVSSync())
	require.Eventually(t, func() bool { return manager.GetAccountManagementStatus().DKVSWriteAllowed }, 30*time.Second, 20*time.Millisecond)
	transport.armed.Store(true)
	require.NoError(t, manager.UpdateWalletName(root.ID, "B uploading"))
	select {
	case <-bHold.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("B backup did not submit")
	}
	b := waitForDKVSRecord(t, network.Core, key)
	require.Equal(t, a.Seq+1, b.Seq)
	require.NoError(t, manager.UpdateWalletName(root.ID, "C newest local"))
	bHold.unblock()
	select {
	case <-cHold.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("C backup was lost after B ACK")
	}
	require.Equal(t, "C newest local", manager.GetWalletCatalog()[0].Name)
	require.True(t, manager.GetAccountManagementStatus().ManagedDataDirty)
	require.Positive(t, manager.GetAccountManagementStatus().PendingChanges)
	remote := waitForDKVSRecord(t, network.Core, key)
	require.Equal(t, dkvs.RecordHash(b), dkvs.RecordHash(remote), "C is still held before the server")
	cHold.unblock()
	requireE2EFirstBackup(t, manager)
	c := waitForDKVSRecord(t, network.Core, key)
	require.Equal(t, b.Seq+1, c.Seq)
	require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
	require.False(t, manager.GetAccountManagementStatus().ManagedDataDirty)
	cold := accountFailureRestore(t, f)
	require.Equal(t, "C newest local", cold.GetWalletCatalog()[0].Name)
	// Deliberate no-op API regression, not a readiness/synchronization helper.
	for i := 0; i < 2; i++ {
		require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	}
	unchanged := waitForDKVSRecord(t, network.Core, key)
	require.Equal(t, dkvs.RecordHash(c), dkvs.RecordHash(unchanged), "read-only synchronization must not echo a revision")
}
