package dkvs

import (
	"errors"

	indexer "github.com/sat20-labs/indexer/common"
)

var ErrReplicaNotReady = errors.New("DKVS subscription has not completed initial synchronization")

// ReplicaStore owns the final Wallet materialized subscription replica and
// RequestID-keyed durable outbox. There is no path-root/generation replica.
type ReplicaStore struct {
	db indexer.KVDB
}

type batchWriter interface {
	Put(key, value []byte) error
	Delete(key []byte) error
}

type OutboxOrigin struct {
	Key                       string
	Domain                    string
	Generation                uint64
	PreservePrefixGenerations bool
}

func NewReplicaStore(db indexer.KVDB) *ReplicaStore {
	return &ReplicaStore{db: db}
}
