package e2e

import (
	"testing"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// P2P carries no generation/history. Identical signed bytes may represent a
// newly authorized lifecycle after deletion; the normal ordered realtime path
// therefore accepts create -> delete -> recreate without relying on RecordHash
// uniqueness across lifetimes.
func TestSDKDKVSGenerationIdenticalRecreation(t *testing.T) {
	p := newGenerationPeerPair(t)
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "p2p-identical-recreate/value")
	require.NoError(t, err)

	first, err := p.source.client.PutSignedRecordWithAutopay(
		owner.Wallet, key, []byte("identical"),
		dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay(),
	)
	require.NoError(t, err)
	p.receiver.OnNotify(p.notification(t, first))
	p.requireValue(t, first)

	deleted, err := p.source.client.DeleteCurrentRecord(owner.Wallet, key, 100)
	require.NoError(t, err)
	p.receiver.OnNotify(p.notification(t, deleted))
	_, err = p.target.client.GetRecord(key)
	require.ErrorIs(t, err, dkvs.ErrRecordNotFound)

	// A new wallet authorization recreates the exact same signed bytes.
	recreated, err := p.source.client.WithWriteSigner(owner.Wallet).PutRecordCAS(
		first, dkvs.WritePrecondition{ExpectAbsent: true},
	)
	require.NoError(t, err)
	require.Equal(t, dkvs.RecordHash(first), dkvs.RecordHash(recreated))
	p.receiver.OnNotify(p.notification(t, recreated))
	p.requireValue(t, recreated)
}
