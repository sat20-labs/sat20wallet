package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

// Observe real DB results; never replace a value or error. Pause at the existing
// import-marker check after all three capture reads, before Update takes path locks.
type activationCaptureBarrierDB struct {
	indexer.KVDB
	wrapper, state, data string
	captured             []string
	mu                   sync.Mutex
	reads                int
	paused               bool
	reached, release     chan struct{}
}

func (d *activationCaptureBarrierDB) Read(key []byte) ([]byte, error) {
	value, err := d.KVDB.Read(key)
	if bytes.HasPrefix(key, []byte("dkvs:subscription-record:")) {
		d.mu.Lock()
		if bytes.HasSuffix(key, []byte(d.wrapper)) {
			d.reads++
			if d.reads == 2 {
				d.captured = append(d.captured, "wrapper")
			}
			// Update must remain unblocked: competitors use these same path locks.
		} else if d.reads == 2 {
			if bytes.HasSuffix(key, []byte(d.state)) {
				d.captured = append(d.captured, "state")
			}
			if bytes.HasSuffix(key, []byte(d.data)) {
				d.captured = append(d.captured, "data")
			}
		}
		d.mu.Unlock()
	}
	if bytes.Equal(key, accountManagedDataImportKey()) {
		d.mu.Lock()
		pause := len(d.captured) == 3 && !d.paused
		if pause {
			d.paused = true
		}
		d.mu.Unlock()
		if pause {
			close(d.reached)
			<-d.release
		}
	}
	return value, err
}

// Fail delivery before remote apply, retaining the production signed outbox.
type activationDeferredHTTP struct {
	*rgb11MemoryDKVSHTTP
	deferDelivery bool
}

func (h *activationDeferredHTTP) SendDKVSPost(path string, body []byte) ([]byte, error) {
	if h.deferDelivery && path == "/v3/dkvs/records/batch-cas" {
		return nil, context.DeadlineExceeded
	}
	return h.rgb11MemoryDKVSHTTP.SendDKVSPost(path, body)
}
func (h *activationDeferredHTTP) SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return h.SendDKVSPost(path, body)
}

func TestAccountActivationACKCaptureOrdering(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	for _, order := range []string{"ACK_before_capture", "ACK_after_capture", "existing_outbox_after_capture"} {
		t.Run(order, func(t *testing.T) {
			manager := newAccountManagementAutoTestManager(t)
			remote := newRGB11MemoryDKVSHTTP()
			transport := &activationDeferredHTTP{rgb11MemoryDKVSHTTP: remote}
			configureRGB11DKVSTestManager(manager, transport)
			client := newRGB11MessageNodeClient(remote)
			manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
			_, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password")
			require.NoError(t, err)
			require.NoError(t, manager.InitializeAccountManagement("password"))
			secret := manager.accountSecret
			managed, err := manager.buildAccountManagedDataSnapshot(secret, manager.accountProfile.AccountID, 1)
			require.NoError(t, err)
			state, err := account.OpenManagedState(secret, manager.accountProfile.AccountID, manager.accountProfile.StateEnvelope)
			require.NoError(t, err)
			state.DataRevision = managed.Bundle.Revision
			state.DataHash = managed.Hash
			envelope, err := account.SealManagedState(secret, manager.accountProfile.AccountID, state, nil)
			require.NoError(t, err)
			manager.accountProfile.StateEnvelope = envelope
			manager.accountProfile.StateHash = accountStateDigest(envelope)
			manager.accountProfile.ManagedDataEnvelope = managed.Envelope
			manager.accountProfile.ManagedDataHash = managed.Hash
			manager.accountProfile.ManagedDataRevision = managed.Bundle.Revision
			manager.accountProfile.RecordTTL = 144

			store, err := manager.accountDKVSStore()
			require.NoError(t, err)
			root, err := manager.accountManagementRootWallet()
			require.NoError(t, err)
			stateKey, err := manager.accountManagedStateKey(root)
			require.NoError(t, err)
			dataKey, err := manager.accountManagedDataBlobKey(root)
			require.NoError(t, err)
			wrapperKey, err := accountRootWrapperKey(root)
			require.NoError(t, err)
			_, err = manager.prepareAccountCoreNodeBinding(root, store)
			require.NoError(t, err)
			require.NoError(t, store.SyncCurrent(wrapperKey, stateKey, dataKey))
			_, err = store.Get(stateKey)
			require.ErrorIs(t, err, ErrDKVSRecordNotFound)
			pkg, auth, activationSecret := rereviewRecoveryPackage(t, manager)
			defer zeroBytes(activationSecret)
			previous := *manager.accountProfile
			publish := func() {
				mutations, err := accountManagementMutations(&previous, root, stateKey, previous.StateEnvelope, dataKey, previous.ManagedDataEnvelope, true)
				require.NoError(t, err)
				ack, err := store.PutBatch(mutations)
				if transport.deferDelivery {
					require.Error(t, err)
					entries, loadErr := newDKVSReplicaStore(manager.db).LoadOutbox(store.client.replicaNamespace)
					require.NoError(t, loadErr)
					require.Len(t, entries, 1)
					require.NotNil(t, entries[0].Authorization)
					t.Log("signed outbox persisted before activation capture; remote not applied")
					return
				}
				require.NoError(t, err)
				require.Len(t, ack, 2)
				require.Equal(t, previous.StateEnvelope, ack[0].Value)
				entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(store.client.replicaNamespace)
				require.NoError(t, err)
				require.Empty(t, entries)
				t.Log("real signed state/data ACK complete; outbox empty; previous state envelope identical")
			}
			barrier := &activationCaptureBarrierDB{KVDB: manager.db, wrapper: wrapperKey, state: stateKey, data: dataKey, reached: make(chan struct{}), release: make(chan struct{})}
			manager.db = barrier
			var release sync.Once
			unblock := func() { release.Do(func() { close(barrier.release) }) }
			defer unblock()
			if order == "ACK_before_capture" {
				publish()
			}
			if order == "existing_outbox_after_capture" {
				transport.deferDelivery = true
				publish()
			}
			done := make(chan error, 1)
			go func() {
				done <- manager.ActivateAccountManagement(activationSecret, "password", *auth, pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID)
			}()
			select {
			case <-barrier.reached:
				barrier.mu.Lock()
				observed := append([]string(nil), barrier.captured...)
				barrier.mu.Unlock()
				require.Equal(t, []string{"wrapper", "state", "data"}, observed)
				t.Log("complete wrapper/state/data capture verified; paused at post-capture import check before path locking")
			case err := <-done:
				t.Fatalf("activation ended before capture barrier: %v", err)
			case <-time.After(time.Minute):
				t.Fatal("capture barrier not reached")
			}
			if order == "ACK_after_capture" {
				publish()
			}
			if order == "existing_outbox_after_capture" {
				transport.deferDelivery = false
				submitted, err := manager.flushDKVSBatchOutbox(store.client, newDKVSReplicaStore(manager.db))
				require.NoError(t, err)
				require.True(t, submitted)
				entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(store.client.replicaNamespace)
				require.NoError(t, err)
				require.Empty(t, entries)
				t.Log("existing signed outbox ACK completed after capture; outbox empty")
			}

			unblock()
			select {
			case err := <-done:
				require.True(t, errors.Is(err, dkvs.ErrWriteConflict), "activation error: %v", err)
			case <-time.After(time.Minute):
				t.Fatal("activation did not finish")
			}
			require.Equal(t, previous.StateEnvelope, manager.accountProfile.StateEnvelope)
			remote.mu.Lock()
			defer remote.mu.Unlock()
			require.Nil(t, remote.records[wrapperKey], "activation wrapper must never be published")
			require.Equal(t, previous.StateEnvelope, remote.records[stateKey].Value)
			require.Equal(t, uint64(1), remote.records[stateKey].Seq)
			require.Equal(t, uint64(1), remote.records[dataKey].Seq)
			require.Len(t, remote.records, 3, "only state/data plus the normal account binding may exist")
		})
	}
}

