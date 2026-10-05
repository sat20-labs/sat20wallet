package e2e

import (
	"testing"
	"time"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestSDKDKVSActiveManager(t *testing.T) {
	fixture := newTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	remote := dkvsClientForNode(t, fixture.Network.Core)
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	manager, _ := newWalletManagerForNode(t, fixture.Network.Core, dkvsClientMnemonic)
	require.NoError(t, manager.InitializeAccountManagement("123456"))
	require.NoError(t, manager.BindAccountToCurrentCoreNode())
	key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "active-manager/settings")
	require.NoError(t, err)
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	sdkDKVSReviewPutFree(t, remote, owner, key, []byte("initial"))
	client, err := manager.GetDKVSClient()
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.StartDKVSSync())
	t.Cleanup(manager.StopDKVSSync)
	readValue := func(value string) bool {
		state, err := manager.GetDKVSSubscriptionStatus()
		if err != nil || state.Status != "READY" { return false }
		record, err := client.GetRecord(key)
		return err == nil && string(record.Value) == value
	}

	t.Run("StartupInstallsConfirmedReplica", func(t *testing.T) {
		require.Eventually(t, func() bool { return readValue("initial") }, 8*time.Second, 30*time.Millisecond)
	})
	t.Run("RemoteUpdateArrivesWithoutPollingMinute", func(t *testing.T) {
		sdkDKVSReviewPutFree(t, remote, owner, key, []byte("pushed"))
		require.Eventually(t, func() bool { return readValue("pushed") }, 6*time.Second, 30*time.Millisecond)
	})
	t.Run("ForegroundWriteDoesNotWaitForLongPoll", func(t *testing.T) {
		started := time.Now()
		_, err := client.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("foreground"),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, remote), TTL: 600})
		require.NoError(t, err)
		require.Less(t, time.Since(started), 6*time.Second)
		require.Eventually(t, func() bool { return readValue("foreground") }, 6*time.Second, 30*time.Millisecond)
	})
	t.Run("SuspendDeleteResumeAndRecreate", func(t *testing.T) {
		started := time.Now()
		manager.StopDKVSSync()
		require.Less(t, time.Since(started), 3*time.Second, "stopping must cancel an idle long poll")
		_, err := remote.DeleteCurrentRecord(owner.Wallet, key, sdkDKVSReviewHeight(t, remote))
		require.NoError(t, err)
		require.NoError(t, manager.StartDKVSSync())
		require.Eventually(t, func() bool {
			state, err := manager.GetDKVSSubscriptionStatus()
			if err != nil || state.Status != "READY" { return false }
			_, err = client.GetRecord(key)
			return err != nil
		}, 6*time.Second, 30*time.Millisecond)
		fresh := sdkDKVSReviewPutFree(t, remote, owner, key, []byte("recreated"))
		require.Equal(t, uint64(1), fresh.Seq)
		require.Eventually(t, func() bool { return readValue("recreated") }, 6*time.Second, 30*time.Millisecond)
	})
	t.Run("ExistingDeleteAPILeavesNoLocalFloor", func(t *testing.T) {
		_, err := client.TombstoneSigned(owner.Wallet, key, dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, remote)})
		require.NoError(t, err)
		state, err := remote.GetKeyState(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
		// The delete ACK confirms the request only. Wait until active sync makes
		// the absence authoritative in the local replica before starting a new
		// key lifetime from Seq=1.
		require.Eventually(t, func() bool {
			status, statusErr := manager.GetDKVSSubscriptionStatus()
			if statusErr != nil || status.Status != "READY" { return false }
			_, readErr := client.GetRecord(key)
			return readErr != nil
		}, 6*time.Second, 30*time.Millisecond)
		fresh, err := client.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("after-own-delete"),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, remote), TTL: 600})
		require.NoError(t, err)
		require.Equal(t, uint64(1), fresh.Seq)
		require.Eventually(t, func() bool { return readValue("after-own-delete") }, 6*time.Second, 30*time.Millisecond)
	})
}
