package dkvs

import (
	"errors"

	indexer "github.com/sat20-labs/indexer/common"
)

var ErrReplicaNotReady = errors.New("DKVS subscription has not completed initial synchronization")

// ReplicaStore owns confirmed current records, source-local ActiveMeta and
// the RequestID-keyed durable outbox. It stores no deletion history.
type ReplicaStore struct {
	db indexer.KVDB
}

type batchWriter interface {
	Put(key, value []byte) error
	Delete(key []byte) error
}

type OutboxOrigin struct {
	Key        string
	Domain     string
	Generation uint64
}

func NewReplicaStore(db indexer.KVDB) *ReplicaStore {
	return &ReplicaStore{db: db}
}