// A signed current-wrapper update is deliberately separate from the ordinary
// atomic three-key account writer. This checks the lower-level accepted-write
// boundary; it must not be mistaken for a reproduced normal device workflow.
func TestAccountActivationRejectsNewOwnerSignedWrapperBaseline(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	manager, _, store, keys := accountTTLNoopFixture(t)
	previous := *manager.accountProfile
	pkg, _, secret := rereviewRecoveryPackage(t, manager)
	defer zeroBytes(secret)
	competing := previous
	competing.PackageID = pkg.Envelope.Locator.PackageID
	require.NotEqual(t, previous.PackageID, competing.PackageID)
	root, err := manager.accountManagementRootWallet()
	require.NoError(t, err)
	envelope, err := sealAccountRootWrapper(root, _chain, previous.AccountID, rootWrapperPayload(competing, secret), nil)
	require.NoError(t, err)
	mutation, err := accountRootWrapperMutation(&competing, root, keys[2], envelope)
	require.NoError(t, err)
	ack, err := store.Put(mutation)
	require.NoError(t, err)
	// This is exactly the baseline visibility effect under review, without
	// applying a patch/overlay to the production activation implementation.
	require.NoError(t, store.SyncCurrent(keys...))
	locator := account.Locator{AccountID: previous.AccountID, PackageID: previous.PackageID, RecoveryMode: previous.RecoveryMode}
	authorization := AccountStorageAuthorization{Mode: previous.StorageMode, RecordOptions: dkvs.RecordOptions{TTL: previous.RecordTTL}, Location: previous.Location}
	activationErr := manager.ActivateAccountManagement(secret, "password", authorization, locator, previous.PublicLocator)
	after, err := store.GetAuthoritative(keys[2])
	require.NoError(t, err)
	t.Logf("owner-signed wrapper-only boundary: activation_rejected=%t competing_hash_preserved=%t", activationErr != nil, after.Hash == ack.Hash)
	if activationErr == nil || after.Hash != ack.Hash {
		t.Fatal(fmt.Sprintf("new signed wrapper baseline replaced: activation_rejected=%t competing_hash_preserved=%t", activationErr != nil, after.Hash == ack.Hash))
	}
}
