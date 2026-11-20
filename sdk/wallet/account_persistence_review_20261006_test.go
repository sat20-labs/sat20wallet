package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
)

var errAccountPersistenceReview = errors.New("account review: injected profile persistence failure")

// Fault only the selected write, whether individual or transactional. All
// other keys delegate to the original DB; business uses public Manager APIs.
type accountPersistenceReviewDB struct {
	indexercommon.KVDB
	profileKey []byte
	flushOnly  bool
	armed      atomic.Bool
	hits       atomic.Int32
}

func (d *accountPersistenceReviewDB) Write(key, value []byte) error {
	if !d.flushOnly && d.armed.Load() && bytes.Equal(key, d.profileKey) {
		d.hits.Add(1)
		return errAccountPersistenceReview
	}
	return d.KVDB.Write(key, value)
}

func (d *accountPersistenceReviewDB) NewWriteBatch() indexercommon.WriteBatch {
	batch := d.KVDB.NewWriteBatch()
	if batch == nil {
		return nil
	}
	return &accountPersistenceReviewBatch{WriteBatch: batch, db: d}
}

type accountPersistenceReviewBatch struct {
	indexercommon.WriteBatch
	db      *accountPersistenceReviewDB
	matched bool
}

func (b *accountPersistenceReviewBatch) Put(key, value []byte) error {
	if b.db.armed.Load() && bytes.Equal(key, b.db.profileKey) {
		if b.db.flushOnly {
			b.matched = true
		} else {
			b.db.hits.Add(1)
			return errAccountPersistenceReview
		}
	}
	return b.WriteBatch.Put(key, value)
}

func (b *accountPersistenceReviewBatch) Flush() error {
	if b.db.flushOnly && b.db.armed.Load() && b.matched {
		b.db.hits.Add(1)
		return errAccountPersistenceReview
	}
	return b.WriteBatch.Flush()
}

func accountPersistenceReviewCatalog(t *testing.T, db indexercommon.KVDB) map[int64]WalletInDB {
	t.Helper()
	infos, err := loadAllWalletFromDB(db)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[int64]WalletInDB, len(infos))
	for id, info := range infos {
		out[id] = info.WalletInDB
	}
	return out
}

func TestAccountPersistenceFailureReview20261006(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	t.Run("RemoteApplyTransactionFailureCanRetry", func(t *testing.T) {
		first, second, remote := reviewAccountDevices(t)
		root := first.GetAccountManagementStatus().RootWalletID
		if err := first.UpdateWalletName(root, "Remote Confirmed Rename"); err != nil {
			t.Fatal(err)
		}
		if err := first.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		original := second.db
		// Wallet-module apply writes wallet records, not the account profile.
		// Target its actual atomic Put and require proof that the fault fired.
		key := getWalletDBKey(second.GetAccountManagementStatus().RootWalletID)
		fault := &accountPersistenceReviewDB{KVDB: original, profileKey: []byte(key)}
		before := second.GetWalletCatalog()
		durable := accountPersistenceReviewCatalog(t, original)
		fault.armed.Store(true)
		second.db = fault
		firstErr := second.SyncAccountManagementState(context.Background())
		fault.armed.Store(false)
		second.db = original
		if !errors.Is(firstErr, errAccountPersistenceReview) || !errors.Is(firstErr, ErrAccountDBStopped) || fault.hits.Load() != 1 {
			t.Fatalf("wallet apply fault must stop the database: err=%v hits=%d", firstErr, fault.hits.Load())
		}
		if !reflect.DeepEqual(before, second.GetWalletCatalog()) ||
			!reflect.DeepEqual(durable, accountPersistenceReviewCatalog(t, original)) {
			t.Fatal("failed wallet apply changed the live or durable catalog")
		}
		if !errors.Is(second.SyncAccountManagementState(context.Background()), ErrAccountDBStopped) {
			t.Fatal("restored storage must not revive the stopped wallet")
		}
		second.Close()
		restarted := NewManager(second.cfg, original)
		if restarted == nil {
			t.Fatal("reopen the original account database")
		}
		t.Cleanup(restarted.Close)
		restarted.SetDKVSHttpClient(remote)
		restarted.SetServerNodeHttpClient(newRGB11MessageNodeClient(remote))
		if _, err := restarted.UnlockWallet("password"); err != nil {
			t.Fatal(err)
		}
		retryErr := restarted.SyncAccountManagementState(context.Background())
		unchanged := reflect.DeepEqual(before, restarted.GetWalletCatalog())
		t.Logf("persistence review: first=%v hits=%d retry=%v old_catalog=%t", firstErr, fault.hits.Load(), retryErr, unchanged)
		if retryErr != nil {
			t.Fatalf("failed local apply must be retryable after restart: %v", retryErr)
		}
		fingerprint := first.GetAccountManagementStatus().RootFingerprint
		if entry := reviewCatalogWallet(t, restarted, fingerprint); entry == nil || entry.Name != "Remote Confirmed Rename" {
			t.Error("restarted device did not apply the remote wallet rename")
		}
	})
}
