package e2e

import (
	"testing"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Public SDK -> real HTTP/router/admission -> real Indexer. Internal mailbox
// delivery is a fixture for MessageManager's already-authenticated write; no
// ordinary RPC is allowed to manufacture an unsigned mailbox record.
func TestSDKDKVSActiveApplications(t *testing.T) {
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	account := dkvs.AccountID(owner.Wallet.GetPubKey().SerializeCompressed())
	seedTopic := func(t *testing.T, f *boundRPCFixture, message string) *wire.DKVSRecord {
		t.Helper()
		key, err := dkvs.MailTopicMessageKey(account, "developers", account, message)
		require.NoError(t, err)
		record := &wire.DKVSRecord{Version: dkvs.Version, Key: key, Value: []byte("authenticated encrypted topic delivery"), Seq: 1, IssueHeight: f.height.Load(), TTL: 100}
		applied, err := f.store.PutInternalMailbox(record)
		require.NoError(t, err)
		require.True(t, applied)
		return record
	}

	t.Run("TopicMailboxDoesNotBlockUnrelatedSettingsCAS", func(t *testing.T) {
		f := newBoundRPCFixture(t)
		f.bind(t, owner, f.coreID)
		seedTopic(t, f, "message-1")
		key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "application/settings")
		require.NoError(t, err)
		first, err := f.client.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("one"), dkvs.RecordOptions{IssueHeight: 100, TTL: 100})
		require.NoError(t, err, "a legitimate topic message must not poison unrelated wallet KV writes")
		second, err := f.client.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("two"), dkvs.RecordOptions{IssueHeight: 100, TTL: 100})
		require.NoError(t, err)
		require.Equal(t, first.Seq+1, second.Seq)
	})

	t.Run("MailboxDeletionPreservesOtherTopicDeliveries", func(t *testing.T) {
		f := newBoundRPCFixture(t)
		f.bind(t, owner, f.coreID)
		one := seedTopic(t, f, "message-1")
		two := seedTopic(t, f, "message-2")
		command, err := f.client.DeleteCurrentRecord(owner.Wallet, one.Key, 100)
		require.NoError(t, err)
		target, err := dkvs.DeleteTargetHash(command)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(one), target)
		_, err = f.client.GetRecord(one.Key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		remaining, err := f.client.GetRecord(two.Key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(two), dkvs.RecordHash(remaining))
	})

	t.Run("TopicKeyPackageDoesNotBlockRecoveryData", func(t *testing.T) {
		f := newBoundRPCFixture(t)
		f.bind(t, owner, f.coreID)
		key, err := dkvs.MailTopicKeyKey(account, "developers", "1")
		require.NoError(t, err)
		_, err = f.store.PutInternalMailbox(&wire.DKVSRecord{Version: dkvs.Version, Key: key, Value: []byte("encrypted key package"), Seq: 1, IssueHeight: 100, TTL: 100})
		require.NoError(t, err)
		_, err = f.client.PutPersonalRecordFreeLocal(owner.Wallet, "account/recovery/test", []byte("encrypted recovery data"), dkvs.RecordOptions{IssueHeight: 100, TTL: 100})
		require.NoError(t, err)
	})
}
