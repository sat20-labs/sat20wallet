package wallet

import (
	"errors"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
)

var ErrAtomicPersistenceUnavailable = errors.New("database does not support atomic write transactions")

// NewAtomicWriteBatch requires a single transaction. Generic bulk writers may
// flush during Put and therefore cannot protect paired chain journals or a
// contract ledger and its reservation from partial persistence.
func NewAtomicWriteBatch(database indexer.KVDB) (indexer.WriteBatch, error) {
	atomic, ok := database.(interface{ NewAtomicWriteBatch() indexer.WriteBatch })
	if !ok {
		return nil, fmt.Errorf("%w: %T", ErrAtomicPersistenceUnavailable, database)
	}
	batch := atomic.NewAtomicWriteBatch()
	if batch == nil {
		return nil, ErrAtomicPersistenceUnavailable
	}
	return batch, nil
}
