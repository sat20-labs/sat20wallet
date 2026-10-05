package e2e

import (
	"encoding/hex"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Public SDK constructor -> public CAS -> real HTTP admission -> Indexer/Pebble.
// Only the node role and current account mapping are fixtures. No tombstone persistence,
// deletion receipt or historical sequence floor is needed by these scenarios.
func TestSDKDKVSDeleteCommand(t *testing.T) {
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	successor := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 1))
	fixture := func(t *testing.T) (*boundRPCFixture, *wire.DKVSRecord) {
		t.Helper()
		f := newBoundRPCFixture(t)
		f.bind(t, owner, f.coreID)
		key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "delete-constructor/value")
		require.NoError(t, err)
		current, err := f.client.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("current-value"),
			dkvs.RecordOptions{IssueHeight: 100, TTL: 10})
		require.NoError(t, err)
		return f, current
	}
	assertCurrent := func(t *testing.T, f *boundRPCFixture, want *wire.DKVSRecord) {
		t.Helper()
		actual, err := f.client.GetRecord(want.Key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(want), dkvs.RecordHash(actual))
	}

	t.Run("BoundWalletDeletesCurrentRecord", func(t *testing.T) {
		f, current := fixture(t)
		expected := dkvs.RecordHash(current)
		command, err := wallet.NewDKVSDeleteCommand(owner.Wallet, current, 100)
		require.NoError(t, err)
		_, err = f.client.WithWriteSigner(owner.Wallet).PutRecordCAS(command, dkvs.WritePrecondition{ExpectedHash: &expected})
		require.NoError(t, err, "a public SDK delete constructor must produce a valid current-state operation")
		target, err := dkvs.DeleteTargetHash(command)
		require.NoError(t, err)
		require.Equal(t, expected, target)
		require.Equal(t, expected, dkvs.RecordHash(current), "building a command must not mutate its input")
		require.Equal(t, current.Seq+1, command.Seq)
		require.Zero(t, command.TTL)
		require.NoError(t, dkvs.VerifySignature(command))
		state, err := f.client.GetKeyState(current.Key)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
		require.Zero(t, state.Seq)
		require.Empty(t, state.ETag)
		_, err = f.store.GetByHash(expected)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		_, err = f.store.GetForRelay(current.Key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		_, err = f.client.WithWriteSigner(owner.Wallet).PutRecordCAS(command, dkvs.WritePrecondition{ExpectedHash: &expected})
		require.NoError(t, err, "an exact retry can confirm current absence without retaining history")
		require.Zero(t, f.unguarded.Load())
	})

	t.Run("DelayedDeleteCannotRemoveSameBlockRecreation", func(t *testing.T) {
		f, current := fixture(t)
		command, err := wallet.NewDKVSDeleteCommand(owner.Wallet, current, 100)
		require.NoError(t, err)
		expected := dkvs.RecordHash(current)
		_, err = f.client.WithWriteSigner(owner.Wallet).PutRecordCAS(command, dkvs.WritePrecondition{ExpectedHash: &expected})
		require.NoError(t, err)
		fresh, err := f.client.PutSignedRecordFreeLocal(owner.Wallet, current.Key, []byte("new-incarnation"),
			dkvs.RecordOptions{IssueHeight: 100, TTL: 10})
		require.NoError(t, err)
		require.Equal(t, current.Seq, fresh.Seq)
		require.Equal(t, current.IssueHeight, fresh.IssueHeight)
		_, err = f.client.WithWriteSigner(owner.Wallet).PutRecordCAS(command, dkvs.WritePrecondition{ExpectedHash: &expected})
		require.ErrorIs(t, err, dkvs.ErrWriteConflict)
		assertCurrent(t, f, fresh)
	})

	t.Run("BindingMustStillPointToThisCoreAtSubmission", func(t *testing.T) {
		f, current := fixture(t)
		command, err := wallet.NewDKVSDeleteCommand(owner.Wallet, current, 100)
		require.NoError(t, err)
		// Move the current mapping to another CoreNode before submission.
		f.bind(t, owner, hex.EncodeToString(successor.Wallet.GetPubKey().SerializeCompressed()))
		expected := dkvs.RecordHash(current)
		_, err = f.client.WithWriteSigner(owner.Wallet).PutRecordCAS(command, dkvs.WritePrecondition{ExpectedHash: &expected})
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
		assertCurrent(t, f, current)
	})

	t.Run("AnotherBoundWalletCannotDeletePersonalData", func(t *testing.T) {
		f, current := fixture(t)
		f.bind(t, successor, f.coreID)
		command, err := wallet.NewDKVSDeleteCommand(successor.Wallet, current, 100)
		require.NoError(t, err)
		expected := dkvs.RecordHash(current)
		_, err = f.client.WithWriteSigner(successor.Wallet).PutRecordCAS(command, dkvs.WritePrecondition{ExpectedHash: &expected})
		require.Error(t, err)
		assertCurrent(t, f, current)
	})

	t.Run("TargetHashIsCoveredBySignature", func(t *testing.T) {
		f, current := fixture(t)
		command, err := wallet.NewDKVSDeleteCommand(owner.Wallet, current, 100)
		require.NoError(t, err)
		command.Value[0] ^= 1
		target, err := dkvs.DeleteTargetHash(command)
		require.NoError(t, err)
		_, err = f.client.WithWriteSigner(owner.Wallet).PutRecordCAS(command, dkvs.WritePrecondition{ExpectedHash: &target})
		require.Error(t, err)
		assertCurrent(t, f, current)
	})

	t.Run("ConflictingBatchDeletesNothing", func(t *testing.T) {
		f, first := fixture(t)
		secondKey, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "delete-constructor/second")
		require.NoError(t, err)
		second, err := f.client.PutSignedRecordFreeLocal(owner.Wallet, secondKey, []byte("second-v1"),
			dkvs.RecordOptions{IssueHeight: 100, TTL: 10})
		require.NoError(t, err)
		deleteFirst, err := wallet.NewDKVSDeleteCommand(owner.Wallet, first, 100)
		require.NoError(t, err)
		deleteSecond, err := wallet.NewDKVSDeleteCommand(owner.Wallet, second, 100)
		require.NoError(t, err)
		newSecond, err := f.client.PutSignedRecordFreeLocal(owner.Wallet, secondKey, []byte("second-v2"),
			dkvs.RecordOptions{IssueHeight: 100, TTL: 10})
		require.NoError(t, err)
		_, err = f.client.WithWriteSigner(owner.Wallet).PutRecordBatchCAS([]dkvs.CASMutation{
			sdkDKVSReviewExpected(deleteFirst, first), sdkDKVSReviewExpected(deleteSecond, second),
		})
		require.ErrorIs(t, err, dkvs.ErrWriteConflict)
		assertCurrent(t, f, first)
		assertCurrent(t, f, newSecond)
	})

	t.Run("DIDSuccessorSignsDeleteWithoutChangingHistoricalRecord", func(t *testing.T) {
		f := newBoundRPCFixture(t)
		f.bind(t, owner, f.coreID)
		f.bind(t, successor, f.coreID)
		const service = "deletecmd.btc"
		setOwner := func(actor *dkvsKeyPathActor) {
			f.store.SetResolver(dkvs.StaticDIDResolver{Services: map[string]dkvs.DIDIdentity{
				service: {CanonicalName: service, NameID: service,
					SigningKeys: [][]byte{actor.Wallet.GetPubKey().SerializeCompressed()}, Active: true},
			}})
		}
		setOwner(owner)
		key, err := dkvs.ServiceKey(service, "config")
		require.NoError(t, err)
		current, err := f.client.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("historical-config"),
			dkvs.RecordOptions{IssueHeight: 100, TTL: 10})
		require.NoError(t, err)
		expected := dkvs.RecordHash(current)
		setOwner(successor)
		oldCommand, err := wallet.NewDKVSDeleteCommand(owner.Wallet, current, 100)
		require.NoError(t, err)
		_, err = f.client.WithWriteSigner(owner.Wallet).PutRecordCAS(oldCommand, dkvs.WritePrecondition{ExpectedHash: &expected})
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
		newCommand, err := wallet.NewDKVSDeleteCommand(successor.Wallet, current, 100)
		require.NoError(t, err)
		require.Equal(t, expected, dkvs.RecordHash(current))
		require.Equal(t, successor.Wallet.GetPubKey().SerializeCompressed(), newCommand.PubKey)
		_, err = f.client.WithWriteSigner(successor.Wallet).PutRecordCAS(newCommand, dkvs.WritePrecondition{ExpectedHash: &expected})
		require.NoError(t, err)
		_, err = f.client.GetRecord(key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
	})

	t.Run("InvalidBuilderInputsFailWithoutPanic", func(t *testing.T) {
		_, err := wallet.NewDKVSDeleteCommand(owner.Wallet, nil, 100)
		require.Error(t, err)
		_, current := fixture(t)
		_, err = wallet.NewDKVSDeleteCommand(nil, current, 100)
		require.Error(t, err)
		command, err := wallet.NewDKVSDeleteCommand(owner.Wallet, current, 100)
		require.NoError(t, err)
		_, err = wallet.NewDKVSDeleteCommand(owner.Wallet, command, 100)
		require.Error(t, err)
		copyRecord := *current
		copyRecord.Seq = ^uint64(0)
		_, err = wallet.NewDKVSDeleteCommand(owner.Wallet, &copyRecord, 100)
		require.Error(t, err)
	})
}
