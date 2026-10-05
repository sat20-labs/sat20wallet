package e2e

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real SatoshiNet Core/Bootstrap/Miner processes and the production Transcend
// BindAccount service. No custom wallet-admission callback is installed. The
// external client has only the public HTTP address and a published signature;
// it never receives a wallet handle, seed or signing capability.
func TestSDKDKVSLaunchBoundReplay(t *testing.T) {
	f := newTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, f.Network)
	core := dkvsClientForNode(t, f.Network.Core)
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	manager, _ := newWalletManagerForNode(t, f.Network.Core, dkvsClientMnemonic)
	require.NoError(t, manager.InitializeAccountManagement("123456"))
	require.NoError(t, manager.BindAccountToCurrentCoreNode())
	key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "launch-bound-replay/value")
	require.NoError(t, err)
	policy, err := core.GetFreeLocalCachePolicy()
	require.NoError(t, err)
	require.NotNil(t, policy)
	require.True(t, policy.Enabled)
	require.NotZero(t, policy.MaxTTL)
	ttl := uint64(100)
	if policy.MaxTTL < ttl {
		ttl = policy.MaxTTL
	}
	created, err := core.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("published-original"),
		dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, core), TTL: ttl})
	require.NoError(t, err)

	address, err := f.Network.Core.IndexerURL("testnet")
	require.NoError(t, err)
	u, err := url.Parse(address)
	require.NoError(t, err)
	external := wallet.NewSatsNetDKVSClient(u.Scheme, u.Host, "testnet",
		&wallet.NetClient{Client: &http.Client{Timeout: 15 * time.Second}})
	published, err := external.GetRecordDirect(key)
	require.NoError(t, err)
	require.Equal(t, dkvs.RecordHash(created), dkvs.RecordHash(published))
	require.NoError(t, dkvs.VerifySignature(published))
	originalHash := dkvs.RecordHash(published)

	t.Run("ControlTamperingWithoutPrivateKeyIsRejected", func(t *testing.T) {
		altered := *published
		altered.Value = []byte("not-authorized-by-owner")
		_, err := external.PutRecordCAS(&altered, dkvs.WritePrecondition{ExpectedHash: &originalHash})
		require.Error(t, err)
		actual, err := core.GetRecordDirect(key)
		require.NoError(t, err)
		require.Equal(t, originalHash, dkvs.RecordHash(actual))
	})

	t.Run("ControlNonBoundNodeRejectsPublishedRecord", func(t *testing.T) {
		other := dkvsClientForNode(t, f.Network.Bootstrap)
		_, err := other.PutRecordCAS(published, dkvs.WritePrecondition{ExpectAbsent: true})
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
	})

	t.Run("HistoricalPublicSignatureCannotRecreateAfterOwnerDeletion", func(t *testing.T) {
		_, err := core.DeleteCurrentRecord(owner.Wallet, key, sdkDKVSReviewHeight(t, core))
		require.NoError(t, err)
		_, err = external.GetRecordDirect(key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		// No signature is generated here. Even on the correct bound CoreNode,
		// a stored record's signature must not be a new RPC authorization.
		_, replayErr := external.PutRecordCAS(published, dkvs.WritePrecondition{ExpectAbsent: true})
		actual, readErr := core.GetRecordDirect(key)
		restored := readErr == nil && actual != nil && dkvs.RecordHash(actual) == originalHash
		t.Logf("launch-review: real-node public-signature replay error=%v read_error=%v restored_original=%t",
			replayErr, readErr, restored)
		assert.Error(t, replayErr, "a client without the owner's private key recreated deleted data via the actual bound CoreNode")
		assert.ErrorIs(t, readErr, dkvs.ErrRecordNotFound, "deleted data became active again")
	})
}
