package wallet

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func coreDKVSE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain) {
	a := coreNewE2EManager(t, cfg, chain, coreE2ESenderMnemonic)
	b := coreNewE2EManager(t, cfg, chain, coreE2ESenderMnemonic)
	ca, err := a.ensureDKVSManager().primaryClient()
	coreRequire(t, "create replica A", err)
	cb, err := b.ensureDKVSManager().primaryClient()
	coreRequire(t, "create replica B", err)
	root, err := a.accountManagementRootWallet()
	coreRequire(t, "DKVS root signer", err)
	prefix, err := dkvsindexer.PersonalKey(root.GetPubKey().SerializeCompressed(), "e2e-core")
	coreRequire(t, "canonical directory prefix", err)
	sibling := prefix + "-sibling/item"
	keys := []string{prefix + "/one", prefix + "/sub/two", prefix + "/three"}
	policy := DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet(), PoolContract: cfg.Contract}
	for i, key := range append(append([]string(nil), keys...), sibling) {
		_, err := ca.PutSignedRecordWithAutopay(root, key, []byte(fmt.Sprintf("initial-%d", i)), dkvsindexer.RecordOptions{}, policy)
		coreRequire(t, "paid DKVS create", err)
	}
	// Own-key ACKs do not establish a complete directory baseline.
	_, _, err = ca.SubscribePrefix(prefix)
	coreRequire(t, "replica A initial current-set snapshot", err)
	_, _, err = cb.SubscribePrefix(prefix)
	coreRequire(t, "replica B initial current-set snapshot", err)
	records, total, err := cb.ListRecords(prefix, 0, 0)
	coreRequire(t, "read local directory", err)
	coreAssert(t, total == 3 && len(records) == 3, "prefix boundary leaked sibling or omitted nested key")
	old, err := cb.GetRecord(keys[0])
	coreRequire(t, "capture pre-update record", err)
	_, err = cb.PutSignedRecordWithAutopay(b.GetWallet(), keys[0], []byte("updated-by-B"), dkvsindexer.RecordOptions{}, policy)
	coreRequire(t, "same root writes on instance B", err)
	storeA, err := a.accountDKVSStore()
	coreRequire(t, "A replica store", err)
	coreRequire(t, "A sync current source generation", storeA.SyncCurrent(keys...))
	actual, err := ca.GetRecord(keys[0])
	coreRequire(t, "A reads B update locally", err)
	coreAssert(t, string(actual.Value) == "updated-by-B" && actual.Seq > old.Seq, "cross-instance update did not advance local state")

	_, err = cb.DeleteCurrentRecord(b.GetWallet(), keys[1], 0)
	coreRequire(t, "B physically deletes nested record", err)
	coreRequire(t, "A reconcile missing key", storeA.SyncCurrent(keys...))
	t.Run("delete_propagates_without_stale_live_value", func(t *testing.T) {
		got, err := ca.GetRecord(keys[1])
		coreAssert(t, errors.Is(err, ErrDKVSRecordNotFound) && got == nil, "deleted value or synthetic deletion record remains readable")
		state, err := cb.GetKeyState(keys[1])
		coreRequire(t, "read current absent state", err)
		coreAssert(t, state.Status == dkvsindexer.KeyStateNeverSeen && state.Seq == 0 && state.ETag == "", "deleted key retained a sequence floor")
	})
	t.Run("offline_directory_retains_exact_live_set", func(t *testing.T) {
		oldTransport := ca.Http
		ca.Http = coreOfflineHTTP{HttpClient: oldTransport}
		defer func() { ca.Http = oldTransport }()
		records, total, err := ca.ListRecords(prefix, 0, 0)
		coreRequire(t, "offline local directory read", err)
		coreAssert(t, len(records) == 2 && total == 2, "offline replica did not retain exact current key set")
		for _, record := range records {
			coreAssert(t, !dkvsindexer.IsTombstone(record.Flags) && record.Key != keys[1], "deleted row leaked into offline replica")
		}
	})
	_, err = cb.PutSignedRecordWithAutopay(b.GetWallet(), keys[1], []byte("recreated-after-delete"), dkvsindexer.RecordOptions{}, policy)
	coreRequire(t, "recreate absent key without historical floor", err)
	coreRequire(t, "A sync recreated record", storeA.SyncCurrent(keys...))
	restored, err := ca.GetRecord(keys[1])
	coreRequire(t, "local recreated key", err)
	coreAssert(t, string(restored.Value) == "recreated-after-delete" && restored.Seq == 1, "recreation reused a hidden deletion floor")

	fresh := coreNewE2EManager(t, cfg, chain, coreE2ESenderMnemonic)
	cc, err := fresh.ensureDKVSManager().primaryClient()
	coreRequire(t, "fresh replica client", err)
	_, _, err = cc.SubscribePrefix(prefix)
	coreRequire(t, "fresh replica prefix recovery", err)
	for _, key := range keys {
		want, err := ca.GetRecord(key)
		coreRequire(t, "read expected local record", err)
		got, err := cc.GetRecord(key)
		coreRequire(t, "read independently rebuilt record", err)
		coreAssert(t, dkvsindexer.RecordHash(want) == dkvsindexer.RecordHash(got), "fresh replica differs in value, sequence or signature")
	}

	direct := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil).WithWriteSigner(root)
	current, err := direct.GetRecordDirect(keys[0])
	coreRequire(t, "read server CAS baseline", err)
	height, err := direct.GetBestHeight()
	coreRequire(t, "current height for CAS conflict requests", err)
	t.Run("stale_CAS_does_not_mutate_record", func(t *testing.T) {
		staleHash := dkvsindexer.RecordHash(old)
		stale, err := newSignedRecordWithAutopay(root, keys[0], []byte("stale-overwrite"), dkvsindexer.RecordOptions{Seq: current.Seq + 1, IssueHeight: height}, policy)
		coreRequire(t, "build deliberate stale CAS", err)
		_, err = direct.PutRecordCAS(stale, dkvsindexer.WritePrecondition{ExpectedHash: &staleHash})
		coreAssert(t, errors.Is(err, dkvsindexer.ErrWriteConflict), "stale CAS must reach and fail the server CAS check")
		got, err := direct.GetRecordDirect(keys[0])
		coreRequire(t, "verify server after stale CAS", err)
		coreAssert(t, dkvsindexer.RecordHash(got) == dkvsindexer.RecordHash(current), "failed CAS changed remote record")
	})
	t.Run("atomic_batch_conflict_commits_neither_key", func(t *testing.T) {
		other, err := direct.GetRecordDirect(keys[2])
		coreRequire(t, "read second batch baseline", err)
		first, err := newSignedRecordWithAutopay(root, keys[0], []byte("batch-first"), dkvsindexer.RecordOptions{Seq: current.Seq + 1, IssueHeight: height}, policy)
		coreRequire(t, "sign batch first record", err)
		second, err := newSignedRecordWithAutopay(root, keys[2], []byte("batch-second"), dkvsindexer.RecordOptions{Seq: other.Seq + 1, IssueHeight: height}, policy)
		coreRequire(t, "sign batch second record", err)
		hash := dkvsindexer.RecordHash(current)
		_, err = direct.PutRecordBatchCAS([]dkvsindexer.CASMutation{
			{Record: first, Precondition: dkvsindexer.WritePrecondition{ExpectedHash: &hash}},
			{Record: second, Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true}},
		})
		coreAssert(t, errors.Is(err, dkvsindexer.ErrWriteConflict), "atomic batch must reach and fail the server CAS check")
		for _, prior := range []struct {
			key   string
			value []byte
		}{{keys[0], current.Value}, {keys[2], other.Value}} {
			got, err := direct.GetRecordDirect(prior.key)
			coreRequire(t, "read batch rollback result", err)
			coreAssert(t, bytes.Equal(got.Value, prior.value), "partial atomic batch changed a record")
		}
	})
	t.Run("wrong_root_cannot_write_personal_key", func(t *testing.T) {
		intruder := coreNewE2EManager(t, cfg, chain, coreE2EReceiverMnemonic)
		forged, err := newSignedRecordWithAutopay(intruder.GetWallet(), keys[0], []byte("unauthorized"), dkvsindexer.RecordOptions{Seq: current.Seq + 1}, policy)
		if err == nil {
			hash := dkvsindexer.RecordHash(current)
			_, err = direct.PutRecordCAS(forged, dkvsindexer.WritePrecondition{ExpectedHash: &hash})
		}
		coreAssert(t, err != nil, "wrong root was allowed to update a personal key")
		got, err := direct.GetRecordDirect(keys[0])
		coreRequire(t, "read record after rejected foreign signer", err)
		coreAssert(t, dkvsindexer.RecordHash(got) == dkvsindexer.RecordHash(current), "foreign signer rejection changed record")
	})
	t.Run("paid_record_replicates_to_another_node", func(t *testing.T) {
		peer := NewSatsNetDKVSClient(cfg.Bootstrap.Scheme, cfg.Bootstrap.Host, cfg.Bootstrap.Proxy, nil)
		deadline := time.Now().Add(15 * time.Second)
		for {
			got, err := peer.GetRecordDirect(keys[0])
			if err == nil && dkvsindexer.RecordHash(got) == dkvsindexer.RecordHash(current) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("core-e2e: paid record not replicated: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
}
