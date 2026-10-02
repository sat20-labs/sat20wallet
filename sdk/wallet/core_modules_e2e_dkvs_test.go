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
	// Own-key PUTs do not establish an entire directory baseline.
	_, _, err = ca.SubscribePrefix(prefix)
	coreRequire(t, "replica A initial prefix snapshot", err)
	_, _, err = cb.SubscribePrefix(prefix)
	coreRequire(t, "replica B initial prefix snapshot", err)
	records, total, err := cb.ListRecords(prefix, 0, 0)
	coreRequire(t, "read local directory", err)
	coreAssert(t, total == 3 && len(records) == 3, "prefix boundary leaked sibling or omitted nested key")
	old, err := cb.GetRecord(keys[0])
	coreRequire(t, "capture pre-update record", err)
	_, err = cb.PutSignedRecordWithAutopay(b.GetWallet(), keys[0], []byte("updated-by-B"), dkvsindexer.RecordOptions{}, policy)
	coreRequire(t, "same root writes on instance B", err)
	storeA, err := a.accountDKVSStore()
	coreRequire(t, "A replica store", err)
	coreRequire(t, "A sync remote generation delta", storeA.SyncCurrent(keys...))
	actual, err := ca.GetRecord(keys[0])
	coreRequire(t, "A reads B update locally", err)
	coreAssert(t, string(actual.Value) == "updated-by-B" && actual.Seq > old.Seq, "cross-instance update did not advance actual local state")

	deleted, err := cb.TombstoneSignedWithAutopay(b.GetWallet(), keys[1], dkvsindexer.RecordOptions{}, policy)
	coreRequire(t, "B deletes nested record", err)
	coreRequire(t, "A sync tombstone", storeA.SyncCurrent(keys...))
	t.Run("delete_propagates_without_stale_live_value", func(t *testing.T) {
		got, err := ca.GetRecord(keys[1])
		if errors.Is(err, ErrDKVSRecordNotFound) { return }
		// Raw record APIs may expose the signed tombstone for sequence/CAS
		// tracking. Accept only the exact acknowledged tombstone, never the old
		// live value or an unrelated transport failure as evidence of deletion.
		if err == nil && got != nil && dkvsindexer.IsTombstone(got.Flags) &&
			dkvsindexer.RecordHash(got) == dkvsindexer.RecordHash(deleted) { return }
		t.Fatalf("core-e2e: local delete did not converge: record_present=%v err=%v", got != nil, err)
	})
	t.Run("offline_directory_retains_exact_live_set", func(t *testing.T) {
		oldTransport := ca.Http
		ca.Http = coreOfflineHTTP{HttpClient: oldTransport}
		defer func() { ca.Http = oldTransport }()
		records, total, err := ca.ListRecords(prefix, 0, 0)
		coreRequire(t, "offline local directory read", err)
		live := 0
		for _, record := range records { if !dkvsindexer.IsTombstone(record.Flags) { live++ } }
		coreAssert(t, live == 2 && total == len(records), "offline replica did not retain exact live key set")
	})
	_, err = cb.PutSignedRecordWithAutopay(b.GetWallet(), keys[1], []byte("recreated-after-delete"), dkvsindexer.RecordOptions{}, policy)
	coreRequire(t, "recreate tombstoned key with next sequence", err)
	coreRequire(t, "A sync recreated record", storeA.SyncCurrent(keys...))
	restored, err := ca.GetRecord(keys[1])
	coreRequire(t, "local recreated key", err)
	coreAssert(t, string(restored.Value) == "recreated-after-delete" && restored.Seq >= 3, "tombstone sequence was reused")

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

	direct := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	current, err := direct.GetRecordDirect(keys[0])
	coreRequire(t, "read server CAS baseline", err)
	t.Run("stale_CAS_does_not_mutate_record", func(t *testing.T) {
		staleHash := dkvsindexer.RecordHash(old)
		stale, err := newSignedRecordWithAutopay(root, keys[0], []byte("stale-overwrite"), dkvsindexer.RecordOptions{Seq: current.Seq + 1}, policy)
		coreRequire(t, "build deliberate stale CAS", err)
		_, err = direct.PutRecordCAS(stale, dkvsindexer.WritePrecondition{ExpectedHash: &staleHash})
		coreAssert(t, err != nil, "stale CAS unexpectedly overwrote remote state")
		got, err := direct.GetRecordDirect(keys[0])
		coreRequire(t, "verify server after stale CAS", err)
		coreAssert(t, dkvsindexer.RecordHash(got) == dkvsindexer.RecordHash(current), "failed CAS changed remote record")
	})
	t.Run("atomic_batch_conflict_commits_neither_key", func(t *testing.T) {
		other, err := direct.GetRecordDirect(keys[2])
		coreRequire(t, "read second batch baseline", err)
		first, err := newSignedRecordWithAutopay(root, keys[0], []byte("batch-first"), dkvsindexer.RecordOptions{Seq: current.Seq + 1}, policy)
		coreRequire(t, "sign batch first record", err)
		second, err := newSignedRecordWithAutopay(root, keys[2], []byte("batch-second"), dkvsindexer.RecordOptions{Seq: other.Seq + 1}, policy)
		coreRequire(t, "sign batch second record", err)
		hash := dkvsindexer.RecordHash(current)
		_, err = direct.PutRecordBatchCAS([]dkvsindexer.CASMutation{
			{Record: first, Precondition: dkvsindexer.WritePrecondition{ExpectedHash: &hash}},
			{Record: second, Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true}},
		})
		coreAssert(t, err != nil, "conflicting atomic batch accepted")
		for _, prior := range []struct{ key string; value []byte }{{keys[0], current.Value}, {keys[2], other.Value}} {
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
		coreAssert(t, dkvsindexer.RecordHash(got) == dkvsindexer.RecordHash(current), "foreign signer rejection changed the record")
	})
	t.Run("paid_record_replicates_to_another_node", func(t *testing.T) {
		peer := NewSatsNetDKVSClient(cfg.Bootstrap.Scheme, cfg.Bootstrap.Host, cfg.Bootstrap.Proxy, nil)
		deadline := time.Now().Add(15 * time.Second)
		for {
			got, err := peer.GetRecordDirect(keys[0])
			if err == nil && dkvsindexer.RecordHash(got) == dkvsindexer.RecordHash(current) { break }
			if time.Now().After(deadline) {
				t.Fatalf("core-e2e: paid record was not replicated to the second node: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
}
