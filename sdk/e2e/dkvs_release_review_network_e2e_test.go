package e2e

import (
	"testing"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// Real isolated processes, bound CoreNode RPC and P2P. Initial funding and L1
// ownership are fixtures. Peers are read-only from the wallet's point of view.
func TestSDKDKVSReleaseReviewNetwork(t *testing.T) {
	f, owner, successor, payment := sdkDKVSReviewPaidFixture(t)
	peer := dkvsClientForNode(t, f.Network.Bootstrap)
	core := dkvsClientForNode(t, f.Network.Core)

	t.Run("LocalDeleteThenPaidReplicationRemainsWritable", func(t *testing.T) {
		key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "release-net-delete/value")
		require.NoError(t, err)
		local := sdkDKVSReviewPutFree(t, core, owner, key, []byte("local-before-upgrade"))
		require.Equal(t, uint64(1), local.Seq)
		deleted, err := core.DeleteCurrentRecord(owner.Wallet, key, sdkDKVSReviewHeight(t, core))
		require.NoError(t, err)
		require.Equal(t, uint64(2), deleted.Seq)
		_, err = peer.GetRecord(key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		absent, err := core.GetKeyState(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, absent.Status)
		paid, err := core.PutSignedRecordWithAutopay(owner.Wallet, key, []byte("canonical-paid-v1"),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, core)}, payment)
		require.NoError(t, err)
		require.Equal(t, uint64(1), paid.Seq)
		requireDKVSValue(t, f.Network.Bootstrap, key, paid.Value)
		state, err := core.GetKeyState(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateActive, state.Status)
		require.Equal(t, dkvs.StorageModeAutopay, state.StorageMode)
		require.Equal(t, paid.Seq, state.Seq)
		updated, err := core.PutSignedRecordWithAutopay(owner.Wallet, key, []byte("canonical-paid-v2"),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, core)}, payment)
		require.NoError(t, err)
		require.Equal(t, state.Seq+1, updated.Seq)
		requireDKVSValue(t, f.Network.Bootstrap, key, updated.Value)
		_, err = peer.PutSignedRecordWithAutopay(owner.Wallet, key, []byte("wrong-entry-point"),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, peer)}, payment)
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
		sdkDKVSReviewUnchanged(t, core, updated)
	})

	t.Run("ReceivingWalletResignsDIDThenSingleKeyUpdatesReplicate", func(t *testing.T) {
		const service = "release-net.btc"
		f.NetworkFakeL1().setNameOwner(service, owner.Address)
		x, err := dkvs.ServiceKey(service, "config/x")
		require.NoError(t, err)
		y, err := dkvs.ServiceKey(service, "config/y")
		require.NoError(t, err)
		for _, key := range []string{x, y} {
			r, err := core.PutSignedRecordWithAutopay(owner.Wallet, key, []byte("old-owner-value"),
				dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, core)}, payment)
			require.NoError(t, err)
			requireDKVSValue(t, f.Network.Bootstrap, key, r.Value)
		}
		f.NetworkFakeL1().setNameOwner(service, successor.Address)
		_, err = core.PutSignedRecordWithAutopay(owner.Wallet, x, []byte("revoked-writer"),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, core)}, payment)
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
		resigned, err := core.ResignDIDRecords(successor.Wallet, service)
		require.NoError(t, err)
		require.Len(t, resigned, 2)
		for _, r := range resigned {
			require.Equal(t, successor.Wallet.GetPubKey().SerializeCompressed(), r.PubKey)
			require.Equal(t, []byte("old-owner-value"), r.Value)
			require.Equal(t, uint64(2), r.Seq)
			requireDKVSValue(t, f.Network.Bootstrap, r.Key, r.Value)
		}
		updatedX, err := core.PutSignedRecordWithAutopay(successor.Wallet, x, []byte("new-owner-x"),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, core)}, payment)
		require.NoError(t, err)
		requireDKVSValue(t, f.Network.Bootstrap, x, updatedX.Value)
		requireDKVSValue(t, f.Network.Bootstrap, y, []byte("old-owner-value"))
		for _, r := range resigned {
			if r.Key == y { sdkDKVSReviewUnchanged(t, peer, r) }
		}
	})
}
