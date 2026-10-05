package e2e

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// Actual SatoshiNet processes, registered Core/Bootstrap identities, Transcend
// BindAccount RPC, and the normal SDK. No test admission callback is installed
// in these nodes, and no production account or public chain is touched.
func TestSDKDKVSBoundWalletRPC(t *testing.T) {
	f := newTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, f.Network)
	core := dkvsClientForNode(t, f.Network.Core)
	bootstrap := dkvsClientForNode(t, f.Network.Bootstrap)
	miner := dkvsClientForNode(t, f.Network.Miner)
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "bound-real/value")
	require.NoError(t, err)
	var manager *wallet.Manager

	if !t.Run("UnboundWalletCannotWriteAnyNode", func(t *testing.T) {
		for _, client := range []*wallet.SatsNetDKVSClient{core, bootstrap, miner} {
			_, err := client.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("denied"),
				dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, client), TTL: 100})
			require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
			_, err = client.GetRecordDirect(key)
			require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		}
	}) { return }

	if !t.Run("ExplicitSDKBindingPrecedesGenericKVWrite", func(t *testing.T) {
		manager, _ = newWalletManagerForNode(t, f.Network.Core, dkvsClientMnemonic)
		require.NoError(t, manager.InitializeAccountManagement("123456"))
		require.Equal(t, owner.Wallet.GetPubKey().SerializeCompressed(), manager.GetWallet().GetPubKey().SerializeCompressed())
		require.NoError(t, manager.BindAccountToCurrentCoreNode())
		// Exact binding retry is supported by the dedicated service, without
		// performing an ordinary /account KV write first.
		require.NoError(t, manager.BindAccountToCurrentCoreNode())
		bindingKey, err := dkvs.AccountMappingKey("testnet", owner.Address)
		require.NoError(t, err)
		binding, err := core.GetRecordDirect(bindingKey)
		require.NoError(t, err)
		_, _, descriptor, err := dkvs.ValidateAccountMappingBindingRecord(binding)
		require.NoError(t, err)
		require.Equal(t, f.Network.Core.nodePubKey, descriptor.CoreNodeID)
		requireDKVSValue(t, f.Network.Bootstrap, bindingKey, binding.Value)
	}) { return }

	t.Run("BoundCoreAllowsCRUDWhileOtherNodesRemainReadOnlyForWallet", func(t *testing.T) {
		first, err := core.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("created"),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, core), TTL: 100})
		require.NoError(t, err)
		second, err := core.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("updated"),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, core), TTL: 100})
		require.NoError(t, err)
		require.Equal(t, first.Seq+1, second.Seq)
		renewed, err := core.RenewRecord(owner.Wallet, second,
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, core), TTL: 150})
		require.NoError(t, err)
		require.Equal(t, second.Seq+1, renewed.Seq)
		for _, other := range []*wallet.SatsNetDKVSClient{bootstrap, miner} {
			_, err := other.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("not-the-bound-core"),
				dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, other), TTL: 100})
			require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
			_, err = other.GetRecordDirect(key)
			require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		}
		_, err = core.DeleteCurrentRecord(owner.Wallet, key, sdkDKVSReviewHeight(t, core))
		require.NoError(t, err)
		state, err := core.GetKeyState(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
	})

	t.Run("LoopbackSnapshotImportCannotBypassCoreBinding", func(t *testing.T) {
		base, err := f.Network.Bootstrap.IndexerURL("testnet")
		require.NoError(t, err)
		for _, path := range []string{"/v3/dkvs/snapshot", "/v3/dkvs/prune", "/v3/dkvs/record"} {
			response, err := http.Post(strings.TrimRight(base, "/")+path, "application/json", bytes.NewBufferString("{}"))
			require.NoError(t, err)
			require.Equal(t, http.StatusNotFound, response.StatusCode)
			response.Body.Close()
		}
	})
}
