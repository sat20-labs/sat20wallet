package wallet

import (
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func testDKVSReplicaRecord(t *testing.T, seq uint64, value string) *swire.DKVSRecord {
	t.Helper()
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := dkvsindexer.PersonalKey(priv.PubKey().SerializeCompressed(), "rgb11/test/head")
	if err != nil {
		t.Fatal(err)
	}
	record, err := dkvsindexer.NewAccountRecord(key, []byte(value), dkvsindexer.RecordOptions{
		Seq: seq, TTL: uint64(time.Hour / time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	hash := dkvsindexer.SigningHash(record)
	signature, err := schnorr.Sign(priv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	record.Signature = signature.Serialize()
	return record
}

func TestDKVSReplicaMirrorDoesNotDeleteOutbox(t *testing.T) {
	store := newDKVSReplicaStore(newMemoryKVDB())
	record := testDKVSReplicaRecord(t, 1, "one")
	filter := dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionKey, Target: record.Key}
	scope := dkvsReplicaScope("production:testnet:node", []dkvsindexer.Subscription{filter})
	if err := store.queueOutbox(scope, record); err != nil {
		t.Fatal(err)
	}
	root := chainhash.DoubleHashH([]byte("root"))
	if err := store.applyConfirmed(scope, []dkvsindexer.Subscription{filter}, nil, root.String()); err != nil {
		t.Fatal(err)
	}
	outbox, err := store.loadOutbox(scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 1 || dkvsindexer.RecordHash(outbox[0]) != dkvsindexer.RecordHash(record) {
		t.Fatalf("outbox changed by mirror: %#v", outbox)
	}
}

func TestDKVSReplicaAtomicallyReplacesConfirmed(t *testing.T) {
	store := newDKVSReplicaStore(newMemoryKVDB())
	first := testDKVSReplicaRecord(t, 1, "one")
	filter := dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionKey, Target: first.Key}
	scope := dkvsReplicaScope("production:testnet:node", []dkvsindexer.Subscription{filter})
	root1 := chainhash.DoubleHashH([]byte("root-1"))
	if err := store.applyConfirmed(scope, []dkvsindexer.Subscription{filter}, []*swire.DKVSRecord{first}, root1.String()); err != nil {
		t.Fatal(err)
	}
	root2 := chainhash.DoubleHashH([]byte("root-2"))
	if err := store.applyConfirmed(scope, []dkvsindexer.Subscription{filter}, nil, root2.String()); err != nil {
		t.Fatal(err)
	}
	records, err := store.loadConfirmed(scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("confirmed records were not replaced: %#v", records)
	}
	gotRoot, err := store.loadRoot(scope)
	if err != nil {
		t.Fatal(err)
	}
	if gotRoot != root2.String() {
		t.Fatalf("root=%s want=%s", gotRoot, root2.String())
	}
}
