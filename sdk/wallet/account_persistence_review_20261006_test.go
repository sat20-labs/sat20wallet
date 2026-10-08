package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
)

var errAccountPersistenceReview = errors.New("account review: injected profile persistence failure")

// Fault only the profile write, whether the implementation uses individual
// writes or a transaction. Wallet keys and the original database are untouched
// by the fixture; all business mutations enter the public Manager interfaces.
type accountPersistenceReviewDB struct {
	indexercommon.KVDB
	profileKey []byte
	armed      atomic.Bool
	hits       atomic.Int32
}

func (d *accountPersistenceReviewDB) Write(key, value []byte) error {
	if d.armed.Load() && bytes.Equal(key, d.profileKey) {
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
	db *accountPersistenceReviewDB
}

func (b *accountPersistenceReviewBatch) Put(key, value []byte) error {
	if b.db.armed.Load() && bytes.Equal(key, b.db.profileKey) {
		b.db.hits.Add(1)
		return errAccountPersistenceReview
	}
	return b.WriteBatch.Put(key, value)
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
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate review test")
	}
	dir := filepath.Dir(source)
	hashes := map[string]string{}
	for _, name := range []string{"interface.go", "db.go", "wallet_catalog.go", "account_management_state.go", "account_managed_data_sync.go"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		hashes[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	var observations []map[string]any
	t.Cleanup(func() {
		var changed []string
		for name, before := range hashes {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != before {
				changed = append(changed, name)
			}
		}
		report := map[string]any{"test": t.Name(), "date": time.Now().Format(time.RFC3339), "passed": !t.Failed(), "observations": observations, "source_sha256": hashes, "changed_sources": changed}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		outDir := filepath.Join(filepath.Dir(dir), "review-evidence")
		if err := os.MkdirAll(outDir, 0700); err != nil {
			t.Error(err)
			return
		}
		path := filepath.Join(outDir, "account-persistence-rereview-20261006.json")
		if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Error(err)
		}
		t.Logf("review evidence: %s", path)
		if len(changed) != 0 {
			t.Errorf("source changed during review: %v", changed)
		}
	})

	for _, scenario := range []struct {
		name   string
		mutate func(*Manager, int64) error
	}{
		{"Rename", func(m *Manager, id int64) error { return m.UpdateWalletName(id, "Rejected Rename") }},
		{"EnsureAccount", func(m *Manager, id int64) error { return m.EnsureAccount(id, 2, "Rejected Account", "did:rejected") }},
		{"AccountMetadata", func(m *Manager, id int64) error {
			return m.UpdateAccountMetadata(id, 0, "Rejected Metadata", "did:rejected")
		}},
		{"CreateWallet", func(m *Manager, _ int64) error { _, _, err := m.CreateWallet("password"); return err }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			manager, _, _ := reviewAccountDevices(t)
			root := manager.GetAccountManagementStatus().RootWalletID
			before := manager.GetWalletCatalog()
			status := manager.GetAccountManagementStatus()
			original := manager.db
			durableBefore := accountPersistenceReviewCatalog(t, original)
			fault := &accountPersistenceReviewDB{KVDB: original, profileKey: append([]byte(nil), accountManagementProfileKey()...)}
			fault.armed.Store(true)
			manager.db = fault
			defer func() { manager.db = original }()
			err := scenario.mutate(manager, root)
			fault.armed.Store(false)
			manager.db = original
			catalogChanged := !reflect.DeepEqual(before, manager.GetWalletCatalog())
			durableChanged := !reflect.DeepEqual(durableBefore, accountPersistenceReviewCatalog(t, original))
			observations = append(observations, map[string]any{"scenario": scenario.name, "error": fmt.Sprint(err), "fault_hits": fault.hits.Load(), "catalog_changed": catalogChanged, "durable_catalog_changed": durableChanged, "pending_before": status.PendingChanges, "pending_after": manager.GetAccountManagementStatus().PendingChanges})
			t.Logf("persistence review: operation=%s error=%v hits=%d catalog_changed=%t durable_changed=%t pending=%d", scenario.name, err, fault.hits.Load(), catalogChanged, durableChanged, manager.GetAccountManagementStatus().PendingChanges)
			if fault.hits.Load() == 0 {
				t.Fatal("profile fault was not exercised")
			}
			if !errors.Is(err, errAccountPersistenceReview) {
				t.Error("profile write failure must be returned by the public operation")
			}
			if catalogChanged || durableChanged {
				t.Error("failed account mutation committed a partial wallet catalog")
			}
		})
	}

	t.Run("RemoteApplyTransactionFailureCanRetry", func(t *testing.T) {
		first, second, _ := reviewAccountDevices(t)
		root := first.GetAccountManagementStatus().RootWalletID
		if err := first.UpdateWalletName(root, "Remote Confirmed Rename"); err != nil {
			t.Fatal(err)
		}
		if err := first.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		original := second.db
		fault := &accountPersistenceReviewDB{KVDB: original, profileKey: append([]byte(nil), accountManagementProfileKey()...)}
		before := second.GetWalletCatalog()
		fault.armed.Store(true)
		second.db = fault
		defer func() { second.db = original }()
		firstErr := second.SyncAccountManagementState(context.Background())
		fault.armed.Store(false)
		second.db = original
		marker, markerErr := second.readAccountManagedDataImportMarker()
		if markerErr != nil {
			t.Fatal(markerErr)
		}
		markerOrigin := ""
		if marker != nil {
			markerOrigin = marker.Origin
		}
		retryErr := second.SyncAccountManagementState(context.Background())
		unchanged := reflect.DeepEqual(before, second.GetWalletCatalog())
		observations = append(observations, map[string]any{"scenario": "RemoteApplyTransactionFailureCanRetry", "first_error": fmt.Sprint(firstErr), "fault_hits": fault.hits.Load(), "marker_origin": markerOrigin, "retry_error": fmt.Sprint(retryErr), "catalog_still_old": unchanged})
		t.Logf("persistence review: first=%v hits=%d marker=%s retry=%v old_catalog=%t", firstErr, fault.hits.Load(), markerOrigin, retryErr, unchanged)
		if !errors.Is(firstErr, errAccountPersistenceReview) || fault.hits.Load() == 0 {
			t.Fatal("test did not reach account apply transaction failure")
		}
		if retryErr != nil {
			t.Errorf("failed local apply must be retryable after persistence recovers: %v", retryErr)
		}
	})
}
