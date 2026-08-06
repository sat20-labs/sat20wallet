package wallet

import (
	indexer "github.com/sat20-labs/indexer/common"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// dkvsReplicaStore is the manager-facing adapter. The persistent replica codec
// and confirmed-state storage live in wallet/dkvs; manager-only path/outbox
// state can continue to use the shared KVDB while the remaining layers migrate.
type dkvsReplicaStore struct {
	db   indexer.KVDB
	core *dkvscore.ReplicaStore
}

type dkvsReplicaBaseline = dkvscore.ReplicaBaseline

const dkvsReplicaVersion = dkvscore.ReplicaVersion

var (
	dkvsReplicaConfirmedPrefix = dkvscore.ReplicaConfirmedPrefix
	dkvsReplicaRootPrefix      = dkvscore.ReplicaRootPrefix
)

func dkvsReplicaRecordKey(base []byte, scope, recordKey string) []byte {
	return dkvscore.ReplicaRecordKey(base, scope, recordKey)
}

func newDKVSReplicaStore(db indexer.KVDB) *dkvsReplicaStore {
	return &dkvsReplicaStore{db: db, core: dkvscore.NewReplicaStore(db)}
}

func dkvsReplicaScope(namespace string, filters []dkvsindexer.Subscription) string {
	return dkvscore.ReplicaScope(namespace, filters)
}

func (s *dkvsReplicaStore) loadConfirmed(scope string) ([]*swire.DKVSRecord, error) {
	return s.core.LoadConfirmed(scope)
}

func (s *dkvsReplicaStore) loadBaseline(scope string) (*dkvsReplicaBaseline, error) {
	return s.core.LoadBaseline(scope)
}

func (s *dkvsReplicaStore) loadRoot(scope string) (string, error) {
	return s.core.LoadRoot(scope)
}

func (s *dkvsReplicaStore) applyConfirmed(scope string, filters []dkvsindexer.Subscription,
	records []*swire.DKVSRecord, directoryRootText string, activeRoot chainhash.Hash,
	generation uint64) error {

	return s.core.ApplyConfirmed(scope, filters, records, directoryRootText, activeRoot, generation)
}
