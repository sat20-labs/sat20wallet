package wallet

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Reuse the PR's real SDK/HTTP/DKVS helpers. No live wallet, external network,
// STP operation or private build tag is used. Shared FT/NFT type compatibility
// is covered separately by TestRGB11RegistrySDKDKVSE2E.
func TestRGB11RegistryReviewE2EFullSnapshotAndReopen(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	var authority []byte
	for ordinal := uint64(1); ordinal <= 12; ordinal++ {
		record := makeRecord("alice", "USD", "f", ordinal, fmt.Sprintf("%064x", 700+ordinal))
		if updated, err := source.indexer.PutInternalRGB11Registry(record); err != nil || !updated {
			t.Fatalf("seed ordinal %d: updated=%v err=%v", ordinal, updated, err)
		}
		authority = append([]byte(nil), record.PubKey...)
	}
	snapshot, err := source.indexer.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	db := newMemoryKVDB()
	cfg := dkvsindexer.Config{
		EndpointID: "rgb11-review-recovery", CurrentHeight: func() uint64 { return 100 },
		SystemVerifier: dkvsindexer.StaticSystemVerifier{Keys: [][]byte{authority}},
	}
	target, _ := newRGB11RegistryE2ESource(t)
	target.indexer = dkvsindexer.New(db, cfg)
	if applied, err := target.indexer.ApplySnapshot(snapshot); err != nil || applied != 12 {
		t.Fatalf("full snapshot: applied=%d err=%v", applied, err)
	}
	if applied, err := target.indexer.ApplySnapshot(snapshot); err != nil || applied != 0 {
		t.Fatalf("full snapshot retry must be idempotent: applied=%d err=%v", applied, err)
	}
	assertRead := func(t *testing.T) {
		t.Helper()
		client, reads := newRGB11RegistryE2EHTTPClient(t, target, nil)
		for _, ordinal := range []uint64{1, 2, 10, 12} {
			id := fmt.Sprintf("%064x", 700+ordinal)
			suffix := ""
			if ordinal > 1 {
				suffix = fmt.Sprintf("_%d", ordinal)
			}
			want := "rgb11:f:usd" + suffix + "@alice"
			got, err := client.GetRGB11Registration("alice", "USD", id)
			if err != nil || got == nil || got.ContractID != id || got.AssetName != want || got.Ordinal != ordinal {
				t.Fatalf("recovered registration: got=%+v err=%v want=%s", got, err, want)
			}
			byName, err := target.indexer.LookupRGB11AssetName(want)
			if err != nil || byName == nil || byName.ContractID != id {
				t.Fatalf("recovered reverse mapping: got=%+v err=%v", byName, err)
			}
		}
		if reads.Load() != 4 {
			t.Fatalf("expected four real HTTP reads, got %d", reads.Load())
		}
	}
	t.Run("after_full_snapshot", assertRead)
	// Recreate only the Indexer over the same memory KVDB. This is not a
	// disk/crash-recovery test and requires neither L1 resolver nor replay.
	target.indexer = dkvsindexer.New(db, cfg)
	t.Run("after_indexer_reconstruction", assertRead)
}

func TestRGB11RegistryReviewE2ERejectMutableRegistryShape(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	id := fmt.Sprintf("%064x", 801)
	original := makeRecord("alice", "USD", "f", 1, id)
	if updated, err := source.indexer.PutInternalRGB11Registry(original); err != nil || !updated {
		t.Fatalf("seed registration: updated=%v err=%v", updated, err)
	}
	core := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params)
	if core == nil || core.GetPubKey() == nil {
		t.Fatal("create public CoreNode test wallet")
	}
	originalHash := dkvsindexer.RecordHash(original)
	for _, tc := range []struct {
		name string
		opts dkvsindexer.RecordOptions
	}{
		{"nonpermanent_TTL", dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 100, TTL: 10}},
		{"mutable_sequence", dkvsindexer.RecordOptions{Seq: 2, IssueHeight: 100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Re-sign with the authorized key: signature verification alone is
			// not sufficient to catch these illegal registry-state shapes.
			malformed, err := NewDKVSSignedRecord(core, original.Key, original.Value, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if err := dkvsindexer.VerifySignature(malformed); err != nil {
				t.Fatalf("fixture signature must be valid: %v", err)
			}
			var attack atomic.Bool
			var injected atomic.Int64
			client, reads := newRGB11RegistryE2EHTTPClient(t, source, func(result *dkvsindexer.PrefixReadResult) {
				if attack.Load() {
					result.Records = []*swire.DKVSRecord{malformed}
					injected.Add(1)
				}
			})
			control, err := client.GetRGB11Registration("alice", "USD", id)
			if err != nil || control == nil || control.AssetName != "rgb11:f:usd@alice" {
				t.Fatalf("same-client positive control failed: got=%+v err=%v", control, err)
			}
			attack.Store(true)
			got, err := client.GetRGB11Registration("alice", "USD", id)
			if reads.Load() != 2 || injected.Load() != 1 {
				t.Fatalf("malformed response not exercised: reads=%d injected=%d", reads.Load(), injected.Load())
			}
			if err == nil || got != nil {
				t.Errorf("illegal registry shape accepted: got=%+v err=%v", got, err)
			}
			stored, getErr := source.indexer.Get(original.Key)
			if getErr != nil || stored == nil || dkvsindexer.RecordHash(stored) != originalHash {
				t.Fatalf("response injection modified real DB: got=%+v err=%v", stored, getErr)
			}
		})
	}
}
