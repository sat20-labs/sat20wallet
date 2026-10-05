package e2e

import (
	"net/url"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// An external SDK client has HTTP access but no wallet/private key. The only
// signature it obtains is the already public signed record returned by GET.
func TestSDKDKVSLaunchReviewRPCReplay(t *testing.T) {
	setup := func(t *testing.T) (*boundRPCFixture, *dkvsKeyPathActor, *wallet.SatsNetDKVSClient, *wire.DKVSRecord) {
		t.Helper()
		owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
		f := newBoundRPCFixture(t)
		f.bind(t, owner, f.coreID)
		key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "launch-rpc-replay/value")
		require.NoError(t, err)
		_, err = f.client.PutSignedRecordWithAutopay(owner.Wallet, key, []byte("previously-published-value"),
			dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
		require.NoError(t, err)
		u, err := url.Parse(f.server.URL)
		require.NoError(t, err)
		outsider := wallet.NewSatsNetDKVSClient(u.Scheme, u.Host, "testnet", &wallet.NetClient{Client: f.server.Client()})
		published, err := outsider.GetRecord(key)
		require.NoError(t, err)
		require.NoError(t, dkvs.VerifySignature(published))
		require.Zero(t, published.TTL)
		return f, owner, outsider, published
	}

	t.Run("ControlTamperedPublicRecordIsRejected", func(t *testing.T) {
		f, _, outsider, published := setup(t)
		originalHash := dkvs.RecordHash(published)
		published.Value = []byte("not-signed-by-owner")
		_, err := outsider.PutRecordCAS(published, dkvs.WritePrecondition{ExpectedHash: &originalHash})
		require.Error(t, err)
		actual, err := f.client.GetRecord(published.Key)
		require.NoError(t, err)
		require.Equal(t, originalHash, dkvs.RecordHash(actual))
	})

	t.Run("PublicHistoricalSignatureCannotAuthorizeRecreation", func(t *testing.T) {
		f, owner, outsider, published := setup(t)
		_, err := f.client.DeleteCurrentRecord(owner.Wallet, published.Key, 100)
		require.NoError(t, err)
		_, err = outsider.GetRecord(published.Key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		// No re-signing, wallet handle, key material or privileged RPC is used.
		// ExpectAbsent is placed on the request by the outsider, not the owner.
		_, replayErr := outsider.PutRecordCAS(published, dkvs.WritePrecondition{ExpectAbsent: true})
		actual, readErr := f.client.GetRecord(published.Key)
		t.Logf("launch-review: public-record replay error=%v read_error=%v restored_record=%+v", replayErr, readErr, actual)
		require.Zero(t, f.unguarded.Load(), "the test must pass through real wallet RPC admission")
		require.Error(t, replayErr, "a published record signature is not a fresh authorization to recreate deleted data")
		require.ErrorIs(t, readErr, dkvs.ErrRecordNotFound)
	})
}
